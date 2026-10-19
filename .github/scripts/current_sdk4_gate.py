#!/usr/bin/env python3
"""Functional validation and configured source integration for current authority.

Receipts are output, never local carry input.

Artifact verification requires the digest emitted by the successful same-run CI
job, not a digest supplied inside the artifact. It also checks the exact checkout
and raw execution events. No clock/OS configuration or credential is changed.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import time
import uuid


FUNCTIONAL_VALIDATION = "functional-validation"
CONFIGURED_SOURCE_INTEGRATION = "configured-source-integration"
PROOF_KINDS = (FUNCTIONAL_VALIDATION, CONFIGURED_SOURCE_INTEGRATION)


ROOT_TEST = "TestCurrentSecurityPublicNativeGate"
SCOPED_TEST = "TestAuth_OIDCRustScopedChangesFacadeRealConnect"
PACKAGE = "github.com/anaregdesign/lantern/tests/integration"
RETAINED_ROOT_TESTS = (
    "TestAuth_ChangesBoundedTerminationAndCursorResumeHTTP2",
    "TestAuth_HeadManagedBlindCreateDeleteReceiptAndGoFacadeRealConnect",
    "TestAuth_MachineRoleBearerRealConnect",
    "TestAuth_OIDCAdminScopeAndAddRecoveryRealConnect",
    "TestAuth_OIDCAdminVertexCountRealConnect",
    "TestAuth_OIDCBrowserLoginFailuresRealConnect",
    "TestAuth_OIDCBrowserSessionRealConnect",
    "TestAuth_OIDCDartScopedChangesFacadeRealConnect",
    "TestAuth_OIDCExactDataAndLifecycleRealConnect",
    "TestAuth_OIDCGoScopedChangesFacadeRealConnect",
    "TestAuth_OIDCHeadManagedEdgesRealConnect",
    "TestAuth_OIDCManagementRealConnect",
    "TestAuth_OIDCMixedBearerProfileCannotMutateRealConnect",
    "TestAuth_OIDCNodeScopedChangesFacadeRealConnect",
    "TestAuth_OIDCOrdinaryManagementQualifiedHumanRealConnect",
    "TestAuth_OIDCPurposeAuthorizationRealConnect",
    "TestAuth_OIDCScopedChangesRealConnect",
    "TestAuth_OIDCScopedCollectionsRealConnect",
    "TestAuth_OIDCScopedReceiptRealConnect",
    "TestAuth_OIDCSecurityChangeCommitProofRealConnect",
    "TestAuth_OIDCSecurityPrecommitRefusalsRealConnect",
    "TestAuth_OIDCSharedRankingConstrainedQueriesRealConnect",
    "TestAuth_PublicChangeRejectionPreservesRPCProtocol",
    "TestAuth_PublicChangesBenchmarkConsumerRealWire",
)
SDK_TESTS = tuple(f"{ROOT_TEST}/{sdk}" for sdk in ("go", "bun", "dart", "cargo"))
FUNCTIONAL_VALIDATION_CASES = tuple(
    f"{ROOT_TEST}/{name}"
    for name in (
        "public_protocols_and_full_binding",
        "ordinary_review_apply_foreign_status",
        "full-cut_and_profile_mismatch_refuse_before_dispatch",
        "sdk_and_typed_data_response",
        "reader_explicit_Deny_and_no_write_grant",
        "machine_data_grants_and_management_refusal",
        "scoped_receipt_export_and_CDC_cut_invalidation",
        "active_export_grant_revocation",
        "operation-bound_real_Code_approval",
        "browser_Code_one_exchange_and_CSRF_bootstrap",
        "original_stop_observation_remains_separate_from_Apply",
        "stale_administrator_cannot_consume_retained_new_work",
        "public_authorization_stops_after_quorum_loss",
    )
)
FUNCTIONAL_VALIDATION_PROTOCOLS = tuple(
    f"{FUNCTIONAL_VALIDATION_CASES[0]}/{mode}"
    for mode in (
        "h2-connect-proto",
        "h2-connect-json",
        "h2-grpc",
        "h2-grpc-web",
        "http1-connect-json",
    )
)
CONFIGURED_SOURCE_INTEGRATION_CASES = tuple(
    f"{ROOT_TEST}/{name}"
    for name in (
        "public_protocols_and_full_binding",
        "ordinary_review_apply_foreign_status",
        "sdk_and_typed_data_response",
        "reader_explicit_Deny_and_no_write_grant",
        "original_stop_observation_remains_separate_from_Apply",
        "active_export_grant_revocation",
        "source_acquisition_budget",
    )
)
CONFIGURED_SOURCE_INTEGRATION_ENVIRONMENT = {
    "LANTERN_CURRENT_PUBLIC_GATE": "1",
    "LANTERN_CURRENT_PUBLIC_SDK4": "1",
    "LANTERN_CURRENT_CONFIGURED_SOURCE_INTEGRATION": "1",
}
RECEIPT_SCHEMA = 3

FUNCTIONAL_VALIDATION_ENVIRONMENT = {
    "LANTERN_CURRENT_PUBLIC_GATE": "1",
    "LANTERN_CURRENT_PUBLIC_SDK4": "1",
    "LANTERN_CURRENT_CONFIGURED_SOURCE_INTEGRATION": "0",
}
LOG_NAMES = ("events.jsonl", "stderr.log")
NATIVE_MARKER = "production native constructor: independently provisioned three voters, two origins, observer; no injected clock or H"
FIXTURE_MARKER = "test-build controlled source: native sampler and loopback UDP; synthetic UTC; shared current composition, three voters, two origins, observer"
FIXTURE_TAG = "lantern_test_time"
MECHANISM_TESTS = ("TestAuthorityTimeFixtureLifecycle", "TestAuthorityTimeFixtureLossRecovery")
MECHANISM_PACKAGE = "github.com/anaregdesign/lantern/server/internal/security"


def proof_contract(kind):
    if kind not in PROOF_KINDS:
        raise ValueError("unknown proof kind")
    return {
        "kind": kind,
        "source_kind": "controlled-loopback" if kind == FUNCTIONAL_VALIDATION else "configured-source",
        "synthetic_utc": kind == FUNCTIONAL_VALIDATION,
        "build_kind": "test-fixture" if kind == FUNCTIONAL_VALIDATION else "normal",
        "build_tags": [FIXTURE_TAG] if kind == FUNCTIONAL_VALIDATION else [],
        "case_contract": "functional-full-v3" if kind == FUNCTIONAL_VALIDATION else "source-cohort-v3",
    }


def build_flags(kind):
    return ["-tags=" + tag for tag in proof_contract(kind)["build_tags"]]


def log_names(kind):
    proof_contract(kind)
    return (
        LOG_NAMES + ("mechanism-events.jsonl", "mechanism-stderr.log")
        if kind == FUNCTIONAL_VALIDATION
        else LOG_NAMES
    )


def execution_environment(kind):
    proof_contract(kind)
    return (
        FUNCTIONAL_VALIDATION_ENVIRONMENT
        if kind == FUNCTIONAL_VALIDATION
        else CONFIGURED_SOURCE_INTEGRATION_ENVIRONMENT
    )


def required_tests(kind):
    proof_contract(kind)
    return (
        (
            ROOT_TEST, *SDK_TESTS, *FUNCTIONAL_VALIDATION_CASES,
            *FUNCTIONAL_VALIDATION_PROTOCOLS, *RETAINED_ROOT_TESTS, SCOPED_TEST,
        )
        if kind == FUNCTIONAL_VALIDATION
        else (ROOT_TEST, *SDK_TESTS, *CONFIGURED_SOURCE_INTEGRATION_CASES)
    )


def acquisition_counts(path):
    rows = []
    pattern = re.compile(
        r"source_acquisition node=(\d+) attempts=(\d+) writes=(\d+) installations=(\d+) in_flight=(\w+) failed=(\w+) denied=(\w+)"
    )
    for line in path.read_text().splitlines():
        event = json.loads(line)
        if (
            event.get("Package") == PACKAGE
            and event.get("Test") == CONFIGURED_SOURCE_INTEGRATION_CASES[-1]
            and event.get("Action") == "output"
        ):
            if match := pattern.search(event.get("Output", "")):
                node, attempts, writes, installs, flight, failed, denied = match.groups()
                rows.append(
                    {
                        "node": int(node),
                        "attempts": int(attempts),
                        "writes": int(writes),
                        "installations": int(installs),
                        "in_flight": flight,
                        "failed": failed,
                        "denied": denied,
                    }
                )
    if [r["node"] for r in rows] != [1, 2, 3] or any(
        not (0 < r["installations"] <= r["writes"] <= r["attempts"])
        or (r["in_flight"], r["failed"], r["denied"]) != ("false", "false", "false")
        for r in rows
    ):
        raise ValueError("incomplete source acquisition counts")
    if sum(r["attempts"] for r in rows) > 12 or sum(r["writes"] for r in rows) > 12:
        raise ValueError("source acquisition budget exceeded")
    return rows


def observed_execution(evidence):
    """Report partial execution honestly; this never authorizes a PASS."""
    result = {}
    for filename, names in (
        (
            "events.jsonl",
            tuple(dict.fromkeys((*required_tests(FUNCTIONAL_VALIDATION), *required_tests(CONFIGURED_SOURCE_INTEGRATION)))),
        ),
    ):
        actions = {name: [] for name in names}
        constructors = []
        path = evidence / filename
        if path.is_file():
            for line in path.read_text(errors="replace").splitlines():
                try:
                    event = json.loads(line)
                except ValueError:
                    continue
                if event.get("Package") != PACKAGE:
                    continue
                name, action = event.get("Test"), event.get("Action")
                if name in actions and action in ("run", "pass", "fail", "skip"):
                    actions[name].append(action)
                if match := re.search(
                    r"current_constructor node=(\d+) mode=(\w+) passed=(\w+)",
                    event.get("Output", ""),
                ):
                    constructors.append(list(match.groups()))
        result[filename] = {
            "tests": {
                name: (
                    "NOT_RUN"
                    if not value
                    else "PASS"
                    if value == ["run", "pass"]
                    else "FAIL"
                    if "fail" in value
                    else "INCOMPLETE"
                )
                for name, value in actions.items()
            },
            "constructors": constructors,
        }
    return result


def mechanism_command():
    return [
        "go",
        "test",
        "-json",
        *build_flags(FUNCTIONAL_VALIDATION),
        "./server/internal/security",
        "-run",
        "^(" + "|".join(MECHANISM_TESTS) + ")$",
        "-count=1",
        "-timeout=4m",
    ]


def sha(data):
    return hashlib.sha256(data).hexdigest()


def encoded(value):
    return (json.dumps(value, sort_keys=True, indent=2) + "\n").encode()


def output(root, command, env=None):
    return subprocess.check_output(
        command, cwd=root, env=env, text=True, stderr=subprocess.STDOUT
    ).strip()


def snapshot(root):
    if output(root, ["git", "status", "--porcelain"]):
        raise ValueError("qualification requires a clean, frozen checkout")
    rows = []
    for entry in subprocess.check_output(["git", "ls-files", "--stage", "-z"], cwd=root).split(
        b"\0"
    ):
        if not entry:
            continue
        metadata, name = entry.split(b"\t", 1)
        mode, blob, stage = metadata.decode().split()
        path = root / name.decode()
        if stage != "0" or mode not in ("100644", "100755", "120000"):
            raise ValueError("unsupported tracked input")
        data = os.readlink(path).encode() if mode == "120000" else path.read_bytes()
        actual = hashlib.sha1(b"blob " + str(len(data)).encode() + b"\0" + data).hexdigest()
        if actual != blob:
            raise ValueError(f"tracked source drift: {name.decode()}")
        rows.append([name.decode(), mode, blob, sha(data)])
    return {
        "head": output(root, ["git", "rev-parse", "HEAD"]),
        "tree": output(root, ["git", "rev-parse", "HEAD^{tree}"]),
        "tracked_files": len(rows),
        "tracked_inputs_sha256": sha(encoded(rows)),
    }


def command(kind=CONFIGURED_SOURCE_INTEGRATION):
    # Functional validation owns the retained Rust case. Configured-source
    # integration creates one cohort without repeating functional constructors.
    names = [ROOT_TEST, *RETAINED_ROOT_TESTS, SCOPED_TEST] if kind == FUNCTIONAL_VALIDATION else [ROOT_TEST]
    return [
        "go",
        "test",
        "-json",
        *build_flags(kind),
        "./tests/integration",
        "-run",
        "^(" + "|".join(names) + ")$",
        "-count=1",
        "-timeout=6m",
    ]


def execution_events(path, kind=CONFIGURED_SOURCE_INTEGRATION):
    required = required_tests(kind)
    return required_execution_events(path, required, kind)


def required_execution_events(path, required, kind=CONFIGURED_SOURCE_INTEGRATION, package=PACKAGE):
    proof_contract(kind)
    state = {name: [] for name in required}
    package_passes = 0
    native_markers = 0
    constructors = []
    marker = FIXTURE_MARKER if kind == FUNCTIONAL_VALIDATION else NATIVE_MARKER
    wrong_marker = NATIVE_MARKER if kind == FUNCTIONAL_VALIDATION else FIXTURE_MARKER
    with path.open() as stream:
        for line in stream:
            event = json.loads(line)
            if event.get("Package") != package:
                continue
            action, test = event.get("Action"), event.get("Test")
            if action in ("fail", "skip"):
                raise ValueError(f"required execution failed/skipped: {test or PACKAGE}")
            if test in state and action in ("run", "pass"):
                state[test].append(action)
            if action == "pass" and test is None:
                package_passes += 1
            if action == "output" and test == ROOT_TEST:
                line = event.get("Output", "")
                if wrong_marker in line:
                    raise ValueError("wrong proof constructor marker")
                if marker in line:
                    native_markers += 1
                if match := re.search(
                    r"current_constructor node=(\d+) mode=(\w+) passed=(\w+)", line
                ):
                    constructors.append(match.groups())
    if any(events != ["run", "pass"] for events in state.values()):
        raise ValueError(f"required root/SDK execution absent, duplicated or incomplete: {state}")
    if package_passes != 1 or (package == PACKAGE and native_markers != 1):
        raise ValueError("native constructor or successful package completion missing")
    expected_constructors = [(str(i), "fresh", "true") for i in (1, 2, 3)]
    if kind == FUNCTIONAL_VALIDATION:
        # Functional validation resumes member2 with intact custody/state in the
        # browser case. Configured-source integration never restarts an owner.
        expected_constructors.append(("2", "resume", "true"))
    if package == PACKAGE and constructors != expected_constructors:
        raise ValueError("required per-owner constructor acquisition evidence missing or failed")
    return list(required)


def environment(root, env, kind=CONFIGURED_SOURCE_INTEGRATION):
    proof_contract(kind)
    if platform.system() not in ("Darwin", "Linux") or platform.machine() not in (
        "arm64",
        "aarch64",
        "x86_64",
    ):
        raise ValueError("unsupported native execution platform")
    # No overlay, test-binary substitute or language loader can substitute inputs
    # outside the frozen checkout. The normal configured dependency caches remain.
    for name in (
        "GOFLAGS",
        "NODE_OPTIONS",
        "BUN_OPTIONS",
        "RUSTFLAGS",
        "RUSTC",
        "CARGO_ENCODED_RUSTFLAGS",
        "RUSTC_WRAPPER",
        "RUSTC_WORKSPACE_WRAPPER",
        "LANTERN_CURRENT_FIXTURE_EXPORTER",
        "LD_PRELOAD",
        "DYLD_INSERT_LIBRARIES",
    ):
        if env.get(name):
            raise ValueError(f"unsupported qualification override: {name}")
    tools = {}
    for name, args in (
        ("go", ["version"]),
        ("bun", ["--version"]),
        ("dart", ["--version"]),
        ("cargo", ["--version"]),
        ("rustc", ["-vV"]),
    ):
        binary = shutil.which(name, path=env.get("PATH"))
        if binary is None:
            raise ValueError(f"required SDK tool missing: {name}")
        path = Path(binary).resolve()
        tools[name] = {
            "path": str(path),
            "sha256": sha(path.read_bytes()),
            "version": output(root, [binary, *args], env),
        }
        selected = path
        if name in ("cargo", "rustc") and shutil.which("rustup", path=env.get("PATH")):
            selected = Path(output(root, ["rustup", "which", name], env)).resolve()
        elif name == "dart" and (path.parent / "cache/dart-sdk/bin/dart").is_file():
            selected = path.parent / "cache/dart-sdk/bin/dart"
        tools[name]["selected_path"] = str(selected)
        tools[name]["selected_sha256"] = sha(selected.read_bytes())
    settings = json.loads(
        output(
            root,
            [
                "go",
                "env",
                "-json",
                "GOVERSION",
                "GOOS",
                "GOARCH",
                "GOROOT",
                "GOWORK",
                "GOFLAGS",
                "GOEXPERIMENT",
                "CGO_ENABLED",
                "GOAMD64",
                "GOARM64",
                "CC",
                "CXX",
            ],
            env,
        )
    )
    if settings["GOFLAGS"] or Path(settings["GOWORK"]).resolve() != root / "go.work":
        raise ValueError("unsupported Go workspace/flags")
    native_goos = {"Darwin": "darwin", "Linux": "linux"}[platform.system()]
    native_goarch = "amd64" if platform.machine() == "x86_64" else "arm64"
    if (settings["GOOS"], settings["GOARCH"]) != (native_goos, native_goarch):
        raise ValueError("native qualification cannot cross-compile its test")
    compiler = Path(settings["GOROOT"]) / "bin/go"
    tools["go"]["selected_toolchain_sha256"] = sha(compiler.read_bytes())
    config = Path("/etc/ntp.conf").read_bytes() if kind == CONFIGURED_SOURCE_INTEGRATION else b"server fixture.invalid\n"
    if not 0 < len(config) <= 4096:
        raise ValueError("native source configuration missing/oversize")
    return {
        "platform": platform.platform(),
        "machine": platform.machine(),
        "tools": tools,
        "go_settings": settings,
        "source_config_sha256": sha(config),
        "source_config_path": "/etc/ntp.conf" if kind == CONFIGURED_SOURCE_INTEGRATION else "compile-time fixture",
        "environment": execution_environment(kind),
        "proof": proof_contract(kind),
        "native_environment": native_environment(),
        "native_premises": "Existing conditional native counter profile; configured-source-integration additionally requires an honest configured time source/path. Functional-validation synthetic UTC does not qualify real UTC or external reachability; neither kind qualifies real identity providers or physical devices.",
    }


def native_environment():
    result = {
        "system": platform.system(),
        "kernel": platform.release(),
        "machine": platform.machine(),
    }
    if platform.system() == "Linux":
        for name in (
            "/proc/sys/kernel/random/boot_id",
            "/proc/self/timens_offsets",
            "/etc/os-release",
        ):
            result[name] = sha(Path(name).read_bytes())
        result["time_namespace"] = os.readlink("/proc/self/ns/time")
        result["capabilities"] = [
            line
            for line in Path("/proc/self/status").read_text().splitlines()
            if line.startswith(("Cap", "NoNewPrivs:"))
        ]
    return result


def verify(root, evidence, expected_head, trusted_digest, kind=CONFIGURED_SOURCE_INTEGRATION):
    # Only a trusted same-workflow job output supplies trusted_digest in CI.
    # A standalone artifact's own status/hash is not authority to accept it.
    data = (evidence / "receipt.json").read_bytes()
    if not trusted_digest or sha(data) != trusted_digest:
        raise ValueError("missing/untrusted or changed SDK4 receipt")
    receipt = json.loads(data)
    source = snapshot(root)
    if source["head"] != expected_head or receipt.get("source") != source:
        raise ValueError("SDK4 receipt belongs to another candidate/source")
    if (
        receipt.get("schema") != RECEIPT_SCHEMA
        or receipt.get("status") != "PASS"
        or receipt.get("exit") != 0
    ):
        raise ValueError("SDK4 receipt is not successful")
    if receipt.get("proof") != proof_contract(kind):
        raise ValueError("receipt proof kind/build/source mismatch")
    if receipt.get("command") != command(kind):
        raise ValueError("SDK4 execution command mismatch")
    if receipt.get("runtime", {}).get("environment") != execution_environment(kind):
        raise ValueError("mandatory SDK4 environment absent")
    runtime = receipt["runtime"]
    if runtime.get("proof") != proof_contract(kind):
        raise ValueError("runtime proof kind/build/source mismatch")
    if not runtime.get("native_environment"):
        raise ValueError("native environment binding missing")
    if set(runtime.get("tools", {})) != {"go", "bun", "dart", "cargo", "rustc"}:
        raise ValueError("SDK4 toolchain identities missing")
    for tool in runtime["tools"].values():
        if not tool.get("version") or len(tool.get("selected_sha256", "")) != 64:
            raise ValueError("SDK4 toolchain binding incomplete")
    if len(runtime.get("source_config_sha256", "")) != 64:
        raise ValueError("native source configuration binding missing")
    runner = root / ".github/scripts/current_sdk4_gate.py"
    if receipt.get("runner_sha256") != sha(runner.read_bytes()):
        raise ValueError("SDK4 runner mismatch")
    if set(receipt.get("logs", {})) != set(log_names(kind)):
        raise ValueError("required raw execution logs missing")
    for name, digest in receipt["logs"].items():
        if sha((evidence / name).read_bytes()) != digest:
            raise ValueError(f"SDK4 raw log mismatch: {name}")
    if receipt.get("executed_tests") != execution_events(evidence / "events.jsonl", kind):
        raise ValueError("SDK4 execution evidence mismatch")
    if receipt.get("observed") != observed_execution(evidence):
        raise ValueError("partial execution result mismatch")
    if "export" in receipt:
        raise ValueError("separate export cohort is not part of the proof contract")
    if kind == CONFIGURED_SOURCE_INTEGRATION and receipt.get("acquisition") != acquisition_counts(evidence / "events.jsonl"):
        raise ValueError("single-cohort source acquisition evidence mismatch")
    if kind == FUNCTIONAL_VALIDATION:
        mechanism = receipt.get("mechanism", {})
        if mechanism.get("command") != mechanism_command() or mechanism.get("exit") != 0:
            raise ValueError("native loopback loss/recovery execution missing")
        if mechanism.get("executed_tests") != required_execution_events(
            evidence / "mechanism-events.jsonl", MECHANISM_TESTS, kind, MECHANISM_PACKAGE
        ):
            raise ValueError("native loopback execution evidence mismatch")
    return receipt


def run(root, evidence, expected_head=None, kind=CONFIGURED_SOURCE_INTEGRATION):
    if evidence == root or root in evidence.parents:
        raise ValueError("evidence must be outside the checkout")
    evidence.mkdir(parents=True, exist_ok=False)
    receipt = {
        "schema": RECEIPT_SCHEMA,
        "proof": proof_contract(kind),
        "status": "FAIL",
        "stage": "preflight",
        "started_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "command": command(kind),
    }
    try:
        source = snapshot(root)
        receipt.update(source=source, runner_sha256=sha(Path(__file__).read_bytes()))
        if expected_head is not None and source["head"] != expected_head:
            raise ValueError("SDK4 checkout is not the expected candidate")
        env = os.environ.copy()
        env.update(execution_environment(kind), GOWORK=str(root / "go.work"))
        runtime = environment(root, env, kind)
        receipt["runtime"] = runtime
        if kind == FUNCTIONAL_VALIDATION:
            receipt["stage"] = "native-loopback"
            mechanism = {"command": mechanism_command()}
            receipt["mechanism"] = mechanism
            with (
                (evidence / "mechanism-events.jsonl").open("wb") as stdout,
                (evidence / "mechanism-stderr.log").open("wb") as stderr,
            ):
                result = subprocess.run(
                    mechanism_command(),
                    cwd=root,
                    env=env,
                    stdout=stdout,
                    stderr=stderr,
                    timeout=300,
                )
            mechanism["exit"] = result.returncode
            if result.returncode:
                raise ValueError(f"native loopback command failed: exit {result.returncode}")
            mechanism["executed_tests"] = required_execution_events(
                evidence / "mechanism-events.jsonl", MECHANISM_TESTS, kind, MECHANISM_PACKAGE
            )
        receipt["stage"] = "sdk4"
        with (
            (evidence / "events.jsonl").open("wb") as stdout,
            (evidence / "stderr.log").open("wb") as stderr,
        ):
            result = subprocess.run(
                command(kind),
                cwd=root,
                env=env,
                stdout=stdout,
                stderr=stderr,
                timeout=600,
            )
        receipt["exit"] = result.returncode
        if result.returncode:
            raise ValueError(f"native SDK4 command failed: exit {result.returncode}")
        receipt["executed_tests"] = execution_events(evidence / "events.jsonl", kind)
        if kind == CONFIGURED_SOURCE_INTEGRATION:
            receipt["acquisition"] = acquisition_counts(evidence / "events.jsonl")
        if snapshot(root) != source or environment(root, env, kind) != runtime:
            raise ValueError("source/runtime configuration changed during SDK4 qualification")
        receipt["status"] = "PASS"
        receipt["stage"] = "complete"
    except Exception as error:
        receipt["error"] = str(error)
    receipt["logs"] = {
        name: sha((evidence / name).read_bytes())
        for name in log_names(kind)
        if (evidence / name).is_file()
    }
    receipt["observed"] = observed_execution(evidence)
    receipt["finished_utc"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    data = encoded(receipt)
    (evidence / "receipt.json").write_bytes(data)
    if receipt["status"] != "PASS":
        raise ValueError(receipt["error"])
    digest = sha(data)
    verify(root, evidence, source["head"], digest, kind)
    print(
        f"PASS {kind} SDK4 head={source['head']} tree={source['tree']} receipt_sha256={digest} evidence={evidence}",
        flush=True,
    )
    if os.environ.get("GITHUB_OUTPUT"):
        with open(os.environ["GITHUB_OUTPUT"], "a") as stream:
            stream.write(f"receipt_sha256={digest}\nhead={source['head']}\ntree={source['tree']}\n")
    return receipt


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="action", required=True)
    for name in ("run", "verify"):
        item = sub.add_parser(name)
        item.add_argument("--root", type=Path, default=Path.cwd())
        item.add_argument("--evidence", type=Path, required=True)
        item.add_argument("--proof-kind", choices=PROOF_KINDS, default=CONFIGURED_SOURCE_INTEGRATION)
        if name == "run":
            item.add_argument("--expected-head")
            item.add_argument(
                "--fresh-child",
                action="store_true",
                help="retain a new output-only receipt for each local qualification",
            )
            item.add_argument(
                "--with-functional-validation",
                action="store_true",
                help="run separate functional-validation evidence before configured-source-integration; conservative full local gate",
            )
        else:
            item.add_argument("--expected-head", required=True)
            item.add_argument("--trusted-receipt-sha256", required=True)
    args = parser.parse_args()
    try:
        if args.action == "run":
            evidence = args.evidence.resolve()
            if args.fresh_child:
                evidence = evidence / str(uuid.uuid4())
            if args.with_functional_validation:
                if args.proof_kind != CONFIGURED_SOURCE_INTEGRATION:
                    raise ValueError("--with-functional-validation requires configured-source-integration")
                run(
                    args.root.resolve(),
                    evidence / FUNCTIONAL_VALIDATION,
                    args.expected_head,
                    FUNCTIONAL_VALIDATION,
                )
                evidence = evidence / CONFIGURED_SOURCE_INTEGRATION
            run(
                args.root.resolve(),
                evidence,
                args.expected_head,
                args.proof_kind,
            )
        else:
            verify(
                args.root.resolve(),
                args.evidence.resolve(),
                args.expected_head,
                args.trusted_receipt_sha256,
                args.proof_kind,
            )
    except Exception as error:
        parser.exit(1, f"FAIL current authority {args.proof_kind}: {error}\n")


if __name__ == "__main__":
    main()
