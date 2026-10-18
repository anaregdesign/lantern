#!/usr/bin/env python3
"""Mandatory native SDK4 execution; receipts are output, never local carry input.

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


ROOT_TEST = "TestCurrentSecurityPublicNativeGate"
EXPORT_TEST = f"{ROOT_TEST}/active_export_grant_revocation"
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
Q_CASES = tuple(
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
Q_ENVIRONMENT = {
    "LANTERN_CURRENT_PUBLIC_GATE": "1",
    "LANTERN_CURRENT_PUBLIC_SDK4": "1",
    "LANTERN_CURRENT_SOURCE_QUALIFICATION": "1",
}
RECEIPT_SCHEMA = 3

ENVIRONMENT = {
    "LANTERN_CURRENT_PUBLIC_GATE": "1",
    "LANTERN_CURRENT_PUBLIC_SDK4": "1",
    "LANTERN_CURRENT_SOURCE_QUALIFICATION": "0",
}
EXPORT_ENVIRONMENT = {**ENVIRONMENT, "LANTERN_CURRENT_PUBLIC_SDK4": "0"}
LOG_NAMES = ("events.jsonl", "stderr.log", "export-events.jsonl", "export-stderr.log")
NATIVE_MARKER = "production native constructor: independently provisioned three voters, two origins, observer; no injected clock or H"
FIXTURE_MARKER = "test-build controlled source: native sampler and loopback UDP; synthetic UTC; shared current composition, three voters, two origins, observer"
FIXTURE_TAG = "lantern_test_time"
MECHANISM_TESTS = ("TestAuthorityTimeFixtureLifecycle", "TestAuthorityTimeFixtureLossRecovery")
MECHANISM_PACKAGE = "github.com/anaregdesign/lantern/server/internal/security"


def proof_contract(kind):
    if kind not in ("F", "Q"):
        raise ValueError("unknown proof kind")
    return {
        "kind": kind,
        "source_kind": "controlled-loopback" if kind == "F" else "configured-source",
        "synthetic_utc": kind == "F",
        "build_kind": "test-fixture" if kind == "F" else "normal",
        "build_tags": [FIXTURE_TAG] if kind == "F" else [],
        "case_contract": "functional-full-v3" if kind == "F" else "source-cohort-v3",
    }


def build_flags(kind):
    return ["-tags=" + tag for tag in proof_contract(kind)["build_tags"]]


def log_names(kind):
    return (
        LOG_NAMES + ("mechanism-events.jsonl", "mechanism-stderr.log")
        if kind == "F"
        else LOG_NAMES[:2]
    )


def execution_environment(kind):
    proof_contract(kind)
    return ENVIRONMENT if kind == "F" else Q_ENVIRONMENT


def required_tests(kind):
    return (
        (ROOT_TEST, *SDK_TESTS, *RETAINED_ROOT_TESTS, SCOPED_TEST)
        if kind == "F"
        else (ROOT_TEST, *SDK_TESTS, *Q_CASES)
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
            and event.get("Test") == Q_CASES[-1]
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
        ("events.jsonl", (ROOT_TEST, *SDK_TESTS, *RETAINED_ROOT_TESTS, SCOPED_TEST, *Q_CASES)),
        ("export-events.jsonl", (ROOT_TEST, EXPORT_TEST)),
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
        *build_flags("F"),
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


def command(kind="Q"):
    # Full functional scope always owns the retained Rust case. Q creates
    # exactly one cohort and never repeats those functional constructors.
    names = [ROOT_TEST, *RETAINED_ROOT_TESTS, SCOPED_TEST] if kind == "F" else [ROOT_TEST]
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


def execution_events(path, kind="Q"):
    required = required_tests(kind)
    return required_execution_events(path, required, kind)


def export_command(kind="Q"):
    return [
        "go",
        "test",
        "-json",
        *build_flags(kind),
        "./tests/integration",
        "-run",
        f"^{ROOT_TEST}$/^active_export_grant_revocation$",
        "-count=1",
        "-timeout=4m",
    ]


def export_execution_events(path, kind="Q"):
    return required_execution_events(path, (ROOT_TEST, EXPORT_TEST), kind)


def required_execution_events(path, required, kind="Q", package=PACKAGE):
    proof_contract(kind)
    state = {name: [] for name in required}
    package_passes = 0
    native_markers = 0
    constructors = []
    marker = FIXTURE_MARKER if kind == "F" else NATIVE_MARKER
    wrong_marker = NATIVE_MARKER if kind == "F" else FIXTURE_MARKER
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
    if package == PACKAGE and constructors != [(str(i), "fresh", "true") for i in (1, 2, 3)]:
        raise ValueError("fresh per-owner constructor acquisition evidence missing or failed")
    return list(required)


def environment(root, env, kind="Q"):
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
    config = Path("/etc/ntp.conf").read_bytes() if kind == "Q" else b"server fixture.invalid\n"
    if not 0 < len(config) <= 4096:
        raise ValueError("native source configuration missing/oversize")
    return {
        "platform": platform.platform(),
        "machine": platform.machine(),
        "tools": tools,
        "go_settings": settings,
        "source_config_sha256": sha(config),
        "source_config_path": "/etc/ntp.conf" if kind == "Q" else "compile-time fixture",
        "environment": execution_environment(kind),
        "proof": proof_contract(kind),
        "native_environment": native_environment(),
        "native_premises": "Existing conditional native counter profile; Q additionally requires honest configured source/path. F synthetic UTC does not qualify real UTC or external reachability.",
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


def verify(root, evidence, expected_head, trusted_digest, kind="Q"):
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
    if kind == "F":
        export = receipt.get("export", {})
        if (
            export.get("command") != export_command(kind)
            or export.get("environment") != EXPORT_ENVIRONMENT
            or export.get("exit") != 0
        ):
            raise ValueError("mandatory active-export command/environment/success absent")
        if export.get("executed_tests") != export_execution_events(
            evidence / "export-events.jsonl", kind
        ):
            raise ValueError("active-export execution evidence mismatch")
    elif "export" in receipt or receipt.get("acquisition") != acquisition_counts(
        evidence / "events.jsonl"
    ):
        raise ValueError("single-cohort source acquisition evidence mismatch")
    if kind == "F":
        mechanism = receipt.get("mechanism", {})
        if mechanism.get("command") != mechanism_command() or mechanism.get("exit") != 0:
            raise ValueError("native loopback loss/recovery execution missing")
        if mechanism.get("executed_tests") != required_execution_events(
            evidence / "mechanism-events.jsonl", MECHANISM_TESTS, kind, MECHANISM_PACKAGE
        ):
            raise ValueError("native loopback execution evidence mismatch")
    return receipt


def run(root, evidence, expected_head=None, kind="Q"):
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
        if kind == "F":
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
        if kind == "Q":
            receipt["acquisition"] = acquisition_counts(evidence / "events.jsonl")
        else:
            # The SDK4 branch returns before the functional export case. Run only
            # that case in the same native environment, without repeating SDKs.
            receipt["stage"] = "active-export"
            export = {"command": export_command(kind), "environment": EXPORT_ENVIRONMENT}
            receipt["export"] = export
            with (
                (evidence / "export-events.jsonl").open("wb") as stdout,
                (evidence / "export-stderr.log").open("wb") as stderr,
            ):
                result = subprocess.run(
                    export_command(kind),
                    cwd=root,
                    env=env | EXPORT_ENVIRONMENT,
                    stdout=stdout,
                    stderr=stderr,
                    timeout=300,
                )
            export["exit"] = result.returncode
            if result.returncode:
                raise ValueError(f"native active-export command failed: exit {result.returncode}")
            export["executed_tests"] = export_execution_events(
                evidence / "export-events.jsonl", kind
            )
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
        item.add_argument("--proof-kind", choices=("F", "Q"), default="Q")
        if name == "run":
            item.add_argument("--expected-head")
            item.add_argument(
                "--fresh-child",
                action="store_true",
                help="retain a new output-only receipt for each local qualification",
            )
            item.add_argument(
                "--with-mechanism",
                action="store_true",
                help="run separate F evidence before Q; conservative full local gate",
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
            if args.with_mechanism:
                if args.proof_kind != "Q":
                    raise ValueError("--with-mechanism requires Q")
                run(
                    args.root.resolve(),
                    evidence / "F",
                    args.expected_head,
                    "F",
                )
                evidence = evidence / "Q"
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
        parser.exit(1, f"FAIL native SDK4 qualification: {error}\n")


if __name__ == "__main__":
    main()
