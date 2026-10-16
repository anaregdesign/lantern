"""Exercise real Git maintenance behind a bounded process-lifetime barrier."""

import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest

from git_test_fixture import synchronous_git_maintenance


class GitFixtureTest(unittest.TestCase):
    def wait_until(self, predicate):
        deadline = time.monotonic() + 15
        while not predicate():
            if time.monotonic() >= deadline:
                self.fail("Git maintenance barrier timed out")
            time.sleep(0.01)

    def test_commit_joins_maintenance_before_temporary_directory_cleanup(self):
        for synchronous in (False, True):
            with self.subTest(synchronous=synchronous), tempfile.TemporaryDirectory() as raw:
                base = Path(raw)
                root = base / "repo"
                root.mkdir()

                def git(*args, **kwargs):
                    return subprocess.check_output(["git", *args], cwd=root,
                                                   stderr=subprocess.DEVNULL, **kwargs)

                git("init", "-q")
                for key, value in {
                    "user.name": "Fixture", "user.email": "fixture@example.invalid",
                    "core.hooksPath": str(root / ".git/hooks"),
                    "maintenance.strategy": "gc", "gc.auto": "1", "gc.autoPackLimit": "1",
                    "maintenance.autoDetach": "true", "gc.autoDetach": "true",
                }.items():
                    git("config", "--local", key, value)
                if synchronous:
                    synchronous_git_maintenance(root)

                # Trigger actual auto-maintenance deterministically, without a
                # probabilistic loose-object count or a repeated test-to-pass loop.
                for i in range(2):
                    oid = git("hash-object", "-w", "--stdin", input=f"fixture {i}".encode())
                    git("pack-objects", ".git/objects/pack/pack", input=oid)
                hook = root / ".git/hooks/pre-auto-gc"
                hook.write_text(f"#!{sys.executable}\n" + '''\
import os
from pathlib import Path
import time
base = Path(os.environ["FIXTURE_MAINTENANCE_BARRIER"])
counter = base / "calls"
count = int(counter.read_text()) + 1 if counter.exists() else 1
counter.write_text(str(count))
# Current Git checks GC once in the foreground, then again after detaching.
if count == 2:
    (base / "worker-pid").write_text(str(os.getppid()))
    (base / "entered").touch()
    deadline = time.monotonic() + 15
    while not (base / "release").exists():
        if time.monotonic() >= deadline:
            raise RuntimeError("maintenance release timed out")
        time.sleep(0.01)
    (base / "finished").touch()
''')
                hook.chmod(0o755)
                env = os.environ | {"FIXTURE_MAINTENANCE_BARRIER": str(base)}
                with subprocess.Popen(["git", "commit", "--allow-empty", "-qm", "fixture"],
                                      cwd=root, env=env, stdout=subprocess.DEVNULL,
                                      stderr=subprocess.DEVNULL) as process:
                    try:
                        self.wait_until(lambda: (base / "entered").exists())
                        if synchronous:
                            self.assertIsNone(process.poll(), "commit returned before its writer")
                        else:
                            self.assertEqual(process.wait(timeout=15), 0)
                        self.assertFalse((base / "finished").exists())
                    finally:
                        (base / "release").touch()
                        self.assertEqual(process.wait(timeout=15), 0)
                        if (base / "entered").exists():
                            self.wait_until(lambda: (base / "finished").exists())
                            # The detached negative control is also joined before
                            # real TemporaryDirectory cleanup; no error is ignored.
                            pid = int((base / "worker-pid").read_text())
                            def exited():
                                try:
                                    os.kill(pid, 0)
                                except ProcessLookupError:
                                    return True
                                return False
                            self.wait_until(exited)
                self.assertFalse((root / ".git/objects/maintenance.lock").exists())
                self.assertFalse((root / ".git/gc.pid").exists())


if __name__ == "__main__":
    unittest.main()
