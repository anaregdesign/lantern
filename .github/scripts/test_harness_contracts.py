import contextlib
import hashlib
import io
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch

import harness_contracts as contracts


def events(names=contracts.GO_TESTS):
    rows = [dict(Action="output", Output="owned_compose_fixture_started pgid=123 evidence=synthetic\n"),
            dict(Action="output", Output="owned_compose_fixture_terminal pgid=123 inactive=true reaped=true timeout=false leftover=false evidence=synthetic\n")]
    for name in names:
        rows.extend([dict(Action="run", Test=name), dict(Action="pass", Test=name)])
    rows.append(dict(Action="pass"))
    return "\n".join(json.dumps(dict(Package=contracts.GO_PACKAGE, **row)) for row in rows) + "\n"


class HarnessContractsTest(unittest.TestCase):
    def test_every_selected_actual_consumer_has_exact_run_and_pass_events(self):
        self.assertEqual(contracts.require_go_events(events()), len(contracts.GO_TESTS))
        source = (contracts.ROOT / "testbed/bench/owned_compose_test.go").read_text()
        source += (contracts.ROOT / "testbed/bench/scenarios_gate_test.go").read_text()
        names = re.findall(r"(?m)^func (Test\w+)\(t \*testing.T\)", source)
        for selected in contracts.GO_TESTS:
            self.assertEqual(names.count(selected), 1, selected)

    def test_missing_repeated_skipped_failed_foreign_or_unselected_events_refuse(self):
        valid = events()
        first = contracts.GO_TESTS[0]
        wrong = ["", events(contracts.GO_TESTS[1:]), valid.replace('"run"', '"output"', 1),
                 valid.replace('"pass"', '"skip"', 1), valid.replace('"pass"', '"fail"', 1),
                 valid + valid, valid.replace(contracts.GO_PACKAGE, "foreign", 1),
                 events((*contracts.GO_TESTS, "TestUnselected")), valid + "not JSON\n",
                 valid.replace('"pass"', '"output"'), valid.replace("inactive=true", "inactive=false"),
                 valid.replace("reaped=true", "reaped=false"), valid.replace("timeout=false", "timeout=true"),
                 valid.replace("leftover=false", "leftover=true"),
                 "\n".join(valid.splitlines()[1:]), "\n".join(valid.splitlines()[2:])]
        for action in ("skip", "fail"):
            wrong.append(valid + json.dumps(dict(Package=contracts.GO_PACKAGE, Action=action, Test=first + "/subtest")))
        for output in wrong:
            with self.subTest(output=output[:100]), self.assertRaises((ValueError, json.JSONDecodeError)):
                contracts.require_go_events(output)
        with self.assertRaisesRegex(ValueError, "selection"):
            contracts.require_go_events(valid, ())

    def test_go_command_uses_the_exact_selector_uncached_and_owns_its_group(self):
        process = Mock(returncode=0)
        diagnostics = b"go: downloading example.invalid/dependency v1.2.3\n"
        def spawn(*args, **kwargs):
            kwargs["stdout"].write(events().encode())
            kwargs["stderr"].write(diagnostics)
            self.assertIsNot(kwargs["stdout"], kwargs["stderr"])
            return process
        with patch.object(contracts.subprocess, "Popen", side_effect=spawn) as start:
            self.assertEqual(contracts.run_go(), (0, events().encode(), diagnostics))
        command = start.call_args.args[0]
        self.assertEqual(command, ["go", "test", "-json", "-count=1", "-timeout=30m", "./testbed/bench",
                                   "-run", "^(" + "|".join(contracts.GO_TESTS) + ")$"])
        self.assertTrue(start.call_args.kwargs["start_new_session"])
        self.assertEqual(start.call_args.kwargs["env"]["GOTOOLCHAIN"], "local")
        self.assertNotIn("docker", command)

    def test_timeout_preserves_output_and_stops_only_the_owned_group(self):
        process = Mock(pid=12345)
        process.wait.side_effect = [subprocess.TimeoutExpired("go", 2100),
                                   subprocess.TimeoutExpired("go", 10), subprocess.TimeoutExpired("go", 10)]
        def spawn(*args, **kwargs):
            kwargs["stdout"].write(b"original failure\n")
            kwargs["stderr"].write(b"original diagnostic\n")
            return process
        with patch.object(contracts.subprocess, "Popen", side_effect=spawn), \
                patch.object(contracts.os, "killpg") as stop:
            self.assertEqual(contracts.run_go(), (1, b"original failure\n", b"original diagnostic\n"))
        self.assertEqual(stop.call_args_list[0].args, (12345, signal.SIGTERM))
        self.assertEqual(stop.call_args_list[1].args, (12345, signal.SIGKILL))

    def test_real_environment_calls_release_consumer_and_restores_raw_selectors(self):
        pin = re.search(r"(?m)^go (\S+)$", (contracts.ROOT / "go.mod").read_text())[1]
        observed = []
        def consume(directory):
            raw = {key: os.environ[key] for key in ("GOENV", "GOTOOLCHAIN", "GOFLAGS", "GOWORK")}
            observed.append(raw)
            self.assertEqual(Path(raw["GOWORK"]), directory / "go.work")
            self.assertEqual((directory / "go.mod").read_text().splitlines()[-1], "go " + pin)
            return raw
        original = {key: os.environ.get(key) for key in ("GOENV", "GOTOOLCHAIN", "GOFLAGS", "GOWORK")}
        with patch.object(contracts.release, "command", return_value=f"go version go{pin} linux/amd64\n".encode()) as command, \
                patch.object(contracts.release, "go_environment", side_effect=consume):
            result = contracts.real_go_environment()
        self.assertEqual(result["raw_goenv"], "off")
        self.assertEqual(result["effective_goenv"], "")
        self.assertEqual(observed[0]["GOTOOLCHAIN"], "local")
        self.assertEqual(command.call_args.args[0], ["go", "version"])
        self.assertEqual(original, {key: os.environ.get(key) for key in original})
        for version in (b"go version go0.0.0 linux/amd64\n", b"unavailable\n"):
            with patch.object(contracts.release, "command", return_value=version), \
                    self.assertRaisesRegex(ValueError, "pinned Go unavailable"):
                contracts.real_go_environment()

    def test_python_skip_is_incomplete_even_when_unittest_reports_success(self):
        class Skipped(unittest.TestCase):
            @unittest.skip("missing required tool")
            def test_missing(self):
                pass
        suite = unittest.TestSuite([Skipped("test_missing")])
        with patch.object(contracts.unittest.defaultTestLoader, "loadTestsFromNames", return_value=suite):
            qualified, count, output = contracts.python_contracts(("release-binaries",))
        self.assertFalse(qualified)
        self.assertEqual(count, 1)
        self.assertIn("skipped", output)

    def test_focused_groups_default_union_and_failure_keep_original_log(self):
        with tempfile.TemporaryDirectory() as temporary:
            evidence = Path(temporary) / "evidence"
            with patch.object(contracts, "real_go_environment", return_value={"raw_goenv": "off"}), \
                    patch.object(contracts, "python_contracts", return_value=(True, 9, "python passed\n")) as python, \
                    patch.object(contracts, "run_go", return_value=(1, b"original strict failure\n", b"original stderr\n")), \
                    contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(contracts.main(["--evidence", str(evidence)]), 1)
            self.assertEqual(python.call_args.args[0], tuple(contracts.GROUPS))
            self.assertEqual((evidence / "go.jsonl").read_text(), "original strict failure\n")
            self.assertEqual((evidence / "go.stderr.log").read_bytes(), b"original stderr\n")
            self.assertEqual(json.loads((evidence / "verdict.json").read_text())["verdict"], "fail")
        with patch.object(contracts, "python_contracts", return_value=(True, 2, "passed\n")) as python, \
                patch.object(contracts, "run_go") as go, patch.object(contracts, "real_go_environment") as environment, \
                contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(contracts.main(["--group", "git-fixture", "--group", "git-fixture"]), 0)
        self.assertEqual(python.call_args.args[0], ("git-fixture",))
        go.assert_not_called()
        environment.assert_not_called()

    def test_tools_only_reports_python_as_not_selected(self):
        with patch.object(contracts, "python_contracts") as python, \
                patch.object(contracts, "run_go", return_value=(0, events().encode(), b"")), \
                patch.object(contracts, "real_go_environment", return_value={"raw_goenv": "off"}), \
                contextlib.redirect_stdout(io.StringIO()) as output:
            self.assertEqual(contracts.main(["--group", "bench-time", "--tools-only"]), 0)
        report = json.loads(output.getvalue().splitlines()[-1])
        self.assertEqual(report["claim"], "harness-tool-contracts")
        self.assertEqual(report["python_status"], "NOT_SELECTED")
        python.assert_not_called()

    def test_download_stderr_stays_raw_and_only_stdout_events_qualify(self):
        diagnostics = b"go: downloading example.invalid/dependency v1.2.3\n"
        for code, output, expected in ((0, events().encode(), 0),
                                       (7, events().encode(), 1),
                                       (0, b"not test JSON\n", 1),
                                       (0, events().replace("inactive=true", "inactive=false").encode(), 1)):
            with self.subTest(code=code, output=output[:80]), tempfile.TemporaryDirectory() as temporary:
                evidence = Path(temporary) / "evidence"
                process = Mock(returncode=code)
                def spawn(*args, **kwargs):
                    kwargs["stdout"].write(output)
                    kwargs["stderr"].write(diagnostics)
                    return process
                with patch.object(contracts.subprocess, "Popen", side_effect=spawn), \
                        patch.object(contracts, "real_go_environment", return_value={"raw_goenv": "off"}), \
                        contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
                    self.assertEqual(contracts.main(["--tools-only", "--group", "bench-time", "--evidence", str(evidence)]), expected)
                report = json.loads((evidence / "verdict.json").read_text())
                self.assertEqual(report["go_exit_code"], code)
                self.assertEqual(report["verdict"], "pass" if expected == 0 else "fail")
                for name, raw in (("go.jsonl", output), ("go.stderr.log", diagnostics)):
                    self.assertEqual((evidence / name).read_bytes(), raw)
                    self.assertEqual(report["logs"][name], {"sha256": hashlib.sha256(raw).hexdigest(), "bytes": len(raw)})

    def test_tools_only_rejects_a_group_without_real_tool_consumers(self):
        with tempfile.TemporaryDirectory() as temporary:
            evidence = Path(temporary) / "evidence"
            with patch.object(contracts, "python_contracts") as python, \
                    patch.object(contracts, "run_go") as go, \
                    patch.object(contracts, "real_go_environment") as environment, \
                    contextlib.redirect_stderr(io.StringIO()) as output, \
                    self.assertRaises(SystemExit) as refused:
                contracts.main(["--group", "git-fixture", "--tools-only", "--evidence", str(evidence)])
            self.assertEqual(refused.exception.code, 2)
            self.assertIn("requires a group with real-tool consumers", output.getvalue())
            self.assertFalse(evidence.exists())
        python.assert_not_called()
        go.assert_not_called()
        environment.assert_not_called()


class WorkflowPlacementTest(unittest.TestCase):
    def test_source_contracts_follow_pinned_go_and_precede_expensive_dependents(self):
        workflow = (contracts.ROOT / ".github/workflows/go.yml").read_text()
        scope = workflow.split("  scope:\n", 1)[1].split("\n  functional-validation:", 1)[0]
        self.assertLess(scope.index("actions/setup-go@v7"), scope.index("harness_contracts.py"))
        self.assertIn("go-version: '" + re.search(r"(?m)^go (\S+)$", (contracts.ROOT / "go.mod").read_text())[1] + "'", scope)
        self.assertNotIn("continue-on-error", scope)
        step = scope.split("      - name: Test early real-tool harness contracts\n", 1)[1].split("      - name:", 1)[0]
        condition = "if: steps.docs.outputs.docs_only != 'true' && steps.evidence.outputs.evidence_only != 'true'"
        self.assertIn(condition, step)
        setup = scope.split("      - name: Set up pinned Go for harness contracts\n", 1)[1].split("      - name:", 1)[0]
        self.assertIn(condition, setup)
        self.assertIn("--tools-only", step)
        self.assertIn("steps.native.outputs.harness_contracts_required != 'false'", step)
        self.assertLess(scope.index("unittest discover -s .github/scripts"), scope.index("harness_contracts.py"))
        self.assertLess(scope.index("evidence_only_pr.py classify"), scope.index("actions/setup-go@v7"))
        for name in ("functional-validation", "configured-source-integration", "native-deployment"):
            body = re.search(r"(?ms)^  " + name + r":\n(.*?)(?=^  [\w-]+:|\Z)", workflow)[1]
            self.assertIn("needs: scope", body)

    def test_manual_contracts_precede_images_and_never_create_a_schedule(self):
        workflow = (contracts.ROOT / ".github/workflows/bench-nightly.yml").read_text()
        contract = workflow.index("harness_contracts.py")
        self.assertLess(workflow.index("actions/setup-go@v7"), contract)
        self.assertLess(contract, workflow.index("name: Build lantern:local image"))
        self.assertLess(contract, workflow.index("name: Run manual leak-gate sweep"))
        self.assertIn("workflow_dispatch:", workflow)
        self.assertNotRegex(workflow, r"(?m)^\s+schedule:")


if __name__ == "__main__":
    unittest.main()
