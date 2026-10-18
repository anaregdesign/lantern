"""Guard the representative coverage and event boundaries of the CI reduction."""

import json
import sys
import tempfile
import textwrap
from pathlib import Path
import re
import tomllib
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[2]


def workflow(name):
    return (ROOT / f".github/workflows/{name}.yml").read_text()


def job(text, name):
    return re.search(rf"(?ms)^  {name}:\n(.*?)(?=^  [\w-]+:\n|\Z)", text)[1]


class RepresentativeCoverageTest(unittest.TestCase):
    def test_windows_retains_native_mtls_without_requesting_unsupported_oidc(self):
        import windows_native
        self.assertIn("TestAuthorityTimeUnsupportedNativeProfile", windows_native.GO_TESTS)
        self.assertIn("transport::tests::real_wire_off_mtls_requires_client_identity", windows_native.RUST_TESTS)
        self.assertNotIn("transport::tests::real_wire_mtls_and_bearer_rotation", windows_native.RUST_TESTS)
        source = (ROOT / "sdks/rust/src/transport/tests.rs").read_text()
        off = source.split("async fn real_wire_off_mtls_requires_client_identity()", 1)[1].split("#[tokio::test]", 1)[0]
        self.assertIn("start_tls_server(&certs, true)", off)
        self.assertIn("tls_client_identity", off)
        self.assertIn("get_status", off)
        self.assertNotIn("token_provider", off)

    def test_receipt_startup_log_exports_only_fixed_categories(self):
        source = (ROOT / "testbed/scripts/native_receipt_fixture.sh").read_text()
        code = source.split("<<'PYDIAGNOSTIC' || true\n", 1)[1].split("\nPYDIAGNOSTIC", 1)[0]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "private-log"
            path.write_text('private-token-key-body\nauthfixture_failure:spawn\nauthfixture_failure:private-token\nauthfixture_readiness:{"server_log":"source_input_missing","err":"private-token"}\nauthfixture_readiness:{"server_log":[]}\nauthfixture_readiness:{"server_log":"private-token"}\n')
            import contextlib
            import io
            with patch.object(sys, "argv", ["diagnostic", str(path)]), contextlib.redirect_stderr(io.StringIO()) as output:
                exec(compile(code, "<receipt-startup-diagnostic>", "exec"), {})
            self.assertEqual(output.getvalue(), "authfixture_failure:spawn\nauthfixture_server_log:source_input_missing\n")
            path.unlink()
            with patch.object(sys, "argv", ["diagnostic", str(path)]), contextlib.redirect_stderr(io.StringIO()) as output:
                exec(compile(code, "<receipt-startup-diagnostic>", "exec"), {})
            self.assertEqual(output.getvalue(), "authfixture_diagnostics:unavailable\n")

    def test_rust_source_and_release_cover_msrv_and_every_native_platform(self):
        manifest = tomllib.loads((ROOT / "sdks/rust/Cargo.toml").read_text())
        floor = manifest["package"]["rust-version"] + ".0"
        expected = {
            ("ubuntu-latest", floor), ("ubuntu-latest", "stable"),
            ("macos-latest", "stable"), ("windows-latest", "stable"),
        }
        for name, key in [("rust-sdk", "test"), ("rust-release", "conformance")]:
            with self.subTest(workflow=name):
                body = job(workflow(name), key)
                matrix = body.split("      matrix:\n", 1)[1].split("    steps:\n", 1)[0]
                if name == "rust-sdk":
                    full, routine = [json.loads(value) for value in re.findall(r"'(\[.*?\])'", matrix)]
                    lanes = [(item["os"], item["rust"]) for item in routine]
                    self.assertEqual({(item["os"], item["rust"]) for item in full}, expected)
                    selected = expected - {("windows-latest", "stable")}
                    self.assertIn("github.event_name == 'workflow_dispatch'", matrix)
                    self.assertIn("  workflow_dispatch:", workflow(name))
                    self.assertIn("'tests/integration/auth_test.go'", workflow(name))
                else:
                    lanes = re.findall(r"- \{os: ([\w-]+), rust: '?([\w.]+)'?\}", matrix)
                    selected = expected
                self.assertEqual(set(lanes), selected)
                self.assertEqual(len(lanes), len(selected))
                self.assertNotRegex(matrix, r"(?m)^        (os|rust|exclude):")
                self.assertIn("fail-fast: false", body)
                for command in ("cargo xtask codegen", "cargo fmt --all -- --check",
                                "cargo clippy --workspace --locked --all-targets --all-features -- -D warnings",
                                "cargo test --locked --no-default-features smoke::native_tls_roots_are_available_by_default",
                                "cargo test --locked --all-features",
                                "cargo doc --locked --no-deps --all-features"):
                    self.assertIn(command, body)
                # Every retained stable platform still executes the wire suite.
                wire = next(step for step in body.split("      - ")
                            if "../../.github/scripts/rust_wire.py" in step)
                self.assertIn("if: matrix.rust == 'stable'", wire)
                self.assertNotIn("matrix.os == 'ubuntu-latest'", wire)
        release = workflow("rust-release")
        self.assertIn("needs: [tag, conformance, current-sdk4]", job(release, "preflight"))
        self.assertIn("--proof-kind Q", job(release, "preflight"))
        self.assertIn("needs: [tag, verify-published]", job(release, "release"))
        self.assertIn('go build -o "sdks/rust/target/lantern-authfixture$suffix"', job(release, "conformance"))
        self.assertIn("../../.github/scripts/rust_wire.py", job(release, "conformance"))
        self.assertNotIn("go test ./tests/integration", job(release, "conformance"))
        self.assertIn("--include-scoped", workflow("current-sdk4"))
        go = workflow("go")
        self.assertIn("receipt-backup-windows]", job(go, "build"))
        self.assertIn("WINDOWS_RESULT: ${{ needs.receipt-backup-windows.result }}", job(go, "build"))
        self.assertIn("windows_native.py", job(go, "receipt-backup-windows"))
        self.assertIn("windows_rust_required", job(go, "receipt-backup-windows"))

    def test_retained_app_wire_rejects_missing_skipped_and_incomplete_execution(self):
        body = job(workflow("dart-sdk"), "offline-sqlite")
        step = next(part for part in body.split("      - name: ")
                    if part.startswith("Test app data permissions"))
        self.assertIn('LANTERN_CURRENT_PUBLIC_GATE: "1"', step)
        self.assertIn("go test -json -tags=lantern_test_time", step)
        source = textwrap.dedent(step.split("<<'PYAPP'\n", 1)[1].split("\n          PYAPP", 1)[0])
        names = ("TestAuth_FlutterDataDemoRealConnect", "TestAuth_DartOfflineScopedChangesRealConnect")
        package = "github.com/anaregdesign/lantern/tests/integration"
        complete = [{"Package": package, "Test": name, "Action": action}
                    for name in names for action in ("run", "pass")]
        complete.append({"Package": package, "Action": "pass"})
        variants = [[], complete[:-1], complete + [complete[1]],
                    [event for event in complete if event.get("Test") != names[1]],
                    [{**event, "Action": "skip"} if event.get("Test") == names[0]
                     and event["Action"] == "pass" else event for event in complete]]
        with tempfile.TemporaryDirectory() as directory:
            events = Path(directory) / "events.jsonl"
            def verify(records):
                events.write_text("".join(json.dumps(event) + "\n" for event in records))
                with patch.object(sys, "argv", ["app-wire-events", str(events)]):
                    exec(compile(source, "<app-wire-events>", "exec"), {})
            verify(complete)
            for records in variants:
                with self.subTest(records=records), self.assertRaises(SystemExit):
                    verify(records)
        self.assertIn('"tests/integration/auth_authority_helpers_test.go"', workflow("dart-sdk"))
        self.assertNotIn("TestAuth_OIDCNodeScopedChangesFacadeRealConnect", workflow("node-sdk"))
        self.assertNotIn("TestAuth_OIDCRustScopedChangesFacadeRealConnect", workflow("rust-sdk"))

    def test_comparison_probe_is_manual_and_retains_both_candidates(self):
        text = workflow("dart-transport-probe")
        events = text.split("on:\n", 1)[1].split("concurrency:\n", 1)[0]
        self.assertEqual(events.strip(), "workflow_dispatch:")
        self.assertIn("transport: [connect, grpc]", job(text, "ios"))
        self.assertIn("Regenerate and check drift", job(text, "dart"))
        production = workflow("dart-sdk")
        for key in ("test", "offline-test", "offline-sqlite", "android", "ios"):
            self.assertIn(key, job(production, "gate"))
        for key in ("android", "ios"):
            self.assertIn("needs.changes.outputs.mobile == 'true'", job(production, key))

    def test_cancellation_is_limited_to_superseded_prs(self):
        for name in ("rust-sdk", "node-sdk", "admin"):
            with self.subTest(workflow=name):
                text = workflow(name)
                group = next(line for line in text.splitlines() if line.startswith("  group:"))
                self.assertEqual(group, "  group: ${{ github.workflow }}-${{ github.event.pull_request.number || github.run_id }}")
                self.assertIn("  cancel-in-progress: ${{ github.event_name == 'pull_request' }}", text)
        # A Node tag must still qualify both runtimes and source before publishing.
        self.assertIn("needs: [test, configured-source]", job(workflow("node-sdk"), "publish"))


if __name__ == "__main__":
    unittest.main()
