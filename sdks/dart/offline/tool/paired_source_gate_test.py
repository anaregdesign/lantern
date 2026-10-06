import subprocess
import tempfile
from pathlib import Path
import unittest
from unittest.mock import patch

import paired_source_gate as gate


class PairedSourceOwnershipTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        parent = Path(self.temp.name)
        package = parent / "offline"
        (package / "tool").mkdir(parents=True)
        (parent / "pubspec.yaml").write_text("version: 0.5.0\n")
        (package / "pubspec.yaml").write_text("dependencies:\n  lantern_client: ^0.5.0\n")
        self.lock = package / "pubspec.lock"
        self.lock.write_bytes(b"hosted-original\n")
        self.source = package / "tool/paired_source.pubspec.lock"
        self.source.write_bytes(b"paired-source-locked\n")
        self.override = package / "pubspec_overrides.yaml"
        self.state = package / ".dart_tool/paired_source_gate.json"
        replacements = dict(PACKAGE=package, PARENT=parent, LOCK=self.lock,
                            SOURCE_LOCK=self.source, OVERRIDE=self.override, STATE=self.state)
        self.addCleanup(patch.stopall)
        patch.multiple(gate, **replacements).start()

    def test_failed_resolve_restores_hosted_state(self):
        def fail(*args, **kwargs):
            self.lock.write_bytes(b"partial-resolver-output")
            raise subprocess.CalledProcessError(1, args[0])
        with patch.object(gate.subprocess, "run", side_effect=fail):
            with self.assertRaises(subprocess.CalledProcessError):
                gate.prepare()
        self.assertEqual(self.lock.read_bytes(), b"hosted-original\n")
        self.assertFalse(self.override.exists())
        self.assertFalse(self.state.exists())

    def test_existing_user_override_is_never_replaced(self):
        self.override.write_bytes(b"user-owned\n")
        with self.assertRaises(ValueError):
            gate.prepare()
        self.assertEqual(self.override.read_bytes(), b"user-owned\n")
        self.assertEqual(self.lock.read_bytes(), b"hosted-original\n")
        self.assertFalse(self.state.exists())

    def test_paired_success_restores_stale_hosted_lock_without_claiming_hosted_proof(self):
        with patch.object(gate.subprocess, 'run') as run:
            gate.prepare()
            self.assertEqual(self.lock.read_bytes(), self.source.read_bytes())
            run.assert_called_once_with(['dart', 'pub', 'get', '--enforce-lockfile'], cwd=gate.PACKAGE, check=True)
            gate.cleanup()
        self.assertEqual(self.lock.read_bytes(), b'hosted-original\n')
        self.assertFalse(self.override.exists())
        self.assertFalse(self.state.exists())

    def test_cleanup_refuses_modified_override_and_preserves_recovery(self):
        with patch.object(gate.subprocess, "run") as run:
            gate.prepare()
        self.assertIn("--enforce-lockfile", run.call_args.args[0])
        self.assertEqual(self.lock.read_bytes(), b"paired-source-locked\n")
        self.override.write_bytes(b"edited-by-user\n")
        with self.assertRaises(ValueError):
            gate.cleanup()
        self.assertEqual(self.override.read_bytes(), b"edited-by-user\n")
        self.assertTrue(self.state.exists())
        self.override.write_bytes(gate.OVERRIDE_BYTES)
        gate.cleanup()
        self.assertEqual(self.lock.read_bytes(), b"hosted-original\n")

    def test_source_version_mismatch_fails_before_any_file_changes(self):
        (gate.PARENT / "pubspec.yaml").write_text("version: 0.4.1\n")
        with self.assertRaises(ValueError):
            gate.prepare()
        self.assertEqual(self.lock.read_bytes(), b"hosted-original\n")
        self.assertFalse(self.override.exists())
        self.assertFalse(self.state.exists())


if __name__ == "__main__":
    unittest.main()
