"""Guard the representative coverage and event boundaries of the CI reduction."""

from pathlib import Path
import re
import tomllib
import unittest


ROOT = Path(__file__).resolve().parents[2]


def workflow(name):
    return (ROOT / f".github/workflows/{name}.yml").read_text()


def job(text, name):
    return re.search(rf"(?ms)^  {name}:\n(.*?)(?=^  [\w-]+:\n|\Z)", text)[1]


class RepresentativeCoverageTest(unittest.TestCase):
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
                lanes = re.findall(r"- \{os: ([\w-]+), rust: '?([\w.]+)'?\}", matrix)
                self.assertEqual(set(lanes), expected)
                self.assertEqual(len(lanes), len(expected))
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
                            if "--ignored --test-threads=1" in step)
                self.assertIn("if: matrix.rust == 'stable'", wire)
                self.assertNotIn("matrix.os == 'ubuntu-latest'", wire)
        release = workflow("rust-release")
        self.assertIn("needs: [tag, conformance, current-sdk4]", job(release, "preflight"))
        self.assertIn("--proof-kind Q", job(release, "preflight"))
        self.assertIn("needs: [tag, verify-published]", job(release, "release"))

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
