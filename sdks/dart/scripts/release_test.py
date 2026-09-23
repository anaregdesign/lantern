import hashlib
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch
from urllib.error import HTTPError

import release

VERSION = "0.2.0"


def archive(files, mtime=0):
    stream = io.BytesIO()
    with tarfile.open(fileobj=stream, mode="w:gz") as output:
        for name, content in files:
            entry = tarfile.TarInfo(name)
            entry.size = len(content)
            entry.mtime = mtime
            output.addfile(entry, io.BytesIO(content))
    return stream.getvalue()


class ReleaseTest(unittest.TestCase):
    def setUp(self):
        self.files = [("pubspec.yaml", b"name: lantern_client\nversion: 0.2.0\n"),
                      ("lib/client.dart", b"library;\n")]
        self.body = archive(self.files)
        self.candidate = release.archive_files(self.body)
        self.metadata = {
            "version": VERSION,
            "pubspec": {"name": release.PACKAGE, "version": VERSION},
            "archive_url": f"https://pub.dev/api/archives/lantern_client-{VERSION}.tar.gz",
            "archive_sha256": hashlib.sha256(self.body).hexdigest(),
        }
        self.package = json.dumps({"name": release.PACKAGE}).encode()

    def test_existing_version_is_checked_before_skip(self):
        with patch.object(release, "fetch", side_effect=[
            self.package, json.dumps(self.metadata).encode(), self.body
        ]) as fetch:
            self.assertFalse(release.preflight(VERSION, self.candidate))
            self.assertEqual(fetch.call_count, 3)

    def test_existing_package_and_missing_version_needs_publish(self):
        with patch.object(release, "fetch", side_effect=[self.package, None]):
            self.assertTrue(release.preflight(VERSION, self.candidate))

    def test_package_404_blocks_even_if_version_might_exist(self):
        with patch.object(release, "fetch", return_value=None) as fetch:
            with self.assertRaisesRegex(ValueError, "package returned 404"):
                release.preflight(VERSION, self.candidate)
            self.assertEqual(fetch.call_count, 1)

    def test_missing_version_never_verifies(self):
        with patch.object(release, "fetch", return_value=None), patch.object(release.time, "sleep"):
            with self.assertRaisesRegex(ValueError, "did not become visible"):
                release.verify(VERSION, self.candidate, attempts=2)

    def test_delayed_visibility_still_checks_archive(self):
        with patch.object(release, "fetch", side_effect=[
            None, json.dumps(self.metadata).encode(), self.body
        ]), patch.object(release.time, "sleep") as sleep:
            release.verify(VERSION, self.candidate, attempts=2)
            sleep.assert_called_once_with(10)

    def test_tar_order_and_timestamps_do_not_change_package_equality(self):
        body = archive(list(reversed(self.files)), mtime=12345)
        metadata = dict(self.metadata, archive_sha256=hashlib.sha256(body).hexdigest())
        with patch.object(release, "fetch", return_value=body):
            release.compare_published(VERSION, metadata, self.candidate)

    def test_different_missing_and_extra_files_fail(self):
        for files in [self.files[:1], self.files + [("extra", b"x")],
                      [self.files[0], ("lib/client.dart", b"changed")]]:
            with self.subTest(files=files):
                body = archive(files)
                metadata = dict(self.metadata, archive_sha256=hashlib.sha256(body).hexdigest())
                with patch.object(release, "fetch", return_value=body):
                    with self.assertRaisesRegex(ValueError, "differs from candidate"):
                        release.compare_published(VERSION, metadata, self.candidate)

    def test_untrusted_or_missing_archive_and_wrong_checksum_fail(self):
        cases = [
            (dict(self.metadata, archive_url="https://example.com/package.tar.gz"), self.body, "URL"),
            (dict(self.metadata, archive_sha256="invalid"), self.body, "checksum"),
            (self.metadata, None, "404"),
            (self.metadata, b"corrupted", "checksum"),
        ]
        for metadata, body, message in cases:
            with self.subTest(message=message), patch.object(release, "fetch", return_value=body):
                with self.assertRaisesRegex(ValueError, message):
                    release.compare_published(VERSION, metadata, self.candidate)

    def test_metadata_identity_must_match(self):
        for field in ["version", "pubspec"]:
            metadata = dict(self.metadata)
            metadata[field] = "0.1.0" if field == "version" else {"name": "other", "version": VERSION}
            with self.subTest(field=field), patch.object(release, "fetch", return_value=json.dumps(metadata).encode()):
                with self.assertRaisesRegex(ValueError, "does not match"):
                    release.version_metadata(VERSION)

    def test_unsafe_duplicate_and_link_archive_entries_fail(self):
        for name in ["../outside", "/absolute", "a/../../outside", "a\\b", "a//b", "pubspec.yaml"]:
            with self.subTest(name=name), self.assertRaisesRegex(ValueError, "unsafe or duplicate"):
                release.archive_files(archive(self.files + [(name, b"x")]))
        stream = io.BytesIO()
        with tarfile.open(fileobj=stream, mode="w:gz") as output:
            entry = tarfile.TarInfo("link")
            entry.type = tarfile.SYMTYPE
            entry.linkname = "../../outside"
            output.addfile(entry)
        with self.assertRaisesRegex(ValueError, "link or special"):
            release.archive_files(stream.getvalue())

    def test_candidate_digest_binds_artifact_to_preflight(self):
        with tempfile.TemporaryDirectory() as directory:
            candidate = Path(directory) / "candidate.tar.gz"
            candidate.write_bytes(self.body)
            self.assertEqual(release.candidate_files(candidate, hashlib.sha256(self.body).hexdigest()), self.candidate)
            with self.assertRaisesRegex(ValueError, "does not match preflight"):
                release.candidate_files(candidate, "0" * 64)

    def test_http_errors_are_not_treated_as_absence(self):
        for status in [401, 403, 429, 500, 503]:
            error = HTTPError(release.API, status, "failed", {}, None)
            with self.subTest(status=status), patch.object(release, "urlopen", side_effect=error):
                with self.assertRaises(HTTPError):
                    release.fetch(release.API)
        with patch.object(release, "urlopen", side_effect=HTTPError(release.API, 404, "missing", {}, None)):
            self.assertIsNone(release.fetch(release.API))


if __name__ == "__main__":
    unittest.main()
