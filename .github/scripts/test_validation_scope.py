import json
from pathlib import Path
import tempfile
import unittest

from local_gate_plan import steps, affected_steps
import validation_scope as scope
from windows_native import GO_TESTS, RUST_TESTS, require_go, require_rust


class ValidationScopeTest(unittest.TestCase):
    def names(self, path):
        selection = scope.affected({path: "M"})
        return {s.name for s in affected_steps(steps(Path("/tmp/evidence")), selection)}

    def test_go_dependency_closure_and_install_before_go(self):
        for owner, expected in scope.GO_CONSUMERS.items():
            path = "cli/example.go" if owner == "." else owner + "/example.go"
            with self.subTest(path=path):
                result = scope.affected({path: "M"})
                self.assertEqual(set(result["go_modules"]), expected)
                plan = affected_steps(steps(Path("/tmp/evidence")), result)
                names = [s.name for s in plan]
                self.assertLess(names.index("admin-frozen-install"), names.index("go-test-."))

    def test_language_consumers_and_fresh_development_boundaries(self):
        cases = {"admin/app/a.tsx": {"admin"}, "sdks/node/src/a.ts": {"node", "admin"},
                 "sdks/dart/lib/a.dart": {"dart", "offline", "sqlite", "example"},
                 "sdks/dart/offline/lib/a.dart": {"offline", "sqlite", "example"},
                 "sdks/dart/offline_sqlite/lib/a.dart": {"sqlite", "example"},
                 "sdks/dart/example/lib/a.dart": {"example"}, "sdks/rust/src/scans.rs": {"rust"}}
        for path, families in cases.items():
            with self.subTest(path=path):
                result = scope.affected({path: "M"})
                self.assertFalse(result["full"])
                self.assertFalse(scope.harness_required(result))
                self.assertEqual(set(result["families"]), families)
                names = self.names(path)
                self.assertTrue({"final-diff", "final-clean"} <= names)
                self.assertFalse({"rust-real-wire", "current-functional-wire", "sqlite-performance-probe"} & names)
        self.assertIn("sqlite-crash-probe", self.names("sdks/dart/offline_sqlite/lib/a.dart"))
        self.assertNotIn("go-test-.", self.names("admin/app/a.tsx"))
        self.assertNotIn("node-generate", self.names("sdks/node/src/a.ts"))

    def test_shared_native_generic_files_and_portable_computation(self):
        for path in ("core/privatefile/file.go", "server/backup/receipt_backup_rename_other.go",
                     "core/mutationlog/filewal.go", "server/service/receipt_directory_sync.go"):
            self.assertTrue(scope.affected({path: "M"})["windows_required"], path)
        for path in ("core/search/index.go", "admin/app/a.tsx", "sdks/rust/src/scans.rs"):
            self.assertFalse(scope.affected({path: "M"})["windows_required"], path)
        for path in ("sdks/rust/src/transport/tests.rs", "sdks/rust/src/test_server.rs", "sdks/rust/src/value.rs"):
            self.assertTrue(scope.affected({path: "M"})["windows_rust_required"], path)
        self.assertTrue(scope.affected({"tests/integration/auth_test.go": "M"})["windows_rust_required"])
        self.assertTrue(scope.affected({"core/search/new_windows.go": "M"})["windows_required"])
        self.assertTrue(scope.affected({"core/search/new.go": "A"})["windows_required"])

    def test_unknown_policy_fixtures_and_bad_diffs_keep_full(self):
        for path in ("unknown.file", "tests/fixture.md", ".github/scripts/validation_scope.py",
                     "go.mod", "sdks/node/package.json", "sdks/rust/Cargo.lock", "sdks/rust/xtask/src/main.rs", "sdks/node/src/generated/api.ts", "server/config.yaml", "../core/a.go", "/core/a.go"):
            self.assertTrue(scope.affected({path: "M"})["full"], path)
        for status in ("T", "R100", "U", ""):
            self.assertTrue(scope.affected({"admin/app/a.tsx": status})["full"])
        for status in ("A", "D"):
            self.assertTrue(scope.affected({"core/privatefile/file.go": status})["windows_required"])
        self.assertTrue(scope.local_scope(Path("/nonexistent"), "missing")["full"])
        full = steps(Path("/tmp/evidence"))
        self.assertEqual(affected_steps(full, scope.full("unknown")), full)
        self.assertEqual(len(full), 79)

    def test_operational_settings_keep_provider_native_coverage_after_extraction(self):
        for path in ("server/internal/config/network.go", "server/internal/config/search_test.go",
                     "server/internal/config/observability.go", "server/internal/config/lifecycle.go",
                     "server/internal/config/environment.go"):
            with self.subTest(path=path):
                result = scope.affected({path: "M"})
                self.assertFalse(result["full"])
                self.assertEqual(set(result["go_modules"]), {"server", "."})
                self.assertTrue(result["windows_required"])
                self.assertTrue(result["windows_rust_required"])
                self.assertIn("go-test-server", self.names(path))
                self.assertIn("go-test-.", self.names(path))
        for path in ("server/internal/config/config.go", "server/internal/config/security_config.go",
                     "server/internal/config/new.yaml", "server/internal/config/unknown.file"):
            self.assertTrue(scope.affected({path: "M"})["full"], path)
        self.assertTrue(scope.affected({"server/internal/config/network.go": "M", "go.mod": "M"})["full"])

    def test_known_harness_readers_select_their_consumers_without_product_carry(self):
        for path, group in scope.HARNESS_INPUTS.items():
            with self.subTest(path=path):
                result = scope.affected({path: "M"})
                self.assertFalse(result["full"])
                self.assertEqual(result["harness_contracts"], [group])
                self.assertTrue(scope.harness_required(result))
                self.assertEqual(result["go_modules"], [])
                self.assertFalse(result["windows_required"])
                plan = affected_steps(steps(Path("/tmp/evidence")), result)
                self.assertEqual([s.name for s in plan],
                                 ["harness-contracts", "final-diff", "final-clean"])
                self.assertEqual(plan[0].command[-2:], ("--group", group))
                for status in ("A", "D", "T", "R100"):
                    self.assertTrue(scope.affected({path: status})["full"])

    def test_harness_union_preserves_product_security_policy_and_unknown_inputs(self):
        result = scope.affected({".github/scripts/bench_time_profile.py": "M",
                                 ".github/scripts/verify_root_release_binaries.py": "M",
                                 "server/internal/security/control.go": "M"})
        self.assertFalse(result["full"])
        self.assertEqual(result["harness_contracts"], ["bench-time", "release-binaries"])
        self.assertTrue(result["windows_required"])
        self.assertEqual(set(result["go_modules"]), {"server", "."})
        for path in ("testbed/bench/local_time.sh", "testbed/bench/owned_compose.sh",
                     "testbed/bench/scenarios/broad_rw.yaml", ".github/workflows/go.yml",
                     ".github/scripts/harness_contracts.py", ".github/scripts/new_reader.py",
                     ".github/scripts/current_qualification_scope.py", "server/go.mod"):
            self.assertTrue(scope.affected({".github/scripts/bench_time_profile.py": "M",
                                           path: "M"})["full"], path)

    def test_harness_modification_with_docs_still_selects_docs_contracts(self):
        result = scope.affected({".github/scripts/git_test_fixture.py": "M", "docs/ci-inventory.md": "M"})
        self.assertFalse(result["full"])
        names = {s.name for s in affected_steps(steps(Path("/tmp/evidence")), result)}
        self.assertEqual(names, {"harness-contracts", "docs-tests", "final-diff", "final-clean"})

    def test_early_real_tools_follow_relevant_changes_without_duplicate_product_sweeps(self):
        for path in ("core/search/index.go", "admin/app/a.tsx", "server/service/runtime.go"):
            self.assertFalse(scope.harness_required(scope.affected({path: "M"})), path)
        for path in (".github/scripts/bench_time_profile.py", "proto/api.proto", "go.mod",
                     "testbed/bench/local_time.sh", ".github/scripts/unknown.py"):
            self.assertTrue(scope.harness_required(scope.affected({path: "M"})), path)
        self.assertTrue(scope.harness_required(scope.full("classification error")))

    def test_mixed_paths_union_including_rename_sides(self):
        result = scope.affected({"sdks/rust/src/value.rs": "D", "admin/app/a.tsx": "A"})
        self.assertEqual(set(result["families"]), {"rust", "admin"})
        self.assertTrue(result["windows_required"])
        self.assertTrue(scope.affected({"core/privatefile/file.go": "D", "docs/a.md": "A"})["windows_required"])

    def test_exact_rust_native_selectors_match_existing_module_and_function(self):
        root = Path(__file__).resolve().parents[2] / "sdks/rust/src"
        for name in RUST_TESTS:
            file, module, function = name.split("::")
            owner = (root / (file + ".rs")).read_text()
            self.assertIn("mod " + module, owner)
            body = owner if file == "test_server" else (root / file / "tests.rs").read_text()
            self.assertIn("async fn " + function + "(", body)

    def test_native_verifiers_refuse_empty_skipped_and_failed_tests(self):
        events = "\n".join(json.dumps(dict(Action="pass", Test=name)) for name in GO_TESTS)
        require_go(events, GO_TESTS)
        for bad in ("", json.dumps(dict(Action="pass")), json.dumps(dict(Action="skip", Test=GO_TESTS[0]))):
            with self.assertRaises(ValueError):
                require_go(bad, GO_TESTS)
        with self.assertRaises(ValueError):
            require_go(events + '\n{"Action":"fail"}', GO_TESTS)
        for name in RUST_TESTS:
            summary = "test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 110 filtered out; finished in 0.15s\n"
            require_rust("test " + name + " ... ok\n" + summary, name)
            # Exact observed Windows shape: inherited server output appears
            # between libtest's test name and its success marker.
            require_rust("\x1b[32mtest " + name + " ... \x1b[0m{\"msg\":\"started call\"}\r\nok\r\n" + summary, name)
            for bad in ("running 0 tests\n", "test " + name + " ... ok\n",
                        "test " + name + " ... ignored\n" + summary.replace("1 passed", "0 passed").replace("0 ignored", "1 ignored"),
                        "test wrong::name ... ok\n" + summary,
                        "test " + name + " ... FAILED\n" + summary.replace("ok. 1 passed; 0 failed", "FAILED. 0 passed; 1 failed")):
                with self.subTest(name=name, bad=bad), self.assertRaises(ValueError):
                    require_rust(bad, name)


if __name__ == "__main__":
    unittest.main()
