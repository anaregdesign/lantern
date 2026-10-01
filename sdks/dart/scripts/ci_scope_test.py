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
    parsed = ast.parse(expression.replace('&&', ' and ').replace(
        '!cancelled()', 'not cancelled()'), mode='eval')

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
        if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.Not):
            return not value(node.operand)
        if isinstance(node, ast.Compare) and len(node.ops) == 1 and isinstance(node.ops[0], ast.Eq):
            return value(node.left) == value(node.comparators[0])
        if isinstance(node, ast.Call) and isinstance(node.func, ast.Name):
            if node.func.id == 'cancelled' and not node.args:
                return canceled
            if node.func.id == 'startsWith' and len(node.args) == 2:
                return value(node.args[0]).startswith(value(node.args[1]))
        raise AssertionError(f'unsupported release expression: {ast.dump(node)}')

    return value(parsed.body)


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
                    context = {'needs': {'gate': {'result': result}},
                               'github': {'event_name': event, 'ref': ref}}
                    self.assertEqual(release_job_runs(expression, context, canceled=canceled), expected)

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
    def test_path_table(self):
        cases = {
            "documentation": (["AGENTS.md", "README.md"], False, (True, False)),
            "Dart package documentation": (
                ["sdks/dart/README.md"],
                False,
                (True, False),
            ),
            "Flutter example documentation": (
                ["sdks/dart/example/physical-device-smoke.md"],
                False,
                (True, False),
            ),
            "physical evidence": (
                ["sdks/dart/example/evidence/2026-09-24/ios.json"],
                False,
                (True, False),
            ),
            "publish selection": (["sdks/dart/.pubignore"], False, (True, False)),
            "release helper": (["sdks/dart/scripts/release.py"], False, (True, False)),
            "Go dependency": (["go.mod", "go.sum"], False, (True, False)),
            "submodule Go dependency": (
                ["core/go.mod", "core/go.sum"],
                False,
                (False, False),
            ),
            "Go toolchain": (["go.mod", "go.work"], True, (True, True)),
            "submodule Go toolchain": (["pb/go.mod"], True, (True, True)),
            "backend": (["server/service/service.go"], False, (False, False)),
            "Dart runtime": (["sdks/dart/lib/src/client.dart"], False, (True, True)),
            "Flutter example": (
                ["sdks/dart/example/lib/main.dart"],
                False,
                (True, True),
            ),
            "SQLite adapter": (
                ["sdks/dart/offline_sqlite/lib/store.dart"],
                False,
                (True, True),
            ),
            "proto": (["proto/graph/v1/lantern.proto"], False, (True, True)),
            "codegen": (["buf.gen.yaml"], False, (True, True)),
            "native CI helper": (
                ["sdks/dart/example/tool/ios_smoke_attach.py"],
                False,
                (True, True),
            ),
            "scope classifier": (
                ["sdks/dart/scripts/ci_scope.py"],
                False,
                (True, True),
            ),
            "mobile contract": (
                ["docs/decisions/0001-dart-mobile-transport.md"],
                False,
                (True, True),
            ),
            "workflow": ([".github/workflows/dart-sdk.yml"], False, (True, True)),
            "host integration test": (
                ["tests/integration/dart_offline_sqlite_test.dart"],
                False,
                (True, False),
            ),
            "identity host integration and fixture": (
                [
                    "tests/integration/dart_identity_offline_sqlite_test.dart",
                    "testbed/scripts/dart_identity_fixture.sh",
                ],
                False,
                (True, False),
            ),
        }
        for name, (paths, toolchain_changed, expected) in cases.items():
            with self.subTest(name):
                self.assertEqual(
                    ci_scope.classify(paths, toolchain_changed=toolchain_changed),
                    expected,
                )

    def test_release_tag_requires_both_gates(self):
        self.assertEqual(ci_scope.classify([], release_tag=True), (True, True))

    def test_workflow_dispatch_requires_both_gates_without_push_range(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "output"
            summary = Path(directory) / "summary"
            with (
                patch.dict(
                    os.environ,
                    {
                        "GITHUB_REF": "refs/heads/main",
                        "GITHUB_EVENT_NAME": "workflow_dispatch",
                        "GITHUB_OUTPUT": str(output),
                        "GITHUB_STEP_SUMMARY": str(summary),
                    },
                    clear=True,
                ),
                patch.object(
                    ci_scope,
                    "classify_range",
                    side_effect=AssertionError("manual dispatch has no push range"),
                ),
            ):
                ci_scope.main()
            self.assertEqual(output.read_text(), "full=true\nmobile=true\nevidence_only=false\n")
            self.assertIn(
                "Full Dart package matrix: True; Android/iOS native matrix: True",
                summary.read_text(),
            )

    def test_evidence_only_pr_skips_package_and_mobile_jobs(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "output"
            summary = Path(directory) / "summary"
            env = {
                "GITHUB_REF": "refs/pull/123/merge",
                "GITHUB_EVENT_NAME": "pull_request",
                "PR_BASE_SHA": "a" * 40,
                "PR_HEAD_SHA": "b" * 40,
                "GITHUB_OUTPUT": str(output),
                "GITHUB_STEP_SUMMARY": str(summary),
            }
            with (
                patch.dict(os.environ, env, clear=True),
                patch.object(ci_scope, "git", return_value=b"a" * 40),
                patch.object(ci_scope, "classify_range", return_value=(True, False)),
                patch.object(ci_scope.evidence_only_pr, "classify_range", return_value=True),
            ):
                ci_scope.main()
            self.assertEqual(output.read_text(), "full=false\nmobile=false\nevidence_only=true\n")
            self.assertIn("evidence-only PR: True", summary.read_text())

    def test_go_directives_ignore_dependency_changes(self):
        old = "module example.com/test\n\ngo 1.27.0\n\nrequire example.com/a v1.0.0\n"
        dependency_update = old.replace("v1.0.0", "v1.1.0")
        toolchain_update = old.replace("go 1.27.0", "go 1.28.0")
        self.assertEqual(
            ci_scope.go_directives(old), ci_scope.go_directives(dependency_update)
        )
        self.assertNotEqual(
            ci_scope.go_directives(old), ci_scope.go_directives(toolchain_update)
        )

    def test_git_range_distinguishes_go_dependency_from_toolchain(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)

            def git(*args):
                return (
                    subprocess.check_output(["git", "-C", str(root), *args])
                    .decode()
                    .strip()
                )

            git("init", "-q")
            git("config", "user.email", "ci@example.test")
            git("config", "user.name", "CI")
            go_mod = root / "go.mod"
            go_mod.write_text(
                "module example.com/test\n\ngo 1.27.0\n\nrequire example.com/a v1.0.0\n"
            )
            git("add", "go.mod")
            git("commit", "-qm", "initial")
            base = git("rev-parse", "HEAD")

            go_mod.write_text(go_mod.read_text().replace("v1.0.0", "v1.1.0"))
            git("commit", "-qam", "dependency update")
            dependency_head = git("rev-parse", "HEAD")
            go_mod.write_text(go_mod.read_text().replace("go 1.27.0", "go 1.28.0"))
            git("commit", "-qam", "toolchain update")
            toolchain_head = git("rev-parse", "HEAD")

            submodule = root / "core"
            submodule.mkdir()
            submodule_mod = submodule / "go.mod"
            submodule_mod.write_text("module example.com/core\n\ngo 1.27.0\n")
            git("add", "core/go.mod")
            git("commit", "-qm", "add submodule")
            submodule_base = git("rev-parse", "HEAD")
            submodule_mod.write_text("module example.com/core\n\ngo 1.28.0\n")
            git("commit", "-qam", "submodule toolchain update")
            submodule_head = git("rev-parse", "HEAD")

            dart_source = root / "sdks" / "dart" / "lib" / "client.dart"
            dart_source.parent.mkdir(parents=True)
            dart_source.write_text("void main() {}\n")
            git("add", "sdks/dart/lib/client.dart")
            git("commit", "-qm", "add Dart source")
            rename_base = git("rev-parse", "HEAD")
            (root / "moved").mkdir()
            git("mv", "sdks/dart/lib/client.dart", "moved/client.dart")
            git("commit", "-qm", "move Dart source away")
            rename_head = git("rev-parse", "HEAD")

            original_directory = Path.cwd()
            try:
                os.chdir(root)
                self.assertEqual(
                    ci_scope.classify_range(base, dependency_head), (True, False)
                )
                self.assertEqual(
                    ci_scope.classify_range(dependency_head, toolchain_head),
                    (True, True),
                )
                self.assertEqual(
                    ci_scope.classify_range(submodule_base, submodule_head),
                    (True, True),
                )
                self.assertEqual(
                    ci_scope.classify_range(rename_base, rename_head), (True, True)
                )
            finally:
                os.chdir(original_directory)


if __name__ == "__main__":
    unittest.main()
