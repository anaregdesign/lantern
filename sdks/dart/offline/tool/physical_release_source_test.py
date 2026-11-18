import subprocess
import tempfile
import unittest
from pathlib import Path

from physical_release_source import check_capture_source


class CaptureSourceTest(unittest.TestCase):
    def test_only_post_capture_reader_edits_can_keep_original_signed_results(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)

            def git(*args):
                return subprocess.check_output(["git", "-C", directory, *args], text=True).strip()

            def commit():
                git("add", ".")
                git("-c", "user.name=fixture", "-c", "user.email=test@example.invalid",
                    "commit", "-qm", "fixture")
                return git("rev-parse", "HEAD")

            git("init", "-q")
            reader = root / "sdks/dart/scripts/physical_contribution_release_gate_test.py"
            reader.parent.mkdir(parents=True)
            reader.write_text("before\n")
            tested = commit()
            reader.write_text("corrected byte fixture\n")
            changed = commit()
            check_capture_source(tested, changed, root)
            with self.assertRaises(ValueError):
                check_capture_source(changed, tested, root)
            (root / "pubspec.yaml").write_text("version: 2\n")
            with self.assertRaisesRegex(ValueError, "device inputs changed"):
                check_capture_source(tested, commit(), root)
            git("checkout", "-q", changed)
            reader.unlink()
            with self.assertRaisesRegex(ValueError, "device inputs changed"):
                check_capture_source(tested, commit(), root)
