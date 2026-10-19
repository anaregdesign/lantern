"""Keep the seven local-authority cases mandatory on supported native hosts."""

from pathlib import Path
import unittest

import rust_wire


class RustWireTest(unittest.TestCase):
    def test_platform_mapping_preserves_compilation_and_supported_oidc_execution(self):
        self.assertEqual(len(rust_wire.LOCAL_OIDC), 7)
        self.assertEqual(len(set(rust_wire.LOCAL_OIDC)), 7)
        root = Path(__file__).resolve().parents[2]
        for name in rust_wire.LOCAL_OIDC:
            module, _, case = name.split("::")
            source = (root / f"sdks/rust/src/{module}/tests.rs").read_text()
            self.assertIn("async fn " + case + "()", source)
        for system in ("Linux", "Darwin", "Windows"):
            command = rust_wire.command(system)
            self.assertEqual(command[:8], ["cargo", "test", "--locked", "--all-features", "--", "--ignored", "--test-threads=1", "--skip"])
            skipped = [command[index + 1] for index, part in enumerate(command) if part == "--skip"]
            self.assertEqual(skipped, list(rust_wire.ROOT_FIXTURES + (rust_wire.LOCAL_OIDC if system == "Windows" else ())))
            self.assertNotIn("transport::tests::real_wire_off_mtls_requires_client_identity", skipped)
        with self.assertRaises(ValueError):
            rust_wire.command("Other")


if __name__ == "__main__":
    unittest.main()
