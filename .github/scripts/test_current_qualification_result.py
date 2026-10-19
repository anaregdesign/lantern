import unittest

from current_qualification_result import accepted


class RequiredResultTest(unittest.TestCase):
    def setUp(self):
        self.good = dict(SCOPE_RESULT="success", MODULE_RESULT="success", FUNCTIONAL_VALIDATION_RESULT="success", CONFIGURED_SOURCE_INTEGRATION_RESULT="success", CONFIGURED_SOURCE_INTEGRATION_REQUIRED="true",
                         WINDOWS_REQUIRED="true", WINDOWS_RESULT="success")

    def test_all_required_pass(self):
        self.assertTrue(accepted(self.good))

    def test_failed_cancelled_missing_or_skipped_required_result_refuses(self):
        for key in ("SCOPE_RESULT", "MODULE_RESULT", "FUNCTIONAL_VALIDATION_RESULT", "CONFIGURED_SOURCE_INTEGRATION_RESULT", "WINDOWS_RESULT"):
            for value in ("failure", "cancelled", "skipped", "", "unknown"):
                self.assertFalse(accepted(self.good | {key: value}), (key, value))

    def test_only_classified_source_integration_not_run_is_permitted(self):
        self.assertTrue(accepted(self.good | {"CONFIGURED_SOURCE_INTEGRATION_REQUIRED": "false", "CONFIGURED_SOURCE_INTEGRATION_RESULT": "skipped"}))
        for required in ("", "unknown", "true", "False"):
            self.assertFalse(accepted(self.good | {"CONFIGURED_SOURCE_INTEGRATION_REQUIRED": required, "CONFIGURED_SOURCE_INTEGRATION_RESULT": "skipped"}))
        for result in ("failure", "cancelled", "", "unknown"):
            self.assertFalse(accepted(self.good | {"CONFIGURED_SOURCE_INTEGRATION_REQUIRED": "false", "CONFIGURED_SOURCE_INTEGRATION_RESULT": result}))

    def test_retired_shorthand_results_cannot_satisfy_the_aggregate(self):
        retired = {key: value for key, value in self.good.items()
                   if key not in ("FUNCTIONAL_VALIDATION_RESULT",
                                  "CONFIGURED_SOURCE_INTEGRATION_RESULT",
                                  "CONFIGURED_SOURCE_INTEGRATION_REQUIRED")}
        retired.update(F_RESULT="success", Q_RESULT="success", Q_REQUIRED="true")
        self.assertFalse(accepted(retired))

    def test_existing_docs_evidence_exemption_does_not_hide_executed_failure(self):
        for key in ("DOCS_ONLY", "EVIDENCE_ONLY"):
            exempt = self.good | {key: "true", "FUNCTIONAL_VALIDATION_RESULT": "skipped", "CONFIGURED_SOURCE_INTEGRATION_RESULT": "skipped", "WINDOWS_RESULT": "skipped"}
            self.assertTrue(accepted(exempt))
            self.assertFalse(accepted(exempt | {"CONFIGURED_SOURCE_INTEGRATION_RESULT": "failure"}))
            self.assertFalse(accepted(exempt | {"MODULE_RESULT": "skipped"}))

    def test_windows_omission_is_explicit_and_never_hides_execution_failure(self):
        self.assertTrue(accepted(self.good | {"WINDOWS_REQUIRED": "false", "WINDOWS_RESULT": "skipped"}))
        for result in ("failure", "cancelled", "", "unknown", "success"):
            self.assertFalse(accepted(self.good | {"WINDOWS_REQUIRED": "false", "WINDOWS_RESULT": result}))
        self.assertFalse(accepted(self.good | {"WINDOWS_REQUIRED": ""}))


if __name__ == "__main__":
    unittest.main()
