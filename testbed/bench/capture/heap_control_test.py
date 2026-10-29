"""Deterministic file/daemon-control tests; never start Docker or collect GC."""
import argparse
import copy
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import heap_control as control


def completion(request, heap=100 << 20):
    count = request["expected_receipts"]
    counter = {"begun": count, "completed": count, "active": 0}
    native = {"generation": count, "faulted": False, "pending_count": 0, "pending_bytes": 0, "pending_local": False, "snapshot_active": False,
              "origins": [{"id": f"{i:032x}", "sequence": count, "wall_ns": 1_793_000_000_000_000_000, "logical": 0} for i in range(1, 4)],
              "log_sequence": count, "log_length": count, "log_capacity": 10000, "log_evicted": 0, "receipt_entries": count, "receipt_bytes": count * 256,
              "receipt_high_water_ms": 1000, "receipt_oldest_deadline_ms": 3_601_000, "vertices": 4, "edges": 0,
              "public": counter, "peer_unary": counter, "apply": counter, "peer_subscribe": {"begun": 2, "completed": 0, "active": 2}}
    def memory(gc, forced):
        return {"heap_alloc": heap, "heap_objects": 1000, "heap_inuse": heap + 1024, "num_gc": gc, "num_forced_gc": forced, "last_gc": gc * 1_000_000,
                "next_gc": heap * 2, "marked_live": heap - 1024, "metric_cycles": gc, "metric_forced": forced, "cycle_after": gc}
    forced = 0 if request["phase"] == "baseline" else 2
    gc = 10 if request["phase"] == "baseline" else 20
    collections = [{"started_ns": 300_000_000, "returned_ns": 400_000_000, "before": memory(gc, forced), "after": memory(gc+1, forced + 1)},
                   {"started_ns": 500_000_000, "returned_ns": 600_000_000, "before": memory(gc+1, forced + 1), "after": memory(gc+2, forced + 2)}]
    return {"schema": 1, "protocol": control.PROTOCOL, "request": request, "status": "complete", "reason": "", "elapsed_ns": 900_000_000,
            "before": native, "after": copy.deepcopy(native), "collections": collections, "memory": memory(gc+2, forced + 2)}


class FakeDocker:
    def __init__(self, root):
        self.root = root
        self.calls = []
        self.state = "created"
        self.config = None
        self.damage = None
        self.signal_error = False
        self.all_requests_committed = True

    def __call__(self, argv, deadline):
        self.calls.append(list(argv))
        if argv[0] == "inspect":
            rows = []
            for index, cid in enumerate(argv[3:]):
                rows.append([cid, "sha256:" + "c" * 64, self.state, self.state == "running", 0, f"{os.getuid()}:{os.getgid()}", "" if self.state == "created" else "2026-10-29T00:00:00Z",
                             [{"Destination": control.TARGET_DIRECTORY, "Type": "bind", "Source": str(self.root / f"lantern-{index}"), "RW": True}]])
            return b"\n".join(control.json_bytes(row).strip() for row in rows)
        if argv[0] == "cp":
            Path(argv[2]).write_bytes(b"owned immutable fake executable")
            return b""
        if argv[:2] == ["kill", "--signal=USR1"]:
            for directory in self.root.iterdir():
                self.all_requests_committed &= (directory / "baseline.request.json").exists() or (directory / "post.request.json").exists()
            config = control.decode(control.read_private(self.root.parent / control.CONFIG_FILE, control.OUTPUT_LIMIT))
            node = next(n for n in config["nodes"] if n["session"]["container_id"] == argv[2])
            directory = Path(node["directory"])
            phase = "post" if (directory / "post.request.json").exists() else "baseline"
            request = control.decode(control.read_private(directory / f"{phase}.request.json", control.INPUT_LIMIT))
            value = completion(request)
            if self.damage:
                self.damage(value)
            control.write_private(directory / f"{phase}.started.json", {"request": request})
            control.write_private(directory / f"{phase}.complete.json", value)
            if self.signal_error:
                raise TimeoutError("uncertain signal")
            return (argv[2] + "\n").encode()
        raise AssertionError(argv)


class HeapControlTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.case = Path(self.temp.name).resolve()
        self.root = self.case / "heap-control-evidence"
        control.initialize(self.root)
        self.scenario = self.case / "scenario.resolved.json"
        self.scenario.write_text('{"name":"receipt","phases":{"steady":{"duration":"45s"}}}')
        for phase, count in (("warmup", 1000), ("steady", 4500)):
            for index, name in enumerate(("receipt_admission", "receipt_lookup")):
                (self.case / f"ghz_{phase}_{index}_{name}.json").write_text(json.dumps({"count": count, "statusCodeDistribution": {"OK": count}}))
        self.runner = FakeDocker(self.root)
        self.args = argparse.Namespace(directory=str(self.root), containers=",".join(f"{i:064x}" for i in range(1, 4)), endpoints=",".join(f"localhost:{9390+i}" for i in range(3)),
                                       source_commit="a" * 40, source_tree="b" * 40, image_id="sha256:" + "c" * 64, runtime_user=f"{os.getuid()}:{os.getgid()}",
                                       scenario_json=str(self.scenario), run_id="owned-test", cpuset="0-3")

    def tearDown(self):
        self.temp.cleanup()

    def prepare(self):
        control.prepare(self.args, self.runner)
        self.runner.state = "running"
        _, config = control.load_config(self.root)
        for index, node in enumerate(config["nodes"]):
            control.write_private(Path(node["directory"]) / "ready.json", {"schema": 1, "protocol": control.PROTOCOL,
                                  "identity": {"session": node["session"], "pid": 1, "start_nonce": f"{index+100:064x}"}})
        return config

    def cut_args(self, phase="baseline"):
        return argparse.Namespace(directory=str(self.root), phase=phase, admission=1000 if phase == "baseline" else 5500, lookup=1000 if phase == "baseline" else 5500)

    def test_initialize_normalizes_only_new_private_control_directories(self):
        root = self.case / "new-control"
        original_chown = os.chown
        with mock.patch.object(control.os, "chown", wraps=original_chown) as ownership:
            control.initialize(root)
            expected = [root, *(root / f"lantern-{index}" for index in range(3))]
            self.assertEqual(ownership.call_args_list, [mock.call(path, os.getuid(), os.getgid(), follow_symlinks=False) for path in expected])
            for directory in expected:
                info = directory.lstat()
                self.assertEqual((info.st_uid, info.st_gid, info.st_mode & 0o7777), (os.getuid(), os.getgid(), 0o700))
            with self.assertRaises(FileExistsError):
                control.initialize(root)
            self.assertEqual(ownership.call_count, 4)

    def test_prepare_reads_three_never_started_bindings_and_copies_one_binary(self):
        self.prepare()
        self.assertEqual([call[0] for call in self.runner.calls], ["inspect", "cp"])
        self.assertTrue((self.case / "heap-observer-binary").is_file())
        with self.assertRaises(ValueError):
            control.prepare(self.args, self.runner)
        self.assertEqual([call[0] for call in self.runner.calls], ["inspect", "cp"])

    def test_full_phase_captures_exact_raw_references_without_replacing_files(self):
        self.prepare()
        self.assertEqual(control.capture(self.cut_args(), self.runner), 0)
        self.assertTrue(self.runner.all_requests_committed)
        result = control.decode(control.read_private(self.case / "retained_heap_pre.json", 256 << 10))
        self.assertEqual(result["status"], "complete")
        self.assertEqual(len(result["replicas"]), 3)
        for replica in result["replicas"]:
            for key in ("session_file", "ready_file", "request_file", "started_file", "completion_file"):
                ref = replica[key]
                self.assertEqual(control.digest(control.read_private(ref["path"], control.OUTPUT_LIMIT)), ref["sha256"])
        before = len([c for c in self.runner.calls if c[0] == "kill"])
        with self.assertRaises(FileExistsError):
            control.capture(self.cut_args(), self.runner)
        self.assertEqual(len([c for c in self.runner.calls if c[0] == "kill"]), before)
        self.assertEqual(control.capture(self.cut_args("post"), self.runner), 0)

    def test_failure_before_persisted_requests_sends_no_signal(self):
        self.prepare()
        (self.root / "lantern-1" / "baseline.request.json").write_text("occupied")
        self.assertEqual(control.capture(self.cut_args(), self.runner), 1)
        self.assertFalse(any(c[0] == "kill" for c in self.runner.calls))
        self.assertEqual(control.decode(control.read_private(self.case / "retained_heap_pre.json", 256 << 10))["status"], "incomplete")

    def test_uncertain_signal_does_not_become_pass_from_later_completion(self):
        self.prepare()
        self.runner.signal_error = True
        self.assertEqual(control.capture(self.cut_args(), self.runner), 1)
        self.assertEqual(len([c for c in self.runner.calls if c[0] == "kill"]), 3)

    def test_incomplete_gc_and_controller_deadline_never_retry_or_pass(self):
        self.prepare()
        self.runner.damage = lambda c: c["collections"].pop()
        self.assertEqual(control.capture(self.cut_args(), self.runner), 1)
        self.assertEqual(len([c for c in self.runner.calls if c[0] == "kill"]), 3)
        result = control.decode(control.read_private(self.case / "retained_heap_pre.json", 256 << 10))
        self.assertEqual(result["status"], "incomplete")

    def test_elapsed_controller_cut_is_incomplete_even_with_target_receipts(self):
        self.prepare()
        times = iter((100_000_000_000, 130_000_000_001, 130_000_000_002))
        self.assertEqual(control.capture(self.cut_args(), self.runner, clock=lambda: next(times)), 1)
        self.assertEqual(len([c for c in self.runner.calls if c[0] == "kill"]), 3)

    def test_submitted_work_mismatch_refuses_before_gc(self):
        self.prepare()
        args = self.cut_args()
        args.admission = args.lookup = 999
        self.assertEqual(control.capture(args, self.runner), 1)
        self.assertFalse(any(c[0] == "kill" for c in self.runner.calls))

    def test_native_and_gc_incompleteness_refuse_without_another_signal(self):
        for damage in (lambda c: c["collections"].pop(), lambda c: c["memory"].update(cycle_after=99), lambda c: c["before"]["public"].update(active=1), lambda c: c["before"].update(receipt_entries=1), lambda c: c.update(elapsed_ns=26_000_000_000)):
            with self.subTest(damage=damage):
                request = {"schema": 1, "protocol": control.PROTOCOL, "phase": "baseline", "expected_receipts": 1000}
                value = completion(request)
                damage(value)
                with self.assertRaises(ValueError):
                    control.validate_completion(value, request, 1000)

    def test_schema_missing_duplicate_null_and_link_are_not_evidence(self):
        with self.assertRaises(ValueError):
            control.decode(b'{"schema":1,"schema":1}')
        path = self.case / "linked.json"
        target = self.case / "target.json"
        control.write_private(target, {"owned": True})
        path.symlink_to(target)
        with self.assertRaises(ValueError):
            control.read_private(path, 4096)
        request = {"phase": "baseline", "expected_receipts": 1000}
        value = completion(request)
        del value["before"]["pending_count"]
        with self.assertRaises(ValueError):
            control.validate_completion(value, request, 1000)

    def test_replacement_fifo_is_nonblocking_and_rejected_before_read(self):
        path = self.case / "replaced.json"
        control.write_private(path, {"owned": True})
        original_open = os.open
        observed = []
        def replace_before_open(candidate, flags, *args, **kwargs):
            if Path(candidate) == path:
                # Fail before the real open when NONBLOCK is missing: this
                # regression must never hang waiting for a FIFO writer.
                self.assertTrue(flags & os.O_NONBLOCK)
                observed.append(True)
                path.unlink()
                os.mkfifo(path, 0o600)
            return original_open(candidate, flags, *args, **kwargs)
        with mock.patch.object(control.os, "open", side_effect=replace_before_open):
            with self.assertRaises(ValueError):
                control.read_private(path, 4096)
        self.assertEqual(observed, [True])

    def test_opened_metadata_replacement_is_not_the_validated_regular_file(self):
        path = self.case / "replaced-mode.json"
        control.write_private(path, {"owned": True})
        original_open = os.open
        def replace_mode(candidate, flags, *args, **kwargs):
            if Path(candidate) == path:
                self.assertTrue(flags & os.O_NONBLOCK)
                path.chmod(0o644)
            return original_open(candidate, flags, *args, **kwargs)
        with mock.patch.object(control.os, "open", side_effect=replace_mode):
            with self.assertRaises(ValueError):
                control.read_private(path, 4096)

    def test_tagged_recipe_has_no_new_production_surface(self):
        bench = Path(__file__).resolve().parent.parent
        recipe = (bench / "Dockerfile.retained").read_text()
        self.assertIn("FROM ${NORMAL_BUILDER_IMAGE}", recipe)
        self.assertIn("FROM ${NORMAL_RUNTIME_IMAGE}", recipe)
        self.assertIn("-tags=lantern_bench_heap", recipe)
        self.assertIn("--network=none", recipe)
        self.assertEqual(recipe.count("COPY "), 1)
        overlay = (bench / "compose.retained-heap.yml").read_text()
        self.assertEqual(overlay.count("target: /run/lantern-bench-heap"), 3)
        self.assertEqual(overlay.count("create_host_path: false"), 3)
        for surface in ("ports:", "environment:", "privileged:", "cap_add:", "network_mode:"):
            self.assertNotIn(surface, overlay)


if __name__ == "__main__":
    unittest.main()
