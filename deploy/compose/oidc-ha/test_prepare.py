"""Isolated preparation contracts; never starts containers or touches trust/state."""
import importlib.util
import json
from pathlib import Path
import re
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location("ha_prepare", Path(__file__).with_name("prepare.py"))
PREPARE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PREPARE)


class PrepareTest(unittest.TestCase):
    def test_new_inputs_leave_generator_and_native_leaves_absent(self):
        with tempfile.TemporaryDirectory() as temporary:
            private = Path(temporary).resolve() / "new inputs"
            with mock.patch.object(PREPARE.os, "getuid", return_value=1234), mock.patch.object(PREPARE.os, "getgid", return_value=1234):
                PREPARE.prepare(private)
            self.assertFalse((private / "material/identity").exists())
            self.assertFalse((private / "operator/custody").exists())
            self.assertFalse((private / "state").exists())
            identities, epochs = set(), set()
            for i in range(3):
                node = private / f"config/lantern-{i}"
                config = json.loads((node / "startup.json").read_text())
                self.assertEqual(config["material_root"], "/material/identity")
                self.assertEqual(config["persistence_root"], "/state")
                self.assertEqual(config["node"], f"node-{i + 1}")
                self.assertNotIn("api_audience", config["issuers"][0])
                entries = dict(line.split("=", 1) for line in (node / "receipt.env").read_text().splitlines())
                identities.add(entries["LANTERN_NODE_ID"])
                epochs.add(entries["LANTERN_RECEIPT_EPOCH"])
                self.assertTrue(all(re.fullmatch(r"[0-9a-f]{32}", value) and int(value, 16) for value in entries.values()))
            self.assertEqual(len(identities), 3)
            self.assertEqual(len(epochs), 1)
            for entry in private.rglob("*"):
                self.assertEqual(entry.stat().st_mode & 0o777, 0o700 if entry.is_dir() else 0o600)
            original = (private / "config/lantern-0/receipt.env").read_bytes()
            with self.assertRaises(ValueError):
                PREPARE.prepare(private)
            self.assertEqual((private / "config/lantern-0/receipt.env").read_bytes(), original)
            self.assertIn(f"LANTERN_HA_PRIVATE_DIRECTORY='{private}'", (private / "compose.env").read_text())

    def test_refuses_ambiguous_path_before_output(self):
        with tempfile.TemporaryDirectory() as temporary:
            private = Path(temporary).resolve() / "unsafe$directory"
            with self.assertRaises(ValueError):
                PREPARE.prepare(private)
            self.assertFalse(private.exists())


if __name__ == "__main__":
    unittest.main()
