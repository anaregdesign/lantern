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
import shutil
import subprocess
import time
import uuid


ROOT_TEST = "TestCurrentSecurityPublicNativeGate"
SCOPED_TEST = "TestAuth_OIDCRustScopedChangesFacadeRealConnect"
PACKAGE = "github.com/anaregdesign/lantern/tests/integration"
SDK_TESTS = tuple(f"{ROOT_TEST}/{sdk}" for sdk in ("go", "bun", "dart", "cargo"))
ENVIRONMENT = {"LANTERN_CURRENT_PUBLIC_GATE": "1", "LANTERN_CURRENT_PUBLIC_SDK4": "1"}
NATIVE_MARKER = "production native constructor: independently provisioned three voters, two origins, observer; no injected clock or H"


def sha(data):
    return hashlib.sha256(data).hexdigest()


def encoded(value):
    return (json.dumps(value, sort_keys=True, indent=2) + "\n").encode()


def output(root, command, env=None):
    return subprocess.check_output(command, cwd=root, env=env, text=True,
                                   stderr=subprocess.STDOUT).strip()


def snapshot(root):
    if output(root, ["git", "status", "--porcelain"]):
        raise ValueError("qualification requires a clean, frozen checkout")
    rows = []
    for entry in subprocess.check_output(["git", "ls-files", "--stage", "-z"], cwd=root).split(b"\0"):
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
    return {"head": output(root, ["git", "rev-parse", "HEAD"]),
            "tree": output(root, ["git", "rev-parse", "HEAD^{tree}"]),
            "tracked_files": len(rows), "tracked_inputs_sha256": sha(encoded(rows))}


def command(include_scoped):
    names = [ROOT_TEST] + ([SCOPED_TEST] if include_scoped else [])
    return ["go", "test", "-json", "./tests/integration", "-run",
            "^(" + "|".join(names) + ")$", "-count=1", "-timeout=6m"]


def execution_events(path, include_scoped):
    required = (ROOT_TEST, *SDK_TESTS) + ((SCOPED_TEST,) if include_scoped else ())
    state = {name: [] for name in required}
    package_passes = 0
    native_markers = 0
    with path.open() as stream:
        for line in stream:
            event = json.loads(line)
            if event.get("Package") != PACKAGE:
                continue
            action, test = event.get("Action"), event.get("Test")
            if action in ("fail", "skip"):
                raise ValueError(f"required execution failed/skipped: {test or PACKAGE}")
            if test in state and action in ("run", "pass"):
                state[test].append(action)
            if action == "pass" and test is None:
                package_passes += 1
            if action == "output" and test == ROOT_TEST and NATIVE_MARKER in event.get("Output", ""):
                native_markers += 1
    if any(events != ["run", "pass"] for events in state.values()):
        raise ValueError(f"required root/SDK execution absent, duplicated or incomplete: {state}")
    if package_passes != 1 or native_markers != 1:
        raise ValueError("native constructor or successful package completion missing")
    return list(required)


def environment(root, env):
    if platform.system() not in ("Darwin", "Linux") or platform.machine() not in ("arm64", "aarch64", "x86_64"):
        raise ValueError("unsupported native execution platform")
    # No overlay, test-binary substitute or language loader can substitute inputs
    # outside the frozen checkout. The normal configured dependency caches remain.
    for name in ("GOFLAGS", "NODE_OPTIONS", "BUN_OPTIONS", "RUSTFLAGS", "RUSTC",
                 "CARGO_ENCODED_RUSTFLAGS", "RUSTC_WRAPPER", "RUSTC_WORKSPACE_WRAPPER",
                 "LANTERN_CURRENT_FIXTURE_EXPORTER"):
        if env.get(name):
            raise ValueError(f"unsupported qualification override: {name}")
    tools = {}
    for name, args in (("go", ["version"]), ("bun", ["--version"]),
                       ("dart", ["--version"]), ("cargo", ["--version"]), ("rustc", ["-vV"])):
        binary = shutil.which(name, path=env.get("PATH"))
        if binary is None:
            raise ValueError(f"required SDK tool missing: {name}")
        path = Path(binary).resolve()
        tools[name] = {"path": str(path), "sha256": sha(path.read_bytes()),
                       "version": output(root, [binary, *args], env)}
        selected = path
        if name in ("cargo", "rustc") and shutil.which("rustup", path=env.get("PATH")):
            selected = Path(output(root, ["rustup", "which", name], env)).resolve()
        elif name == "dart" and (path.parent / "cache/dart-sdk/bin/dart").is_file():
            selected = path.parent / "cache/dart-sdk/bin/dart"
        tools[name]["selected_path"] = str(selected)
        tools[name]["selected_sha256"] = sha(selected.read_bytes())
    settings = json.loads(output(root, ["go", "env", "-json", "GOVERSION", "GOOS", "GOARCH", "GOROOT", "GOWORK", "GOFLAGS"], env))
    if settings["GOFLAGS"] or Path(settings["GOWORK"]).resolve() != root / "go.work":
        raise ValueError("unsupported Go workspace/flags")
    native_goos = {"Darwin": "darwin", "Linux": "linux"}[platform.system()]
    native_goarch = "amd64" if platform.machine() == "x86_64" else "arm64"
    if (settings["GOOS"], settings["GOARCH"]) != (native_goos, native_goarch):
        raise ValueError("native qualification cannot cross-compile its test")
    compiler = Path(settings["GOROOT"]) / "bin/go"
    tools["go"]["selected_toolchain_sha256"] = sha(compiler.read_bytes())
    config = Path("/etc/ntp.conf").read_bytes()
    if not 0 < len(config) <= 4096:
        raise ValueError("native source configuration missing/oversize")
    return {"platform": platform.platform(), "machine": platform.machine(),
            "tools": tools, "go_settings": settings, "source_config_sha256": sha(config),
            "source_config_path": "/etc/ntp.conf", "environment": ENVIRONMENT,
            "native_premises": "Existing conditional native profile, honest configured source/path and bounded counter; no clock injection or physical-host qualification."}


def verify(root, evidence, expected_head, trusted_digest):
    # Only a trusted same-workflow job output supplies trusted_digest in CI.
    # A standalone artifact's own status/hash is not authority to accept it.
    data = (evidence / "receipt.json").read_bytes()
    if not trusted_digest or sha(data) != trusted_digest:
        raise ValueError("missing/untrusted or changed SDK4 receipt")
    receipt = json.loads(data)
    source = snapshot(root)
    if source["head"] != expected_head or receipt.get("source") != source:
        raise ValueError("SDK4 receipt belongs to another candidate/source")
    if receipt.get("schema") != 1 or receipt.get("status") != "PASS" or receipt.get("exit") != 0:
        raise ValueError("SDK4 receipt is not successful")
    include_scoped = receipt.get("include_scoped")
    if type(include_scoped) is not bool or receipt.get("command") != command(include_scoped):
        raise ValueError("SDK4 execution command mismatch")
    if receipt.get("runtime", {}).get("environment") != ENVIRONMENT:
        raise ValueError("mandatory SDK4 environment absent")
    runtime = receipt["runtime"]
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
    if set(receipt.get("logs", {})) != {"events.jsonl", "stderr.log"}:
        raise ValueError("required raw execution logs missing")
    for name, digest in receipt["logs"].items():
        if sha((evidence / name).read_bytes()) != digest:
            raise ValueError(f"SDK4 raw log mismatch: {name}")
    if receipt.get("executed_tests") != execution_events(evidence / "events.jsonl", include_scoped):
        raise ValueError("SDK4 execution evidence mismatch")
    return receipt


def run(root, evidence, include_scoped=False, expected_head=None):
    if evidence == root or root in evidence.parents:
        raise ValueError("evidence must be outside the checkout")
    evidence.mkdir(parents=True, exist_ok=False)
    receipt = {"schema": 1, "status": "FAIL", "started_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
               "include_scoped": include_scoped, "command": command(include_scoped)}
    try:
        source = snapshot(root)
        receipt.update(source=source, runner_sha256=sha(Path(__file__).read_bytes()))
        if expected_head is not None and source["head"] != expected_head:
            raise ValueError("SDK4 checkout is not the expected candidate")
        env = os.environ.copy()
        env.update(ENVIRONMENT, GOWORK=str(root / "go.work"))
        runtime = environment(root, env)
        receipt["runtime"] = runtime
        with (evidence / "events.jsonl").open("wb") as stdout, (evidence / "stderr.log").open("wb") as stderr:
            result = subprocess.run(command(include_scoped), cwd=root, env=env,
                                    stdout=stdout, stderr=stderr, timeout=600)
        receipt["exit"] = result.returncode
        if result.returncode:
            raise ValueError(f"native SDK4 command failed: exit {result.returncode}")
        receipt["executed_tests"] = execution_events(evidence / "events.jsonl", include_scoped)
        if snapshot(root) != source or environment(root, env) != runtime:
            raise ValueError("source/runtime configuration changed during SDK4 qualification")
        receipt["logs"] = {name: sha((evidence / name).read_bytes()) for name in ("events.jsonl", "stderr.log")}
        receipt["status"] = "PASS"
    except Exception as error:
        receipt["error"] = str(error)
    receipt["logs"] = {name: sha((evidence / name).read_bytes())
                       for name in ("events.jsonl", "stderr.log") if (evidence / name).is_file()}
    receipt["finished_utc"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    data = encoded(receipt)
    (evidence / "receipt.json").write_bytes(data)
    if receipt["status"] != "PASS":
        raise ValueError(receipt["error"])
    digest = sha(data)
    verify(root, evidence, source["head"], digest)
    print(f"PASS native SDK4 head={source['head']} tree={source['tree']} receipt_sha256={digest} evidence={evidence}", flush=True)
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
        if name == "run":
            item.add_argument("--include-scoped", action="store_true")
            item.add_argument("--expected-head")
            item.add_argument("--fresh-child", action="store_true", help="retain a new output-only receipt for each local qualification")
        else:
            item.add_argument("--expected-head", required=True)
            item.add_argument("--trusted-receipt-sha256", required=True)
    args = parser.parse_args()
    try:
        if args.action == "run":
            evidence = args.evidence.resolve()
            if args.fresh_child:
                evidence = evidence / str(uuid.uuid4())
            run(args.root.resolve(), evidence, args.include_scoped, args.expected_head)
        else:
            verify(args.root.resolve(), args.evidence.resolve(), args.expected_head, args.trusted_receipt_sha256)
    except Exception as error:
        parser.exit(1, f"FAIL native SDK4 qualification: {error}\n")


if __name__ == "__main__":
    main()
