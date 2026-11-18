"""The Dart workflow's package and native-platform selection contract."""

import ast
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import ci_scope


def release_condition(job):
    workflow = Path(__file__).resolve().parents[3] / '.github/workflows/dart-sdk.yml'
    body = re.search(
        rf'(?ms)^  {re.escape(job)}:\n(.*?)(?=^  [\w-]+:\n|\Z)',
        workflow.read_text(),
    ).group(1).splitlines()
    index = next(i for i, line in enumerate(body) if line.startswith('    if:'))
    expression = body[index].partition(':')[2].strip()
    if expression in ('>-', '|'):
        parts = []
        for line in body[index + 1:]:
            if not line.startswith('      '):
                break
            parts.append(line.strip())
        expression = ' '.join(parts)
    return expression.removeprefix('${{').removesuffix('}}').strip()


def release_job_runs(expression, context, *, canceled=False):
    # GitHub's implicit success() includes skipped indirect ancestors. Model
    # the actual release chain: evidence-only skipped -> Gate succeeded.
    if not re.search(r'\b(?:success|failure|always|cancelled)\(', expression):
        return False
    expression = re.sub(r'needs\.([\w-]+)', r"needs['\1']", expression)
    expression = re.sub(r'!(?!=)', 'not ', expression)
    parsed = ast.parse(expression.replace('&&', ' and ').replace('||', ' or '), mode='eval')

    def value(node):
        if isinstance(node, ast.Constant):
            return node.value
        if isinstance(node, ast.Name):
            return context[node.id]
        if isinstance(node, ast.Attribute):
            return value(node.value)[node.attr]
        if isinstance(node, ast.Subscript):
            return value(node.value)[value(node.slice)]
        if isinstance(node, ast.BoolOp) and isinstance(node.op, ast.And):
            return all(value(item) for item in node.values)
        if isinstance(node, ast.BoolOp) and isinstance(node.op, ast.Or):
            return any(value(item) for item in node.values)
        if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.Not):
            return not value(node.operand)
        if isinstance(node, ast.Compare) and len(node.ops) == 1 and isinstance(node.ops[0], ast.Eq):
            return value(node.left) == value(node.comparators[0])
        if isinstance(node, ast.Compare) and len(node.ops) == 1 and isinstance(node.ops[0], ast.NotEq):
            return value(node.left) != value(node.comparators[0])
        if isinstance(node, ast.Call) and isinstance(node.func, ast.Name):
            if node.func.id == 'cancelled' and not node.args:
                return canceled
            if node.func.id == 'startsWith' and len(node.args) == 2:
                return value(node.args[0]).startswith(value(node.args[1]))
        raise AssertionError(f'unsupported release expression: {ast.dump(node)}')

    return value(parsed.body)


class ResourceProfileTest(unittest.TestCase):
    def test_only_exact_package_publication_pushes_measure_resources(self):
        parent = "refs/tags/sdks/dart/v0.5.0"
        child = "refs/tags/sdks/dart/offline/v0.6.0"
        for event in ("push", "pull_request", "workflow_dispatch", "schedule", ""):
            for ref in (parent, child, "refs/heads/main", parent + "-extra", ""):
                with self.subTest(event=event, ref=ref):
                    expected = {parent: "parent-contracts", child: "full"}.get(ref) if event == "push" else None
                    self.assertEqual(ci_scope.resource_profile(event, ref), expected or "unit-contracts")

    def test_both_resource_jobs_consume_classified_profile(self):
        workflow = (Path(__file__).resolve().parents[3] / '.github/workflows/dart-sdk.yml').read_text()
        self.assertIn('resource_profile: ${{ steps.scope.outputs.resource_profile }}', workflow)
        self.assertEqual(workflow.count('RESOURCE_PROFILE: ${{ needs.changes.outputs.resource_profile }}'), 2)
        self.assertEqual(workflow.count('dart run tool/performance_probe.dart --profile "$RESOURCE_PROFILE"'), 2)
        self.assertEqual(workflow.count("if: needs.changes.outputs.resource_profile == 'full' || needs.changes.outputs.resource_profile == 'parent-contracts'"), 4)
        diagnostic = workflow.split('  sqlite-diagnostic:', 1)[1].split('  offline-sqlite:', 1)[0]
        self.assertNotIn('--profile', diagnostic)


class ReleaseConditionsTest(unittest.TestCase):
    def test_preflight_survives_skipped_ancestor_and_requires_qualified_push(self):
        for job, prefix, other in (
            ('release-preflight', 'refs/tags/sdks/dart/v', 'refs/tags/sdks/dart/offline/v'),
            ('offline-release-preflight', 'refs/tags/sdks/dart/offline/v', 'refs/tags/sdks/dart/v'),
        ):
            expression = release_condition(job)
            for result, event, ref, canceled, expected in (
                ('success', 'push', prefix + '0.4.1', False, True),
                ('failure', 'push', prefix + '0.4.1', False, False),
                ('cancelled', 'push', prefix + '0.4.1', False, False),
                ('skipped', 'push', prefix + '0.4.1', False, False),
                ('success', 'push', prefix + '0.4.1', True, False),
                ('success', 'workflow_dispatch', prefix + '0.4.1', False, False),
                ('success', 'pull_request', prefix + '0.4.1', False, False),
                ('success', 'push', 'refs/heads/main', False, False),
                ('success', 'push', other + '0.4.1', False, False),
            ):
                with self.subTest(job=job, result=result, event=event, ref=ref, canceled=canceled):
                    context = {'needs': {'configured-source-integration': {'result': 'success'}, 'gate': {'result': result},
                                        'offline-hosted': {'result': 'success', 'outputs': {'qualified': 'true'}}},
                               'github': {'event_name': event, 'ref': ref}}
                    self.assertEqual(release_job_runs(expression, context, canceled=canceled), expected)

    def test_parent_and_offline_release_refuse_failed_or_missing_source_integration(self):
        for job, tag in (("release-preflight", "sdks/dart/v0.5.0"),
                         ("offline-release-preflight", "sdks/dart/offline/v0.6.0")):
            for source_result in ("failure", "cancelled", "skipped", ""):
                context = {"needs": {"gate": {"result": "success"},
                                     "configured-source-integration": {"result": source_result},
                                     "offline-hosted": {"result": "success", "outputs": {"qualified": "true"}}},
                           "github": {"event_name": "push", "ref": "refs/tags/" + tag}}
                self.assertFalse(release_job_runs(release_condition(job), context))

    def test_hosted_qualification_waits_for_parent_equality_and_survives_expected_skips(self):
        expression = release_condition('offline-hosted')
        parent = 'refs/tags/sdks/dart/v0.5.0'
        for event, ref, verification, expected in (
            ('push', parent, 'success', True),
            ('push', parent, 'failure', False),
            ('push', parent, 'cancelled', False),
            ('push', parent, 'skipped', False),
            ('push', 'refs/tags/sdks/dart/offline/v0.6.0', 'skipped', True),
            ('push', 'refs/heads/main', 'skipped', True),
            ('pull_request', 'refs/pull/1/merge', 'skipped', True),
            ('workflow_dispatch', parent, 'skipped', True),
            ('workflow_dispatch', 'refs/tags/sdks/dart/offline/v0.6.0', 'skipped', True),
            ('workflow_dispatch', 'refs/heads/main', 'skipped', True),
        ):
            for gate, full, canceled in (('success', 'true', False), ('failure', 'true', False),
                                         ('skipped', 'true', False), ('success', 'false', False),
                                         ('success', 'true', True)):
                with self.subTest(event=event, ref=ref, verification=verification, gate=gate, full=full):
                    context = {'github': {'event_name': event, 'ref': ref}, 'needs': {
                        'gate': {'result': gate}, 'changes': {'outputs': {'full': full, 'offline': 'true'}},
                        'verify-published': {'result': verification}}}
                    self.assertEqual(release_job_runs(expression, context, canceled=canceled),
                                     expected and gate == 'success' and full == 'true' and not canceled)

    def test_offline_preflight_never_accepts_pending_failed_or_skipped_hosted_proof(self):
        expression = release_condition('offline-release-preflight')
        for result, qualified in (('failure', 'true'), ('cancelled', 'true'),
                                  ('skipped', 'true'), ('success', 'false'), ('success', None)):
            with self.subTest(result=result, qualified=qualified):
                context = {'github': {'event_name': 'push', 'ref': 'refs/tags/sdks/dart/offline/v0.6.0'},
                           'needs': {'configured-source-integration': {'result': 'success'}, 'gate': {'result': 'success'}, 'offline-hosted': {
                               'result': result, 'outputs': {'qualified': qualified}}}}
                self.assertFalse(release_job_runs(expression, context))

    def test_published_verification_rejects_failed_publish_and_unexpected_skips(self):
        for job, preflight, publish in (
            ('verify-published', 'release-preflight', 'publish'),
            ('verify-offline-published', 'offline-release-preflight', 'publish-offline'),
        ):
            for result, required, expected in (
                ('success', 'true', True), ('skipped', 'false', True),
                ('skipped', 'true', False), ('failure', 'false', False),
                ('cancelled', 'true', False), ('skipped', None, False),
            ):
                with self.subTest(job=job, result=result, required=required):
                    context = {'needs': {preflight: {'result': 'success', 'outputs': {'publish_required': required}},
                                         publish: {'result': result}}}
                    self.assertEqual(release_job_runs(release_condition(job), context), expected)

    def test_publish_survives_skipped_ancestor_and_requires_verified_artifact(self):
        for job, preflight in (
            ('publish', 'release-preflight'),
            ('publish-offline', 'offline-release-preflight'),
        ):
            expression = release_condition(job)
            for result, required, canceled, expected in (
                ('success', 'true', False, True),
                ('failure', 'true', False, False),
                ('cancelled', 'true', False, False),
                ('skipped', 'true', False, False),
                ('success', 'true', True, False),
                ('success', 'false', False, False),
                ('success', None, False, False),
            ):
                with self.subTest(job=job, result=result, required=required, canceled=canceled):
                    context = {'needs': {preflight: {
                        'result': result, 'outputs': {'publish_required': required}}}}
                    self.assertEqual(release_job_runs(expression, context, canceled=canceled), expected)


class ScopeTest(unittest.TestCase):
    def test_modified_host_reader_does_not_hide_deleted_or_renamed_release_tool(self):
        reader = "sdks/dart/offline/tool/physical_release_source.py"
        removed = "sdks/dart/offline/tool/physical_receipt_attestation.py"
        for status in ("D", "A"):
            changes = {reader: "M", removed: status}
            scope = ci_scope.validation_scope.affected(changes)
            jobs, _ = ci_scope.selected_jobs(changes, scope)
            self.assertEqual(jobs, {"release_host", "offline", "sqlite", "mobile", "full"})

    def test_routing_selects_only_the_owning_jobs(self):
        action = ci_scope.DART_WORKFLOW
        reused = ".github/workflows/current-sdk4.yml"
        semantic = {action: "setup-dart", reused: "setup-dart", "go.mod": "cli-dependency", "go.sum": "cli-dependency"}
        for paths, expected in (
            (["go.mod", "go.sum"], set()),
            ([action, reused], {"parent", "offline", "full"}),
            ([reused], set()),
            ([action, "sdks/dart/example/lib/view.dart"], {"parent", "offline", "full", "mobile", "sqlite"}),
            (["README.md", "server/service/edge.go"], ci_scope.WIRE),
            (["sdks/dart/offline/lib/session.dart"], {"offline", "sqlite", "mobile", "full"}),
            (["sdks/dart/.pubignore"], {"parent", "offline", "full"}),
            (["sdks/dart/scripts/release.py"], {"release_host", "parent", "offline", "full"}),
            (["sdks/dart/scripts/release.py", "sdks/dart/offline/tool/physical_release_source.py"],
             {"release_host", "parent", "offline", "full"}),
            (["sdks/dart/offline/tool/release_contract_test.py", action],
             {"release_host", "parent", "offline", "full"}),
            (["sdks/dart/example/evidence/2026/os.json"], ci_scope.WIRE | {"full"}),
            (["sdks/dart/offline/test/mutations_test.dart"], {"offline", "full"}),
            (["sdks/dart/offline/test/mutations_test.dart", "admin/app.ts"], {"offline", "full"}),
            (["tests/integration/dart_identity_offline_sqlite_test.dart", "sdks/dart/offline/test/test.dart"],
             {"sqlite", "offline", "full"}),
            (["sdks/dart/offline_sqlite/test/store_test.dart"], {"sqlite"}),
            (["tests/integration/dart_identity_offline_sqlite_test.dart"], {"sqlite"}),
            (["tests/integration/dart_data_client_test.dart"], {"parent"}),
            (["tests/integration/dart_contribution_delete_test.dart"], {"parent"}),
            (["sdks/dart/offline/test/support/traversal_contract.dart"],
             {"offline", "sqlite", "mobile", "full"}),
            (["sdks/dart/offline_sqlite/test/support/confirmation_wire.dart"],
             {"sqlite", "mobile", "full"}),
            (["sdks/dart/lib/transport.dart"], ci_scope.JOBS),
            (["proto/graph/v1/service.proto"], ci_scope.JOBS),
            (["sdks/dart/example/lib/view.dart"], {"sqlite", "mobile", "full"}),
            (["buf.gen.yaml"], ci_scope.JOBS),
            (["docs/decisions/0001-dart-mobile-transport.md"], set()),
            ([".github/workflows/unmapped.yml"], ci_scope.JOBS),
        ):
            with self.subTest(paths=paths):
                relevant = {p: semantic[p] for p in paths if p in semantic}
                change = ci_scope.validation_scope.affected(dict.fromkeys(paths, "M"), relevant)
                self.assertEqual(ci_scope.selected_jobs(dict.fromkeys(paths, "M"), change)[0], expected)
        for path, owners, expected in (
            ("server/go.mod", {"server"}, ci_scope.WIRE),
            ("mcp/go.mod", {"mcp"}, set()),
            ("sdks/dart/offline/pubspec.lock", set(), {"offline", "sqlite", "mobile", "full"}),
            ("admin/bun.lock", set(), set()),
        ):
            with self.subTest(path=path):
                kind = "go-dependency" if owners else "package-dependency"
                change = ci_scope.validation_scope.affected({path: "M"}, {path: kind}, owners)
                self.assertEqual(ci_scope.selected_jobs({path: "M"}, change)[0], expected)
        unknown = ci_scope.validation_scope.full("classification unavailable: ValueError")
        self.assertEqual(ci_scope.selected_jobs({"go.sum": "M"}, unknown)[0], ci_scope.JOBS)

    def test_example_lock_preflight_precedes_all_consumers(self):
        workflow = (Path(__file__).resolve().parents[3] / ci_scope.DART_WORKFLOW).read_text()
        changes = workflow.split("  changes:\n", 1)[1].split("  documentation:\n", 1)[0]
        condition = "if: steps.scope.outputs.example_lock == 'true'"
        setup, resolve = changes.split("      - name: Check frozen example dependencies", 1)
        self.assertIn(condition, setup.split("      - name: Set up Flutter for example", 1)[1])
        self.assertIn(condition, resolve.split("      - name:", 1)[0])
        self.assertIn("working-directory: sdks/dart/example\n        run: flutter pub get --enforce-lockfile", resolve)
        for name, flag in (("android", "mobile"), ("ios", "mobile"), ("offline-sqlite", "sqlite")):
            consumer = workflow.split(f"  {name}:\n", 1)[1].split("    steps:\n", 1)[0]
            self.assertIn("    needs: changes\n", consumer)
            self.assertEqual(release_condition(name), f"needs.changes.outputs.{flag} == 'true'")

    def test_documentation_gate_checks_actual_results_and_classification_failure(self):
        workflow = Path(__file__).resolve().parents[3] / '.github/workflows/dart-sdk.yml'
        gate = workflow.read_text().split('  gate:\n')[1].split('  release-preflight:\n')[0]
        script = gate.split('        run: |\n')[1]
        script = '\n'.join(line[10:] for line in script.splitlines())
        with tempfile.TemporaryDirectory() as directory:
            env = os.environ.copy()
            env.update(FULL='false', MOBILE='false', EVIDENCE_ONLY='false',
                       DOCS_ONLY='true', DOCS_RESULT='success', CHANGES_RESULT='success',
                       GITHUB_STEP_SUMMARY=str(Path(directory) / 'summary'))
            for name in ('EVIDENCE_RESULT', 'TEST_RESULT', 'MINIMUM_DART_RESULT',
                         'OFFLINE_TEST_RESULT', 'OFFLINE_MINIMUM_DART_RESULT',
                         'OFFLINE_SQLITE_RESULT', 'ANDROID_RESULT', 'IOS_RESULT'):
                env[name] = 'skipped'
            self.assertEqual(subprocess.run(['bash', '-c', script], env=env,
                                           capture_output=True).returncode, 0)
            for name in ('CHANGES_RESULT', 'DOCS_RESULT', 'TEST_RESULT', 'IOS_RESULT'):
                for result in ('failure', 'cancelled'):
                    failed = dict(env, **{name: result})
                    self.assertNotEqual(subprocess.run(['bash', '-c', script], env=failed,
                                                       capture_output=True).returncode, 0)

    def test_source_gate_keeps_package_native_and_paired_jobs_mandatory(self):
        workflow = Path(__file__).resolve().parents[3] / '.github/workflows/dart-sdk.yml'
        gate = workflow.read_text().split('  gate:\n')[1].split('  release-preflight:\n')[0]
        script = '\n'.join(line[10:] for line in gate.split('        run: |\n')[1].splitlines())
        with tempfile.TemporaryDirectory() as directory:
            env = dict(os.environ, FULL='true', MOBILE='true', PARENT='true', OFFLINE='true', SQLITE='true', EVIDENCE_ONLY='false', DOCS_ONLY='false',
                       DOCS_RESULT='skipped', EVIDENCE_RESULT='skipped',
                       GITHUB_STEP_SUMMARY=str(Path(directory) / 'summary'))
            required = ('CHANGES_RESULT', 'TEST_RESULT', 'MINIMUM_DART_RESULT', 'OFFLINE_TEST_RESULT',
                        'OFFLINE_MINIMUM_DART_RESULT', 'OFFLINE_SQLITE_RESULT', 'ANDROID_RESULT', 'IOS_RESULT')
            env.update(dict.fromkeys(required, 'success'))
            self.assertEqual(subprocess.run(['bash', '-c', script], env=env, capture_output=True).returncode, 0)
            for name in required:
                for result in ('failure', 'cancelled', 'skipped'):
                    with self.subTest(name=name, result=result):
                        self.assertNotEqual(subprocess.run(['bash', '-c', script],
                            env=dict(env, **{name: result}), capture_output=True).returncode, 0)

    def test_pr_and_main_route_every_diff_through_classifier(self):
        workflow = Path(__file__).resolve().parents[3] / ".github/workflows/dart-sdk.yml"
        events = workflow.read_text().split("permissions:", 1)[0]
        self.assertIn("  pull_request:\n", events)
        self.assertIn("    branches: [main]", events)
        self.assertNotRegex(events, r"(?m)^\s+paths(?:-ignore)?:")
        self.assertEqual(ci_scope.selected_jobs({}, ci_scope.validation_scope.full("empty"))[0], ci_scope.JOBS)

    def test_partial_scope_refuses_extra_or_missing_jobs(self):
        path = Path(__file__).resolve().parents[3] / ".github/workflows/dart-sdk.yml"
        gate = path.read_text().split("  gate:\n")[1].split("  release-preflight:\n")[0]
        script = "\n".join(line[10:] for line in gate.split("        run: |\n")[1].splitlines())
        with tempfile.TemporaryDirectory() as directory:
            env = dict(os.environ, DOCS_ONLY="false", EVIDENCE_ONLY="false",
                       CHANGES_RESULT="success", DOCS_RESULT="skipped", EVIDENCE_RESULT="skipped",
                       FULL="true", MOBILE="false", PARENT="true", OFFLINE="true", SQLITE="false",
                       TEST_RESULT="success", MINIMUM_DART_RESULT="success",
                       OFFLINE_TEST_RESULT="success", OFFLINE_MINIMUM_DART_RESULT="success",
                       OFFLINE_SQLITE_RESULT="skipped", ANDROID_RESULT="skipped", IOS_RESULT="skipped",
                       GITHUB_STEP_SUMMARY=str(Path(directory) / "summary"))
            self.assertEqual(subprocess.run(["bash", "-c", script], env=env, capture_output=True).returncode, 0)
            for job, wrong in (("OFFLINE_TEST_RESULT", "skipped"), ("IOS_RESULT", "success"),
                               ("TEST_RESULT", "cancelled"), ("OFFLINE_SQLITE_RESULT", "failure")):
                with self.subTest(job=job):
                    self.assertNotEqual(subprocess.run(["bash", "-c", script], env={**env, job: wrong},
                                                       capture_output=True).returncode, 0)

    def test_event_routing_manual_tag_evidence_and_unavailable_base(self):
        example = dict.fromkeys(ci_scope.EXAMPLE_DEPENDENCIES, "M")
        semantic = dict.fromkeys(example, "package-dependency")
        consumers = {"mobile", "sqlite", "full"}
        for event, ref, ranges, paths, evidence, expected, profile in (
            ("pull_request", "refs/pull/1/merge", ("a", "b"), example, False,
             consumers | {"example_lock"}, "unit-contracts"),
            ("push", "refs/heads/main", ("a", "b"), {**example, "README.md": "M"}, False,
             consumers | {"example_lock"}, "unit-contracts"),
            ("push", "refs/heads/main", ("a", "b"), {**example, "sdks/dart/example/lib/view.dart": "M"},
             False, consumers, "unit-contracts"),
            ("workflow_dispatch", "refs/heads/main", None, {}, False, ci_scope.JOBS, "unit-contracts"),
            ("push", "refs/tags/sdks/dart/offline/v0.6.0", None, {}, False, ci_scope.JOBS, "full"),
            ("pull_request", "refs/pull/1/merge", ("a", "b"),
             dict.fromkeys(ci_scope.evidence_only_pr.REQUIRED, "A"), True, set(), "unit-contracts"),
            ("push", "refs/heads/main", None, {}, False, ci_scope.JOBS, "unit-contracts"),
        ):
            with self.subTest(event=event, ref=ref), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / "output"
                with patch.dict(os.environ, GITHUB_REF=ref, GITHUB_EVENT_NAME=event,
                                GITHUB_OUTPUT=str(output), GITHUB_STEP_SUMMARY=str(Path(directory) / "summary")), \
                     patch.object(ci_scope.ci_docs, "change_range", return_value=ranges), \
                     patch.object(ci_scope.evidence_only_pr, "changed_files", return_value=paths), \
                     patch.object(ci_scope.validation_scope, "classify_revisions",
                                  return_value=ci_scope.validation_scope.affected(paths, semantic)):
                    ci_scope.main()
                flags = dict(line.split("=") for line in output.read_text().splitlines())
                self.assertEqual({k for k in ci_scope.JOBS | {"example_lock"} if flags[k] == "true"}, expected)
                self.assertEqual(flags["resource_profile"], profile)
                self.assertEqual(flags["evidence_only"], str(evidence).lower())


if __name__ == "__main__":
    unittest.main()
