"""Singleflight evidence cannot convert skipped, malformed or private output to PASS."""

import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import configured_time_diagnostic as diagnostic


class ConfiguredTimeDiagnosticTest(unittest.TestCase):
    def output(self, record=None, terminal="PASS"):
        if record is None:
            record = dict(version=1, factory="ready", source="expected", stage="measured", reason="ok", attempt=1, install="installed", current="ok", verdict="pass")
        return (f"=== RUN   {diagnostic.TEST}\nprivate-token endpoint=private-endpoint\n"
                + "    source_test.go:9: configured_time_diagnostic:" + json.dumps(record) + "\n"
                + f"--- {terminal}: {diagnostic.TEST} (0.03s)\n").encode()

    def test_only_exact_executed_singleflight_passes_without_private_text(self):
        result = diagnostic.parse_result(self.output())
        self.assertEqual(result["verdict"], "pass")
        self.assertNotIn("private", json.dumps(result))
        self.assertEqual(result["observation"]["attempt"], 1)

    def test_missing_skipped_duplicate_and_invalid_markers_refuse(self):
        valid = self.output()
        for raw in (b"", valid + valid, self.output(terminal="SKIP"),
                    valid.replace(b'"attempt": 1', b'"attempt": 2'),
                    valid.replace(b'"factory": "ready"', b'"factory": "private-token"'),
                    valid.replace(b'"version": 1', b'"version": true'),
                    valid.replace(b'"version": 1', b'"version": 1, "version": 1'),
                    valid.replace(b'"version": 1', b'"extra": "private-token", "version": 1'),
                    valid.replace(b'"stage": "measured"', b'"stage": "read"')):
            with self.subTest(raw=raw):
                self.assertNotEqual(diagnostic.parse_result(raw)["verdict"], "pass")

    def test_failed_read_records_boundary_without_claiming_network_cause(self):
        record = dict(version=1, factory="ready", source="expected", stage="read", reason="read_timeout", attempt=1, install="not_run", current="not_run", verdict="fail")
        result = diagnostic.parse_result(self.output(record, "FAIL"))
        self.assertEqual(result["verdict"], "test_failed")
        self.assertEqual(result["observation"]["reason"], "read_timeout")

    def test_campaign_has_one_measurement_and_stops_after_precondition_failure(self):
        with tempfile.TemporaryDirectory() as temporary:
            log = Path(temporary) / "private.log"
            log.write_bytes(b"private-token")
            phase = dict(exit=1, timeout=False, cleanup="reaped", elapsed_ms=3, log_sha256="a"*64)
            with patch.object(diagnostic, "run_phase", return_value=(phase, log)) as run:
                result = diagnostic.campaign(Path(temporary), {})
            self.assertEqual(result["verdict"], "prepare_failed")
            self.assertEqual(run.call_count, 1)
            log.write_bytes(self.output())
            phase["exit"] = 0
            with patch.object(diagnostic, "run_phase", return_value=(phase, log)) as run:
                result = diagnostic.campaign(Path(temporary), {})
            self.assertEqual(result["verdict"], "pass")
            self.assertEqual([call.args[-1] for call in run.call_args_list], [10, 180, 10])
            command = run.call_args_list[-1].args[0]
            self.assertIn("-test.count=1", command)
            self.assertEqual(sum("-test.run=" in arg for arg in command), 1)

    def test_manual_diagnostic_keeps_ordinary_jobs_and_has_no_raw_artifact(self):
        root = Path(__file__).resolve().parents[2]
        workflow = (root / ".github/workflows/rust-sdk.yml").read_text()
        self.assertIn("if: ${{ !inputs.native_time_diagnostic }}", workflow)
        job = workflow.split("  native-time-diagnostic:\n", 1)[1]
        self.assertIn("github.event_name == 'workflow_dispatch' && inputs.native_time_diagnostic", job)
        self.assertIn("timeout-minutes: 4", job)
        self.assertIn("path: ${{ runner.temp }}/lantern-native-time-result.json", job)
        self.assertNotIn("private-", job)


if __name__ == "__main__":
    unittest.main()
