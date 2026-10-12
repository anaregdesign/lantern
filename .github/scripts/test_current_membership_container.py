"""Failure-oriented checks for the bounded membership evidence reader."""
import hashlib
import importlib.util
import json
import pathlib
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("membership_container", pathlib.Path(__file__).with_name("current_membership_container.py"))
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


class MembershipEvidenceTests(unittest.TestCase):
    def test_checkpoint_requires_clean_and_exact_floor_hash(self):
        with tempfile.TemporaryDirectory() as raw:
            directory = pathlib.Path(raw)
            floor = b'{"M":{"Version":12}}'
            (directory / "floors.json").write_bytes(floor)
            checkpoint = {"Phase": "CLEAN", "Cycle": 2, "Floors": list(hashlib.sha256(floor).digest())}
            path = directory / "floors.json.state"
            path.write_text(json.dumps(checkpoint))
            self.assertEqual(gate.clean_checkpoint(directory)["cycle"], 2)
            (directory / "floors.json").write_bytes(floor+b" ")
            with self.assertRaises(RuntimeError):
                gate.clean_checkpoint(directory)
            (directory / "floors.json").write_bytes(floor)
            checkpoint["Phase"] = "OPEN"
            path.write_text(json.dumps(checkpoint))
            with self.assertRaises(RuntimeError):
                gate.clean_checkpoint(directory)

    def test_consumed_digest_is_exact_signed_payload_after_magic(self):
        with tempfile.TemporaryDirectory() as raw:
            path = pathlib.Path(raw) / "membership"
            envelope = b'{"payload":{"version":9,"expires_at":"2026-10-12T12:00:00Z"},"signature":"test"}'
            path.write_bytes(b"LNPMS001"+envelope)
            version, _, digest = gate.manifest(path)
            self.assertEqual(version, 9)
            self.assertEqual(digest, hashlib.sha256(envelope).hexdigest())
            path.write_bytes(b"WRONG001"+envelope)
            with self.assertRaises(json.JSONDecodeError):
                gate.manifest(path)


if __name__ == "__main__":
    unittest.main()
