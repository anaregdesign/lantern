from pathlib import Path
import re
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


if __name__ == '__main__':
    unittest.main()
