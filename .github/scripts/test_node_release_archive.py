import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

from node_release_archive import compare_archives


class NodeReleaseArchiveTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.files = {"package/package.json": json.dumps({
            "name": "lantern-sdk", "version": "0.14.0"}).encode(),
            "package/dist/index.js": b"export const value = 42;\n",
            "package/dist/index.d.ts": b"export declare const value = 42;\n",
            "package/README.md": b"# SDK\n"}
        self.candidate = self.archive("candidate", self.files)

    def archive(self, name, files, *, mtime=0, extra=None, executable=False):
        path = self.root / (name + ".tgz")
        with tarfile.open(path, "w:gz") as archive:
            for key, data in files.items():
                member = tarfile.TarInfo(key)
                member.size, member.mtime = len(data), mtime
                member.mode = 0o755 if executable else 0o644
                archive.addfile(member, io.BytesIO(data))
            if extra is not None:
                archive.addfile(extra)
        return path

    def test_same_payload_allows_archive_order_and_timestamp_differences(self):
        actual = self.archive("published", dict(reversed(list(self.files.items()))), mtime=123)
        result = compare_archives(self.candidate, actual, "0.14.0")
        self.assertEqual((result["verdict"], result["files"]), ("pass", 4))
        self.assertNotEqual(result["candidate_archive_sha256"], result["published_archive_sha256"])

    def test_changed_runtime_declaration_or_document_is_rejected(self):
        for name in self.files:
            with self.subTest(name=name):
                changed = dict(self.files)
                changed[name] += b" "
                actual = self.archive("changed", changed)
                with self.assertRaisesRegex(ValueError, "file bytes"):
                    compare_archives(self.candidate, actual, "0.14.0")

    def test_missing_extra_or_executable_change_is_rejected(self):
        missing = dict(self.files)
        del missing["package/dist/index.js"]
        extra = {**self.files, "package/.npmrc": b"unexpected"}
        for files, executable in [(missing, False), (extra, False), (self.files, True)]:
            with self.subTest(files=list(files), executable=executable):
                actual = self.archive("changed", files, executable=executable)
                with self.assertRaisesRegex(ValueError, "file set|file bytes"):
                    compare_archives(self.candidate, actual, "0.14.0")

    def test_wrong_package_or_version_is_rejected_even_if_both_match(self):
        for identity in [{"name": "other", "version": "0.14.0"},
                         {"name": "lantern-sdk", "version": "0.13.0"}]:
            with self.subTest(identity=identity):
                wrong = {**self.files, "package/package.json": json.dumps(identity).encode()}
                actual = self.archive("wrong", wrong)
                with self.assertRaisesRegex(ValueError, "identity/version"):
                    compare_archives(actual, actual, "0.14.0")

    def test_duplicates_links_and_non_package_paths_are_rejected(self):
        for name, kind in [("package/dist/index.js", tarfile.REGTYPE),
                           ("package/link", tarfile.SYMTYPE),
                           ("package/link", tarfile.LNKTYPE),
                           ("package/../outside", tarfile.REGTYPE),
                           ("/package/outside", tarfile.REGTYPE),
                           ("outside", tarfile.REGTYPE)]:
            with self.subTest(name=name, kind=kind):
                member = tarfile.TarInfo(name)
                member.type, member.linkname = kind, "package/dist/index.js"
                actual = self.archive("unsafe", self.files, extra=member)
                with self.assertRaises(ValueError):
                    compare_archives(self.candidate, actual, "0.14.0")


if __name__ == "__main__":
    unittest.main()
