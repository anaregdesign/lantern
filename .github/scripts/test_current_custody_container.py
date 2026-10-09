import hashlib
import importlib.util
import json
import pathlib
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name("current_custody_container.py")
SPEC = importlib.util.spec_from_file_location("current_custody_container", SCRIPT)
CUSTODY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CUSTODY)


class CustodyReceiptTest(unittest.TestCase):
    def test_partial_pair_or_previous_cycle_cannot_pass_normal_shutdown(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            floor, state = root / "floors.json", root / "floors.json.state"
            floor.write_bytes(b"independent final floor bytes")
            clean = {"Version": 1, "Phase": "CLEAN", "Cycle": 2, "Floors": list(hashlib.sha256(floor.read_bytes()).digest())}
            state.write_text(json.dumps(clean))
            self.assertEqual(CUSTODY.checked_checkpoint(state, floor, 2), clean)
            for field, bad in (("Phase", "RUNNING"), ("Cycle", 1), ("Version", 2), ("Floors", [0] * 32)):
                with self.subTest(field=field):
                    state.write_text(json.dumps(dict(clean, **{field: bad})))
                    with self.assertRaises(RuntimeError):
                        CUSTODY.checked_checkpoint(state, floor, 2)
            state.write_text(json.dumps(clean))
            floor.write_bytes(b"different generation or partial publication")
            with self.assertRaises(RuntimeError):
                CUSTODY.checked_checkpoint(state, floor, 2)


if __name__ == "__main__":
    unittest.main()
