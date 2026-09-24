#!/usr/bin/env python3
"""Fail closed unless a tagged offline candidate has exact-code physical evidence."""

import argparse
from datetime import datetime, timezone
import json
from pathlib import Path
import re
import subprocess


EVIDENCE_DIR = Path("sdks/dart/example/evidence/offline-release")
REQUIRED_COMMON = {
    "online_exact_values",
    "offline_put_replay",
    "authoritative_server_expiry",
    "watch_cleanup",
    "wipe_before_send",
    "sqlite_pending_close_reopen",
    "sqlite_original_ttl_preserved",
    "sqlite_expired_before_replay",
    "sqlite_logout_wipe_reopen",
    "sqlite_partition_isolation",
    "platform_trusted_tls",
    "untrusted_tls_rejection",
    "token_rotation",
    "radio_offline_foreground_recovery",
}
REQUIRED_PLATFORM = {
    "android": "android_doze_like_pause",
    "ios": "ios_local_network_privacy_denial_retry",
}
REQUIRED_CDC = {
    "identity_checkpoint_revalidation",
    "identity_live_vertex_invalidation",
    "identity_live_edge_invalidation",
    "identity_cursor_persisted",
    "identity_unknown_survives_sqlite_reopen",
    "identity_resume_live_invalidation",
    "identity_partition_wipe",
    "identity_same_responder",
    "identity_runtime_token_refresh",
    "identity_cancellation",
}
SUITES = {
    "smoke": {
        "suffix": "",
        "kind": "physical_offline_release_evidence",
        "target": "integration_test/mobile_smoke_test.dart",
    },
    "cdc": {
        "suffix": "-cdc",
        "kind": "physical_offline_identity_cdc_evidence",
        "target": "integration_test/physical_identity_cdc_test.dart",
    },
}
HEX40 = re.compile(r"[0-9a-f]{40}\Z")
HEX64 = re.compile(r"[0-9a-f]{64}\Z")


def git(*args):
    return subprocess.check_output(["git", *args], text=True).strip()


def source_identity(tag_sha, tested_sha):
    if not HEX40.fullmatch(tag_sha) or not HEX40.fullmatch(tested_sha):
        raise ValueError("release and tested commit must be full Git SHAs")
    if git("rev-parse", f"{tag_sha}^") != tested_sha:
        raise ValueError("tag must point to the evidence-only child of the tested code commit")
    changed = set(git("diff", "--name-only", "--no-renames", tested_sha, tag_sha).splitlines())
    required = {
        str(EVIDENCE_DIR / f"{platform}{suite['suffix']}.json")
        for platform in ("android", "ios") for suite in SUITES.values()
    }
    if not required <= changed or not changed <= required | {str(EVIDENCE_DIR / "README.md")}:
        raise ValueError(f"tag changed code or lacks all physical records: {sorted(changed)}")


def validate_record(record, ci_record, platform, tested_sha, suite="smoke"):
    contract = SUITES[suite]
    expected_kind = f"physical-{platform}"
    if (
        record.get("schema") != 1
        or record.get("kind") != contract["kind"]
        or record.get("repository") != "anaregdesign/lantern"
        or record.get("testedCommit") != tested_sha
        or record.get("physicalDevice") is not True
        or record.get("contentFree") is not True
        or record.get("cleanCheckout") is not True
        or record.get("result") != "passed"
        or record.get("limitations") != []
    ):
        raise ValueError(f"{platform} physical evidence identity or result is invalid")
    if set(record) != {
        "schema", "kind", "repository", "testedCommit", "recordedAt",
        "physicalDevice", "contentFree", "cleanCheckout", "toolchain",
        "application", "platform", "network", "scenarios", "result", "limitations",
    }:
        raise ValueError(f"{platform} physical evidence has unexpected or missing fields")
    recorded_at = record["recordedAt"]
    when = datetime.fromisoformat(recorded_at.replace("Z", "+00:00"))
    if when.tzinfo != timezone.utc or when > datetime.now(timezone.utc):
        raise ValueError(f"{platform} physical evidence has invalid UTC timestamp")
    toolchain = record["toolchain"]
    if set(toolchain) != {"flutter", "flutterRevision", "dart"} or any(
        toolchain.get(key) != ci_record.get("toolchain", {}).get(key)
        for key in ("flutter", "flutterRevision", "dart")
    ) or not HEX40.fullmatch(toolchain["flutterRevision"]):
        raise ValueError(f"{platform} physical toolchain differs from tag CI")
    application = record["application"]
    if (
        set(application) != {"packageId", "target", "binarySha256"}
        or application["packageId"] != ci_record.get("application", {}).get("packageId")
        or application["target"] != contract["target"]
        or not HEX64.fullmatch(application["binarySha256"])
    ):
        raise ValueError(f"{platform} physical application identity is invalid")
    device = record["platform"]
    if (
        set(device) != {"kind", "model", "os"}
        or device["kind"] != expected_kind
        or not all(isinstance(device[key], str) and 2 <= len(device[key]) <= 80
                   for key in ("model", "os"))
    ):
        raise ValueError(f"{platform} physical platform identity is invalid")
    network = record["network"]
    if (
        set(network) != {"transport", "topology", "authenticated", "platformTrustedTls"}
        or network["transport"] != "Connect/HTTPS"
        or network["authenticated"] is not True
        or network["platformTrustedTls"] is not True
        or not isinstance(network["topology"], str)
        or not 5 <= len(network["topology"]) <= 120
    ):
        raise ValueError(f"{platform} did not prove authenticated platform-trusted TLS")
    if re.search(r"https?://|\b(?:\d{1,3}\.){3}\d{1,3}\b|@", json.dumps(record)):
        raise ValueError(f"{platform} physical evidence may contain an endpoint or identifier")
    scenarios = record["scenarios"]
    required_scenarios = (
        REQUIRED_COMMON | {REQUIRED_PLATFORM[platform]}
        if suite == "smoke" else REQUIRED_CDC
    )
    if (
        not isinstance(scenarios, list)
        or len(scenarios) != len(set(scenarios))
        or set(scenarios) != required_scenarios
    ):
        raise ValueError(f"{platform} physical release matrix is incomplete")
    if (
        ci_record.get("schema") != 1
        or ci_record.get("kind") != "ci_mobile_revision_evidence"
        or ci_record.get("repository") != "anaregdesign/lantern"
        or ci_record.get("contentFree") is not True
        or ci_record.get("physicalDevice") is not False
        or not re.fullmatch(
            r"refs/tags/sdks/dart/offline/v[0-9]+\.[0-9]+\.[0-9]+",
            ci_record.get("ref", ""),
        )
        or ci_record.get("result") != "passed"
    ):
        raise ValueError(f"{platform} tag CI manifest is invalid")


def validate(tag_sha, evidence_dir, ci_dir, run_id, run_attempt):
    records = {}
    for platform in ("android", "ios"):
        ci_path = ci_dir / f"{platform}.json"
        if not ci_path.is_file():
            raise ValueError(f"missing {platform} tag CI manifest")
        ci_record = json.loads(ci_path.read_text())
        if ci_record.get("commit") != tag_sha:
            raise ValueError(f"{platform} simulator manifest is not bound to the tag")
        if ci_record.get("workflow") != {"runId": run_id, "attempt": run_attempt}:
            raise ValueError(f"{platform} simulator manifest is from another CI attempt")
        if platform == "android" and ci_record.get("platform", {}).get("kind") != "android-emulator":
            raise ValueError("Android tag CI did not use its emulator")
        if platform == "ios" and ci_record.get("platform", {}).get("kind") != "ios-simulator":
            raise ValueError("iOS tag CI did not use its simulator")
        for suite, contract in SUITES.items():
            path = evidence_dir / f"{platform}{contract['suffix']}.json"
            if not path.is_file():
                raise ValueError(f"missing {platform} {suite} physical record")
            records[platform, suite] = json.loads(path.read_text())
    tested = records["android", "smoke"].get("testedCommit")
    if any(record.get("testedCommit") != tested for record in records.values()):
        raise ValueError("physical records tested different code commits")
    source_identity(tag_sha, tested)
    for platform in ("android", "ios"):
        ci_record = json.loads((ci_dir / f"{platform}.json").read_text())
        for suite in SUITES:
            validate_record(records[platform, suite], ci_record, platform, tested, suite)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tag-sha", required=True)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--run-attempt", required=True)
    parser.add_argument("--evidence-dir", type=Path, default=EVIDENCE_DIR)
    parser.add_argument("--ci-dir", type=Path, required=True)
    args = parser.parse_args()
    validate(args.tag_sha, args.evidence_dir, args.ci_dir, args.run_id, args.run_attempt)
    print(f"offline physical release matrix passed for tag {args.tag_sha}")


if __name__ == "__main__":
    main()
