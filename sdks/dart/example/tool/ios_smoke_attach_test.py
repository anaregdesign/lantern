#!/usr/bin/env python3
"""Finite failure-path checks for the native iOS smoke subprocess owner."""

import os
from pathlib import Path
import signal
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, call, patch

import ios_smoke_attach as smoke


class CleanupTests(unittest.TestCase):
    def test_denied_group_signal_stops_owned_child_and_remains_failure(self):
        for denied_signal in (signal.SIGTERM, signal.SIGKILL):
            with self.subTest(signal=denied_signal):
                process = Mock(pid=123)
                process.wait.return_value = -15

                def group_signal(pid, sig):
                    self.assertEqual(pid, process.pid)
                    if sig == denied_signal:
                        raise PermissionError("denied")

                with patch.object(smoke.os, "killpg", side_effect=group_signal):
                    with self.assertRaises(smoke.CleanupError):
                        smoke.stop_process(process)
                process.send_signal.assert_called_once_with(denied_signal)
                self.assertTrue(process.wait.called)
                for wait in process.wait.call_args_list:
                    self.assertEqual(wait, call(timeout=2))

    def test_unreaped_child_has_no_unbounded_wait(self):
        process = Mock(pid=123)
        process.wait.side_effect = subprocess.TimeoutExpired("owned-command", 2)
        with patch.object(smoke.os, "killpg") as killpg:
            with self.assertRaises(smoke.CleanupError):
                smoke.stop_process(process)
        self.assertEqual(
            killpg.call_args_list,
            [call(123, signal.SIGTERM), call(123, signal.SIGKILL)],
        )
        self.assertEqual(process.wait.call_args_list, [call(timeout=2)] * 2)

    def test_already_exited_group_is_reaped(self):
        process = Mock(pid=123)
        process.wait.return_value = 0
        with patch.object(smoke.os, "killpg", side_effect=ProcessLookupError):
            smoke.stop_process(process)
        process.poll.assert_called_once_with()
        process.wait.assert_called_once_with(timeout=2)
        process.send_signal.assert_not_called()

    def test_failed_or_incomplete_install_does_not_launch(self):
        for code, timed_out, exceeded in [(1, False, False), (-15, True, False), (0, False, True)]:
            with self.subTest(code=code, timed_out=timed_out, exceeded=exceeded):
                with tempfile.TemporaryDirectory() as temporary:
                    root = Path(temporary)
                    with (
                        patch.object(smoke, "run_logged", return_value=(0, False, b"")) as logged,
                        patch.object(
                            smoke, "read_process",
                            return_value=(code, b"", timed_out, exceeded),
                        ) as read,
                        patch.object(smoke, "capture_diagnostics"),
                        patch.dict(os.environ, {"GITHUB_OUTPUT": str(root / "output")}),
                    ):
                        status = smoke.run_attempt("SOURCE-DEVICE", "initial", root)
                    self.assertNotEqual(status, 0)
                    self.assertEqual(
                        (root / "initial/classification.txt").read_text().strip(),
                        "launch_stall" if timed_out else "launch_failure",
                    )
                    commands = [c.args[0] for c in logged.call_args_list + read.call_args_list]
                    self.assertTrue(any("install" in c for c in commands))
                    self.assertFalse(any("terminate" in c or "launch" in c for c in commands))

    def test_failure_classification_survives_failed_diagnostics(self):
        cases = [
            (smoke.CleanupError("denied"), "cleanup_failure"),
            ((1, False, b""), "build_failure"),
            ((0, True, b""), "build_stall"),
        ]
        for result, classification in cases:
            with self.subTest(classification=classification):
                with tempfile.TemporaryDirectory() as temporary:
                    root = Path(temporary)
                    output = root / "github-output"
                    options = (
                        {"side_effect": result}
                        if isinstance(result, Exception)
                        else {"return_value": result}
                    )
                    with (
                        patch.object(smoke, "run_logged", **options),
                        patch.object(
                            smoke, "capture_diagnostics",
                            side_effect=PermissionError("diagnostics denied"),
                        ),
                        patch.dict(os.environ, {"GITHUB_OUTPUT": str(output)}),
                    ):
                        status = smoke.run_attempt("SOURCE-DEVICE", "initial", root)
                    self.assertNotEqual(status, 0)
                    self.assertEqual(
                        (root / "initial/classification.txt").read_text().strip(),
                        classification,
                    )
                    self.assertEqual(
                        output.read_text(), f"classification={classification}\n",
                    )
                    self.assertFalse((root / "initial/phases/build_done").exists())


if __name__ == "__main__":
    unittest.main()
