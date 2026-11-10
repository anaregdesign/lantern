from datetime import datetime, timedelta, timezone
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from physical_result_attestation import validate_private_result
import physical_receipt_attestation as attestation

fixture_tools = attestation.fixture_provenance_tools()


def utc(value):
    return value.isoformat().replace("+00:00", "Z")


class PhysicalResultAttestationTest(unittest.TestCase):
    def setUp(self):
        self.sandbox = tempfile.TemporaryDirectory()
        self.addCleanup(self.sandbox.cleanup)
        root = Path(self.sandbox.name)
        self.marker = root / "marker.json"
        self.record = root / "record.json"
        self.binary = root / "signed.apk"
        self.binary.write_bytes(b"signed installed application bytes")
        self.digest = hashlib.sha256(self.binary.read_bytes()).hexdigest()
        now = datetime.now(timezone.utc)
        self.launched = utc(now - timedelta(minutes=2))
        self.marker_data = {
            "schema": 1, "kind": "physical_mobile_smoke_on_device_result",
            "contentFree": True, "status": "passed", "phase": "complete",
            "startedAt": utc(now - timedelta(minutes=1)),
            "updatedAt": utc(now - timedelta(seconds=10)),
            "platform": "android",
            "packageId": "com.anaregdesign.lantern_example",
            "installedBinarySha256": self.digest,
        }
        self.record_data = {
            "schema": 1, "testedCommit": "a" * 40,
            "kind": "physical_offline_release_evidence",
            "result": "passed", "recordedAt": utc(now),
            "platform": {"kind": "physical-android"},
            "application": {
                "packageId": "com.anaregdesign.lantern_example",
                "target": "integration_test/mobile_smoke_test.dart",
                "binarySha256": self.digest,
            },
        }
        self.save()

    def save(self):
        self.marker.write_text(json.dumps(self.marker_data))
        self.record.write_text(json.dumps(self.record_data))

    def validate(self, suite="smoke", **options):
        return validate_private_result(
            self.marker, self.record, self.binary,
            platform="android", suite=suite,
            launched_after=self.launched,
            **options,
        )

    def controlled(self):
        self.record_data["schema"] = 2
        start = datetime.fromisoformat(self.marker_data["startedAt"].replace("Z", "+00:00"))
        finish = datetime.fromisoformat(self.marker_data["updatedAt"].replace("Z", "+00:00"))
        self.record_data["fixture"] = {
            "schema": 1, "profile": "physical", "sourceCommit": "a" * 40,
            "sourceKind": "controlled-source", "buildKind": "test-only",
            "buildTags": ["lantern_test_time"], "syntheticUtc": True,
            "imageSha256": "1" * 64, "descriptorSha256": "2" * 64,
            "receiptSha256": "3" * 64, "sidecarSha256": "4" * 64,
            "preparedAt": utc(start - timedelta(seconds=1)),
            "servingUntil": utc(finish + timedelta(seconds=1)),
            "lifecycleFinishedAt": utc(finish + timedelta(seconds=1)),
            "servingStoppedAt": utc(finish),
            "deviceStartedAt": self.marker_data["startedAt"], "deviceFinishedAt": self.marker_data["updatedAt"],
            "phoneMarkerSha256": hashlib.sha256(json.dumps(self.marker_data).encode()).hexdigest(),
            "recordPayloadSha256": fixture_tools.digest(fixture_tools.encoded(self.record_data)),
        }
        self.save()
        return self.record_data["fixture"]

    def test_controlled_capture_requires_matching_final_private_sidecar(self):
        summary = self.controlled()
        with self.assertRaisesRegex(ValueError, "requires --fixture-provenance"):
            self.validate()
        with patch.object(fixture_tools, "public_summary", return_value=summary) as verify:
            self.assertEqual(self.validate(fixture_provenance=self.record.parent / "sidecar"), self.digest)
            self.assertEqual(verify.call_args.args[1:3], ("a" * 40, "physical"))
        self.marker_data["updatedAt"] = self.marker_data["startedAt"]
        self.save()
        with self.assertRaisesRegex(ValueError, "capture binding mismatch"):
            self.validate(fixture_provenance=self.record.parent / "sidecar")

    def test_legacy_normal_capture_rejects_sidecar_and_schema2_rejects_missing_summary(self):
        with self.assertRaisesRegex(ValueError, "legacy record cannot claim"):
            self.validate(fixture_provenance=self.record.parent / "sidecar")
        self.record_data["schema"] = 2
        self.save()
        with self.assertRaises(ValueError):
            self.validate(fixture_provenance=self.record.parent / "sidecar")

    def test_signed_smoke_and_cdc_match_private_marker(self):
        self.assertEqual(self.validate(), self.digest)
        self.marker_data["kind"] = "physical_identity_cdc_on_device_result"
        self.record_data["kind"] = "physical_offline_identity_cdc_evidence"
        self.record_data["application"]["target"] = (
            "integration_test/physical_identity_cdc_test.dart"
        )
        self.save()
        self.assertEqual(self.validate("cdc"), self.digest)

    def test_intermediate_or_unqualified_marker_cannot_pass(self):
        del self.marker_data["installedBinarySha256"]
        self.save()
        with self.assertRaisesRegex(ValueError, "qualified final pass"):
            self.validate()
        self.marker_data["installedBinarySha256"] = self.digest
        self.marker_data["phase"] = "live_invalidation"
        self.save()
        with self.assertRaisesRegex(ValueError, "qualified final pass"):
            self.validate()

    def test_boolean_schema_cannot_stand_in_for_version_one(self):
        self.marker_data["schema"] = True
        self.save()
        with self.assertRaisesRegex(ValueError, "qualified final pass"):
            self.validate()

    def test_signed_bytes_and_public_record_must_match_device(self):
        self.binary.write_bytes(b"different signed build")
        with self.assertRaisesRegex(ValueError, "signed build bytes differ"):
            self.validate()
        self.binary.write_bytes(b"signed installed application bytes")
        self.record_data["application"]["binarySha256"] = "f" * 64
        self.save()
        with self.assertRaisesRegex(ValueError, "record differs"):
            self.validate()

    def test_malformed_record_platform_fails_closed(self):
        self.record_data["platform"] = "android"
        self.save()
        with self.assertRaisesRegex(ValueError, "record differs"):
            self.validate()

    def test_stale_or_duplicate_marker_fails(self):
        self.marker_data["startedAt"] = utc(
            datetime.fromisoformat(self.launched.replace("Z", "+00:00"))
            - timedelta(minutes=1)
        )
        self.save()
        with self.assertRaisesRegex(ValueError, "stale"):
            self.validate()
        self.marker.write_text(
            self.marker.read_text().replace('"status": "passed"',
                                            '"status": "passed", "status": "passed"')
        )
        with self.assertRaisesRegex(ValueError, "duplicate physical result"):
            self.validate()


if __name__ == "__main__":
    unittest.main()
