#!/usr/bin/env python3
"""Bind a ready physical fixture to its completed, same-source lifecycle.

This local-file-only helper never queries a clock source or controls a device.
`prepare` runs before the consuming phone test; `finalize` runs after the driver
has joined its owners. Its content-free output proves fixture provenance only,
not a phone assertion, real provider, UTC, or configured-source qualification.
Retain the SHA-256 printed by prepare independently and supply it to finalize.
"""

import argparse
from datetime import datetime, timezone
import hashlib
import json
import math
import os
from pathlib import Path
import re
import stat
import time
from urllib.parse import urlsplit

import native_login_gate as driver


HEX40 = re.compile(r"[0-9a-f]{40}\Z")
HEX64 = re.compile(r"[0-9a-f]{64}\Z")
BINARIES = ("security.test", "integration.test")
OWNERS = [{"node": i, "phase": phase, "mode": mode}
          for phase, mode in (("initialize", "fresh"), ("serve", "resume"))
          for i in (1, 2, 3)]
PROOF = "fixture serving lifecycle only; device/provider/UTC/configured-source qualification NOT_PROVEN"


def require(condition, message):
    if not condition:
        raise ValueError(message)


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def encoded(value):
    return (json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False) + "\n").encode()


def canonical(path):
    path = Path(path)
    require(path.is_absolute() and ".." not in path.parts, "absolute canonical artifact path required")
    cursor = Path(path.anchor)
    for part in path.parts[1:]:
        cursor /= part
        require(not cursor.is_symlink(), "symlink artifact path refused")
    return path


def directory(path):
    path = canonical(path)
    info = path.stat()
    require(stat.S_ISDIR(info.st_mode) and stat.S_IMODE(info.st_mode) == 0o700
            and info.st_uid == os.getuid(), "owner-only artifact directory required")
    return path


def read(path, *, maximum=2 << 20, executable=False):
    path = canonical(path)
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), "rb") as source:
        info = os.fstat(source.fileno())
        modes = (0o500, 0o555, 0o700, 0o755) if executable else (0o600,)
        require(stat.S_ISREG(info.st_mode) and stat.S_IMODE(info.st_mode) in modes
                and info.st_uid == os.getuid(), "private regular artifact required")
        require(0 <= info.st_size <= maximum, "artifact exceeds bounded size")
        raw = source.read(maximum + 1)
        require(len(raw) <= maximum, "artifact exceeds bounded size")
        return raw


def file_digest(path):
    path = canonical(path)
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), "rb") as source:
        info = os.fstat(source.fileno())
        require(stat.S_ISREG(info.st_mode) and stat.S_IMODE(info.st_mode) in (0o500, 0o555, 0o700, 0o755)
                and info.st_uid == os.getuid() and 0 < info.st_size <= 512 << 20,
                "bounded owned executable required")
        result = hashlib.sha256()
        for block in iter(lambda: source.read(1 << 20), b""):
            result.update(block)
        return result.hexdigest()


def decode(raw):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            require(key not in result, "duplicate JSON key refused")
            result[key] = value
        return result

    def finite(_):
        raise ValueError("nonfinite JSON refused")

    value = json.loads(raw, object_pairs_hook=unique, parse_constant=finite)
    require(isinstance(value, dict), "JSON object required")
    return value


def utc(value):
    require(isinstance(value, str) and value.endswith("Z"), "UTC timestamp required")
    result = datetime.fromisoformat(value[:-1] + "+00:00")
    require(result.tzinfo == timezone.utc, "UTC timestamp required")
    return result.timestamp()


def number(value):
    return type(value) in (int, float) and math.isfinite(value)


def expected_mode(source_kind):
    require(source_kind in ("configured-source", "controlled-source"), "explicit fixture source required")
    controlled = source_kind == "controlled-source"
    return {"source_kind": source_kind, "build_kind": "test-only" if controlled else "normal",
            "build_tags": ["lantern_test_time"] if controlled else [], "synthetic_utc": controlled}


def configured_source_digest():
    # Fixed existing host input, not a source query or a permission/config write.
    with os.fdopen(os.open("/etc/ntp.conf", os.O_RDONLY | os.O_NOFOLLOW), "rb") as source:
        info = os.fstat(source.fileno())
        require(stat.S_ISREG(info.st_mode) and 0 < info.st_size <= 4096, "bounded configured-source input required")
        raw = source.read(4097)
        require(0 < len(raw) <= 4096, "bounded configured-source input required")
    return digest(raw)


def source_fields(value, source_kind):
    mode = expected_mode(source_kind)
    require(all(value.get(key) == expected and type(value.get(key)) is type(expected)
                for key, expected in mode.items()), "source/build provenance mismatch")
    return mode


def commands(evidence, receipt):
    rows = receipt.get("commands")
    require(isinstance(rows, list), "driver command records required")
    for i, row in enumerate(rows):
        require(isinstance(row, dict) and set(row) == {"argv", "exit", "log", "sha256"}
                and isinstance(row["argv"], list) and all(isinstance(arg, str) for arg in row["argv"])
                and type(row["exit"]) is int and row["log"] == f"{i:02d}.log"
                and isinstance(row["sha256"], str) and HEX64.fullmatch(row["sha256"]),
                "complete ordered driver command required")
        require(digest(read(evidence / row["log"], maximum=32 << 20)) == row["sha256"],
                "driver command log hash mismatch")
    return rows


def inputs(evidence, receipt, profile, source_kind, expected_head):
    require(profile in ("native", "physical") and HEX40.fullmatch(expected_head), "explicit profile/head required")
    require(type(receipt.get("schema")) is int and receipt["schema"] == 1 and receipt.get("mode") == "serving"
            and receipt.get("serving_profile") == profile and receipt.get("head") == expected_head
            and receipt.get("development_changes") is False, "clean exact-source serving receipt required")
    mode = source_fields(receipt, source_kind)
    for field, pattern in (("tree", HEX40), ("source_sha256", HEX64), ("serving_origin_sha256", HEX64)):
        require(isinstance(receipt.get(field), str) and pattern.fullmatch(receipt[field]), "source identity missing")
    require(isinstance(receipt.get("image"), str) and receipt["image"].startswith("sha256:")
            and HEX64.fullmatch(receipt["image"][7:]), "immutable image digest required")
    require(type(receipt.get("serving_lifetime_seconds")) is int
            and 15 <= receipt["serving_lifetime_seconds"] <= 540 and number(receipt.get("started")),
            "finite serving lifetime required")
    binaries = receipt.get("binaries")
    require(isinstance(binaries, dict) and set(binaries) == set(BINARIES), "both serving binaries required")
    directory(evidence / "build")
    for name in BINARIES:
        require(isinstance(binaries[name], str) and HEX64.fullmatch(binaries[name])
                and file_digest(evidence / "build" / name) == binaries[name], "serving binary hash mismatch")
    if source_kind == "controlled-source":
        require("source_config_sha256" not in receipt, "controlled fixture contains configured-source binding")
    else:
        require(isinstance(receipt.get("source_config_sha256"), str)
                and HEX64.fullmatch(receipt["source_config_sha256"])
                and configured_source_digest() == receipt["source_config_sha256"], "configured-source input hash required")
    directory(evidence / "private")
    raw = read(evidence / "private" / "fixture.json")
    fixture = decode(raw)
    controlled = source_kind == "controlled-source"
    kind = ("controlled-physical-serving" if profile == "physical" else "controlled-native-serving") if controlled else (
        "current-physical-serving" if profile == "physical" else "synthetic-native-serving")
    require(type(fixture.get("schema")) is int and fixture["schema"] == (2 if controlled else 1)
            and fixture.get("kind") == kind and fixture.get("ca_file") == "ca.pem", "fixture schema/kind mismatch")
    if controlled:
        source_fields(fixture, source_kind)
    else:
        require(not any(key in fixture for key in mode), "normal descriptor contains mixed provenance")
    endpoint = fixture.get("endpoint")
    require(isinstance(endpoint, str), "fixture HTTPS origin required")
    origin = urlsplit(endpoint)
    require(origin.scheme == "https" and origin.hostname and not origin.username and not origin.password
            and not origin.path and not origin.query and not origin.fragment and origin.netloc == origin.netloc.lower()
            and origin.geturl() == endpoint and (origin.port is None or 1 <= origin.port <= 65535)
            and digest(endpoint.encode()) == receipt["serving_origin_sha256"],
            "fixture origin binding mismatch")
    until = utc(fixture.get("serving_until"))
    if controlled:
        serving_from = utc(fixture.get("serving_from"))
        require(receipt["started"] <= serving_from < until
                and abs(until - serving_from - receipt["serving_lifetime_seconds"]) < 0.00001,
                "controlled serving interval mismatch")
    if profile == "physical":
        require(fixture.get("receipt_retention_ms") == 21_600_000 and fixture.get("receipt_max_entries") == 1024
                and fixture.get("receipt_max_bytes") == 32 << 20, "physical certified receipt policy mismatch")
        credentials = fixture.get("credentials")
        require(isinstance(credentials, dict) and isinstance(credentials.get("observer_tokens"), list)
                and len(credentials["observer_tokens"]) == 2, "physical observer credentials missing")
        tokens = [*credentials["observer_tokens"], credentials.get("writer_token")]
        require(all(isinstance(token, str) and token.startswith("lnt_m1_") and len(token) <= 8192 for token in tokens)
                and len(set(tokens)) == 3 and utc(credentials.get("expires_at")) > until,
                "distinct original unexpired physical credentials required")
    else:
        accounts = fixture.get("accounts")
        require(isinstance(fixture.get("prefix"), str) and fixture["prefix"].startswith("orders:")
                and fixture["prefix"].endswith(":") and len(fixture["prefix"].encode()) <= 128,
                "bounded native fixture prefix required")
        require(isinstance(accounts, dict) and set(accounts) == {"a", "b"}, "native A/B accounts required")
        tokens = []
        for account in accounts.values():
            require(isinstance(account, dict) and isinstance(account.get("access_token"), str)
                    and account["access_token"].startswith("lnu1_") and len(account["access_token"]) <= 8192
                    and utc(account.get("expires_at")) > until, "original native credential required")
            tokens.append(account["access_token"])
        require(tokens[0] != tokens[1], "distinct native accounts required")
    rows = commands(evidence, receipt)
    require(len(rows) >= 3 and all(row["exit"] == 0 for row in rows[:3]), "successful fixture builds required")
    for i, (package, binary) in enumerate((("./server/internal/security", BINARIES[0]), ("./tests/integration", BINARIES[1]))):
        require(rows[i]["argv"] == ["go", "test", "-c", *(["-tags=lantern_test_time"] if controlled else []),
                                    "-o", str(evidence / "build" / binary), package], "binary build provenance mismatch")
    label = "lantern.task=2119-controlled-serving" if controlled else "lantern.task=1809-native-serving"
    build = rows[2]["argv"]
    require(len(build) == 9 and build[:7] == ["docker", "build", "--pull=false", "--network=none", "--label", label, "-t"]
            and re.fullmatch(r"lantern-native-login-[0-9a-f]{12}", build[7]) and build[8:] == [str(evidence / "build")],
            "isolated fixture image build required")
    selected = {key: receipt[key] for key in ("head", "tree", "source_sha256", "image", "binaries", "serving_origin_sha256",
                                             "serving_lifetime_seconds", "started")}
    selected.update(mode, profile=profile, descriptor_schema=fixture["schema"], descriptor_kind=kind,
                    descriptor_sha256=digest(raw), ca_sha256=digest(read(evidence / "private" / "ca.pem")),
                    serving_until=fixture["serving_until"])
    if controlled:
        selected["serving_from"] = fixture["serving_from"]
    if not controlled:
        selected["source_config_sha256"] = receipt["source_config_sha256"]
    return selected, rows, build[7], fixture


def ready_log(raw, profile, source_kind):
    text = raw.decode("utf-8")
    controlled = source_kind == "controlled-source"
    source = driver.CONTROLLED_SOURCE_MARKER if controlled else driver.NORMAL_SOURCE_MARKER
    other = "production native constructor:" if controlled else "test-build controlled source:"
    marker = "native" if profile == "native" else "physical"
    ready = "accounts=2" if profile == "native" else "observer_credentials=2 writer_credentials=1"
    source_prefix = "test-build controlled source:" if controlled else "production native constructor:"
    require(text.count(source) == 1 and text.count(source_prefix) == 1 and other not in text
            and "--- FAIL:" not in text and "--- SKIP:" not in text,
            "serving source evidence mismatch")
    require(re.findall(r"current_constructor node=(\d+) mode=(\w+) passed=(\w+)", text)
            == [(str(i), mode, "true") for mode in ("fresh", "resume") for i in (1, 2, 3)],
            "six ordered current constructors required")
    ready_lines = re.findall(r"(?:native|physical)_fixture_ready[^\r\n]*", text)
    require(ready_lines == [f"{marker}_fixture_ready schema={2 if controlled else 1} {ready}"],
            "same-profile ready observation required")
    return text


def prepare(evidence, expected_head, profile, source_kind, now=None):
    evidence = directory(evidence)
    now = time.time() if now is None else now
    raw = read(evidence / "receipt.json")
    receipt = decode(raw)
    require(receipt.get("status") == "RUNNING" and "finished" not in receipt and "error" not in receipt,
            "running serving driver required before phone test")
    selected, rows, _, _ = inputs(evidence, receipt, profile, source_kind, expected_head)
    require(len(rows) == 3 and selected["started"] <= now < utc(selected["serving_until"]),
            "live finite serving interval required")
    require(source_kind != "controlled-source" or utc(selected["serving_from"]) <= now,
            "controlled fixture has not started")
    read(evidence / "private" / "ready", maximum=4096)
    log_name = f"{len(rows):02d}.log"
    log = read(evidence / log_name, maximum=32 << 20)
    text = ready_log(log, profile, source_kind)
    require("_fixture_stopped reason=" not in text and "--- PASS: " + driver.ROOT_TEST not in text,
            "fixture already stopped")
    return {"schema": 1, "kind": "physical-fixture-binding", "status": "PREPARED", "proof": PROOF,
            "prepared_at": now, "fixture": selected, "running_receipt_sha256": digest(raw),
            "command_prefix_sha256": [digest(encoded(row)) for row in rows],
            "active_log": {"name": log_name, "bytes": len(log), "sha256": digest(log)}}


def run_command(row, evidence, receipt, name, fixture):
    args = row["argv"]
    require(row["exit"] == 0 and args[:3] == ["docker", "run", "--rm"], "successful serving process required")
    pairs = {}
    singles = {"--rm", "--pull=never", "--network=bridge", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
               "--pids-limit=256", "--memory=2g", "--cpus=4"}
    rest = args[2:]
    while rest and rest[0] != receipt["image"]:
        flag = rest.pop(0)
        if flag in singles:
            require(flag not in pairs, "duplicate container flag refused")
            pairs[flag] = [True]
        else:
            require(flag in {"--name", "--label", "--user", "--sysctl", "--tmpfs", "--mount", "--publish", "--add-host", "--env"}
                    and rest, "unknown serving container flag")
            pairs.setdefault(flag, []).append(rest.pop(0))
    require(all(pairs.get(flag) == [True] for flag in singles), "bounded serving isolation required")
    controlled = receipt["source_kind"] == "controlled-source"
    require(pairs.get("--name") == [name]
            and pairs.get("--label") == ["lantern.task=2119-controlled-serving" if controlled else "lantern.task=1809-native-serving"]
            and pairs.get("--user") == [f"{os.getuid()}:{os.getgid()}"]
            and pairs.get("--sysctl") == ["net.ipv4.ip_unprivileged_port_start=0"]
            and pairs.get("--tmpfs") == ["/work:rw,nosuid,nodev,size=512m,mode=1777"], "serving ownership mismatch")
    mounts = ([] if controlled else ["type=bind,source=/etc/ntp.conf,target=/etc/ntp.conf,readonly"])
    mounts += [f"type=bind,source={evidence / 'private'},target=/native-fixture"]
    require(pairs.get("--mount") == mounts and pairs.get("--add-host") == [host + ":127.0.0.1" for host in driver.HOSTS],
            "fixture mounts/hosts mismatch")
    publish = pairs.get("--publish", [])
    require(len(publish) == 1 and re.fullmatch(r"127\.0\.0\.1:[0-9]+:[0-9]+/tcp", publish[0]), "loopback publication required")
    ports = [int(value) for value in publish[0].removesuffix("/tcp").split(":")[1:]]
    require(all(1024 <= port <= 65535 for port in ports), "unprivileged fixture ports required")
    env_rows = pairs.get("--env", [])
    require(all("=" in item for item in env_rows), "explicit serving environment required")
    environment = dict(item.split("=", 1) for item in env_rows)
    expected = {"LANTERN_CURRENT_PUBLIC_GATE": "1", "LANTERN_NATIVE_LOGIN_GATE": "1",
                "LANTERN_CURRENT_FIXTURE_EXPORTER": "/security.test", "TMPDIR": "/work", "LANTERN_NATIVE_FIXTURE_SERVE": "1",
                "LANTERN_NATIVE_FIXTURE_ORIGIN": "<private-service-origin>", "LANTERN_NATIVE_FIXTURE_PREFIX": fixture.get("prefix", "orders:"),
                "LANTERN_NATIVE_FIXTURE_LISTEN_PORT": str(ports[1]),
                "LANTERN_NATIVE_FIXTURE_LIFETIME_SECONDS": str(receipt["serving_lifetime_seconds"]),
                "LANTERN_NATIVE_FIXTURE_EXPORT_DIR": "/native-fixture"}
    if receipt["serving_profile"] == "physical":
        expected["LANTERN_NATIVE_FIXTURE_PROFILE"] = "physical"
    if controlled:
        expected["LANTERN_NATIVE_FIXTURE_TIME_SOURCE"] = "controlled-source"
    require(len(environment) == len(env_rows) and environment == expected, "serving environment provenance mismatch")
    require(rest == [receipt["image"], "-test.v", "-test.run=^" + driver.ROOT_TEST + "$",
                     f"-test.timeout={receipt['serving_lifetime_seconds'] + 300}s"], "exact serving test required")


def finalize(evidence, binding_raw, binding_sha256, now=None):
    require(isinstance(binding_sha256, str) and HEX64.fullmatch(binding_sha256)
            and digest(binding_raw) == binding_sha256, "original binding digest required")
    binding = decode(binding_raw)
    require(set(binding) == {"schema", "kind", "status", "proof", "prepared_at", "fixture", "running_receipt_sha256",
                             "command_prefix_sha256", "active_log"} and binding["schema"] == 1
            and binding["kind"] == "physical-fixture-binding" and binding["status"] == "PREPARED"
            and binding["proof"] == PROOF, "prepared fixture binding required")
    evidence = directory(evidence)
    now = time.time() if now is None else now
    raw = read(evidence / "receipt.json")
    receipt = decode(raw)
    require(receipt.get("status") == "PASS" and "error" not in receipt and number(receipt.get("finished"))
            and number(binding["prepared_at"]) and binding["prepared_at"] <= receipt["finished"] <= now + 1,
            "completed passing serving driver required")
    previous = binding["fixture"]
    require(isinstance(previous, dict), "fixture binding required")
    selected, rows, name, fixture = inputs(evidence, receipt, previous.get("profile"), previous.get("source_kind"), previous.get("head", ""))
    require(selected == previous and len(rows) == 6
            and [digest(encoded(row)) for row in rows[:3]] == binding["command_prefix_sha256"],
            "fixture inputs/command prefix changed")
    require(not (evidence / "private" / "ready").exists() and not (evidence / "private" / "ready").is_symlink(),
            "fixture still ready")
    active = binding["active_log"]
    require(isinstance(active, dict) and set(active) == {"name", "bytes", "sha256"}
            and active["name"] == rows[3]["log"] and type(active["bytes"]) is int and 0 < active["bytes"] <= 32 << 20,
            "original serving log binding required")
    log = read(evidence / rows[3]["log"], maximum=32 << 20)
    require(digest(log[:active["bytes"]]) == active["sha256"], "serving log prefix changed")
    text = ready_log(log, selected["profile"], selected["source_kind"])
    driver.require_serving(text, selected["profile"], selected["source_kind"])
    require(receipt.get("native_owner_constructors") == OWNERS, "exact final native owner records required")
    case = driver.SERVING_CASE if selected["profile"] == "native" else driver.ROOT_TEST + "/physical_fixture_serving_export"
    require(receipt.get("cases") == [case], "exact serving case record required")
    run_command(rows[3], evidence, receipt, name, fixture)
    # --rm owns successful child removal; a subsequent rm of the absent name may
    # report 1. Retain that result honestly rather than pretending both were 0.
    require(rows[4]["argv"] == ["docker", "rm", "--force", name]
            and rows[5]["argv"] == ["docker", "image", "rm", name]
            and all(row["exit"] in (0, 1) for row in rows[4:]), "owned cleanup command records required")
    result = {"schema": 1, "kind": "physical-fixture-provenance", "status": "PASS", "proof": PROOF,
              "fixture": selected, "prepared_at": binding["prepared_at"], "finished": receipt["finished"], "finalized_at": now,
              "binding_sha256": binding_sha256, "receipt_sha256": digest(raw), "lifecycle_log_sha256": rows[3]["sha256"],
              "native_owner_constructors": [dict(owner) for owner in OWNERS],
              "command_results": [{key: row[key] for key in ("log", "sha256", "exit")} for row in rows]}
    if selected["source_kind"] == "controlled-source":
        stopped = driver.controlled_serving_stop(text, selected["profile"])
        require(binding["prepared_at"] <= utc(stopped) <= receipt["finished"],
                "actual controlled serving stop observation required")
        result["serving_stopped_at"] = stopped
    return result


def write_new(path, value):
    path = canonical(path)
    directory(path.parent)
    raw = encoded(value)
    with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600), "wb") as output:
        output.write(raw)
        output.flush()
        os.fsync(output.fileno())
    return digest(raw)


def verify_final(path):
    """Revalidate a saved sidecar against its private local evidence, not a claim."""
    path = canonical(path)
    require(path.name == "physical-fixture-provenance.json", "fixed final provenance filename required")
    raw = read(path)
    saved = decode(raw)
    require(number(saved.get("finalized_at")) and saved["finalized_at"] <= time.time() + 1,
            "final provenance timestamp required")
    binding_raw = read(path.parent / "physical-fixture-binding.json")
    verified = finalize(path.parent, binding_raw, saved.get("binding_sha256"), now=saved["finalized_at"])
    require(saved == verified, "final provenance sidecar mismatch")
    return saved, digest(raw)


PUBLIC_FIXTURE_KEYS = {"schema", "profile", "sourceCommit", "sourceKind", "buildKind", "buildTags", "syntheticUtc",
                       "imageSha256", "descriptorSha256", "receiptSha256", "sidecarSha256", "preparedAt", "servingUntil",
                       "servingStoppedAt", "lifecycleFinishedAt", "deviceStartedAt", "deviceFinishedAt",
                       "phoneMarkerSha256", "recordPayloadSha256"}


def stamp(value):
    return datetime.fromtimestamp(value, timezone.utc).isoformat().replace("+00:00", "Z")


def validate_summary(summary, tested_commit, profile, *, device_started_at=None, device_finished_at=None,
                     phone_marker_sha256=None, record_payload_sha256=None):
    """Validate archived shape/bindings; this cannot read or certify a device."""
    require(isinstance(summary, dict) and set(summary) == PUBLIC_FIXTURE_KEYS
            and type(summary["schema"]) is int and summary["schema"] == 1
            and isinstance(tested_commit, str) and HEX40.fullmatch(tested_commit) and summary["sourceCommit"] == tested_commit
            and profile in ("native", "physical") and summary["profile"] == profile,
            "fixture summary identity mismatch")
    mode = expected_mode(summary["sourceKind"])
    require(summary["buildKind"] == mode["build_kind"] and summary["buildTags"] == mode["build_tags"]
            and type(summary["syntheticUtc"]) is bool and summary["syntheticUtc"] == mode["synthetic_utc"],
            "fixture summary source/build mismatch")
    for key in ("imageSha256", "descriptorSha256", "receiptSha256", "sidecarSha256", "phoneMarkerSha256", "recordPayloadSha256"):
        require(isinstance(summary[key], str) and HEX64.fullmatch(summary[key]), "fixture summary digest required")
    prepared, start, finish, until, stopped, ended = [utc(summary[key]) for key in
                                                     ("preparedAt", "deviceStartedAt", "deviceFinishedAt", "servingUntil",
                                                      "servingStoppedAt", "lifecycleFinishedAt")]
    require(prepared <= start <= finish < until and finish <= stopped <= ended and until - prepared <= 540,
            "device assertions outside bound serving interval")
    for key, expected in (("deviceStartedAt", device_started_at), ("deviceFinishedAt", device_finished_at),
                          ("phoneMarkerSha256", phone_marker_sha256), ("recordPayloadSha256", record_payload_sha256)):
        require(expected is None or summary[key] == expected, "fixture summary capture binding mismatch")
    return summary


def public_summary(provenance_path, tested_commit, profile, device_started_at, device_finished_at,
                   phone_marker_sha256, record_payload_sha256):
    verified, sidecar_sha = verify_final(provenance_path)
    fixture = verified["fixture"]
    require("serving_stopped_at" in verified, "explicit controlled serving stop required for public summary")
    summary = {"schema": 1, "profile": fixture["profile"], "sourceCommit": fixture["head"],
               "sourceKind": fixture["source_kind"], "buildKind": fixture["build_kind"],
               "buildTags": fixture["build_tags"], "syntheticUtc": fixture["synthetic_utc"],
               "imageSha256": fixture["image"][7:], "descriptorSha256": fixture["descriptor_sha256"],
               "receiptSha256": verified["receipt_sha256"], "sidecarSha256": sidecar_sha,
               "preparedAt": stamp(verified["prepared_at"]), "servingUntil": fixture["serving_until"],
               "lifecycleFinishedAt": stamp(verified["finished"]),
               "servingStoppedAt": verified["serving_stopped_at"],
               "deviceStartedAt": device_started_at, "deviceFinishedAt": device_finished_at,
               "phoneMarkerSha256": phone_marker_sha256, "recordPayloadSha256": record_payload_sha256}
    return validate_summary(summary, tested_commit, profile)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    subcommands = parser.add_subparsers(dest="action", required=True)
    for action in ("prepare", "finalize"):
        command = subcommands.add_parser(action)
        command.add_argument("--evidence", type=Path, required=True)
        command.add_argument("--output", type=Path, required=True)
        if action == "prepare":
            command.add_argument("--expected-head", required=True)
            command.add_argument("--profile", choices=("native", "physical"), required=True)
            command.add_argument("--source-kind", choices=("configured-source", "controlled-source"), required=True)
        else:
            command.add_argument("--binding", type=Path, required=True)
            command.add_argument("--binding-sha256", required=True)
    args = parser.parse_args(argv)
    try:
        if args.action == "prepare":
            require(args.output == args.evidence / "physical-fixture-binding.json", "fixed binding output path required")
            result = prepare(args.evidence, args.expected_head, args.profile, args.source_kind)
        else:
            require(args.output == args.evidence / "physical-fixture-provenance.json"
                    and args.binding == args.evidence / "physical-fixture-binding.json", "fixed provenance paths required")
            result = finalize(args.evidence, read(args.binding), args.binding_sha256)
        result_sha = write_new(args.output, result)
    except (ValueError, OSError, KeyError, TypeError, UnicodeError, RuntimeError):
        parser.exit(1, "physical fixture provenance validation failed; no qualification emitted\n")
    print(json.dumps({"status": result["status"], "sha256": result_sha, "kind": result["kind"]}))


if __name__ == "__main__":
    main()
