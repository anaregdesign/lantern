from datetime import datetime, timedelta, timezone
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import physical_release_gate as gate
from physical_receipt_attestation import (
    MAX_EVIDENCE_BYTES,
    validate_archived_receipt_evidence,
)


TESTED = "a" * 40
TAG = "b" * 40
REVISION = "c" * 40


def fixtures(platform, suite="smoke"):
    package = (
        "com.anaregdesign.lantern_example" if platform == "android"
        else "com.anaregdesign.lanternExample"
    )
    ci = {
        "schema": 1, "kind": "ci_mobile_revision_evidence",
        "repository": "anaregdesign/lantern", "contentFree": True,
        "physicalDevice": False, "result": "passed", "commit": TAG,
        "ref": "refs/tags/sdks/dart/offline/v0.3.0",
        "workflow": {"runId": "123", "attempt": "1"},
        "toolchain": {"flutter": "3.44.6", "flutterRevision": REVISION, "dart": "3.12.2"},
        "application": {"packageId": package},
        "platform": {"kind": "android-emulator" if platform == "android" else "ios-simulator"},
    }
    record = {
        "schema": 1, "kind": gate.SUITES[suite]["kind"],
        "repository": "anaregdesign/lantern", "testedCommit": TESTED,
        "recordedAt": datetime.now(timezone.utc).isoformat(),
        "physicalDevice": True, "contentFree": True, "cleanCheckout": True,
        "toolchain": ci["toolchain"],
        "application": {"packageId": package,
                        "target": gate.SUITES[suite]["target"],
                        "binarySha256": "d" * 64},
        "platform": {"kind": f"physical-{platform}", "model": "test model", "os": "test OS"},
        "network": {"transport": "Connect/HTTPS", "topology": "trusted private LAN",
                    "authenticated": True, "platformTrustedTls": True},
        "scenarios": sorted(
            gate.REQUIRED_COMMON | {gate.REQUIRED_PLATFORM[platform]}
            if suite == "smoke" else gate.REQUIRED_CDC
        ),
        "result": "passed", "limitations": [],
    }
    if suite == "receipt":
        scenarios = gate.REQUIRED_RECEIPT_COMMON | {
            gate.REQUIRED_RECEIPT_PLATFORM[platform]
        }
        started = datetime.now(timezone.utc).replace(microsecond=0)
        record = {
            "schema": 1, "kind": gate.SUITES["receipt"]["kind"],
            "repository": "anaregdesign/lantern",
            "contentFree": True, "physicalDevice": True,
            "testedCommit": TESTED,
            "runId": ("a" if platform == "android" else "b") * 32,
            "runStartedAt": _utc(started - timedelta(minutes=3)),
            "recordedAt": _utc(started),
            "platform": {"kind": f"physical-{platform}"},
            "application": {
                "packageId": package,
                "target": gate.SUITES["receipt"]["target"],
                "binarySha256": "d" * 64,
            },
            "network": {
                "transport": "Connect/HTTPS", "authenticated": True,
                "platformTrustedTls": True,
                "fault": "committed-response-socket-drop",
            },
            "scenarios": sorted(scenarios), "result": "passed",
        }
    return record, ci


def _utc(value):
    return value.isoformat().replace("+00:00", "Z")


def receipt_marker(platform, record):
    start = datetime.fromisoformat(record["runStartedAt"].replace("Z", "+00:00"))
    return {
        "schema": 2, "kind": "physical_receipt_attestation",
        "contentFree": True, "testedCommit": TESTED,
        "target": gate.SUITES["receipt"]["target"], "runId": record["runId"],
        "platform": platform, "packageId": record["application"]["packageId"],
        "installedBinarySha256": record["application"]["binarySha256"],
        "startedAt": _utc(start + timedelta(seconds=10)),
        "completedScenarios": record["scenarios"],
        "status": "passed", "phase": "complete",
        "restart": {
            "preparedAt": _utc(start + timedelta(seconds=45)),
            "resumedAt": _utc(start + timedelta(seconds=65)),
            "processChanged": True,
        },
        "finishedAt": _utc(start + timedelta(seconds=90)),
    }


class PhysicalReleaseGateTest(unittest.TestCase):
    def _write_evidence(self, directory):
        evidence = Path(directory) / "physical"
        ci_dir = Path(directory) / "ci"
        evidence.mkdir()
        ci_dir.mkdir()
        for platform in ("android", "ios"):
            _, ci = fixtures(platform)
            (ci_dir / f"{platform}.json").write_text(json.dumps(ci))
            for suite, contract in gate.SUITES.items():
                record, _ = fixtures(platform, suite)
                (evidence / f"{platform}{contract['suffix']}.json").write_text(
                    json.dumps(record)
                )
                if suite == "receipt":
                    (evidence / f"{platform}-receipt-marker.json").write_text(
                        json.dumps(receipt_marker(platform, record))
                    )
        return evidence, ci_dir

    def test_both_platforms_must_match_the_tag_and_tested_code(self):
        with tempfile.TemporaryDirectory() as directory:
            evidence, ci_dir = self._write_evidence(directory)
            with patch.object(gate, "source_identity") as source_identity:
                gate.validate(TAG, evidence, ci_dir, "123", "1")
                source_identity.assert_called_once_with(TAG, TESTED)
            ci = json.loads((ci_dir / "ios.json").read_text())
            ci["commit"] = "f" * 40
            (ci_dir / "ios.json").write_text(json.dumps(ci))
            with self.assertRaisesRegex(ValueError, "not bound to the tag"):
                gate.validate(TAG, evidence, ci_dir, "123", "1")
            ci["commit"] = TAG
            ci["workflow"]["attempt"] = "0"
            (ci_dir / "ios.json").write_text(json.dumps(ci))
            with self.assertRaisesRegex(ValueError, "another CI attempt"):
                gate.validate(TAG, evidence, ci_dir, "123", "1")
            ci["workflow"]["attempt"] = "1"
            (ci_dir / "ios.json").write_text(json.dumps(ci))
            (evidence / "ios-cdc.json").unlink()
            with self.assertRaisesRegex(ValueError, "missing ios cdc physical record"):
                gate.validate(TAG, evidence, ci_dir, "123", "1")

    def test_complete_android_and_ios_records_pass(self):
        for platform in ("android", "ios"):
            for suite in ("smoke", "cdc"):
                with self.subTest(platform=platform, suite=suite):
                    gate.validate_record(*fixtures(platform, suite), platform, TESTED, suite)

    def test_receipt_release_requires_the_actual_phased_marker(self):
        with tempfile.TemporaryDirectory() as directory:
            evidence, ci_dir = self._write_evidence(directory)
            record, _ = fixtures("android", "receipt")
            self.assertEqual(
                validate_archived_receipt_evidence(
                    evidence / "android-receipt-marker.json",
                    evidence / "android-receipt.json",
                    tested_commit=TESTED, platform="android",
                    required_scenarios=gate.REQUIRED_RECEIPT_COMMON | {
                        gate.REQUIRED_RECEIPT_PLATFORM["android"]
                    },
                ),
                record["application"]["binarySha256"],
            )
            with patch.object(gate, "source_identity"):
                gate.validate(TAG, evidence, ci_dir, "123", "1")
            (evidence / "ios-receipt-marker.json").unlink()
            with patch.object(gate, "source_identity"), self.assertRaisesRegex(
                ValueError, "missing on-device receipt marker",
            ):
                gate.validate(TAG, evidence, ci_dir, "123", "1")

    def test_receipt_release_rejects_incomplete_marker_and_reused_run(self):
        with tempfile.TemporaryDirectory() as directory:
            evidence, ci_dir = self._write_evidence(directory)
            path = evidence / "android-receipt-marker.json"
            marker = json.loads(path.read_text())
            marker["completedScenarios"].remove("receipt_relaunch_status_first_no_resend")
            path.write_text(json.dumps(marker))
            with patch.object(gate, "source_identity"), self.assertRaisesRegex(
                ValueError, "required receipt scenarios",
            ):
                gate.validate(TAG, evidence, ci_dir, "123", "1")

            record = json.loads((evidence / "android-receipt.json").read_text())
            marker["completedScenarios"] = record["scenarios"]
            path.write_text(json.dumps(marker))
            ios_record = json.loads((evidence / "ios-receipt.json").read_text())
            ios_marker_path = evidence / "ios-receipt-marker.json"
            ios_marker = json.loads(ios_marker_path.read_text())
            ios_record["runId"] = record["runId"]
            ios_marker["runId"] = record["runId"]
            (evidence / "ios-receipt.json").write_text(json.dumps(ios_record))
            ios_marker_path.write_text(json.dumps(ios_marker))
            with patch.object(gate, "source_identity"), self.assertRaisesRegex(
                ValueError, "reused a run ID",
            ):
                gate.validate(TAG, evidence, ci_dir, "123", "1")

    def test_receipt_release_rejects_duplicate_oversized_or_linked_records(self):
        with tempfile.TemporaryDirectory() as directory:
            evidence, ci_dir = self._write_evidence(directory)
            path = evidence / "android-receipt.json"
            original = path.read_text()
            path.write_text(original.replace('"schema": 1', '"schema": 2, "schema": 1', 1))
            with self.assertRaisesRegex(ValueError, "duplicate receipt JSON field"):
                gate.validate(TAG, evidence, ci_dir, "123", "1")
            path.write_bytes(b"x" * (MAX_EVIDENCE_BYTES + 1))
            with self.assertRaisesRegex(ValueError, "oversized"):
                gate.validate(TAG, evidence, ci_dir, "123", "1")
            path.unlink()
            external = Path(directory) / "linked-receipt.json"
            external.write_text(original)
            path.symlink_to(external)
            with self.assertRaisesRegex(ValueError, "missing receipt evidence"):
                gate.validate(TAG, evidence, ci_dir, "123", "1")

    def test_incomplete_or_debug_insecure_records_fail(self):
        for change in (
            lambda record: record["scenarios"].remove("platform_trusted_tls"),
            lambda record: record["network"].update(transport="Connect/h2c"),
            lambda record: record["network"].update(topology="https://private.example"),
            lambda record: record["limitations"].append("iOS privacy not tested"),
        ):
            record, ci = fixtures("android")
            change(record)
            with self.subTest(change=change), self.assertRaises(ValueError):
                gate.validate_record(record, ci, "android", TESTED)

    def test_cdc_record_rejects_wrong_target_or_incomplete_scenarios(self):
        record, ci = fixtures("android", "cdc")
        record["application"]["target"] = "integration_test/mobile_smoke_test.dart"
        with self.assertRaisesRegex(ValueError, "application identity"):
            gate.validate_record(record, ci, "android", TESTED, "cdc")
        record, ci = fixtures("android", "cdc")
        record["scenarios"].remove("identity_live_edge_invalidation")
        with self.assertRaisesRegex(ValueError, "release matrix"):
            gate.validate_record(record, ci, "android", TESTED, "cdc")

    def test_tag_can_only_add_evidence_to_tested_code(self):
        valid_diff = "\n".join(
            str(gate.EVIDENCE_DIR / f"{platform}{contract['suffix']}.json")
            for platform in ("android", "ios") for contract in gate.SUITES.values()
        )
        valid_diff += "\n" + "\n".join(
            str(gate.EVIDENCE_DIR / f"{platform}-receipt-marker.json")
            for platform in ("android", "ios")
        )
        with patch.object(gate, "git", side_effect=[TESTED, valid_diff]):
            gate.source_identity(TAG, TESTED)
        with patch.object(gate, "git", side_effect=[TESTED, valid_diff + "\nsdks/dart/offline/lib/remote.dart"]):
            with self.assertRaisesRegex(ValueError, "changed code"):
                gate.source_identity(TAG, TESTED)
        with patch.object(gate, "git", return_value="f" * 40):
            with self.assertRaisesRegex(ValueError, "evidence-only child"):
                gate.source_identity(TAG, TESTED)


if __name__ == "__main__":
    unittest.main()
