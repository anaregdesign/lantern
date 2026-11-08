"""Review boundary inventory: profile changes may not hide resource contracts."""
from pathlib import Path
import re
import unittest

TOOL = Path(__file__).resolve().parent


class ResourceAssertionsTest(unittest.TestCase):
    def test_automatic_contracts_are_unconditional(self):
        source = (TOOL / 'automatic_baseline.dart').read_text()
        calls = re.findall(r'requireMeasurement\((.*?)\);', source, re.S)
        for label in ('settle_budget', 'close_budget', 'settlement', 'send_permits',
                      'read_permits', 'scope_read_permits', 'joined'):
            call = next(c for c in calls if f"'{label}'" in c)
            self.assertNotIn('profile', call, label)
        memory = next(c for c in calls if "'memory_budget'" in c)
        self.assertIn('profile.envelope(rssDelta <= 512 * 1024 * 1024) &&', memory)
        self.assertIn('retainedBytes <= 4 * 1024 * 1024', memory)
        self.assertIn('ticket.timeout(const Duration(seconds: 30))', source)
        self.assertIn('Future.wait(ready).timeout(const Duration(seconds: 5))', source)

    def test_retention_limits_remain_outside_profile(self):
        source = (TOOL / 'performance_probe.dart').read_text()
        for call in re.findall(r'_within\((.*?)\);', source, re.S):
            if "'snapshot_bytes'" in call or "'contribution_delete_codec_bytes'" in call:
                self.assertNotIn('profile:', call)
        sqlite = (TOOL.parent.parent / 'offline_sqlite/tool/performance_probe.dart').read_text()
        for expression in ('bytes <= 8 * 1024 * 1024', 'cacheWal <= 64 * 1024 * 1024',
                           'settledWal! <= 128 * 1024 * 1024'):
            self.assertIn(f'baselineRequire({expression},', sqlite)
        for expression in ('diskBytes > 8 * 1024 * 1024', 'walBytes > 64 * 1024 * 1024',
                           'replay.elapsedMilliseconds > 30000', 'lifecycleWalBytes > 128 * 1024 * 1024'):
            self.assertIn(expression, sqlite)
        self.assertIn("if (diagnostics != null) result['qualifies'] = false;", sqlite)


if __name__ == '__main__':
    unittest.main()
