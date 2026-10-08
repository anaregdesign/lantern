"""Exercise real git ranges and the checked-in workflow routing contracts."""

import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import textwrap
import unittest
from unittest.mock import patch

import ci_docs


ROOT = Path(__file__).resolve().parents[2]


def workflow_paths(name, event):
    text = (ROOT / f".github/workflows/{name}.yml").read_text()
    body = re.search(rf"(?ms)^  {event}:\n(.*?)(?=^  \w|^\w|\Z)", text).group(1)
    lines = body.splitlines()
    start = lines.index("    paths:") + 1
    result = []
    for line in lines[start:]:
        if not line.startswith("      - "):
            break
        result.append(line[8:].strip().strip("\"'"))
    return result


def matches(pattern, path):
    # Patterns used here have only *, ** and literal path segments. Match
    # GitHub's distinction between a single segment and recursive globs.
    regex = re.escape(pattern).replace(r"\*\*/", "(?:.*/)?")
    regex = regex.replace(r"\*\*", ".*").replace(r"\*", "[^/]*")
    return re.fullmatch(regex, path) is not None


def triggers(patterns, paths):
    for path in paths:
        included = False
        for pattern in patterns:
            if matches(pattern.lstrip("!"), path):
                included = not pattern.startswith("!")
        if included:
            return True
    return False


class DocumentationTest(unittest.TestCase):
    def test_conservative_allowlist(self):
        for path in ("README.md", "CONTRIBUTING.md", "docs/ha-runbook.md",
                     "docs/new-guide.md", "sdks/dart/offline/README.md",
                     "admin/README.md", "sdks/rust/CHANGELOG.md"):
            with self.subTest(path=path):
                self.assertTrue(ci_docs.documentation_only([path]))
        for path in (*ci_docs.CONTRACT_DOCS, "server/service/service.go",
                     "testdata/search/README.md", "sdks/dart/test/fixture.md",
                     "sdks/rust/src/generated/README.md", "unknown/README.md",
                     "deploy/helm/lantern/templates/NOTES.txt", "go.mod",
                     ".github/workflows/go.yml", ".github/scripts/ci_docs.py",
                     "../README.md", "/README.md", "docs/../README.md"):
            with self.subTest(path=path):
                self.assertFalse(ci_docs.documentation_only([path]))
                self.assertFalse(ci_docs.documentation_only(["README.md", path]))
        self.assertFalse(ci_docs.documentation_only([]))

    def test_events_without_trustworthy_range_keep_full_gates(self):
        for event, ref in (("push", "refs/tags/sdks/dart/v0.4.1"),
                           ("workflow_dispatch", "refs/heads/main"),
                           ("schedule", "refs/heads/main"),
                           ("unknown", "refs/heads/main")):
            self.assertEqual(ci_docs.classify({"GITHUB_EVENT_NAME": event,
                                              "GITHUB_REF": ref}), (False, None))
        self.assertEqual(ci_docs.classify({"GITHUB_EVENT_NAME": "push",
                                          "PUSH_BEFORE_SHA": "0" * 40,
                                          "GITHUB_SHA": "b" * 40}), (False, None))
        with self.assertRaises(KeyError):
            ci_docs.classify({"GITHUB_EVENT_NAME": "pull_request"})

    def test_real_git_range_merge_base_rename_delete_and_validation(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            def git(*args):
                return subprocess.check_output(["git", "-C", directory, *args],
                                               text=True).strip()
            git("init", "-q")
            git("config", "user.name", "CI")
            git("config", "user.email", "ci@example.test")
            (root / "README.md").write_text("original\n")
            (root / "source.go").write_text("package main\n")
            git("add", ".")
            git("commit", "-qm", "base")
            base = git("rev-parse", "HEAD")
            (root / "README.md").write_text("updated\n")
            git("commit", "-qam", "docs")
            head = git("rev-parse", "HEAD")
            # A concurrent base-branch source commit is not in the PR's diff.
            git("checkout", "-qb", "base", base)
            (root / "source.go").write_text("package changed\n")
            git("commit", "-qam", "concurrent base")
            pr_base = git("rev-parse", "HEAD")
            git("checkout", "-q", head)
            previous = os.getcwd()
            os.chdir(root)
            try:
                docs, revisions = ci_docs.classify({"GITHUB_EVENT_NAME": "pull_request",
                    "PR_BASE_SHA": pr_base, "PR_HEAD_SHA": head})
                self.assertTrue(docs)
                self.assertEqual(revisions, (base, head))
                ci_docs.validate(revisions)
                git("mv", "source.go", "AGENTS.md")
                git("commit", "-qm", "source renamed into prose")
                rename_head = git("rev-parse", "HEAD")
                self.assertFalse(ci_docs.classify({"GITHUB_EVENT_NAME": "push",
                    "PUSH_BEFORE_SHA": head, "GITHUB_SHA": rename_head})[0])
                git("rm", "README.md")
                git("commit", "-qm", "remove prose")
                delete_head = git("rev-parse", "HEAD")
                self.assertTrue(ci_docs.classify({"GITHUB_EVENT_NAME": "push",
                    "PUSH_BEFORE_SHA": rename_head, "GITHUB_SHA": delete_head})[0])
                ci_docs.validate((rename_head, delete_head))
                (root / "README.md").symlink_to("source.go")
                git("add", "README.md")
                git("commit", "-qm", "symlink")
                with self.assertRaisesRegex(ValueError, "regular text"):
                    ci_docs.validate((delete_head, git("rev-parse", "HEAD")))
            finally:
                os.chdir(previous)

    def test_invalid_utf8_fails_before_output(self):
        with patch.object(ci_docs, "classify", return_value=(True, ("base", "head"))), \
             patch.object(ci_docs, "validate", side_effect=UnicodeError):
            with self.assertRaises(UnicodeError):
                ci_docs.main()


class WorkflowRoutingTest(unittest.TestCase):
    def test_nonrequired_workflows_do_not_start_for_ordinary_prose(self):
        cases = {
            "codeql": (["README.md", "docs/ha-runbook.md"], ["server/service/service.go"]),
            "dart-sdk": (["README.md", "sdks/dart/CHANGELOG.md", "sdks/dart/example/physical-device-smoke.md"], ["sdks/dart/lib/src/client.dart"]),
            "node-sdk": (["sdks/node/README.md", "server/README.md"], ["sdks/node/src/client.ts"]),
            "rust-sdk": (["sdks/rust/RELEASING.md", "core/README.md"], ["sdks/rust/src/lib.rs"]),
            "admin": (["admin/README.md", "sdks/node/README.md"], ["admin/app/root.tsx"]),
            "dart-transport-probe": (["testbed/dart-transport-probe/README.md"], ["testbed/dart-transport-probe/connect/lib/probe.dart"]),
        }
        for name, (docs, source) in cases.items():
            events = ["pull_request"] if name == "dart-transport-probe" else ["push", "pull_request"]
            for event in events:
                patterns = workflow_paths(name, event)
                with self.subTest(name=name, event=event):
                    self.assertFalse(triggers(patterns, docs))
                    self.assertTrue(triggers(patterns, docs + source))
                    self.assertTrue(triggers(patterns, [f".github/workflows/{name}.yml"]))
        for name, paths in {
            "dart-sdk": [".github/scripts/ci_docs.py", "docs/decisions/0001-dart-mobile-transport.md", "docs/decisions/0002-dart-offline-repository-contract.md", "sdks/dart/test/fixture.md", "sdks/dart/offline/pubspec.yaml"],
            "rust-sdk": ["docs/decisions/0011-native-rust-sdk.md", "docs/rust-sdk-v0.1-contract.md"],
            "codeql": list(ci_docs.CONTRACT_DOCS),
        }.items():
            for path in paths:
                self.assertTrue(triggers(workflow_paths(name, "pull_request"), [path]), (name, path))

    def test_required_workflow_is_unfiltered_and_failure_remains_blocking(self):
        text = (ROOT / ".github/workflows/go.yml").read_text()
        triggers_text = text.split("jobs:")[0]
        self.assertNotIn("paths:", triggers_text)
        self.assertNotIn("paths-ignore:", triggers_text)
        for name in ("Build & Test", "Lint", "Proto (buf)", "govulncheck"):
            self.assertIn(f"name: {name}\n", text)
        build = text.split("  build:\n")[1].split("  receipt-backup-windows:")[0]
        self.assertIn("    if: always()\n", build)
        self.assertIn('test "$SCOPE_RESULT" = success', build)
        for job in ("lint", "proto", "govulncheck", "receipt-backup-windows", "fuzz"):
            body = re.search(rf"(?ms)^  {job}:\n(.*?)(?=^  [\w-]+:\n|\Z)", text).group(1)
            condition = next(line for line in body.splitlines() if line.startswith("    if:"))
            self.assertIn("needs.scope.outputs.docs_only != 'true'", condition)


class AdminCaddyGateTest(unittest.TestCase):
    def test_existing_ci_step_requires_real_test_success_without_skips(self):
        # Exercise the actual CI shell/Python step with synthetic suites. These
        # fixtures qualify the gate control, not real Caddy route behavior.
        workflow = (ROOT / '.github/workflows/admin.yml').read_text()
        step = workflow.split('      - name: Verify fixed API and protected diagnostics routes\n', 1)[1]
        step = step.split('      - name: Setup Bun\n', 1)[0]
        script = textwrap.dedent(step.split('        run: |\n', 1)[1])
        required = re.search(r"required = '([^']+)'", script).group(1)
        container_tests = str(ROOT / 'admin/tests/container')
        pending = list(unittest.TestLoader().discover(container_tests, pattern='*_test.py',
                                                     top_level_dir=container_tests))
        actual_ids = set()
        while pending:
            test = pending.pop()
            if isinstance(test, unittest.TestSuite):
                pending.extend(test)
            else:
                actual_ids.add(test.id())
        self.assertIn(required, actual_ids)

        def fixture(body, extra=''):
            return ('import unittest\n\nclass EntrypointTest(unittest.TestCase):\n'
                    '    def test_real_caddy_route_and_operations_admission(self):\n'
                    f'        {body}\n{extra}')

        cases = (
            ('pass', fixture('self.assertTrue(True)'), 0, 0),
            ('missing-suite', '', 0, 1),
            ('missing-real-test', fixture('pass').replace(
                'test_real_caddy_route_and_operations_admission', 'test_other'), 0, 1),
            ('real-test-skipped', fixture("self.skipTest('fixture skip')"), 0, 1),
            ('other-test-skipped', fixture('pass',
                "    def test_other(self):\n        self.skipTest('other skip')\n"), 0, 1),
            ('failure', fixture("self.fail('fixture failure')"), 0, 1),
            ('error', fixture("raise RuntimeError('fixture error')"), 0, 1),
            ('expected-failure', fixture("self.fail('expected failure')").replace(
                '    def test_real', '    @unittest.expectedFailure\n    def test_real'), 0, 1),
            ('image-pull-failed', fixture('pass'), 1, 1),
        )
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            tests = root / 'admin/tests/container'
            tests.mkdir(parents=True)
            docker = root / 'docker'
            docker.write_text(f'#!{sys.executable}\n' + textwrap.dedent('''\
                import json
                import os
                from pathlib import Path
                import sys
                args = sys.argv[1:]
                Path(os.environ['DOCKER_CAPTURE']).write_text(json.dumps(args))
                if args != ['pull', 'caddy:2.10-alpine']:
                    sys.exit(2)
                sys.exit(int(os.environ['DOCKER_PULL_EXIT']))
                '''))
            docker.chmod(0o755)
            for name, source, pull_exit, expected_exit in cases:
                with self.subTest(case=name):
                    (tests / 'entrypoint_test.py').write_text(source)
                    env = os.environ | {
                        'PATH': f'{root}{os.pathsep}{os.environ["PATH"]}',
                        'DOCKER_CAPTURE': str(root / 'docker-args.json'),
                        'DOCKER_PULL_EXIT': str(pull_exit),
                    }
                    result = subprocess.run(
                        ['bash', '--noprofile', '--norc', '-eo', 'pipefail', '-c', script],
                        cwd=root, env=env, capture_output=True, text=True)
                    self.assertEqual(result.returncode, expected_exit, result.stderr)
                    self.assertEqual(json.loads((root / 'docker-args.json').read_text()),
                                     ['pull', 'caddy:2.10-alpine'])
                    if name == 'pass':
                        self.assertIn('test_real_caddy_route_and_operations_admission',
                                      result.stderr)
                        self.assertIn('Ran 1 test', result.stderr)
                        self.assertIn('\nOK\n', result.stderr)
                    elif name.startswith('missing-'):
                        self.assertIn('Required real Caddy test was not discovered', result.stderr)
                    elif name == 'image-pull-failed':
                        self.assertNotIn('Ran ', result.stderr)
                    else:
                        self.assertIn('Caddy route gate requires all tests to pass with zero skips',
                                      result.stderr)


class AdminReleaseGuidanceTest(unittest.TestCase):
    def test_generated_create_and_edit_notes_preserve_routing_contract(self):
        # Execute the actual release step, with gh replaced by a local capture.
        # No GitHub write, image publication, or release tag is performed.
        workflow = (ROOT / '.github/workflows/admin-publish.yml').read_text()
        step = workflow.split('      - name: Create or update GitHub Release\n', 1)[1]
        script = textwrap.dedent(step.split('        run: |\n', 1)[1])
        tag = 'admin/v9.8.7'
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            gh = root / 'gh'
            gh.write_text(f'#!{sys.executable}\n' + textwrap.dedent('''\
                import json
                import os
                from pathlib import Path
                import sys
                args = sys.argv[1:]
                if args[:2] == ['release', 'view']:
                    sys.exit(0 if os.environ['RELEASE_EXISTS'] == 'yes' else 1)
                if args[:2] not in (['release', 'create'], ['release', 'edit']):
                    sys.exit(2)
                root = Path(os.environ['GH_CAPTURE'])
                notes = Path(args[args.index('--notes-file') + 1]).read_text()
                (root / 'notes.md').write_text(notes)
                (root / 'args.json').write_text(json.dumps(args))
                '''))
            gh.chmod(0o755)
            for exists, action in (('no', 'create'), ('yes', 'edit')):
                with self.subTest(action=action):
                    env = os.environ | {
                        'PATH': f'{root}{os.pathsep}{os.environ["PATH"]}',
                        'TAG': tag, 'REPO': 'anaregdesign/lantern',
                        'IMAGE': 'ghcr.io/anaregdesign/lantern-admin',
                        'GH_TOKEN': 'fixture-only', 'GH_CAPTURE': str(root),
                        'RELEASE_EXISTS': exists, 'TMPDIR': str(root),
                    }
                    result = subprocess.run(['bash', '-c', script], env=env,
                                            capture_output=True, text=True)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    args = json.loads((root / 'args.json').read_text())
                    self.assertEqual(args[:3], ['release', action, tag])
                    self.assertIn('--verify-tag', args)
                    if action == 'create':
                        self.assertEqual(args[args.index('--title') + 1], tag)
                    notes = (root / 'notes.md').read_text()
                    for contract in (
                        'ghcr.io/anaregdesign/lantern-admin:v9.8.7',
                        '### OFF: direct Server with CORS',
                        'LANTERN_CORS_ALLOWED_ORIGINS=http://localhost:8080',
                        '### OIDC: HTTPS same-origin routes',
                        'select that exact origin',
                        'LANTERN_OIDC_BROWSER_ORIGIN=https://admin.example.com:8443',
                        'LANTERN_OIDC_REDIRECT_URI', '/auth/callback/<SHA-256>',
                        '/auth/*', '/browser/*', '/graph.v1.*/*',
                        'LANTERN_ADMIN_SERVER_UPSTREAM', 'scheme://hostname:port',
                        'https://writer.internal.example:6380',
                        'gateway picker never selects that upstream',
                        'Verify the upstream certificate against its hostname',
                        'LANTERN_ADMIN_SERVER_CA_FILE', 'public Host/scheme',
                        'LANTERN_OIDC_TRUSTED_PROXY_IPS',
                        'explicitly trusted exact gateway IP',
                        'current fixed-writer baseline',
                        'Future eligible-node routing', '#1608/#1609 S5 work',
                        'Missing upstream routes fail closed', '/auth/operations',
                        'operations.read', 'Only GET diagnostics',
                        'Prometheus never receives cookies or Authorization',
                        'browser local storage and Vite env',
                    ):
                        self.assertIn(contract, notes)
                    for path in ('docs/oidc-operations.md#browser-and-diagnostics-boundary',
                                 'docs/ha-runbook.md', 'server/README.md'):
                        self.assertIn(f'https://github.com/anaregdesign/lantern/blob/{tag}/{path}',
                                      notes)
                        self.assertTrue((ROOT / path.split('#')[0]).is_file())
                    self.assertIn('## Browser and diagnostics boundary',
                                  (ROOT / 'docs/oidc-operations.md').read_text())
                    for retired in ('does not reverse-proxy', 'LANTERN_PEERS',
                                    'tls_insecure_skip_verify', 'fixture-only', '${REPO}'):
                        self.assertNotIn(retired, notes)


if __name__ == "__main__":
    unittest.main()
