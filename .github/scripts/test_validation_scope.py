import json
from pathlib import Path
import re
import tempfile
import unittest
from unittest.mock import patch

from local_gate_plan import steps, affected_steps
import local_gate_preflight as preflight
import validation_scope as scope
from windows_native import RUST_TESTS, require_rust


class ValidationScopeTest(unittest.TestCase):
    def test_single_raw_diff_preserves_paths_and_regular_modes(self):
        raw = (b":000000 100755 0000000 abc1234 A\0cli/added\tfile.go\0"
               b":100644 100644 abc1234 def5678 M\0cli/line\nbreak.go\0"
               b":100755 100755 abc1234 def5678 M\0cli/script\0"
               b":100755 000000 abc1234 0000000 D\0cli/old name.go\0")
        with patch.object(scope.subprocess, "check_output", return_value=raw) as git:
            self.assertEqual(scope.changed(Path("."), "base", "head"), {
                "cli/added\tfile.go": "A", "cli/line\nbreak.go": "M",
                "cli/script": "M", "cli/old name.go": "D",
            })
            git.assert_called_once_with(
                ["git", "diff", "--raw", "--no-renames", "-z", "base", "head", "--"], cwd=Path("."))
        with patch.object(scope.subprocess, "check_output", return_value=b""):
            self.assertEqual(scope.changed(Path("."), "base"), {})

    def test_unsafe_modes_and_malformed_records_take_the_broad_path(self):
        regular = b":100644 100644 abc1234 def5678 M\0cli/file.go\0"
        for raw in (
            b":000000 120000 0000000 abc1234 A\0symlink\0",
            b":160000 000000 abc1234 0000000 D\0submodule\0",
            b":100644 100755 abc1234 abc1234 M\0script\0",
            b":100644 120000 abc1234 def5678 T\0replaced\0",
            b":100644 100644 abc1234 def5678 R100\0old\0new\0",
            b":100644 100644 abc1234 def5678 A\0file\0",
            regular[:-1], regular.split(b"\0")[0] + b"\0", regular + regular,
            regular.replace(b"cli/file.go", b""), regular.replace(b"cli/file.go", b"cli/\xff.go"),
        ):
            with self.subTest(raw=raw), patch.object(scope.subprocess, "check_output", return_value=raw):
                self.assertTrue(scope.classify_revisions(Path("."), "base", "head")["full"])

    def names(self, path):
        selection = scope.affected({path: "M"})
        return {s.name for s in affected_steps(steps(Path("/tmp/evidence")), selection)}

    def test_go_units_follow_dependency_closure_without_foreign_tools(self):
        for owner, expected in scope.GO_CONSUMERS.items():
            path = "cli/example.go" if owner == "." else owner + "/example.go"
            with self.subTest(path=path):
                result = scope.affected({path: "M"})
                self.assertEqual(set(result["go_modules"]), expected)
                plan = affected_steps(steps(Path("/tmp/evidence")), result)
                names = [s.name for s in plan]
                self.assertIn("go-test-cli", names)
                foreign = {"admin-frozen-install", "node-prebuild", "example-frozen-preflight"}
                if owner == "pb":
                    self.assertTrue(foreign <= set(names))
                else:
                    self.assertFalse(foreign & set(names))

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
        self.assertIn("sqlite-test", self.names("sdks/dart/offline_sqlite/lib/a.dart"))
        self.assertNotIn("go-test-cli", self.names("admin/app/a.tsx"))
        self.assertNotIn("node-generate", self.names("sdks/node/src/a.ts"))

    def test_shared_native_generic_files_and_portable_computation(self):
        for path in ("core/privatefile/file.go", "server/backup/receipt_backup_rename_other.go",
                     "core/mutationlog/filewal.go", "server/service/receipt_directory_sync.go"):
            self.assertFalse(scope.affected({path: "M"})["windows_required"], path)
        for path in ("core/search/index.go", "admin/app/a.tsx", "sdks/rust/src/scans.rs"):
            self.assertFalse(scope.affected({path: "M"})["windows_required"], path)
        for path in ("sdks/rust/src/transport/tests.rs", "sdks/rust/src/test_server.rs", "sdks/rust/src/value.rs"):
            self.assertTrue(scope.affected({path: "M"})["windows_required"], path)
        self.assertTrue(scope.affected({"tests/integration/auth_test.go": "M"})["windows_required"])
        self.assertFalse(scope.affected({"core/search/new_windows.go": "M"})["windows_required"])
        self.assertFalse(scope.affected({"core/search/new.go": "A"})["windows_required"])

    def test_data_runtime_retains_native_and_module_obligations(self):
        for path in ("server/internal/dataruntime/runtime.go",
                     "server/internal/dataruntime/receipt_directory_sync_notwindows.go",
                     "server/internal/dataruntime/receipt_wal_recovery.go",
                     "server/internal/dataruntime/publication_gate.go",
                     "server/internal/dataruntime/snapshot_tail.go"):
            with self.subTest(path=path):
                result = scope.affected({path: "M"})
                self.assertFalse(result["full"])
                self.assertEqual(set(result["go_modules"]), {"server", "."})
                self.assertFalse(result["windows_required"])
                self.assertIn("go-test-server", self.names(path))
                self.assertIn("go-test-cli", self.names(path))
        self.assertTrue(scope.affected({"server/internal/dataruntime/runtime.go": "M",
                                       ".github/scripts/windows_native.py": "M"})["full"])

    def test_unknown_policy_fixtures_and_bad_diffs_keep_full(self):
        self.assertEqual(scope.affected({"docs/oidc-operations.md": "M"})["families"], ["docs"])
        for path in ("unknown.file", "tests/fixture.md", ".github/scripts/unmapped.py",
                     "go.mod", "sdks/node/package.json", "sdks/rust/Cargo.lock", "sdks/rust/xtask/src/main.rs", "sdks/node/src/generated/api.ts", "server/config.yaml", "../core/a.go", "/core/a.go"):
            self.assertTrue(scope.affected({path: "M"})["full"], path)
        for status in ("T", "R100", "U", ""):
            self.assertTrue(scope.affected({"admin/app/a.tsx": status})["full"])
        for status in ("A", "D"):
            self.assertFalse(scope.affected({"core/privatefile/file.go": status})["windows_required"])
        self.assertTrue(scope.local_scope(Path("/nonexistent"), "missing")["full"])
        full = steps(Path("/tmp/evidence"))
        fallback = {s.name for s in affected_steps(full, scope.full("unknown"))}
        self.assertTrue({"go-test-cli", "go-test-server", "dart-test", "admin-test", "rust-test"} <= fallback)
        self.assertFalse({"sqlite-performance-probe", "admin-playwright", "current-functional-wire"} & fallback)
        self.assertEqual(len(full), 79)

    def test_host_routing_changes_run_only_their_contracts(self):
        routing = dict.fromkeys(scope.CI_ROUTING, "M")
        impact = scope.affected(routing)
        self.assertEqual(impact["fixture_contracts"], ["ci-routing"])
        self.assertEqual({s.name for s in affected_steps(steps(Path("/tmp/evidence")), impact)},
                         {"ci-routing-contracts", "dart-workflow-conditions", "final-diff", "final-clean"})
        self.assertFalse(any(value is True for value in scope.hosted(impact, routing).values()))
        for path in scope.CI_ROUTING:
            for status in ("A", "D"):
                self.assertTrue(scope.affected({path: status})["full"], (path, status))
        mixed = scope.affected({**routing, "server/service/connect.go": "M"})
        self.assertEqual(set(mixed["go_modules"]), {"server", "."})
        self.assertEqual(mixed["fixture_contracts"], ["ci-routing"])

    def test_operational_settings_keep_provider_native_coverage_after_extraction(self):
        for path in ("server/internal/config/network.go", "server/internal/config/search_test.go",
                     "server/internal/config/observability.go", "server/internal/config/lifecycle.go",
                     "server/internal/config/environment.go"):
            with self.subTest(path=path):
                result = scope.affected({path: "M"})
                self.assertFalse(result["full"])
                self.assertEqual(set(result["go_modules"]), {"server", "."})
                self.assertTrue(result["windows_required"])
                self.assertIn("go-test-server", self.names(path))
                self.assertIn("go-test-cli", self.names(path))
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
        self.assertFalse(result["windows_required"])
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
        self.assertEqual(names, {"harness-contracts", "docs-text-links", "final-diff", "final-clean"})

    def test_product_fixture_mapping_keeps_owned_checks_and_production_modules(self):
        full = steps(Path("/tmp/evidence"))
        for path in scope.PRODUCT_FIXTURE_INPUTS:
            with self.subTest(path=path):
                result = scope.affected({path: "M"})
                self.assertFalse(result["full"])
                self.assertEqual(result["fixture_contracts"], ["controlled-native-provider"])
                self.assertEqual(result["harness_contracts"], [])
                self.assertIsNone(scope.harness_group(path, "M"))
                production = path == "server/internal/membershippublisher/publisher.go"
                self.assertEqual(set(result["go_modules"]), {"server", "."} if production else set())
                self.assertTrue(result["windows_required"])
                plan = affected_steps(full, result)
                names = [s.name for s in plan]
                self.assertEqual(len(plan), 7 if production else 5)
                self.assertEqual(names[0], "controlled-native-python-contracts")
                self.assertEqual(names[-2:], ["final-diff", "final-clean"])
                self.assertTrue({"server-controlled-native-contracts",
                                 "server-controlled-native-capture-contracts"} <= set(names))
                module_checks = {"go-test-server", "go-test-cli"}
                if production:
                    self.assertTrue(module_checks <= set(names))
                    self.assertEqual(next(s.command for s in plan if s.name == "go-test-server"),
                                     ("go", "test", "./..."))
                else:
                    self.assertFalse(module_checks & set(names))
                self.assertFalse({"dart-doc", "offline-doc", "rust-doc", "dart-publish-dry-run",
                                  "node-verify-package", "go-generate", "current-functional-wire",
                                  "admin-playwright", "sqlite-performance-probe"} & set(names))
                for status in ("A", "D", "T", "R100", "U", ""):
                    self.assertTrue(scope.affected({path: status})["full"], status)
        self.assertEqual(len(full), 79)
        self.assertNotIn("sqlite-performance-probe", {s.name for s in affected_steps(full, scope.full("policy"))})

    def test_fixture_union_deduplicates_python_and_preserves_unknown_policy(self):
        fixture = ".github/scripts/controlled_native_provider_fixture.py"
        changes = {fixture: "M", "docs/controlled-native-providers.md": "M"}
        result = scope.affected(changes)
        plan = affected_steps(steps(Path("/tmp/evidence")), result)
        names = [s.name for s in plan]
        self.assertEqual(len(plan), 6)
        self.assertIn("docs-text-links", names)
        self.assertIn("controlled-native-python-contracts", names)
        result = scope.affected({fixture: "M", "core/collection/map.go": "M",
                                 ".github/scripts/bench_time_profile.py": "M", "admin/app/a.tsx": "M"})
        self.assertEqual(set(result["go_modules"]), {"core", "server", "."})
        self.assertEqual(result["families"], ["admin"])
        self.assertEqual(result["harness_contracts"], ["bench-time"])
        self.assertEqual(result["fixture_contracts"], ["controlled-native-provider"])
        for path in ("server/service/connect.go", "server/internal/security/control.go",
                     "server/internal/membershippublisher/publisher.go"):
            mixed = scope.affected({fixture: "M", path: "M"})
            mixed_plan = affected_steps(steps(Path("/tmp/evidence")), mixed)
            self.assertEqual(set(mixed["go_modules"]), {"server", "."}, path)
            self.assertEqual(len(mixed_plan), 7, path)
            self.assertIn("go-test-server", {s.name for s in mixed_plan}, path)
        for path in (".github/scripts/unknown_fixture.py", ".github/workflows/go.yml",
                     ".github/scripts/local_gate_session.py", ".github/scripts/current_qualification_scope.py",
                     "CONTRIBUTING.md", "server/go.mod", "sdks/dart/pubspec.lock"):
            self.assertTrue(scope.affected({fixture: "M", path: "M"})["full"], path)
        for path in (".github/scripts/controlled_native_unknown_fixture.py",
                     "server/cmd/controlled_native_new_gate_test.go",
                     "server/cmd/controlled_native_owner_stop_gate_test.go",
                     "server/internal/membershippublisher/new_controlled_clock_fixture_test.go"):
            for status in ("M", "A", "D"):
                self.assertTrue(scope.affected({path: status})["full"], (path, status))
                self.assertTrue(scope.affected({fixture: "M", path: status})["full"], (path, status))
        for path in ("server/cmd/ordinary_test.go", "server/internal/membershippublisher/ordinary_test.go"):
            result = scope.affected({path: "M"})
            self.assertFalse(result["full"], path)
            self.assertEqual(set(result["go_modules"]), {"server", "."}, path)

    def test_clock_source_names_are_not_dependency_lockfiles(self):
        for path in ("core/hlc/clock.go", "core/collection/block.go",
                     "core/privatefile/file_lock.go",
                     "server/internal/membershippublisher/controlled_clock_fixture.go"):
            self.assertFalse(scope.dependency_lock(path), path)
            self.assertFalse(scope.affected({path: "M"})["full"], path)
        for path in ("sdks/node/package-lock.json", "admin/npm-shrinkwrap.json", "Cargo.lock",
                     "sdks/dart/pubspec.lock", "bun.lockb", "packages.lock.json", "lockfile"):
            self.assertTrue(scope.dependency_lock(path), path)
            self.assertTrue(scope.affected({path: "M"})["full"], path)

    def test_focused_tagged_selectors_bind_existing_pure_tests(self):
        selection = scope.affected({".github/scripts/controlled_native_provider_fixture.py": "M"})
        plan = affected_steps(steps(Path("/tmp/evidence")), selection)
        step = next(s for s in plan if s.name == "server-controlled-native-contracts")
        pattern = re.compile(step.command[step.command.index("-run") + 1])
        root = Path(__file__).resolve().parents[2] / "server"
        selected = set()
        for directory in ("cmd", "provider", "internal/membershippublisher"):
            for source in (root / directory).glob("*_test.go"):
                selected.update(name for name in re.findall(r"^func (Test\w+)\(", source.read_text(), re.MULTILINE)
                                if pattern.fullmatch(name))
        self.assertEqual(len(selected), 17)
        self.assertTrue({"TestControlledNativeProviderNativeLeaseMonitor",
                         "TestControlledNativeProviderPublisherRenewal",
                         "TestControlledFixtureClockOwnsAdmissionPublicationAndObservations",
                         "TestControlledFixtureClockPreservesFiniteBudgetAndModes"} <= selected)
        self.assertFalse({"TestControlledNativeProviderChild", "TestControlledNativeProviderPublisherChild",
                          "TestControlledNativeProviderProfileDescription", "TestControlledNativeProviderBuildRevision",
                          "TestControlledNativeProviderSignedRenewalTranscript"} & selected)
        owners = (
            "cmd/controlled_native_provider_gate_test.go",
            "cmd/controlled_native_owner_window_gate_test.go",
            "cmd/controlled_native_lease_gate_test.go",
            "cmd/controlled_native_publisher_gate_test.go",
            "internal/membershippublisher/controlled_clock_fixture_test.go",
        )
        actual = {name for owner in owners for name in re.findall(
            r"^func (Test\w+)\(", (root / owner).read_text(), re.MULTILINE)}
        delegated = {"TestControlledNativeProviderBuildRevision", "TestControlledNativeProviderChild",
                     "TestControlledNativeProviderPublisherChild", "TestControlledNativeProviderProfileDescription",
                     "TestControlledNativeProviderSignedRenewalTranscript"}
        self.assertFalse(actual - selected - delegated,
                         "new fixture Tests require explicit coverage: " + str(sorted(actual - selected - delegated)))
        capture = next(s for s in plan if s.name == "server-controlled-native-capture-contracts")
        parent, child = capture.command[capture.command.index("-run") + 1].split("/")
        self.assertTrue(re.fullmatch(parent, "TestControlledNativeProviderSignedRenewalTranscript"))
        source = (root / "cmd/controlled_native_lease_gate_test.go").read_text()
        names = [name.replace(" ", "_") for name in re.findall(r't.Run\("([^"\n]+)"', source)]
        self.assertEqual({name for name in names if re.fullmatch(child, name)}, {
            "captured_atomically_replaced_signed_inputs", "bounded_open_ENOENT_capture",
            "unsafe_renewable_input_refused"})
        self.assertIsNone(re.fullmatch(child, "maintenance_maximum_seven_days_replay"))

    def test_pure_fixture_contracts_still_require_pinned_go_preflight(self):
        result = scope.affected({".github/scripts/controlled_native_provider_fixture.py": "M"})
        self.assertEqual(result["go_modules"], [])
        plan = affected_steps(steps(Path("/tmp/evidence")), result)
        root = Path(__file__).resolve().parents[2]
        with patch.object(preflight, "scratch_environment", return_value={}), \
                patch.object(preflight, "executable", return_value="/selected/go"), \
                patch.object(preflight, "command", return_value="go version go0.0.1 linux/amd64") as command:
            with self.assertRaisesRegex(ValueError, "local child.*expected=.*actual=0.0.1"):
                preflight.check(root, {}, plan)
            self.assertEqual(command.call_count, 1)

    def test_go_with_docs_preserves_module_docs_and_native_obligations(self):
        cases = (
            ("server/internal/authn/browser_login.go", {"server", "."}, True),
            ("core/search/index.go", {"core", "server", "."}, False),
        )
        for path, modules, native in cases:
            with self.subTest(path=path):
                result = scope.affected({path: "M", "docs/oidc-operations.md": "M"})
                self.assertFalse(result["full"])
                self.assertEqual(set(result["families"]), {"docs"})
                self.assertEqual(set(result["go_modules"]), modules)
                self.assertFalse(result["windows_required"])
                self.assertFalse(result["generation"])
                self.assertFalse(scope.harness_required(result))
                names = {s.name for s in affected_steps(steps(Path("/tmp/evidence")), result)}
                self.assertEqual({name for name in names if name.startswith("go-test-")},
                                 {"go-test-cli" if module == "." else "go-test-" + module.replace("/", "-") for module in modules})
                self.assertIn("docs-text-links", names)
                self.assertFalse({"docs-tests", "go-lint", "node-prebuild", "admin-frozen-install"} & names)
                self.assertFalse({"current-functional-wire", "native-login-wire", "admin-playwright",
                                  "rust-real-wire", "dart-test", "sqlite-performance-probe"} & names)

    def test_early_real_tools_follow_relevant_changes_without_duplicate_product_sweeps(self):
        for path in ("core/search/index.go", "admin/app/a.tsx", "server/service/runtime.go"):
            self.assertFalse(scope.harness_required(scope.affected({path: "M"})), path)
        for path in (".github/scripts/bench_time_profile.py", "proto/api.proto", "go.mod",
                     "testbed/bench/local_time.sh", ".github/scripts/unknown.py"):
            self.assertTrue(scope.harness_required(scope.affected({path: "M"})), path)
        self.assertTrue(scope.harness_required(scope.full("classification error")))

    def test_shared_cli_grammar_fixture_selects_go_and_admin_consumers(self):
        path = "admin/test/cli-grammar/verbs.json"
        for status in ("A", "M", "D"):
            changes = {path: status}
            scope_result = scope.affected(changes)
            self.assertEqual(scope_result["families"], ["admin"])
            self.assertEqual(scope_result["go_modules"], ["."])
            self.assertTrue(scope.hosted(scope_result, changes)["admin_required"])

    def test_mixed_paths_union_including_rename_sides(self):
        result = scope.affected({"sdks/rust/src/value.rs": "D", "admin/app/a.tsx": "A"})
        self.assertEqual(set(result["families"]), {"rust", "admin"})
        self.assertTrue(result["windows_required"])
        self.assertFalse(scope.affected({"core/privatefile/file.go": "D", "docs/a.md": "A"})["windows_required"])

    def test_exact_rust_native_selectors_match_existing_module_and_function(self):
        root = Path(__file__).resolve().parents[2] / "sdks/rust/src"
        for name in RUST_TESTS:
            file, module, function = name.split("::")
            owner = (root / (file + ".rs")).read_text()
            self.assertIn("mod " + module, owner)
            body = owner if file == "test_server" else (root / file / "tests.rs").read_text()
            self.assertIn("async fn " + function + "(", body)

    def test_native_rust_verifier_rejects_missing_or_failed_tests(self):
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
