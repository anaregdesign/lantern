#!/usr/bin/env python3
"""Validate a captured receipt marker against the exact installed build bytes.

This is opt-in preparation. The four-record smoke/CDC release gate does not call
this validator until the final receipt target and its required scenario set exist.
"""

import argparse
from datetime import datetime, timedelta, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import select
import subprocess
import tempfile
import time


REPOSITORY = "anaregdesign/lantern"
MARKER_KIND = "physical_receipt_attestation"
RECORD_KIND = "physical_offline_receipt_evidence"
PACKAGE_IDS = {
    "android": "com.anaregdesign.lantern_example",
    "ios": "com.anaregdesign.lanternExample",
}
BUILD_SUFFIXES = {
    "android": ("build", "app", "outputs", "flutter-apk", "app-profile.apk"),
    "ios": ("build", "ios", "iphoneos", "Runner.app", "Frameworks", "App.framework", "App"),
}
HEX40 = re.compile(r"[0-9a-f]{40}\Z")
HEX32 = re.compile(r"[0-9a-f]{32}\Z")
HEX64 = re.compile(r"[0-9a-f]{64}\Z")
TARGET = re.compile(r"integration_test/[a-z][a-z0-9_]*_test\.dart\Z")
SCENARIO = re.compile(r"[a-z][a-z0-9_]*\Z")
UTC_TIMESTAMP = re.compile(
    r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}"
    r"(?:\.[0-9]{1,6})?Z\Z"
)
CLOCK_SKEW = timedelta(minutes=2)
LAUNCH_LIMIT = timedelta(minutes=10)
RUN_LIMIT = timedelta(hours=4)
CAPTURE_LIMIT = timedelta(minutes=30)
MAX_EVIDENCE_BYTES = 64 * 1024
MAX_DEVICE_PROBE_BYTES = 32
MARKER_FIELDS = {
    "schema", "kind", "contentFree", "testedCommit", "target", "runId",
    "platform", "packageId", "installedBinarySha256", "startedAt",
    "finishedAt", "completedScenarios", "status", "phase",
}
RECORD_FIELDS = {
    "schema", "kind", "repository", "contentFree", "physicalDevice",
    "testedCommit", "runId", "runStartedAt", "recordedAt", "platform",
    "application", "scenarios", "result",
}


def _unique_json_fields(pairs):
    fields = {}
    for key, value in pairs:
        if key in fields:
            raise ValueError("duplicate receipt JSON field")
        fields[key] = value
    return fields


def _read_bytes_limited(path, label):
    if not path.is_file():
        raise ValueError(f"missing {label}")
    try:
        with path.open("rb") as evidence:
            data = evidence.read(MAX_EVIDENCE_BYTES + 1)
    except OSError as error:
        raise ValueError(f"invalid {label}") from error
    if not data or len(data) > MAX_EVIDENCE_BYTES:
        raise ValueError(f"{label} is missing or oversized")
    return data


def _read_object(path, label):
    try:
        value = json.loads(
            _read_bytes_limited(path, label).decode("utf-8"),
            object_pairs_hook=_unique_json_fields,
        )
    except (UnicodeError, json.JSONDecodeError) as error:
        raise ValueError(f"invalid {label}") from error
    if not isinstance(value, dict):
        raise ValueError(f"{label} must be a JSON object")
    return value


def _utc(value, label):
    if not isinstance(value, str) or not UTC_TIMESTAMP.fullmatch(value):
        raise ValueError(f"{label} must be a UTC timestamp ending in Z")
    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as error:
        raise ValueError(f"{label} is not a valid timestamp") from error


def _scenarios(value, expected, label):
    if (
        not isinstance(value, list)
        or any(not isinstance(item, str) or not SCENARIO.fullmatch(item) for item in value)
        or len(value) != len(set(value))
        or set(value) != expected
    ):
        raise ValueError(f"{label} does not prove the required receipt scenarios")


def _built_digest(path, platform):
    suffix = BUILD_SUFFIXES[platform]
    if tuple(path.parts[-len(suffix):]) != suffix or path.is_symlink() or not path.is_file():
        raise ValueError(f"{platform} host binary must be the profile target build artifact")
    before = path.stat()
    if before.st_size == 0:
        raise ValueError("host binary is empty")
    digest = hashlib.sha256()
    with path.open("rb") as binary:
        for chunk in iter(lambda: binary.read(1024 * 1024), b""):
            digest.update(chunk)
    after = path.stat()
    if (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns) != (
        after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns
    ):
        raise ValueError("host binary changed during validation")
    return digest.hexdigest()


def assert_capture_checkout(tested_commit, built_binary_path, platform, target):
    """The capture CLI must use the clean tested checkout's own build output."""
    if (
        platform not in PACKAGE_IDS
        or not isinstance(target, str)
        or not TARGET.fullmatch(target)
        or target == "integration_test/receipt_binary_probe_test.dart"
    ):
        raise ValueError("receipt capture requires a dedicated target")
    try:
        head = subprocess.check_output(
            ["git", "rev-parse", "HEAD"], text=True, stderr=subprocess.DEVNULL
        ).strip()
        dirty = subprocess.check_output(
            ["git", "status", "--porcelain"], text=True, stderr=subprocess.DEVNULL
        ).strip()
        root = subprocess.check_output(
            ["git", "rev-parse", "--show-toplevel"], text=True, stderr=subprocess.DEVNULL
        ).strip()
    except subprocess.CalledProcessError as error:
        raise ValueError("receipt capture requires a Git checkout") from error
    if head != tested_commit or dirty:
        raise ValueError("receipt capture requires the clean tested source commit")
    relative_target = f"sdks/dart/example/{target}"
    if not Path(root, relative_target).is_file():
        raise ValueError("receipt capture target is missing from the checkout")
    try:
        subprocess.check_output(
            ["git", "cat-file", "-e", f"HEAD:{relative_target}"],
            text=True,
            stderr=subprocess.DEVNULL,
        )
    except subprocess.CalledProcessError as error:
        raise ValueError("receipt capture target is not in the tested commit") from error
    expected = Path(root, "sdks/dart/example", *BUILD_SUFFIXES[platform])
    if Path(built_binary_path).resolve() != expected.resolve():
        raise ValueError("receipt capture must use this checkout's target build")


def _bounded_command_stdout(command, *, timeout, limit, label):
    process = subprocess.Popen(
        command, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL
    )
    try:
        deadline = time.monotonic() + timeout
        data = bytearray()
        with process.stdout as output:
            while True:
                remaining = deadline - time.monotonic()
                if remaining <= 0 or not select.select([output], [], [], remaining)[0]:
                    raise subprocess.TimeoutExpired(command, timeout)
                chunk = os.read(output.fileno(), min(8192, limit + 1 - len(data)))
                if not chunk:
                    break
                data.extend(chunk)
                if len(data) > limit:
                    raise ValueError(f"{label} is oversized")
        process.wait(timeout=max(0, deadline - time.monotonic()))
        if process.returncode:
            raise subprocess.CalledProcessError(process.returncode, command)
        return bytes(data)
    finally:
        if process.poll() is None:
            try:
                process.kill()
            except ProcessLookupError:
                pass
        process.wait()


def capture_device_marker(platform, device_id=None):
    """Read the current marker from a physical app, never from operator JSON."""
    try:
        if platform == "android":
            if device_id is not None:
                raise ValueError("Android capture uses one USB-connected physical device")
            qemu = _bounded_command_stdout(
                ["adb", "-d", "shell", "getprop", "ro.kernel.qemu"],
                timeout=15, limit=MAX_DEVICE_PROBE_BYTES, label="Android device probe",
            ).strip()
            if qemu not in (b"", b"0"):
                raise ValueError("Android receipt capture requires physical hardware")
            marker = _bounded_command_stdout(
                ["adb", "-d", "exec-out", "run-as", PACKAGE_IDS["android"],
                 "cat", "cache/lantern-receipt-attestation.json"],
                timeout=30, limit=MAX_EVIDENCE_BYTES, label="on-device receipt marker",
            )
        elif platform == "ios":
            if not isinstance(device_id, str) or not device_id:
                raise ValueError("iOS receipt capture requires a private physical device ID")
            with tempfile.TemporaryDirectory() as directory:
                destination = Path(directory) / "lantern-receipt-attestation.json"
                subprocess.run(
                    ["xcrun", "devicectl", "device", "copy", "from",
                     "--device", device_id,
                     "--domain-type", "appDataContainer",
                     "--domain-identifier", PACKAGE_IDS["ios"],
                     "--source", "tmp/lantern-receipt-attestation.json",
                     "--destination", str(destination)],
                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                    check=True, timeout=60,
                )
                marker = _read_bytes_limited(destination, "on-device receipt marker")
        else:
            raise ValueError("unsupported receipt platform")
    except subprocess.TimeoutExpired:
        raise ValueError("on-device receipt marker capture timed out") from None
    except subprocess.CalledProcessError:
        raise ValueError("on-device receipt marker copy failed") from None
    except OSError:
        raise ValueError("on-device receipt marker or capture command is unavailable") from None
    if not marker or len(marker) > MAX_EVIDENCE_BYTES:
        raise ValueError("on-device receipt marker is missing or oversized")
    return marker


def validate_receipt_attestation(
    marker_path,
    record_path,
    built_binary_path,
    *,
    tested_commit,
    target,
    platform,
    required_scenarios,
    run_id,
    run_started_at,
    now=None,
):
    """Require executed, fresh on-device results bound to host-built target bytes.

    The marker must be copied from the installed app's data container. The host
    binary must be the exact APK/App.framework/App installed for this target.
    """
    if not isinstance(platform, str) or platform not in PACKAGE_IDS:
        raise ValueError("unsupported receipt platform")
    if not isinstance(tested_commit, str) or not HEX40.fullmatch(tested_commit):
        raise ValueError("tested receipt commit must be a full lowercase Git SHA")
    if (
        not isinstance(target, str)
        or not TARGET.fullmatch(target)
        or target == "integration_test/receipt_binary_probe_test.dart"
    ):
        raise ValueError("a dedicated receipt test target is required")
    if not isinstance(run_id, str) or not HEX32.fullmatch(run_id):
        raise ValueError("receipt run ID must be 32 lowercase hex characters")
    if (
        not isinstance(required_scenarios, (set, frozenset))
        or not required_scenarios
        or any(not isinstance(item, str) or not SCENARIO.fullmatch(item)
               for item in required_scenarios)
    ):
        raise ValueError("the independent receipt scenario set is required")
    run_start = _utc(run_started_at, "host run start")
    checked_at = now if now is not None else datetime.now(timezone.utc)
    if (
        not isinstance(checked_at, datetime)
        or checked_at.tzinfo is None
        or checked_at.utcoffset() != timedelta(0)
    ):
        raise ValueError("validation time must be UTC")
    if run_start > checked_at + CLOCK_SKEW:
        raise ValueError("host run start is in the future")

    built_digest = _built_digest(Path(built_binary_path), platform)
    marker = _read_object(Path(marker_path), "on-device receipt marker")
    record = _read_object(Path(record_path), "receipt evidence record")
    if set(marker) != MARKER_FIELDS or set(record) != RECORD_FIELDS:
        raise ValueError("receipt marker or record has missing or unexpected fields")
    if (
        type(marker["schema"]) is not int
        or marker["schema"] != 1
        or marker["kind"] != MARKER_KIND
        or marker["contentFree"] is not True
        or marker["status"] != "passed"
        or marker["phase"] != "complete"
    ):
        raise ValueError("on-device receipt marker did not pass after cleanup")
    if (
        type(record["schema"]) is not int
        or record["schema"] != 1
        or record["kind"] != RECORD_KIND
        or record["repository"] != REPOSITORY
        or record["contentFree"] is not True
        or record["physicalDevice"] is not True
        or record["result"] != "passed"
    ):
        raise ValueError("receipt evidence identity or result is invalid")
    application = record["application"]
    if not isinstance(application, dict) or set(application) != {
        "packageId", "target", "binarySha256",
    }:
        raise ValueError("receipt application identity is invalid")
    if marker["testedCommit"] != tested_commit or record["testedCommit"] != tested_commit:
        raise ValueError("receipt source commit differs from tested code")
    if marker["runId"] != run_id or record["runId"] != run_id:
        raise ValueError("receipt marker or record is from another run")
    if marker["target"] != target or application["target"] != target:
        raise ValueError("receipt target differs from the installed test build")
    if (
        marker["platform"] != platform
        or record["platform"] != {"kind": f"physical-{platform}"}
        or marker["packageId"] != PACKAGE_IDS[platform]
        or application["packageId"] != PACKAGE_IDS[platform]
    ):
        raise ValueError("receipt platform or installed package identity is invalid")
    if (
        not isinstance(marker["installedBinarySha256"], str)
        or not HEX64.fullmatch(marker["installedBinarySha256"])
        or marker["installedBinarySha256"] != built_digest
        or application["binarySha256"] != built_digest
    ):
        raise ValueError("receipt installed and host-built binary hashes differ")
    _scenarios(marker["completedScenarios"], required_scenarios, "on-device marker")
    _scenarios(record["scenarios"], required_scenarios, "receipt record")
    if record["scenarios"] != sorted(record["scenarios"]):
        raise ValueError("receipt record scenarios must be sorted")

    started = _utc(marker["startedAt"], "on-device start")
    finished = _utc(marker["finishedAt"], "on-device finish")
    recorded = _utc(record["recordedAt"], "host capture")
    if record["runStartedAt"] != run_started_at:
        raise ValueError("receipt record does not match the host run start")
    if not run_start - CLOCK_SKEW <= started <= run_start + LAUNCH_LIMIT:
        raise ValueError("receipt marker is stale or outside the launch window")
    if not started <= finished <= started + RUN_LIMIT:
        raise ValueError("receipt marker has invalid completion timing")
    if not finished - CLOCK_SKEW <= recorded <= finished + CAPTURE_LIMIT:
        raise ValueError("receipt marker was not freshly captured")
    if recorded < run_start or max(started, finished, recorded) > checked_at + CLOCK_SKEW:
        raise ValueError("receipt marker or record has impossible UTC timing")
    if recorded < checked_at - CAPTURE_LIMIT:
        raise ValueError("receipt record is stale for capture-time validation")
    return built_digest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--marker", required=True, type=Path)
    parser.add_argument("--record", required=True, type=Path)
    parser.add_argument("--built-binary", required=True, type=Path)
    parser.add_argument("--tested-commit", required=True)
    parser.add_argument("--target", required=True)
    parser.add_argument("--platform", required=True, choices=tuple(PACKAGE_IDS))
    parser.add_argument("--device-id", help="private physical iOS device ID; Android uses adb -d")
    parser.add_argument("--scenario", required=True, action="append")
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--run-started-at", required=True)
    args = parser.parse_args()
    if len(args.scenario) != len(set(args.scenario)):
        parser.error("receipt scenario IDs must be unique")
    try:
        assert_capture_checkout(args.tested_commit, args.built_binary, args.platform, args.target)
        if _read_bytes_limited(Path(args.marker), "on-device receipt marker") != (
            capture_device_marker(args.platform, args.device_id)
        ):
            raise ValueError("receipt marker differs from the installed device's marker")
        validate_receipt_attestation(
            args.marker,
            args.record,
            args.built_binary,
            tested_commit=args.tested_commit,
            target=args.target,
            platform=args.platform,
            required_scenarios=set(args.scenario),
            run_id=args.run_id,
            run_started_at=args.run_started_at,
        )
    except (OSError, ValueError) as error:
        parser.error(str(error))
    print("physical receipt marker, evidence and installed build bytes match")


if __name__ == "__main__":
    main()
