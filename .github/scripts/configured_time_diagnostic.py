#!/usr/bin/env python3
"""One explicitly requested hosted Darwin observation; never a configured-source-integration qualification."""

import hashlib
import json
import os
from pathlib import Path
import platform
import re
import signal
import subprocess
import sys
import tempfile
import time


ROOT = Path(__file__).resolve().parents[2]
TEST = "TestAuthorityTimeDarwinConfiguredUpstream"
FIELDS = {
    "factory": {"failed", "ready"},
    "source": {"not_checked", "mismatch", "expected"},
    "stage": {"not_run", "dns", "random", "dial", "native_send", "send", "read", "native_receive", "packet", "native_binding", "measured"},
    "reason": {"not_run", "ok", "dns", "random", "dial", "native_send", "send", "read", "read_timeout", "native_receive", "packet", "native_binding", "rate", "deny", "source_fault"},
    "install": {"not_run", "binding", "rtt", "source_bound", "transmit", "receive", "reference", "overflow", "interval", "native_install", "age", "anchor_interval", "utc_window", "width", "native_binding", "elapsed", "contradiction", "installed"},
    "current": {"not_run", "closed", "native_terminal", "no_anchor", "native_sample", "native_binding", "elapsed", "age", "anchor_interval", "overflow", "utc_window", "width", "ok"},
    "verdict": {"pass", "fail"},
}


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate diagnostic field")
        result[key] = value
    return result


def parse_result(raw):
    text = raw.decode("utf-8", errors="replace")
    runs = len(re.findall(r"(?m)^=== RUN   " + TEST + r"$", text))
    passes = len(re.findall(r"(?m)^--- PASS: " + TEST + r" \(", text))
    failures = len(re.findall(r"(?m)^--- FAIL: " + TEST + r" \(", text))
    skips = len(re.findall(r"(?m)^--- SKIP: " + TEST + r" \(", text))
    if runs != 1 or skips or passes + failures != 1:
        return {"verdict": "not_executed"}
    markers = re.findall(r"(?m)^\s+[^\n]*configured_time_diagnostic:(\{[^\n]*\})$", text)
    if len(markers) != 1:
        return {"verdict": "marker_invalid"}
    try:
        record = json.loads(markers[0], object_pairs_hook=unique_object)
        if (not isinstance(record, dict) or set(record) != set(FIELDS) | {"version", "attempt"}
                or type(record["version"]) is not int or record["version"] != 1
                or type(record["attempt"]) is not int or record["attempt"] not in (0, 1)
                or any(not isinstance(record[key], str) or record[key] not in values for key, values in FIELDS.items())):
            raise ValueError("invalid fixed observation")
    except (ValueError, TypeError):
        return {"verdict": "marker_invalid"}
    expected = dict(version=1, factory="ready", source="expected", stage="measured", reason="ok", attempt=1, install="installed", current="ok", verdict="pass")
    if passes and record != expected or failures and record["verdict"] != "fail":
        return {"verdict": "marker_invalid"}
    # Construct a fresh object; never forward arbitrary log fields or lines.
    return {"verdict": "pass" if passes else "test_failed", "observation": {key: record[key] for key in expected}}


def run_phase(command, directory, environment, name, timeout):
    log = directory / (name + ".log")
    started = time.monotonic()
    with log.open("xb") as output:
        log.chmod(0o600)
        child = subprocess.Popen(command, cwd=ROOT, env=environment, stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
        timed_out = False
        cleanup = "reaped"
        try:
            child.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            timed_out = True
        finally:
            if child.poll() is None:
                try:
                    os.killpg(child.pid, signal.SIGTERM)
                    try:
                        child.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        os.killpg(child.pid, signal.SIGKILL)
                        child.wait(timeout=5)
                except (OSError, subprocess.SubprocessError):
                    cleanup = "failed"
    return {"exit": child.returncode, "timeout": timed_out, "cleanup": cleanup, "elapsed_ms": round((time.monotonic()-started)*1000), "log_sha256": hashlib.sha256(log.read_bytes()).hexdigest()}, log


def campaign(directory, environment):
    started = time.monotonic()
    result = {"version": 1, "qualification": "singleflight_only", "raw_retention": "runner_ephemeral", "phases": {}}
    commands = [
        ("prepare", [sys.executable, "-B", str(ROOT / ".github/scripts/configured_time_input.py")], 10),
        ("compile", ["go", "test", "-c", "-tags=lantern_source_qualification", "-buildvcs=true", "-o", str(directory / "security.test"), "./server/internal/security"], 180),
        ("test", [str(directory / "security.test"), "-test.run=^" + TEST + "$", "-test.count=1", "-test.v", "-test.timeout=10s"], 10),
    ]
    for name, command, timeout in commands:
        phase, log = run_phase(command, directory, environment, name, timeout)
        result["phases"][name] = phase
        if phase["cleanup"] != "reaped":
            result["verdict"] = "cleanup_failed"
            return result
        if time.monotonic() - started > 210:
            result["verdict"] = "budget_exceeded"
            return result
        if phase["timeout"]:
            result["verdict"] = name + "_timeout"
            return result
        if name == "test":
            result.update(parse_result(log.read_bytes()))
            if phase["exit"] != 0 and result["verdict"] == "pass":
                result["verdict"] = "test_failed"
        elif phase["exit"] != 0:
            result["verdict"] = name + "_failed"
            return result
    return result


def main():
    temporary = Path(os.environ.get("RUNNER_TEMP", ""))
    if (len(sys.argv) != 1 or os.environ.get("GITHUB_ACTIONS") != "true"
            or os.environ.get("LANTERN_CI_DISPOSABLE_SOURCE_INPUT") != "github-hosted"
            or platform.system() != "Darwin" or platform.machine() != "arm64"
            or not temporary.is_absolute() or not temporary.is_dir()
            or not re.fullmatch(r"[0-9a-f]{40}", os.environ.get("GITHUB_SHA", ""))):
        print('{"verdict":"environment_refused"}')
        return 1
    environment = {key: value for key, value in os.environ.items() if not key.upper().startswith("LANTERN_")}
    environment.update(GOFLAGS="", GOWORK=str(ROOT / "go.work"), GOOS="darwin", GOARCH="arm64", LANTERN_CI_DISPOSABLE_SOURCE_INPUT="github-hosted", LANTERN_TEST_CONFIGURED_TIME_UPSTREAM="1")
    directory = Path(tempfile.mkdtemp(prefix="lantern-native-time-private-", dir=temporary))
    directory.chmod(0o700)
    try:
        result = campaign(directory, environment)
    except (OSError, subprocess.SubprocessError):
        result = {"verdict": "execution_failed"}
    result["source_sha"] = os.environ["GITHUB_SHA"]
    result["platform"] = "darwin_arm64"
    for key in ("ImageOS", "ImageVersion"):
        value = os.environ.get(key, "")
        if re.fullmatch(r"[A-Za-z0-9_.-]{1,80}", value):
            result[key] = value
    safe = json.dumps(result, sort_keys=True) + "\n"
    (temporary / "lantern-native-time-result.json").write_text(safe)
    print(safe, end="")
    return 0 if result["verdict"] == "pass" else 1


if __name__ == "__main__":
    raise SystemExit(main())
