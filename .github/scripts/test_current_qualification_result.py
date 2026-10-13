import unittest

from current_qualification_result import accepted


class RequiredResultTest(unittest.TestCase):
    def setUp(self):
        self.good = dict(SCOPE_RESULT="success", MODULE_RESULT="success", F_RESULT="success", Q_RESULT="success", Q_REQUIRED="true")

    def test_all_required_pass(self):
        self.assertTrue(accepted(self.good))

    def test_failed_cancelled_missing_or_skipped_required_result_refuses(self):
        for key in ("SCOPE_RESULT", "MODULE_RESULT", "F_RESULT", "Q_RESULT"):
            for value in ("failure", "cancelled", "skipped", "", "unknown"):
                self.assertFalse(accepted(self.good | {key: value}), (key, value))

    def test_only_classified_q_not_run_is_permitted(self):
        self.assertTrue(accepted(self.good | {"Q_REQUIRED": "false", "Q_RESULT": "skipped"}))
        for required in ("", "unknown", "true", "False"):
            self.assertFalse(accepted(self.good | {"Q_REQUIRED": required, "Q_RESULT": "skipped"}))
        for result in ("failure", "cancelled", "", "unknown"):
            self.assertFalse(accepted(self.good | {"Q_REQUIRED": "false", "Q_RESULT": result}))

    def test_existing_docs_evidence_exemption_does_not_hide_executed_failure(self):
        for key in ("DOCS_ONLY", "EVIDENCE_ONLY"):
            exempt = self.good | {key: "true", "F_RESULT": "skipped", "Q_RESULT": "skipped"}
            self.assertTrue(accepted(exempt))
            self.assertFalse(accepted(exempt | {"Q_RESULT": "failure"}))
            self.assertFalse(accepted(exempt | {"MODULE_RESULT": "skipped"}))


if __name__ == "__main__":
    unittest.main()
