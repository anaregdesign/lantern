from contextlib import contextmanager, redirect_stdout
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import local_gate_session as gate
from local_gate_plan import Step, steps


class SessionTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name)
        self.root = self.base / "repo"
        self.root.mkdir()
        self.git("init", "-b", "feature")
        self.git("config", "user.email", "gate-fixture@example.invalid")
        self.git("config", "user.name", "Gate fixture")
        for path in ("core/source.go", "pb/generated.go", "server/source.go", "proto/api.proto",
                     "tests/fixture.json", "admin/app/view.tsx", "go.mod", "README.md"):
            self.write(path, "baseline\n")
        self.commit()
        self.session = self.new_session("evidence")

    def git(self, *arguments):
        return subprocess.check_output(["git", *arguments], cwd=self.root, stderr=subprocess.DEVNULL,
                                       text=True).strip()

    def write(self, path, text):
        target = self.root / path
        target.parent.mkdir(exist_ok=True, parents=True)
        target.write_text(text)

    def commit(self):
        self.git("add", "--all")
        self.git("commit", "-qm", "fixture candidate")

    def new_session(self, name):
        session = gate.Session(self.root, self.base / name)
        # Execute real synthetic subprocesses; none are Go/Rust/Flutter validation.
        # These fixtures simulate local sessions even when unittest runs in CI.
        # The production CI carry prohibition is exercised explicitly below.
        session.env.pop("GITHUB_ACTIONS", None)
        session.plan = (
            Step("go-test-core", ".", (sys.executable, "-c", "print('fixture core pass')")),
            Step("rust-real-wire", ".", (sys.executable, "-c", "print('fixture wire pass')")),
            Step("final-clean", ".", ("git", "status", "--porcelain"), empty=True))
        session.context = lambda: "fixture-toolchain-and-environment"
        return session

    def qualify(self):
        with redirect_stdout(io.StringIO()):
            result = self.session.qualify()
        self.assertTrue(result["passed"], result)
        return result

    def modes(self, result):
        return {r["name"]: r["mode"] for r in result["steps"]}

    @contextmanager
    def mutable_inputs_control(self, head, tree, selected=None):
        # Negative control for the reviewed old execution boundary. Production
        # never exposes a choice to execute eligible steps in the developer cwd.
        yield self.root

    def test_independent_frontend_change_carries_only_eligible_pass(self):
        first = self.qualify()
        self.assertEqual(set(self.modes(first).values()), {"executed"})
        self.write("admin/app/view.tsx", "reviewed edit\n")
        self.commit()
        second = self.qualify()
        self.assertEqual(self.modes(second), {"go-test-core": "carried", "rust-real-wire": "executed",
                                             "final-clean": "executed"})
        carried = second["steps"][0]
        self.assertEqual(carried["from_head"], first["head"])
        self.assertEqual(carried["from_tree"], first["tree"])

    def test_ci_execution_always_reruns_instead_of_carrying_local_success(self):
        self.qualify()
        self.session.env["GITHUB_ACTIONS"] = "true"
        result = self.qualify()
        self.assertEqual(set(self.modes(result).values()), {"executed"})
        self.assertIn("local feature branch", result["carry_reason"])

    def test_dependency_source_fixture_generated_proto_and_lock_changes_rerun(self):
        for path in ("core/source.go", "pb/generated.go", "server/source.go", "proto/api.proto",
                     "tests/fixture.json", "go.mod", "core/go.sum", "sdks/dart/pubspec.lock",
                     ".github/workflows/go.yml", "CONTRIBUTING.md", "admin/vite.config.ts"):
            with self.subTest(path=path):
                self.qualify()
                self.write(path, f"changed at {self.session.attempt}\n")
                self.commit()
                result = self.qualify()
                self.assertEqual(set(self.modes(result).values()), {"executed"})

    def test_unknown_path_and_classifier_failure_require_full_gate(self):
        self.qualify()
        self.write("new-unknown-input", "not classified\n")
        self.commit()
        result = self.qualify()
        self.assertEqual(result["carry_reason"], "unknown path")
        self.assertEqual(set(self.modes(result).values()), {"executed"})
        self.git("rm", "new-unknown-input")
        self.commit()
        self.qualify()
        with patch.object(gate, "known", side_effect=RuntimeError("classifier broken")):
            result = self.qualify()
        self.assertEqual(result["carry_reason"], "classifier failure")
        self.assertEqual(set(self.modes(result).values()), {"executed"})

    def test_missing_or_tampered_raw_log_and_json_require_full_gate(self):
        for kind in ("missing-log", "tampered-log", "self-reported-json"):
            with self.subTest(kind=kind):
                first = self.qualify()
                if kind == "missing-log":
                    Path(first["steps"][0]["log"]).unlink()
                elif kind == "tampered-log":
                    Path(first["steps"][0]["log"]).write_text("forged PASS\n")
                else:
                    self.session.report[0].write_text(json.dumps({"passed": True, "steps": []}))
                result = self.qualify()
                self.assertEqual(result["carry_reason"], "missing or altered live-session evidence")
                self.assertEqual(set(self.modes(result).values()), {"executed"})

    def test_restart_does_not_import_any_previous_receipt(self):
        self.qualify()
        self.session = self.new_session("new-evidence")
        result = self.qualify()
        self.assertEqual(set(self.modes(result).values()), {"executed"})
        with self.assertRaises(FileExistsError):
            gate.Session(self.root, self.base / "evidence")

    def test_shared_ignored_fixture_negative_control_and_committed_input_boundary(self):
        self.write(".gitignore", "tests/ignored-fixture\n")
        self.commit()
        self.write("tests/ignored-fixture", "valid\n")
        command = (sys.executable, "-c", "from pathlib import Path; import sys; "
                   "sys.exit(0 if Path('tests/ignored-fixture').read_text() == 'valid\\n' else 1)")
        self.session.plan = (Step("go-test-core", ".", command),) + self.session.plan[1:]
        self.assertEqual(subprocess.run(command, cwd=self.root, check=False).returncode, 0)
        # Removing both protections reproduces the old false success: an ignored
        # fixture was consumed in the mutable cwd but absent from the fingerprint.
        with patch.object(self.session, "fixed_inputs", self.mutable_inputs_control), \
                patch.object(self.session, "ignored_inputs", return_value=False):
            self.qualify()
            self.write("tests/ignored-fixture", "invalid\n")
            self.assertEqual(subprocess.run(command, cwd=self.root, check=False).returncode, 1)
            self.assertEqual(self.modes(self.qualify())["go-test-core"], "carried")
        with redirect_stdout(io.StringIO()):
            result = self.session.qualify()
        self.assertFalse(result["passed"])
        self.assertEqual(result["carry_reason"], "untracked ignored validation inputs")
        self.assertEqual(result["steps"][0]["mode"], "executed")
        self.assertFalse(self.session.receipts)
        # Required ignored fixtures are unsupported: they are never copied into
        # the committed input namespace or silently granted a reusable receipt.

    def test_ignored_guard_covers_shared_root_and_policy_inputs(self):
        self.write(".gitignore", "ignored-fixture\n")
        self.commit()
        for path in ("tests/ignored-fixture", "testdata/ignored-fixture", "testbed/ignored-fixture",
                     "proto/ignored-fixture", "ignored-fixture", ".github/ignored-fixture"):
            with self.subTest(path=path):
                self.qualify()
                self.write(path, "ignored input\n")
                result = self.qualify()
                self.assertEqual(result["carry_reason"], "untracked ignored validation inputs")
                self.assertEqual(set(self.modes(result).values()), {"executed"})
                (self.root / path).unlink()

    def test_source_ABA_negative_control_and_fixed_execution_inputs(self):
        command = (sys.executable, "-c", "from pathlib import Path; import sys; "
                   "sys.exit(0 if Path('core/source.go').read_text() == 'transient pass\\n' else 1)")
        plan = (Step("go-test-core", ".", command),) + self.session.plan[1:]
        self.session.plan = plan
        original = (self.root / "core/source.go").read_bytes()
        self.assertEqual(subprocess.run(command, cwd=self.root, check=False).returncode, 1)

        def changing_run(actual_run, step, log):
            if step.name != "go-test-core":
                return actual_run(step, log)
            self.write("core/source.go", "transient pass\n")
            try:
                return actual_run(step, log)
            finally:
                (self.root / "core/source.go").write_bytes(original)

        actual_run = self.session.run
        with patch.object(self.session, "fixed_inputs", self.mutable_inputs_control), \
                patch.object(self.session, "run", side_effect=lambda s, l: changing_run(actual_run, s, l)):
            self.assertTrue(self.qualify()["passed"])
        self.assertEqual(self.modes(self.qualify())["go-test-core"], "carried")
        self.assertEqual((self.root / "core/source.go").read_bytes(), original)

        # A real restart is required when the loaded runner changes. In the fixed
        # runner the same transient developer edit cannot alter the exported cwd.
        self.session = self.new_session("fixed-evidence")
        self.session.plan = plan
        actual_run = self.session.run
        with patch.object(self.session, "run", side_effect=lambda s, l: changing_run(actual_run, s, l)), \
                redirect_stdout(io.StringIO()):
            fixed = self.session.qualify()
        self.assertFalse(fixed["passed"])
        self.assertEqual(fixed["steps"][0]["exit"], 1)
        self.assertFalse(self.session.receipts)
        self.assertEqual((self.root / "core/source.go").read_bytes(), original)
        self.assertFalse(self.git("status", "--porcelain"))
        with redirect_stdout(io.StringIO()):
            retry = self.session.qualify()
        self.assertFalse(retry["passed"])
        self.assertEqual(retry["steps"][0]["mode"], "executed")

    def test_noneligible_evidence_negative_control_and_full_fallback(self):
        first = self.qualify()
        Path(first["steps"][1]["log"]).write_text("altered wire log\n")
        def eligible_only_control():
            return all(gate.digest(r.log.read_bytes()) == r.log_hash for r in self.session.receipts.values())
        with patch.object(self.session, "intact", side_effect=eligible_only_control):
            self.assertEqual(self.modes(self.qualify())["go-test-core"], "carried")
        for kind in ("missing", "tampered"):
            with self.subTest(kind=kind):
                first = self.qualify()
                log = Path(first["steps"][1]["log"])
                if kind == "missing":
                    log.unlink()
                else:
                    log.write_text("altered noneligible log\n")
                result = self.qualify()
                self.assertEqual(result["carry_reason"], "missing or altered live-session evidence")
                self.assertEqual(result["carried"], 0)
                self.assertEqual(result["executed"], len(self.session.plan))

    def test_export_excludes_build_outputs_and_is_independent_of_developer_cwd(self):
        self.write(".gitignore", "node_modules/\n.dart_tool/\n")
        self.commit()
        self.write("sdks/node/node_modules/ignored.go", "generated dependency artifact\n")
        self.write("testbed/dart-transport-probe/connect/.dart_tool/package_config.json", "cached config\n")
        head, source_tree, tree = self.session.snapshot()
        closure = gate.inputs(Step("go-test-core", ".", ("go", "test", "./...")), tree)
        with self.session.fixed_inputs(head, tree, closure) as exported:
            self.assertNotEqual(exported, self.root.resolve())
            self.assertEqual((exported / "core/source.go").read_bytes(), (self.root / "core/source.go").read_bytes())
            self.assertFalse((exported / "sdks/node/node_modules").exists())
            self.assertFalse((exported / "testbed/dart-transport-probe/connect/.dart_tool").exists())
            self.assertFalse((exported / "admin/app").exists())
            self.assertEqual((exported / "core/source.go").stat().st_mode & 0o222, 0)
            self.assertEqual(exported.stat().st_mode & 0o222, 0)
            self.write("core/source.go", "developer source changes\n")
            self.assertEqual((exported / "core/source.go").read_text(), "baseline\n")
        self.write("core/source.go", "baseline\n")
        self.assertFalse(self.session.ignored_inputs())

    def test_go_overlays_and_external_local_replacements_are_unsupported(self):
        exported = self.root.resolve()
        env = {}
        with patch.object(gate.subprocess, "check_output", return_value="-overlay=/developer/overlay.json"):
            with self.assertRaisesRegex(ValueError, "GOFLAGS"):
                self.session.check_go_inputs(exported, env)
        def go_metadata(command, **kwargs):
            if command == ["go", "env", "GOFLAGS"]:
                return ""
            return json.dumps({"Use": [{"DiskPath": "../external"}]})
        with patch.object(gate.subprocess, "check_output", side_effect=go_metadata):
            with self.assertRaisesRegex(ValueError, "external workspace"):
                self.session.check_go_inputs(exported, env)
        def external_replacement(command, **kwargs):
            if command == ["go", "env", "GOFLAGS"]:
                return ""
            return json.dumps({"Replace": [{"New": {"Path": "../external", "Version": ""}}]})
        with patch.object(gate.subprocess, "check_output", side_effect=external_replacement):
            with self.assertRaisesRegex(ValueError, "external local replacement"):
                self.session.check_go_inputs(exported, env)

    def test_hidden_dirty_inputs_and_changes_during_execution_refuse_qualification(self):
        self.git("update-index", "--assume-unchanged", "core/source.go")
        self.write("core/source.go", "hidden dirty\n")
        self.assertFalse(self.git("status", "--porcelain"))
        with redirect_stdout(io.StringIO()):
            result = self.session.qualify()
        self.assertFalse(result["passed"])
        self.assertIn("differs from committed", result["error"])
        self.git("update-index", "--no-assume-unchanged", "core/source.go")
        self.commit()
        actual_run = self.session.run
        def changing_run(step, log):
            result = actual_run(step, log)
            if step.name == "go-test-core":
                self.write("core/source.go", "changed during run\n")
            return result
        with patch.object(self.session, "run", side_effect=changing_run), redirect_stdout(io.StringIO()):
            result = self.session.qualify()
        self.assertFalse(result["passed"])
        self.assertFalse(self.session.receipts)

    def test_context_and_closure_failure_cannot_retain_a_success(self):
        with patch.object(self.session, "context", side_effect=["before", "after"]), redirect_stdout(io.StringIO()):
            result = self.session.qualify()
        self.assertFalse(result["passed"])
        self.assertFalse(self.session.receipts)
        self.qualify()
        with patch.object(gate, "inputs", side_effect=RuntimeError("closure unavailable")):
            result = self.qualify()
        self.assertEqual(result["carry_reason"], "classifier failure")
        self.assertEqual(set(self.modes(result).values()), {"executed"})

    def test_context_change_main_branch_and_rewritten_history_disable_carry(self):
        self.qualify()
        self.session.context = lambda: "different-toolchain"
        result = self.qualify()
        self.assertEqual(result["carry_reason"], "toolchain or execution environment changed")
        self.git("switch", "-c", "main")
        result = self.qualify()
        self.assertEqual(set(self.modes(result).values()), {"executed"})
        self.git("switch", "feature")
        self.qualify()
        self.write("admin/app/view.tsx", "amended\n")
        self.git("add", "--all")
        self.git("commit", "--amend", "-qm", "rewrite fixture")
        result = self.qualify()
        self.assertEqual(set(self.modes(result).values()), {"executed"})

    def test_ignored_inputs_dirty_source_runner_changes_and_failure_cannot_qualify(self):
        self.write(".gitignore", "ignored-fixture\n")
        self.commit()
        self.qualify()
        self.write("core/ignored-fixture", "untracked input\n")
        result = self.qualify()
        self.assertEqual(result["carry_reason"], "untracked ignored validation inputs")
        self.assertEqual(set(self.modes(result).values()), {"executed"})
        self.write("core/source.go", "dirty source\n")
        with redirect_stdout(io.StringIO()):
            result = self.session.qualify()
        self.assertFalse(result["passed"])
        self.assertFalse(self.session.receipts)
        self.commit()
        self.session.plan = (Step("go-test-core", ".", (sys.executable, "-c", "raise SystemExit(1)")),)
        with redirect_stdout(io.StringIO()):
            result = self.session.qualify()
        self.assertFalse(result["passed"])
        self.assertEqual(result["steps"][0]["exit"], 1)
        self.assertFalse(self.session.receipts)
        self.session.runner_hashes[next(iter(self.session.runner_hashes))] = "tampered"
        with redirect_stdout(io.StringIO()):
            result = self.session.qualify()
        self.assertFalse(result["passed"])
        self.assertIn("restart", result["error"])

    def test_docs_discovery_pattern_runs_from_repo_root_with_nested_test_files(self):
        # Reproduce step 74's layout: no root test_*.py; five discovery files
        # under .github/scripts. These are real unittest subprocesses, not a
        # mock success or imported gate receipt.
        names = ("test_ci_docs.py", "test_evidence_only_pr.py", "test_go_modules.py",
                 "test_local_gate_session.py", "test_verify_go_sdk_release.py")
        for name in names:
            self.write(".github/scripts/" + name,
                       "import unittest\nfrom pathlib import Path\n"
                       "class Discovery(unittest.TestCase):\n"
                       "    def test_root(self):\n"
                       "        self.assertTrue(Path('.github/scripts').is_dir())\n"
                       "        self.assertFalse(list(Path('.').glob('test_*.py')))\n")
        step = next(s for s in steps(self.session.evidence) if s.name == "docs-tests")
        self.assertEqual(step.cwd, ".")
        self.assertEqual(step.command, ("python3", "-B", "-m", "unittest", "discover",
                                      "-s", ".github/scripts", "-p", "test_*.py"))
        # Negative control: the original expansion fails before unittest starts.
        with self.assertRaisesRegex(ValueError, "empty command glob"):
            for arg in step.command:
                if "*" in arg and not list((self.root / step.cwd).glob(arg)):
                    raise ValueError("empty command glob: " + arg)
        self.session.env["PATH"] = str(Path(sys.executable).parent) + os.pathsep + self.session.env["PATH"]
        self.commit()
        self.session.plan = steps(self.session.evidence)
        actual_run = self.session.run
        synthetic_prefix = []
        def run(step, log):
            if step.name in {"docs-tests", "final-diff", "final-clean"}:
                return actual_run(step, log)
            synthetic_prefix.append(step.name)
            log.write_text("Synthetic test prefix; no production qualification\n")
            return 0, True
        # 73 mock prefix commands followed by real subprocesses for 74–76.
        # The temporary fixture report is only a regression test artifact.
        with patch.object(self.session, "run", side_effect=run):
            result = self.qualify()
        self.assertEqual(len(synthetic_prefix), 73)
        self.assertEqual((result["executed"], result["carried"]), (76, 0))
        self.assertEqual([r["name"] for r in result["steps"][-3:]],
                         ["docs-tests", "final-diff", "final-clean"])
        log = Path(result["steps"][-3]["log"])
        self.assertIn("Ran 5 tests", log.read_text())
        self.assertIn("OK", log.read_text())

    def test_only_declared_pathname_glob_expands_and_preserves_space_argument(self):
        self.write("sdks/dart/lib/src/z.dart", "")
        self.write("sdks/dart/lib/src/a space.dart", "")
        self.write("sdks/dart/lib/src/ignored.txt", "")
        executable = self.base / "bin/dart"
        executable.parent.mkdir()
        executable.write_text("#!" + sys.executable + "\nimport json, sys\nprint(json.dumps(sys.argv[1:]))\n")
        executable.chmod(0o755)
        self.session.env["PATH"] = str(executable.parent) + os.pathsep + self.session.env["PATH"]
        step = next(s for s in steps(self.session.evidence) if s.name == "dart-format")
        log = self.session.evidence / "real-pathname-glob.log"
        self.assertEqual(self.session.run(step, log), (0, True))
        self.assertEqual(json.loads(log.read_text()),
                         ["format", "--output=none", "--set-exit-if-changed", "lib/lantern_client.dart",
                          "lib/src/a space.dart", "lib/src/z.dart", "test"])
        (self.root / "sdks/dart/lib/src/z.dart").unlink()
        (self.root / "sdks/dart/lib/src/a space.dart").unlink()
        with patch.object(gate.subprocess, "run") as execute:
            with self.assertRaisesRegex(ValueError, "empty command glob"):
                self.session.run(step, self.session.evidence / "empty-pathname-glob.log")
            execute.assert_not_called()

    def test_literal_patterns_quotes_and_shell_syntax_remain_single_argv_values(self):
        self.write("test_decoy.py", "must not replace the literal pattern")
        values = ("*.py", "a space", "'quotes'", "$HOME", "$(touch sentinel)", "; touch sentinel")
        command = (sys.executable, "-c", "import json, sys; print(json.dumps(sys.argv[1:]))") + values
        log = self.session.evidence / "real-literal-argv.log"
        self.assertEqual(self.session.run(Step("literal-fixture", ".", command), log), (0, True))
        self.assertEqual(json.loads(log.read_text()), list(values))
        self.assertFalse((self.root / "sentinel").exists())


class PlanTest(unittest.TestCase):
    def test_glob_metadata_marks_only_the_formatter_pathname_and_refuses_invalid_indexes(self):
        plan = steps(Path("/tmp/fixture-evidence"))
        marked = [(s.name, i, s.command[i]) for s in plan for i in s.glob_args]
        self.assertEqual(marked, [("dart-format", 5, "lib/src/*.dart")])
        docs = next(s for s in plan if s.name == "docs-tests")
        self.assertEqual(docs.glob_args, ())
        for indexes in ((0,), (-1,), (2,), (1, 1), (True,)):
            with self.subTest(indexes=indexes), self.assertRaises(ValueError):
                Step("invalid", ".", ("fixture", "*.py"), glob_args=indexes)

    def test_all_76_executor_calls_preserve_declared_argv_cwd_and_environment(self):
        with tempfile.TemporaryDirectory(prefix="gate argv with spaces ") as directory:
            root = Path(directory) / "repo with spaces"
            root.mkdir()
            session = gate.Session(root, Path(directory) / "evidence with spaces")
            root = session.root
            session.env = {"PATH": "/fixture tool path", "RUSTDOCFLAGS": "-D warnings",
                           "LITERAL": "$HOME;*.py", "GOWORK": "/developer/workspace"}
            session.eligible_root = Path(directory) / "committed export"
            for rel in ("lib/src/z.dart", "lib/src/a space.dart"):
                path = root / "sdks/dart" / rel
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("")
            captured = []
            def execute(command, **kwargs):
                captured.append((command, kwargs))
                return subprocess.CompletedProcess(command, 0)
            with patch.object(gate.subprocess, "run", side_effect=execute), \
                    patch.object(session, "check_go_inputs") as check_inputs:
                for index, step in enumerate(session.plan):
                    self.assertEqual(session.run(step, session.evidence / f"mock-{index}.log"), (0, True))
            self.assertEqual(len(captured), 76)
            self.assertEqual(check_inputs.call_count, 6)
            for step, (command, kwargs) in zip(session.plan, captured):
                with self.subTest(step=step.name):
                    expected = list(step.command)
                    if step.name == "dart-format":
                        expected[5:6] = ["lib/src/a space.dart", "lib/src/z.dart"]
                    self.assertEqual(command, expected)
                    execution_root = session.eligible_root if step.name in gate.ELIGIBLE else root
                    self.assertEqual(kwargs["cwd"], execution_root / step.cwd)
                    environment = session.env.copy()
                    if step.name in gate.ELIGIBLE:
                        environment.update(GOWORK=str(execution_root / "go.work"), PWD=str(execution_root / step.cwd))
                    if step.name == "admin-playwright":
                        environment.update(LANTERN_E2E_PREVIEW_PORT="44469", LANTERN_PORT="64469", LANTERN_E2E_METRICS_PORT="59469")
                    if step.name == "rust-real-wire":
                        environment.update(LANTERN_RUST_TEST_SERVER=str(root / "sdks/rust/target/lantern-smoke"),
                                           LANTERN_RUST_TEST_AUTH_FIXTURE=str(root / "sdks/rust/target/lantern-authfixture"))
                    self.assertEqual(kwargs["env"], environment)
                    self.assertNotIn("shell", kwargs)
                    self.assertEqual(kwargs["stderr"], subprocess.STDOUT)
                    self.assertFalse(kwargs["check"])

    def test_complete_plan_keeps_install_order_generation_and_unsupported_qualification(self):
        plan = steps(Path("/tmp/fixture-evidence"))
        self.assertEqual(len(plan), 76)
        self.assertEqual(len({s.name for s in plan}), 76)
        self.assertEqual([s.name for s in plan[:5]], ["node-frozen-install", "node-prebuild",
                         "admin-frozen-install", "example-frozen-preflight", "gofmt"])
        names = {s.name for s in plan}
        self.assertTrue(gate.ELIGIBLE <= names)
        for name in ("generated-drift", "rust-real-wire", "rust-oidc-scoped-wire", "sqlite-performance-probe",
                     "admin-playwright", "final-clean", "go-test-."):
            self.assertIn(name, names)
            self.assertNotIn(name, gate.ELIGIBLE)
        self.assertEqual([s.name for s in plan[-2:]], ["final-diff", "final-clean"])

    def test_sdk_wire_closure_covers_backend_proto_shared_fixture_and_generated_consumers(self):
        tree = {p: "hash" for p in ("core/source.go", "server/source.go", "pb/generated.go", "proto/api.proto",
                                  "tests/fixture.json", "testbed/fixture", "sdks/rust/src/generated.rs",
                                  "sdks/node/src/gen/service.ts", "sdks/dart/lib/src/gen/service.dart")}
        for name in ("rust-real-wire", "rust-oidc-scoped-wire", "node-test", "dart-test", "go-test-."):
            self.assertEqual(gate.inputs(Step(name, ".", ("fixture",)), tree), tree)
            self.assertNotIn(name, gate.ELIGIBLE)


class GoSettingsTest(unittest.TestCase):
    def settings(self, suffix="123", kind="ffile"):
        return {"GOGCCFLAGS": f"-fPIC -{kind}-prefix-map=/tmp/compiler/go-build{suffix}=/tmp/go-build -gno-record-gcc-switches",
                "CGO_CFLAGS": "-O2 -g", "GOFLAGS": "", "GOTMPDIR": "/tmp/compiler"}

    def test_only_generated_work_suffix_is_stable_for_both_supported_map_kinds(self):
        for kind in ("ffile", "fdebug"):
            with self.subTest(kind=kind):
                before, after = self.settings("123", kind), self.settings("987654", kind)
                self.assertNotEqual(gate.digest(gate.encoded(before)), gate.digest(gate.encoded(after)))
                self.assertEqual(gate.stable_go_settings(before), gate.stable_go_settings(after))
                self.assertIn("go-build123", before["GOGCCFLAGS"])
                spaced = dict(before, GOGCCFLAGS=before["GOGCCFLAGS"].replace(
                    "/tmp/compiler/go-build123=/tmp/go-build", "'/tmp/compiler space/go-build123=/tmp/go-build'"))
                spaced_after = dict(spaced, GOGCCFLAGS=spaced["GOGCCFLAGS"].replace("go-build123=", "go-build987="))
                self.assertEqual(gate.stable_go_settings(spaced), gate.stable_go_settings(spaced_after))

    def test_compiler_flag_map_base_destination_and_user_settings_still_change_fingerprint(self):
        baseline = self.settings()
        for before, after in (("-fPIC", "-fPIE"), ("-ffile-", "-fdebug-"),
                              ("/tmp/compiler/", "/tmp/other/"),
                              ("=/tmp/go-build", "=/tmp/other")):
            with self.subTest(change=after):
                changed = dict(baseline, GOGCCFLAGS=baseline["GOGCCFLAGS"].replace(before, after))
                self.assertNotEqual(gate.stable_go_settings(baseline), gate.stable_go_settings(changed))
        for key, value in (("CGO_CFLAGS", "-O0 -g"), ("GOFLAGS", "-race"), ("GOTMPDIR", "/tmp/other")):
            with self.subTest(key=key):
                self.assertNotEqual(gate.stable_go_settings(baseline),
                                    gate.stable_go_settings(dict(baseline, **{key: value})))
        unrecognized = dict(baseline, GOGCCFLAGS="-ffile-prefix-map=/tmp/not-go-build123=/tmp/go-build")
        self.assertNotEqual(gate.stable_go_settings(unrecognized), gate.stable_go_settings(
            dict(unrecognized, GOGCCFLAGS=unrecognized["GOGCCFLAGS"].replace("123", "456"))))

    def test_malformed_or_ambiguous_compiler_settings_fail_closed(self):
        with self.assertRaises(ValueError):
            gate.stable_go_settings(dict(self.settings(), GOGCCFLAGS="'unterminated"))
        with self.assertRaises(ValueError):
            gate.stable_go_settings(dict(self.settings(), GOGCCFLAGS=
                "-ffile-prefix-map=/tmp/go-build123=/tmp/go-build -fdebug-prefix-map=/tmp/go-build456=/tmp/go-build"))
        with self.assertRaises(KeyError):
            gate.stable_go_settings({})

    def test_production_context_stays_stable_but_preserves_semantic_and_toolchain_changes(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            root = base / "repo"
            root.mkdir()
            session = gate.Session(root, base / "evidence")
            goroot = base / "toolchain"
            for name in ("src/source.go", "pkg/tool/compile", "bin/go"):
                path = goroot / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("fixed toolchain input")
            git = base / "git"
            git.write_text("fixed Git input")
            settings = dict(self.settings(), GOROOT=str(goroot), GOWORK=str(session.root / "go.work"),
                            CC="fixture-cc", CXX="fixture-cxx", CGO_ENABLED="0")
            def check_output(command, **kwargs):
                if command[-1] == "version":
                    return b"go version fixture\n"
                return json.dumps(settings).encode()
            def which(name, **kwargs):
                return {"go": str(goroot / "bin/go"), "git": str(git)}.get(name)
            with patch.object(gate.shutil, "which", side_effect=which), \
                    patch.object(gate.subprocess, "check_output", side_effect=check_output), \
                    patch.object(gate.platform, "platform", return_value="fixture-platform"), \
                    patch.object(gate.subprocess, "run"):
                first = session.context()
                settings["GOGCCFLAGS"] = settings["GOGCCFLAGS"].replace("go-build123=", "go-build456=")
                self.assertEqual(first, session.context())
                settings["CGO_CFLAGS"] = "-O0 -g"
                self.assertNotEqual(first, session.context())
                settings["CGO_CFLAGS"] = "-O2 -g"
                self.assertEqual(first, session.context())
                (goroot / "src/source.go").write_text("changed producing toolchain input")
                self.assertNotEqual(first, session.context())


if __name__ == "__main__":
    unittest.main()
