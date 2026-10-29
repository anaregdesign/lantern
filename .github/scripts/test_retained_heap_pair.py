import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import retained_heap_pair as pair


def write(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, allow_nan=False))
    return {"path": str(path), "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}


def memory(cycle, forced, heap=1000):
    return {"heap_alloc": heap, "heap_objects": 50, "heap_inuse": heap + 100,
            "num_gc": cycle, "num_forced_gc": forced, "last_gc": cycle * 1000,
            "next_gc": heap * 2, "marked_live": heap, "metric_cycles": cycle,
            "metric_forced": forced, "cycle_after": cycle}


def native(count):
    idle = {"begun": count, "completed": count, "active": 0}
    return {"generation": 1, "faulted": False, "pending_count": 0, "pending_bytes": 0,
            "pending_local": False, "snapshot_active": False,
            "origins": [{"id": "origin-a", "sequence": count, "wall_ns": 100, "logical": 0}],
            "log_sequence": count + 22, "log_length": count + 22, "log_capacity": 10000,
            "log_evicted": 0, "receipt_entries": count, "receipt_bytes": count * 200,
            "receipt_high_water_ms": count, "receipt_oldest_deadline_ms": count + 10000,
            "vertices": count, "edges": 0,
            "public": idle, "peer_unary": copy.deepcopy(idle), "apply": copy.deepcopy(idle),
            "peer_subscribe": {"begun": 2, "completed": 0, "active": 2}}


class PairTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.normal = Path(self.temp.name).resolve() / "normal"
        self.tagged = Path(self.temp.name).resolve() / "tagged"
        self.expected = {"source_commit": "a" * 40, "source_tree": "b" * 40,
                         "normal_image_id": "sha256:" + "c" * 64,
                         "retained_image_id": "sha256:" + "d" * 64,
                         "runtime_user": "501:20", "cpuset": "0-3"}
        self.scenario = {"name": "receipt", "steady": {"duration": "45s"}}
        for root, image in ((self.normal, self.expected["normal_image_id"]),
                            (self.tagged, self.expected["retained_image_id"])):
            write(root / "scenario.resolved.json", self.scenario)
            for name in ("perf_gate", "workload_gate"):
                write(root / (name + ".json"), {"verdict": "pass"})
            provenance = {"source_commit": self.expected["source_commit"], "image_id": image,
                          "runtime_user": "501:20", "replicas": [
                              {"service": f"lantern-{i}", "container_id": str(i + 1) * 64,
                               "image_id": image, "runtime_user": "501:20", "cpuset": "0-3"}
                              for i in range(3)]}
            for phase in ("pre", "post"):
                write(root / f"image_provenance_{phase}.json", provenance)
        write(self.normal / "leak_gate.json", {"snapshot_protocol": pair.NORMAL_PROTOCOL,
              "verdict": "pass", "natural_checks_verdict": "pass",
              "natural_post_heap_observation_verdict": "fail", "retained_verdict": "requires_pair",
              "pair_required": True, "overall_memory_verdict": "requires_pair",
              "thresholds": {"goroutine_max_delta": 15, "heap_alloc_max_delta_mb": 32,
                             "steady_heap_alloc_max_delta_mb": 40}})
        snapshots = [{"endpoint": endpoint, "heap_alloc_bytes": 1000, "goroutines": 10}
                     for endpoint in pair.ENDPOINTS]
        write(self.normal / "runtime_pre.json", snapshots)
        post = copy.deepcopy(snapshots)
        post[0]["heap_alloc_bytes"] += 33 * pair.MIB
        write(self.normal / "runtime_post.json", post)
        write(self.normal / "runtime_steady.json", {"interval_ms": 5000, "duration_ms": 45000,
              "endpoints": list(pair.ENDPOINTS), "samples": [{"elapsed_ms": at, "replicas": snapshots}
                                                              for at in range(0, 45001, 5000)]})
        for phase, count in (("warmup", 10), ("steady", 20)):
            for i, name in enumerate(("receipt_admission", "receipt_lookup")):
                write(self.tagged / f"ghz_{phase}_{i}_{name}.json",
                      {"count": count, "statusCodeDistribution": {"OK": count}})
        self.cuts = {}
        for phase in ("baseline", "post"):
            count, forced, cycle = (10, 0, 5) if phase == "baseline" else (30, 2, 10)
            start = 1_000_000 if phase == "baseline" else 2_000_000_000
            cut = {"schema": 1, "protocol": pair.PROTOCOL, "phase": phase, "status": "complete",
                   "controller_monotonic_start_ns": start,
                   "controller_monotonic_end_ns": start + 400_000_000,
                   "context": {"run_id": "retained-test-run", "source_commit": "a" * 40,
                       "source_tree": "b" * 40, "image_id": self.expected["retained_image_id"],
                       "scenario_sha256": pair.canonical_sha(self.scenario), "runtime_user": "501:20",
                       "cpuset": "0-3", "workload": {"admission": count, "lookup": count}}, "replicas": []}
            for index, endpoint in enumerate(pair.ENDPOINTS):
                session = {"schema": 1, "protocol": pair.PROTOCOL, "run_nonce": "e" * 64,
                           "source_commit": "a" * 40, "source_tree": "b" * 40,
                           "image_id": self.expected["retained_image_id"], "container_id": str(index + 1) * 64,
                           "binary_sha256": "f" * 64, "uid": 501}
                identity = {"session": session, "pid": 1, "start_nonce": str(index + 4) * 64}
                request = {"schema": 1, "protocol": pair.PROTOCOL, "identity": identity,
                           "phase": phase, "ordinal": 1 if phase == "baseline" else 2,
                           "expected_receipts": count,
                           "cut_nonce": f"{index + (1 if phase == 'baseline' else 4):064x}"}
                heap = 1000 + (32 * pair.MIB if phase == "post" else 0)
                collections = [{"started_ns": 250_000_000 + i * 2_000_000,
                    "returned_ns": 251_000_000 + i * 2_000_000,
                    "before": memory(cycle + i, forced + i, heap),
                    "after": memory(cycle + i + 1, forced + i + 1, heap)} for i in range(2)]
                completion = {"schema": 1, "protocol": pair.PROTOCOL, "request": request,
                              "status": "complete", "reason": "", "elapsed_ns": 300_000_000,
                              "before": native(count), "after": native(count),
                              "collections": collections, "memory": collections[1]["after"]}
                cut["replicas"].append({"endpoint": endpoint, "completion": completion})
            self.cuts[phase] = cut
        self.save()

    def save(self):
        refs = {}
        for phase, suffix in (("baseline", "pre"), ("post", "post")):
            cut = self.cuts[phase]
            for index, row in enumerate(cut["replicas"]):
                completion = row["completion"]
                request = completion["request"]
                directory = self.tagged / "heap-control-evidence" / f"lantern-{index}"
                row["session_file"] = write(directory / "session.json", request["identity"]["session"])
                row["ready_file"] = write(directory / "ready.json",
                    {"schema": 1, "protocol": pair.PROTOCOL, "identity": request["identity"]})
                row["request_file"] = write(directory / f"{phase}.request.json", request)
                row["started_file"] = write(directory / f"{phase}.started.json", {"request": request})
                row["completion_file"] = write(directory / f"{phase}.complete.json", completion)
            refs[suffix] = write(self.tagged / f"retained_heap_{suffix}.json", cut)
        write(self.tagged / "leak_gate.json", {"snapshot_protocol": pair.PROTOCOL, "verdict": "pass",
              "retained_verdict": "pass", "pair_required": True,
              "overall_memory_verdict": "requires_pair", "retained_inputs": refs})

    def validate(self):
        return pair.validate_pair(self.normal, self.tagged, self.scenario, self.expected)

    def test_same_source_pair_accepts_boundary_and_keeps_natural_failure_observation(self):
        result = self.validate()
        self.assertEqual(result["verdict"], "pass")
        self.assertEqual([r["heap_alloc_delta_bytes"] for r in result["replicas"]], [32 * pair.MIB] * 3)
        self.assertNotEqual(result["normal_image_id"], result["retained_image_id"])

    def test_forged_pass_cannot_hide_retained_excess(self):
        row = self.cuts["post"]["replicas"][0]["completion"]
        row["memory"]["heap_alloc"] += 1
        self.save()
        with self.assertRaisesRegex(pair.Incomplete, "32 MiB"):
            self.validate()

    def test_incomplete_asymmetric_active_and_cycle_drift_refuse(self):
        cases = (
            lambda row: row.update(status="incomplete"),
            lambda row: row.update(collections=row["collections"][:1]),
            lambda row: row["before"]["public"].update(begun=31, active=1),
            lambda row: row["memory"].update(cycle_after=999),
            lambda row: row.update(elapsed_ns=25_000_000_001),
            lambda row: row["collections"][0]["after"].update(num_forced_gc=100),
        )
        original = copy.deepcopy(self.cuts)
        for change in cases:
            with self.subTest(change=change):
                self.cuts = copy.deepcopy(original)
                change(self.cuts["post"]["replicas"][0]["completion"])
                self.save()
                with self.assertRaises(pair.Incomplete):
                    self.validate()

    def test_frontier_mismatch_and_submitted_work_gap_refuse(self):
        row = self.cuts["post"]["replicas"][0]["completion"]
        for side in ("before", "after"):
            row[side]["origins"][0]["sequence"] -= 1
        self.save()
        with self.assertRaisesRegex(pair.Incomplete, "frontiers"):
            self.validate()
        self.cuts["post"]["replicas"][1]["completion"] = copy.deepcopy(row)
        self.save()
        with self.assertRaises(pair.Incomplete):
            self.validate()

    def test_replay_restart_and_foreign_source_refuse(self):
        original = copy.deepcopy(self.cuts)
        for field, value in (("start_nonce", "9" * 64), ("pid", 2)):
            with self.subTest(field=field):
                self.cuts = copy.deepcopy(original)
                self.cuts["post"]["replicas"][0]["completion"]["request"]["identity"][field] = value
                self.save()
                with self.assertRaises(pair.Incomplete):
                    self.validate()
        self.cuts = copy.deepcopy(original)
        row = self.cuts["post"]["replicas"][0]["completion"]["request"]
        row["cut_nonce"] = self.cuts["baseline"]["replicas"][0]["completion"]["request"]["cut_nonce"]
        self.save()
        with self.assertRaisesRegex(pair.Incomplete, "replayed"):
            self.validate()

    def test_missing_pair_hash_mismatch_and_legacy_protocol_refuse(self):
        self.expected["retained_image_id"] = self.expected["normal_image_id"]
        with self.assertRaises(pair.Incomplete):
            self.validate()
        self.expected["retained_image_id"] = "sha256:" + "d" * 64
        path = self.tagged / "heap-control-evidence/lantern-0/post.complete.json"
        path.write_text(path.read_text() + " ")
        with self.assertRaisesRegex(pair.Incomplete, "hash"):
            self.validate()
        self.save()
        value = json.loads((self.normal / "leak_gate.json").read_text())
        value["snapshot_protocol"] = "natural_gc_single"
        write(self.normal / "leak_gate.json", value)
        with self.assertRaises(pair.Incomplete):
            self.validate()

    def test_natural_peak_and_goroutine_cannot_hide_behind_pair_pass(self):
        path = self.normal / "runtime_steady.json"
        original = json.loads(path.read_text())
        for key, value in (("heap_alloc_bytes", 1001 + 40 * pair.MIB), ("goroutines", 26)):
            with self.subTest(key=key):
                bad = copy.deepcopy(original)
                bad["samples"][2]["replicas"][1][key] = value
                write(path, bad)
                with self.assertRaises(pair.Incomplete):
                    self.validate()

    def test_symlink_escape_duplicate_keys_and_nonfinite_json_refuse(self):
        root = self.tagged / "unsafe"
        root.symlink_to(self.normal, target_is_directory=True)
        with self.assertRaises(pair.Incomplete):
            pair.read(self.tagged, root / "perf_gate.json")
        for content in ('{"x":1,"x":2}', '{"x":NaN}'):
            path = self.tagged / "bad.json"
            path.write_text(content)
            with self.assertRaises(pair.Incomplete):
                pair.read(self.tagged, path)

    def test_refusal_regular_or_broken_symlink_overrides_completion(self):
        path = self.tagged / "heap-control-evidence/lantern-0/refusal.json"
        for linked in (False, True):
            with self.subTest(linked=linked):
                if linked:
                    path.symlink_to("missing-refusal-target")
                else:
                    path.write_text("{}")
                with self.assertRaisesRegex(pair.Incomplete, "refusal"):
                    self.validate()
                path.unlink()

    def test_missing_native_fields_or_excessive_expected_receipts_refuse(self):
        original = copy.deepcopy(self.cuts)
        for field in ("vertices", "edges", "log_sequence", "receipt_bytes"):
            with self.subTest(field=field):
                self.cuts = copy.deepcopy(original)
                row = self.cuts["post"]["replicas"][0]["completion"]
                row["before"].pop(field)
                row["after"].pop(field)
                self.save()
                with self.assertRaises(pair.Incomplete):
                    self.validate()
        self.cuts = copy.deepcopy(original)
        row = self.cuts["post"]["replicas"][0]["completion"]
        row["request"]["expected_receipts"] = 8001
        self.cuts["post"]["context"]["workload"] = {"admission": 8001, "lookup": 8001}
        self.save()
        with self.assertRaises(pair.Incomplete):
            self.validate()

    def test_gc_counters_cannot_roll_back_with_individually_coherent_samples(self):
        row = self.cuts["post"]["replicas"][0]["completion"]
        row["collections"][1]["before"] = memory(1, 3)
        row["collections"][1]["after"] = memory(2, 4)
        row["memory"] = row["collections"][1]["after"]
        self.save()
        with self.assertRaisesRegex(pair.Incomplete, "rolled back"):
            self.validate()

    def test_case_root_ancestor_symlink_refuses_even_matching_bytes(self):
        alias = self.normal.parent / "ancestor-alias"
        alias.symlink_to(self.normal.parent, target_is_directory=True)
        with self.assertRaises(pair.Incomplete):
            pair.validate_pair(alias / "normal", self.tagged, self.scenario, self.expected)
