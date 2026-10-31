#!/usr/bin/env python3
"""Run bounded harness consumers before images, long workloads or matrices.

These contracts inspect fixtures and exercise owned fake Docker commands. They
do not start a Docker workload and do not qualify product performance or a host.
"""

import argparse
from collections import Counter
import contextlib
import hashlib
import io
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import tempfile
import unittest

import verify_root_release_binaries as release


ROOT = Path(__file__).resolve().parents[2]
GROUPS = {
    "bench-time": ("test_bench_time_profile", "test_retained_heap_pair",
                   "test_local_release_qualification"),
    "release-binaries": ("test_verify_root_release_binaries", "test_ci_efficiency"),
    "git-fixture": ("test_git_test_fixture", "test_local_gate_session", "test_verify_go_sdk_release",
                    "test_local_prose_gate", "test_native_login_gate", "test_ci_docs",
                    "test_current_sdk4_gate", "test_validation_scope"),
}
GO_PACKAGE = "github.com/anaregdesign/lantern/testbed/bench"
GO_TESTS = (
    "TestOwnedComposeMeasurementSecurityMountProfile",
    "TestOwnedComposeLocalTimeNTPFileBinding",
    "TestOwnedComposeLocalTimeCaptureStartAdmission",
    "TestOwnedComposeLocalTimeRelayMemoryProfile",
    "TestOwnedComposeLocalTimeFailureDiagnostics",
    "TestReceiptBenchCleanupPreservesFailureAndProjectScope",
)


def require(condition, message):
    if not condition:
        raise ValueError(message)


def require_go_events(output, expected=GO_TESTS):
    """Every selected top-level test must actually run and pass exactly once."""
    require(expected and len(set(expected)) == len(expected), "invalid Go contract selection")
    runs, passes = Counter(), Counter()
    package_passes = 0
    fixture_output = []
    for line in output.splitlines():
        event = json.loads(line)
        require(type(event) is dict and event.get("Package") == GO_PACKAGE,
                "foreign or malformed Go contract event")
        action, name = event.get("Action"), event.get("Test")
        require(action not in {"fail", "skip"}, "Go contract failed or skipped")
        if name is not None:
            require(type(name) is str and name.split("/", 1)[0] in expected,
                    "unselected Go contract executed")
            if name in expected and action == "run":
                runs[name] += 1
            if name in expected and action == "pass":
                passes[name] += 1
        elif action == "pass":
            package_passes += 1
        if "Output" in event:
            require(type(event["Output"]) is str, "malformed Go contract output")
            fixture_output.append(event["Output"])
    exact = Counter({name: 1 for name in expected})
    require(runs == passes == exact and package_passes == 1,
            "selected Go contracts missing, repeated or incomplete")
    raw = "".join(fixture_output)
    started = Counter(re.findall(r"(?m)^owned_compose_fixture_started pgid=(\d+) ", raw))
    terminal = re.findall(r"(?m)^owned_compose_fixture_terminal pgid=(\d+) inactive=(\w+) reaped=(\w+) timeout=(\w+) leftover=(\w+) ", raw)
    require(started and Counter(row[0] for row in terminal) == started
            and all(row[1:] == ("true", "true", "false", "false") for row in terminal),
            "owned shell fixture terminal evidence missing or incomplete")
    return len(expected)


@contextlib.contextmanager
def selected_environment(values):
    previous = {key: os.environ.get(key) for key in values}
    os.environ.update(values)
    try:
        yield
    finally:
        for key, value in previous.items():
            if value is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = value


def real_go_environment(root=ROOT):
    """Use the real release consumer; absent/wrong tools are failures, not skips."""
    pin = re.search(r"(?m)^go (\d+\.\d+\.\d+)$", (root / "go.mod").read_text())
    require(pin is not None, "exact Go source pin missing")
    pin = pin[1]
    with tempfile.TemporaryDirectory(prefix="lantern-go-env-contract-") as temporary:
        directory = Path(temporary).resolve()
        (directory / "go.mod").write_text("module example.invalid/harness-contract\n\ngo " + pin + "\n")
        (directory / "go.work").write_text("go " + pin + "\n\nuse .\n")
        raw = {"GOENV": "off", "GOTOOLCHAIN": "local", "GOFLAGS": "",
               "GOWORK": str(directory / "go.work")}
        # local selection prevents automatic toolchain downloads. Only version
        # and env are inspected; no resolution, compilation or network call.
        with selected_environment(raw):
            version = release.command(["go", "version"], directory).decode().strip()
            require(re.fullmatch(r"go version go" + re.escape(pin) + r" \S+/\S+", version),
                    "pinned Go unavailable for real environment contract")
            require(release.go_environment(directory) == raw,
                    "release receipt did not retain raw Go inputs")
    return {"version": version, "raw_goenv": "off", "effective_goenv": ""}


def run_go(root=ROOT):
    selector = "^(" + "|".join(GO_TESTS) + ")$"
    command = ["go", "test", "-json", "-count=1", "-timeout=30m", "./testbed/bench", "-run", selector]
    environment = dict(os.environ, GOENV="off", GOTOOLCHAIN="local", GOFLAGS="",
                       GOWORK=str(root / "go.work"))
    # A regular file cannot be held open as an unreadable pipe by descendants.
    # Existing fixture controllers own their separate process groups. Their
    # terminal records, not stopping Go's group, establish fixture cleanup.
    # Go writes dependency/build diagnostics to stderr even with -json. Keep
    # those bytes separately; only stdout is the test event stream.
    with tempfile.TemporaryFile() as capture, tempfile.TemporaryFile() as diagnostics:
        process = subprocess.Popen(command, cwd=root, env=environment,
                                   stdout=capture, stderr=diagnostics, start_new_session=True)
        try:
            # This backstop exceeds the sum of declared fixture/cleanup budgets;
            # ordinary runs end promptly. Do not interrupt a live controller at
            # the individual fixture's short timeout.
            process.wait(timeout=2100)
            code = process.returncode
        except subprocess.TimeoutExpired:
            try:
                os.killpg(process.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    pass
            code = 1
        capture.seek(0)
        diagnostics.seek(0)
        output, errors = capture.read(), diagnostics.read()
    return code, output, errors


def python_contracts(groups):
    suite = unittest.defaultTestLoader.loadTestsFromNames(
        [module for group in groups for module in GROUPS[group]])
    expected = suite.countTestCases()
    output = io.StringIO()
    result = unittest.TextTestRunner(stream=output, verbosity=2).run(suite)
    qualified = (expected > 0 and result.testsRun == expected and result.wasSuccessful()
                 and not result.skipped)
    return qualified, result.testsRun, output.getvalue()


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--group", action="append", choices=tuple(GROUPS))
    parser.add_argument("--tools-only", action="store_true",
                        help="only real-tool consumers; Python suites remain NOT_SELECTED")
    parser.add_argument("--evidence", type=Path, help="new directory for raw logs and content-free verdict")
    args = parser.parse_args(argv)
    groups = tuple(dict.fromkeys(args.group or GROUPS))
    if args.tools_only and not {"bench-time", "release-binaries"} & set(groups):
        parser.error("--tools-only requires a group with real-tool consumers")
    if args.evidence:
        args.evidence.mkdir(parents=True, exist_ok=False)
    report = {"groups": list(groups), "claim": "harness-tool-contracts" if args.tools_only else "harness-contracts",
              "python_status": "NOT_SELECTED" if args.tools_only else "NOT_RUN", "verdict": "fail", "logs": {}}

    def log(name, output, stream=sys.stdout):
        encoded = output.encode() if isinstance(output, str) else output
        report["logs"][name] = {"sha256": hashlib.sha256(encoded).hexdigest(), "bytes": len(encoded)}
        if args.evidence:
            (args.evidence / name).write_bytes(encoded)
        print(encoded.decode("utf-8", errors="replace"), end="", file=stream, flush=True)

    try:
        # The real release environment contract runs before a possibly skipped
        # unittest. Its required path never converts unavailable Go into PASS.
        if {"bench-time", "release-binaries"} & set(groups):
            report["real_go_environment"] = real_go_environment()
        if not args.tools_only:
            qualified, count, output = python_contracts(groups)
            log("python.log", output)
            report["python_tests"] = count
            report["python_status"] = "PASS" if qualified else "FAIL"
            require(qualified, "Python contracts failed, skipped or incomplete")
        if "bench-time" in groups:
            code, output, errors = run_go()
            report["go_exit_code"] = code
            log("go.jsonl", output)
            log("go.stderr.log", errors, stream=sys.stderr)
            require(code == 0, "Go harness consumer command failed; fixture cleanup is unproven until terminal evidence is checked")
            report["go_tests"] = require_go_events(output)
        report["verdict"] = "pass"
    except Exception as error:
        report["error"] = str(error)
    rendered = json.dumps(report, sort_keys=True) + "\n"
    if args.evidence:
        (args.evidence / "verdict.json").write_text(rendered)
    print(rendered, end="", flush=True)
    return 0 if report["verdict"] == "pass" else 1


if __name__ == "__main__":
    sys.exit(main())
