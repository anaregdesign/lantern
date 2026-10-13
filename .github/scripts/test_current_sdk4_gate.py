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


def events(tests=gate.SDK_TESTS, kind="Q"):
    result = [{"Action": "run", "Test": gate.ROOT_TEST},
              {"Action": "output", "Test": gate.ROOT_TEST, "Output": gate.NATIVE_MARKER if kind == "Q" else gate.FIXTURE_MARKER}]
    result += [{"Action": "output", "Test": gate.ROOT_TEST,
                "Output": f"current_constructor node={i} mode=fresh passed=true"} for i in (1, 2, 3)]
    for name in tests:
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
        self.runtime = {"proof": gate.proof_contract("Q"), "native_environment": {"system": "synthetic-test"}, "environment": gate.ENVIRONMENT.copy(), "source_config_sha256": "a" * 64,
                        "tools": {name: {"version": "synthetic", "selected_sha256": "b" * 64}
                                  for name in ("go", "bun", "dart", "cargo", "rustc")}}
        self.write_events(events())
        (self.evidence / "stderr.log").write_bytes(b"")
        self.write_events(events((gate.EXPORT_TEST,)), "export-events.jsonl")
        (self.evidence / "export-stderr.log").write_bytes(b"")
        self.receipt = {"schema": 2, "proof": gate.proof_contract("Q"), "status": "PASS", "exit": 0, "source": self.source,
                        "runner_sha256": gate.sha(script.read_bytes()), "runtime": self.runtime,
                        "include_scoped": False, "command": gate.command(False),
                        "executed_tests": [gate.ROOT_TEST, *gate.SDK_TESTS],
                        "export": {"command": gate.export_command(), "environment": gate.EXPORT_ENVIRONMENT,
                                   "exit": 0, "executed_tests": [gate.ROOT_TEST, gate.EXPORT_TEST]}}
        self.save()

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.root, stderr=subprocess.STDOUT)

    def write_events(self, records, name="events.jsonl"):
        (self.evidence / name).write_text("".join(json.dumps(record) + "\n" for record in records))

    def save(self):
        self.receipt["observed"] = gate.observed_execution(self.evidence)
        self.receipt["logs"] = {name: gate.sha((self.evidence / name).read_bytes())
                                for name in gate.LOG_NAMES}
        data = gate.encoded(self.receipt)
        (self.evidence / "receipt.json").write_bytes(data)
        self.trusted_digest = gate.sha(data)

    def verify(self):
        return gate.verify(self.root, self.evidence, self.source["head"], self.trusted_digest)

    def test_complete_execution_and_exact_source(self):
        self.assertEqual(self.verify()["executed_tests"], [gate.ROOT_TEST, *gate.SDK_TESTS])
        self.assertEqual(self.verify()["export"]["executed_tests"], [gate.ROOT_TEST, gate.EXPORT_TEST])

    def test_constructor_failure_reports_body_not_run_without_inventing_success(self):
        self.write_events([{"Package": gate.PACKAGE, "Test": gate.ROOT_TEST, "Action": "run"},
                           {"Package": gate.PACKAGE, "Test": gate.ROOT_TEST, "Action": "output",
                            "Output": "current_constructor node=1 mode=fresh passed=false"},
                           {"Package": gate.PACKAGE, "Test": gate.ROOT_TEST, "Action": "fail"}], "export-events.jsonl")
        observed = gate.observed_execution(self.evidence)["export-events.jsonl"]
        self.assertEqual(observed["tests"][gate.ROOT_TEST], "FAIL")
        self.assertEqual(observed["tests"][gate.EXPORT_TEST], "NOT_RUN")
        self.assertEqual(observed["constructors"], [["1", "fresh", "false"]])

    def test_proof_build_source_and_fresh_owner_mismatch_refuse(self):
        for proof in ({}, gate.proof_contract("F"), {**gate.proof_contract("Q"), "synthetic_utc": True},
                      {**gate.proof_contract("Q"), "build_tags": [gate.FIXTURE_TAG]}):
            self.receipt["proof"] = proof
            self.save()
            with self.assertRaisesRegex(ValueError, "proof kind"):
                self.verify()
        self.receipt["proof"] = gate.proof_contract("Q")
        for variant in (events(kind="F"),
                        [r for r in events() if "node=2" not in r.get("Output", "")],
                        [{**r, "Output": r.get("Output", "").replace("node=2 mode=fresh passed=true", "node=2 mode=fresh passed=false")}
                         if "Output" in r else r for r in events()]):
            self.write_events(variant)
            self.save()
            with self.assertRaises(ValueError):
                self.verify()

    def test_f_requires_native_loss_recovery_and_cannot_be_reused_as_q(self):
        self.receipt["proof"] = self.runtime["proof"] = gate.proof_contract("F")
        self.receipt["command"] = gate.command(False, "F")
        self.receipt["export"]["command"] = gate.export_command("F")
        self.write_events(events(kind="F"))
        self.write_events(events((gate.EXPORT_TEST,), "F"), "export-events.jsonl")
        mechanism_events = [{"Package": gate.MECHANISM_PACKAGE, "Action": action, "Test": test}
                            for test in gate.MECHANISM_TESTS for action in ("run", "pass")]
        mechanism_events.append({"Package": gate.MECHANISM_PACKAGE, "Action": "pass"})
        self.write_events(mechanism_events, "mechanism-events.jsonl")
        (self.evidence / "mechanism-stderr.log").write_bytes(b"")
        self.receipt["mechanism"] = {"command": gate.mechanism_command(), "exit": 0,
                                     "executed_tests": list(gate.MECHANISM_TESTS)}
        self.save()
        self.receipt["logs"].update({n: gate.sha((self.evidence / n).read_bytes())
                                    for n in gate.log_names("F")})
        def save_f():
            data = gate.encoded(self.receipt)
            (self.evidence / "receipt.json").write_bytes(data)
            self.trusted_digest = gate.sha(data)
        save_f()
        gate.verify(self.root, self.evidence, self.source["head"], self.trusted_digest, "F")
        with self.assertRaisesRegex(ValueError, "proof kind"):
            self.verify()
        self.receipt.pop("mechanism")
        save_f()
        with self.assertRaisesRegex(ValueError, "loss/recovery"):
            gate.verify(self.root, self.evidence, self.source["head"], self.trusted_digest, "F")

    def test_export_missing_skip_fail_duplicate_or_sdk4_only_refuses(self):
        complete = events((gate.EXPORT_TEST,))
        variants = [[], events(), complete + [complete[2]],
                    [record for record in complete if record.get("Test") != gate.EXPORT_TEST]]
        for action in ("skip", "fail"):
            variants.append([{**record, "Action": action}
                             if record.get("Test") == gate.EXPORT_TEST and record["Action"] == "pass"
                             else record for record in complete])
        for records in variants:
            with self.subTest(records=records):
                self.write_events(records, "export-events.jsonl")
                self.save()
                with self.assertRaises(ValueError):
                    self.verify()

    def test_export_command_environment_exit_and_raw_log_are_bound(self):
        original = copy.deepcopy(self.receipt["export"])
        for mutation in ({}, {**original, "command": gate.command(False)},
                         {**original, "environment": gate.ENVIRONMENT}, {**original, "exit": 1},
                         {**original, "executed_tests": [gate.ROOT_TEST]}):
            self.receipt["export"] = mutation
            self.save()
            with self.assertRaises(ValueError):
                self.verify()
        self.receipt["export"] = original
        self.save()
        (self.evidence / "export-events.jsonl").write_text("")
        with self.assertRaisesRegex(ValueError, "log mismatch"):
            self.verify()

    def test_runner_executes_export_with_sdk4_disabled_and_retains_failure(self):
        for export_exit in (0, 1):
            with self.subTest(export_exit=export_exit):
                evidence = Path(self.temp.name) / f"export-run-{export_exit}"
                calls = []

                def execute(cmd, **kwargs):
                    exporting = len(calls) == 1
                    self.assertEqual(cmd, gate.export_command() if exporting else gate.command(False))
                    expected_env = gate.EXPORT_ENVIRONMENT if exporting else gate.ENVIRONMENT
                    for name, value in expected_env.items():
                        self.assertEqual(kwargs["env"][name], value)
                    calls.append(cmd)
                    records = events((gate.EXPORT_TEST,)) if exporting else events()
                    if exporting and export_exit:
                        records[-1]["Action"] = "fail"
                    for record in records:
                        kwargs["stdout"].write((json.dumps(record) + "\n").encode())
                    return subprocess.CompletedProcess(cmd, export_exit if exporting else 0)

                with patch.object(gate, "snapshot", return_value=self.source), \
                        patch.object(gate, "environment", return_value=self.runtime), \
                        patch.object(gate.subprocess, "run", side_effect=execute), \
                        patch.dict(os.environ, {}, clear=True):
                    if export_exit:
                        with self.assertRaisesRegex(ValueError, "active-export command failed"):
                            gate.run(self.root, evidence)
                    else:
                        gate.run(self.root, evidence)
                self.assertEqual(len(calls), 2)
                receipt = json.loads((evidence / "receipt.json").read_text())
                self.assertEqual(receipt["status"], "FAIL" if export_exit else "PASS")
                self.assertEqual(receipt["export"]["exit"], export_exit)
                self.assertEqual(set(receipt["logs"]), set(gate.LOG_NAMES))

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
    def test_normal_and_fixture_builds_have_no_runtime_clock_selector(self):
        normal = (ROOT / "server/internal/security/control_time_composition.go").read_text()
        fixture = (ROOT / "server/internal/security/control_time_fixture.go").read_text()
        composition = (ROOT / "server/internal/security/control_current_owner.go").read_text()
        self.assertTrue(normal.startswith("//go:build !lantern_test_time"))
        self.assertTrue(fixture.startswith("//go:build lantern_test_time && (darwin || linux)"))
        self.assertIn("return newNativeAuthorityTimeOwner()", normal)
        self.assertIn("if !testing.Testing()", fixture)
        self.assertNotIn("os.Getenv", normal + fixture)
        self.assertIn("if c.timeOwner != nil", composition)
        for boundary in ("newCurrentAuthorityTimeOwner()", "bindAuthorityNetworkTime(&c, clock)",
                         "custody.running()", "openS3AOwner(c, fresh, floors)", "attachAuthorityOrigin(n, key, browserOrigin)"):
            self.assertIn(boundary, composition)

    def test_f_and_modules_run_independently_of_q_but_required_result_binds_both(self):
        for job in ("mechanism", "modules", "current-sdk4"):
            self.assertIn("needs: scope\n", workflow_job("go", job))
        self.assertIn("proof_kind: F", workflow_job("go", "mechanism"))
        self.assertIn("proof_kind: Q", workflow_job("go", "current-sdk4"))
        self.assertIn("q_required != 'false'", workflow_job("go", "current-sdk4"))
        build = workflow_job("go", "build")
        for obligation in ("current_qualification_result.py", "--proof-kind F", "--proof-kind Q"):
            self.assertIn(obligation, build)
        self.assertIn("go_modules.py", workflow_job("go", "modules"))

    def test_publication_paths_require_exact_normal_build_q(self):
        wrapper = (ROOT / ".github/workflows/current-source-release.yml").read_text()
        self.assertIn("proof_kind: Q", wrapper)
        self.assertIn('verify --expected-head "$GITHUB_SHA" --proof-kind Q', wrapper)
        self.assertIn("needs.qualify.outputs.receipt_sha256", wrapper)
        for workflow in ("_verify", "admin-publish", "node-sdk", "dart-sdk"):
            self.assertIn("uses: ./.github/workflows/current-source-release.yml", workflow_job(workflow, "configured-source"))
        for workflow, job in (("admin-publish", "build"), ("node-sdk", "publish"),
                              ("dart-sdk", "release-preflight"), ("dart-sdk", "offline-release-preflight")):
            needs = next(line for line in workflow_job(workflow, job).splitlines() if line.strip().startswith("needs:"))
            self.assertIn("configured-source", needs)

    def test_local_acceptance_runs_sdk4_and_scoped_without_receipt_import(self):
        plan = steps(Path("/tmp/qualification"))
        self.assertEqual(len(plan), 76)
        step = next(step for step in plan if step.name == "rust-oidc-scoped-wire")
        self.assertIn(".github/scripts/current_sdk4_gate.py", step.command)
        for flag in ("run", "--include-scoped", "--with-mechanism", "--fresh-child"):
            self.assertIn(flag, step.command)
        self.assertNotIn("verify", step.command)

    def test_required_pr_main_build_checks_same_run_native_receipt(self):
        build = workflow_job("go", "build")
        self.assertIn("needs: [scope, modules, mechanism, current-sdk4]", build)
        self.assertIn("run: python3 -B .github/scripts/current_qualification_result.py", build)
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
