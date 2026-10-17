#!/usr/bin/env python3
"""Finite failure-path checks for the native iOS smoke subprocess owner."""

import errno
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import Mock, call, patch

import ios_smoke_attach as smoke


class CleanupTests(unittest.TestCase):
    def test_child_exit_between_poll_and_denied_signal_proves_group_absence(self):
        process = subprocess.Popen(
            [sys.executable, "-c", "import sys; sys.stdin.buffer.read(1)"],
            stdin=subprocess.PIPE,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
        )
        killpg = os.killpg
        signals = []

        def group_signal(pid, sig):
            self.assertEqual(pid, process.pid)
            signals.append(sig)
            if sig == signal.SIGTERM:
                self.assertIsNone(process.returncode)
                process.stdin.close()
                deadline = time.monotonic() + 2
                while time.monotonic() < deadline:
                    exited = os.waitid(
                        os.P_PID, pid, os.WEXITED | os.WNOHANG | os.WNOWAIT,
                    )
                    if exited is not None:
                        break
                    time.sleep(0.01)
                else:
                    self.fail("owned child did not exit within the probe bound")
                self.assertEqual(exited.si_pid, pid)
                self.assertEqual(exited.si_status, 0)
                self.assertIsNone(process.returncode)
                raise PermissionError("signal denied for exited, unreaped child")
            if sig == 0:
                self.assertEqual(process.returncode, 0)
            return killpg(pid, sig)

        try:
            with patch.object(smoke.os, "killpg", side_effect=group_signal):
                smoke.stop_process(process)
            self.assertEqual(signals, [signal.SIGTERM, signal.SIGKILL, 0])
            self.assertEqual(process.returncode, 0)
            with self.assertRaises(ProcessLookupError):
                killpg(process.pid, 0)
        finally:
            process.stdin.close()
            if process.poll() is None:
                process.kill()
            process.wait(timeout=2)

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

    def test_reaped_child_cannot_resolve_denial_without_group_absence(self):
        cases = [
            (None, "group still exists"),
            (PermissionError("denied"), "group absence check was denied"),
            (OSError(errno.EIO, "unknown"), "group absence check failed"),
        ]
        for outcome, reason in cases:
            with self.subTest(reason=reason):
                process = Mock(pid=123)
                process.wait.return_value = 0

                def group_signal(pid, sig):
                    self.assertEqual(pid, process.pid)
                    if sig == signal.SIGTERM:
                        raise PermissionError("denied")
                    if sig == signal.SIGKILL:
                        raise ProcessLookupError
                    self.assertEqual(sig, 0)
                    process.wait.assert_called_with(timeout=2)
                    if outcome is not None:
                        raise outcome

                with patch.object(smoke.os, "killpg", side_effect=group_signal) as group:
                    with self.assertRaisesRegex(smoke.CleanupError, reason):
                        smoke.stop_process(process)
                self.assertEqual(
                    group.call_args_list,
                    [call(123, signal.SIGTERM), call(123, signal.SIGKILL), call(123, 0)],
                )
                process.send_signal.assert_called_once_with(signal.SIGTERM)

    def test_unreaped_child_has_no_unbounded_wait(self):
        for denied in (False, True):
            with self.subTest(denied=denied):
                process = Mock(pid=123)
                process.wait.side_effect = subprocess.TimeoutExpired("owned-command", 2)
                with patch.object(
                    smoke.os, "killpg", side_effect=PermissionError if denied else None,
                ) as killpg:
                    with self.assertRaisesRegex(smoke.CleanupError, "did not exit"):
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
                    if classification == "cleanup_failure":
                        self.assertIn(
                            "cleanup failure during build: denied",
                            (root / "initial/flutter.log").read_text(),
                        )

    def test_capture_cleanup_reason_is_bounded_redacted_and_retained(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            log = root / "initial/flutter.log"
            reason = (
                "x" * smoke.MAX_FILE_BYTES
                + "\ngroup denied http://127.0.0.1:1234/private-vm-code/"
                + " Authorization: Bearer private-auth token=private-token"
            )

            def fail_capture(*args, **kwargs):
                log.write_bytes(b"x" * smoke.MAX_FILE_BYTES)
                raise smoke.CleanupError(reason)

            with (
                patch.object(smoke, "run_logged", return_value=(0, False, b"")),
                patch.object(smoke, "read_process", side_effect=fail_capture),
                patch.object(
                    smoke, "capture_diagnostics",
                    side_effect=PermissionError("diagnostics denied"),
                ),
                patch.dict(os.environ, {"GITHUB_OUTPUT": str(root / "output")}),
            ):
                status = smoke.run_attempt("SOURCE-DEVICE", "initial", root)
            self.assertEqual(status, 72)
            self.assertEqual(
                (root / "initial/classification.txt").read_text(),
                "cleanup_failure\n",
            )
            retained = log.read_bytes()
            self.assertLessEqual(len(retained), smoke.MAX_FILE_BYTES)
            record = retained[retained.index(b"[ios smoke] cleanup failure"):]
            self.assertLessEqual(len(record), smoke.MAX_LINE_BYTES)
            self.assertIn(b"cleanup failure during install:", record)
            self.assertIn(b"group denied", record)
            self.assertIn(b"<redacted-url>", record)
            self.assertIn(b"Authorization: Bearer <redacted>", record)
            self.assertIn(b"token=<redacted>", record)
            for secret in (b"private-vm-code", b"private-auth", b"private-token"):
                self.assertNotIn(secret, retained)


if __name__ == "__main__":
    unittest.main()
