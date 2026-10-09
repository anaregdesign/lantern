"""Synthetic gate contracts; these fixtures never qualify native SDK execution."""

import copy
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import current_sdk4_gate as gate
from local_gate_plan import steps


ROOT = Path(__file__).resolve().parents[2]


def events():
    result = [{"Action": "run", "Test": gate.ROOT_TEST},
              {"Action": "output", "Test": gate.ROOT_TEST, "Output": gate.NATIVE_MARKER}]
    for name in gate.SDK_TESTS:
        result += [{"Action": "run", "Test": name}, {"Action": "pass", "Test": name}]
    result += [{"Action": "pass", "Test": gate.ROOT_TEST}, {"Action": "pass"}]
    return [{"Package": gate.PACKAGE, **event} for event in result]


def workflow_job(name, job):
    text = (ROOT / f".github/workflows/{name}.yml").read_text()
    return re.search(rf"(?ms)^  {job}:\n(.*?)(?=^  [\w-]+:|\Z)", text).group(1)


class SDK4ReceiptTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / "source"
        self.evidence = Path(self.temp.name) / "evidence"
        self.root.mkdir()
        self.evidence.mkdir()
        script = self.root / ".github/scripts/current_sdk4_gate.py"
        script.parent.mkdir(parents=True)
        script.write_bytes(Path(gate.__file__).read_bytes())
        (self.root / "go.work").write_text("go 1.27.2\n")
        self.git("init", "-q")
        self.git("config", "user.name", "Fixture")
        self.git("config", "user.email", "fixture@example.test")
        self.git("add", ".")
        self.git("commit", "-qm", "fixture")
        self.source = gate.snapshot(self.root)
        self.runtime = {"environment": gate.ENVIRONMENT.copy(), "source_config_sha256": "a" * 64,
                        "tools": {name: {"version": "synthetic", "selected_sha256": "b" * 64}
                                  for name in ("go", "bun", "dart", "cargo", "rustc")}}
        self.write_events(events())
        (self.evidence / "stderr.log").write_bytes(b"")
        self.receipt = {"schema": 1, "status": "PASS", "exit": 0, "source": self.source,
                        "runner_sha256": gate.sha(script.read_bytes()), "runtime": self.runtime,
                        "include_scoped": False, "command": gate.command(False),
                        "executed_tests": [gate.ROOT_TEST, *gate.SDK_TESTS]}
        self.save()

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.root, stderr=subprocess.STDOUT)

    def write_events(self, records):
        (self.evidence / "events.jsonl").write_text("".join(json.dumps(record) + "\n" for record in records))

    def save(self):
        self.receipt["logs"] = {name: gate.sha((self.evidence / name).read_bytes())
                                for name in ("events.jsonl", "stderr.log")}
        data = gate.encoded(self.receipt)
        (self.evidence / "receipt.json").write_bytes(data)
        self.trusted_digest = gate.sha(data)

    def verify(self):
        return gate.verify(self.root, self.evidence, self.source["head"], self.trusted_digest)

    def test_complete_execution_and_exact_source(self):
        self.assertEqual(self.verify()["executed_tests"], [gate.ROOT_TEST, *gate.SDK_TESTS])

    def test_container_checkout_trust_is_exact_and_job_local(self):
        # Model the host-owned bind mount; never change the developer's Git config.
        env = os.environ | {"GIT_TEST_ASSUME_DIFFERENT_OWNER": "1",
                            "GIT_CONFIG_NOSYSTEM": "1",
                            "GIT_CONFIG_GLOBAL": str(Path(self.temp.name) / "job.gitconfig")}
        def run(*args):
            return subprocess.run(["git", *args], cwd=self.root, env=env,
                                  capture_output=True, text=True)
        self.assertNotEqual(run("status", "--porcelain").returncode, 0)
        self.assertEqual(run("config", "--global", "--add", "safe.directory", str(self.root.resolve())).returncode, 0)
        self.assertEqual(run("status", "--porcelain").returncode, 0)
        other = Path(self.temp.name) / "another-source"
        subprocess.run(["git", "init", "-q", str(other)], check=True)
        self.assertNotEqual(run("-C", str(other), "status", "--porcelain").returncode, 0)

    def test_missing_receipt_or_missing_trusted_job_digest_refuses(self):
        with self.assertRaisesRegex(ValueError, "untrusted"):
            gate.verify(self.root, self.evidence, self.source["head"], "")
        (self.evidence / "receipt.json").unlink()
        with self.assertRaises(FileNotFoundError):
            self.verify()

    def test_old_candidate_head_tree_or_input_manifest_refuses(self):
        for key in ("head", "tree", "tracked_inputs_sha256"):
            with self.subTest(key=key):
                self.receipt["source"] = {**self.source, key: "0" * len(self.source[key])}
                self.save()
                with self.assertRaisesRegex(ValueError, "another candidate"):
                    self.verify()

    def test_source_edit_refuses_even_when_receipt_is_intact(self):
        (self.root / "go.work").write_text("go 1.27.3\n")
        with self.assertRaisesRegex(ValueError, "clean"):
            self.verify()

    def test_receipt_and_raw_log_tampering_refuse(self):
        (self.evidence / "receipt.json").write_text("{}")
        with self.assertRaisesRegex(ValueError, "untrusted"):
            self.verify()
        self.save()
        (self.evidence / "events.jsonl").write_text("")
        with self.assertRaisesRegex(ValueError, "log mismatch"):
            self.verify()

    def test_skip_zero_or_missing_sdk_cannot_pass_even_with_rehashed_receipt(self):
        variants = {"zero": [], "root only": [record for record in events()
                                                if record.get("Test") not in gate.SDK_TESTS]}
        for sdk in (gate.ROOT_TEST, *gate.SDK_TESTS):
            variants[sdk + " missing"] = [record for record in events() if record.get("Test") != sdk]
            variants[sdk + " skip"] = [{**record, "Action": "skip"}
                                       if record.get("Test") == sdk and record["Action"] == "pass"
                                       else record for record in events()]
        for name, records in variants.items():
            with self.subTest(name=name):
                self.write_events(records)
                self.save()
                with self.assertRaises(ValueError):
                    self.verify()

    def test_fail_duplicate_cached_command_or_missing_native_constructor_refuse(self):
        variants = [events() + [{"Package": gate.PACKAGE, "Action": "fail"}],
                    events() + [events()[0]],
                    [record for record in events() if record["Action"] != "output"]]
        for records in variants:
            self.write_events(records)
            self.save()
            with self.assertRaises(ValueError):
                self.verify()
        self.write_events(events())
        self.receipt["command"] = [arg for arg in self.receipt["command"] if arg != "-count=1"]
        self.save()
        with self.assertRaisesRegex(ValueError, "command mismatch"):
            self.verify()

    def test_missing_sdk4_environment_or_toolchain_binding_refuses(self):
        mutations = [lambda r: r["environment"].pop("LANTERN_CURRENT_PUBLIC_SDK4"),
                     lambda r: r["tools"].pop("cargo"),
                     lambda r: r.pop("source_config_sha256")]
        for mutate in mutations:
            self.receipt["runtime"] = copy.deepcopy(self.runtime)
            mutate(self.receipt["runtime"])
            self.save()
            with self.assertRaises(ValueError):
                self.verify()

    def test_wrong_runner_cannot_authorize_the_receipt(self):
        self.receipt["runner_sha256"] = "0" * 64
        self.save()
        with self.assertRaisesRegex(ValueError, "runner mismatch"):
            self.verify()

    def test_unsupported_platform_missing_tool_and_substitute_binary_refuse(self):
        with patch.object(gate.platform, "system", return_value="Windows"):
            with self.assertRaisesRegex(ValueError, "unsupported native"):
                gate.environment(self.root, {})
        with patch.object(gate.platform, "system", return_value="Linux"), \
                patch.object(gate.platform, "machine", return_value="x86_64"), \
                patch.object(gate.shutil, "which", return_value=None):
            with self.assertRaisesRegex(ValueError, "tool missing"):
                gate.environment(self.root, {})
            with self.assertRaisesRegex(ValueError, "unsupported qualification override"):
                gate.environment(self.root, {"LANTERN_CURRENT_FIXTURE_EXPORTER": "/old/candidate"})

    def test_include_scoped_does_not_drop_existing_rust_case(self):
        self.receipt["include_scoped"] = True
        self.receipt["command"] = gate.command(True)
        self.save()
        with self.assertRaisesRegex(ValueError, "incomplete"):
            self.verify()
        self.write_events(events()[:-1] + [
            {"Package": gate.PACKAGE, "Action": action, "Test": gate.SCOPED_TEST}
            for action in ("run", "pass")] + events()[-1:])
        self.receipt["executed_tests"].append(gate.SCOPED_TEST)
        self.save()
        self.verify()

    def test_ineligible_environment_fails_without_executing_or_publishing_success(self):
        evidence = Path(self.temp.name) / "failed-environment"
        with patch.object(gate, "environment", side_effect=ValueError("native source unavailable")), \
                patch.object(gate.subprocess, "run") as execute:
            # Keep git snapshot reads outside the mocked subprocess boundary.
            with patch.object(gate, "snapshot", return_value=self.source):
                with self.assertRaisesRegex(ValueError, "native source unavailable"):
                    gate.run(self.root, evidence)
            execute.assert_not_called()
        self.assertEqual(json.loads((evidence / "receipt.json").read_text())["status"], "FAIL")

    def test_native_command_sets_both_flags_and_rejects_skip(self):
        evidence = Path(self.temp.name) / "failed-skip"

        def execute(cmd, **kwargs):
            self.assertEqual(cmd, gate.command(False))
            for name, value in gate.ENVIRONMENT.items():
                self.assertEqual(kwargs["env"][name], value)
            kwargs["stdout"].write(gate.encoded({"Package": gate.PACKAGE, "Action": "skip", "Test": gate.ROOT_TEST}).replace(b"\n", b" ") + b"\n")
            return subprocess.CompletedProcess(cmd, 0)

        with patch.object(gate, "snapshot", return_value=self.source), \
                patch.object(gate, "environment", return_value=self.runtime), \
                patch.object(gate.subprocess, "run", side_effect=execute):
            with self.assertRaisesRegex(ValueError, "skipped"):
                gate.run(self.root, evidence)
        self.assertEqual(json.loads((evidence / "receipt.json").read_text())["status"], "FAIL")


class SDK4WiringTest(unittest.TestCase):
    def test_local_acceptance_runs_sdk4_and_scoped_without_receipt_import(self):
        plan = steps(Path("/tmp/qualification"))
        self.assertEqual(len(plan), 76)
        step = next(step for step in plan if step.name == "rust-oidc-scoped-wire")
        self.assertIn(".github/scripts/current_sdk4_gate.py", step.command)
        for flag in ("run", "--include-scoped", "--fresh-child"):
            self.assertIn(flag, step.command)
        self.assertNotIn("verify", step.command)

    def test_required_pr_main_build_checks_same_run_native_receipt(self):
        build = workflow_job("go", "build")
        self.assertIn("needs: [scope, current-sdk4]", build)
        self.assertIn('run: test "$NATIVE_RESULT" = success', build)
        self.assertIn("needs.current-sdk4.outputs.artifact_id", build)
        self.assertIn("needs.current-sdk4.outputs.receipt_sha256", build)
        self.assertIn('verify --expected-head "$GITHUB_SHA"', build)
        self.assertIn("uses: ./.github/workflows/current-sdk4.yml", workflow_job("go", "current-sdk4"))

    def test_release_archive_and_publish_require_the_tag_native_lane(self):
        native = workflow_job("rust-release", "current-sdk4")
        self.assertIn("needs: tag", native)
        self.assertIn("uses: ./.github/workflows/current-sdk4.yml", native)
        preflight = workflow_job("rust-release", "preflight")
        self.assertIn("needs: [tag, conformance, current-sdk4]", preflight)
        self.assertIn("needs.current-sdk4.outputs.artifact_id", preflight)
        self.assertIn("needs.current-sdk4.outputs.receipt_sha256", preflight)
        self.assertLess(preflight.index('verify --expected-head "$GITHUB_SHA"'),
                        preflight.index("Package and inspect the exact-tag candidate"))
        self.assertIn("needs: [tag, preflight, current-sdk4]", workflow_job("rust-release", "publish"))

    def test_native_lane_cannot_conditionally_omit_execution_or_hide_failure(self):
        native = workflow_job("current-sdk4", "native")
        self.assertNotIn("continue-on-error", native)
        # This pure-Dart lane does not provision the separate Flutter example.
        self.assertIn("dart pub get --enforce-lockfile --no-example", native)
        self.assertIn('git config --global --add safe.directory "$GITHUB_WORKSPACE"', native)
        self.assertNotIn('safe.directory "*"', native)
        qualify = native.split("- name: Require exact candidate native SDK4 execution", 1)[1].split("- name:", 1)[0]
        self.assertNotIn("if:", qualify)
        self.assertIn('run --expected-head "$GITHUB_SHA"', qualify)
        self.assertIn("if-no-files-found: error", native)


if __name__ == "__main__":
    unittest.main()
