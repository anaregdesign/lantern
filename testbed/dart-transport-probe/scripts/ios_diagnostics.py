#!/usr/bin/env python3
"""Retain content-free iOS transport evidence; never retry a failed probe."""

import argparse
import json
import os
from pathlib import Path
import re
import selectors
import signal
import subprocess
import time

PREFIX = "LANTERN_PROBE_EVENT "
MAX_LINE = 8192
MAX_FILE = 262144
SCENARIOS = {"plaintext", "wrong_host", "missing_auth", "trusted_tls", "marker"}
NEGATIVE = {"wrong_host", "missing_auth"}
METHODS = {"none", "PutVertex", "GetVertex", "BackupSnapshot", "ChannelShutdown"}
PHASES = {"preparing", "building", "installing", "launching", "observing"}
STATES = {"start", "success", "failure", "expected_failure"}
CODES = {
    "none", "ok", "cancelled", "unknown", "invalid_argument", "deadline_exceeded",
    "not_found", "already_exists", "permission_denied", "resource_exhausted",
    "failed_precondition", "aborted", "out_of_range", "unimplemented", "internal",
    "unavailable", "data_loss", "unauthenticated", "assertion", "configuration",
    "unexpected_success",
}
STACK = re.compile(r"(?:probe|probe_diagnostics|mobile_main|main)\.dart:[0-9]{1,7}:[0-9]{1,7}\Z")
TIMESTAMP = re.compile(r"[0-9T:.+Z-]{10,40}\Z")


def write_json(path, value):
    data = (json.dumps(value, sort_keys=True) + "\n").encode()
    if len(data) > MAX_FILE:
        raise ValueError("diagnostic metadata exceeded its bound")
    atomic_write(path, data)


def atomic_write(path, data):
    # Interrupted outer steps must not leave the sticky classification state
    # half-written. The temporary file contains only already-sanitized bytes.
    temporary = path.with_name(path.name + ".next")
    temporary.write_bytes(data)
    temporary.replace(path)


def load_state(root):
    path = root / "state.json"
    return json.loads(path.read_text()) if path.exists() else {"phase": "preparing"}


def bounded_append(path, value):
    line = (json.dumps(value, sort_keys=True) + "\n").encode()
    previous = path.read_bytes() if path.exists() else b""
    data = previous + line
    if len(data) > MAX_FILE:
        data = data[-MAX_FILE:]
        data = data.partition(b"\n")[2]
    atomic_write(path, data)


def safe_event(line, transport):
    """Allowlist the schema, never redact and retain arbitrary application text."""
    if PREFIX not in line:
        return None
    try:
        raw = json.loads(line.split(PREFIX, 1)[1])
    except (ValueError, TypeError):
        return None
    if not isinstance(raw, dict) or raw.get("schema") != 1 or raw.get("transport") != transport:
        return None
    for key, allowed in (("kind", {"scenario", "rpc", "result"}), ("state", STATES),
                         ("scenario", SCENARIOS), ("method", METHODS)):
        if not isinstance(raw.get(key), str) or raw[key] not in allowed:
            return None
    code = raw.get("code", "none")
    if not ((type(code) is int and 0 <= code <= 16) or (type(code) is str and code in CODES)):
        return None
    stack = raw.get("stack", [])
    if not isinstance(stack, list) or len(stack) > 8 or any(not isinstance(s, str) or not STACK.fullmatch(s) for s in stack):
        return None
    return {key: raw[key] for key in ("schema", "transport", "kind", "scenario", "method", "state")} | {"code": code, "stack": stack}


class AppEvidence:
    def __init__(self, root):
        self.root = root
        self.state = load_state(root)
        self.seen = set()
        path = root / "app.jsonl"
        if path.exists():
            self.seen = set(path.read_text().splitlines())

    def line(self, line):
        event = safe_event(line, self.state.get("transport"))
        if event is not None:
            encoded = json.dumps(event, sort_keys=True)
            if encoded not in self.seen and len(self.seen) < 256:
                bounded_append(self.root / "app.jsonl", event)
                self.seen.add(encoded)
            if event["kind"] == "rpc" and event["state"] == "failure" and event["scenario"] not in NEGATIVE:
                self.state.setdefault("rpc_failure", event)
            if event["kind"] == "result":
                if event["state"] == "failure":
                    self.state["terminal_failure"] = event
                elif event["state"] == "success":
                    self.state["terminal_success"] = True
            self.state["last_event"] = event
        elif "Unhandled Exception:" in line or "[ERROR:flutter/runtime/" in line:
            # Preserve only a flag, not an arbitrary exception message or URL.
            self.state["runtime_failure"] = True
        elif "The Dart VM service is listening" in line:
            self.state["vm_started"] = True
        else:
            return
        write_json(self.root / "state.json", self.state)


def read_lines(command, consume, timeout=10):
    """Bound time, line memory and retained output; kill only this owned reader."""
    process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True)
    selector = None
    pending = bytearray()
    discarding = False
    omitted = 0
    deadline = time.monotonic() + timeout
    timed_out = False
    try:
        selector = selectors.DefaultSelector()
        selector.register(process.stdout, selectors.EVENT_READ)
        while selector.get_map():
            if time.monotonic() >= deadline:
                timed_out = True
                break
            for key, _ in selector.select(min(0.1, max(0, deadline - time.monotonic()))):
                chunk = os.read(key.fd, 65536)
                if not chunk:
                    selector.unregister(key.fileobj)
                    if pending and not discarding:
                        consume(pending.decode("utf-8", errors="replace"))
                    break
                for part in chunk.splitlines(keepends=True):
                    ended = part.endswith((b"\n", b"\r"))
                    if not discarding:
                        if len(pending) + len(part) > MAX_LINE:
                            pending.clear()
                            discarding = True
                            omitted += 1
                        else:
                            pending.extend(part)
                    if ended:
                        if not discarding:
                            consume(pending.decode("utf-8", errors="replace"))
                        pending.clear()
                        discarding = False
    finally:
        if selector is not None:
            selector.close()
        try:
            if process.poll() is None:
                try:
                    process.wait(timeout=min(0.5, max(0, deadline - time.monotonic())))
                except subprocess.TimeoutExpired:
                    pass
            # An exited launcher can leave a descendant holding the pipe.
            # This group belongs only to the reader created above; clean it
            # independently of the leader's liveness, including consumer errors.
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.wait()
        finally:
            process.stdout.close()
    return {"exit_code": process.returncode, "timed_out": timed_out, "oversized_lines_omitted": omitted}


def observe_app(root, device, xcrun="xcrun"):
    evidence = AppEvidence(root)
    pid = evidence.state.get("pid")
    if type(pid) is not int or pid <= 0:
        return
    executable = [xcrun] if isinstance(xcrun, str) else xcrun
    command = [*executable, "simctl", "spawn", device, "log", "show", "--last", "5m", "--style", "compact", "--predicate", f"processIdentifier == {pid}"]
    try:
        result = read_lines(command, evidence.line)
    except OSError as error:
        result = {"unavailable": True, "errno": error.errno}
    write_json(root / "app-collection.json", result)


def watch(root, device):
    stopped = False

    def stop(_signum, _frame):
        nonlocal stopped
        stopped = True

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    # The app's existing observer loop owns the attempt. This reader only
    # persists diagnostics and never sends an application RPC or retries it.
    deadline = time.monotonic() + 900
    while not stopped and time.monotonic() < deadline:
        observe_app(root, device)
        if not stopped:
            time.sleep(2)


def classify(state, exit_code):
    if state.get("rpc_failure"):
        return "rpc_failure"
    if state.get("terminal_failure") or state.get("runtime_failure"):
        return "application_failure"
    if exit_code == 0 and state.get("terminal_success"):
        return "success"
    if state.get("terminal_success"):
        return "wire_observer_failure"
    if state.get("phase") in {"preparing", "building"}:
        return "build_failure"
    if state.get("phase") in {"installing", "launching"}:
        return "launch_failure"
    return "probe_incomplete"


def summarize(state, exit_code):
    result = classify(state, exit_code)
    summary = {"classification": result, "retry_eligible": False, "script_exit_code": exit_code}
    failure = state.get("rpc_failure") or state.get("terminal_failure")
    if failure:
        summary.update({key: failure[key] for key in ("scenario", "method", "code")})
    return summary


def finish(root, device, exit_code, xcrun="xcrun"):
    # On wire success, allow bounded retrospective-log flush time. This does
    # not rerun any application operation or forgive an RPC/assertion failure.
    deadline = time.monotonic() + (10 if exit_code == 0 else 0)
    while True:
        observe_app(root, device, xcrun)
        state = load_state(root)
        result = classify(state, exit_code)
        if result != "probe_incomplete" or time.monotonic() >= deadline:
            break
        time.sleep(0.25)
    summary = summarize(state, exit_code)
    write_json(root / "classification.json", summary)
    print(json.dumps(summary, sort_keys=True))
    return 0 if result == "success" else 1


def server_summary(source, destination):
    """Read a bounded regular-file tail and retain only typed, content-free fields."""
    if not source.is_file() or source.is_symlink():
        write_json(destination, {"available": False})
        return
    with source.open("rb") as handle:
        size = source.stat().st_size
        handle.seek(max(0, size - MAX_FILE))
        data = handle.read(MAX_FILE)
    if size > MAX_FILE:
        data = data.partition(b"\n")[2]
    counts = {"structured": 0, "unstructured_omitted": 0, "oversized_omitted": 0}
    destination.write_bytes(b"")
    for line in data.splitlines():
        if len(line) > MAX_LINE:
            counts["oversized_omitted"] += 1
            continue
        try:
            raw = json.loads(line)
        except (ValueError, TypeError):
            counts["unstructured_omitted"] += 1
            continue
        if not isinstance(raw, dict):
            counts["unstructured_omitted"] += 1
            continue
        level = raw.get("level")
        record = {"level": level if isinstance(level, str) and level in {"DEBUG", "INFO", "WARN", "ERROR"} else "unknown"}
        stamp = raw.get("time")
        if isinstance(stamp, str) and TIMESTAMP.fullmatch(stamp):
            record["time"] = stamp
        # Never emit message/error, paths, keys, headers, identity or token data.
        text = str(raw.get("msg", "")).lower()
        record["category"] = next((word for word in ("panic", "deadline", "tls", "auth", "listen", "shutdown", "ready") if word in text), "other")
        counts["structured"] += 1
        bounded_append(destination, record)
    bounded_append(destination, counts)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("init", "phase", "watch", "finish", "servers"))
    parser.add_argument("output", type=Path)
    parser.add_argument("--transport", choices=("connect", "grpc"))
    parser.add_argument("--phase", choices=sorted(PHASES))
    parser.add_argument("--pid", type=int)
    parser.add_argument("--device", default="")
    parser.add_argument("--exit-code", type=int, default=1)
    parser.add_argument("--plain-log", type=Path)
    parser.add_argument("--native-directory", type=Path)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    if args.output.is_symlink():
        raise ValueError("diagnostic directory must not be a symlink")
    if args.command == "init":
        if not args.transport:
            parser.error("init requires --transport")
        if any(args.output.iterdir()):
            parser.error("init requires a fresh diagnostic directory")
        write_json(args.output / "state.json", {"phase": "preparing", "transport": args.transport})
    elif args.command == "phase":
        state = load_state(args.output)
        if not args.phase:
            parser.error("phase requires --phase")
        state["phase"] = args.phase
        if args.pid is not None:
            if args.pid <= 0:
                parser.error("pid must be positive")
            state["pid"] = args.pid
        write_json(args.output / "state.json", state)
    elif args.command == "watch":
        watch(args.output, args.device)
    elif args.command == "finish":
        return finish(args.output, args.device, args.exit_code)
    else:
        if args.plain_log:
            server_summary(args.plain_log, args.output / "server-plain.jsonl")
        if args.native_directory:
            server_summary(args.native_directory / "supervisor.log", args.output / "server-supervisor.jsonl")
            server_summary(args.native_directory / "trust" / "node-0-server.log", args.output / "server-native.jsonl")
        if not (args.output / "classification.json").exists():
            # A timed-out/killed outer step may bypass its EXIT trap. Retained
            # phase/failure flags still determine classification, never retry.
            summary = summarize(load_state(args.output), 1)
            summary["outer_step_interrupted"] = True
            write_json(args.output / "classification.json", summary)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
