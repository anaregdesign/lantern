"""Exercise fixture isolation, finite required cases and source/evidence binding."""

import json
from contextlib import redirect_stdout
import io
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import native_login_gate as gate
from local_gate_plan import steps
from git_test_fixture import synchronous_git_maintenance


class NativeLoginGateTest(unittest.TestCase):
    def execute(self, mode):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory).resolve()
            root, binaries = base / "repo", base / "bin"
            root.mkdir()
            binaries.mkdir()
            (root / "source").write_text("original")
            subprocess.run(["git", "init", "-q"], cwd=root, check=True)
            synchronous_git_maintenance(root)
            for command in (["git", "add", "source"],
                            ["git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture"]):
                subprocess.run(command, cwd=root, check=True)
            log = "\n".join(f"--- PASS: {gate.ROOT_TEST}/{case} (0.01s)" for case in gate.CASES)
            log += f"\n--- PASS: {gate.ROOT_TEST} (0.1s)\nPASS\n"
            script = f"#!{sys.executable}\n" + '''\
import json, os
from pathlib import Path
import sys
name, args = Path(sys.argv[0]).name, sys.argv[1:]
with open(os.environ['CALLS'], 'a') as out:
    out.write(json.dumps([name, args]) + '\\n')
if name == 'go':
    Path(args[args.index('-o') + 1]).write_text('test binary')
elif args[:2] == ['image', 'inspect']:
    print('sha256:fixture')
elif args[0] == 'run':
    text = os.environ['CASE_LOG']
    mode = os.environ['MODE']
    if mode == 'missing':
        text = '\\n'.join(text.splitlines()[1:])
    if mode == 'skip':
        text += '\\n--- SKIP: fixture'
    if mode == 'changed':
        Path('source').write_text('changed while executing')
    print(text)
    if mode == 'failure':
        sys.exit(1)
'''
            for name in ("go", "docker"):
                path = binaries / name
                path.write_text(script)
                path.chmod(0o755)
            with patch.dict(os.environ, {"PATH": str(binaries) + os.pathsep + os.environ["PATH"],
                                         "CALLS": str(base / "calls"), "MODE": mode, "CASE_LOG": log}):
                destinations = [base / "evidence"]
                if mode == "repeat":
                    destinations = []
                    saved = {}
                    for _ in range(2):
                        output = io.StringIO()
                        with patch.object(gate, "__file__", str(root / ".github/scripts/native_login_gate.py")), redirect_stdout(output):
                            gate.main(["--fresh-child", "--evidence", str(base / "evidence")])
                        receipt_path = Path(json.loads(output.getvalue())["receipt"])
                        destinations.append(receipt_path.parent)
                        saved[receipt_path] = receipt_path.read_bytes()
                    self.assertEqual(len(set(destinations)), 2)
                    self.assertTrue(all(p.parent == base / "evidence" for p in destinations))
                    for path, original in saved.items():
                        self.assertEqual(path.read_bytes(), original)
                    # Explicit single-run destinations still refuse replacement.
                    with self.assertRaises(FileExistsError):
                        gate.run(root, destinations[0])
                    self.assertEqual(next(iter(saved)).read_bytes(), next(iter(saved.values())))
                elif mode == "pass":
                    gate.run(root, base / "evidence")
                else:
                    with self.assertRaises(RuntimeError):
                        gate.run(root, base / "evidence")
            receipt = json.loads((destinations[-1] / "receipt.json").read_text())
            self.assertEqual(receipt["status"], "PASS" if mode in ("pass", "repeat") else "FAIL")
            calls = [json.loads(row) for row in (base / "calls").read_text().splitlines()]
            self.assertEqual([c[1] for c in calls if c[0] == "go"], [
                ["test", "-c", "-tags=lantern_test_time", "-o", str(destination / "build" / binary), package]
                for destination in destinations
                for package, binary in (("./server/internal/security", "security.test"), ("./tests/integration", "integration.test"))])
            run = next(args for name, args in reversed(calls) if name == "docker" and args[0] == "run")
            for flag in ("--network=none", "--read-only", "--cap-drop=ALL", "--pull=never",
                         "--security-opt=no-new-privileges", "--memory=2g", "--cpus=4", "--pids-limit=256"):
                self.assertIn(flag, run)
            self.assertNotIn("--privileged", run)
            self.assertNotIn("--mount", run)
            self.assertEqual(run[run.index("--user") + 1], "65534:65534")
            self.assertEqual([run[i + 1] for i, arg in enumerate(run) if arg == "--add-host"],
                             [host + ":127.0.0.1" for host in gate.HOSTS])
            name = run[run.index("--name") + 1]
            self.assertEqual(calls[-2:], [["docker", ["rm", "--force", name]], ["docker", ["image", "rm", name]]])
            self.assertEqual(receipt["development_changes"], False)
            self.assertEqual(len(receipt["source_sha256"]), 64)
            self.assertTrue(all(len(command["sha256"]) == 64 for command in receipt["commands"]))

    def test_pass_and_failures_preserve_isolation_and_cleanup(self):
        for mode in ("pass", "missing", "skip", "changed", "failure"):
            with self.subTest(mode=mode):
                self.execute(mode)

    def test_repeated_local_invocations_preserve_distinct_evidence(self):
        self.execute("repeat")

    def test_native_fixture_is_fresh_and_required_by_local_and_hosted_gates(self):
        plan = steps(Path("/tmp/evidence"))
        native = [step for step in plan if step.name == "native-login-wire"]
        self.assertEqual(len(native), 1)
        self.assertEqual(native[0].command, ("python3", "-B", ".github/scripts/native_login_gate.py", "--fresh-child", "--evidence", "/tmp/evidence/native-login"))
        workflow = (Path(__file__).resolve().parents[1] / "workflows/go.yml").read_text()
        body = workflow.split("      - name: Native provider session wire fixture\n", 1)[1].split("      - name:", 1)[0]
        self.assertIn("steps.toolchain.outcome == 'success'", body)
        self.assertIn('native_login_gate.py --evidence "$RUNNER_TEMP/native-login"', body)
        self.assertNotIn("continue-on-error", body)


if __name__ == "__main__":
    unittest.main()
