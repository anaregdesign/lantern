"""Real Git ranges and synthetic subprocesses verify local eligibility/fallback."""

from contextlib import redirect_stdout
import io
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import local_gate_session as gate
import local_prose_gate as prose
from local_gate_plan import Step


class ProseTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / "repo"
        self.root.mkdir()
        self.git("init", "-qb", "feature")
        self.git("config", "user.email", "prose@example.invalid")
        self.git("config", "user.name", "Prose fixture")
        for name in ("local_prose_gate.py", "local_gate_plan.py", "ci_docs.py", "evidence_only_pr.py"):
            self.write(".github/scripts/" + name, Path(prose.__file__).with_name(name).read_text())
        for directory, _ in prose.TEST_SUITES:
            self.write(directory + "/test_stub_test.py", "import unittest\nclass Fixture(unittest.TestCase):\n def test_pass(self): self.assertTrue(True)\n")
        self.write("README.md", "Explanation with `stable` literal.\n\n```sh\ngo test ./...\n```\n")
        self.write("core/README.md", "Core overview\n")
        self.write("source.go", "package main\n")
        self.write("docs/security-current-authority.md", "canonical contract\n")
        self.commit()
        self.base = self.git("rev-parse", "HEAD")
        self.git("update-ref", "refs/remotes/origin/main", self.base)
        self.session = gate.Session(self.root, Path(self.temp.name) / "evidence")
        self.session.env.pop("GITHUB_ACTIONS", None)
        self.session.context = lambda: "fixture-context"
        self.session.plan = (Step("fixture-full", ".", (sys.executable, "-c", "print('full fixture')")),)
        self.write("README.md", "Clear explanation with `stable` literal.\n\n```sh\ngo test ./...\n```\n")
        self.commit()

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.root, text=True, stderr=subprocess.DEVNULL).strip()

    def write(self, path, content):
        target = self.root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)

    def commit(self):
        self.git("add", "--all")
        self.git("commit", "-qm", "fixture")

    def qualify(self):
        with redirect_stdout(io.StringIO()):
            return prose.qualify(self.session, "origin/main")

    def test_fresh_prose_path_runs_all_suites_and_never_touches_go_or_carry(self):
        with patch.object(self.session, "context", side_effect=AssertionError("must not inspect Go")):
            result = self.qualify()
        self.assertTrue(result["passed"], result)
        self.assertEqual((result["executed"], result["carried"], result["required"]), (5, 0, 5))
        self.assertFalse(self.session.receipts)
        self.assertIsNone(self.session.prior_tree)
        self.assertEqual(result["base"], self.base)
        for item in result["steps"][1:]:
            self.assertIn("Ran 1 test", Path(item["log"]).read_text())

    def test_mixed_or_contract_or_unknown_inputs_require_full(self):
        for path in ("source.go", "docs/security-current-authority.md", "docs/new-guide.md",
                     "AGENTS.md", "CONTRIBUTING.md", ".github/workflows/go.yml", "go.mod",
                     "testdata/README.md", "pb/README.md", "testbed/bench/README.md", "sdks/rust/RELEASING.md",
                     "sdks/dart/test/fixture.md", "sdks/dart/lib/src/gen/README.md"):
            with self.subTest(path=path):
                self.write(path, "changed\n")
                self.commit()
                self.assertIsNone(self.qualify())
                self.git("reset", "--hard", self.base)
                self.write("README.md", "prose edit\n\n```sh\ngo test ./...\n```\n")
                self.commit()

    def test_empty_add_delete_rename_modes_and_binary_require_full(self):
        for kind in ("empty", "add", "delete", "rename", "mode", "binary", "utf8", "symlink"):
            with self.subTest(kind=kind):
                self.git("reset", "--hard", self.base)
                if kind == "add": self.write("core/CHANGELOG.md", "added\n")
                elif kind == "delete": self.git("rm", "README.md")
                elif kind == "rename": self.git("mv", "source.go", "core/CHANGELOG.md")
                elif kind == "mode": (self.root / "README.md").chmod(0o755)
                elif kind == "binary": (self.root / "README.md").write_bytes(b"bad\0bytes")
                elif kind == "utf8": (self.root / "README.md").write_bytes(b"bad\xff")
                elif kind == "symlink":
                    (self.root / "README.md").unlink()
                    (self.root / "README.md").symlink_to("source.go")
                if kind != "empty": self.commit()
                self.assertIsNone(self.qualify())

    def test_stale_or_hidden_base_main_detached_tag_ci_and_dirty_require_full(self):
        self.assertRaises(ValueError, prose.select, self.session, "HEAD")
        for kind in ("main", "detached", "tag", "ci", "dirty", "hidden-dirty", "missing"):
            with self.subTest(kind=kind):
                head = self.git("rev-parse", "HEAD")
                if kind == "main": self.git("branch", "-m", "main")
                elif kind == "detached": self.git("checkout", "--detach", "-q")
                elif kind == "tag": self.git("tag", "v0.0.0")
                elif kind == "ci": self.session.env["GITHUB_ACTIONS"] = "true"
                elif kind == "hidden-dirty": self.git("update-index", "--assume-unchanged", "source.go")
                if kind in {"dirty", "hidden-dirty"}: self.write("source.go", "hidden change\n")
                if kind == "missing": (self.root / "source.go").unlink()
                self.assertIsNone(self.qualify())
                self.session.env.pop("GITHUB_ACTIONS", None)
                if kind == "main": self.git("branch", "-m", "feature")
                elif kind == "detached": self.git("checkout", "-q", "feature")
                elif kind == "tag": self.git("tag", "-d", "v0.0.0")
                elif kind == "hidden-dirty": self.git("update-index", "--no-assume-unchanged", "source.go")
                self.git("reset", "--hard", head)

    def test_code_literals_fences_and_html_require_full(self):
        for text in ("Changed `other` literal.\n", "```sh\ngo build ./...\n```\n", "<div>changed</div>\n", "``a`b``\n", "```unclosed\n"):
            with self.subTest(text=text):
                self.write("README.md", text)
                self.commit()
                self.assertIsNone(self.qualify())
        self.assertNotEqual(prose.literals("``a`b``\n"), prose.literals("``a`c``\n"))

    def test_html_block_bodies_and_ambiguous_markup_route_to_full(self):
        for before, after in (
            ("<pre>\nLANTERN_AUTH_MODE=oidc\n</pre>\n", "<pre>\nLANTERN_AUTH_MODE=off\n</pre>\n"),
            ("<pre\n class='example'>\noidc\n</pre>\n", "<pre\n class='example'>\noff\n</pre>\n"),
            ("<pre>\noidc\n", "<pre>\noff\n"),
            ("<!--\noidc\n-->\n", "<!--\noff\n-->\n"),
            ("<pre>\nstable\n</pre>\nExplanation\n", "<pre>\nstable\n</pre>\nClear explanation\n"),
        ):
            with self.subTest(before=before, after=after):
                self.git("reset", "--hard", self.base)
                self.write("README.md", before)
                self.commit()
                self.git("update-ref", "refs/remotes/origin/main", self.git("rev-parse", "HEAD"))
                self.write("README.md", after)
                self.commit()
                with patch.object(sys, "argv", ["gate", str(self.root), str(self.session.evidence), "--prose-base", "origin/main"]), \
                     patch.object(gate, "Session", return_value=self.session), redirect_stdout(io.StringIO()) as output:
                    self.assertEqual(gate.main(), 0)
                self.assertIn("FULL REQUIRED: prose eligibility refused: HTML or angle markup", output.getvalue())
                result = __import__("json").loads(sorted(self.session.evidence.glob("*-manifest.json"))[-1].read_text())
                self.assertEqual([item["name"] for item in result["steps"]], ["fixture-full"])
                self.assertEqual((result["executed"], result["carried"]), (1, 0))

    def test_classifier_error_routes_to_actual_full_control(self):
        self.session.plan = (Step("go-test-core", ".", (sys.executable, "-c", "print('full fixture')")),)
        with redirect_stdout(io.StringIO()):
            self.assertTrue(self.session.qualify()["passed"])
        self.assertTrue(self.session.receipts)
        with patch.object(prose.ci_docs, "documentation_only", side_effect=RuntimeError("broken classifier")), \
             patch.object(sys, "argv", ["gate", str(self.root), str(self.session.evidence), "--prose-base", "origin/main"]), \
             patch.object(gate, "Session", return_value=self.session), redirect_stdout(io.StringIO()):
            self.assertEqual(gate.main(), 0)
        result = __import__("json").loads((self.session.evidence / "002-manifest.json").read_text())
        self.assertEqual([r["name"] for r in result["steps"]], ["go-test-core"])
        self.assertEqual(result["carried"], 0)
        self.assertEqual(result["executed"], 1)

    def test_changed_source_or_failed_zero_skipped_contract_suite_cannot_pass(self):
        for content in ("", "import unittest\nclass Fixture(unittest.TestCase):\n @unittest.skip('skip')\n def test_pass(self): pass\n",
                        "import unittest\nclass Fixture(unittest.TestCase):\n def test_fail(self): self.fail('failure')\n"):
            with self.subTest(content=content):
                self.git("reset", "--hard", self.base)
                self.write(".github/scripts/test_stub_test.py", content)
                self.commit()
                baseline = self.git("rev-parse", "HEAD")
                self.git("update-ref", "refs/remotes/origin/main", baseline)
                self.write("core/README.md", "new explanation\n")
                self.commit()
                self.session = gate.Session(self.root, Path(self.temp.name) / ("evidence-" + str(len(content))))
                self.session.env.pop("GITHUB_ACTIONS", None)
                result = self.qualify()
                self.assertFalse(result["passed"], result)
                self.assertIn("required prose check failed", result["error"])

    def test_new_link_target_is_checked_against_committed_tree(self):
        self.write("README.md", "Clear explanation with `stable` literal. [missing](missing.md)\n\n```sh\ngo test ./...\n```\n")
        self.commit()
        result = self.qualify()
        self.assertFalse(result["passed"], result)
        self.assertIn("missing committed link target", Path(result["steps"][0]["log"]).read_text())

    def test_failed_prose_check_never_falls_through_to_full_success(self):
        self.write("README.md", "Clear explanation with `stable` literal. [missing](missing.md)\n\n```sh\ngo test ./...\n```\n")
        self.commit()
        with patch.object(self.session, "qualify", side_effect=AssertionError("cannot replace failed prose result")), \
             patch.object(sys, "argv", ["gate", str(self.root), str(self.session.evidence), "--prose-base", "origin/main"]), \
             patch.object(gate, "Session", return_value=self.session), redirect_stdout(io.StringIO()):
            self.assertEqual(gate.main(), 1)

    def test_diverged_target_main_requires_full(self):
        head = self.git("rev-parse", "HEAD")
        self.git("checkout", "-qb", "target", self.base)
        self.write("source.go", "package updated\n")
        self.commit()
        self.git("update-ref", "refs/remotes/origin/main", self.git("rev-parse", "HEAD"))
        self.git("checkout", "-q", "feature")
        self.assertEqual(self.git("rev-parse", "HEAD"), head)
        self.assertIsNone(self.qualify())

    def test_ignored_validation_inputs_require_full(self):
        self.git("reset", "--hard", self.base)
        self.write(".gitignore", "ignored.py\n")
        self.commit()
        self.git("update-ref", "refs/remotes/origin/main", self.git("rev-parse", "HEAD"))
        self.write("core/README.md", "clear explanation\n")
        self.commit()
        for directory, _ in prose.TEST_SUITES:
            with self.subTest(directory=directory):
                self.write(directory + "/ignored.py", "unverified input\n")
                self.assertIsNone(self.qualify())
                (self.root / directory / "ignored.py").unlink()

    def test_source_mutation_and_raw_log_tampering_fail_qualification(self):
        actual = self.session.run
        def run(step, log):
            result = actual(step, log)
            if step.name == "prose-contracts-3":
                self.write("README.md", "changed after tests\n")
            return result
        with patch.object(self.session, "run", side_effect=run):
            result = self.qualify()
        self.assertFalse(result["passed"])
        self.git("reset", "--hard", "HEAD")
        self.session = gate.Session(self.root, Path(self.temp.name) / "tampered-evidence")
        self.session.env.pop("GITHUB_ACTIONS", None)
        actual = self.session.run
        first_log = []
        def tamper(step, log):
            result = actual(step, log)
            if not first_log:
                first_log.append(log)
            elif step.name == "prose-contracts-3":
                first_log[0].write_text("forged output\n")
            return result
        with patch.object(self.session, "run", side_effect=tamper):
            result = self.qualify()
        self.assertFalse(result["passed"])
        self.assertIn("raw evidence changed", result["error"])
