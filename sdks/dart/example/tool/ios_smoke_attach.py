#!/usr/bin/env python3
"""Run the iOS smoke app through a bounded native launch and VM attachment."""

import json
import os
from pathlib import Path
import re
import selectors
import signal
import subprocess
import sys
import time
from urllib.parse import urlsplit

from ios_smoke_log import MAX_FILE_BYTES, MAX_LINE_BYTES, Redactor, text_tail


BUNDLE_ID = "com.anaregdesign.lanternExample"
TARGET = "integration_test/mobile_smoke_test.dart"
DRIVER = "test_driver/integration_test.dart"
VM_URL = re.compile(
    r"The Dart VM service is listening on (https?://[A-Za-z0-9:/=_\-.\[\]]+)"
)
PID = re.compile(rf"^{re.escape(BUNDLE_ID)}: ([1-9][0-9]*)$", re.MULTILINE)


def positive_env(name, default):
    raw = os.environ.get(name, str(default))
    if not raw.isdecimal() or int(raw) < 1:
        raise ValueError(f"invalid {name}: {raw}")
    return int(raw)


def stop_process(process):
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    try:
        process.wait(timeout=2)
    except subprocess.TimeoutExpired:
        pass
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    process.wait()


def wait_bounded(process, deadline):
    try:
        return process.wait(timeout=max(0, deadline - time.monotonic())), False
    except subprocess.TimeoutExpired:
        stop_process(process)
        return process.wait(), True


def read_process(command, timeout, limit, *, merge_stderr=False):
    """Capture transient command output within a byte and wall-time bound."""
    process = subprocess.Popen(
        command,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT if merge_stderr else subprocess.PIPE,
        start_new_session=True,
    )
    streams = [process.stdout] if merge_stderr else [process.stdout, process.stderr]
    selector = selectors.DefaultSelector()
    for stream in streams:
        selector.register(stream, selectors.EVENT_READ)
    output = bytearray()
    deadline = time.monotonic() + timeout
    timed_out = False
    exceeded = False
    try:
        while selector.get_map():
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                timed_out = True
                break
            for key, _ in selector.select(min(remaining, 0.25)):
                chunk = os.read(key.fileobj.fileno(), 65536)
                if not chunk:
                    selector.unregister(key.fileobj)
                    continue
                if merge_stderr or key.fileobj is process.stdout:
                    if len(output) + len(chunk) > limit:
                        exceeded = True
                        break
                    output.extend(chunk)
            if exceeded:
                break
        if timed_out or exceeded:
            stop_process(process)
        status, wait_timed_out = wait_bounded(process, deadline)
        return status, bytes(output), timed_out or wait_timed_out, exceeded
    finally:
        selector.close()
        for stream in streams:
            stream.close()


def run_logged(command, path, timeout, console_budget, *, collect_pid=False):
    """Stream redacted output without retaining an unbounded raw log."""
    process = subprocess.Popen(
        command,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        start_new_session=True,
    )
    selector = selectors.DefaultSelector()
    selector.register(process.stdout, selectors.EVENT_READ)
    redactor = Redactor()
    retained = path.read_bytes() if path.exists() else b""
    pid_output = bytearray()
    deadline = time.monotonic() + timeout
    timed_out = False
    try:
        while selector.get_map():
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                timed_out = True
                stop_process(process)
                break
            for key, _ in selector.select(min(remaining, 0.25)):
                chunk = os.read(key.fileobj.fileno(), 65536)
                if not chunk:
                    selector.unregister(key.fileobj)
                elif collect_pid:
                    pid_output.extend(chunk[: max(0, 4096 - len(pid_output))])
                safe = redactor.feed(chunk, final=not chunk)
                if safe:
                    retained = text_tail(retained + safe, MAX_FILE_BYTES)
                    path.write_bytes(retained)
                    visible = safe[: max(0, console_budget[0])]
                    if visible:
                        sys.stdout.buffer.write(visible)
                        sys.stdout.buffer.flush()
                        console_budget[0] -= len(visible)
        status, wait_timed_out = wait_bounded(process, deadline)
        return status, timed_out or wait_timed_out, bytes(pid_output)
    finally:
        selector.close()
        process.stdout.close()


def record_phase(phases, name):
    (phases / name).write_text("seen\n")


def record_classification(destination, classification):
    (destination / "classification.txt").write_text(classification + "\n")
    output = os.environ.get("GITHUB_OUTPUT")
    if output:
        with open(output, "a", encoding="utf-8") as stream:
            stream.write(f"classification={classification}\n")
    print(f"[ios smoke] classification={classification}", flush=True)


def valid_vm_url(message):
    match = VM_URL.search(message)
    if match is None:
        return None
    url = match.group(1)
    parsed = urlsplit(url)
    if parsed.scheme != "http" or parsed.hostname not in {"127.0.0.1", "localhost", "::1"}:
        return None
    return url


def process_alive(pid):
    """A Simulator app PID is a host PID; distinguish its exit from a log stall."""
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        pass
    status, output, timed_out, exceeded = read_process(
        [os.environ.get("IOS_SMOKE_PS_BIN", "ps"), "-p", str(pid), "-o", "stat="],
        3, 1024, merge_stderr=True,
    )
    if timed_out or exceeded:
        return True  # Unknown liveness must not be treated as an app crash.
    return status == 0 and not output.strip().startswith(b"Z")


def read_app_log(xcrun, device, pid, destination, phases, timeout):
    predicate = f'processID == {pid} AND eventMessage CONTAINS "flutter:"'
    command = [
        xcrun, "simctl", "spawn", device, "log", "show", "--last", "5m",
        "--style", "json", "--predicate", predicate,
    ]
    status, raw, timed_out, exceeded = read_process(command, timeout, 2 * 1024 * 1024)
    if timed_out or exceeded or status:
        return None, None
    try:
        events = json.loads(raw)
    except (UnicodeDecodeError, json.JSONDecodeError):
        return None, None
    if not isinstance(events, list):
        return None, None
    vm_url = None
    result = None
    lines = []
    for event in events:
        if not isinstance(event, dict):
            continue
        message = event.get("eventMessage")
        if not isinstance(message, str) or "flutter:" not in message:
            continue
        if len(message.encode("utf-8")) > MAX_LINE_BYTES:
            lines.append(b"[ios smoke: oversized app log line omitted]\n")
            continue
        if vm_url is None:
            vm_url = valid_vm_url(message)
        if "MOBILE_SMOKE_BODY_STARTED" in message:
            record_phase(phases, "body_started")
        if "MOBILE_SMOKE_PASS " in message:
            record_phase(phases, "smoke_pass")
        if "All tests passed!" in message:
            record_phase(phases, "tests_passed")
            result = "passed"
        if "Some tests failed." in message:
            record_phase(phases, "tests_failed")
            result = "failed"
        lines.append(Redactor.redact((message + "\n").encode("utf-8")))
    destination.write_bytes(text_tail(b"".join(lines), MAX_FILE_BYTES))
    return vm_url, result


def remaining(deadline, maximum):
    return max(1, min(maximum, int(deadline - time.monotonic())))


def capture_diagnostics(script, device, destination):
    process = subprocess.Popen(
        ["bash", str(script), "capture-diagnostics", device, str(destination)],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )
    try:
        process.wait(timeout=65)
    except subprocess.TimeoutExpired:
        stop_process(process)
        print("[ios smoke] diagnostic capture timed out", flush=True)


def print_driver_summary(path):
    retained = path.read_bytes()
    marker = retained.find(b"VMServiceFlutterDriver:")
    relevant = retained[marker:] if marker >= 0 else text_tail(retained, 8192)
    relevant = text_tail(relevant, 65536)
    path.write_bytes(relevant)
    sys.stdout.buffer.write(text_tail(relevant, 8192))
    sys.stdout.buffer.flush()


def run_attempt(device, attempt, root):
    if not re.fullmatch(r"[a-z0-9_-]+", attempt):
        raise ValueError(f"invalid attempt label: {attempt}")
    if not re.fullmatch(r"[A-Za-z0-9-]+", device):
        raise ValueError("invalid device id")
    destination = root / attempt
    phases = destination / "phases"
    phases.mkdir(parents=True, exist_ok=True)
    for phase in (
        "build_started", "build_done", "body_started", "smoke_pass",
        "tests_passed", "tests_failed",
    ):
        (phases / phase).unlink(missing_ok=True)
    flutter_log = destination / "flutter.log"
    flutter_log.write_bytes(b"")
    console_budget = [MAX_FILE_BYTES]
    flutter = os.environ.get("IOS_SMOKE_FLUTTER_BIN", "flutter")
    xcrun = os.environ.get("IOS_SMOKE_XCRUN_BIN", "xcrun")
    endpoint = os.environ.get("IOS_SMOKE_ENDPOINT", "http://127.0.0.1:6380")
    total = positive_env("IOS_SMOKE_TOTAL_TIMEOUT_SECONDS", 480)
    launch_limit = positive_env("IOS_SMOKE_LAUNCH_TIMEOUT_SECONDS", 180)
    build_limit = positive_env("IOS_SMOKE_BUILD_TIMEOUT_SECONDS", 180)
    attach_limit = positive_env("IOS_SMOKE_ATTACH_TIMEOUT_SECONDS", 120)
    deadline = time.monotonic() + total
    classification = "pre_body_failure"
    try:
        record_phase(phases, "build_started")
        status, timed_out, _ = run_logged(
            [flutter, "build", "ios", "--simulator", "--debug", "--no-codesign", "--no-pub",
             f"--target={TARGET}", f"--dart-define=LANTERN_ENDPOINT={endpoint}",
             "--dart-define=LANTERN_ALLOW_INSECURE=true",
             "--dart-define=LANTERN_IOS_DIRECT_LAUNCH=true"],
            flutter_log, remaining(deadline, build_limit), console_budget,
        )
        if timed_out:
            classification = "build_stall"
            return 71
        if status:
            classification = "build_failure"
            return 72
        record_phase(phases, "build_done")

        # Clear any preceding app process so the returned PID identifies this run.
        read_process([xcrun, "simctl", "terminate", device, BUNDLE_ID], 15, 4096, merge_stderr=True)
        status, _, timed_out, exceeded = read_process(
            [xcrun, "simctl", "install", device, "build/ios/iphonesimulator/Runner.app"],
            remaining(deadline, 90), 65536, merge_stderr=True,
        )
        if timed_out:
            classification = "launch_stall"
            return 70
        if exceeded or status:
            classification = "launch_failure"
            return 72
        status, timed_out, output = run_logged(
            [xcrun, "simctl", "launch", device, BUNDLE_ID,
             "--enable-dart-profiling", "--disable-vm-service-publication",
             "--enable-checked-mode", "--verify-entry-points"],
            flutter_log, remaining(deadline, launch_limit), console_budget, collect_pid=True,
        )
        if timed_out:
            classification = "launch_stall"
            return 70
        match = PID.search(output.decode("utf-8", errors="replace"))
        if status or match is None:
            classification = "launch_failure"
            return 72
        pid = int(match.group(1))
        launch_started = time.monotonic()
        vm_url = None
        terminal = None
        while time.monotonic() < deadline:
            found_url, found_terminal = read_app_log(
                xcrun, device, pid, destination / "app.log", phases,
                remaining(deadline, 12),
            )
            vm_url = vm_url or found_url
            terminal = found_terminal or terminal
            if terminal == "failed":
                classification = "test_failure"
                break
            if terminal == "passed" and vm_url:
                break
            if not process_alive(pid):
                # Unified logging can flush the final app messages just after
                # the process exits. Give it one last bounded read first.
                final_url, final_terminal = read_app_log(
                    xcrun, device, pid, destination / "app.log", phases,
                    remaining(deadline, 12),
                )
                vm_url = vm_url or final_url
                terminal = final_terminal or terminal
                classification = (
                    "test_failure" if terminal or (phases / "body_started").exists()
                    else "launch_failure"
                )
                return 72
            if (
                not (phases / "body_started").exists()
                and time.monotonic() - launch_started >= launch_limit
            ):
                classification = "launch_stall"
                return 70
            time.sleep(1)
        if terminal != "passed" and terminal != "failed":
            classification = (
                "test_body_stall" if (phases / "body_started").exists()
                else "launch_stall"
            )
            return 71
        if not vm_url:
            if terminal == "passed":
                classification = "attach_stall"
            return 72
        driver_log = destination / "driver.log"
        driver_log.write_bytes(b"")
        status, timed_out, _ = run_logged(
            [flutter, "drive", "--no-pub", f"--use-existing-app={vm_url}",
             f"--driver={DRIVER}", f"--target={TARGET}", "-d", device],
            driver_log, remaining(deadline, attach_limit), [0],
        )
        print_driver_summary(driver_log)
        if timed_out:
            classification = "attach_stall" if terminal == "passed" else "test_failure"
            return 71
        if terminal == "passed" and status == 0 and (phases / "smoke_pass").exists():
            classification = "success"
            return 0
        classification = "test_failure"
        return 72
    finally:
        if classification != "success":
            capture_diagnostics(
                Path(__file__).with_name("ios_smoke_ci.sh"),
                device, destination / "diagnostics",
            )
        record_classification(destination, classification)


def main():
    if len(sys.argv) != 4:
        raise SystemExit("usage: ios_smoke_attach.py <device> <label> <diagnostics-root>")
    raise SystemExit(run_attempt(sys.argv[1], sys.argv[2], Path(sys.argv[3])))


if __name__ == "__main__":
    main()
