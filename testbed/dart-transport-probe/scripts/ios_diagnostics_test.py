import contextlib
import io
import json
import os
import signal
import subprocess
from pathlib import Path
import sys
import tempfile
import time
import unittest
from unittest import mock

import ios_diagnostics as diagnostics


def event(kind="rpc", state="failure", scenario="trusted_tls", code="deadline_exceeded", **extra):
    return dict(schema=1, transport="grpc", kind=kind, scenario=scenario,
                method="PutVertex", state=state, code=code,
                stack=["probe.dart:50:7"], **extra)


class IOSDiagnosticsTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.output = self.root / "diagnostics"
        self.output.mkdir()
        diagnostics.write_json(self.output / "state.json", {
            "phase": "observing", "pid": 66208, "transport": "grpc",
        })

    def marker(self, value):
        return "Runner: flutter: " + diagnostics.PREFIX + json.dumps(value)

    def fake_log_command(self, lines):
        script = self.root / "fake_xcrun.py"
        script.write_text(
            "import sys\n"
            "assert sys.argv[1:] == ['simctl', 'spawn', 'fixture-device', 'log', 'show', '--last', '5m', '--style', 'compact', '--predicate', 'processIdentifier == 66208']\n"
            f"print({chr(10).join(lines)!r})\n"
        )
        return [sys.executable, str(script)]

    def finish(self, lines, status=0):
        with contextlib.redirect_stdout(io.StringIO()):
            code = diagnostics.finish(self.output, "fixture-device", status,
                                      self.fake_log_command(lines))
        collection = json.loads((self.output / "app-collection.json").read_text())
        self.assertEqual(collection.get("exit_code"), 0, collection)
        return code, json.loads((self.output / "classification.json").read_text())

    def test_forced_rpc_failure_is_blocking_and_identifies_scenario_method(self):
        code, result = self.finish([
            "The Dart VM service is listening on http://127.0.0.1:1234/SECRET/",
            self.marker(event()),
            self.marker(event(kind="result")),
        ], status=1)
        self.assertEqual(code, 1)
        self.assertEqual(result["classification"], "rpc_failure")
        self.assertEqual(result["scenario"], "trusted_tls")
        self.assertEqual(result["method"], "PutVertex")
        self.assertEqual(result["code"], "deadline_exceeded")
        self.assertFalse(result["retry_eligible"])
        self.assertNotIn("SECRET", "".join(p.read_text() for p in self.output.iterdir()))
        self.assertTrue(diagnostics.load_state(self.output)["vm_started"])

    def test_expected_negative_failures_are_distinct_from_success(self):
        lines = [self.marker(event(scenario=scenario, state="expected_failure", code=code))
                 for scenario, code in (("wrong_host", "unavailable"), ("missing_auth", "unauthenticated"))]
        lines.append(self.marker(event(kind="result", state="success", scenario="marker", code="none")))
        code, result = self.finish(lines)
        self.assertEqual(code, 0)
        self.assertEqual(result["classification"], "success")
        retained = (self.output / "app.jsonl").read_text()
        self.assertIn("expected_failure", retained)
        self.assertIn("unauthenticated", retained)

    def test_unexpected_negative_success_or_assertion_cannot_pass(self):
        for failure_code in ("assertion", "unexpected_success"):
            with self.subTest(code=failure_code):
                code, result = self.finish([
                    self.marker(event(kind="result", scenario="missing_auth", code=failure_code)),
                    self.marker(event(kind="result", state="success", scenario="marker", code="none")),
                ])
                self.assertEqual(code, 1)
                self.assertEqual(result["classification"], "application_failure")
                self.assertFalse(result["retry_eligible"])

    def test_application_success_never_overrides_failed_host_observation(self):
        code, result = self.finish([
            self.marker(event(kind="result", state="success", scenario="marker", code="none")),
        ], status=1)
        self.assertEqual(code, 1)
        self.assertEqual(result["classification"], "wire_observer_failure")

    def test_retained_first_rpc_failure_survives_cleanup_failure_and_outer_stop(self):
        evidence = diagnostics.AppEvidence(self.output)
        evidence.line(self.marker(event()))
        cleanup = event(code="unavailable")
        cleanup["method"] = "ChannelShutdown"
        evidence.line(self.marker(cleanup))
        # The always-run finalizer can classify persisted evidence even when
        # an outer timeout prevented the shell's normal finish handler.
        summary = diagnostics.summarize(diagnostics.load_state(self.output), 1)
        self.assertEqual(summary["classification"], "rpc_failure")
        self.assertEqual(summary["method"], "PutVertex")
        self.assertEqual(summary["code"], "deadline_exceeded")
        self.assertFalse(summary["retry_eligible"])

    def test_no_failure_or_incomplete_phase_is_retry_eligible(self):
        for state, expected in (
            ({"phase": "building"}, "build_failure"),
            ({"phase": "launching"}, "launch_failure"),
            ({"phase": "observing", "vm_started": True}, "probe_incomplete"),
            ({"phase": "observing", "runtime_failure": True}, "application_failure"),
        ):
            with self.subTest(expected=expected):
                self.assertEqual(diagnostics.classify(state, 1), expected)

    def test_schema_allowlist_drops_raw_error_fields_and_rejects_secret_labels(self):
        safe = diagnostics.safe_event(self.marker(event(message="Bearer SECRET", cookie="SECRET")), "grpc")
        self.assertNotIn("SECRET", json.dumps(safe))
        for key in ("scenario", "method", "code", "state"):
            for invalid in ("SECRET", ["SECRET"], {"SECRET": 1}, None):
                value = event()
                value[key] = invalid
                self.assertIsNone(diagnostics.safe_event(self.marker(value), "grpc"))
        value = event()
        value["stack"] = ["https://idp.invalid/SECRET"]
        self.assertIsNone(diagnostics.safe_event(self.marker(value), "grpc"))
        self.assertIsNone(diagnostics.safe_event(self.marker(event()), "connect"))

    def test_noisy_app_output_remains_bounded_without_losing_failure(self):
        evidence = diagnostics.AppEvidence(self.output)
        evidence.line(self.marker(event()))
        for line_number in range(1000):
            value = event(kind="rpc", state="success", code="none")
            value["stack"] = [f"probe.dart:{line_number}:1"]
            evidence.line(self.marker(value))
        self.assertEqual(diagnostics.classify(diagnostics.load_state(self.output), 0), "rpc_failure")
        self.assertLessEqual(len(evidence.seen), 256)
        self.assertLessEqual((self.output / "app.jsonl").stat().st_size, diagnostics.MAX_FILE)

    def test_bounded_reader_omits_oversized_secret_lines_and_reaps_timeout(self):
        retained = []
        script = "import sys,time; print('SECRET'*10000,flush=True); print('safe-line',flush=True); time.sleep(30)"
        started = time.monotonic()
        result = diagnostics.read_lines([sys.executable, "-c", script], retained.append, timeout=0.5)
        self.assertTrue(result["timed_out"])
        self.assertEqual(result["oversized_lines_omitted"], 1)
        self.assertEqual(retained, ["safe-line\n"])
        self.assertLess(time.monotonic() - started, 5)

    def test_reader_reaps_its_process_when_consumer_fails(self):
        created = []
        popen = diagnostics.subprocess.Popen

        def capture(*args, **kwargs):
            process = popen(*args, **kwargs)
            created.append(process)
            return process

        def reject(_line):
            raise ValueError("diagnostic consumer interrupted")

        with mock.patch.object(diagnostics.subprocess, "Popen", side_effect=capture):
            with self.assertRaises(ValueError):
                diagnostics.read_lines([sys.executable, "-c", "import time; print('event',flush=True); time.sleep(30)"], reject)
        self.assertIsNotNone(created[0].returncode)
        self.assertTrue(created[0].stdout.closed)

    def test_reader_stops_owned_descendant_after_launcher_exits(self):
        created = []
        retained = []
        popen = diagnostics.subprocess.Popen

        def capture(*args, **kwargs):
            process = popen(*args, **kwargs)
            created.append(process)
            return process

        # The launcher exits; its child keeps the reader pipe open. A timeout
        # must clean the owned group even though the direct child was reaped.
        child = "import os,time; print(os.getpid(),flush=True); time.sleep(30)"
        launcher = f"import subprocess,sys; subprocess.Popen([sys.executable, '-c', {child!r}])"
        try:
            with mock.patch.object(diagnostics.subprocess, "Popen", side_effect=capture):
                result = diagnostics.read_lines([sys.executable, "-c", launcher], retained.append, timeout=0.5)
            self.assertTrue(result["timed_out"])
            self.assertEqual(result["exit_code"], 0)
            self.assertEqual(len(retained), 1)
            child_pid = int(retained[0])
            deadline = time.monotonic() + 2
            while time.monotonic() < deadline:
                try:
                    os.kill(child_pid, 0)
                except ProcessLookupError:
                    break
                time.sleep(0.01)
            else:
                self.fail("owned log-reader descendant survived timeout")
        finally:
            if created:
                try:
                    os.killpg(created[0].pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass

    def test_missing_log_reader_records_only_safe_unavailability(self):
        diagnostics.observe_app(self.output, "fixture-device", str(self.root / "SECRET-not-installed"))
        result = json.loads((self.output / "app-collection.json").read_text())
        self.assertEqual(result, {"unavailable": True, "errno": 2})
        self.assertNotIn("SECRET", (self.output / "app-collection.json").read_text())
        self.assertEqual(diagnostics.classify(diagnostics.load_state(self.output), 1), "probe_incomplete")

    def console_attempt(self, name, lines):
        output = self.root / name
        output.mkdir()
        diagnostics.write_json(output / "state.json", {"phase": "launching", "transport": "grpc"})
        script = self.root / (name + ".py")
        script.write_text(
            "import sys\n"
            "assert sys.argv[1:] == ['simctl', 'launch', '--console', '--terminate-running-process', 'fixture-device', 'com.example.probe']\n"
            f"print({chr(10).join(lines)!r}, flush=True)\n"
        )
        diagnostics.launch_console(output, "fixture-device", "com.example.probe",
                                   [sys.executable, str(script)])
        return output

    def test_console_capture_preserves_the_existing_blocking_verdicts(self):
        success = self.marker(event(kind="result", state="success", scenario="marker"))
        negative = self.marker(event(scenario="wrong_host", state="expected_failure"))
        cases = (
            ("success", [negative, success], 0, "success"),
            ("missing_terminal", [negative], 0, "probe_incomplete"),
            ("host_failure", [success], 1, "wire_observer_failure"),
            ("rpc_failure", [self.marker(event()), success], 0, "rpc_failure"),
            ("assertion", [self.marker(event(kind="result", code="assertion")), success], 0, "application_failure"),
            ("runtime", ["Unhandled Exception: SECRET", success], 0, "application_failure"),
        )
        for name, events, status, expected in cases:
            with self.subTest(name=name):
                # Include an event before the PID acknowledgement: attaching
                # the console must not lose the first application failure.
                output = self.console_attempt(name, [events[0], "com.example.probe: 66208", *events[1:]])
                self.assertEqual(diagnostics.await_console_launch(output, timeout=0.2), 0)
                with mock.patch.object(diagnostics, "observe_app", side_effect=AssertionError("retrospective query")):
                    with contextlib.redirect_stdout(io.StringIO()):
                        code = diagnostics.finish(output, "fixture-device", status)
                result = json.loads((output / "classification.json").read_text())
                self.assertEqual(result["classification"], expected)
                self.assertEqual(code, 0 if expected == "success" else 1)
                self.assertFalse(result["retry_eligible"])
                self.assertNotIn("SECRET", "".join(p.read_text() for p in output.iterdir()))
                with self.assertRaises(ValueError):
                    diagnostics.launch_console(output, "fixture-device", "com.example.probe")

    def test_console_requires_its_exact_launch_ack_and_filters_noisy_output(self):
        output = self.console_attempt("no_ack", ["com.other.probe: 66208", "SECRET" * 20000])
        self.assertEqual(diagnostics.await_console_launch(output, timeout=0.2), 1)
        self.assertNotIn("pid", diagnostics.load_state(output))
        result = json.loads((output / "console-collection.json").read_text())
        self.assertEqual(result["oversized_lines_omitted"], 1)
        self.assertNotIn("SECRET", "".join(p.read_text() for p in output.iterdir()))

    def test_live_console_settles_terminal_event_before_stopping_owned_reader(self):
        for result_state in ("success", "failure"):
            with self.subTest(result_state=result_state):
                self.live_console_terminal(result_state)

    def live_console_terminal(self, result_state):
        output = self.root / ("live-" + result_state)
        output.mkdir()
        diagnostics.write_json(output / "state.json", {"phase": "launching", "transport": "grpc"})
        release = self.root / ("release-" + result_state)
        script = self.root / ("live_console_" + result_state + ".py")
        terminal = self.marker(event(kind="result", state=result_state, scenario="marker"))
        script.write_text(
            "import os,time\nfrom pathlib import Path\n"
            "print(f'com.example.probe: {os.getpid()}', flush=True)\n"
            f"while not Path({str(release)!r}).exists(): time.sleep(0.01)\n"
            f"print({terminal!r}, flush=True)\n"
            "time.sleep(30)\n"
        )
        worker = subprocess.Popen([sys.executable, "-c",
            f"import sys; sys.path.insert(0, {str(Path(diagnostics.__file__).parent)!r}); "
            "from pathlib import Path; import ios_diagnostics as d; "
            f"d.launch_console(Path({str(output)!r}), 'fixture-device', 'com.example.probe', "
            f"[{sys.executable!r}, {str(script)!r}])"])
        unrelated = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"])
        try:
            self.assertEqual(diagnostics.await_console_launch(output, timeout=5), 0)
            reader_pid = diagnostics.load_state(output)["pid"]
            self.assertFalse(diagnostics.load_state(output).get("terminal_success", False))
            release.write_text("release\n")
            diagnostics.settle_console(output, 0, timeout=5)
            self.assertTrue(diagnostics.load_state(output).get("terminal_" + result_state))
            worker.terminate()
            self.assertEqual(worker.wait(timeout=5), 0)
            with self.assertRaises(ProcessLookupError):
                os.kill(reader_pid, 0)
            self.assertIsNone(unrelated.poll())
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(diagnostics.finish(output, "fixture-device", 0),
                                 0 if result_state == "success" else 1)
            summary = json.loads((output / "classification.json").read_text())
            self.assertFalse(summary["retry_eligible"])
        finally:
            for process in (worker, unrelated):
                if process.poll() is None:
                    process.terminate()
                process.wait(timeout=5)

    def test_server_diagnostics_retain_only_safe_fields_with_fixed_bounds(self):
        source = self.root / "server.log"
        source.write_text((json.dumps({"time": "2026-10-05T05:29:33Z", "level": "ERROR",
                                     "msg": "TLS SECRET failed", "token": "SECRET", "error": "private@email.invalid"}) + "\n") * 3000)
        output = self.output / "server-native.jsonl"
        diagnostics.server_summary(source, output)
        content = output.read_text()
        self.assertIn('"category": "tls"', content)
        self.assertNotIn("SECRET", content)
        self.assertNotIn("email", content)
        self.assertLessEqual(output.stat().st_size, diagnostics.MAX_FILE)
        symlink = self.root / "private-link"
        symlink.symlink_to(source)
        diagnostics.server_summary(symlink, output)
        self.assertEqual(json.loads(output.read_text()), {"available": False})


if __name__ == "__main__":
    unittest.main()
