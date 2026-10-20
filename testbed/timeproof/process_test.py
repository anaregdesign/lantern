import json
from pathlib import Path
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import process

from process import Child
from deadline import Window


def limit(seconds):
    return Window(monotonic=time.monotonic_ns, boottime=time.monotonic_ns).limit(seconds)


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
        deadline = limit(3)
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
            child.receive("reserved", limit(2))

    def test_missing_phase_and_no_owner_restart(self):
        child = self.start("pass\n")
        with self.assertRaises(ValueError):
            child.receive("final", limit(2))
        self.assertEqual(child.process.wait(timeout=2), 0)

    def test_oversize_input_refuses_before_write(self):
        child = self.start("import sys\nsys.stdin.read()\n")
        with self.assertRaises(ValueError):
            child.send("configure", limit(1), environment=["LANTERN_X=" + "x" * 65536])

    def test_deadline_interrupts_blocked_output(self):
        child = self.start("import sys\nsys.stdin.read()\n")
        with self.assertRaises(TimeoutError):
            child.receive("final", limit(.05))

    def test_bounded_raw_output_refuses(self):
        child = self.start("print('x'*65537,flush=True)\n")
        with self.assertRaises(ValueError):
            child.receive("final", limit(2))

    def test_duplicate_json_output_refuses(self):
        child = self.start('print(\'off_source {"pid":1,"pid":2}\',flush=True)\n')
        with self.assertRaises(ValueError):
            child.receive("final", limit(2))

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

    def test_resume_after_writable_wait_sends_no_more_input(self):
        child = self.start("import sys\nsys.stdin.read()\n")
        clock = {"boot": 0}
        deadline = Window(lambda: 0, lambda: clock["boot"]).limit(1)

        def resumed(*args):
            clock["boot"] = 130_000_000_000
            return [], [child.process.stdin], []

        with patch.object(process.select, "select", side_effect=resumed), patch.object(process.os, "write") as write:
            with self.assertRaises(TimeoutError):
                child.send("configure", deadline, environment=[])
            write.assert_not_called()

    def test_resume_during_process_wait_stops_waiting_on_active_clock(self):
        child = self.start("import sys\nsys.stdin.read()\n")
        clock = {"boot": 0}
        deadline = Window(lambda: 0, lambda: clock["boot"]).limit(120)

        def resumed(*args, **kwargs):
            self.assertLessEqual(kwargs["timeout"], .1)
            clock["boot"] = 130_000_000_000
            raise process.subprocess.TimeoutExpired("synthetic child", .1)

        with patch.object(child.process, "wait", side_effect=resumed) as wait:
            with self.assertRaises(TimeoutError):
                child.wait(deadline, "fixture")
            self.assertEqual(wait.call_count, 1)

    def test_all_post_spawn_initialization_failures_reap_and_close(self):
        binary = self.directory / "initialization-child"
        binary.write_text("#!"+sys.executable+"\nimport sys\nsys.stdin.read()\n")
        binary.chmod(0o700)
        original = process.subprocess.Popen
        for slot, target in enumerate(("set_blocking", "Thread", "start"), 1):
            with self.subTest(target=target):
                spawned, streams = [], []
                def spawn(*args, **kwargs):
                    child = original(*args, **kwargs)
                    streams.append(kwargs["stderr"])
                    child.wait = unittest.mock.Mock(wraps=child.wait)
                    spawned.append(child)
                    return child
                owner = process.os if target == "set_blocking" else process.threading if target == "Thread" else process.threading.Thread
                try:
                    with patch.object(process.subprocess, "Popen", side_effect=spawn), patch.object(owner, target, side_effect=RuntimeError("initialization fixture")):
                        with self.assertRaises(RuntimeError):
                            Child(binary, slot, "1"*64, self.directory, time.monotonic_ns())
                    self.assertEqual(len(spawned), 1)
                    self.assertIsNotNone(spawned[0].poll())
                    self.assertTrue(spawned[0].wait.called)
                    self.assertTrue(spawned[0].stdin.closed)
                    self.assertTrue(spawned[0].stdout.closed)
                    self.assertTrue(streams[0].closed)
                finally:
                    for child in spawned:
                        if child.poll() is None: child.kill()
                        child.wait(timeout=3)
                        child.stdin.close(); child.stdout.close()
                    for stream in streams: stream.close()
