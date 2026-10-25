"""Deterministic bounds and privacy checks for the iOS CI host Health control."""

import contextlib
import importlib.util
import io
import json
from pathlib import Path
import signal
import tempfile
import unittest
from unittest.mock import Mock, call, patch


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location(
    "ios_backend_observation",
    ROOT / "sdks/dart/example/tool/ios_backend_observation.py",
)
CONTROL = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CONTROL)


class IOSBackendObservationTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.pid = self.root / "private-pid-path"
        self.pid.write_text("10321\n")
        self.output = self.root / "observations"
        self.supported_platform = CONTROL._supported_platform
        self.enterContext(patch.object(
            CONTROL, "_supported_platform", return_value=True,
        ))
        self.kill = self.enterContext(patch.object(CONTROL.os, "kill"))
        self.response = Mock(status=200)
        self.response.read.return_value = b'{"status":"SERVING"}'
        self.connection = Mock()
        self.connection.getresponse.return_value = self.response
        self.http = self.enterContext(patch.object(
            CONTROL.http.client, "HTTPConnection", return_value=self.connection,
        ))
        self.previous_handler = object()
        self.handler = self.enterContext(patch.object(CONTROL.signal, "signal"))
        self.gethandler = self.enterContext(patch.object(
            CONTROL.signal, "getsignal", return_value=self.previous_handler,
        ))
        self.gettimer = self.enterContext(patch.object(
            CONTROL.signal, "getitimer", return_value=(0.0, 0.0), create=True,
        ))
        self.timer = self.enterContext(patch.object(
            CONTROL.signal, "setitimer", create=True,
        ))
        # Unit tests never arm a real timer or use a real socket/process signal.
        self.enterContext(patch.object(CONTROL.signal, "SIGALRM", 14, create=True))
        self.enterContext(patch.object(CONTROL.signal, "ITIMER_REAL", 0, create=True))
        self.clock = self.enterContext(patch.object(
            CONTROL.time, "monotonic", return_value=100.0,
        ))

    def observe(self):
        return CONTROL.observe("initial", "before", self.pid)

    def main(self, attempt="initial", phase="before"):
        with contextlib.redirect_stdout(io.StringIO()) as output:
            with contextlib.redirect_stderr(io.StringIO()) as errors:
                status = CONTROL.main([
                    attempt, phase, str(self.pid), str(self.output),
                ])
        return status, output.getvalue(), errors.getvalue()

    def test_exact_single_serving_post_and_whole_deadline_are_preserved(self):
        for serving in ("SERVING", "SERVING_STATUS_SERVING"):
            with self.subTest(status=serving):
                self.http.reset_mock()
                self.connection.reset_mock()
                self.response.read.return_value = json.dumps({"status": serving}).encode()
                self.timer.reset_mock()
                self.handler.reset_mock()
                self.clock.side_effect = [100.0, 100.125]
                record = self.observe()
                self.assertEqual(record["health_outcome"], "serving")
                self.assertEqual(record["health_status"], "serving")
                self.assertEqual(record["pid_presence"], "present")
                self.assertEqual(record["http_status"], 200)
                self.assertEqual(record["elapsed_ms"], 125)
                self.assertFalse(record["continuous_health"])
                self.http.assert_called_once_with("127.0.0.1", 6380, timeout=10)
                self.connection.request.assert_called_once_with(
                    "POST", "/grpc.health.v1.Health/Check", body=b"{}",
                    headers={"Content-Type": "application/json",
                             "Connect-Protocol-Version": "1"},
                )
                self.response.read.assert_called_with(CONTROL.MAX_RESPONSE_BYTES + 1)
                self.assertEqual(self.timer.call_args_list, [
                    call(signal.ITIMER_REAL, 10),
                    call(signal.ITIMER_REAL, 0),
                    call(signal.ITIMER_REAL, 0.0, 0.0),
                ])
                self.assertEqual(self.handler.call_args_list, [
                    call(signal.SIGALRM, CONTROL._expired),
                    call(signal.SIGALRM, self.previous_handler),
                ])
                self.connection.close.assert_called_once_with()
        self.assertEqual(self.kill.call_args_list, [call(10321, 0)] * 2)

    def test_malformed_oversized_noisy_and_private_responses_are_normalized(self):
        cases = [
            (b'private prefix {"status":"SERVING"}', "malformed"),
            (b'{"status":"SERVING","token":"private-token"}', "malformed"),
            (b'{"status":"NOT_SERVING","status":"SERVING"}', "malformed"),
            (b'{"status":true}', "malformed"),
            (b'{"status":1}', "malformed"),
            (b'[{"status":"SERVING"}]', "malformed"),
            (b"\xff", "malformed"),
            (b"x" * (CONTROL.MAX_RESPONSE_BYTES + 1), "oversized"),
            (b'{"status":"NOT_SERVING"}', "not_serving"),
            (b'{"status":"private-token"}', "not_serving"),
        ]
        for body, outcome in cases:
            with self.subTest(outcome=outcome, bytes=len(body)):
                self.response.read.return_value = body
                self.http.reset_mock()
                record = self.observe()
                self.assertEqual(record["health_outcome"], outcome)
                self.assertNotIn("private", json.dumps(record))
                self.http.assert_called_once()

    def test_http_error_and_redirect_never_read_or_follow_private_body(self):
        for status in (302, 401, 503):
            with self.subTest(status=status):
                self.response.status = status
                self.response.read.reset_mock()
                self.connection.request.reset_mock()
                record = self.observe()
                self.assertEqual(record["health_outcome"], "http_error")
                self.assertEqual(record["http_status"], status)
                self.response.read.assert_not_called()
                self.connection.request.assert_called_once()

    def test_deadline_interrupts_pid_headers_and_body_without_rearming(self):
        for location in ("pid", "headers", "body"):
            with self.subTest(location=location):
                self.http.reset_mock()
                self.timer.reset_mock()
                self.kill.side_effect = CONTROL.ControlTimeout if location == "pid" else None
                self.connection.getresponse.side_effect = (
                    CONTROL.ControlTimeout if location == "headers" else None
                )
                self.response.read.side_effect = (
                    CONTROL.ControlTimeout if location == "body" else None
                )
                record = self.observe()
                self.assertEqual(record["health_outcome"], "timeout")
                self.assertEqual(self.timer.call_args_list, [
                    call(signal.ITIMER_REAL, 10),
                    call(signal.ITIMER_REAL, 0),
                    call(signal.ITIMER_REAL, 0.0, 0.0),
                ])
                if location == "pid":
                    self.http.assert_not_called()
                else:
                    self.http.assert_called_once()
        with self.assertRaises(CONTROL.ControlTimeout):
            CONTROL._expired(None, None)
        self.kill.side_effect = None
        self.http.reset_mock()
        with patch.object(CONTROL.Path, "open", side_effect=CONTROL.ControlTimeout):
            self.assertEqual(self.observe()["health_outcome"], "timeout")
        self.http.assert_not_called()

    def test_dead_and_invalid_pid_never_become_a_serving_precondition(self):
        for raw in ("-1\n", "0\n", "private-token", "9" * 33, "2147483648\n"):
            with self.subTest(raw_length=len(raw)):
                self.pid.write_text(raw)
                self.kill.reset_mock()
                record = self.observe()
                self.assertEqual(record["pid_presence"], "pid_invalid")
                self.assertEqual(record["health_outcome"], "serving")
                self.kill.assert_not_called()
        self.pid.write_text("10321\n")
        self.kill.side_effect = ProcessLookupError("private-error")
        self.assertEqual(self.observe()["pid_presence"], "absent")
        self.kill.side_effect = PermissionError("private-error")
        self.assertEqual(self.observe()["pid_presence"], "unavailable")
        self.pid.unlink()
        self.assertEqual(self.observe()["pid_presence"], "pid_missing")

    def test_unsupported_platform_and_busy_timer_do_not_touch_pid_or_socket(self):
        with patch.object(CONTROL.os, "name", "nt"):
            with patch.object(CONTROL, "_supported_platform", self.supported_platform):
                record = self.observe()
        self.assertEqual(record["health_outcome"], "unsupported")
        self.http.assert_not_called()
        self.kill.assert_not_called()
        self.gethandler.assert_not_called()
        self.timer.assert_not_called()
        with patch.object(CONTROL.os, "name", "posix"):
            with patch.object(CONTROL, "_supported_platform", self.supported_platform):
                with patch.dict(CONTROL.signal.__dict__):
                    del CONTROL.signal.SIGALRM
                    self.assertEqual(self.observe()["health_outcome"], "unsupported")
        self.http.assert_not_called()
        self.kill.assert_not_called()
        self.gethandler.assert_not_called()
        self.gettimer.return_value = (2.0, 1.0)
        self.assertEqual(self.observe()["health_outcome"], "control_unavailable")
        self.http.assert_not_called()
        self.kill.assert_not_called()
        self.handler.assert_not_called()
        self.timer.assert_not_called()

    def test_after_observation_cannot_replace_app_failure_and_never_prints_error(self):
        original = self.root / "classification.txt"
        original.write_text("test_failure\n")
        self.connection.request.side_effect = OSError("private-token /private/path")
        status, output, errors = self.main(phase="after")
        self.assertEqual((status, errors), (0, ""))
        self.assertEqual(original.read_text(), "test_failure\n")
        self.assertNotIn("private", output)
        record = json.loads((self.output / "initial-after.json").read_text())
        self.assertEqual(record["health_outcome"], "transport_error")
        status, output, errors = self.main(phase="before")
        self.assertEqual(status, 1)
        self.assertEqual(errors, "")
        self.assertEqual(json.loads(output)["health_outcome"], "transport_error")

    def test_four_fixed_small_records_refuse_duplicates_before_any_request(self):
        for attempt in ("initial", "retry"):
            for phase in ("before", "after"):
                self.assertEqual(self.main(attempt, phase)[0], 0)
        self.assertEqual({p.name for p in self.output.iterdir()}, CONTROL.SLOTS)
        for file in self.output.iterdir():
            self.assertLessEqual(file.stat().st_size, CONTROL.MAX_RECORD_BYTES)
            self.assertNotIn("private", file.read_text())
        requests = self.connection.request.call_count
        original = (self.output / "initial-before.json").read_bytes()
        status, output, errors = self.main()
        self.assertEqual((status, output, errors), (
            1, "", "ios_backend_observation_failed\n",
        ))
        self.assertEqual(self.connection.request.call_count, requests)
        self.assertEqual((self.output / "initial-before.json").read_bytes(), original)
        status, output, errors = self.main(attempt="private-token")
        self.assertEqual((status, output, errors), (
            1, "", "ios_backend_observation_failed\n",
        ))

    def test_workflow_gates_each_attempt_and_keeps_existing_smoke_limits(self):
        workflow = (ROOT / ".github/workflows/dart-sdk.yml").read_text()
        ios = workflow.split("\n  ios:\n", 1)[1].split("\n  gate:\n", 1)[0]
        names = [
            "Observe iOS backend before initial attempt",
            "Run attached iOS native smoke",
            "Observe iOS backend after initial attempt",
            "Create independently clean retry simulator",
            "Observe iOS backend before retry attempt",
            "Retry attached iOS native smoke on fresh simulator",
            "Observe iOS backend after retry attempt",
            "Upload bounded iOS backend point observations",
            "Show logs and clean up",
        ]
        positions = [ios.index("- name: " + name) for name in names]
        self.assertEqual(positions, sorted(positions))
        initial = ios.split("- name: Run attached iOS native smoke\n", 1)[1].split(
            "\n      - name:", 1,
        )[0]
        retry = ios.split("- name: Retry attached iOS native smoke on fresh simulator\n", 1)[1].split(
            "\n      - name:", 1,
        )[0]
        self.assertIn("steps.ios_backend_initial_before.outcome == 'success'", initial)
        self.assertIn("steps.ios_backend_retry_before.outcome == 'success'", retry)
        self.assertIn("steps.ios_fresh_device.outcome == 'success'", retry)
        self.assertIn('fromJSON(\'["launch_stall","attach_stall"]\')', retry)
        for attempt in (initial, retry):
            for unchanged in (
                "continue-on-error: true", "timeout-minutes: 14",
                "IOS_SMOKE_BUILD_TIMEOUT_SECONDS: 360",
                "IOS_SMOKE_BOOT_TIMEOUT_SECONDS: 180",
                "IOS_SMOKE_LAUNCH_TIMEOUT_SECONDS: 180",
                "IOS_SMOKE_TOTAL_TIMEOUT_SECONDS: 720",
            ):
                self.assertIn(unchanged, attempt)
        self.assertEqual(ios.count('echo $! > "$RUNNER_TEMP/ios-server.pid"'), 1)
        for slot in CONTROL.SLOTS:
            self.assertEqual(ios.count("/ios-backend-observations/" + slot), 1)


if __name__ == "__main__":
    unittest.main()
