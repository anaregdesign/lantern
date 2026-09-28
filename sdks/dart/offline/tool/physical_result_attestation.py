#!/usr/bin/env python3
"""Bind a private smoke/CDC on-device pass marker to its signed build and record."""

import argparse
from datetime import datetime, timedelta, timezone
import hashlib
import json
from pathlib import Path
import re

from physical_release_gate import SUITES


HEX64 = re.compile(r"[0-9a-f]{64}\Z")
MARKER_KINDS = {
    "smoke": "physical_mobile_smoke_on_device_result",
    "cdc": "physical_identity_cdc_on_device_result",
}
PACKAGE_IDS = {
    "android": "com.anaregdesign.lantern_example",
    "ios": "com.anaregdesign.lanternExample",
}
FINAL_KEYS = {
    "schema", "kind", "contentFree", "status", "phase", "startedAt",
    "updatedAt", "platform", "packageId", "installedBinarySha256",
}


def _unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate physical result JSON field")
        result[key] = value
    return result


def _read_object(path, label):
    path = Path(path)
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"missing {label}")
    if path.stat().st_size > 64 * 1024:
        raise ValueError(f"oversized {label}")
    try:
        value = json.loads(path.read_bytes(), object_pairs_hook=_unique_object)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ValueError(f"invalid {label}") from error
    if not isinstance(value, dict):
        raise ValueError(f"invalid {label}")
    return value


def _utc(value, label):
    if not isinstance(value, str):
        raise ValueError(f"invalid {label} UTC time")
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as error:
        raise ValueError(f"invalid {label} UTC time") from error
    if parsed.tzinfo != timezone.utc:
        raise ValueError(f"invalid {label} UTC time")
    return parsed


def validate_private_result(
    marker_path, record_path, built_binary_path, *, platform, suite, launched_after,
):
    if platform not in ("android", "ios") or suite not in MARKER_KINDS:
        raise ValueError("unsupported physical result target")
    marker = _read_object(marker_path, "on-device result marker")
    record = _read_object(record_path, "physical evidence record")
    if (
        set(marker) != FINAL_KEYS
        or marker["schema"] != 1
        or marker["kind"] != MARKER_KINDS[suite]
        or marker["contentFree"] is not True
        or marker["status"] != "passed"
        or marker["phase"] != "complete"
        or marker["platform"] != platform
        or marker["packageId"] != PACKAGE_IDS[platform]
        or not isinstance(marker["installedBinarySha256"], str)
        or not HEX64.fullmatch(marker["installedBinarySha256"])
    ):
        raise ValueError("on-device result marker is not a qualified final pass")
    application = record.get("application")
    device = record.get("platform")
    if (
        record.get("kind") != SUITES[suite]["kind"]
        or record.get("result") != "passed"
        or not isinstance(device, dict)
        or device.get("kind") != f"physical-{platform}"
        or not isinstance(application, dict)
        or application.get("target") != SUITES[suite]["target"]
        or application.get("packageId") != marker["packageId"]
        or application.get("binarySha256") != marker["installedBinarySha256"]
    ):
        raise ValueError("physical record differs from the on-device result")
    started = _utc(marker["startedAt"], "marker start")
    updated = _utc(marker["updatedAt"], "marker completion")
    launched = _utc(launched_after, "launch")
    recorded = _utc(record.get("recordedAt"), "record")
    if (
        started < launched - timedelta(seconds=30)
        or updated < started
        or updated > datetime.now(timezone.utc) + timedelta(seconds=30)
        or recorded < updated - timedelta(seconds=30)
    ):
        raise ValueError("on-device result marker is stale or has invalid timing")
    binary = Path(built_binary_path)
    if not binary.is_file():
        raise ValueError("signed build binary is missing")
    with binary.open("rb") as source:
        digest = hashlib.file_digest(source, "sha256").hexdigest()
    if digest != marker["installedBinarySha256"]:
        raise ValueError("signed build bytes differ from installed device bytes")
    return digest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--marker", type=Path, required=True)
    parser.add_argument("--record", type=Path, required=True)
    parser.add_argument("--built-binary", type=Path, required=True)
    parser.add_argument("--platform", choices=("android", "ios"), required=True)
    parser.add_argument("--suite", choices=tuple(MARKER_KINDS), required=True)
    parser.add_argument("--launched-after", required=True)
    args = parser.parse_args()
    validate_private_result(
        args.marker, args.record, args.built_binary,
        platform=args.platform, suite=args.suite,
        launched_after=args.launched_after,
    )
    print("physical on-device marker, record, and signed build bytes match")


if __name__ == "__main__":
    main()
