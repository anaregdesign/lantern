from pathlib import Path
import subprocess
import tempfile
import unittest

from git_test_fixture import synchronous_git_maintenance
import validation_scope


PFLAG = "github.com/spf13/pflag"
OLD = "6afc89df92d6eb3834022f73cd65adc8cdfcb92d # v1.8.1"
NEW = "2286a0c042b44434fe26d292bbce8d34f7ec662b # v1.8.2"
WORKFLOWS = (".github/workflows/dart-sdk.yml", ".github/workflows/current-sdk4.yml")


class DependencyRoutingTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.git("init", "-q")
        synchronous_git_maintenance(self.root)
        self.git("config", "user.name", "fixture")
        self.git("config", "user.email", "fixture@example.test")
        for mod in validation_scope.GO_CONSUMERS:
            self.write(str(Path(mod) / "go.mod"), "module example.test/" + mod + "\ngo 1.27\n")
        self.write("cli/main.go", 'package main\nimport "' + PFLAG + '"\n')
        self.dependency("v1.0.10")
        for path in WORKFLOWS:
            self.write(path, "jobs:\n  native:\n    steps:\n      - uses: dart-lang/setup-dart@" + OLD + "\n")
        self.base = self.commit()

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.root, text=True).strip()

    def write(self, name, value):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(value)

    def commit(self):
        self.git("add", ".")
        self.git("commit", "-qm", "fixture")
        return self.git("rev-parse", "HEAD")

    def select(self):
        return validation_scope.classify_revisions(self.root, self.base, self.commit())

    def dependency(self, version, mod=".", name=PFLAG):
        manifest = str(Path(mod) / "go.mod")
        self.write(manifest, "module example.test/" + mod + "\ngo 1.27\n\nrequire (\n\t" + name + " " + version + "\n)\n")
        self.write(str(Path(mod) / "go.sum"), "".join(
            f"{name} {version}{suffix} h1:{'a' * 43}=\n" for suffix in ("", "/go.mod")))

    def pins(self):
        for path in WORKFLOWS:
            self.write(path, (self.root / path).read_text().replace(OLD, NEW))

    def test_2124_root_cli_require_and_sums(self):
        self.dependency("v1.1.0")
        result = self.select()
        self.assertEqual(result["go_modules"], ["."])
        self.assertEqual(result["semantic"], dict.fromkeys(("go.mod", "go.sum"), "cli-dependency"))
        self.assertEqual(result["codeql_languages"], [{"language": "go", "build-mode": "autobuild"}])
        self.assertTrue(result["verify_dependencies"])
        for name in ("full", "functional_required", "rust_required", "node_required", "admin_required",
                     "generation_required", "deployment_required", "fuzz_required", "windows_required"):
            self.assertFalse(result[name], name)

    def test_2198_pure_pins_then_mixed_with_cli(self):
        self.pins()
        result = self.select()
        self.assertEqual(result["semantic"], dict.fromkeys(WORKFLOWS, "setup-dart"))
        self.assertEqual(result["go_modules"], [])
        self.assertFalse(result["windows_required"])
        self.assertTrue(result["functional_required"])
        self.assertEqual(result["codeql_languages"], [{"language": "actions", "build-mode": "none"}])
        self.dependency("v1.1.0")
        mixed = self.select()
        self.assertTrue(mixed["go_required"])
        self.assertTrue(mixed["functional_required"])
        self.assertFalse(mixed["windows_required"])
        self.assertEqual([item["language"] for item in mixed["codeql_languages"]], ["go", "actions"])

    def test_a_changed_nested_checksum_ledger_selects_its_owner(self):
        self.write("server/go.sum", (self.root / "go.sum").read_text())
        self.base = self.commit()
        self.dependency("v1.1.0")
        self.write("server/go.sum", (self.root / "go.sum").read_text())
        result = self.select()
        self.assertEqual(result["go_modules"], [".", "server"])
        self.assertTrue(result["verify_dependencies"] and result["backend_dependencies"])
        self.assertTrue(result["functional_required"])

    def test_an_unmodified_module_declaring_the_same_dependency_is_a_consumer(self):
        self.dependency("v1.0.10", "server")
        self.base = self.commit()
        self.dependency("v1.1.0")
        self.write("go.work.sum", f"{PFLAG} v1.1.0 h1:{'a' * 43}=\n")
        # Adding a checksum file is structurally unknown; a normal existing
        # workspace checksum replacement is supported.
        self.assertTrue(self.select()["full"])
        self.base = self.git("rev-parse", "HEAD")
        self.dependency("v1.2.0")
        self.write("go.work.sum", f"{PFLAG} v1.2.0 h1:{'a' * 43}=\n")
        result = self.select()
        self.assertEqual(result["dependency_owners"], [".", "server"])
        self.assertEqual(result["go_modules"], [".", "server"])
        self.assertTrue(result["backend_dependencies"])
        self.assertTrue(result["windows_required"] and result["rust_required"] and result["node_required"])

    def test_transitive_workspace_sums_cover_all_go_modules(self):
        self.dependency("v1.0.10", "server")
        self.write("go.work.sum", f"{PFLAG} v1.0.10 h1:{'a' * 43}=\n")
        self.base = self.commit()
        self.dependency("v1.1.0", "server")
        self.write("go.work.sum", (self.root / "go.work.sum").read_text() +
                   f"example.test/transitive v1.0.1/go.mod h1:{'b' * 43}=\n")
        result = self.select()
        self.assertFalse(result["full"])
        self.assertEqual(set(result["go_modules"]), set(validation_scope.GO_CONSUMERS))
        self.assertTrue(result["backend_dependencies"] and result["verify_dependencies"])

    def test_changes_outside_version_updates_require_broad_validation(self):
        self.dependency("v1.1.0")
        self.write("go.sum", (self.root / "go.sum").read_text() + f"example.test/more v1.0.0 h1:{'b' * 43}=\n")
        self.assertTrue(self.select()["full"])
        self.dependency("v1.1.0")
        self.write("go.mod", (self.root / "go.mod").read_text().replace("go 1.27", "go 1.28"))
        self.assertTrue(self.select()["full"])
        self.dependency("v1.1.0")
        self.pins()
        path = WORKFLOWS[0]
        self.write(path, (self.root / path).read_text() + "      - run: echo changed\n")
        self.assertTrue(self.select()["full"])

    def test_both_rename_sides_select_owners_and_unavailable_range_is_broad(self):
        self.write("admin/app/button.ts", "export const value = true\n")
        self.base = self.commit()
        (self.root / "sdks/node/src").mkdir(parents=True)
        self.git("mv", "admin/app/button.ts", "sdks/node/src/button.ts")
        result = self.select()
        self.assertTrue(result["admin_required"] and result["node_required"])
        self.assertFalse(result["rust_required"])
        unknown = validation_scope.classify_revisions(self.root, "missing-revision", "HEAD")
        self.assertTrue(unknown["full"] and unknown["rust_required"] and unknown["functional_required"])

    def test_2238_2239_example_dependency_updates_skip_sdk4_but_mixed_changes_select_it(self):
        manifest = "sdks/dart/example/pubspec.yaml"
        lock = "sdks/dart/example/pubspec.lock"
        for dependency, section, old_constraint, new_constraint, old_version, new_version in (
            ("sqflite_common_ffi", "dev_dependencies", "^2.4.0", "^2.4.7", "2.4.3", "2.4.7"),
            ("crypto", "dependencies", "3.0.7", "3.0.8", "3.0.7", "3.0.8"),
        ):
            with self.subTest(dependency=dependency):
                self.write(manifest, f"{section}:\n  {dependency}: {old_constraint}\n")
                self.write(lock, f'packages:\n  {dependency}:\n    version: "{old_version}"\n')
                self.base = self.commit()
                self.write(manifest, f"{section}:\n  {dependency}: {new_constraint}\n")
                self.write(lock, f'packages:\n  {dependency}:\n    version: "{new_version}"\n')
                result = self.select()
                self.assertEqual(result["semantic"], dict.fromkeys((manifest, lock), "package-dependency"))
                self.assertEqual(result["families"], ["example"])
                self.assertFalse(result["functional_required"])

        # Mix the same dependency diff with each separate wire consumer.
        dependency_head = self.git("rev-parse", "HEAD")
        for path, required in (
            ("sdks/dart/lib/client.dart", True),
            ("sdks/dart/integration_test/current_security_test.dart", True),
            ("sdks/dart/integration_test/scoped_changes_test.dart", True),
            ("admin/app/lib/client/usecase/security/security-management.ts", True),
            ("admin/app/lib/client/usecase/security/security-draft-recovery.ts", True),
            ("admin/app/lib/client/usecase/security/security-management.test.ts", False),
        ):
            with self.subTest(path=path):
                self.git("reset", "--hard", dependency_head)
                self.write(path, "// change\n")
                self.assertEqual(self.select()["functional_required"], required)

    def test_only_locks_in_sdk4_working_directories_select_authority(self):
        for path, required in (
            ("sdks/dart/pubspec.lock", True),
            ("sdks/node/bun.lock", True),
            ("sdks/rust/Cargo.lock", True),
            ("admin/bun.lock", True),
            ("sdks/dart/offline/pubspec.lock", False),
            ("sdks/dart/offline/tool/paired_source.pubspec.lock", False),
            ("sdks/dart/offline_sqlite/pubspec.lock", False),
        ):
            with self.subTest(path=path):
                self.write(path, "previous resolution\n")
                self.base = self.commit()
                self.write(path, "updated resolution\n")
                result = self.select()
                self.assertEqual(result["semantic"], {path: "package-dependency"})
                self.assertEqual(result["functional_required"], required)

    def test_package_lock_and_manifest_belong_to_package_toolchain_is_broad(self):
        self.write("admin/bun.lock", "lock before")
        self.write("admin/package.json", '{"dependencies":{"example":"1.0"},"packageManager":"bun@1"}')
        self.base = self.commit()
        self.write("admin/bun.lock", "lock after")
        self.write("admin/package.json", '{"dependencies":{"example":"1.1"},"packageManager":"bun@1"}')
        result = self.select()
        self.assertTrue(result["admin_required"] and result["functional_required"])
        self.assertFalse(result["go_required"] or result["windows_required"] or result["rust_required"])
        self.write("admin/package.json", '{"dependencies":{"example":"1.1"},"packageManager":"bun@2"}')
        self.assertTrue(self.select()["full"])


if __name__ == "__main__":
    unittest.main()
