import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import local_gate_preflight as preflight
from local_gate_plan import Step


ROOT = Path(__file__).resolve().parents[2]


class PreflightTest(unittest.TestCase):
    def test_pin_readers_use_current_authoritative_files(self):
        go = preflight.exact_pin(ROOT, "go.mod", r"^go (\d+\.\d+\.\d+)$")
        self.assertIn("go " + go, (ROOT / "go.work").read_text())
        lint = preflight.exact_pin(ROOT, ".github/workflows/go.yml", r"^\s+version: v(2\.\d+\.\d+)\s*$")
        self.assertRegex(lint, r"^2\.\d+\.\d+$")
        bun = preflight.exact_pin(ROOT, ".github/workflows/node-sdk.yml", r"^\s+bun-version: (\d+\.\d+\.\d+)\s*$")
        self.assertIn('bun-version: ' + bun, (ROOT / ".github/workflows/current-sdk4.yml").read_text())

    def test_missing_ambiguous_and_changed_pin(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for text, expected in [("go 9.8.7\n", "9.8.7"), ("", None),
                                   ("go 9.8.7\ngo 9.8.6\n", None)]:
                (root / "go.mod").write_text(text)
                if expected:
                    self.assertEqual(preflight.exact_pin(root, "go.mod", r"^go (\S+)$"), expected)
                else:
                    with self.assertRaises(ValueError):
                        preflight.exact_pin(root, "go.mod", r"^go (\S+)$")

    def test_inspection_is_local_bounded_and_does_not_mutate_environment(self):
        environment = {"GOTOOLCHAIN": "auto", "RUSTUP_AUTO_INSTALL": "1"}
        with patch.object(subprocess, "run") as run:
            run.return_value.stdout = "go version"
            preflight.command(["go", "version"], ROOT, environment)
            options = run.call_args.kwargs
            self.assertEqual(options["env"]["GOTOOLCHAIN"], "local")
            self.assertEqual(options["env"]["RUSTUP_AUTO_INSTALL"], "0")
            self.assertEqual(options["timeout"], 10)
        self.assertEqual(environment, {"GOTOOLCHAIN": "auto", "RUSTUP_AUTO_INSTALL": "1"})

    def test_wrong_child_go_fails_before_other_tools(self):
        with patch.object(preflight, "scratch_environment", return_value={}), \
                patch.object(preflight, "executable", return_value="/selected/go"), \
                patch.object(preflight, "command", return_value="go version go0.0.1 linux/amd64") as command:
            with self.assertRaisesRegex(ValueError, "local child.*expected=.*actual=0.0.1"):
                preflight.check(ROOT, {}, [Step("go-lint", ".", ("make", "lint"))])
            self.assertEqual(command.call_count, 1)

    def test_correct_go_and_linter_continue_and_wrong_linter_refuses(self):
        pin = preflight.exact_pin(ROOT, "go.mod", r"^go (\S+)$")
        lint = preflight.exact_pin(ROOT, ".github/workflows/go.yml", r"^\s+version: v(2\.\d+\.\d+)\s*$")
        for version in [lint, "0.0.0"]:
            with self.subTest(version=version), \
                    patch.object(preflight, "scratch_environment", return_value={}), \
                    patch.object(preflight, "executable", side_effect=lambda name, _: "/selected/" + name), \
                    patch.object(preflight, "command", side_effect=["go version go" + pin + " linux/amd64",
                        json.dumps({"GOVERSION": "go" + pin, "GOROOT": "/go"}),
                        "golangci-lint has version " + version]):
                if version == lint:
                    result = preflight.check(ROOT, {}, [Step("go-lint", ".", ("make", "lint"))])
                    self.assertEqual(len(result["tools"]), 2)
                else:
                    with self.assertRaisesRegex(ValueError, "golangci-lint.*expected=.*actual=0.0.0"):
                        preflight.check(ROOT, {}, [Step("go-lint", ".", ("make", "lint"))])

    def test_passive_scratch_noexec_full_and_missing(self):
        with tempfile.TemporaryDirectory() as directory:
            for flags, blocks in [(8, 10), (0, 0)]:
                with self.subTest(flags=flags), patch.object(preflight.os, "ST_NOEXEC", 8, create=True), \
                        patch.object(preflight.os, "statvfs") as stat:
                    stat.return_value.f_flag = flags
                    stat.return_value.f_bavail = blocks
                    with self.assertRaises(ValueError):
                        preflight.scratch_environment({"GOTMPDIR": directory})
            with self.assertRaisesRegex(ValueError, "unavailable"):
                preflight.scratch_environment({"GOTMPDIR": directory + "/absent"})

    def test_bun_mismatch_and_missing_helper_fail_early(self):
        with patch.object(preflight, "scratch_environment", return_value={}), \
                patch.object(preflight, "executable", return_value="/selected/bun"), \
                patch.object(preflight, "command", return_value="0.0.0"):
            with self.assertRaisesRegex(ValueError, "bun.*expected=.*actual=0.0.0"):
                preflight.check(ROOT, {}, [Step("node-prebuild", ".", ("bun", "run", "build"))])
        with patch.object(preflight.shutil, "which", return_value=None):
            with self.assertRaisesRegex(ValueError, "cargo-audit not found"):
                preflight.executable("cargo-audit", {})

    def test_inspection_timeout_and_nonzero_are_refusals(self):
        for error in [subprocess.TimeoutExpired("go", 10), subprocess.CalledProcessError(1, "go")]:
            with self.subTest(error=type(error)), patch.object(subprocess, "run", side_effect=error):
                with self.assertRaisesRegex(ValueError, "inspection failed"):
                    preflight.command(["go", "version"], ROOT, {})

    def test_parent_selection_and_manifest_toolchain_drift_refuse(self):
        pin = preflight.exact_pin(ROOT, "go.mod", r"^go (\S+)$")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for toolchain, selection in [("go0.0.1", "local"), ("default", "go0.0.1+auto")]:
                with self.subTest(toolchain=toolchain, selection=selection):
                    (root / "go.mod").write_text("go " + pin + "\ntoolchain " + toolchain + "\n")
                    (root / "go.work").write_text("go " + pin + "\n")
                    with patch.object(preflight, "scratch_environment", return_value={}), \
                            patch.object(preflight, "executable", return_value="/selected/go"), \
                            patch.object(preflight, "command", side_effect=["go version go" + pin + " linux/amd64",
                                json.dumps({"GOVERSION": "go" + pin, "GOROOT": "/go"})]):
                        with self.assertRaisesRegex(ValueError, "differs from source pin"):
                            preflight.check(root, {"GOTOOLCHAIN": selection}, [Step("go-build", ".", ("go", "build"))])

    def test_remaining_pin_readers_fail_on_drift_not_a_second_version_table(self):
        readers = [
            (".github/workflows/current-sdk4.yml", r"^\s+toolchain: '(\d+\.\d+\.\d+)'\s*$"),
            (".github/workflows/current-sdk4.yml", r"^\s+sdk: (\d+\.\d+\.\d+)\s*$"),
            (".github/workflows/dart-sdk.yml", r"^\s+flutter-version: (\d+\.\d+\.\d+)\s*$"),
            (".github/workflows/rust-release.yml", r"^\s+cargo install --locked --version (\d+\.\d+\.\d+) cargo-audit\s*$"),
        ]
        for file, pattern in readers:
            with self.subTest(file=file):
                self.assertRegex(preflight.exact_pin(ROOT, file, pattern), r"^\d+\.\d+\.\d+$")

    def test_buf_selected_helper_mismatch(self):
        pin = preflight.exact_pin(ROOT, "go.mod", r"^go (\S+)$")
        with patch.object(preflight, "scratch_environment", return_value={}), \
                patch.object(preflight, "executable", side_effect=lambda name, _: "/selected/" + name), \
                patch.object(preflight, "command", side_effect=["go version go" + pin + " linux/amd64",
                    json.dumps({"GOVERSION": "go" + pin, "GOROOT": "/go"}), "9.9.9"]):
            with self.assertRaisesRegex(ValueError, "buf.*expected=.*actual=9.9.9"):
                preflight.check(ROOT, {}, [Step("node-generate", ".", ("bash", "codegen.sh"))])

    def test_native_rust_wrong_version_refuses_without_build(self):
        pin = preflight.exact_pin(ROOT, "go.mod", r"^go (\S+)$")
        with patch.object(preflight, "scratch_environment", return_value={}), \
                patch.object(preflight, "executable", side_effect=lambda name, _: "/selected/" + name), \
                patch.object(preflight, "command", side_effect=["go version go" + pin + " linux/amd64",
                    json.dumps({"GOVERSION": "go" + pin, "GOROOT": "/go"}), "cargo 0.0.1"] ) as command:
            with self.assertRaisesRegex(ValueError, "cargo.*expected=.*actual=0.0.1"):
                preflight.check(ROOT, {}, [Step("rust-test", ".", ("cargo", "test")),
                                          Step("current-functional-wire", ".", ("python3", "gate.py"))])
            self.assertEqual(command.call_count, 3)

    def test_dart_and_flutter_mismatches_never_invoke_bootstrap(self):
        dart_pin = preflight.exact_pin(ROOT, ".github/workflows/current-sdk4.yml", r"^\s+sdk: (\d+\.\d+\.\d+)\s*$")
        flutter_pin = preflight.exact_pin(ROOT, ".github/workflows/dart-sdk.yml", r"^\s+flutter-version: (\d+\.\d+\.\d+)\s*$")
        with tempfile.TemporaryDirectory() as directory:
            binpath = Path(directory) / "bin"
            native = binpath / "cache/dart-sdk/bin/dart"
            native.parent.mkdir(parents=True)
            native.write_bytes(b"\x7fELFfixture")
            for name in ["dart", "flutter"]:
                (binpath / name).write_text("#!/bin/sh\nexit 99\n")
            metadata = binpath / "cache/flutter.version.json"
            for dart_version, framework, revision in [("0.0.1", flutter_pin, "same"),
                                                       (dart_pin, "0.0.1", "same"),
                                                       (dart_pin, flutter_pin, "different")]:
                with self.subTest(dart=dart_version, flutter=framework, revision=revision):
                    metadata.write_text(json.dumps({"frameworkVersion": framework, "frameworkRevision": "same"}))
                    with patch.object(preflight, "scratch_environment", return_value={}), \
                            patch.object(preflight, "executable", side_effect=lambda name, _: str(binpath / name)), \
                            patch.object(preflight, "command", side_effect=["Dart SDK version: " + dart_version, revision]) as command:
                        with self.assertRaisesRegex(ValueError, "expected=.*actual="):
                            preflight.check(ROOT, {}, [Step("example-test", ".", ("flutter", "test"))])
                        for call in command.call_args_list:
                            self.assertNotIn(call.args[0][0], [str(binpath / "dart"), str(binpath / "flutter")])

    def test_unknown_dart_launcher_fails_without_execution(self):
        with tempfile.TemporaryDirectory() as directory:
            launcher = Path(directory) / "dart"
            launcher.write_text("#!/bin/sh\nexit 99\n")
            with patch.object(preflight, "scratch_environment", return_value={}), \
                    patch.object(preflight, "executable", return_value=str(launcher)), \
                    patch.object(preflight, "command") as command:
                with self.assertRaisesRegex(ValueError, "not an installed native SDK"):
                    preflight.check(ROOT, {}, [Step("dart-test", ".", ("dart", "test"))])
                command.assert_not_called()
