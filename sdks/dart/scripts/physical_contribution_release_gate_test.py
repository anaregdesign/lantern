from datetime import datetime, timezone
from pathlib import Path
import hashlib
import json
import re
import tempfile
import unittest
from unittest.mock import patch

import physical_contribution_release_gate as gate
from physical_receipt_attestation import fixture_provenance_tools


def current_matrix_fixture(marker_raw, record_raw):
    """Adapt only historical, unbound schema-1 scenario fixtures."""
    record = json.loads(record_raw)
    if record['schema'] == 1:
        marker = json.loads(marker_raw)
        marker['completedScenarios'] = sorted(gate.SCENARIOS)
        record['scenarios'] = sorted(gate.SCENARIOS)
        return json.dumps(marker).encode(), json.dumps(record).encode()
    # Controlled records bind the exact captured marker bytes. Neither captured
    # file may be reserialized merely to construct an accepted test fixture.
    return marker_raw, record_raw


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
        marker_raw, record_raw = current_matrix_fixture(
            (evidence / 'android-marker.json').read_bytes(),
            (evidence / 'android.json').read_bytes())
        marker, record = json.loads(marker_raw), json.loads(record_raw)
        current = set(gate.SCENARIOS)
        self.assertIn('advertised_contribution_delete_capability', current)
        self.assertNotIn('unsupported_offline_family', current)
        with tempfile.TemporaryDirectory() as directory:
            marker_path = Path(directory, 'marker.json')
            record_path = Path(directory, 'record.json')
            marker_path.write_bytes(marker_raw)
            record_path.write_bytes(record_raw)
            args = dict(tested_commit=record['testedCommit'],
                        platform='android', required_scenarios=current,
                        target=gate.CONTRIBUTION_TARGET)
            self.assertEqual(gate.validate_archived_receipt_evidence(
                marker_path, record_path, **args), record['application']['binarySha256'])

            obsolete = current - {'advertised_contribution_delete_capability'} | {
                'unsupported_offline_family'}
            marker['completedScenarios'] = sorted(obsolete)
            record['scenarios'] = sorted(obsolete)
            marker_path.write_text(json.dumps(marker))
            record_path.write_text(json.dumps(record))
            with self.assertRaisesRegex(ValueError,
                    '^on-device marker does not prove the required receipt scenarios$'):
                gate.validate_archived_receipt_evidence(marker_path, record_path, **args)

    def test_controlled_fixture_preserves_bytes_and_refuses_marker_reserialization(self):
        # Synthetic unit-only evidence, never a phone or release qualification.
        commit, run, binary = 'a' * 40, 'b' * 32, 'c' * 64
        marker = {
            'schema': 2, 'kind': 'physical_receipt_attestation', 'contentFree': True,
            'testedCommit': commit, 'target': gate.CONTRIBUTION_TARGET, 'runId': run,
            'platform': 'android', 'packageId': 'com.anaregdesign.lantern_example',
            'installedBinarySha256': binary, 'startedAt': '2026-01-01T00:00:10Z',
            'completedScenarios': sorted(gate.SCENARIOS), 'status': 'passed',
            'phase': 'complete', 'restart': {
                'preparedAt': '2026-01-01T00:00:40Z',
                'resumedAt': '2026-01-01T00:01:10Z', 'processChanged': True},
            'finishedAt': '2026-01-01T00:02:00Z',
        }
        marker_raw = (json.dumps(marker, separators=(',', ':')) + '\n').encode()
        record = {
            'schema': 2, 'kind': 'physical_edge_contribution_delete_evidence',
            'repository': 'anaregdesign/lantern', 'contentFree': True,
            'physicalDevice': True, 'testedCommit': commit, 'runId': run,
            'runStartedAt': '2026-01-01T00:00:00Z', 'recordedAt': '2026-01-01T00:02:10Z',
            'platform': {'kind': 'physical-android'}, 'application': {
                'packageId': marker['packageId'], 'target': gate.CONTRIBUTION_TARGET,
                'binarySha256': binary},
            'scenarios': sorted(gate.SCENARIOS), 'result': 'passed', 'network': {
                'transport': 'Connect/HTTPS', 'authenticated': True,
                'platformTrustedTls': True, 'fault': 'committed-response-socket-drop'},
        }
        tools = fixture_provenance_tools()
        record['fixture'] = {
            'schema': 1, 'profile': 'physical', 'sourceCommit': commit,
            'sourceKind': 'controlled-source', 'buildKind': 'test-only',
            'buildTags': ['lantern_test_time'], 'syntheticUtc': True,
            'imageSha256': '1' * 64, 'descriptorSha256': '2' * 64,
            'receiptSha256': '3' * 64, 'sidecarSha256': '4' * 64,
            'preparedAt': '2026-01-01T00:00:09Z', 'servingUntil': '2026-01-01T00:02:01Z',
            'servingStoppedAt': marker['finishedAt'],
            'lifecycleFinishedAt': '2026-01-01T00:02:01Z',
            'deviceStartedAt': marker['startedAt'], 'deviceFinishedAt': marker['finishedAt'],
            'phoneMarkerSha256': hashlib.sha256(marker_raw).hexdigest(),
            'recordPayloadSha256': tools.digest(tools.encoded(record)),
        }
        record_raw = (json.dumps(record, indent=2) + '\n').encode()
        accepted = current_matrix_fixture(marker_raw, record_raw)
        self.assertEqual(accepted, (marker_raw, record_raw))
        with tempfile.TemporaryDirectory() as directory:
            marker_path, record_path = Path(directory, 'marker.json'), Path(directory, 'record.json')
            marker_path.write_bytes(accepted[0])
            record_path.write_bytes(accepted[1])
            args = dict(tested_commit=commit, platform='android', required_scenarios=gate.SCENARIOS,
                        target=gate.CONTRIBUTION_TARGET, now=datetime(2026, 1, 1, 0, 3, tzinfo=timezone.utc))
            self.assertEqual(gate.validate_archived_receipt_evidence(
                marker_path, record_path, **args), binary)

            reencoded = json.dumps(json.loads(marker_raw)).encode()
            self.assertEqual(json.loads(reencoded), marker)
            self.assertNotEqual(reencoded, marker_raw)
            marker_path.write_bytes(reencoded)
            with self.assertRaisesRegex(ValueError, '^fixture summary capture binding mismatch$'):
                gate.validate_archived_receipt_evidence(marker_path, record_path, **args)


if __name__ == '__main__':
    unittest.main()
