import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import unittest
from unittest.mock import patch
from unittest.mock import Mock
from receipt import verify

import off_source


class SourceSupervisorTest(unittest.TestCase):
    def test_snapshot_binds_actual_clean_bytes(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            subprocess.run(["git", "init", "-q", str(root)], check=True)
            path = root / "file.txt"
            path.write_text("original")
            subprocess.run(["git", "add", "."], cwd=root, check=True)
            subprocess.run(["git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture"], cwd=root, check=True)
            snapshot = off_source.snapshot(root)
            self.assertIn("file.txt", snapshot["inputs"])
            subprocess.run(["git", "update-index", "--assume-unchanged", "file.txt"], cwd=root, check=True)
            path.write_text("hidden change")
            with self.assertRaises(ValueError):
                off_source.snapshot(root)

    def test_runtime_overrides_refuse_without_execution(self):
        with patch.dict(os.environ, {"GOFLAGS": "-overlay=untrusted"}):
            with self.assertRaises(ValueError):
                off_source.checked_environment()

    def test_build_export_uses_git_objects_and_excludes_ignored_inputs(self):
        with tempfile.TemporaryDirectory() as temp:
            root, output = Path(temp)/"repo", Path(temp)/"output"
            output.mkdir()
            subprocess.run(["git", "init", "-q", str(root)], check=True)
            (root/"source.go").write_text("package fixture\n")
            (root/".gitignore").write_text("ignored.go\n")
            subprocess.run(["git", "add", "."], cwd=root, check=True)
            subprocess.run(["git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture"], cwd=root, check=True)
            source = off_source.snapshot(root)
            (root/"source.go").write_text("working mutation")
            (root/"ignored.go").write_text("untrusted ignored input")
            with off_source.tracked_build(root, output, source) as build:
                self.assertEqual((build/"source.go").read_text(), "package fixture\n")
                self.assertFalse((build/"ignored.go").exists())
                self.assertEqual((build/"source.go").stat().st_mode & 0o222, 0)
                self.assertEqual(build.stat().st_mode & 0o222, 0)
            self.assertFalse(build.exists())

    def test_prepare_refuses_dirty_source_before_any_build(self):
        with tempfile.TemporaryDirectory() as temp, patch.object(off_source, "snapshot", side_effect=ValueError("dirty")), patch.object(off_source.subprocess, "run") as execute:
            with self.assertRaises(ValueError):
                off_source.prepare(Path(temp), Path(temp) / "out", "/unused/go")
            execute.assert_not_called()

    def test_run_preconditions_refuse_before_spawning(self):
        with tempfile.TemporaryDirectory() as temp, patch.object(off_source, "environment", side_effect=ValueError("source unavailable")), patch.object(off_source, "Child") as child:
            root = Path(temp)
            with self.assertRaises(ValueError):
                off_source.run(root, root / "prepared", root.parent / (root.name + "-evidence"), {})
            child.assert_not_called()

    def test_prepared_digest_and_artifact_change_refuse(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            source = {"head": "f" * 40}
            manifest = {"schema": off_source.SCHEMA, "source": source, "child_sha256": "wrong", "fixture_sha256": "wrong"}
            raw = json.dumps(manifest).encode()
            (directory / "prepared.json").write_bytes(raw)
            (directory / "off-child.test").write_bytes(b"changed")
            with patch.object(off_source, "snapshot", return_value=source):
                with self.assertRaises(ValueError):
                    off_source.load_prepared(directory, directory, "untrusted")
                with self.assertRaises(ValueError):
                    off_source.load_prepared(directory, directory, off_source.sha(raw))

    def test_explicit_execution_switch_is_required(self):
        with patch("sys.argv", ["off_source.py", "run", "--root", "/unused", "--prepared", "/unused", "--prepared-sha256", "digest", "--evidence", "/unused"]), patch.object(off_source, "load_prepared", return_value={}), patch.object(off_source, "run") as run:
            with self.assertRaises(SystemExit):
                off_source.main()
            run.assert_not_called()

    def test_failed_cohort_consumes_three_slots_and_cannot_restart(self):
        with tempfile.TemporaryDirectory() as temp:
            parent = Path(temp)
            root, prepared, output = parent / "root", parent / "prepared", parent / "evidence"
            root.mkdir()
            prepared.mkdir()
            allocated = []

            class SyntheticChild:
                def __init__(self, binary, slot, nonce, directory, started):
                    self.slot, self.nonce = slot, nonce
                    self.process = Mock(pid=slot, returncode=0)
                    self.process.poll.return_value = 0
                    self.reader, self.stderr = Mock(), Mock()
                    self.records, self.stop_ns, self.launch_ns, self.killed = [], None, slot, False
                    allocated.append(self)

                def send(self, phase, deadline, **fields):
                    if phase == "stop":
                        self.stop_ns = 1

                def receive(self, event, deadline):
                    return {"event": event, "public": f"[::]:{1000+self.slot}", "peer": f"127.0.0.1:{2000+self.slot}"}

            def provision(*args, **kwargs):
                material = output / "material"
                material.mkdir()
                kwargs["stdout"].write(json.dumps({"nodes": [{"environment": {}} for _ in range(3)]}).encode())

            manifest = {"source": {"head": "synthetic"}, "digest": "synthetic", "target": ["linux", "arm64"], "child_sha256": "synthetic"}
            with patch.object(off_source, "environment", return_value={"synthetic_unit_fixture": True}), patch.object(off_source.platform, "machine", return_value="aarch64"), patch.object(off_source, "Child", SyntheticChild), patch.object(off_source.subprocess, "run", side_effect=provision), patch.object(off_source, "wire_smoke", side_effect=ValueError("synthetic wire refusal")), patch.object(off_source.time, "CLOCK_BOOTTIME", 0, create=True), patch.object(off_source.time, "clock_gettime_ns", side_effect=lambda _: time.monotonic_ns()):
                result = off_source.run(root, prepared, output, manifest)
                self.assertEqual(result["status"], "FAIL")
                self.assertEqual([c.slot for c in allocated], [1, 2, 3])
                self.assertEqual(len(result["launch_ledger"]), 3)
                self.assertTrue(all(c.stop_ns is not None for c in allocated))
                self.assertTrue((prepared / "OFF-ALLOCATION-CONSUMED").exists())
                with self.assertRaises(FileExistsError):
                    off_source.run(root, prepared, parent / "second-evidence", manifest)
                self.assertEqual(len(allocated), 3)

    def test_complete_synthetic_cohort_reconciles_all_raw_rows(self):
        with tempfile.TemporaryDirectory() as temp:
            parent = Path(temp)
            root, directory, output = parent / "root", parent / "prepared", parent / "evidence"
            root.mkdir()
            directory.mkdir()
            manifest = {"source": {"head": "a"*40}, "digest": "synthetic", "target": ["linux", "arm64"], "child_sha256": "synthetic"}
            native = {"system": "Linux", "kernel": "synthetic-unit-fixture", "machine": "aarch64", "boot_sha256": "a"*64, "namespace": "time:[1]", "offsets_sha256": "b"*64, "source_path": "/etc/ntp.conf", "source_sha256": off_source.sha(b"server time.asia.apple.com\n"), "source_read_only": True, "capabilities": ["CapEff:\t0"]}

            class SyntheticChild:
                def __init__(self, binary, slot, nonce, directory, started):
                    self.slot, self.nonce, self.directory, self.started = slot, nonce, directory, started
                    self.process = Mock(pid=1000+slot, returncode=0)
                    self.process.poll.return_value = 0
                    self.reader, self.stderr = Mock(), Mock()
                    self.records, self.stop_ns, self.launch_ns, self.killed = [], None, time.monotonic_ns()-started, False

                def send(self, phase, deadline, **fields):
                    if phase == "stop":
                        self.stop_ns = time.monotonic_ns()-self.started

                def receive(self, event, deadline):
                    row = {"event": event, "pid": self.process.pid, "nonce": self.nonce, "elapsed_ns": time.monotonic_ns()-self.started, "received_ns": time.monotonic_ns()-self.started}
                    if event == "reserved": row.update(public=f"[::]:{1000+self.slot}", peer=f"127.0.0.1:{2000+self.slot}")
                    if event == "adopted": row.update(mode="off", initializers=1, revision=manifest["source"]["head"], goos="linux", goarch="arm64")
                    if event in ("probe", "app_returned"): row["ok"] = True
                    if event == "final":
                        counts = {"attempts": 1, "writes": 1, "installations": 1, "in_flight": False, "failed": False, "denied": False}
                        row.update(mode="off", counters=counts, repeat=counts.copy(), closed_refused=True, app_ok=True)
                    self.records.append(row)
                    return row

                def wait(self, deadline, binary_sha256):
                    raw = b"".join(b"off_source "+json.dumps({k:v for k,v in r.items() if k != "received_ns"}).encode()+b"\n" for r in self.records)
                    (self.directory / f"child-{self.slot}.stdout").write_bytes(raw)
                    (self.directory / f"child-{self.slot}.stderr").write_bytes(b"")
                    return {"slot": self.slot, "pid": self.process.pid, "nonce": self.nonce, "exit": 0, "killed": False, "launch_ns": self.launch_ns, "stop_ns": self.stop_ns, "wait_ns": time.monotonic_ns()-self.started, "binary_sha256": binary_sha256, "records": self.records, "stdout_sha256": off_source.sha(raw), "stderr_sha256": off_source.sha(b"")}

            def provision(*args, **kwargs):
                material = output / "material"
                material.mkdir()
                kwargs["stdout"].write(json.dumps({"nodes": [{"environment": {}} for _ in range(3)]}).encode())

            def wire(fixture, children, deadline):
                for child in children:
                    child.receive("probe", deadline)
                return {"public_off": True, "replication": True, "private": True}

            with patch.object(off_source, "environment", return_value=native), patch.object(off_source.platform, "machine", return_value="aarch64"), patch.object(off_source, "snapshot", return_value=manifest["source"]), patch.object(off_source, "Child", SyntheticChild), patch.object(off_source.subprocess, "run", side_effect=provision), patch.object(off_source, "wire_smoke", side_effect=wire), patch.object(off_source.time, "CLOCK_BOOTTIME", 0, create=True), patch.object(off_source.time, "clock_gettime_ns", side_effect=lambda _: time.monotonic_ns()):
                result = off_source.run(root, directory, output, manifest)
            self.assertEqual(result["status"], "PASS", result.get("failure"))
            self.assertEqual(verify(result, manifest, output), {"owners": 3, "attempts": 3, "writes": 3})

    def test_public_probe_sends_no_credentials(self):
        opener = Mock()
        opener.open.return_value.__enter__ = Mock(return_value=Mock(read=Mock(return_value=b'{"status":"SERVING_STATUS_SERVING"}')))
        opener.open.return_value.__exit__ = Mock(return_value=False)
        self.assertEqual(off_source.request(opener, "https://fixture.invalid/grpc.health.v1.Health/Check", off_source.Window(monotonic=time.monotonic_ns, boottime=time.monotonic_ns).limit(1), {}), {"status": "SERVING_STATUS_SERVING"})
        sent = opener.open.call_args.args[0]
        self.assertNotIn("Authorization", dict(sent.header_items()))

    def test_resume_over_budget_cannot_configure_or_start_wire_work(self):
        with tempfile.TemporaryDirectory() as temp:
            parent = Path(temp)
            root, prepared, output = parent/"root", parent/"prepared", parent/"evidence"
            root.mkdir(); prepared.mkdir()
            clock, children, commands = {"boot": 0}, [], []
            window = off_source.Window(lambda: 0, lambda: clock["boot"])

            class SyntheticChild:
                def __init__(self, binary, slot, nonce, directory, started):
                    self.slot, self.nonce = slot, nonce
                    self.process = Mock(pid=slot, returncode=0)
                    self.process.poll.return_value = 0
                    self.reader, self.stderr = Mock(), Mock()
                    self.records, self.stop_ns, self.launch_ns, self.killed = [], None, 0, False
                    children.append(self)

                def send(self, phase, deadline, **fields):
                    deadline.check()
                    commands.append(phase)

                def receive(self, event, deadline):
                    deadline.check()
                    return {"public": f"[::]:{1000+self.slot}", "peer": f"127.0.0.1:{2000+self.slot}"}

                def terminate(self, kill=False):
                    self.killed = True

            def provision(*args, **kwargs):
                kwargs["stdout"].write(json.dumps({"nodes": [{"environment": {}} for _ in range(3)]}).encode())
                clock["boot"] = 130_000_000_000

            manifest = {"source": {"head": "synthetic"}, "digest": "synthetic", "target": ["linux", "arm64"], "child_sha256": "synthetic"}
            with patch.object(off_source, "environment", return_value={"unit_fixture": True}), patch.object(off_source.platform, "machine", return_value="aarch64"), patch.object(off_source, "Window", return_value=window), patch.object(off_source, "Child", SyntheticChild), patch.object(off_source.subprocess, "run", side_effect=provision), patch.object(off_source, "wire_smoke") as wire:
                result = off_source.run(root, prepared, output, manifest)
            self.assertEqual(result["status"], "FAIL")
            self.assertEqual(result["final_elapsed_ns"], 130_000_000_000)
            self.assertEqual(commands, ["reserve"]*3)
            self.assertTrue(all(child.killed for child in children))
            wire.assert_not_called()
