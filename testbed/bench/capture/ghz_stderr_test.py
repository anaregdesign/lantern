import hashlib
import io
import json
import os
from pathlib import Path
import stat
import tempfile
import unittest
from unittest import mock

import ghz_stderr


class ObservedInput(io.BytesIO):
    def __init__(self, data):
        super().__init__(data)
        self.eof = False
        self.bytes_read = 0

    def read(self, size=-1):
        data = super().read(size)
        self.bytes_read += len(data)
        self.eof = not data
        return data


class GhzStderrTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.directory.chmod(0o700)
        self.output = self.directory / "private.json"
        self.binary = self.directory / "fixture-ghz"
        self.binary.write_bytes(b"observed binary fixture")

    def run_capture(self, data, output=None):
        stream, errors = ObservedInput(data), io.StringIO()
        code = ghz_stderr.main([str(output or self.output), str(self.binary)], stream, errors)
        self.assertTrue(stream.eof)
        self.assertEqual(stream.bytes_read, len(data))
        return code, errors.getvalue()

    def test_fixed_families_codes_and_unknown_lines(self):
        data = (b"metadata reflection service descriptor dial connect\n"
                b"rpc error: code = Unavailable desc = context deadline exceeded\n"
                b"connection refused; tls: x509 failure\nunknown content\n")
        self.assertEqual(self.run_capture(data), (0, ""))
        result = json.loads(self.output.read_text())
        self.assertEqual(result["observed_stages"], list(ghz_stderr.STAGES))
        self.assertEqual(result["observed_classes"], list(ghz_stderr.CLASSES) + ["grpc_status"])
        self.assertEqual(result["grpc_codes"], ["Unavailable"])
        self.assertEqual(result["unclassified_line_count"], 1)
        self.assertFalse(result["raw_stderr_saved"])
        self.assertFalse(result["truncated"])
        self.assertEqual(result["observed_stderr_bytes"], len(data))
        self.assertEqual(result["observed_ghz_binary_sha256"],
                         hashlib.sha256(self.binary.read_bytes()).hexdigest())
        self.assertEqual(stat.S_IMODE(self.output.stat().st_mode), 0o600)

    def test_secrets_urls_paths_payloads_and_unknown_codes_are_never_saved(self):
        secret = b"SECRET-TOKEN user@example.test https://private.test/path /private/file payload"
        self.assertEqual(self.run_capture(secret + b"\ncode = NotARealCode\n"), (0, ""))
        encoded = self.output.read_bytes()
        for value in secret.split() + [str(self.binary).encode(), b"NotARealCode"]:
            self.assertNotIn(value, encoded)
        result = json.loads(encoded)
        self.assertEqual(result["grpc_codes"], [])
        self.assertEqual(result["stage"], "unclassified")
        self.assertEqual(result["class"], "unclassified")
        self.assertEqual(result["unclassified_line_count"], 2)

    def test_cap_preserves_prefix_only_and_drains_beyond_cap(self):
        data = b"x" * ghz_stderr.CAP + b"\nmetadata code = Internal\n" * 10000
        self.assertEqual(self.run_capture(data), (0, ""))
        result = json.loads(self.output.read_text())
        self.assertEqual(result["captured_prefix_bytes"], ghz_stderr.CAP)
        self.assertEqual(result["observed_stderr_bytes"], len(data))
        self.assertTrue(result["truncated"])
        self.assertEqual(result["grpc_codes"], [])
        self.assertEqual(result["observed_stages"], [])

    def test_output_failure_is_fixed_and_after_eof(self):
        stream, errors = ObservedInput(b"SECRET\n" * 50000), io.StringIO()

        def reject(*args):
            self.assertTrue(stream.eof)
            raise OSError("SECRET https://private.test/raw")

        with mock.patch.object(ghz_stderr, "write_private", side_effect=reject):
            self.assertEqual(ghz_stderr.main([str(self.output), str(self.binary)], stream, errors), 1)
        self.assertEqual(errors.getvalue(), "write_failure\n")
        self.assertFalse(self.output.exists())

    def test_private_output_rejects_existing_symlink_and_nonprivate_parent(self):
        self.output.write_text("preserve")
        self.assertEqual(self.run_capture(b"x"), (1, "write_failure\n"))
        self.assertEqual(self.output.read_text(), "preserve")
        self.output.unlink()
        self.output.symlink_to(self.binary)
        self.assertEqual(self.run_capture(b"x"), (1, "write_failure\n"))
        self.assertEqual(self.binary.read_bytes(), b"observed binary fixture")
        self.output.unlink()
        self.directory.chmod(0o755)
        self.assertEqual(self.run_capture(b"x"), (1, "write_failure\n"))
        self.assertFalse(self.output.exists())

    def test_empty_stderr_and_saturating_byte_count(self):
        self.assertEqual(self.run_capture(b""), (0, ""))
        result = json.loads(self.output.read_text())
        self.assertEqual((result["stage"], result["class"]), ("none", "empty"))
        with mock.patch.object(ghz_stderr, "MAX_BYTES", 3):
            prefix, count, saturated = ghz_stderr.drain(ObservedInput(b"abcdef"))
        self.assertEqual(prefix, b"abcdef")
        self.assertEqual(count, 3)
        self.assertTrue(saturated)

    def test_check_does_not_read_stdin_or_touch_files(self):
        stream = mock.Mock()
        with mock.patch.object(ghz_stderr, "write_private") as write:
            self.assertEqual(ghz_stderr.main(["--check"], stream, io.StringIO()), 0)
        stream.read.assert_not_called()
        write.assert_not_called()
        self.assertFalse(self.output.exists())


if __name__ == "__main__":
    unittest.main()
