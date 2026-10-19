"""Keep disposable source setup separate from runtime time qualification."""

import contextlib
import importlib.util
import io
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch


SPEC = importlib.util.spec_from_file_location("configured_time_input", Path(__file__).with_name("configured_time_input.py"))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class ConfiguredTimeInputTest(unittest.TestCase):
    def test_authenticated_host_and_container_jobs_share_the_declared_preparation(self):
        root = Path(__file__).resolve().parents[2]
        for name, expected in (("node-sdk", 1), ("dart-sdk", 3), ("rust-sdk", 1), ("rust-release", 1), ("current-sdk4", 1)):
            with self.subTest(workflow=name):
                source = (root / f".github/workflows/{name}.yml").read_text()
                self.assertEqual(source.count("run: python3 -B .github/scripts/configured_time_input.py"), expected)
                self.assertEqual(source.count("LANTERN_CI_DISPOSABLE_SOURCE_INPUT: ${{ runner.environment }}"), expected + (name == "rust-sdk"))
                self.assertNotIn("install -m 0444", source)

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.source = Path(self.directory.name) / "ntp.conf"
        self.enterContext(patch.object(MODULE, "SOURCE_INPUT", self.source))
        self.enterContext(patch.dict(os.environ, {
            "GITHUB_ACTIONS": "true", "LANTERN_CI_DISPOSABLE_SOURCE_INPUT": "github-hosted",
            "RUNNER_TEMP": self.directory.name,
        }, clear=True))
        self.enterContext(patch.object(MODULE.platform, "system", return_value="Linux"))

    def test_local_self_hosted_unsupported_and_relative_temp_refuse_without_install(self):
        for environment in ({"GITHUB_ACTIONS": "false"},
                            {"LANTERN_CI_DISPOSABLE_SOURCE_INPUT": "self-hosted"},
                            {"RUNNER_TEMP": "relative"}):
            with self.subTest(environment=environment), patch.dict(os.environ, environment), patch.object(MODULE.subprocess, "run") as install:
                with self.assertRaises(ValueError):
                    MODULE.prepare()
                install.assert_not_called()
        with patch.object(MODULE.platform, "system", return_value="Windows"), patch.object(MODULE.subprocess, "run") as install:
            with self.assertRaises(ValueError):
                MODULE.prepare()
            install.assert_not_called()

    def test_missing_input_installs_only_fixed_bytes_and_checks_read_only_result(self):
        def install(command, **kwargs):
            self.assertEqual(command[:5], ["sudo", "-n", "install", "-m", "0444"])
            self.assertEqual(Path(command[-2]).read_bytes(), MODULE.SOURCE_BYTES)
            self.assertEqual(command[-1], str(self.source))
            self.source.write_bytes(Path(command[-2]).read_bytes())
            self.source.chmod(0o444)
        original = MODULE.inspect_input
        # This unprivileged test cannot chown; emulate only the final stat check.
        def inspect():
            result = original()
            return "ready" if result == "matching_content" else result
        with patch.object(MODULE.os, "geteuid", return_value=501), patch.object(MODULE.subprocess, "run", side_effect=install) as run, patch.object(MODULE, "inspect_input", side_effect=inspect), contextlib.redirect_stdout(io.StringIO()) as output:
            MODULE.prepare()
            self.assertEqual(run.call_count, 1)
            self.assertEqual(output.getvalue(), "configured_source_input_before=missing\nconfigured_source_input_ready=true\n")
        self.assertEqual(list(Path(self.directory.name).iterdir()), [self.source])

    def test_ready_input_is_preserved_and_symlink_refuses(self):
        with patch.object(MODULE, "inspect_input", return_value="ready"), patch.object(MODULE.subprocess, "run") as install, contextlib.redirect_stdout(io.StringIO()):
            MODULE.prepare()
            install.assert_not_called()
        target = Path(self.directory.name) / "target"
        target.write_bytes(b"unchanged")
        self.source.symlink_to(target)
        with patch.object(MODULE.subprocess, "run") as install:
            with self.assertRaises(ValueError):
                MODULE.prepare()
            install.assert_not_called()
        self.assertEqual(target.read_bytes(), b"unchanged")

    def test_bounded_content_and_failed_postcondition_do_not_claim_success(self):
        self.source.write_bytes(b"x" * 8192)
        self.assertEqual(MODULE.inspect_input(), "different")
        with patch.object(MODULE.subprocess, "run"), contextlib.redirect_stdout(io.StringIO()) as output:
            with self.assertRaises(ValueError):
                MODULE.prepare()
        self.assertNotIn("ready=true", output.getvalue())

    def test_public_failure_never_copies_child_private_error_or_path(self):
        error = subprocess.CalledProcessError(1, ["private-path"], stderr=b"private-token")
        with patch.object(MODULE, "prepare", side_effect=error), patch.object(MODULE.sys, "argv", ["helper"]), contextlib.redirect_stderr(io.StringIO()) as output:
            self.assertEqual(MODULE.main(), 1)
        self.assertEqual(output.getvalue(), "configured_source_input_preparation_failed\n")


if __name__ == "__main__":
    unittest.main()
