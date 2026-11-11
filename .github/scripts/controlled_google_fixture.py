#!/usr/bin/env python3
"""One explicit real-Google component cohort with test-only controlled UTC.

Default is PLAN_ONLY. This profile exports no credentials and qualifies neither
real UTC nor a full provider case. Normal/synthetic fixture readers reject it.
The operator supplies reviewed exact-source builds, public roots, verified seed
provenance and a fresh owner-present dispatch; no credential is derived here.
"""

import argparse
from datetime import datetime, timezone
import fcntl
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import shutil
import signal
import stat
import subprocess
import time

import native_login_gate
from physical_fixture_provenance import canonical, decode, digest, encoded, read, require, utc


PROFILE = "controlled-real-google-component"
KIND = "controlled-real-google-component-serving"
ISSUER = "https://accounts.google.com"
HEX40 = re.compile(r"[0-9a-f]{40}\Z")
HEX64 = re.compile(r"[0-9a-f]{64}\Z")
CLIENT = re.compile(r"([0-9]{6,20})-[a-z0-9]+\.apps\.googleusercontent\.com\Z")
ROOT = Path(__file__).resolve().parents[2]
CHILD = "TestControlledGoogleNativeChild"
SOURCE_FIELDS = ("head", "tree", "source_sha256")


def instant(value):
    return datetime.fromtimestamp(value, timezone.utc).isoformat().replace("+00:00", "Z")


def reference(value, *, maximum=4 << 20):
    require(isinstance(value, dict) and set(value) == {"path", "sha256"}
            and isinstance(value["sha256"], str) and HEX64.fullmatch(value["sha256"]), "resolved immutable reference required")
    raw = read(value["path"], maximum=maximum)
    require(digest(raw) == value["sha256"], "immutable reference changed")
    return raw


def input_snapshot(plan, extra_refs=()):
    """Hash the actual admitted inputs again; native state is deliberately separate."""
    refs = [plan[section][key] for section, keys in (
        ("source", ("proof", "review")), ("build", ("receipt", "description", "public_roots")),
        ("seed", ("material", "verification")), ("public", ("certificate", "key")),
        ("network", ("admission",))) for key in keys]
    refs += decode(reference(plan["seed"]["verification"]))["basis_refs"] + list(extra_refs)
    return {ref["path"]: digest(reference(ref)) for ref in refs}


def source_snapshot(plan, window, *, cleanup=False):
    actual = {}
    for name, args in (("head", ["rev-parse", "HEAD"]), ("tree", ["rev-parse", "HEAD^{tree}"]),
                       ("clean", ["status", "--porcelain", "--untracked-files=all"])):
        actual[name] = subprocess.check_output(["git", "--no-optional-locks", "-C", str(ROOT), *args],
                                               timeout=window.command_timeout(20, cleanup), text=True).strip()
    actual["source_sha256"] = native_login_gate.source_identity(ROOT, timeout=window.command_timeout(20, cleanup))
    require(actual == {**{key: plan["source"][key] for key in SOURCE_FIELDS}, "clean": ""}, "clean exact source required")
    require(window.left(cleanup=cleanup, setup=not cleanup) > 0, "source fence exceeded absolute budget")
    return actual


def material_snapshot(output):
    """Only immutable intent/custody/views, never the three mutable native states."""
    result = {}
    for directory in ("inputs", "material", "operator", "views"):
        root = output / directory
        for path in [root, *sorted(root.rglob("*"))]:
            info = path.lstat()
            require(info.st_uid == os.getuid() and not info.st_mode & 0o022
                    and (stat.S_ISDIR(info.st_mode) or stat.S_ISREG(info.st_mode)), "owned immutable material required")
            name = str(path.relative_to(output))
            if stat.S_ISREG(info.st_mode):
                require(info.st_nlink == 1 and info.st_size <= 4 << 20, "bounded unique immutable material required")
                result[name] = {"sha256": digest(read(path, maximum=4 << 20)), "mode": stat.S_IMODE(info.st_mode)}
            else:
                result[name] = {"directory": True, "mode": stat.S_IMODE(info.st_mode)}
    return result


def resolved(plan):
    """Pure first fence: unresolved source/ref pins never reach any other input."""
    require(isinstance(plan, dict) and type(plan.get("schema")) is int and plan["schema"] == 1 and plan.get("kind") == "controlled-real-google-plan", "explicit real-Google plan required")
    source = plan.get("source", {})
    require(HEX40.fullmatch(source.get("head", "")) and HEX40.fullmatch(source.get("tree", ""))
            and HEX64.fullmatch(source.get("source_sha256", "")), "final source pins pending")
    require(set(plan) == {"schema", "kind", "source", "build", "seed", "clients", "profile_ids", "public", "network",
                          "output", "cohort", "lifetime_seconds", "planned_stop_at", "cleanup_by"}, "unknown profile input refused")
    require(type(plan["lifetime_seconds"]) is int and 15 <= plan["lifetime_seconds"] <= 540
            and utc(plan["cleanup_by"]) - utc(plan["planned_stop_at"]) == 20, "fixed bounded stop/cleanup required")
    require(HEX64.fullmatch(plan["cohort"]), "new cohort nonce required")
    require(set(plan["clients"]) == {"web", "android", "ios"}
            and all(CLIENT.fullmatch(v) for v in plan["clients"].values()) and len(set(plan["clients"].values())) == 3,
            "dedicated exact Google clients required")
    require(len({CLIENT.fullmatch(v).group(1) for v in plan["clients"].values()}) == 1, "Google clients must share the operator-selected project")
    require(set(plan["profile_ids"]) == {"android", "ios"}
            and len(set(plan["profile_ids"].values())) == 2
            and all(re.fullmatch(r"[a-z][a-z0-9_-]{0,63}", v) for v in plan["profile_ids"].values()), "two exact native profile IDs required")
    public = plan["public"]
    require(set(public) == {"origin", "lan_ip", "certificate", "key"}, "exact public TLS input required")
    # The production parser revalidates this origin; the profile additionally
    # keeps the distinct approved temporary port and a specific LAN binding.
    from urllib.parse import urlsplit
    origin = urlsplit(public["origin"])
    require(origin.scheme == "https" and origin.hostname and origin.port == 24445 and not origin.username
            and not origin.password and not origin.path and not origin.query and not origin.fragment
            and origin.geturl() == public["origin"], "distinct canonical HTTPS port24445 required")
    address = ipaddress.ip_address(public["lan_ip"])
    require(address.version == 4 and address.is_private and not address.is_loopback and not address.is_unspecified,
            "explicit approved Mac LAN IPv4 required")
    require(set(plan["network"]) == {"id", "name", "admission"} and HEX64.fullmatch(plan["network"]["id"]), "new dedicated bridge identity pending")
    require(set(plan["build"]) == {"image", "receipt", "description", "public_roots"}
            and re.fullmatch(r"sha256:[0-9a-f]{64}", plan["build"]["image"]), "immutable controlled image pending")
    require(set(plan["seed"]) == {"material", "verification"}, "verified retained seed references required")
    require(set(source) == {"head", "tree", "source_sha256", "proof", "review"}, "exact main source admission required")
    # Validate reference shapes without reading them. No fallback file or old
    # runtime input can repair a PENDING reference.
    for value in (source["proof"], source["review"], plan["build"]["receipt"], plan["build"]["description"],
                  plan["build"]["public_roots"], plan["seed"]["material"], plan["seed"]["verification"],
                  public["certificate"], public["key"], plan["network"]["admission"]):
        require(set(value) == {"path", "sha256"} and HEX64.fullmatch(value["sha256"])
                and Path(value["path"]).is_absolute(), "profile input reference pending")
    require(not any(c in str(plan["output"]) for c in ",\r\n\0"), "unambiguous new private output path required")
    canonical(plan["output"])
    return plan


def admitted_inputs(plan):
    resolved(plan)
    source = plan["source"]
    proof, review = (decode(reference(source[key])) for key in ("proof", "review"))
    require(proof.get("schema") == 1 and proof.get("status") == "PASS" and proof.get("selected_ci_verified") is True
            and proof.get("required_configured_source_verified") is True, "new exact-main qualification required")
    require(proof.get("source_head") == source["head"] and proof.get("source_tree") == source["tree"], "exact main source aliases required")
    require(review.get("status") == "APPROVE", "Root exact-source acceptance required")
    for record in (proof, review):
        require(record.get("head") == source["head"] and record.get("tree") == source["tree"]
                and record.get("source_sha256", record.get("sourceSha256")) == source["source_sha256"], "source admission mismatch")
    build, description = (decode(reference(plan["build"][key])) for key in ("receipt", "description"))
    require(build.get("schema") == 1 and build.get("kind") == "controlled-real-google-build" and build.get("status") == "PASS"
            and all(build.get(k) == source[k] for k in SOURCE_FIELDS) and build.get("image") == plan["build"]["image"]
            and build.get("build_tags") == ["lantern_test_time"], "new exact controlled build required")
    require(description.get("kind") == "controlled-real-google-profile-description" and description.get("revision") == source["head"]
            and description.get("provider_source") == "real-google" and description.get("synthetic_utc") is True
            and description.get("build_tags") == ["lantern_test_time"] and HEX64.fullmatch(description.get("roles_sha256", ""))
            and build.get("roles_sha256") == description["roles_sha256"], "same-binary default Role description required")
    require(HEX64.fullmatch(build.get("binary_sha256", "")) and description.get("binary_sha256") == build["binary_sha256"], "same compiled binary binding required")
    compile_argv = build.get("compile_argv", [])
    require(len(compile_argv) >= 8 and compile_argv[:4] == ["go", "test", "-c", "-tags=lantern_test_time"]
            and compile_argv[-1] == "./server/cmd" and "-X github.com/anaregdesign/lantern/server/cmd.controlledGoogleRevision=" + source["head"] in compile_argv,
            "actual controlled compile argv required")
    roots = reference(plan["build"]["public_roots"])
    require(b"-----BEGIN CERTIFICATE-----" in roots and build.get("public_roots_sha256") == digest(roots)
            and description.get("public_roots_sha256") == digest(roots), "ordinary public-root bundle mismatch")
    material = decode(reference(plan["seed"]["material"]))
    verified = decode(reference(plan["seed"]["verification"]))
    require(verified.get("status") == "APPROVE_VERIFIED_RETAINED_SEED" and verified.get("provider_ownership_verified") is True
            and verified.get("material_sha256") == plan["seed"]["material"]["sha256"]
            and verified.get("subject_source") == "retained-material.initial_admin" and verified.get("basis_refs"),
            "verified seed ownership remains a setup prerequisite")
    for ref in verified["basis_refs"]:
        reference(ref)
    seed = material.get("initial_admin")
    require(isinstance(seed, dict) and set(seed) == {"issuer", "subject"} and seed["issuer"] == ISSUER
            and isinstance(seed["subject"], str) and 0 < len(seed["subject"]) <= 255, "exact retained seed identity required")
    network = decode(reference(plan["network"]["admission"]))
    require(network.get("status") == "APPROVE_EMPTY_DEDICATED_BRIDGE" and network.get("id") == plan["network"]["id"]
            and network.get("name") == plan["network"]["name"] and network.get("cohort") == plan["cohort"], "fresh dedicated bridge admission required")
    return {"seed": seed, "roles_sha256": description["roles_sha256"],
            "certificate": reference(plan["public"]["certificate"]).decode(), "key": reference(plan["public"]["key"]).decode()}


def configurations(plan, admitted):
    """Private intent only. Provider.ProvisionSecurity remains the sole genesis."""
    clients = plan["clients"]
    prefix = "cg-" + plan["cohort"][:16]
    nodes = [{"name": f"node-{i}", "control_origin": f"https://{prefix}-{i}:6381", "data_origin": f"https://{prefix}-{i}:6382",
              "control_listen": "0.0.0.0:6381", "data_listen": "0.0.0.0:6382"} for i in (1, 2, 3)]
    issuer = {"issuer": ISSUER, "enabled": True, "client_id": clients["web"], "algorithms": ["RS256"],
              "native_clients": [{"id": plan["profile_ids"][p], "kind": "google_id_token", "client_id": clients["web"],
                                  "presenters": [clients[p]]} for p in ("android", "ios")]}
    base = {"public_origin": plan["public"]["origin"], "material_root": "/material/identity", "persistence_root": "/state",
            "target_platform": "linux", "tls_termination": "server", "initial_admin": admitted["seed"], "nodes": nodes,
            "issuers": [issuer], "tls": {"certificate_pem": admitted["certificate"], "private_key_pem": admitted["key"]}}
    return [{**base, "node": f"node-{i}"} for i in (1, 2, 3)]


def child_input(plan, config, admitted, mode):
    return {"schema": 1, "profile": PROFILE, "mode": mode, "nonce": plan["cohort"],
            "config_sha256": digest(json.dumps(config, separators=(",", ":")).encode()),
            "seed_identity_sha256": digest((ISSUER + "\0" + admitted["seed"]["subject"]).encode()),
            "roles_sha256": admitted["roles_sha256"], "service_origin": plan["public"]["origin"],
            "web_client_id": plan["clients"]["web"], "android_client_id": plan["clients"]["android"], "ios_client_id": plan["clients"]["ios"],
            "profile_ids": [plan["profile_ids"][p] for p in ("android", "ios")], "lifetime_seconds": plan["lifetime_seconds"],
            "planned_stop_at": plan["planned_stop_at"], "cleanup_by": plan["cleanup_by"]}


def new_file(path, raw):
    raw = raw if isinstance(raw, bytes) else encoded(raw)
    with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600), "wb") as output:
        output.write(raw)
        output.flush()
        os.fsync(output.fileno())


def descriptor(plan, plan_sha256, started, ready, authority_profile):
    require(re.fullmatch(r"current-v2:[0-9a-f]{64}", authority_profile), "original signed cohort profile required")
    return {"schema": 1, "kind": KIND, "profile": PROFILE, "provider_source": "real-google", "time_source": "controlled-source",
            "synthetic_utc": True, "build_kind": "test-only", "build_tags": ["lantern_test_time"],
            "head": plan["source"]["head"], "tree": plan["source"]["tree"], "source_sha256": plan["source"]["source_sha256"],
            "plan_sha256": plan_sha256, "cohort": plan["cohort"], "authority_profile": authority_profile, "image": plan["build"]["image"],
            "serving_from": instant(ready), "serving_until": plan["planned_stop_at"], "setup_from": instant(started),
            "cleanup_by": plan["cleanup_by"], "route_classes": ["compiled-loopback-udp-time", "production-public-https-provider", "own-public-tls-health"],
            "registration_enabled": False, "uid_allocation_claim": False, "provider_components_passed": 0,
            "full_provider_cells_passed": 0, "signup_passed": 0, "normal_qualification_claim": False, "real_utc_claim": False,
            "input_refs": {"source": plan["source"], "build": plan["build"], "seed": plan["seed"], "public": {
                "certificate": plan["public"]["certificate"], "key": plan["public"]["key"]}, "network": plan["network"]}}


def validate_descriptor(value, plan, plan_sha256, authority_profile):
    expected = descriptor(plan, plan_sha256, utc(value["setup_from"]), utc(value["serving_from"]), authority_profile)
    require(encoded(value) == encoded(expected), "real-Google controlled descriptor mismatch or extra credential field")
    duration = utc(value["serving_until"]) - utc(value["serving_from"])
    require(15 <= duration <= plan["lifetime_seconds"] <= 540, "actual finite serving interval required")
    return value


def container_argv(plan, name, mounts, env_file, *, network="none", alias=None, publish=False):
    args = ["docker", "--context", "desktop-linux", "create", "--pull=never", "--name", name,
            "--label", "lantern.task=2138-controlled-real-google", "--label", "lantern.cohort=" + plan["cohort"],
            "--network", network, "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
            "--user", f"{os.getuid()}:{os.getgid()}", "--pids-limit=128", "--memory=768m", "--cpus=1",
            "--log-driver=json-file", "--log-opt=max-size=2m", "--log-opt=max-file=1", "--tmpfs", "/tmp:rw,nosuid,nodev,size=64m,mode=1777"]
    if alias:
        args += ["--network-alias", alias]
    for source, target, writable in mounts:
        args += ["--mount", f"type=bind,source={source},target={target}" + ("" if writable else ",readonly")]
    if publish:
        args += ["--publish", f"{plan['public']['lan_ip']}:24445:6380/tcp"]
    return args + ["--env-file", str(env_file), plan["build"]["image"]]


def counters(value):
    return (isinstance(value, dict) and set(value) == {"attempts", "writes", "installations", "in_flight", "failed", "denied"}
            and all(type(value[k]) is int for k in ("attempts", "writes", "installations"))
            and 0 < value["installations"] <= value["writes"] <= value["attempts"]
            and all(value[k] is False for k in ("in_flight", "failed", "denied")))


def child_records(raw, plan, mode, node):
    records = []
    traces = []
    for line in raw.splitlines():
        if not line.startswith(b"{"):
            continue
        value = decode(line)
        if value.get("kind") == "controlled_public_trace":
            traces.append(value)
        elif value.get("profile") == PROFILE:
            require(value.get("nonce") == plan["cohort"] and value.get("revision") == plan["source"]["head"]
                    and value.get("mode") == mode and type(value.get("pid")) is int and value["pid"] > 0,
                    "actual child identity binding mismatch")
            require(value.get("node") == node and HEX64.fullmatch(value.get("config_sha256", "")), "actual selected node/config binding required")
            require(re.fullmatch(r"current-v2:[0-9a-f]{64}", value.get("authority_profile", "")), "actual signed cohort identity required")
            records.append(value)
    return records, traces


def validate_child(raw, plan, mode, node, config_sha256=None):
    records, traces = child_records(raw, plan, mode, node)
    require(len({r["config_sha256"] for r in records}) == 1, "child configuration drift refused")
    require(config_sha256 is None or records[0]["config_sha256"] == config_sha256, "child exact configured intent mismatch")
    require(len({r["authority_profile"] for r in records}) == 1, "child original cohort drift refused")
    if mode == "initialize":
        require(len(records) == 1 and records[0].get("event") == "initialized" and records[0].get("ok") is True
                and counters(records[0].get("counters")) and "repeat" not in records[0] and not traces,
                "actual initializer terminal counters required")
    else:
        require([r.get("event") for r in records] == ["constructed", "ready", "joined"]
                and len({r["pid"] for r in records}) == 1, "same owned App lifecycle required")
        before, _, after = records
        require(counters(before.get("counters")) and counters(after.get("counters")) and after.get("repeat") == after["counters"]
                and after.get("ok") is True and all(after["counters"][k] >= before["counters"][k] for k in ("attempts", "writes", "installations")),
                "settled constructor and stable joined counters required")
        require(len(traces) == 1 and traces[0].get("kind") == "controlled_public_trace" and type(traces[0].get("dropped")) is int
                and traces[0]["dropped"] == 0 and isinstance(traces[0].get("events"), list), "complete bounded terminal decision capture required")
        require(utc(records[1]["ready_at"]) < utc(after["stopped_at"]) <= utc(plan["cleanup_by"]), "actual serving/stop interval refused")
    first = records[0]
    require(utc(first["host_seed_interval_from"]) <= utc(first["host_seed_interval_until"]), "true enclosing host seed interval required")
    return records


class Window:
    """Convert the already fixed absolute endpoints once; wall changes cannot extend them."""
    def __init__(self, plan, wall=None, monotonic=None):
        self.wall, self.monotonic = wall or time.time, monotonic or time.monotonic
        self.started_wall, self.started_mono = self.wall(), self.monotonic()
        remaining = utc(plan["planned_stop_at"]) - self.started_wall
        require(15 <= remaining <= plan["lifetime_seconds"] <= 540, "fixed profile window unavailable")
        self.stop = self.started_mono + remaining
        self.cleanup = self.started_mono + utc(plan["cleanup_by"]) - self.started_wall

    def left(self, *, cleanup=False, setup=False):
        end = self.cleanup if cleanup else self.stop - (15 if setup else 0)
        return end - self.monotonic()

    def command_timeout(self, requested, cleanup=False):
        remaining = self.left(cleanup=cleanup, setup=not cleanup)
        require(remaining > 0, "absolute profile command budget exhausted")
        return min(requested, remaining)


class Commands:
    """Bounded safe argv and private logs; configuration values never enter argv."""
    def __init__(self, output, window):
        self.output, self.rows = output, []
        self.window = window
        self.cleanup = False

    def run(self, args, timeout=20, check=True):
        require(len(self.rows) < (256 if self.cleanup else 224), "bounded profile command budget exhausted")
        timeout = self.window.command_timeout(timeout, self.cleanup)
        path = self.output / f"command-{len(self.rows):03d}.log"
        start = time.monotonic()
        with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600), "wb") as log:
            try:
                done = subprocess.run(args, cwd=ROOT, stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT, timeout=timeout)
                exit_code = done.returncode
            except subprocess.TimeoutExpired:
                exit_code = None
        raw = path.read_bytes()
        self.rows.append({"argv": args, "exit": exit_code, "timeout_seconds": timeout, "cleanup_phase": self.cleanup,
                          "seconds": time.monotonic() - start, "log": path.name, "sha256": digest(raw)})
        require(len(raw) <= 8 << 20, "bounded profile log exceeded")
        require(not check or exit_code == 0, "owned profile command failed")
        return raw, exit_code


def approved_dispatch(plan, plan_sha256, approval):
    require(approval.get("status") == "APPROVE" and approval.get("kind") == "controlled-google-owner-dispatch"
            and approval.get("plan_sha256") == plan_sha256 and approval.get("owner_present") is True,
            "reviewed owner-present single-window dispatch required")
    require(approval.get("seed_verification_sha256") == plan["seed"]["verification"]["sha256"], "Root verified seed selection required")


def run(plan, plan_sha256, approval, approval_sha256, dispatch_refs=()):
    """The single actual window. Its operator approval is separate from plan hashing."""
    resolved(plan)
    approved_dispatch(plan, plan_sha256, approval)
    admitted = admitted_inputs(plan)
    window = Window(plan)
    source_before = source_snapshot(plan, window)
    inputs_before = input_snapshot(plan, dispatch_refs)
    output = canonical(plan["output"])
    output.mkdir(mode=0o700)
    commands = Commands(output, window)
    names, all_ids, serving = [], [], []
    started, ready_at = time.time(), None
    authority_profile = None
    material_before = None
    result = {"schema": 1, "kind": KIND + "-lifecycle", "status": "FAIL", "plan_sha256": plan_sha256,
              "cohort": plan["cohort"], "head": plan["source"]["head"], "driver_sent_sigkill": False, "retry": False}
    new_file(output / "STARTED.json", {"schema": 1, "kind": "controlled-real-google-start", "status": "SETUP",
             "driver_pid": os.getpid(), "source": plan["source"], "cohort": plan["cohort"], "image": plan["build"]["image"],
             "plan_sha256": plan_sha256, "root_dispatch_sha256": approval_sha256,
             "started_at": instant(started), "planned_stop_at": plan["planned_stop_at"], "cleanup_by": plan["cleanup_by"]})
    interrupted = set()
    previous = {n: signal.getsignal(n) for n in (signal.SIGINT, signal.SIGTERM)}
    for number in previous:
        signal.signal(number, lambda n, _f: interrupted.add(n))
    docker = ["docker", "--context", "desktop-linux"]
    cleanup_known = False
    try:
        require(commands.run(docker + ["image", "inspect", "--format", "{{.Id}}", plan["build"]["image"]])[0].decode().strip() == plan["build"]["image"], "immutable image missing")
        # Only these public image metadata fields are observed. Never dump an
        # image Env value or serving-container Env (which carries private TLS).
        image_fields = decode(commands.run(docker + ["image", "inspect", "--format",
                    '{"entrypoint":{{json .Config.Entrypoint}},"cmd":{{json .Config.Cmd}},"env_count":{{len .Config.Env}}}', plan["build"]["image"]])[0])
        require(set(image_fields) == {"entrypoint", "cmd", "env_count"} and image_fields["entrypoint"] == ["/controlled-google.test"]
                and image_fields["cmd"] in (None, []) and type(image_fields["env_count"]) is int and image_fields["env_count"] == 0,
                "exact Scratch entrypoint/empty inherited environment required")
        require(commands.run(docker + ["network", "inspect", "--format", "{{.Id}}", plan["network"]["id"]])[0].decode().strip() == plan["network"]["id"], "dedicated bridge changed")
        require(commands.run(docker + ["network", "inspect", "--format", "{{.Name}}", plan["network"]["id"]])[0].decode().strip() == plan["network"]["name"], "dedicated bridge name changed")
        require(decode(commands.run(docker + ["network", "inspect", "--format", "{{json .Containers}}", plan["network"]["id"]])[0]) == {}, "dedicated bridge must be empty")
        for name in ("material", "operator", "state", "inputs", "views"):
            (output / name).mkdir(mode=0o700)
        configs = configurations(plan, admitted)
        def environment(index, mode, label=None):
            config_raw = json.dumps(configs[index], separators=(",", ":"))
            child = child_input(plan, configs[index], admitted, mode)
            child_path = output / "inputs" / f"{index}-{label or mode}.json"
            new_file(child_path, child)
            values = {"LANTERN_AUTH_MODE": "oidc", "LANTERN_SECURITY_CONFIG_JSON": config_raw, "LANTERN_PORT": "6380",
                      "LANTERN_METRICS_ADDR": "127.0.0.1:9090", "LANTERN_BACKUP_ENABLED": "false", "LANTERN_BACKUP_RESTORE_ON_START": "false",
                      "LANTERN_DRAIN_DELAY_SECONDS": "0", "LANTERN_LOG_LEVEL": "warn", "CONTROLLED_GOOGLE_CHILD_INPUT": "/inputs/" + child_path.name,
                      "CONTROLLED_GOOGLE_CHILD_INPUT_SHA256": digest(child_path.read_bytes())}
            path = output / "inputs" / f"{index}-{label or mode}.env"
            new_file(path, ("\n".join(k + "=" + v for k, v in values.items()) + "\n").encode())
            return path
        def create(stage, index, mounts, env_file, tail, network="none", publish=False):
            require(not interrupted and window.left(setup=True) > 0, "no late or interrupted profile dispatch")
            name = f"cg-{plan['cohort'][:16]}-{stage}-{index}"
            args = container_argv(plan, name, mounts, env_file, network=network,
                                  alias=f"cg-{plan['cohort'][:16]}-{index + 1}" if stage == "resume" else None, publish=publish) + tail
            names.append(name) # Bind possible create-time ambiguity before dispatch.
            new_file(output / f"create-attempt-{len(names):02d}.json", {"name": name, "argv": args, "create_attempted": True})
            raw, _ = commands.run(args)
            identity = raw.decode().strip()
            require(HEX64.fullmatch(identity), "exact created container identity required")
            all_ids.append(identity)
            return identity
        # Provisioning alone sees the signing-custody parent. No serving or
        # initializer container receives that mount or the original master root.
        mounts = [(output / "material", "/material", True), (output / "operator", "/operator", True)]
        describe_env = output / "inputs" / "description.env"
        new_file(describe_env, b"CONTROLLED_GOOGLE_DESCRIBE=1\n")
        description_id = create("description", 0, [], describe_env, ["-test.v", "-test.run=^TestControlledGoogleProfileDescription$", "-test.timeout=20s"])
        description_raw, _ = commands.run(docker + ["start", "--attach", description_id], timeout=25)
        descriptions = [decode(line) for line in description_raw.splitlines() if line.startswith(b"{")]
        require(descriptions == [decode(reference(plan["build"]["description"]))], "actual same-image passive profile description required")
        provision = create("provision", 0, mounts, environment(0, "initialize", "provision"), ["provision-security", "--operator-directory", "/operator/custody", "--valid-for", "1h"])
        commands.run(docker + ["start", "--attach", provision], timeout=60)
        for i in range(3):
            view, state = output / "views" / str(i), output / "state" / str(i)
            (view / "identity" / "nodes").mkdir(mode=0o700, parents=True)
            shutil.copytree(output / "material" / "identity" / "nodes" / f"node-{i + 1}", view / "identity" / "nodes" / f"node-{i + 1}")
            state.mkdir(mode=0o700)
            mounts = [(view, "/material", False), (state, "/state", True), (output / "inputs", "/inputs", False)]
            identity = create("initialize", i, mounts, environment(i, "initialize"), ["-test.v", "-test.run=^" + CHILD + "$", "-test.timeout=80s"])
            raw, _ = commands.run(docker + ["start", "--attach", identity], timeout=75)
            initialized = validate_child(raw, plan, "initialize", f"node-{i + 1}", child_input(plan, configs[i], admitted, "initialize")["config_sha256"])
            if authority_profile is None:
                authority_profile = initialized[0]["authority_profile"]
            require(initialized[0]["authority_profile"] == authority_profile, "initializer original cohort drift")
            identity = create("resume", i, mounts, environment(i, "resume"), ["-test.v", "-test.run=^" + CHILD + "$", "-test.timeout=11m"],
                              network=plan["network"]["id"], publish=i == 0)
            serving.append(identity)
        material_before = material_snapshot(output)
        new_file(output / "immutable-before.json", {"source": source_before, "inputs": inputs_before, "material": material_before})
        for identity in serving:
            commands.run(docker + ["start", identity])
        observed = set()
        while len(observed) < 3:
            require(not interrupted and window.left(setup=True) > 0 and window.monotonic() - window.started_mono < 90,
                    "all three current native owners did not become ready")
            for i, identity in enumerate(serving):
                raw, _ = commands.run(docker + ["logs", identity])
                records, _ = child_records(raw, plan, "resume", f"node-{i + 1}")
                expected_config = child_input(plan, configs[i], admitted, "resume")["config_sha256"]
                require(all(r["authority_profile"] == authority_profile and r["config_sha256"] == expected_config for r in records), "resume original cohort/config drift")
                if any(r.get("event") == "ready" for r in records):
                    observed.add(i)
            time.sleep(1)
        ready_at = time.time()
        value = validate_descriptor(descriptor(plan, plan_sha256, started, ready_at, authority_profile), plan, plan_sha256, authority_profile)
        new_file(output / "fixture.json", value)
        new_file(output / "ready", b"controlled-real-google-component schema1\n")
        while window.left() > 0:
            require(not interrupted, "controlled component interrupted")
            release = output / "release"
            if release.exists():
                require(read(release, maximum=128) == b"release\n", "exact private normal release required")
                break
            time.sleep(0.2)
        result["status"] = "PASS"
    except BaseException as error:
        result["failure_class"] = type(error).__name__
    finally:
        commands.cleanup = True # Reserve bounded observations/stops after a body budget failure.
        states = []
        # Exactly one normal stop per owned serving child. Never rm, restart,
        # retry, change protections, or destroy failed native state.
        try:
            if all_ids:
                # Completed one-offs are already stopped; this also closes an
                # owned init/provision child after a setup timeout. One stop only.
                commands.run(docker + ["stop", "--time", "10", *all_ids], timeout=15, check=False)
            for identity in all_ids:
                raw, code = commands.run(docker + ["inspect", "--format", "{{json .State}}", identity], check=False)
                try:
                    state = decode(raw) if code == 0 else {}
                except ValueError:
                    state = {}
                stopped = state.get("Running") is False and state.get("Pid") == 0 and state.get("Restarting") is False and state.get("Paused") is False
                states.append({"id": identity, "stopped": stopped, "exit": state.get("ExitCode"),
                               "normal_exit": stopped and state.get("Status") == "exited" and state.get("ExitCode") == 0
                               and state.get("OOMKilled") is False and state.get("Error") == ""})
                if identity in serving and stopped:
                    raw, log_code = commands.run(docker + ["logs", identity], check=False)
                    try:
                        require(log_code == 0 and state.get("ExitCode") == 0, "normal App exit required")
                        index = serving.index(identity)
                        final_records = validate_child(raw, plan, "resume", f"node-{index + 1}", child_input(plan, configs[index], admitted, "resume")["config_sha256"])
                        require(all(r["authority_profile"] == authority_profile for r in final_records), "final original cohort drift")
                    except (ValueError, KeyError, TypeError):
                        result["status"] = "FAIL"
            cleanup_known = len(states) == len(all_ids) == len(names) and all(s["stopped"] for s in states)
        except BaseException as error:
            result["status"] = "FAIL"
            result["cleanup_failure_class"] = type(error).__name__
        try:
            require(source_snapshot(plan, window, cleanup=True) == source_before
                    and input_snapshot(plan, dispatch_refs) == inputs_before, "immutable admission changed")
            if material_before is not None:
                require(material_snapshot(output) == material_before, "immutable cohort intent/custody changed")
            result["immutable_after_verified"] = True
        except BaseException as error:
            result["status"] = "FAIL"
            result["immutable_after_verified"] = False
            result["final_fence_failure_class"] = type(error).__name__
        if not cleanup_known or any(not s["normal_exit"] for s in states):
            result["status"] = "FAIL"
        for number, handler in previous.items():
            signal.signal(number, handler)
        if interrupted or window.left(cleanup=True) < 0:
            result["status"] = "FAIL"
        result.update(commands=commands.rows, attempted_names=names, containers=states, cleanup_known=cleanup_known, ready_at=instant(ready_at) if ready_at else None,
                      finished_at=instant(time.time()), provider_components_passed=0, full_provider_cells_passed=0, signup_passed=0)
        new_file(output / "lifecycle.json", result)
        if interrupted or window.left(cleanup=True) < 0:
            result["status"] = "FAIL"
            new_file(output / "NON_ACCEPTING_POST_PUBLICATION.json", {"status": "FAIL", "reason": "signal_or_deadline_after_publication"})
    return result


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--execute-reviewed-owner-window", action="store_true")
    parser.add_argument("--plan", type=Path)
    parser.add_argument("--plan-sha256")
    parser.add_argument("--root-review", type=Path)
    parser.add_argument("--root-review-sha256")
    args = parser.parse_args(argv)
    if not args.execute_reviewed_owner_window:
        print("PLAN_ONLY_NO_INPUT_NO_LOCK_NO_RUNTIME")
        return 0
    require(all(v is not None for v in (args.plan, args.plan_sha256, args.root_review, args.root_review_sha256)), "exact reviewed inputs required")
    raw = reference({"path": str(args.plan), "sha256": args.plan_sha256})
    plan = resolved(decode(raw))
    approval = decode(reference({"path": str(args.root_review), "sha256": args.root_review_sha256}))
    approved_dispatch(plan, args.plan_sha256, approval)
    lock = os.open("/private/tmp/lantern-heavy-local.lock", os.O_RDWR | os.O_NOFOLLOW)
    info = os.fstat(lock)
    require(stat.S_ISREG(info.st_mode) and info.st_uid == os.getuid() and info.st_nlink == 1
            and stat.S_IMODE(info.st_mode) == 0o600, "owned shared resource lock required")
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    result = None
    ordinary_handlers = {n: signal.getsignal(n) for n in (signal.SIGINT, signal.SIGTERM)}
    try:
        result = run(plan, args.plan_sha256, approval, args.root_review_sha256,
                     ({"path": str(args.plan), "sha256": args.plan_sha256},
                      {"path": str(args.root_review), "sha256": args.root_review_sha256}))
    finally:
        # Even a cleanup-observation exception must leave the idle holder
        # normally terminable after Root resolves its exact owned resources.
        for number, handler in ordinary_handlers.items():
            signal.signal(number, handler)
        if result is not None and result.get("cleanup_known") is True:
            fcntl.flock(lock, fcntl.LOCK_UN)
            os.close(lock)
        else:
            try:
                print(json.dumps({"status": "CLEANUP_UNKNOWN", "holder_pid": os.getpid(), "shared_lock_retained": True}), flush=True)
            except BaseException:
                pass  # Fallible stdout cannot release an unresolved owned slot.
            # No new work: Root resolves owned resource state, then explicitly
            # terminates this idle holder. Unknown is never a clean result.
            while True:
                time.sleep(1)
    print(json.dumps({"status": result["status"], "cleanup_known": result["cleanup_known"], "provider_components_passed": 0}))
    return 0 if result["status"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
