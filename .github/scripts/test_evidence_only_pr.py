"""Evidence-only PR selection and fast validation regressions."""

import json
from pathlib import Path
import shutil
import sys
import tempfile
import unittest
from unittest.mock import patch

import evidence_only_pr as candidate


sys.path.insert(0, str(candidate.ROOT / "sdks/dart/offline/tool"))
import physical_release_gate as gate


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
        source = candidate.ROOT / candidate.EVIDENCE_DIR
        tested = json.loads((source / "android.json").read_text())["testedCommit"]
        with tempfile.TemporaryDirectory() as directory:
            evidence = Path(directory)
            for path in candidate.REQUIRED:
                shutil.copyfile(source / Path(path).name, evidence / Path(path).name)
            with patch.object(gate, "source_identity") as identity:
                gate.validate_pr_evidence("a" * 40, tested, evidence)
                identity.assert_called_once_with("a" * 40, tested)
            marker = evidence / "android-receipt-marker.json"
            marker.write_text('{"status":"passed","status":"failed"}')
            with patch.object(gate, "source_identity"), self.assertRaises(ValueError):
                gate.validate_pr_evidence("a" * 40, tested, evidence)
            marker.unlink()
            with patch.object(gate, "source_identity"), self.assertRaises(ValueError):
                gate.validate_pr_evidence("a" * 40, tested, evidence)


if __name__ == "__main__":
    unittest.main()
