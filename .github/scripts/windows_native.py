#!/usr/bin/env python3
"""Run the selected native Windows boundary tests; empty matches fail."""

import argparse
import json
import os
from pathlib import Path
import subprocess


GO_TESTS = (
    "TestCreateAndCheck", "TestCreateTemp", "TestWindowsHandleOwnerDACL",
    "TestFileWALLeaseCrossProcess", "TestSyncFileWALDirectory",
    "TestSyncClockJournalDirectory", "TestClockJournalSurvivesProcessExit",
    "TestSyncReceiptDirectory", "TestServingRuntimeDurableFreshRestartCertifiesOneCut",
    "TestWritePrivateInput", "TestFixtureFailureCategory", "TestReservedListenerOwnership",
    "TestReservedListenerRoleGuards", "TestListenerLaunchProtocol",
    "TestNewListenerAdoptionRequiresCertification", "TestPeerListenerAdoptionRequiresExactRuntime",
    "TestListenerLaunchRejectsUnboundArguments", "TestNativeListenerLaunchLifecycle",
    "TestFixtureReservedEndpointValidation",
)
RUST_TESTS = (
    "transport::tests::real_wire_https_roots_and_invalid_certificates",
    "transport::tests::real_wire_mtls_and_bearer_rotation",
    "test_server::tests::real_wire_fixture_owns_ephemeral_listener_on_return",
)


def require_go(output, expected=()):
    events = [json.loads(line) for line in output.splitlines() if line.startswith("{")]
    passed = {event.get("Test") for event in events if event.get("Action") == "pass"}
    if not any(passed) or not set(expected) <= passed:
        raise ValueError("selected native Go tests missing, skipped or incomplete")
    if any(event.get("Action") == "fail" for event in events):
        raise ValueError("native Go failure")


def require_rust(output, name):
    if f"test {name} ... ok" not in output.splitlines():
        raise ValueError("selected native Rust test did not pass: " + name)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--rust", choices=("true", "false"), required=True)
    parser.add_argument("--evidence", type=Path, required=True)
    args = parser.parse_args()
    if os.name != "nt":
        parser.error("native Windows execution required")
    args.evidence.mkdir(parents=True, exist_ok=False)
    root = Path.cwd()
    env = os.environ.copy()
    server = args.evidence.resolve() / "lantern-smoke.exe"
    fixture = args.evidence.resolve() / "lantern-authfixture.exe"
    env.update(LANTERN_TEST_LAUNCH_SERVER=str(server), LANTERN_RUST_TEST_SERVER=str(server),
               LANTERN_RUST_TEST_AUTH_FIXTURE=str(fixture))

    def run(name, command, directory=root):
        result = subprocess.run(command, cwd=directory, env=env, text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False)
        (args.evidence / (name + ".log")).write_text(result.stdout)
        print(result.stdout, end="", flush=True)
        if result.returncode:
            raise ValueError(f"{name} failed ({result.returncode})")
        return result.stdout

    run("server-build", ["go", "build", "-o", str(server), "./server/cmd"])
    run("fixture-build", ["go", "build", "-o", str(fixture), "./server/cmd/authfixture"])
    pattern = "^(" + "|".join(GO_TESTS) + ")$"
    output = run("native-go", ["go", "test", "-json", "-count=1", "./core/privatefile",
        "./core/mutationlog", "./core/mutationreceipt", "./server/service", "./server/provider",
        "./server/internal/listenerlaunch", "./server/cmd", "./server/cmd/authfixture", "-run", pattern])
    require_go(output, GO_TESTS)
    require_go(run("backup", ["go", "test", "-json", "-count=1", "./backup"], root / "server"))
    if args.rust == "true":
        directory = root / "sdks/rust"
        name = "smoke::native_tls_roots_are_available_by_default"
        require_rust(run("native-roots", ["cargo", "test", "--locked", "--no-default-features",
                     "--lib", name, "--", "--exact"], directory), name)
        name = "value::tests::timestamps_validate_full_proto_range_and_distinguish_zero_time"
        require_rust(run("native-values", ["cargo", "test", "--locked", "--lib", name,
                     "--", "--exact"], directory), name)
        for index, name in enumerate(RUST_TESTS):
            require_rust(run(f"native-wire-{index}", ["cargo", "test", "--locked", "--all-features",
                "--lib", name, "--", "--ignored", "--exact", "--test-threads=1"], directory), name)


if __name__ == "__main__":
    main()
