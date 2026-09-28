from datetime import datetime, timedelta, timezone
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from physical_result_attestation import validate_private_result


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

    def validate(self, suite="smoke"):
        return validate_private_result(
            self.marker, self.record, self.binary,
            platform="android", suite=suite,
            launched_after=self.launched,
        )

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
