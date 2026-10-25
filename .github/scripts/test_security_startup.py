"""The fixture bridge requires and consumes operator JSON for every process."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from security_startup import startup_command


class SecurityStartupTest(unittest.TestCase):
    def test_initialize_and_restarts_consume_required_current_json(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            startup, binary, calls = root / "startup.json", root / "server", root / "calls.jsonl"
            binary.write_text(f"#!{sys.executable}\n" + "\n".join((
                "import json, os, sys",
                "with open(os.environ['CALLS'], 'a') as output:",
                "    output.write(json.dumps([sys.argv[1:], json.loads(os.environ['LANTERN_SECURITY_CONFIG_JSON'])]) + '\\n')",
            )) + "\n")
            binary.chmod(0o700)
            initial = {"public_origin": "https://fixture.invalid", "issuers": [], "secret": "synthetic-secret"}
            startup.write_text(json.dumps(initial, indent=2) + "\n")
            env = {**os.environ, "CALLS": str(calls), "LANTERN_SECURITY_CONFIG_JSON": "stale inherited input"}

            def run(initialize=False):
                command = startup_command(str(startup), initialize=initialize, binary=str(binary))
                self.assertNotIn(initial["secret"], " ".join(command))
                return subprocess.run(["sh", *command], env=env, capture_output=True, text=True)

            self.assertEqual(run(initialize=True).returncode, 0)
            self.assertEqual(run().returncode, 0)
            updated = {**initial, "issuers": [{"issuer": "https://added.invalid"}]}
            startup.write_text(json.dumps(updated))
            self.assertEqual(run().returncode, 0)
            startup.unlink()
            self.assertNotEqual(run().returncode, 0)
            self.assertNotEqual(run(initialize=True).returncode, 0)
            self.assertEqual([json.loads(line) for line in calls.read_text().splitlines()], [
                [["init-security"], initial], [[], initial], [[], updated],
            ])

    def test_paths_are_absolute_clean_and_passed_as_arguments(self):
        for path in ("relative/startup.json", "/provision/../startup.json", "/provision/invalid\x00.json"):
            with self.subTest(path=path), self.assertRaises(ValueError):
                startup_command(path)
        command = startup_command("/private config/startup.json")
        self.assertEqual(command[-3:], ["/private config/startup.json", "/server", "serve"])
        self.assertNotIn("/private config/startup.json", command[1])


if __name__ == "__main__":
    unittest.main()
