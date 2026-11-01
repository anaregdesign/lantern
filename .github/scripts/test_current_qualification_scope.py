import contextlib
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import current_qualification_scope as scope


class QualificationScopeTest(unittest.TestCase):
    def test_classifier_exports_the_descriptive_ci_output(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "output"
            summary = Path(directory) / "summary"
            for required in (False, True):
                with self.subTest(required=required), \
                        patch.dict(os.environ, {"GITHUB_OUTPUT": str(output),
                                                "GITHUB_STEP_SUMMARY": str(summary)}, clear=True), \
                        patch.object(scope, "classify", return_value=(required, "fixture")), \
                        contextlib.redirect_stdout(io.StringIO()) as stdout:
                    output.write_text("")
                    scope.main()
                self.assertEqual(output.read_text(),
                                 f"configured_source_integration_required={str(required).lower()}\n")
                self.assertEqual(json.loads(stdout.getvalue()),
                                 {"configured_source_integration_required": required,
                                  "reason": "fixture"})

    def test_only_reviewed_modified_application_sources_can_omit_source_integration(self):
        for path in ("admin/app/components/Card.tsx", "sdks/dart/offline/lib/store.dart", "sdks/dart/offline_sqlite/lib/store.dart",
                     "core/collection/map.go", "core/nlp/tokenizer.go", "core/model/model.go", "core/search/index_test.go",
                     "server/provider/pprof.go", "server/provider/runtime_profiling_test.go"):
            self.assertFalse(scope.requires_qualification({path: "M"}))
            for status in ("A", "D", "R100", "T", "U", ""):
                self.assertTrue(scope.requires_qualification({path: status}))

    def test_unknown_production_dependency_and_proof_changes_require_source_integration(self):
        for path in ("server/internal/security/control_time_owner.go", "server/provider/security_current_runtime.go",
                     "server/internal/dataruntime/runtime.go", "server/internal/dataruntime/receipt_wal_recovery.go",
                     "server/internal/dataruntime/publication_gate.go", "server/internal/dataruntime/snapshot_tail.go",
                     "server/internal/config/network.go", "server/internal/config/observability.go",
                     "server/internal/config/search_test.go", "server/internal/config/lifecycle.go",
                     "core/cache/cache.go", "server/provider/public_metrics.go", "server/provider/provider.go",
                     "server/internal/authn/session.go", "core/hlc/clock.go", "core/mutationreceipt/clock_journal.go",
                     "go.mod", "go.work", "sdks/node/bun.lock", "admin/package.json",
                     "admin/app/new.yaml", "admin/app/../../server/x.ts", "/admin/app/a.ts",
                     "tests/integration/current_security_gate_test.go", ".github/scripts/current_sdk4_gate.py",
                     ".github/scripts/current_qualification_scope.py", ".github/workflows/go.yml", "Dockerfile",
                     "AGENTS.md", "CONTRIBUTING.md", "unknown.file"):
            self.assertTrue(scope.requires_qualification({path: "M"}), path)
        self.assertTrue(scope.requires_qualification({}))
        self.assertTrue(scope.requires_qualification({"admin/app/Card.tsx": "M", "go.sum": "M"}))

    def test_known_harness_closure_does_not_hide_mixed_or_new_boundaries(self):
        for path in scope.validation_scope.HARNESS_INPUTS:
            self.assertFalse(scope.requires_qualification({path: "M"}), path)
            for status in ("A", "D", "T", "R100"):
                self.assertTrue(scope.requires_qualification({path: status}), (path, status))
            for boundary in ("go.mod", "server/internal/security/control.go",
                             "testbed/bench/local_time.sh", ".github/scripts/unknown_reader.py"):
                self.assertTrue(scope.requires_qualification({path: "M", boundary: "M"}), boundary)

    def test_known_harness_still_requires_regular_inputs(self):
        path = ".github/scripts/bench_time_profile.py"
        with patch.object(scope.ci_docs, "change_range", return_value=("base", "head")), \
                patch.object(scope.evidence_only_pr, "changed_files", return_value={path: "M"}), \
                patch.object(scope.evidence_only_pr, "git", return_value="120000 blob abc\\t" + path):
            self.assertTrue(scope.classify({})[0])

    def test_missing_base_unsupported_event_and_classifier_errors_require_source_integration(self):
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
