"""Keep the seven local-authority cases mandatory on supported native hosts."""

import unittest

import rust_wire


class RustWireTest(unittest.TestCase):
    def test_platform_mapping_preserves_compilation_and_supported_oidc_execution(self):
        for system in ("Linux", "Darwin"):
            command = rust_wire.command(system)
            self.assertEqual(command[:8], ["cargo", "test", "--locked", "--all-features", "--", "--ignored", "--test-threads=1", "--skip"])
            skipped = [command[index + 1] for index, part in enumerate(command) if part == "--skip"]
            self.assertEqual(skipped, list(rust_wire.ROOT_FIXTURES))
            self.assertNotIn("transport::tests::real_wire_off_mtls_requires_client_identity", skipped)
        with self.assertRaises(ValueError):
            rust_wire.command("Windows")


if __name__ == "__main__":
    unittest.main()
