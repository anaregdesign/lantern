import unittest
from unittest.mock import patch

import current_qualification_scope as scope


class QualificationScopeTest(unittest.TestCase):
    def test_only_reviewed_modified_application_sources_can_omit_q(self):
        for path in ("admin/app/components/Card.tsx", "sdks/dart/offline/lib/store.dart", "sdks/dart/offline_sqlite/lib/store.dart"):
            self.assertFalse(scope.requires_qualification({path: "M"}))
            for status in ("A", "D", "R100", "T", "U", ""):
                self.assertTrue(scope.requires_qualification({path: status}))

    def test_unknown_production_dependency_and_proof_changes_require_q(self):
        for path in ("server/internal/security/control_time_owner.go", "server/provider/security_current_runtime.go",
                     "core/cache/cache.go", "go.mod", "go.work", "sdks/node/bun.lock", "admin/package.json",
                     "admin/app/new.yaml", "admin/app/../../server/x.ts", "/admin/app/a.ts",
                     "tests/integration/current_security_gate_test.go", ".github/scripts/current_sdk4_gate.py",
                     ".github/scripts/current_qualification_scope.py", ".github/workflows/go.yml", "Dockerfile",
                     "AGENTS.md", "CONTRIBUTING.md", "unknown.file"):
            self.assertTrue(scope.requires_qualification({path: "M"}), path)
        self.assertTrue(scope.requires_qualification({}))
        self.assertTrue(scope.requires_qualification({"admin/app/Card.tsx": "M", "go.sum": "M"}))

    def test_missing_base_unsupported_event_and_classifier_errors_require_q(self):
        self.assertTrue(scope.classify({})[0])
        self.assertTrue(scope.classify({"GITHUB_EVENT_NAME": "push", "GITHUB_SHA": "a", "PUSH_BEFORE_SHA": "0" * 40})[0])
        with patch.object(scope.ci_docs, "change_range", side_effect=ValueError("missing base")):
            self.assertTrue(scope.classify({})[0])

    def test_regular_file_type_is_required_at_both_revisions(self):
        with patch.object(scope.ci_docs, "change_range", return_value=("base", "head")), \
                patch.object(scope.evidence_only_pr, "changed_files", return_value={"admin/app/Card.tsx": "M"}), \
                patch.object(scope.evidence_only_pr, "git", return_value="100644 blob abc\tadmin/app/Card.tsx"):
            self.assertFalse(scope.classify({})[0])
        with patch.object(scope.ci_docs, "change_range", return_value=("base", "head")), \
                patch.object(scope.evidence_only_pr, "changed_files", return_value={"admin/app/Card.tsx": "M"}), \
                patch.object(scope.evidence_only_pr, "git", return_value="120000 blob abc\tadmin/app/Card.tsx"):
            self.assertTrue(scope.classify({})[0])


if __name__ == "__main__":
    unittest.main()
