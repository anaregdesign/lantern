from datetime import datetime, timezone
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import physical_release_gate as gate


TESTED = "a" * 40
TAG = "b" * 40
REVISION = "c" * 40


def fixtures(platform):
    package = (
        "com.anaregdesign.lantern_example" if platform == "android"
        else "com.anaregdesign.lanternExample"
    )
    ci = {
        "schema": 1, "kind": "ci_mobile_revision_evidence",
        "repository": "anaregdesign/lantern", "contentFree": True,
        "physicalDevice": False, "result": "passed", "commit": TAG,
        "ref": "refs/tags/sdks/dart/offline/v0.2.0",
        "workflow": {"runId": "123", "attempt": "1"},
        "toolchain": {"flutter": "3.44.6", "flutterRevision": REVISION, "dart": "3.12.2"},
        "application": {"packageId": package},
        "platform": {"kind": "android-emulator" if platform == "android" else "ios-simulator"},
    }
    record = {
        "schema": 1, "kind": "physical_offline_release_evidence",
        "repository": "anaregdesign/lantern", "testedCommit": TESTED,
        "recordedAt": datetime.now(timezone.utc).isoformat(),
        "physicalDevice": True, "contentFree": True, "cleanCheckout": True,
        "toolchain": ci["toolchain"],
        "application": {"packageId": package,
                        "target": "integration_test/mobile_smoke_test.dart",
                        "binarySha256": "d" * 64},
        "platform": {"kind": f"physical-{platform}", "model": "test model", "os": "test OS"},
        "network": {"transport": "Connect/HTTPS", "topology": "trusted private LAN",
                    "authenticated": True, "platformTrustedTls": True},
        "scenarios": sorted(gate.REQUIRED_COMMON | {gate.REQUIRED_PLATFORM[platform]}),
        "result": "passed", "limitations": [],
    }
    return record, ci


class PhysicalReleaseGateTest(unittest.TestCase):
    def test_both_platforms_must_match_the_tag_and_tested_code(self):
        with tempfile.TemporaryDirectory() as directory:
            evidence = Path(directory) / "physical"
            ci_dir = Path(directory) / "ci"
            evidence.mkdir()
            ci_dir.mkdir()
            for platform in ("android", "ios"):
                record, ci = fixtures(platform)
                (evidence / f"{platform}.json").write_text(json.dumps(record))
                (ci_dir / f"{platform}.json").write_text(json.dumps(ci))
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

    def test_complete_android_and_ios_records_pass(self):
        for platform in ("android", "ios"):
            with self.subTest(platform=platform):
                gate.validate_record(*fixtures(platform), platform, TESTED)

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

    def test_tag_can_only_add_evidence_to_tested_code(self):
        valid_diff = "\n".join(str(gate.EVIDENCE_DIR / f"{platform}.json")
                               for platform in ("android", "ios"))
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
