import io
from pathlib import Path
import tarfile
import tempfile
import unittest

import release_contract


PUBSPEC = b"""name: lantern_client_offline
version: 0.3.0
dependencies:
  lantern_client: ^0.3.0
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
        release_contract.validate(self.archive, "0.3.0", "0.3.0")

    def test_storage_neutral_archive_passes(self):
        self.check()

    def test_repository_files_are_rejected(self):
        for name in ("test/fixture.dart", "tool/evidence.json", "offline_sqlite/lib/store.dart", "device-id.txt", "lib/credential.txt"):
            with self.subTest(name=name), self.assertRaisesRegex(ValueError, "repository-only"):
                self.check(self.files | {name: b"private"})

    def test_path_and_platform_runtime_dependencies_are_rejected(self):
        for pubspec in (
            PUBSPEC.replace(b"^0.3.0", b"\n    path: .."),
            PUBSPEC.replace(b"dev_dependencies:", b"  sqflite: ^2.4.4\ndev_dependencies:"),
            PUBSPEC.replace(b"dev_dependencies:", b"  sqflite2: ^2.4.4\ndev_dependencies:"),
        ):
            with self.subTest(pubspec=pubspec), self.assertRaises(ValueError):
                self.check(self.files | {"pubspec.yaml": pubspec})

    def test_wrong_version_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "version"):
            self.check(self.files | {"pubspec.yaml": PUBSPEC.replace(b"0.3.0", b"0.1.0", 1)})


if __name__ == "__main__":
    unittest.main()
