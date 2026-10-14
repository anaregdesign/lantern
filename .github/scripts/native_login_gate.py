#!/usr/bin/env python3
"""Bounded isolated native-provider wire fixture; no external provider evidence.

Official provider names resolve only inside a new network-less container. TLS,
pinned destinations, the production Fetcher and current authority remain intact.
The compile-time controlled source is mechanism evidence, not configured UTC Q.
No image pull, host DNS/clock change, credential reuse or privileged container.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import time
import uuid


ROOT_TEST = "TestCurrentSecurityPublicNativeGate"
HOSTS = ("accounts.google.com", "oauth2.googleapis.com", "www.googleapis.com", "appleid.apple.com")
CASES = (
    "ambiguous_native_wire_rejected_before_provider_work",
    "google_repeated_restored_and_native_RPC",
    "provider_profile_and_existing_Principal/unknown",
    "provider_profile_and_existing_Principal/wrong_audience",
    "provider_profile_and_existing_Principal/bare_issuer",
    "provider_profile_and_existing_Principal/expired",
    "Apple_native_and_Android_callback/apple_ios",
    "Apple_native_and_Android_callback/apple_web",
    "Apple_response_loss_never_retries_code",
    "issued_response_loss_leaves_no_replay_or_credential_recovery",
    "concurrent_Apple_completion_issues_once",
    "native_logout_retained_status",
    "current_Deny_suspension_and_active_stream_withdrawal",
    "registration_Google_personal_data_and_account_isolation",
    "registration_personal_stream_closes_on_current_withdrawal",
    "registration_Apply_response_loss_and_fresh_original_recovery",
    "registration_Prepare_response_loss_never_dispatches",
    "registration_Apple_single_exchange/apple_ios",
    "registration_Apple_single_exchange/apple_web",
    "registration_legacy_enrollment_and_restrictive_policy",
)


def source_identity(root):
    """Hash actual tracked/untracked nonignored bytes, including dirty edits."""
    names = subprocess.check_output(["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"], cwd=root).split(b"\0")
    digest = hashlib.sha256()
    for name in sorted(set(names) - {b""}):
        path = root / os.fsdecode(name)
        value = os.fsencode(os.readlink(path)) if path.is_symlink() else path.read_bytes() if path.exists() else b"deleted"
        mode = b"link" if path.is_symlink() else b"file" if path.exists() else b"missing"
        digest.update(name + b"\0" + mode + b"\0" + hashlib.sha256(value).digest())
    return digest.hexdigest()


def require_cases(log):
    names = (ROOT_TEST,) + tuple(ROOT_TEST + "/" + case for case in CASES)
    if "--- SKIP:" in log or "--- FAIL:" in log or any(
            not re.search(r"(?m)^\s*--- PASS: " + re.escape(name) + r" \(", log) for name in names):
        raise RuntimeError("native wire cases did not all execute successfully")


def run(root, evidence):
    evidence.mkdir(parents=True, exist_ok=False)
    build = evidence / "build"
    build.mkdir()
    name = "lantern-native-login-" + uuid.uuid4().hex[:12]
    receipt = {"schema": 1, "status": "RUNNING", "proof": "controlled-source deterministic provider fixture; no real provider/device/UTC qualification", "started": time.time(), "commands": []}

    def save():
        (evidence / "receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")

    def command(args, timeout=180, env=None, check=True):
        index = len(receipt["commands"])
        log = evidence / f"{index:02d}.log"
        with log.open("wb") as output:
            result = subprocess.run(args, cwd=root, env=env, stdout=output, stderr=subprocess.STDOUT, timeout=timeout)
        receipt["commands"].append({"argv": args, "exit": result.returncode, "log": log.name, "sha256": hashlib.sha256(log.read_bytes()).hexdigest()})
        save()
        if check and result.returncode:
            raise RuntimeError(f"command {index} failed; see {log.name}")
        return result.returncode

    image_created = False
    try:
        receipt["head"] = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
        receipt["tree"] = subprocess.check_output(["git", "rev-parse", "HEAD^{tree}"], cwd=root, text=True).strip()
        receipt["development_changes"] = bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=root))
        receipt["source_sha256"] = source_identity(root)
        machine = platform.machine()
        arch = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64"}.get(machine)
        if arch is None:
            raise ValueError("unsupported fixture host architecture")
        env = {**os.environ, "GOOS": "linux", "GOARCH": arch, "CGO_ENABLED": "0"}
        for package, binary in (("./server/internal/security", "security.test"), ("./tests/integration", "integration.test")):
            command(["go", "test", "-c", "-tags=lantern_test_time", "-o", str(build / binary), package], env=env)
        (build / "Dockerfile").write_text('FROM scratch\nCOPY security.test /security.test\nCOPY integration.test /integration.test\nENTRYPOINT ["/integration.test"]\n')
        command(["docker", "build", "--pull=false", "--network=none", "--label", "lantern.task=1750-native-login", "-t", name, str(build)])
        image_created = True
        receipt["image"] = subprocess.check_output(["docker", "image", "inspect", "--format", "{{.Id}}", name], text=True).strip()
        receipt["binaries"] = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in build.glob("*.test")}
        args = ["docker", "run", "--rm", "--pull=never", "--name", name, "--label", "lantern.task=1750-native-login", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user", "65534:65534", "--pids-limit=256", "--memory=2g", "--cpus=4", "--sysctl", "net.ipv4.ip_unprivileged_port_start=0", "--tmpfs", "/tmp:rw,nosuid,nodev,size=512m,mode=1777"]
        for host in HOSTS:
            args += ["--add-host", host + ":127.0.0.1"]
        for key, value in {"LANTERN_CURRENT_PUBLIC_GATE": "1", "LANTERN_NATIVE_LOGIN_GATE": "1", "LANTERN_CURRENT_FIXTURE_EXPORTER": "/security.test"}.items():
            args += ["--env", key + "=" + value]
        args += [receipt["image"], "-test.v", "-test.run=^" + ROOT_TEST + "$", "-test.timeout=4m"]
        command(args, timeout=260)
        last_log = evidence / receipt["commands"][-1]["log"]
        require_cases(last_log.read_text())
        if source_identity(root) != receipt["source_sha256"] or subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip() != receipt["head"]:
            raise RuntimeError("source changed during native wire execution")
        receipt["cases"] = list(CASES)
        receipt["status"] = "PASS"
    except BaseException as error:
        receipt["status"], receipt["error"] = "FAIL", repr(error)
        raise
    finally:
        # Only this invocation's newly named container/image may be removed.
        if image_created:
            command(["docker", "rm", "--force", name], check=False)
            command(["docker", "image", "rm", name], check=False)
        receipt["finished"] = time.time()
        save()
    return receipt


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence", type=Path, required=True)
    parser.add_argument("--fresh-child", action="store_true",
                        help="retain a new child directory for this execution")
    args = parser.parse_args(argv)
    if args.fresh_child:
        args.evidence = args.evidence / str(uuid.uuid4())
    root = Path(__file__).resolve().parents[2]
    result = run(root, args.evidence.resolve())
    print(json.dumps({"status": result["status"], "receipt": str(args.evidence / "receipt.json"), "development_changes": result["development_changes"]}))


if __name__ == "__main__":
    main()
