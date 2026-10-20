import json
from pathlib import Path
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import process

from process import Child


class ChildTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)

    def start(self, body):
        binary = self.directory / "synthetic-child"
        binary.write_text("#!" + sys.executable + "\n" + body)
        binary.chmod(0o700)
        child = Child(binary, 1, "1" * 64, self.directory, time.monotonic_ns())
        def cleanup():
            child.terminate(kill=True)
            child.process.wait(timeout=3)
            child.process.stdin.close()
            child.reader.join(timeout=2)
            child.process.stdout.close()
            child.stderr.close()
        self.addCleanup(cleanup)
        return child

    def test_owned_pipe_nonce_and_stop_are_bound_and_reaped(self):
        child = self.start('''import json,os,sys
for line in sys.stdin:
    request=json.loads(line)
    phase=request["phase"]
    print("off_source "+json.dumps({"event":phase,"nonce":request["nonce"],"pid":os.getpid(),"elapsed_ns":1}),flush=True)
    if phase=="stop": break
''')
        deadline = time.monotonic() + 3
        child.send("reserve", deadline, private=True)
        self.assertEqual(child.receive("reserve", deadline)["nonce"], "1" * 64)
        child.send("stop", deadline)
        child.send("stop", deadline)  # No duplicate command can extend/restart it.
        child.receive("stop", deadline)
        row = child.wait(deadline, "synthetic-binary")
        self.assertEqual(row["exit"], 0)
        self.assertFalse(row["killed"])
        self.assertEqual(len(row["records"]), 2)
        self.assertIsNotNone(row["stop_ns"])

    def test_foreign_nonce_output_refuses(self):
        child = self.start('''import json,os
print("off_source "+json.dumps({"event":"reserved","nonce":"2"*64,"pid":os.getpid()}),flush=True)
''')
        with self.assertRaises(ValueError):
            child.receive("reserved", time.monotonic() + 2)

    def test_missing_phase_and_no_owner_restart(self):
        child = self.start("pass\n")
        with self.assertRaises(ValueError):
            child.receive("final", time.monotonic() + 2)
        self.assertEqual(child.process.wait(timeout=2), 0)

    def test_oversize_input_refuses_before_write(self):
        child = self.start("import sys\nsys.stdin.read()\n")
        with self.assertRaises(ValueError):
            child.send("configure", time.monotonic() + 1, environment=["LANTERN_X=" + "x" * 65536])

    def test_deadline_interrupts_blocked_output(self):
        child = self.start("import sys\nsys.stdin.read()\n")
        with self.assertRaises(TimeoutError):
            child.receive("final", time.monotonic() + .05)

    def test_bounded_raw_output_refuses(self):
        child = self.start("print('x'*65537,flush=True)\n")
        with self.assertRaises(ValueError):
            child.receive("final", time.monotonic() + 2)

    def test_duplicate_json_output_refuses(self):
        child = self.start('print(\'off_source {"pid":1,"pid":2}\',flush=True)\n')
        with self.assertRaises(ValueError):
            child.receive("final", time.monotonic() + 2)

    def test_reader_start_failure_reaps_unconfigured_child(self):
        binary = self.directory / "synthetic-child"
        binary.write_text("#!"+sys.executable+"\nimport sys\nsys.stdin.read()\n")
        binary.chmod(0o700)
        original = process.subprocess.Popen
        spawned = []

        def spawn(*args, **kwargs):
            child = original(*args, **kwargs)
            spawned.append(child)
            return child

        with patch.object(process.subprocess, "Popen", side_effect=spawn), patch.object(process.threading.Thread, "start", side_effect=RuntimeError("synthetic thread failure")):
            with self.assertRaises(RuntimeError):
                Child(binary, 1, "1"*64, self.directory, time.monotonic_ns())
        self.assertEqual(len(spawned), 1)
        self.assertIsNotNone(spawned[0].poll())
        self.assertTrue(spawned[0].stdin.closed)
        self.assertTrue(spawned[0].stdout.closed)
