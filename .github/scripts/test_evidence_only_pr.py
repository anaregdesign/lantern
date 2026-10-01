"""Evidence-only PR selection and fast validation regressions."""

import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

import evidence_only_pr as candidate


sys.path.insert(0, str(candidate.ROOT / "sdks/dart/offline/tool"))
import physical_release_gate as gate
from physical_release_gate_test import TESTED, fixtures, receipt_marker


class EvidenceOnlyPRTest(unittest.TestCase):
    def test_exact_eight_files_only(self):
        changed = dict.fromkeys(candidate.REQUIRED, "A")
        self.assertTrue(candidate.is_candidate(changed))
        for path in candidate.REQUIRED:
            with self.subTest(missing=path):
                self.assertFalse(candidate.is_candidate(changed | {path: "D"}))
                self.assertFalse(candidate.is_candidate({k: v for k, v in changed.items() if k != path}))
        self.assertFalse(candidate.is_candidate(changed | {"server/service/service.go": "M"}))
        self.assertFalse(candidate.is_candidate(changed | {".github/workflows/go.yml": "M"}))
        self.assertFalse(candidate.is_candidate(changed | {"sdks/dart/example/evidence/offline-release/README.md": "M"}))
        self.assertFalse(candidate.is_candidate(changed | {"sdks/dart/example/evidence/offline-release/renamed.json": "A"}))
        self.assertFalse(candidate.is_candidate(changed | {next(iter(changed)): "R100"}))
        self.assertTrue(candidate.is_candidate(dict.fromkeys(candidate.REQUIRED, "M")))

    def test_valid_evidence_and_malformed_pair(self):
        tested = TESTED
        with tempfile.TemporaryDirectory() as directory:
            evidence = Path(directory)
            # Synthetic fixtures follow the current contract; historical release
            # records must remain tied to the scenarios they actually observed.
            for platform in ("android", "ios"):
                for suite, contract in gate.SUITES.items():
                    record, _ = fixtures(platform, suite)
                    (evidence / f"{platform}{contract['suffix']}.json").write_text(
                        json.dumps(record)
                    )
                    if suite == "receipt":
                        (evidence / f"{platform}-receipt-marker.json").write_text(
                            json.dumps(receipt_marker(platform, record))
                        )
            with patch.object(gate, "source_identity") as identity:
                gate.validate_pr_evidence("a" * 40, tested, evidence)
                identity.assert_called_once_with("a" * 40, tested)
            cdc_path = evidence / "android-cdc.json"
            current_cdc = cdc_path.read_text()
            old_cdc = json.loads(current_cdc)
            old_cdc["scenarios"].remove("identity_contribution_delete_refetch")
            cdc_path.write_text(json.dumps(old_cdc))
            with patch.object(gate, "source_identity"), self.assertRaisesRegex(
                ValueError, "matrix is incomplete"
            ):
                gate.validate_pr_evidence("a" * 40, tested, evidence)
            cdc_path.write_text(current_cdc)
            marker = evidence / "android-receipt-marker.json"
            marker.write_text('{"status":"passed","status":"failed"}')
            with patch.object(gate, "source_identity"), self.assertRaises(ValueError):
                gate.validate_pr_evidence("a" * 40, tested, evidence)
            marker.unlink()
            with patch.object(gate, "source_identity"), self.assertRaises(ValueError):
                gate.validate_pr_evidence("a" * 40, tested, evidence)


if __name__ == "__main__":
    unittest.main()
