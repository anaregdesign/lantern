import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

import go_modules


ROOT = Path(__file__).resolve().parents[1]


class GoModulesTest(unittest.TestCase):
    def execute(self, failure: str):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            for module in go_modules.MODULES:
                (root / module).mkdir(exist_ok=True, parents=True)
            stub = root / "go"
            stub.write_text(f"#!{sys.executable}\n" + '''\
import json, os
from pathlib import Path
import sys
root = Path(os.environ['FIXTURE_ROOT'])
module = str(Path.cwd().relative_to(root))
args = sys.argv[1:]
with (root / 'calls.jsonl').open('a') as out:
    out.write(json.dumps([module, args]) + '\\n')
if args[0] == 'test':
    profile = next(a.split('=', 1)[1] for a in args if a.startswith('-coverprofile='))
    Path(profile).write_text('mode: atomic\\nfixture.go:1.1,1.2 1 1\\n')
sys.exit(1 if f"{module}:{args[0]}" in os.environ['FAILURES'].split(',') else 0)
''')
            stub.chmod(0o755)
            result = subprocess.run([sys.executable, "-B", str(ROOT / "scripts/go_modules.py")],
                                    cwd=root, capture_output=True, text=True,
                                    env=os.environ | {"PATH": f"{root}:{os.environ['PATH']}",
                                                      "FIXTURE_ROOT": str(root), "FAILURES": failure,
                                                      "GITHUB_STEP_SUMMARY": str(root / "summary.md")})
            calls = [json.loads(s) for s in (root / "calls.jsonl").read_text().splitlines()]
            records = json.loads((root / "coverage/modules.json").read_text())
            self.assertEqual(len(calls), 18)
            self.assertEqual(len(records), 18)
            self.assertEqual([(r["phase"], r["module"]) for r in records],
                             [(p, m) for p in ("vet", "build", "test") for m in go_modules.MODULES])
            for module, args in calls:
                self.assertEqual(args[-1], "./...")
                if args[0] == "test":
                    self.assertEqual(args[1:5], ["-race", "-shuffle=on", "-coverpkg=./...", "-covermode=atomic"])
            self.assertEqual((root / "coverage.out").read_bytes(), (root / "coverage/root.out").read_bytes())
            self.assertIn("| server | test |", (root / "summary.md").read_text())
            return result, records

    def test_root_and_middle_failures_do_not_hide_later_modules_or_phases(self):
        for failures in (".:vet", ".:build", ".:test", "core:test,sdks/go:vet,server:build"):
            with self.subTest(failures=failures):
                result, records = self.execute(failures)
                self.assertEqual(result.returncode, 1, result.stderr)
                self.assertTrue(any(r["exit"] for r in records))
                self.assertIn("go test (server)", result.stdout)

    def test_all_success(self):
        result, records = self.execute("")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(all(r["exit"] == 0 for r in records))

    def test_required_check_and_floor_contract_remain_blocking(self):
        workflow = (ROOT / "workflows/go.yml").read_text()
        self.assertIn("    name: Build & Test\n", workflow)
        self.assertIn('floors="root:31 core:84 mcp:84 pb:15 sdks-go:37 server:52"', workflow)
        self.assertNotIn("continue-on-error", workflow)
        self.assertIn("run: python3 -B .github/scripts/go_modules.py", workflow)
        # Coverage still runs after a failed collector, and absent profiles fail.
        for name in ("Merge per-module coverage", "Enforce coverage floors"):
            body = workflow.split(f"      - name: {name}\n", 1)[1]
            self.assertIn("!cancelled()", body.splitlines()[0])
            self.assertIn("steps.modules.outcome", body.splitlines()[0])
        floor = workflow.split("      - name: Enforce coverage floors\n", 1)[1]
        self.assertIn('if [ ! -f "$prof" ]; then', floor)
        self.assertIn('if [ "$fail" -ne 0 ]; then', floor)
        self.assertIn("exit 1", floor)


if __name__ == "__main__":
    unittest.main()
