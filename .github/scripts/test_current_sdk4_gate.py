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
from git_test_fixture import synchronous_git_maintenance
from local_gate_plan import steps


ROOT = Path(__file__).resolve().parents[2]


def events(tests=None, kind="Q"):
    if tests is None:
        tests = gate.required_tests(kind)[1:]
    result = [
        {"Action": "run", "Test": gate.ROOT_TEST},
        {
            "Action": "output",
            "Test": gate.ROOT_TEST,
            "Output": gate.NATIVE_MARKER if kind == "Q" else gate.FIXTURE_MARKER,
        },
    ]
    result += [
        {
            "Action": "output",
            "Test": gate.ROOT_TEST,
            "Output": f"current_constructor node={i} mode=fresh passed=true",
        }
        for i in (1, 2, 3)
    ]
    for name in tests:
        result += [{"Action": "run", "Test": name}, {"Action": "pass", "Test": name}]
    if kind == "F":
        result.append(
            {
                "Action": "output",
                "Test": gate.ROOT_TEST,
                "Output": "current_constructor node=2 mode=resume passed=true",
            }
        )
    if kind == "Q":
        result += [
            {
                "Action": "output",
                "Test": gate.Q_CASES[-1],
                "Output": f"source_acquisition node={i} attempts=1 writes=1 installations=1 in_flight=false failed=false denied=false",
            }
            for i in (1, 2, 3)
        ]
    result += [{"Action": "pass", "Test": gate.ROOT_TEST}, {"Action": "pass"}]
    return [{"Package": gate.PACKAGE, **record} for record in result]


def mechanism_events():
    return [
        {"Package": gate.MECHANISM_PACKAGE, "Action": action, "Test": name}
        for name in gate.MECHANISM_TESTS
        for action in ("run", "pass")
    ] + [{"Package": gate.MECHANISM_PACKAGE, "Action": "pass"}]


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
        synchronous_git_maintenance(self.root)
        self.git("config", "user.name", "Fixture")
        self.git("config", "user.email", "fixture@example.test")
        self.git("add", ".")
        self.git("commit", "-qm", "fixture")
        self.source = gate.snapshot(self.root)
        self.configure("Q")

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.root, stderr=subprocess.STDOUT)

    def write_events(self, records, name="events.jsonl"):
        (self.evidence / name).write_text("".join(json.dumps(record) + "\n" for record in records))

    def configure(self, kind):
        self.kind = kind
        self.runtime = {
            "proof": gate.proof_contract(kind),
            "native_environment": {"system": "synthetic-test"},
            "environment": gate.execution_environment(kind).copy(),
            "source_config_sha256": "a" * 64,
            "tools": {
                name: {"version": "synthetic", "selected_sha256": "b" * 64}
                for name in ("go", "bun", "dart", "cargo", "rustc")
            },
        }
        self.write_events(events(kind=kind))
        for name in gate.log_names(kind):
            if not (self.evidence / name).exists():
                (self.evidence / name).write_bytes(b"")
        self.receipt = {
            "schema": gate.RECEIPT_SCHEMA,
            "proof": gate.proof_contract(kind),
            "status": "PASS",
            "exit": 0,
            "source": self.source,
            "runner_sha256": gate.sha(Path(gate.__file__).read_bytes()),
            "runtime": self.runtime,
            "command": gate.command(kind),
            "executed_tests": list(gate.required_tests(kind)),
        }
        if kind == "Q":
            self.receipt["acquisition"] = gate.acquisition_counts(self.evidence / "events.jsonl")
        else:
            self.write_events(mechanism_events(), "mechanism-events.jsonl")
            self.receipt["mechanism"] = {
                "command": gate.mechanism_command(),
                "exit": 0,
                "executed_tests": list(gate.MECHANISM_TESTS),
            }
        self.save()

    def save(self):
        self.receipt["observed"] = gate.observed_execution(self.evidence)
        self.receipt["logs"] = {
            name: gate.sha((self.evidence / name).read_bytes())
            for name in gate.log_names(self.kind)
        }
        data = gate.encoded(self.receipt)
        (self.evidence / "receipt.json").write_bytes(data)
        self.trusted_digest = gate.sha(data)

    def verify(self, kind=None):
        return gate.verify(
            self.root, self.evidence, self.source["head"], self.trusted_digest, kind or self.kind
        )

    def test_complete_distinct_contracts_and_old_schema_refusal(self):
        for kind in ("F", "Q"):
            self.configure(kind)
            self.assertEqual(self.verify()["executed_tests"], list(gate.required_tests(kind)))
            self.receipt["schema"] = 2
            self.save()
            with self.assertRaisesRegex(ValueError, "not successful"):
                self.verify()
        self.assertNotIn(gate.RETAINED_ROOT_TESTS[0], gate.required_tests("Q"))
        self.assertIn(gate.SCOPED_TEST, gate.required_tests("F"))

    def test_missing_skip_fail_duplicate_body_and_constructor_refuse(self):
        for kind in ("F", "Q"):
            self.configure(kind)
            complete = events(kind=kind)
            variants = [
                [],
                complete + [complete[0]],
                [r for r in complete if r.get("Action") != "output"],
            ]
            for name in gate.required_tests(kind):
                variants.append([r for r in complete if r.get("Test") != name])
                for action in ("skip", "fail"):
                    variants.append(
                        [
                            {**r, "Action": action}
                            if r.get("Test") == name and r["Action"] == "pass"
                            else r
                            for r in complete
                        ]
                    )
            for records in variants:
                self.write_events(records)
                self.save()
                with self.assertRaises(ValueError):
                    self.verify()

    def test_q_counts_are_complete_bounded_and_not_lossy_trace_inferences(self):
        complete = events()
        count = [r for r in complete if "source_acquisition" in r.get("Output", "")]
        variants = [[r for r in complete if r not in count], complete + [count[0]]]
        for old, new in (
            ("attempts=1", "attempts=5"),
            ("in_flight=false", "in_flight=true"),
            ("failed=false", "failed=true"),
            ("denied=false", "denied=true"),
            ("installations=1", "installations=0"),
            ("writes=1", "writes=0"),
        ):
            variants.append(
                [
                    {**r, "Output": r.get("Output", "").replace(old, new)} if r in count else r
                    for r in complete
                ]
            )
        for records in variants:
            self.write_events(records)
            self.save()
            with self.assertRaises(ValueError):
                self.verify()

    def test_proof_source_build_environment_and_command_mismatch_refuse(self):
        for kind in ("F", "Q"):
            self.configure(kind)
            original = copy.deepcopy(self.receipt)
            for field, value in (
                ("proof", gate.proof_contract("Q" if kind == "F" else "F")),
                ("command", [x for x in original["command"] if x != "-count=1"]),
                ("runner_sha256", "0" * 64),
            ):
                self.receipt = copy.deepcopy(original)
                self.receipt[field] = value
                self.save()
                with self.assertRaises(ValueError):
                    self.verify()
            for field in ("environment", "tools", "source_config_sha256", "native_environment"):
                self.receipt = copy.deepcopy(original)
                self.receipt["runtime"].pop(field)
                self.save()
                with self.assertRaises(ValueError):
                    self.verify()

    def test_f_requires_all_public_bodies_after_sdk4_in_the_same_cohort(self):
        self.configure("F")
        complete = events(kind="F")
        # Independent of the producer's inventory: reproduce the old SDK4-only
        # receipt, even with a successful separate export cohort alongside it.
        public = (ROOT / "tests/integration/current_security_gate_test.go").read_text()
        names = re.findall(r'runCase\("([^"]+)"', public)
        names += ["active export grant revocation"]
        expected = {f"{gate.ROOT_TEST}/{name.replace(' ', '_')}" for name in names}
        self.assertEqual(len(expected), 13)
        self.assertEqual(set(gate.F_CASES), expected)
        self.assertEqual(len(gate.F_PROTOCOLS), 5)
        omitted = expected | set(gate.F_PROTOCOLS)
        self.write_events([r for r in complete if r.get("Test") not in omitted])
        self.write_events(
            events((f"{gate.ROOT_TEST}/active_export_grant_revocation",), "F"),
            "export-events.jsonl",
        )
        self.save()
        with self.assertRaisesRegex(ValueError, "absent, duplicated or incomplete"):
            self.verify()
        self.assertNotIn("export-events.jsonl", gate.log_names("F"))

    def test_f_requires_actual_export_and_mechanism_bodies(self):
        for logfile, name in (
            ("events.jsonl", f"{gate.ROOT_TEST}/active_export_grant_revocation"),
            ("mechanism-events.jsonl", gate.MECHANISM_TESTS[0]),
        ):
            self.configure("F")
            complete = [
                json.loads(line) for line in (self.evidence / logfile).read_text().splitlines()
            ]
            for records in (
                [],
                [r for r in complete if r.get("Test") != name],
                complete + [complete[0]],
            ):
                self.write_events(records, logfile)
                self.save()
                with self.assertRaises(ValueError):
                    self.verify()
        self.configure("F")
        with self.assertRaisesRegex(ValueError, "proof kind"):
            self.verify("Q")

    def test_f_requires_the_intact_resume_and_q_forbids_an_extra_owner(self):
        for kind in ("F", "Q"):
            self.configure(kind)
            complete = events(kind=kind)
            constructors = [r for r in complete if "current_constructor" in r.get("Output", "")]
            for row in constructors:
                for replacement in (
                    None,
                    {**row, "Output": row["Output"].replace("true", "false")},
                ):
                    records = [r for r in complete if r is not row]
                    if replacement is not None:
                        records.append(replacement)
                    self.write_events(records)
                    self.save()
                    with self.assertRaises(ValueError):
                        self.verify()
            self.write_events(complete + [constructors[-1]])
            self.save()
            with self.assertRaises(ValueError):
                self.verify()

    def test_receipt_logs_exact_source_and_trusted_digest_are_bound(self):
        with self.assertRaisesRegex(ValueError, "untrusted"):
            gate.verify(self.root, self.evidence, self.source["head"], "")
        (self.evidence / "receipt.json").write_text("{}")
        with self.assertRaisesRegex(ValueError, "untrusted"):
            self.verify()
        self.save()
        (self.evidence / "events.jsonl").write_text("")
        with self.assertRaisesRegex(ValueError, "log mismatch"):
            self.verify()
        self.configure("Q")
        for key in ("head", "tree", "tracked_inputs_sha256"):
            self.receipt["source"] = {**self.source, key: "0" * len(self.source[key])}
            self.save()
            with self.assertRaisesRegex(ValueError, "another candidate"):
                self.verify()
        self.receipt["source"] = self.source
        self.save()
        (self.root / "go.work").write_text("go 1.27.3\n")
        with self.assertRaisesRegex(ValueError, "clean"):
            self.verify()

    def test_runner_q_uses_one_cohort_and_retains_failure(self):
        for failure in (False, True):
            evidence = Path(self.temp.name) / f"run-q-{failure}"

            def execute(cmd, **kwargs):
                self.assertEqual(cmd, gate.command("Q"))
                self.assertEqual(kwargs["env"]["LANTERN_CURRENT_SOURCE_QUALIFICATION"], "1")
                records = events()
                if failure:
                    records[-1]["Action"] = "fail"
                for record in records:
                    kwargs["stdout"].write((json.dumps(record) + "\n").encode())
                return subprocess.CompletedProcess(cmd, 1 if failure else 0)

            with (
                patch.object(gate, "snapshot", return_value=self.source),
                patch.object(gate, "environment", return_value=self.runtime),
                patch.object(gate.subprocess, "run", side_effect=execute) as run,
            ):
                if failure:
                    with self.assertRaises(ValueError):
                        gate.run(self.root, evidence)
                else:
                    gate.run(self.root, evidence)
                self.assertEqual(run.call_count, 1)
            receipt = json.loads((evidence / "receipt.json").read_text())
            self.assertNotIn("export", receipt)
            self.assertEqual(receipt["status"], "FAIL" if failure else "PASS")

    def test_runner_f_keeps_mechanism_full_behavior_and_export(self):
        self.configure("F")
        evidence = Path(self.temp.name) / "run-f"
        calls = []

        def execute(cmd, **kwargs):
            expected = (
                gate.mechanism_command(),
                gate.command("F"),
            )[len(calls)]
            self.assertEqual(cmd, expected)
            records = (mechanism_events(), events(kind="F"))[len(calls)]
            calls.append(cmd)
            for record in records:
                kwargs["stdout"].write((json.dumps(record) + "\n").encode())
            return subprocess.CompletedProcess(cmd, 0)

        with (
            patch.object(gate, "snapshot", return_value=self.source),
            patch.object(gate, "environment", return_value=self.runtime),
            patch.object(gate.subprocess, "run", side_effect=execute),
        ):
            gate.run(self.root, evidence, kind="F")
        self.assertEqual(len(calls), 2)
        self.assertNotIn("export", json.loads((evidence / "receipt.json").read_text()))

    def test_ineligible_environment_never_starts_a_native_process(self):
        evidence = Path(self.temp.name) / "failed-environment"
        with (
            patch.object(gate, "snapshot", return_value=self.source),
            patch.object(gate, "environment", side_effect=ValueError("source unavailable")),
            patch.object(gate.subprocess, "run") as execute,
        ):
            with self.assertRaisesRegex(ValueError, "source unavailable"):
                gate.run(self.root, evidence)
            execute.assert_not_called()

    def test_unsupported_platform_missing_tools_or_substitute_binary_refuse(self):
        with patch.object(gate.platform, "system", return_value="Windows"):
            with self.assertRaisesRegex(ValueError, "unsupported native"):
                gate.environment(self.root, {})
        with (
            patch.object(gate.platform, "system", return_value="Linux"),
            patch.object(gate.platform, "machine", return_value="x86_64"),
            patch.object(gate.shutil, "which", return_value=None),
        ):
            with self.assertRaisesRegex(ValueError, "tool missing"):
                gate.environment(self.root, {})
            with self.assertRaisesRegex(ValueError, "unsupported qualification override"):
                gate.environment(self.root, {"GOFLAGS": "-overlay=foreign"})


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
        for boundary in (
            "newCurrentAuthorityTimeOwner()",
            "bindAuthorityNetworkTime(&c, clock)",
            "custody.running()",
            "openS3AOwner(c, fresh, floors)",
            "attachAuthorityOrigin(n, key, browserOrigin)",
        ):
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
            self.assertIn(
                "uses: ./.github/workflows/current-source-release.yml",
                workflow_job(workflow, "configured-source"),
            )
        for workflow, job in (
            ("admin-publish", "build"),
            ("node-sdk", "publish"),
            ("dart-sdk", "release-preflight"),
            ("dart-sdk", "offline-release-preflight"),
        ):
            needs = next(
                line
                for line in workflow_job(workflow, job).splitlines()
                if line.strip().startswith("needs:")
            )
            self.assertIn("configured-source", needs)

    def test_local_acceptance_runs_sdk4_and_scoped_without_receipt_import(self):
        plan = steps(Path("/tmp/qualification"))
        self.assertEqual(len(plan), 77)
        step = next(step for step in plan if step.name == "rust-oidc-scoped-wire")
        self.assertIn(".github/scripts/current_sdk4_gate.py", step.command)
        for flag in ("run", "--with-mechanism", "--fresh-child"):
            self.assertIn(flag, step.command)
        self.assertNotIn("verify", step.command)

    def test_required_pr_main_build_checks_same_run_native_receipt(self):
        build = workflow_job("go", "build")
        self.assertIn(
            "needs: [scope, modules, mechanism, current-sdk4, receipt-backup-windows]", build
        )
        self.assertIn("run: python3 -B .github/scripts/current_qualification_result.py", build)
        self.assertIn("needs.current-sdk4.outputs.artifact_id", build)
        self.assertIn("needs.current-sdk4.outputs.receipt_sha256", build)
        self.assertIn('verify --expected-head "$GITHUB_SHA"', build)
        self.assertIn(
            "uses: ./.github/workflows/current-sdk4.yml", workflow_job("go", "current-sdk4")
        )

    def test_release_archive_and_publish_require_the_tag_native_lane(self):
        native = workflow_job("rust-release", "current-sdk4")
        self.assertIn("needs: tag", native)
        self.assertIn("uses: ./.github/workflows/current-sdk4.yml", native)
        preflight = workflow_job("rust-release", "preflight")
        self.assertIn("needs: [tag, conformance, current-sdk4]", preflight)
        self.assertIn("needs.current-sdk4.outputs.artifact_id", preflight)
        self.assertIn("needs.current-sdk4.outputs.receipt_sha256", preflight)
        self.assertLess(
            preflight.index('verify --expected-head "$GITHUB_SHA"'),
            preflight.index("Package and inspect the exact-tag candidate"),
        )
        self.assertIn(
            "needs: [tag, preflight, current-sdk4]", workflow_job("rust-release", "publish")
        )

    def test_native_lane_cannot_conditionally_omit_execution_or_hide_failure(self):
        native = workflow_job("current-sdk4", "native")
        self.assertNotIn("continue-on-error", native)
        # This pure-Dart lane does not provision the separate Flutter example.
        self.assertIn("dart pub get --enforce-lockfile --no-example", native)
        self.assertIn('git config --global --add safe.directory "$GITHUB_WORKSPACE"', native)
        self.assertNotIn('safe.directory "*"', native)
        qualify = native.split("- name: Require exact candidate native SDK4 execution", 1)[1].split(
            "- name:", 1
        )[0]
        self.assertNotIn("if:", qualify)
        self.assertIn('run --expected-head "$GITHUB_SHA"', qualify)
        self.assertIn("if-no-files-found: error", native)


if __name__ == "__main__":
    unittest.main()
