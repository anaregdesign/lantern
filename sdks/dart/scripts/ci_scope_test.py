"""The Dart workflow's package and native-platform selection contract."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest

import ci_scope


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
        }
        for name, (paths, toolchain_changed, expected) in cases.items():
            with self.subTest(name):
                self.assertEqual(
                    ci_scope.classify(paths, toolchain_changed=toolchain_changed),
                    expected,
                )

    def test_release_tag_requires_both_gates(self):
        self.assertEqual(ci_scope.classify([], release_tag=True), (True, True))

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
