#!/usr/bin/env python3
"""Bounded isolated native-provider wire fixture; no external provider evidence.

By default, provider names resolve only inside a new network-less container. TLS,
pinned destinations, the production Fetcher and current authority remain intact.
The compile-time controlled source supplies mechanism evidence; configured time-
source integration remains separate.
No image pull, host DNS/clock change, credential reuse or privileged container.

--serve is a separate normal-build synthetic-provider prerequisite, not the
ordinary gate or configured-source-integration/provider/device acceptance. It bridges actual NTP using
the existing read-only /etc/ntp.conf and publishes only one loopback TLS port.
Its new private/ directory contains ca.pem and fixture.json (schema 1: endpoint,
prefix, ca_file, serving_until, accounts.{a,b}.{access_token,expires_at}). Wait for
private/ready before reading it. BFF routes return each original access_token.
Create private/release as a mode-0600 regular file to join all owners early;
otherwise --lifetime-seconds stops serving. No destructive gate cases run here.

--serve-profile physical uses the same normal-build, finite current runtime but
exports rotating same-identity machine credentials and a separate minimal Head
writer with a certified six-hour receipt policy. It performs no native login and
cannot substitute for the default registered native A/B controller evidence.
"""

import argparse
from dataclasses import dataclass
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import time
import uuid
from urllib.parse import urlsplit


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
SERVING_CASE = ROOT_TEST + "/native_fixture_serving_export"
NORMAL_SOURCE_MARKER = "production native constructor: independently provisioned three voters, two origins, observer; no injected clock or H"


@dataclass(frozen=True)
class ServingOptions:
    origin: str
    listen_port: int
    publish_port: int
    lifetime_seconds: int = 540
    prefix: str = "orders:"
    profile: str = "native"

    def validate(self):
        if self.profile not in ("native", "physical") or self.profile == "physical" and self.prefix != "orders:":
            raise ValueError("explicit native or fixed-scope physical serving profile required")
        origin = urlsplit(self.origin)
        if (origin.scheme != "https" or not origin.hostname or origin.username is not None or
                origin.password is not None or origin.path or origin.query or origin.fragment or
                origin.netloc != origin.netloc.lower() or origin.geturl() != self.origin or
                origin.port is not None and not 1 <= origin.port <= 65535):
            raise ValueError("canonical HTTPS native service origin required")
        if (not self.prefix.startswith("orders:") or not self.prefix.endswith(":") or
                len(self.prefix.encode()) > 128 or any(c in self.prefix for c in " \t\r\n\0")):
            raise ValueError("bounded orders: fixture prefix required")
        if any(not 1024 <= port <= 65535 for port in (self.listen_port, self.publish_port)):
            raise ValueError("explicit unprivileged listener/published ports required")
        if not 15 <= self.lifetime_seconds <= 540:
            raise ValueError("serving lifetime must be 15..540 seconds")


def write_private(path, value):
    with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as output:
        output.write(value)


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


def require_serving(log, profile="native"):
    if profile not in ("native", "physical"):
        raise ValueError("unknown serving profile")
    case = SERVING_CASE if profile == "native" else ROOT_TEST + "/physical_fixture_serving_export"
    marker = "native" if profile == "native" else "physical"
    ready = "accounts=2" if profile == "native" else "observer_credentials=2 writer_credentials=1"
    other = "physical" if profile == "native" else "native"
    for name in (ROOT_TEST, case):
        if len(re.findall(r"(?m)^\s*--- PASS: " + re.escape(name) + r" \(", log)) != 1:
            raise RuntimeError("native serving lifecycle did not complete")
    if ("--- SKIP:" in log or "--- FAIL:" in log or
            log.count(NORMAL_SOURCE_MARKER) != 1 or "test-build controlled source:" in log or
            log.count(f"{marker}_fixture_ready schema=1 {ready}") != 1 or
            len(re.findall(marker + r"_fixture_stopped reason=(released|timeout)\b", log)) != 1 or
            f"{other}_fixture_ready" in log or f"/{other}_fixture_serving_export" in log or
            re.findall(r"current_constructor node=(\d+) mode=(\w+) passed=(\w+)", log) !=
            [(str(i), "fresh", "true") for i in (1, 2, 3)] or
            any(ROOT_TEST + "/" + case in log for case in CASES)):
        raise RuntimeError("native serving evidence missing, synthetic-time, destructive or failed")


def run(root, evidence, serving=None):
    if serving is not None:
        serving.validate()
        if os.getuid() == 0:
            raise ValueError("native serving requires a non-root private artifact owner")
        if evidence.resolve().is_relative_to(root.resolve()):
            raise ValueError("private native serving evidence must be outside the source checkout")
    missing = []
    parent = evidence.parent
    while not parent.exists():
        missing.append(parent)
        parent = parent.parent
    for parent in reversed(missing):
        parent.mkdir(mode=0o700)
    evidence.mkdir(mode=0o700, exist_ok=False)
    build = evidence / "build"
    build.mkdir(mode=0o700)
    private = evidence / "private"
    if serving is not None:
        private.mkdir(mode=0o700)
    name = "lantern-native-login-" + uuid.uuid4().hex[:12]
    receipt = {"schema": 1, "status": "RUNNING", "proof": "controlled-source deterministic provider fixture; no real provider/device/UTC qualification", "started": time.time(), "commands": []}
    if serving is not None:
        receipt.update({"mode": "serving", "build_tags": [], "synthetic_utc": False,
                        "serving_profile": serving.profile,
                        "proof": "normal-build configured-source " + serving.profile + " fixture serving lifecycle only; ordinary native gate/configured-source-integration/real provider/device acceptance NOT_RUN"})

    def save():
        write_private(evidence / "receipt.json", json.dumps(receipt, indent=2) + "\n")

    def command(args, timeout=180, env=None, check=True):
        index = len(receipt["commands"])
        log = evidence / f"{index:02d}.log"
        recorded = [arg.replace(serving.origin, "<private-service-origin>") for arg in args] if serving else args
        with os.fdopen(os.open(log, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as output:
            try:
                result = subprocess.run(args, cwd=root, env=env, stdout=output, stderr=subprocess.STDOUT, timeout=timeout)
            except subprocess.TimeoutExpired:
                receipt["commands"].append({"argv": recorded, "exit": None, "error": "timeout", "log": log.name, "sha256": hashlib.sha256(log.read_bytes()).hexdigest()})
                save()
                raise RuntimeError(f"command {index} timed out; see {log.name}") from None
        receipt["commands"].append({"argv": recorded, "exit": result.returncode, "log": log.name, "sha256": hashlib.sha256(log.read_bytes()).hexdigest()})
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
        if serving is not None:
            if env.get("GOFLAGS") or subprocess.check_output(["go", "env", "GOFLAGS"], cwd=root, env=env).strip():
                raise ValueError("normal-build native serving refuses GOFLAGS overrides")
            config = Path("/etc/ntp.conf").read_bytes()
            if not 0 < len(config) <= 4096:
                raise ValueError("existing configured native source missing/oversize")
            receipt["source_config_sha256"] = hashlib.sha256(config).hexdigest()
        for package, binary in (("./server/internal/security", "security.test"), ("./tests/integration", "integration.test")):
            command(["go", "test", "-c", *([] if serving else ["-tags=lantern_test_time"]), "-o", str(build / binary), package], env=env)
        # Private host umask must not prevent the non-root container user from
        # executing these public-code programs. Runtime state stays private.
        write_private(build / "Dockerfile", 'FROM scratch\nCOPY --chmod=0555 security.test /security.test\nCOPY --chmod=0555 integration.test /integration.test\nENTRYPOINT ["/integration.test"]\n')
        label = "lantern.task=1809-native-serving" if serving else "lantern.task=1750-native-login"
        command(["docker", "build", "--pull=false", "--network=none", "--label", label, "-t", name, str(build)])
        image_created = True
        receipt["image"] = subprocess.check_output(["docker", "image", "inspect", "--format", "{{.Id}}", name], text=True).strip()
        receipt["binaries"] = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in build.glob("*.test")}
        args = ["docker", "run", "--rm", "--pull=never", "--name", name, "--label", label, "--network=bridge" if serving else "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user", f"{os.getuid()}:{os.getgid()}" if serving else "65534:65534", "--pids-limit=256", "--memory=2g", "--cpus=4", "--sysctl", "net.ipv4.ip_unprivileged_port_start=0", "--tmpfs", ("/work" if serving else "/tmp") + ":rw,nosuid,nodev,size=512m,mode=1777"]
        if serving is not None:
            args += ["--mount", "type=bind,source=/etc/ntp.conf,target=/etc/ntp.conf,readonly",
                     "--mount", f"type=bind,source={private},target=/native-fixture",
                     "--publish", f"127.0.0.1:{serving.publish_port}:{serving.listen_port}/tcp"]
        for host in HOSTS:
            args += ["--add-host", host + ":127.0.0.1"]
        environment = {"LANTERN_CURRENT_PUBLIC_GATE": "1", "LANTERN_NATIVE_LOGIN_GATE": "1", "LANTERN_CURRENT_FIXTURE_EXPORTER": "/security.test"}
        if serving is not None:
            environment.update({"TMPDIR": "/work", "LANTERN_NATIVE_FIXTURE_SERVE": "1",
                                "LANTERN_NATIVE_FIXTURE_ORIGIN": serving.origin,
                                "LANTERN_NATIVE_FIXTURE_PREFIX": serving.prefix,
                                "LANTERN_NATIVE_FIXTURE_LISTEN_PORT": str(serving.listen_port),
                                "LANTERN_NATIVE_FIXTURE_LIFETIME_SECONDS": str(serving.lifetime_seconds),
                                "LANTERN_NATIVE_FIXTURE_EXPORT_DIR": "/native-fixture"})
            if serving.profile == "physical":
                environment["LANTERN_NATIVE_FIXTURE_PROFILE"] = "physical"
        for key, value in environment.items():
            args += ["--env", key + "=" + value]
        timeout = serving.lifetime_seconds + 300 if serving else 240
        args += [receipt["image"], "-test.v", "-test.run=^" + ROOT_TEST + "$", f"-test.timeout={timeout}s" if serving else "-test.timeout=4m"]
        command(args, timeout=timeout + 20)
        last_log = evidence / receipt["commands"][-1]["log"]
        if serving is not None:
            require_serving(last_log.read_text(), serving.profile)
        else:
            require_cases(last_log.read_text())
        if source_identity(root) != receipt["source_sha256"] or subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip() != receipt["head"]:
            raise RuntimeError("source changed during native wire execution")
        receipt["cases"] = [SERVING_CASE if serving.profile == "native" else ROOT_TEST + "/physical_fixture_serving_export"] if serving else list(CASES)
        receipt["status"] = "PASS"
    except BaseException as error:
        receipt["status"], receipt["error"] = "FAIL", repr(error).replace(serving.origin, "<private-service-origin>") if serving else repr(error)
        raise
    finally:
        # Only this invocation's newly named container/image may be removed.
        if image_created:
            command(["docker", "rm", "--force", name], check=False)
            command(["docker", "image", "rm", name], check=False)
        if serving is not None:
            (private / "ready").unlink(missing_ok=True)
        receipt["finished"] = time.time()
        save()
    return receipt


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence", type=Path, required=True)
    parser.add_argument("--fresh-child", action="store_true",
                        help="retain a new child directory for this execution")
    parser.add_argument("--serve", action="store_true", help="normal-build synthetic native serving/export only")
    parser.add_argument("--service-origin", help="exact HTTPS origin used by the consuming client")
    parser.add_argument("--listen-port", type=int, help="selected container TLS port")
    parser.add_argument("--publish-port", type=int, help="publish only on this host loopback port")
    parser.add_argument("--lifetime-seconds", type=int, help="finite 15..540 second serving window (default 540; signed membership must cover it)")
    parser.add_argument("--prefix", help="explicit orders: test prefix (default orders:)")
    parser.add_argument("--serve-profile", choices=("native", "physical"),
                        help="native registered A/B (default), or scoped rotating machine/Head receipt fixture")
    args = parser.parse_args(argv)
    serving = None
    if args.serve:
        if args.service_origin is None or args.listen_port is None or args.publish_port is None:
            parser.error("--serve requires --service-origin, --listen-port and --publish-port")
        serving = ServingOptions(args.service_origin, args.listen_port, args.publish_port,
                                 args.lifetime_seconds if args.lifetime_seconds is not None else 540,
                                 args.prefix if args.prefix is not None else "orders:",
                                 args.serve_profile if args.serve_profile is not None else "native")
        try:
            serving.validate()
        except ValueError as error:
            parser.error(str(error))
    elif any(value is not None for value in (args.service_origin, args.listen_port, args.publish_port, args.lifetime_seconds, args.prefix, args.serve_profile)):
        parser.error("serving options require --serve")
    if args.fresh_child:
        args.evidence = args.evidence / str(uuid.uuid4())
    root = Path(__file__).resolve().parents[2]
    result = run(root, args.evidence.resolve(), serving)
    print(json.dumps({"status": result["status"], "receipt": str(args.evidence / "receipt.json"), "development_changes": result["development_changes"]}))


if __name__ == "__main__":
    main()
