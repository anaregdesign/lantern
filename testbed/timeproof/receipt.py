"""Passive verification for the separate OFF source proof; never starts a process."""

import hashlib
import json
import re
from pathlib import Path


SCHEMA = "lantern-off-source-1"
COUNTERS = {"attempts", "writes", "installations", "in_flight", "failed", "denied"}
EVENTS = ("reserved", "adopted", "probe", "stop_requested", "app_returned", "cleanup_returned", "final")


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def strict_json(raw, limit=65536):
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise ValueError("duplicate JSON field")
            result[key] = value
        return result
    if len(raw) > limit:
        raise ValueError("oversize JSON record")
    return json.loads(raw, object_pairs_hook=pairs)


def integer(value):
    return type(value) is int and 0 <= value < 2**64


def verify(receipt, prepared, evidence):
    """Caller must separately trust the exact prepared/receipt digests."""
    if receipt.get("schema") != SCHEMA or receipt.get("status") != "PASS":
        raise ValueError("not a successful OFF receipt")
    if receipt.get("source") != prepared["source"] or receipt.get("prepared_sha256") != prepared["digest"]:
        raise ValueError("source/preparation mismatch")
    if receipt.get("before") != receipt.get("after") or not receipt.get("before"):
        raise ValueError("environment/source changed")
    native = receipt["before"]
    expected_fields = {"system", "kernel", "machine", "boot_sha256", "namespace", "offsets_sha256", "source_sha256", "source_path", "source_read_only", "capabilities"}
    if set(native) != expected_fields or native["system"] != "Linux" or not isinstance(native["kernel"], str) or not 1 <= len(native["kernel"]) <= 256 or native["machine"] not in ("aarch64", "arm64", "x86_64", "amd64") or native["source_path"] != "/etc/ntp.conf" or native["source_sha256"] != sha(b"server time.asia.apple.com\n") or native["source_read_only"] is not True or not isinstance(native["namespace"], str) or not re.fullmatch(r"time:\[[1-9][0-9]*\]", native["namespace"]):
        raise ValueError("invalid configured source/native provenance")
    if any(not isinstance(native[k], str) or not re.fullmatch(r"[0-9a-f]{64}", native[k]) for k in ("boot_sha256", "offsets_sha256")) or not isinstance(native["capabilities"], list) or not 1 <= len(native["capabilities"]) <= 10 or any(not isinstance(line, str) or len(line) > 128 for line in native["capabilities"]):
        raise ValueError("missing native provenance")
    arch = "arm64" if native["machine"] in ("aarch64", "arm64") else "amd64"
    if prepared["target"] != ["linux", arch]:
        raise ValueError("receipt belongs to another target")
    if not integer(receipt.get("elapsed_ns")) or receipt["elapsed_ns"] > 120_000_000_000:
        raise ValueError("cohort exceeded120 seconds")
    if not integer(receipt.get("boottime_elapsed_ns")) or receipt["boottime_elapsed_ns"] > 120_000_000_000:
        raise ValueError("suspend-inclusive cohort exceeded120 seconds")
    if not integer(receipt.get("final_elapsed_ns")) or not receipt["elapsed_ns"] <= receipt["final_elapsed_ns"] <= 120_000_000_000:
        raise ValueError("final completion exceeded120 seconds")
    if receipt.get("wire") != {"public_off": True, "replication": True, "private": True}:
        raise ValueError("wire checks incomplete")
    rows = receipt.get("children", [])
    if len(rows) != 3 or any(type(r.get("slot")) is not int for r in rows) or [r.get("slot") for r in rows] != [1, 2, 3]:
        raise ValueError("exactly three child slots required")
    ledger = receipt.get("launch_ledger", [])
    if len(ledger) != 3 or any(type(r.get("slot")) is not int for r in ledger) or [r.get("slot") for r in ledger] != [1, 2, 3]:
        raise ValueError("complete three-attempt launch ledger required")
    pids, nonces, attempts, writes = set(), set(), 0, 0
    for row in rows:
        launch = ledger[row["slot"] - 1]
        if launch.get("final_counters_available") is not True or any(launch.get(k) != row.get(k) for k in ("slot", "pid", "nonce", "exit", "killed", "launch_ns", "stop_ns")):
            raise ValueError("launch/exit ledger does not match final rows")
        pid, nonce = row.get("pid"), row.get("nonce", "")
        if type(pid) is not int or pid <= 0 or pid in pids or not re.fullmatch(r"[0-9a-f]{64}", nonce) or int(nonce, 16) == 0 or nonce in nonces:
            raise ValueError("invalid/reused child identity")
        pids.add(pid)
        nonces.add(nonce)
        if type(row.get("exit")) is not int or row["exit"] != 0 or row.get("killed") is not False or row.get("binary_sha256") != prepared["child_sha256"]:
            raise ValueError("child exit/binary mismatch")
        if not integer(row.get("stop_ns")) or row["stop_ns"] > 90_000_000_000:
            raise ValueError("work not canceled by90 seconds")
        if not integer(row.get("wait_ns")) or row["wait_ns"] > receipt["elapsed_ns"]:
            raise ValueError("missing/late process wait")
        records = row.get("records", [])
        if [r.get("event") for r in records] != list(EVENTS):
            raise ValueError("missing/duplicate/out-of-order phase")
        previous, child_elapsed = row["launch_ns"], 0
        if not integer(previous):
            raise ValueError("invalid launch offset")
        for record in records:
            offset, elapsed = record.get("received_ns"), record.get("elapsed_ns")
            if record.get("nonce") != nonce or record.get("pid") != pid or not integer(offset) or not previous <= offset <= row["wait_ns"] or not integer(elapsed) or elapsed < child_elapsed:
                raise ValueError("unbound phase or invalid ordering")
            previous, child_elapsed = offset, elapsed
        adopted, probe, app, final = records[1], records[2], records[4], records[-1]
        if adopted.get("mode") != "off" or type(adopted.get("initializers")) is not int or adopted["initializers"] != 1 or adopted.get("revision") != prepared["source"]["head"] or (adopted.get("goos"), adopted.get("goarch")) != tuple(prepared["target"]):
            raise ValueError("not the expected one-owner OFF composition")
        if probe.get("ok") is not True or app.get("ok") is not True or final.get("mode") != "off" or final.get("closed_refused") is not True or final.get("app_ok") is not True:
            raise ValueError("probe/closed capability/cleanup failed")
        counts = final.get("counters", {})
        if set(counts) != COUNTERS or counts != final.get("repeat"):
            raise ValueError("incomplete/unstable final counters")
        if not all(integer(counts[k]) for k in ("attempts", "writes", "installations")) or not 0 < counts["installations"] <= counts["writes"] <= counts["attempts"] or any(counts[k] is not False for k in ("in_flight", "failed", "denied")):
            raise ValueError("invalid final producer evidence")
        attempts += counts["attempts"]
        writes += counts["writes"]
        for stream in ("stdout", "stderr"):
            name = f"child-{row['slot']}.{stream}"
            raw = (Path(evidence) / name).read_bytes()
            if len(raw) > 4 << 20 or sha(raw) != row.get(stream + "_sha256"):
                raise ValueError("raw transcript changed/oversize")
            if stream == "stdout":
                decoded = [strict_json(line[len(b"off_source "):]) for line in raw.splitlines() if line.startswith(b"off_source ")]
                stripped = [{k: v for k, v in r.items() if k != "received_ns"} for r in records]
                if decoded != stripped:
                    raise ValueError("phase record differs from raw child output")
    if attempts > 12 or writes > 12:
        raise ValueError("aggregate acquisition budget exceeded")
    return {"owners": 3, "attempts": attempts, "writes": writes}
