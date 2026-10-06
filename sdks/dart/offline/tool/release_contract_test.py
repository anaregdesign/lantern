import io
import hashlib
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch
from urllib.error import HTTPError

import release_contract


PUBSPEC = b"""name: lantern_client_offline
version: 0.6.0
dependencies:
  lantern_client: ^0.5.0
dev_dependencies:
  test: 1.31.2
"""


def archive_file(path, files):
    with tarfile.open(path, mode="w:gz") as archive:
        for name, body in files.items():
            entry = tarfile.TarInfo(name)
            entry.size = len(body)
            archive.addfile(entry, io.BytesIO(body))
    return path


class ReleaseContractTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.archive = Path(self.temp.name) / "candidate.tar.gz"
        self.files = {name: b"x" for name in release_contract.ROOT_FILES}
        self.files["pubspec.yaml"] = PUBSPEC
        self.files["lib/lantern_client_offline.dart"] = b"library lantern_client_offline;\n"

    def check(self, files=None):
        archive_file(self.archive, files or self.files)
        release_contract.validate(self.archive, "0.6.0", "0.5.0")

    def test_storage_neutral_archive_passes(self):
        self.check()

    def test_repository_files_are_rejected(self):
        for name in ("test/fixture.dart", "tool/evidence.json", "offline_sqlite/lib/store.dart", "device-id.txt", "lib/credential.txt"):
            with self.subTest(name=name), self.assertRaisesRegex(ValueError, "repository-only"):
                self.check(self.files | {name: b"private"})

    def test_path_and_platform_runtime_dependencies_are_rejected(self):
        for pubspec in (
            PUBSPEC.replace(b"^0.5.0", b"\n    path: .."),
            PUBSPEC.replace(b"dev_dependencies:", b"  sqflite: ^2.4.4\ndev_dependencies:"),
            PUBSPEC.replace(b"dev_dependencies:", b"  sqflite2: ^2.4.4\ndev_dependencies:"),
        ):
            with self.subTest(pubspec=pubspec), self.assertRaises(ValueError):
                self.check(self.files | {"pubspec.yaml": pubspec})

    def test_wrong_version_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "version"):
            self.check(self.files | {"pubspec.yaml": PUBSPEC.replace(b"0.6.0", b"0.1.0", 1)})

    def test_old_hosted_parent_is_rejected(self):
        for parent in (b"^0.4.1", b"^0.4.0", b"^0.3.3", b"^0.3.0"):
            with self.subTest(parent=parent), self.assertRaisesRegex(ValueError, "storage-neutral"):
                self.check(self.files | {"pubspec.yaml": PUBSPEC.replace(b"^0.5.0", parent)})


class HostedParentTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.pubspec = self.root / 'pubspec.yaml'
        self.pubspec.write_bytes(PUBSPEC)
        self.candidate = archive_file(self.root / 'parent.tar.gz', {
            'pubspec.yaml': b'name: lantern_client\nversion: 0.5.0\n',
            'lib/client.dart': b'library;\n',
        })
        self.body = self.candidate.read_bytes()
        self.sha256 = hashlib.sha256(self.body).hexdigest()
        self.metadata = {'version': '0.5.0', 'pubspec': {'name': 'lantern_client', 'version': '0.5.0'},
                         'archive_url': 'https://pub.dev/api/archives/lantern_client-0.5.0.tar.gz',
                         'archive_sha256': self.sha256}
        self.lock = self.root / 'pubspec.lock'
        self.lock.write_text(f'''packages:
  lantern_client:
    dependency: "direct main"
    description:
      name: lantern_client
      sha256: "{self.sha256}"
      url: "https://pub.dev"
    source: hosted
    version: "0.5.0"
sdks:
  dart: ">=3.11.0 <4.0.0"
''')

    def check(self):
        return release_contract.hosted_parent_state(self.pubspec, self.lock, '0.5.0', self.candidate, self.sha256)

    def hosted(self):
        return patch.object(release_contract.release, 'fetch', side_effect=[json.dumps(self.metadata).encode(), self.body])

    def test_verified_parent_and_lock_are_ready_but_not_offline_qualification(self):
        with self.hosted():
            state, reason = self.check()
        self.assertEqual(state, 'ready')
        self.assertIn('offline archive still needs qualification', reason)

    def test_missing_parent_is_truthfully_pending_and_changes_no_lock(self):
        before = self.lock.read_bytes()
        with patch.object(release_contract.release, 'fetch', return_value=None) as fetch:
            self.assertEqual(self.check()[0], 'pending')
        self.assertEqual(fetch.call_count, 1)
        self.assertEqual(self.lock.read_bytes(), before)

    def test_stale_or_missing_lock_is_pending_after_archive_equality(self):
        self.lock.write_text(self.lock.read_text().replace('version: "0.5.0"', 'version: "0.4.1"'))
        for missing in (False, True):
            if missing:
                self.lock.unlink()
            with self.hosted() as fetch:
                self.assertEqual(self.check()[0], 'pending')
                self.assertEqual(fetch.call_count, 2)

    def test_archive_failure_or_mismatch_cannot_hide_behind_stale_lock(self):
        self.lock.write_text(self.lock.read_text().replace('version: "0.5.0"', 'version: "0.4.1"'))
        changed = archive_file(self.root / 'changed.tar.gz', {'pubspec.yaml': b'changed'}).read_bytes()
        for body in (None, b'corrupt', changed):
            metadata = dict(self.metadata)
            if body == changed:
                metadata['archive_sha256'] = hashlib.sha256(body).hexdigest()
            with self.subTest(body=body), patch.object(release_contract.release, 'fetch', side_effect=[
                json.dumps(metadata).encode(), body
            ]), self.assertRaises(ValueError):
                self.check()

    def test_transport_failure_is_not_pending(self):
        for status in (401, 403, 429, 503):
            with self.subTest(status=status), patch.object(release_contract.release, 'fetch',
                side_effect=HTTPError('https://pub.dev', status, 'failed', {}, None)), self.assertRaises(HTTPError):
                self.check()

    def test_source_or_lock_override_and_wrong_checksum_fail(self):
        original = self.lock.read_text()
        for text in (original.replace('source: hosted', 'source: path'),
                     original.replace('https://pub.dev', 'https://example.com'),
                     original.replace(self.sha256, '0' * 64)):
            self.lock.write_text(text)
            with self.subTest(text=text), self.hosted(), self.assertRaises(ValueError):
                self.check()
        self.pubspec.write_bytes(PUBSPEC.replace(b'^0.5.0', b'\n    path: ..'))
        with self.assertRaises(ValueError):
            self.check()

    def test_pending_is_reported_but_strict_offline_boundary_fails(self):
        output = self.root / 'output'
        summary = self.root / 'summary'
        args = ['release_contract.py', '--check-parent', '--parent-version', '0.5.0',
                '--parent-candidate', str(self.candidate), '--parent-sha256', self.sha256,
                '--pubspec', str(self.pubspec), '--lockfile', str(self.lock),
                '--output', str(output), '--summary', str(summary)]
        for required in (False, True):
            with patch.object(release_contract.sys, 'argv', args + (['--require-hosted'] if required else [])), \
                 patch.object(release_contract.release, 'fetch', return_value=None):
                if required:
                    with self.assertRaisesRegex(ValueError, 'pending'):
                        release_contract.main()
                else:
                    release_contract.main()
        self.assertEqual(output.read_text(), 'parent_ready=false\n' * 2)
        self.assertIn('publish and verify it first', summary.read_text())


if __name__ == "__main__":
    unittest.main()
