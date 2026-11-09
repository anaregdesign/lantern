from pathlib import Path
import json
import re
import tempfile
import unittest
from unittest.mock import patch

import physical_contribution_release_gate as gate


class ContributionReleaseGateTest(unittest.TestCase):
    def test_frozen_source_only_accepts_immediate_evidence_child(self):
        with patch.object(gate, 'git', side_effect=['', 'b' * 40, 'a' * 40,
                                                   '\n'.join(sorted(gate.EXPECTED))]):
            self.assertEqual(gate.source_identity(), ('a' * 40, 'b' * 40))
        for responses in [
            [' M lib/src/crud.dart'],
            ['', 'b' * 40, 'a' * 40 + ' ' + 'c' * 40],
            ['', 'b' * 40, 'a' * 40, '\n'.join(gate.EXPECTED | {'sdks/dart/lib/src/crud.dart'})],
            ['', 'b' * 40, 'a' * 40, '\n'.join(sorted(gate.EXPECTED)[:-1])],
        ]:
            with self.subTest(responses=responses), patch.object(gate, 'git', side_effect=responses):
                with self.assertRaises(ValueError):
                    gate.source_identity()

    def test_required_scenarios_are_bound_to_executed_target(self):
        text = Path(gate.ROOT, 'sdks/dart/example', gate.CONTRIBUTION_TARGET).read_text()
        matrix = text.split('const contributionScenarios = <String>{', 1)[1].split('};', 1)[0]
        self.assertEqual(gate.SCENARIOS, set(re.findall(r"'([a-z_]+)'", matrix)))
        self.assertIn('physical_sigkill_status_first', gate.SCENARIOS)
        self.assertIn('committed_response_loss', gate.SCENARIOS)

    def test_current_matrix_rejects_obsolete_offline_refusal_evidence(self):
        evidence = Path(gate.ROOT, gate.EVIDENCE)
        marker = json.loads((evidence / 'android-marker.json').read_text())
        record = json.loads((evidence / 'android.json').read_text())
        current = set(gate.SCENARIOS)
        self.assertIn('advertised_contribution_delete_capability', current)
        self.assertNotIn('unsupported_offline_family', current)
        with tempfile.TemporaryDirectory() as directory:
            marker_path = Path(directory, 'marker.json')
            record_path = Path(directory, 'record.json')
            for scenarios, accepted in (
                (current, True),
                (current - {'advertised_contribution_delete_capability'} |
                 {'unsupported_offline_family'}, False),
            ):
                with self.subTest(accepted=accepted):
                    marker['completedScenarios'] = sorted(scenarios)
                    record['scenarios'] = sorted(scenarios)
                    marker_path.write_text(json.dumps(marker))
                    record_path.write_text(json.dumps(record))
                    args = dict(tested_commit=record['testedCommit'],
                                platform='android', required_scenarios=current,
                                target=gate.CONTRIBUTION_TARGET)
                    if accepted:
                        self.assertEqual(gate.validate_archived_receipt_evidence(
                            marker_path, record_path, **args),
                            record['application']['binarySha256'])
                    else:
                        with self.assertRaises(ValueError):
                            gate.validate_archived_receipt_evidence(
                                marker_path, record_path, **args)


if __name__ == '__main__':
    unittest.main()
