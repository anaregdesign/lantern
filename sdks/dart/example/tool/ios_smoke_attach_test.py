#!/usr/bin/env python3
"""Finite failure-path checks for the native iOS smoke subprocess owner."""

import errno
import io
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
import ios_smoke_log as smoke_log


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
                            return_value=(code, b"install detail\n", timed_out, exceeded),
                        ) as read,
                        patch.object(smoke, "capture_diagnostics"),
                        patch.object(smoke.time, "monotonic", return_value=100),
                        patch.dict(os.environ, {
                            "GITHUB_OUTPUT": str(root / "output"),
                            "IOS_SMOKE_TOTAL_TIMEOUT_SECONDS": "720",
                        }),
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
                    self.assertEqual(read.call_args.args[1:3], (90, 65536))
                    operation = root / "initial/phases/install_operation"
                    self.assertTrue(operation.is_file(), "install evidence must be retained")
                    outcome = "timed_out" if timed_out else "returned"
                    self.assertEqual(
                        operation.read_text(),
                        f"started\n{outcome} exit_code={code} output_limit={int(exceeded)}\n",
                    )
                    self.assertFalse(
                        (root / "initial/phases/native_launch_operation").exists(),
                    )
                    self.assertIn(
                        "install detail", (root / "initial/flutter.log").read_text(),
                    )

    def test_native_launch_operation_outcomes(self):
        for code, timed_out in [(-15, True), (1, False), (0, False)]:
            with self.subTest(code=code, timed_out=timed_out):
                with tempfile.TemporaryDirectory() as temporary:
                    root = Path(temporary)
                    with (
                        patch.object(
                            smoke, "run_logged",
                            side_effect=[(0, False, b"")] * 3 + [(code, timed_out, b"")],
                        ) as logged,
                        patch.object(smoke, "read_process", return_value=(0, b"", False, False)),
                        patch.object(
                            smoke, "capture_diagnostics",
                            side_effect=PermissionError("diagnostics unavailable"),
                        ),
                        patch.object(smoke.time, "monotonic", return_value=100),
                        patch.dict(os.environ, {
                            "GITHUB_OUTPUT": str(root / "output"),
                            "IOS_SMOKE_TOTAL_TIMEOUT_SECONDS": "720",
                            "IOS_SMOKE_LAUNCH_TIMEOUT_SECONDS": "180",
                        }),
                    ):
                        status = smoke.run_attempt("SOURCE-DEVICE", "initial", root)
                    self.assertEqual(status, 70 if timed_out else 72)
                    self.assertEqual(
                        (root / "initial/classification.txt").read_text().strip(),
                        "launch_stall" if timed_out else "launch_failure",
                    )
                    operation = root / "initial/phases/native_launch_operation"
                    self.assertTrue(operation.is_file(), "native launch evidence must be retained")
                    outcome = "timed_out" if timed_out else "returned"
                    self.assertEqual(
                        operation.read_text(), f"started\n{outcome} exit_code={code}\n",
                    )
                    self.assertEqual(
                        (root / "initial/phases/install_operation").read_text(),
                        "started\nreturned exit_code=0 output_limit=0\n",
                    )
                    self.assertEqual(logged.call_args.args[2], 180)
                    self.assertTrue(logged.call_args.kwargs["collect_pid"])

    def test_install_output_is_redacted_and_bounded(self):
        payload = (
            b"https://private.example.test/install\n"
            b"Authorization: Bearer private-bearer\n"
            b"token=private-token\nmobile-smoke:private-key\n"
        ) * 200 + b"z" * (smoke.MAX_LINE_BYTES + 1) + b"\ninstall detail retained\n"
        self.assertLessEqual(len(payload), 65536)
        for console_bytes in (0, 23):
            with self.subTest(console_bytes=console_bytes):
                with tempfile.TemporaryDirectory() as temporary:
                    root = Path(temporary)

                    def logged(command, path, timeout, budget, **kwargs):
                        path.write_bytes(b"x" * (smoke.MAX_FILE_BYTES - 1) + b"\n")
                        budget[0] = console_bytes
                        return 0, False, b""

                    with (
                        patch.object(smoke, "run_logged", side_effect=logged),
                        patch.object(
                            smoke, "read_process", return_value=(1, payload, False, False),
                        ),
                        patch.object(smoke, "capture_diagnostics"),
                        patch.object(smoke.sys, "stdout") as output,
                        patch.dict(os.environ, {"GITHUB_OUTPUT": str(root / "output")}),
                    ):
                        output.buffer = io.BytesIO()
                        status = smoke.run_attempt("SOURCE-DEVICE", "initial", root)
                        visible = output.buffer.getvalue()
                    self.assertEqual(status, 72)
                    retained = (root / "initial/flutter.log").read_bytes()
                    self.assertLessEqual(len(retained), smoke.MAX_FILE_BYTES)
                    self.assertTrue(retained.endswith(b"install detail retained\n"))
                    self.assertIn(b"<redacted-url>", retained)
                    self.assertIn(b"oversized log line omitted", retained)
                    self.assertEqual(len(visible), console_bytes)
                    for secret in (
                        b"private.example", b"private-bearer", b"private-token", b"private-key",
                    ):
                        self.assertNotIn(secret, retained)
                        self.assertNotIn(secret, visible)

    def test_operation_evidence_does_not_reset_shared_deadline(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            now = [100]
            budgets = []

            def logged(command, path, timeout, budget, **kwargs):
                stage = command[1] if command[0] == "flutter" else command[2]
                budgets.append((stage, timeout))
                if stage == "launch":
                    return -15, True, b""
                now[0] += {"build": 340, "boot": 90, "bootstatus": 70}[stage]
                return 0, False, b""

            def installed(command, timeout, limit, **kwargs):
                self.assertEqual((timeout, limit), (90, 65536))
                now[0] += 85
                return 0, b"", False, False

            with (
                patch.object(smoke, "run_logged", side_effect=logged),
                patch.object(smoke, "read_process", side_effect=installed),
                patch.object(smoke, "capture_diagnostics"),
                patch.object(smoke.time, "monotonic", side_effect=lambda: now[0]),
                patch.dict(os.environ, {
                    "GITHUB_OUTPUT": str(root / "output"),
                    "IOS_SMOKE_FLUTTER_BIN": "flutter",
                    "IOS_SMOKE_XCRUN_BIN": "xcrun",
                    "IOS_SMOKE_TOTAL_TIMEOUT_SECONDS": "720",
                    "IOS_SMOKE_BUILD_TIMEOUT_SECONDS": "360",
                    "IOS_SMOKE_BOOT_TIMEOUT_SECONDS": "180",
                    "IOS_SMOKE_LAUNCH_TIMEOUT_SECONDS": "180",
                }),
            ):
                status = smoke.run_attempt("SOURCE-DEVICE", "initial", root)
            self.assertEqual(status, 70)
            self.assertEqual(
                budgets, [("build", 360), ("boot", 180), ("bootstatus", 90), ("launch", 135)],
            )

    def test_operation_metadata_is_closed_and_reset_before_failed_build(self):
        for name in (
            "install_operation", "native_launch_operation",
            "boot_started", "boot_done", "tests_failed",
        ):
            self.assertEqual(smoke_log.priority(Path("initial/phases") / name), 1)
        self.assertNotEqual(smoke_log.priority(Path("initial/phases/unknown_operation")), 1)
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            phases = root / "initial/phases"
            phases.mkdir(parents=True)
            for name in ("install_operation", "native_launch_operation"):
                (phases / name).write_text("started\nreturned exit_code=0\n")
            with (
                patch.object(smoke, "run_logged", return_value=(1, False, b"")),
                patch.object(smoke, "read_process") as read,
                patch.object(smoke, "capture_diagnostics"),
                patch.dict(os.environ, {"GITHUB_OUTPUT": str(root / "output")}),
            ):
                self.assertEqual(smoke.run_attempt("SOURCE-DEVICE", "initial", root), 72)
            read.assert_not_called()
            self.assertFalse((phases / "install_operation").exists())
            self.assertFalse((phases / "native_launch_operation").exists())

    def test_cleanup_failure_keeps_started_operation_without_claiming_completion(self):
        for operation in ("install", "native launch"):
            with self.subTest(operation=operation):
                with tempfile.TemporaryDirectory() as temporary:
                    root = Path(temporary)

                    def logged(command, *args, **kwargs):
                        if "launch" in command:
                            raise smoke.CleanupError("owned group absence unknown")
                        return 0, False, b""

                    with (
                        patch.object(smoke, "run_logged", side_effect=logged),
                        patch.object(
                            smoke, "read_process",
                            **(
                                {"side_effect": smoke.CleanupError("owned group absence unknown")}
                                if operation == "install"
                                else {"return_value": (0, b"", False, False)}
                            ),
                        ),
                        patch.object(
                            smoke, "capture_diagnostics",
                            side_effect=PermissionError("diagnostics unavailable"),
                        ),
                        patch.dict(os.environ, {"GITHUB_OUTPUT": str(root / "output")}),
                    ):
                        self.assertEqual(smoke.run_attempt("SOURCE-DEVICE", "initial", root), 72)
                    phase = "install_operation" if operation == "install" else "native_launch_operation"
                    evidence = root / "initial/phases" / phase
                    self.assertTrue(evidence.is_file())
                    self.assertEqual(evidence.read_text(), "started\n")
                    self.assertEqual(
                        (root / "initial/classification.txt").read_text(), "cleanup_failure\n",
                    )
                    self.assertIn(
                        f"cleanup failure during {operation}: owned group absence unknown",
                        (root / "initial/flutter.log").read_text(),
                    )

    def test_observation_io_failure_preserves_primary_timeout(self):
        real_open = Path.open
        for failing in ("metadata", "install output"):
            with self.subTest(failing=failing):
                with tempfile.TemporaryDirectory() as temporary:
                    root = Path(temporary)

                    def opened(path, *args, **kwargs):
                        if path.name in ("install_operation", "native_launch_operation"):
                            raise PermissionError("private-owner-value")
                        return real_open(path, *args, **kwargs)

                    failure = (
                        patch.object(Path, "open", opened)
                        if failing == "metadata"
                        else patch.object(
                            smoke, "append_log", side_effect=PermissionError("private-owner-value"),
                        )
                    )
                    with (
                        failure,
                        patch.object(smoke, "run_logged", return_value=(0, False, b"")),
                        patch.object(
                            smoke, "read_process", return_value=(-15, b"install detail\n", True, False),
                        ) as read,
                        patch.object(
                            smoke, "capture_diagnostics",
                            side_effect=PermissionError("diagnostics unavailable"),
                        ),
                        patch.object(smoke.sys, "stdout") as output,
                        patch.object(smoke.sys, "stderr") as warnings,
                        patch.dict(os.environ, {"GITHUB_OUTPUT": str(root / "output")}),
                    ):
                        output.buffer = io.BytesIO()
                        status = smoke.run_attempt("SOURCE-DEVICE", "initial", root)
                    self.assertEqual(status, 70)
                    read.assert_called_once()
                    self.assertEqual(
                        (root / "initial/classification.txt").read_text(), "launch_stall\n",
                    )
                    warning = "".join(c.args[0] for c in warnings.write.call_args_list)
                    self.assertIn("unavailable (PermissionError)", warning)
                    self.assertNotIn("private-owner-value", warning)

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
