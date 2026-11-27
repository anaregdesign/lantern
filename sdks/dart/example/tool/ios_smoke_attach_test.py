#!/usr/bin/env python3
"""Finite failure-path checks for the native iOS smoke subprocess owner."""

import errno
import io
import os
from pathlib import Path
import plistlib
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
    def setUp(self):
        # These existing cases isolate later stage/cleanup failures. Native
        # identity reads and their refusal paths are exercised separately below.
        self.enterContext(patch.object(smoke, "prepare_runner_launch", return_value="/owned/Runner.app/Runner"))
        self.enterContext(patch.object(smoke, "verify_runner_launch"))
        self.enterContext(patch.object(smoke, "observe_preinstall_apps", return_value="unknown"))

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

                with (
                    patch.object(smoke.os, "killpg", side_effect=group_signal),
                    patch.object(smoke.time, "monotonic", side_effect=[0, 2]),
                ):
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

                with (
                    patch.object(smoke.os, "killpg", side_effect=group_signal) as group,
                    patch.object(smoke.time, "monotonic", side_effect=[0, 2]),
                ):
                    with self.assertRaisesRegex(smoke.CleanupError, reason):
                        smoke.stop_process(process)
                self.assertEqual(
                    group.call_args_list,
                    [call(123, signal.SIGTERM), call(123, signal.SIGKILL), call(123, 0)],
                )
                process.send_signal.assert_called_once_with(signal.SIGTERM)

    def test_reaped_child_waits_for_actual_group_absence_within_same_budget(self):
        process = Mock(pid=123, returncode=0)
        process.wait.return_value = 0
        now = [0.0]
        signals = []

        def group_signal(pid, sig):
            self.assertEqual(pid, 123)
            signals.append(sig)
            if sig == signal.SIGTERM:
                raise PermissionError(errno.EPERM, "private command detail")
            if sig == signal.SIGKILL:
                raise ProcessLookupError
            self.assertEqual(sig, 0)
            if now[0] < 0.1:
                raise PermissionError(errno.EPERM, "private command detail")
            raise ProcessLookupError

        def advance(delay):
            now[0] += delay

        with (
            patch.object(smoke.os, "killpg", side_effect=group_signal),
            patch.object(smoke.time, "monotonic", side_effect=lambda: now[0]),
            patch.object(smoke.time, "sleep", side_effect=advance),
        ):
            smoke.stop_process(process)
        self.assertAlmostEqual(now[0], 0.1)
        self.assertEqual(
            [sig for sig in signals if sig], [signal.SIGTERM, signal.SIGKILL],
        )
        self.assertEqual(signals[-1], 0)
        process.send_signal.assert_called_once_with(signal.SIGTERM)
        self.assertEqual(process.wait.call_args_list, [call(timeout=2)] * 2)

    def test_absence_refusals_and_consumed_reap_budget_remain_bounded(self):
        for outcome, consumed, reason, number in [
            ("denied", 0, "group absence check was denied", errno.EPERM),
            ("live", 0, "group still exists", "unknown"),
            ("other", 0, "group absence check failed", errno.EIO),
            ("denied", 2, "group absence check was denied", errno.EPERM),
        ]:
            with self.subTest(outcome=outcome, consumed=consumed):
                process = Mock(pid=123, returncode=0)
                now = [0.0]
                waits = []
                signals = []

                def wait(timeout):
                    waits.append(timeout)
                    if len(waits) == 2:
                        now[0] += consumed
                    return 0

                def group_signal(pid, sig):
                    self.assertEqual(pid, 123)
                    signals.append(sig)
                    if sig == signal.SIGTERM:
                        raise PermissionError(errno.EPERM, "private command detail")
                    if sig == signal.SIGKILL:
                        raise ProcessLookupError
                    self.assertEqual(sig, 0)
                    if outcome == "denied":
                        raise PermissionError(errno.EPERM, "private command detail")
                    if outcome == "other":
                        raise OSError(errno.EIO, "private command detail")

                def advance(delay):
                    now[0] += delay

                process.wait.side_effect = wait
                with (
                    patch.object(smoke.os, "killpg", side_effect=group_signal),
                    patch.object(smoke.time, "monotonic", side_effect=lambda: now[0]),
                    patch.object(smoke.time, "sleep", side_effect=advance) as sleep,
                ):
                    with self.assertRaisesRegex(smoke.CleanupError, reason) as caught:
                        smoke.stop_process(process)
                self.assertLessEqual(now[0], 2)
                self.assertEqual(
                    [sig for sig in signals if sig], [signal.SIGTERM, signal.SIGKILL],
                )
                self.assertEqual(waits, [2, 2])
                self.assertIn(f"pid=123 rc=0 errno={number}", str(caught.exception))
                self.assertNotIn("private command detail", str(caught.exception))
                if consumed or outcome == "other":
                    sleep.assert_not_called()
                    self.assertEqual(signals.count(0), 1)
                else:
                    self.assertAlmostEqual(now[0], 2)

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
                    verification = "verification=not_eligible preinstall=unknown\n" if timed_out else ""
                    self.assertEqual(
                        operation.read_text(),
                        f"started\n{outcome} exit_code={code} output_limit={int(exceeded)}\n{verification}",
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
                    "IOS_SMOKE_BUILD_TIMEOUT_SECONDS": "540",
                    "IOS_SMOKE_BOOT_TIMEOUT_SECONDS": "180",
                    "IOS_SMOKE_LAUNCH_TIMEOUT_SECONDS": "180",
                }),
            ):
                status = smoke.run_attempt("SOURCE-DEVICE", "initial", root)
            self.assertEqual(status, 70)
            self.assertEqual(
                budgets, [("build", 540), ("boot", 180), ("bootstatus", 90), ("launch", 135)],
            )

    def test_identity_launch_and_returned_pid_share_the_original_native_bound(self):
        for total in (720, 40, 0.25):
            with self.subTest(total=total), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                now = [100.0]
                phase_deadline = min(100 + total, 280)
                consumed = 12 if total >= 40 else 0
                launches = []

                def prepared(*args):
                    self.assertEqual(args[2], phase_deadline)
                    now[0] += consumed
                    return "/owned/Runner.app/Runner"

                def logged(command, path, timeout, budget, **kwargs):
                    if "launch" in command:
                        launches.append(timeout)
                        return 0, False, (smoke.BUNDLE_ID + ": 42\n").encode()
                    return 0, False, b""

                with patch.object(smoke.time, "monotonic", side_effect=lambda: now[0]), patch.object(smoke, "prepare_runner_launch", side_effect=prepared), patch.object(smoke, "verify_runner_launch", side_effect=smoke.RunnerOwnershipError("stop_after_identity")) as verify, patch.object(smoke, "run_logged", side_effect=logged), patch.object(smoke, "read_process", return_value=(0, b"", False, False)), patch.object(smoke, "capture_diagnostics"), patch.dict(os.environ, {"GITHUB_OUTPUT": str(root / "output"), "IOS_SMOKE_TOTAL_TIMEOUT_SECONDS": str(total), "IOS_SMOKE_LAUNCH_TIMEOUT_SECONDS": "180"}):
                    if total == 0.25:
                        # The whole attempt remains an integer setting; model
                        # its final fractional slice after successful install.
                        os.environ["IOS_SMOKE_TOTAL_TIMEOUT_SECONDS"] = "40"
                        with patch.object(smoke, "read_process", side_effect=lambda *a, **k: (now.__setitem__(0, 139.75) or (0, b"", False, False))):
                            phase_deadline = 140
                            self.assertEqual(smoke.run_attempt("SOURCE-DEVICE", "initial", root), 72)
                    else:
                        self.assertEqual(smoke.run_attempt("SOURCE-DEVICE", "initial", root), 72)
                self.assertEqual(launches, [0.25 if total == 0.25 else min(total, 180) - consumed])
                verify.assert_called_once_with(42, "/owned/Runner.app/Runner", phase_deadline)

    def test_exhausted_native_or_whole_bound_never_dispatches_launch(self):
        for total in (720, 40):
            with self.subTest(total=total), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                now = [100.0]

                def prepared(*args):
                    now[0] = min(100 + total, 280)
                    return "/owned/Runner.app/Runner"

                with patch.object(smoke.time, "monotonic", side_effect=lambda: now[0]), patch.object(smoke, "prepare_runner_launch", side_effect=prepared), patch.object(smoke, "run_logged", return_value=(0, False, b"")) as logged, patch.object(smoke, "read_process", return_value=(0, b"", False, False)), patch.object(smoke, "capture_diagnostics"), patch.dict(os.environ, {"GITHUB_OUTPUT": str(root / "output"), "IOS_SMOKE_TOTAL_TIMEOUT_SECONDS": str(total), "IOS_SMOKE_LAUNCH_TIMEOUT_SECONDS": "180"}):
                    self.assertEqual(smoke.run_attempt("SOURCE-DEVICE", "initial", root), 72)
                self.assertFalse(any("launch" in call.args[0] for call in logged.call_args_list))

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


class InstallReconciliationTests(unittest.TestCase):
    def test_complete_device_inventory_is_required_for_absence(self):
        system_app = {"com.apple.Preferences": {"CFBundleIdentifier": "com.apple.Preferences"}}
        stale_app = {smoke.BUNDLE_ID.lower(): {"CFBundleIdentifier": smoke.BUNDLE_ID.lower()}}
        for result, expected in [
            ((0, plistlib.dumps(system_app), False, False), "absent"),
            ((0, plistlib.dumps(system_app | stale_app), False, False), "present"),
            ((0, plistlib.dumps({}), False, False), "unknown"),
            ((0, plistlib.dumps({"com.apple.Preferences": {}}), False, False), "unknown"),
            ((0, plistlib.dumps(system_app), True, False), "unknown"),
            ((0, plistlib.dumps(system_app), False, True), "unknown"),
            ((1, plistlib.dumps(system_app), False, False), "unknown"),
        ]:
            with self.subTest(expected=expected), patch.object(smoke.time, "monotonic", return_value=100), patch.object(smoke, "read_process", return_value=result) as read:
                self.assertEqual(smoke.observe_preinstall_apps("xcrun", "SOURCE-DEVICE", 104), expected)
                read.assert_called_once_with(["xcrun", "simctl", "listapps", "SOURCE-DEVICE"], 4, smoke.MAX_FILE_BYTES, merge_stderr=True)

    def test_openstep_device_inventory_converted_within_same_deadline(self):
        now = [100]
        raw = b'{ "com.apple.Preferences" = { CFBundleIdentifier = "com.apple.Preferences"; }; }'
        xml = plistlib.dumps({"com.apple.Preferences": {"CFBundleIdentifier": "com.apple.Preferences"}})

        def read(command, timeout, limit, **kwargs):
            if command[0] == "xcrun":
                self.assertEqual(timeout, 8)
                now[0] += 6
                return 0, raw, False, False
            self.assertEqual(command, ["/usr/bin/plutil", "-convert", "xml1", "-o", "-", "--", "-"])
            self.assertEqual(timeout, 2)
            self.assertEqual(kwargs["stdin"].read(), raw)
            return 0, xml, False, False

        with patch.object(smoke.time, "monotonic", side_effect=lambda: now[0]), patch.object(smoke, "read_process", side_effect=read):
            self.assertEqual(smoke.observe_preinstall_apps("xcrun", "SOURCE-DEVICE", 108), "absent")

    def test_worker_compares_complete_bundle_and_rejects_unsafe_inputs(self):
        for change, verdict in [
            ("equal", "match"), ("bytes", "mismatch"), ("extra", "mismatch"),
            ("missing", "mismatch"), ("directory", "mismatch"), ("symlink", "mismatch"),
            ("escaping", "mismatch"), ("missing bundle", "unverifiable"),
        ]:
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                built = root / "build/Runner.app"
                installed = root / "install/Runner.app"
                for bundle in (built, installed):
                    (bundle / "assets").mkdir(parents=True)
                    (bundle / "empty").mkdir()
                    (bundle / "Runner").write_bytes(b"test runner")
                    (bundle / "Info.plist").write_bytes(plistlib.dumps({
                        "CFBundleIdentifier": smoke.BUNDLE_ID, "CFBundleExecutable": "Runner",
                    }))
                    (bundle / "assets/data").write_bytes(b"correct bytes")
                    (bundle / "data-link").symlink_to("assets/data")
                if change == "bytes":
                    (installed / "assets/data").write_bytes(b"changed bytes")
                elif change == "extra":
                    (installed / "assets/extra").touch()
                elif change == "missing":
                    (installed / "empty").rmdir()
                elif change == "directory":
                    (installed / "assets/data").unlink()
                    (installed / "assets/data").mkdir()
                elif change == "symlink":
                    (installed / "data-link").unlink()
                    (installed / "data-link").symlink_to("./assets/data")
                elif change == "escaping":
                    for bundle in (built, installed):
                        (bundle / "data-link").unlink()
                        (bundle / "data-link").symlink_to("../../outside")
                    (root / "outside").touch()
                elif change == "missing bundle":
                    installed = root / "unknown/Runner.app"
                with patch.object(smoke, "RUNNER_BUNDLE", built):
                    self.assertEqual(smoke.verify_installed_bundle(str(installed / "Runner"), time.monotonic() + 5), verdict)

    def test_worker_deadline_and_incomplete_results_do_not_authorize_launch(self):
        for result in [(0, b"match\n", True, False), (0, b"match\n", False, True), (1, b"match\n", False, False), (0, b"match\nextra", False, False)]:
            with self.subTest(result=result), patch.object(smoke.time, "monotonic", return_value=100), patch.object(smoke, "read_process", return_value=result) as read:
                self.assertEqual(smoke.verify_installed_bundle("/owned/Runner.app/Runner", 100), "unverifiable")
                read.assert_not_called()
                self.assertEqual(smoke.verify_installed_bundle("/owned/Runner.app/Runner", 112), "unverifiable")
                self.assertEqual(read.call_args.args[1:], (12, 64))
        with patch.object(smoke.time, "monotonic", side_effect=[100, 110]), patch.object(smoke, "read_process", return_value=(0, b"match\n", False, False)):
            self.assertEqual(smoke.verify_installed_bundle("/owned/Runner.app/Runner", 110), "unverifiable")

    def test_timeout_recovery_keeps_failure_evidence_and_requires_the_whole_smoke(self):
        for preinstall, proof, driver, expected in [
            ("present", "match", 0, "launch_stall"),
            ("unknown", "match", 0, "launch_stall"),
            ("absent", "mismatch", 0, "launch_stall"),
            ("absent", "unverifiable", 0, "launch_stall"),
            ("absent", "match", 1, "test_failure"),
            ("absent", "match", 0, "success"),
        ]:
            with self.subTest(preinstall=preinstall, proof=proof, driver=driver), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                now = [100.0]
                installed_runner = "/owned/Runner.app/Runner"

                def read(command, timeout, limit, **kwargs):
                    self.assertEqual(command[2], "install")
                    self.assertEqual(timeout, 90)
                    now[0] += timeout
                    return -15, b"", True, False

                def verify(executable, deadline):
                    self.assertEqual((executable, deadline), (installed_runner, 205))
                    now[0] += 12
                    return proof

                def logged(command, path, timeout, budget, **kwargs):
                    if "launch" in command:
                        self.assertEqual(timeout, 28)
                        return 0, False, (smoke.BUNDLE_ID + ": 42\n").encode()
                    return driver if "drive" in command else 0, False, b""

                def app_log(xcrun, device, pid, destination, phases, timeout):
                    smoke.record_phase(phases, "body_started")
                    smoke.record_phase(phases, "smoke_pass")
                    return "http://localhost:42/", "passed"

                with (
                    patch.object(smoke, "observe_preinstall_apps", return_value=preinstall),
                    patch.object(smoke, "prepare_runner_launch", return_value=installed_runner) as prepare,
                    patch.object(smoke, "read_process", side_effect=read) as install,
                    patch.object(smoke, "verify_installed_bundle", side_effect=verify) as compare,
                    patch.object(smoke, "verify_runner_launch") as verify_pid,
                    patch.object(smoke, "run_logged", side_effect=logged) as commands,
                    patch.object(smoke, "read_app_log", side_effect=app_log) as body,
                    patch.object(smoke, "print_driver_summary"),
                    patch.object(smoke, "capture_diagnostics"),
                    patch.object(smoke.time, "monotonic", side_effect=lambda: now[0]),
                    patch.dict(os.environ, {"IOS_SMOKE_TOTAL_TIMEOUT_SECONDS": "130", "IOS_SMOKE_LAUNCH_TIMEOUT_SECONDS": "180", "GITHUB_OUTPUT": str(root / "output")}),
                ):
                    result = smoke.run_attempt("SOURCE-DEVICE", "initial", root)
                self.assertEqual(result, {"success": 0, "test_failure": 72, "launch_stall": 70}[expected])
                self.assertEqual((root / "initial/classification.txt").read_text(), expected + "\n")
                verdict = proof if preinstall == "absent" else "not_eligible"
                self.assertEqual((root / "initial/phases/install_operation").read_text(),
                                 f"started\ntimed_out exit_code=-15 output_limit=0\nverification={verdict} preinstall={preinstall}\n")
                install.assert_called_once()
                if preinstall == "absent":
                    prepare.assert_called_once_with("xcrun", "SOURCE-DEVICE", 205)
                else:
                    prepare.assert_not_called()
                    compare.assert_not_called()
                if expected != "launch_stall":
                    verify_pid.assert_called_once_with(42, installed_runner, 230)
                    body.assert_called_once()
                    self.assertTrue(any("drive" in command.args[0] for command in commands.call_args_list))
                else:
                    verify_pid.assert_not_called()
                    body.assert_not_called()
                    self.assertFalse(any("launch" in command.args[0] for command in commands.call_args_list))


class RunnerOwnershipTests(unittest.TestCase):
    device = "OWNED-DEVICE"
    container = str(Path("/tmp/Devices/OWNED-DEVICE/data/Containers/Bundle/Application/INSTALL/Runner.app").resolve())

    def test_valid_identity_read_can_finish_beyond_ten_seconds(self):
        now = [100.0]
        budgets = []

        def read(command, timeout, limit, **kwargs):
            budgets.append(timeout)
            if "get_app_container" in command:
                now[0] += 12
                return 0, (self.container + "\n").encode(), timeout < 12, False
            return 0, b"1 /sbin/launchd\n", False, False

        with patch.object(smoke.time, "monotonic", side_effect=lambda: now[0]), patch.object(smoke, "read_process", side_effect=read):
            self.assertEqual(smoke.prepare_runner_launch("xcrun", self.device, 280), self.container + "/Runner")
        self.assertEqual(budgets, [180, 168])

    def test_identity_deadline_refuses_before_or_after_the_owned_read(self):
        for before in (True, False):
            with (
                self.subTest(before=before),
                patch.object(smoke.time, "monotonic", side_effect=[110] if before else [100, 110]),
                patch.object(smoke, "read_process", return_value=(0, b"42 S /owned/Runner\n", False, False)) as read,
            ):
                with self.assertRaisesRegex(smoke.RunnerOwnershipError, "identity_deadline"):
                    smoke.runner_identity_read(["owned-query"], 110, 8192)
                if before:
                    read.assert_not_called()
                else:
                    read.assert_called_once_with(["owned-query"], 10, 8192, merge_stderr=True)

    def prepared(self, outputs):
        with patch.object(smoke, "read_process", side_effect=outputs) as read, patch.object(smoke.time, "monotonic", return_value=100):
            result = smoke.prepare_runner_launch("xcrun", self.device, 200)
        return result, read

    def test_exact_installed_container_and_content_free_process_snapshot(self):
        executable, read = self.prepared([(0, (self.container + "\n").encode(), False, False), (0, b"1 /sbin/launchd\n", False, False)])
        self.assertEqual(executable, self.container + "/Runner")
        self.assertEqual(read.call_args_list[0].args[0], ["xcrun", "simctl", "get_app_container", self.device, smoke.BUNDLE_ID, "app"])
        self.assertEqual(read.call_args_list[1].args[0], ["ps", "-axo", "pid=,comm=", "-ww"])
        self.assertEqual(read.call_args_list[0].args[1:3], (100, 8192))
        self.assertEqual(read.call_args_list[1].args[1:3], (100, smoke.MAX_FILE_BYTES))

    def test_identity_refusal_retains_stage_and_actual_outcome_without_private_content(self):
        for status, timed_out, exceeded, reason in [
            (0, True, False, "identity_incomplete"),
            (0, False, True, "identity_incomplete"),
            (7, False, False, "identity_exit"),
        ]:
            with self.subTest(status=status, timed_out=timed_out, exceeded=exceeded), patch.object(smoke.time, "monotonic", return_value=100), patch.object(smoke, "read_process", return_value=(status, b"private response", timed_out, exceeded)):
                with self.assertRaises(smoke.RunnerOwnershipError) as caught:
                    smoke.runner_identity_read(["private-command-path"], 200, 8192, "prelaunch_ps")
            self.assertEqual(str(caught.exception), f"stage=prelaunch_ps reason={reason} timed_out={int(timed_out)} output_limit={int(exceeded)} exit_code={status} deadline_expired=0")
            self.assertNotIn("private", str(caught.exception))
        with patch.object(smoke.time, "monotonic", return_value=100), patch.object(smoke, "read_process", side_effect=OSError("private path")):
            with self.assertRaises(smoke.RunnerOwnershipError) as caught:
                smoke.runner_identity_read(["private-command-path"], 200, 8192, "container")
        self.assertEqual(str(caught.exception), "stage=container reason=identity_unavailable timed_out=unknown output_limit=unknown exit_code=unknown deadline_expired=unknown")

    def test_preexisting_runner_unknown_or_incomplete_identity_is_refused(self):
        container = (0, (self.container + "\n").encode(), False, False)
        snapshots = [
            (0, ("42 " + self.container + "/Runner\n").encode(), False, False),
            (0, b"42 /tmp/Devices/OWNED-DEVICE/data/Containers/Bundle/Application/OLD/Runner.app/Runner\n", False, False),
            (0, b"42 Runner\n", False, False),
            (0, b"malformed\n", False, False), (0, b"", False, False),
            (1, b"private snapshot detail", False, False),
            (0, b"1 /sbin/launchd\n", True, False),
            (0, b"1 /sbin/launchd\n", False, True),
        ]
        for snapshot in snapshots:
            with self.subTest(snapshot=snapshot), self.assertRaises(smoke.RunnerOwnershipError) as caught:
                self.prepared([container, snapshot])
            self.assertNotIn("private", str(caught.exception))
        for invalid in ["/tmp/Runner.app", self.container.replace(self.device, "OTHER-DEVICE"), self.container + "\nprivate path"]:
            with self.subTest(container=invalid), self.assertRaises(smoke.RunnerOwnershipError):
                self.prepared([(0, invalid.encode(), False, False)])

    def test_other_simulator_runner_is_not_signalled_or_mistaken_for_this_runner(self):
        result, _ = self.prepared([(0, (self.container + "\n").encode(), False, False), (0, b"42 /tmp/Devices/OTHER-DEVICE/data/Containers/Bundle/Application/INSTALL/Runner.app/Runner\n", False, False)])
        self.assertEqual(result, self.container + "/Runner")

    def test_returned_pid_requires_exact_live_executable(self):
        executable = self.container + "/Runner"
        for result, accepted in [
            ((0, ("42 S " + executable + "\n").encode(), False, False), True),
            ((0, ("42 Z " + executable + "\n").encode(), False, False), False),
            ((0, ("42 ? " + executable + "\n").encode(), False, False), False),
            ((0, ("43 S " + executable + "\n").encode(), False, False), False),
            ((0, b"42 S /other/Runner.app/Runner\n", False, False), False),
            ((1, b"", False, False), False),
            ((0, b"", False, False), False),
            ((0, ("42 S " + executable + "\n").encode(), True, False), False),
        ]:
            with self.subTest(result=result), patch.object(smoke, "read_process", return_value=result) as read, patch.object(smoke.time, "monotonic", return_value=100):
                if accepted:
                    smoke.verify_runner_launch(42, executable, 200)
                else:
                    with self.assertRaises(smoke.RunnerOwnershipError):
                        smoke.verify_runner_launch(42, executable, 200)
                self.assertEqual(read.call_args.args[0], ["ps", "-p", "42", "-o", "pid=,stat=,comm=", "-ww"])

    def test_unknown_prelaunch_or_fast_exit_never_reads_body_or_starts_driver(self):
        for prelaunch in [True, False]:
            with self.subTest(prelaunch=prelaunch), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                with patch.object(smoke, "observe_preinstall_apps", return_value="unknown"), patch.object(smoke, "prepare_runner_launch", **({"side_effect": smoke.RunnerOwnershipError("runner_preexisting")} if prelaunch else {"return_value": self.container + "/Runner"})), patch.object(smoke, "verify_runner_launch", side_effect=smoke.RunnerOwnershipError("returned_process_unobserved")), patch.object(smoke, "run_logged", side_effect=[(0, False, b"")] * 3 + [(0, False, (smoke.BUNDLE_ID + ": 42\n").encode())]) as logged, patch.object(smoke, "read_process", return_value=(0, b"", False, False)), patch.object(smoke, "read_app_log") as app_log, patch.object(smoke, "capture_diagnostics"), patch.dict(os.environ, {"GITHUB_OUTPUT": str(root / "output")}):
                    self.assertEqual(smoke.run_attempt(self.device, "initial", root), 72)
                self.assertEqual((root / "initial/classification.txt").read_text(), "launch_failure\n")
                app_log.assert_not_called()
                launches = [c.args[0] for c in logged.call_args_list if "launch" in c.args[0]]
                self.assertEqual(len(launches), 0 if prelaunch else 1)
                if launches:
                    self.assertEqual(launches[0][1:5], ["simctl", "launch", self.device, smoke.BUNDLE_ID])
                    self.assertNotIn("--terminate-running-process", launches[0])
                    self.assertEqual(launches[0][5:], ["--enable-dart-profiling", "--disable-vm-service-publication", "--enable-checked-mode", "--verify-entry-points"])
                self.assertFalse(any("drive" in c.args[0] for c in logged.call_args_list))


if __name__ == "__main__":
    unittest.main()
