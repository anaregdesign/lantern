import argparse
import errno
import json
import os
from pathlib import Path
import signal
import tempfile
import unittest
from unittest.mock import patch

import local_release_qualification as qualification


SHA, TREE, IMAGE = "a" * 40, "b" * 40, "sha256:" + "c" * 64
REFERENCE, OWNER = "lantern:local-qualification-manual-test", "501:20"


def put(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value))


def runtime():
    return [{"endpoint": f"localhost:{port}", "goroutines": 10, "heap_alloc_bytes": 1000,
             "heap_inuse_bytes": 2000, "heap_objects": 30} for port in (9390, 9391, 9392)]


def provenance(receipt=False):
    replicas = []
    for i, service in enumerate(qualification.SERVICES):
        row = {"service": service, "container_id": str(i + 1) * 64,
               "image_id": IMAGE, "image_ref": REFERENCE, "runtime_user": OWNER, "cpuset": "0-3"}
        if not receipt:
            row["graph_snapshot_spool"] = {"volume": f"manual-test-spool-{i}",
                "target": "/var/lib/lantern-snapshot-spool", "runtime_user": OWNER,
                "mode": "0700", "read_write": True, "configured_nocopy": True}
        replicas.append(row)
    return {"source_commit": SHA, "image_id": IMAGE, "image_ref": REFERENCE,
            "runtime_user": OWNER, "replicas": replicas}


def fixture(directory, receipt=False):
    document = {"target": {"calls": [{"name": "receipt_admission"}, {"name": "receipt_lookup"}]}}
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "report.md").write_text("# Test report\n")
    for phase in ("pre", "post"):
        put(directory / f"runtime_{phase}.json", runtime())
        put(directory / f"image_provenance_{phase}.json", provenance(receipt))
    put(directory / "leak_gate.json", {"verdict": "pass", "snapshot_protocol": "natural_reference_v2",
        "natural_steady_memory_verdict": "pass", "goroutine_checks_verdict": "pass", "retained_verdict": "requires_pair", "pair_required": True,
        "overall_memory_verdict": "requires_pair", "steady_sample_interval": "5s", "steady_sample_count": 9})
    policy_input = directory / "goroutine_policy_input.json"
    put(policy_input, {"fixture": "raw input reference; independent consumer tested separately"})
    os.chmod(policy_input, 0o600)
    leak = json.loads((directory / "leak_gate.json").read_text())
    leak["goroutine_policy_input"] = {"path": str(policy_input), "sha256": qualification.digest(policy_input)}
    put(directory / "leak_gate.json", leak)
    put(directory / "workload_gate.json", {"verdict": "pass", "producer_failed": 0,
        "producer_diagnostics_failed": 0, "failed_consumers": 0, "chaos_failed": 0})
    put(directory / "perf_gate.json", {"verdict": "pass", "producer_results": [
        {"name": name, "verdict": "pass", "observed": {"count": 100}}
        for name in ("receipt_admission", "receipt_lookup")]})
    for phase in ("warmup", "steady"):
        for i, name in enumerate(("receipt_admission", "receipt_lookup")):
            put(directory / f"ghz_{phase}_{i}_{name}.json", {"count": 100, "statusCodeDistribution": {"OK": 100}})
    if receipt:
        tls = {"ca_sha256": "d" * 64, "peers": [{"service": s, "certificate_sha256": "e" * 64}
                for s in qualification.SERVICES]}
        for phase in ("pre", "post"):
            put(directory / f"peer_tls_{phase}.json", tls)
        put(directory / "membership_renewal.json", {"renewals": 1, "last_at": "2026-10-26T00:00:00Z"})
        put(directory / "runtime_steady.json", steady())
    return document


def steady():
    return {"interval_ms": 5000, "duration_ms": 45000,
            "endpoints": [row["endpoint"] for row in runtime()],
            "samples": [{"elapsed_ms": at, "replicas": runtime()} for at in range(5000, 45001, 5000)]}


def identity(driver, running="true", service="lantern-0"):
    return ["1" * 64, IMAGE, driver.owner, running, driver.args.campaign, service, "0-3",
            REFERENCE, f"/{driver.args.campaign}-{service}-1", "<no value>", "False", "<no value>",
            '["/app/lantern"]', 'null']


def bind_probe_identity(driver, pairs=1, service="lantern-0"):
    row = identity(driver, service=service + "-bind-proof")
    targets = [f"/proof/{side}-{slot}" for slot in range(pairs) for side in ("original", "clone")]
    profile = {"Cmd": ["stat", "-c", "%d:%i:%F", *targets], "NetworkMode": "none",
        "ReadonlyRootfs": True, "Privileged": False, "AutoRemove": False,
        "CapDrop": ["ALL"], "CapAdd": None, "SecurityOpt": ["no-new-privileges:true"],
        "Memory": 33554432, "NanoCpus": 250000000, "PidsLimit": 16,
        "VolumesFromCount": 0, "TmpfsCount": 0, "ConfigVolumeCount": 0,
        "DeviceCount": 0, "DeviceRequestCount": 0, "DeviceCgroupRuleCount": 0,
        "PidMode": "", "UTSMode": "", "UsernsMode": "", "PublishAllPorts": False,
        "PortBindingCount": 0, "RestartPolicy": {"Name": "no", "MaximumRetryCount": 0},
        "BindCount": 0, "Mounts": [{"Type": "bind", "Destination": target,
            "RW": False, "Mode": "", "Propagation": "rprivate"} for target in targets],
        "HostMounts": [{"Type": "bind", "Target": target, "ReadOnly": True,
            "BindOptions": {"Propagation": "rprivate"}, "UnsupportedOptions": False} for target in targets]}
    row[7:14] = [IMAGE, f"/{driver.args.campaign}-{row[5]}", "compose-bind-identity-probe",
                 "True", "lantern-owned-init.ABC123", '["/bin/busybox"]', json.dumps(profile)]
    return row


class ArtifactTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.directory = Path(self.temporary.name) / "run"
        self.addCleanup(self.temporary.cleanup)

    def validate(self, document, receipt=False):
        return qualification.validate_run(self.directory, document, SHA, REFERENCE, IMAGE, OWNER, receipt)

    def test_off_and_receipt_evidence_pass_without_measurements(self):
        for receipt in (False, True):
            with self.subTest(receipt=receipt):
                document = fixture(self.directory, receipt)
                self.assertEqual(self.validate(document, receipt)["workload_gate"], "pass")

    def test_receipt_lane_does_not_accept_legacy_or_unpaired_memory_claim(self):
        document = fixture(self.directory, True)
        path = self.directory / "leak_gate.json"
        original = json.loads(path.read_text())
        for field, value in (("snapshot_protocol", "natural_gc_single"),
                             ("overall_memory_verdict", "pass"), ("pair_required", False),
                             ("retained_verdict", "pass"), ("natural_steady_memory_verdict", "fail"), ("goroutine_checks_verdict", "incomplete"), ("snapshot_protocol", "natural_reference_v1")):
            with self.subTest(field=field):
                bad = dict(original)
                bad[field] = value
                put(path, bad)
                with self.assertRaises(qualification.Refused):
                    self.validate(document, True)

    def test_missing_retained_pair_cannot_qualify(self):
        source = Path(qualification.__file__).resolve().parents[2]
        consumer = source / ".github/scripts/retained_heap_pair.py"
        inventory = {".github/scripts/retained_heap_pair.py": {
            "sha256": qualification.digest(consumer), "mode": consumer.stat().st_mode & 0o777}}
        with self.assertRaisesRegex(qualification.Refused, "retained pair incomplete"):
            qualification.validate_retained_pair(self.directory, self.directory, {}, {}, source, inventory)

    def test_pair_consumer_comes_from_frozen_inventory_not_launcher(self):
        source = self.directory / "frozen"
        consumer = source / ".github/scripts/retained_heap_pair.py"
        consumer.parent.mkdir(parents=True)
        consumer.write_text("def validate_pair(*args): return {'selected': 'frozen'}\n")
        inventory = {".github/scripts/retained_heap_pair.py": {
            "sha256": qualification.digest(consumer), "mode": consumer.stat().st_mode & 0o777}}
        launcher = self.directory / "launcher/local_release_qualification.py"
        launcher.parent.mkdir(parents=True)
        launcher.with_name("retained_heap_pair.py").write_text("raise AssertionError('launcher consumer executed')\n")
        with patch.object(qualification, "__file__", str(launcher)):
            result = qualification.validate_retained_pair(self.directory, self.directory, {}, {}, source, inventory)
        self.assertEqual(result, {"selected": "frozen"})
        consumer.write_text("raise AssertionError('changed consumer executed')\n")
        with self.assertRaisesRegex(qualification.Refused, "frozen pair consumer differs"):
            qualification.validate_retained_pair(self.directory, self.directory, {}, {}, source, inventory)

    def test_normal_policy_consumer_is_inventory_bound(self):
        document = fixture(self.directory, True)
        source = self.directory / "frozen"
        consumer = source / ".github/scripts/retained_heap_pair.py"
        consumer.parent.mkdir(parents=True)
        consumer.write_text("class Incomplete(ValueError): pass\ndef validate_normal_policy(root, scenario, expected, report):\n    if report['goroutine_checks_verdict'] != 'pass': raise Incomplete('raw component failed')\n    return {'selected':'frozen-normal','protocol':report['snapshot_protocol']}\n")
        inventory = {".github/scripts/retained_heap_pair.py": {"sha256": qualification.digest(consumer), "mode": consumer.stat().st_mode & 0o777}}
        result = qualification.validate_normal_policy(self.directory, document, {}, source, inventory)
        self.assertEqual(result, {"selected":"frozen-normal", "protocol":"natural_reference_v2"})
        leak = qualification.read_json(self.directory / "leak_gate.json")
        put(self.directory / "leak_gate.json", leak | {"goroutine_checks_verdict":"fail"})
        with self.assertRaisesRegex(qualification.Refused, "normal goroutine component incomplete"):
            qualification.validate_normal_policy(self.directory, document, {}, source, inventory)

    def test_composed_pass_requires_actual_policy_input_reference(self):
        document = fixture(self.directory, True)
        (self.directory / "goroutine_policy_input.json").unlink()
        with self.assertRaisesRegex(qualification.Refused, "regular|evidence"):
            self.validate(document, True)

    def test_frozen_consumer_fifo_replacement_cannot_block_open(self):
        source = self.directory / "frozen"
        consumer = source / ".github/scripts/retained_heap_pair.py"
        consumer.parent.mkdir(parents=True)
        consumer.write_text("def validate_pair(*args): return {}\n")
        inventory = {".github/scripts/retained_heap_pair.py": {
            "sha256": qualification.digest(consumer), "mode": consumer.stat().st_mode & 0o777}}
        real_open = os.open
        def replace(path, flags, *args, **kwargs):
            if path == consumer:
                self.assertTrue(flags & os.O_NONBLOCK, "source replacement must not block the owner")
                consumer.unlink()
                os.mkfifo(consumer, 0o600)
            return real_open(path, flags, *args, **kwargs)
        with patch.object(qualification.os, "open", side_effect=replace):
            with self.assertRaisesRegex(qualification.Refused, "frozen pair consumer changed"):
                qualification.validate_retained_pair(self.directory, self.directory, {}, {}, source, inventory)

    def test_zero_missing_duplicate_and_nonfinite_runtime_refuse(self):
        cases = []
        for value in (0, None, -1, 0.5, float("nan"), True, 2 ** 53):
            bad = runtime()
            bad[0]["heap_alloc_bytes"] = value
            cases.append(bad)
        cases += [runtime()[:2], [runtime()[0]] * 3]
        for bad in cases:
            with self.subTest(value=bad), self.assertRaises(qualification.Refused):
                qualification.validate_runtime(bad)

    def test_missing_or_failed_workload_metric_and_semantic_never_pass(self):
        document = fixture(self.directory)
        document["metric_gate"] = {"metrics": {"index": {"min_post": 1}}}
        with self.assertRaises(FileNotFoundError):
            self.validate(document)
        put(self.directory / "metric_gate.json", {"verdict": "pass"})
        document["semantic_gate"] = {"kind": "search"}
        put(self.directory / "semantic_pre.json", {"phase": "pre", "verdict": "pass", "replicas": []})
        with self.assertRaises(qualification.Refused):
            self.validate(document)
        for phase in ("pre", "post"):
            put(self.directory / f"semantic_{phase}.json", {"phase": phase, "verdict": "pass", "replicas": [
                {"endpoint": f"http://localhost:{port}", "checks": 10, "verdict": "pass"}
                for port in (6380, 6381, 6382)]})
        self.assertEqual(self.validate(document)["semantic_gate"], "pass")
        workload = qualification.read_json(self.directory / "workload_gate.json")
        workload["failed_consumers"] = 1
        put(self.directory / "workload_gate.json", workload)
        with self.assertRaises(qualification.Refused):
            self.validate(document)

    def test_source_image_container_and_spool_drift_refuse(self):
        for field in ("source_commit", "image_id", "runtime_user"):
            value = provenance()
            value[field] = "wrong"
            with self.subTest(field=field), self.assertRaises(qualification.Refused):
                qualification.validate_provenance(value, SHA, REFERENCE, IMAGE, OWNER, False)
        value = provenance()
        value["replicas"][1]["graph_snapshot_spool"]["volume"] = value["replicas"][0]["graph_snapshot_spool"]["volume"]
        with self.assertRaises(qualification.Refused):
            qualification.validate_provenance(value, SHA, REFERENCE, IMAGE, OWNER, False)
        document = fixture(self.directory)
        value = provenance()
        value["replicas"][0]["container_id"] = "f" * 64
        put(self.directory / "image_provenance_post.json", value)
        with self.assertRaises(qualification.Refused):
            self.validate(document)

    def test_actual_pre_post_user_and_cpuset_are_required_without_observer(self):
        for field in ("runtime_user", "cpuset"):
            for mutation in ("missing", "drift"):
                proof = provenance()
                if mutation == "missing":
                    del proof["replicas"][0][field]
                else:
                    proof["replicas"][0][field] = "foreign"
                with self.subTest(field=field, mutation=mutation), self.assertRaises(qualification.Refused):
                    qualification.validate_provenance(proof, SHA, REFERENCE, IMAGE, OWNER, False)

    def test_unqualified_cleanup_annotation_refuses_green_gates(self):
        document = fixture(self.directory)
        with (self.directory / "report.md").open("a") as stream:
            stream.write("\n**Bench run:** unqualified (named Compose teardown failed).\n")
        with self.assertRaises(qualification.Refused):
            self.validate(document)

    def test_receipt_gc_sampling_and_pair_coverage_refuse(self):
        document = fixture(self.directory, True)
        leak = qualification.read_json(self.directory / "leak_gate.json")
        for change in ({"snapshot_protocol": "forced_gc_minimum"}, {"steady_sample_count": 8}):
            put(self.directory / "leak_gate.json", leak | change)
            with self.assertRaises(qualification.Refused):
                self.validate(document, True)
        put(self.directory / "leak_gate.json", leak)
        put(self.directory / "ghz_steady_1_receipt_lookup.json", {"count": 99, "statusCodeDistribution": {"OK": 99}})
        with self.assertRaises(qualification.Refused):
            self.validate(document, True)

    def test_receipt_steady_gap_replica_loss_and_missing_tail_refuse(self):
        for change in ("gap", "missing", "tail", "duplicate"):
            value = steady()
            if change == "gap":
                value["samples"][1]["elapsed_ms"] = 14000
            elif change == "missing":
                value["samples"][0]["replicas"].pop()
            elif change == "tail":
                value["duration_ms"] = 55000
            else:
                value["samples"][1]["elapsed_ms"] = value["samples"][0]["elapsed_ms"]
            with self.subTest(change=change), self.assertRaises(qualification.Refused):
                qualification.validate_steady(value)

    def test_named_producer_missing_reordered_or_failed_refuses(self):
        document = fixture(self.directory)
        good = qualification.read_json(self.directory / "perf_gate.json")
        for producers in (good["producer_results"][:1], list(reversed(good["producer_results"])),
                          [good["producer_results"][0] | {"verdict": "fail"}, good["producer_results"][1]]):
            put(self.directory / "perf_gate.json", good | {"producer_results": producers})
            with self.assertRaises(qualification.Refused):
                self.validate(document)

    def test_lifecycle_must_be_in_authoritative_perf_and_counter_evidence(self):
        document = fixture(self.directory)
        document["lifecycle_gate"] = {"reason": "SEARCH_INDEX_INCOMPLETE", "max_ratio": 0.1,
                                      "producers": {"receipt_admission": {"metric_labels": {}}}}
        with self.assertRaises(qualification.Refused):
            self.validate(document)
        perf = qualification.read_json(self.directory / "perf_gate.json")
        perf.update(lifecycle_reason="SEARCH_INDEX_INCOMPLETE", thresholds={"max_expected_lifecycle_ratio": 0.1})
        perf["producer_results"][0].update(lifecycle_reason="SEARCH_INDEX_INCOMPLETE",
                                          thresholds={"max_expected_lifecycle_ratio": 0.1})
        put(self.directory / "perf_gate.json", perf)
        for phase in ("pre", "post"):
            put(self.directory / f"lifecycle_{phase}.json", {"metric": "lantern_search_calls_total", "replicas": [
                {"index": i, "endpoint": f"http://localhost:{port}/metrics", "series": [{"labels": {}, "value": 1}]}
                for i, port in enumerate((9390, 9391, 9392))]})
        self.assertEqual(self.validate(document)["lifecycle_gate"], "pass")
        put(self.directory / "lifecycle_post.json", {"metric": "wrong", "replicas": []})
        with self.assertRaises(qualification.Refused):
            self.validate(document)

    def test_collapsed_topology_refuses_even_positive_counts(self):
        document = fixture(self.directory)
        document["preflight"] = {"kind": "broad_illuminate"}
        good = {"walk_vertices": 193, "walk_edges": 8256, "walk_root_degree": 64,
                "walk_layer1_vertices": 64, "walk_layer2_vertices": 64, "walk_layer3_vertices": 64,
                "community_members": 32, "community_edges": 31, "ppr_vertices": 32}
        put(self.directory / "topology_preflight.json", good)
        self.assertEqual(self.validate(document)["topology_preflight"], "pass")
        put(self.directory / "topology_preflight.json", {key: 1 for key in good})
        with self.assertRaises(qualification.Refused):
            self.validate(document)

    def test_strict_json_rejects_duplicates_nonfinite_and_symlinks(self):
        path = self.directory / "bad.json"
        self.directory.mkdir()
        for text in ('{"verdict":"fail","verdict":"pass"}', '{"value":NaN}'):
            path.write_text(text)
            with self.assertRaises(qualification.Refused):
                qualification.read_json(path)
        path.unlink()
        path.symlink_to(Path(self.temporary.name) / "outside")
        with self.assertRaises(qualification.Refused):
            qualification.regular(path)

    def test_aggregate_exact_order_and_every_configured_gate(self):
        documents = {name: {} for name in qualification.CANONICAL}
        documents["search_churn"] = {"metric_gate": {"metrics": {}}, "semantic_gate": {"kind": "search"}}
        rows = [f"| `{name}` | `pass` | `{'pass' if name == 'search_churn' else '—'}` | "
                f"`{'pass' if name == 'search_churn' else '—'}` | `pass` | 100 | 10 | 0 |"
                for name in qualification.CANONICAL]
        report = "## Summary\n\n" + "\n".join(rows) + "\n## Details\n"
        qualification.validate_summary(report, documents)
        for bad in (report + "## Truncated\n", report.replace(rows[0], rows[1]),
                    report.replace("`pass` | `pass` | `pass`", "`pass` | `—` | `pass`")):
            with self.assertRaises(qualification.Refused):
                qualification.validate_summary(bad, documents)


class EnvironmentAndEvidenceTest(unittest.TestCase):
    def test_stable_compose_v2_v5_and_desktop_versions_are_supported(self):
        for version in ("2.24.4", "v2.37.2-desktop.1", "2.40.3-desktop.1",
                        "5.0.0", "5.1.4", "v5.1.4", "v5.1.4-desktop.1"):
            with self.subTest(version=version):
                qualification.require_compose_version(version)

    def test_legacy_future_prerelease_and_malformed_compose_versions_refuse(self):
        for version in ("1.29.2", "3.9.0", "4.0.0", "6.0.0", "50.1.4", "", "5", "5.1",
                        "5.01.4", "5.1.04", "5.\u0661.4", "5.1.4-rc.1", "5.1.4+unknown",
                        "5.1.4-desktop.01", "5.1.4-desktop", "5.1.4suffix", "5.1.4\nother",
                        "Docker Compose version v5.1.4"):
            with self.subTest(version=version), self.assertRaises(qualification.Refused):
                qualification.require_compose_version(version)

    def test_clear_override_and_redirection_without_dumping_proxy_secrets(self):
        original = {"PATH": "/bin", "LANTERN_BENCH_MUTATION_LOG_CAPACITY": "1", "SNAPSHOT_ROUNDS": "99",
                    "LANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER": "1", "GOFLAGS": "-overlay=secret",
                    "GOWORK": "/foreign", "GOENV": "/foreign-env", "BASH_ENV": "/inject",
                    "GIT_CONFIG_KEY_0": "remote.origin.url", "GOPROXY": "https://secret@example.invalid",
                    "HTTPS_PROXY": "secret-proxy", "GOTOOLCHAIN": "auto"}
        env, removed = qualification.clean_environment(original, Path("/frozen"), "manual-test", "0-3")
        self.assertEqual(env["GOWORK"], "/frozen/go.work")
        self.assertEqual(env["GOFLAGS"], "")
        self.assertEqual(env["GOENV"], "off")
        self.assertEqual(env["SNAPSHOT_ROUNDS"], "3")
        self.assertEqual(env["RELEASE_BENCH_BUDGET_SECONDS"], "0")
        self.assertNotIn("LANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER", env)
        self.assertEqual(env["HTTPS_PROXY"], "secret-proxy")
        self.assertEqual(env["GOPROXY"], original["GOPROXY"])
        self.assertNotIn("secret", json.dumps(removed))

    def test_inventory_rejects_file_and_symlink_changes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "input").write_text("reviewed")
            (root / "link").symlink_to("input")
            inventory = qualification.source_inventory(root)
            qualification.verify_inventory(root, inventory)
            (root / "input").write_text("changed")
            with self.assertRaises(qualification.Refused):
                qualification.verify_inventory(root, inventory)

    def test_inventory_rejects_added_go_shell_and_config_inputs(self):
        for name in ("server/extra.go", ".github/scripts/extra.sh", "deployment.yaml"):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                (root / "server").mkdir()
                (root / ".github/scripts").mkdir(parents=True)
                (root / "reviewed").write_text("input")
                inventory = qualification.source_inventory(root)
                (root / name).write_text("unreviewed consumed input")
                with self.assertRaises(qualification.Refused):
                    qualification.verify_inventory(root, inventory)

    def test_inventory_only_allows_real_generated_evidence_namespace(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "testbed/bench").mkdir(parents=True)
            inventory = qualification.source_inventory(root)
            output = root / "testbed/bench/out"
            (output / "scenario/run").mkdir(parents=True)
            (output / "scenario/run/report.md").write_text("generated evidence")
            qualification.verify_inventory(root, inventory)
            (root / "testbed/bench/outside.sh").write_text("not evidence")
            with self.assertRaises(qualification.Refused):
                qualification.verify_inventory(root, inventory)
            (root / "testbed/bench/outside.sh").unlink()
            (output / "scenario/run/report.md").unlink()
            (output / "scenario/run").rmdir()
            (output / "scenario").rmdir()
            output.rmdir()
            output.symlink_to(root / "testbed/bench")
            with self.assertRaises(qualification.Refused):
                qualification.verify_inventory(root, inventory)

    def test_private_failure_copy_is_owned_private_and_refuses_symlinks(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            private = root / "lantern-native-failure.ABC123"
            private.mkdir(mode=0o700)
            (private / "server.log").write_text("private content")
            log = root / "run.log"
            log.write_text("run.sh: private startup/runtime evidence retained at " + str(private) + "\n")
            destination = root / "retained"
            destination.mkdir(mode=0o700)
            originals = qualification.capture_private_outputs([log], destination, {root.resolve()})
            self.assertEqual(originals, [str(private)])
            copied = destination / private.name / "server.log"
            self.assertEqual(copied.read_text(), "private content")
            self.assertEqual(copied.stat().st_mode & 0o777, 0o600)
            (private / "unsafe").symlink_to(log)
            other = root / "other"
            other.mkdir()
            with self.assertRaises(qualification.Refused):
                qualification.capture_private_outputs([log], other, {root.resolve()})

    def test_new_initializer_private_receipt_is_copied_without_public_bytes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            private = root / "lantern-owned-init.ABC123"
            private.mkdir(mode=0o700)
            (private / "environment").write_text("private fixture material")
            log = root / "run.log"
            log.write_text("initializer private evidence retained at " + str(private) + "\n")
            destination = root / "retained"
            destination.mkdir(mode=0o700)
            originals = qualification.capture_private_outputs([log], destination, {root.resolve()})
            self.assertEqual(originals, [str(private)])
            self.assertEqual((destination / private.name / "environment").stat().st_mode & 0o777, 0o600)

    def test_host_lock_is_exclusive_and_does_not_unlink_evidence_of_prior_owner(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "private.lock"
            with qualification.HostLock(path) as first:
                self.assertIsInstance(first.fd, int)
                with self.assertRaises(BlockingIOError):
                    with qualification.HostLock(path):
                        self.fail("second owner acquired lock")
            self.assertTrue(path.exists())
            with qualification.HostLock(path):
                pass

    def bind_proof(self, root):
        private = root / "lantern-bind-proof.ABC123"
        private.mkdir(mode=0o700)
        proof = private / "proof.json"
        proof.write_bytes(b"private raw proof")
        proof.chmod(0o600)
        log = root / "run.log"
        log.write_text("read-only bind proof private evidence retained at " + str(private) + "\n")
        destination = root / "retained"
        destination.mkdir(mode=0o700)
        return private, log, destination

    def test_bind_proof_private_bytes_are_copied_once_from_exact_marker(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            private, log, destination = self.bind_proof(root)
            (private / "original-before.json").write_bytes(b'{"Env":["private-value"]}')
            (private / "original-before.json").chmod(0o600)
            originals = qualification.capture_private_outputs([log, log], destination, {root.resolve()})
            self.assertEqual(originals, [str(private)])
            self.assertEqual({p.name: p.read_bytes() for p in (destination / private.name).iterdir()},
                             {p.name: p.read_bytes() for p in private.iterdir()})
            self.assertEqual((destination / private.name).stat().st_mode & 0o777, 0o700)
            self.assertTrue(all(p.stat().st_mode & 0o777 == 0o600 for p in (destination / private.name).iterdir()))

    def test_bind_proof_marker_and_directory_profile_cannot_be_interchanged(self):
        for marker, name in (("initializer private evidence retained at ", "lantern-bind-proof.ABC123"),
                             ("read-only bind proof private evidence retained at ", "lantern-owned-init.ABC123")):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                private, log, destination = self.bind_proof(root)
                if private.name != name:
                    private.rename(root / name)
                log.write_text(marker + str(root / name) + "\n")
                with self.assertRaises(qualification.Refused):
                    qualification.capture_private_outputs([log], destination, {root.resolve()})

    def test_bind_proof_rejects_nonprivate_foreign_or_aliased_directories(self):
        for case in ("mode", "owner", "symlink", "outside", "preexisting-target"):
            with self.subTest(case=case), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                private, log, destination = self.bind_proof(root)
                if case == "mode":
                    private.chmod(0o755)
                elif case == "symlink":
                    moved = root / "actual"
                    private.rename(moved)
                    private.symlink_to(moved, target_is_directory=True)
                elif case == "preexisting-target":
                    (destination / private.name).symlink_to(private, target_is_directory=True)
                with patch.object(qualification.os, "getuid", return_value=os.getuid() + (case == "owner")):
                    with self.assertRaises((qualification.Refused, FileExistsError)):
                        qualification.capture_private_outputs([log], destination,
                            set() if case == "outside" else {root.resolve()})

    def test_bind_proof_rejects_unsafe_files_and_all_three_size_bounds(self):
        for case in ("mode", "directory", "symlink", "hardlink", "file-size", "file-count", "total-size"):
            with self.subTest(case=case), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                private, log, destination = self.bind_proof(root)
                proof = private / "proof.json"
                if case == "mode":
                    proof.chmod(0o644)
                elif case == "directory":
                    (private / "nested").mkdir(mode=0o700)
                elif case == "symlink":
                    (private / "alias").symlink_to(log)
                elif case == "hardlink":
                    os.link(proof, private / "alias")
                elif case == "file-count":
                    (private / "another").write_bytes(b"x")
                    (private / "another").chmod(0o600)
                with patch.object(qualification, "BIND_PROOF_MAX_FILE_BYTES", 1 if case == "file-size" else 128 * 1024), \
                        patch.object(qualification, "BIND_PROOF_MAX_FILES", 1 if case == "file-count" else 32), \
                        patch.object(qualification, "BIND_PROOF_MAX_BYTES", 1 if case == "total-size" else 2 * 1024 * 1024):
                    with self.assertRaises(qualification.Refused):
                        qualification.capture_private_outputs([log], destination, {root.resolve()})

    def test_bind_proof_detects_file_replacement_between_stat_and_open(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            private, log, destination = self.bind_proof(root)
            replacement = root / "replacement"
            replacement.write_bytes(b"replacement private proof")
            replacement.chmod(0o600)
            actual_open = os.open

            def replace_then_open(path, *args, **kwargs):
                if path == "proof.json" and "dir_fd" in kwargs:
                    replacement.replace(private / "proof.json")
                return actual_open(path, *args, **kwargs)
            with patch.object(qualification.os, "open", side_effect=replace_then_open):
                with self.assertRaises(qualification.Refused):
                    qualification.capture_private_outputs([log], destination, {root.resolve()})
            self.assertFalse((destination / private.name / "proof.json").exists())


class FakeClock:
    def __init__(self):
        self.now = 0.0

    def __call__(self):
        return self.now

    def sleep(self, value):
        self.now += value


class FakeProcess:
    pid = 12345

    def __init__(self, exits=True, descendants=False):
        self.returncode = None
        self.exits, self.descendants, self.live, self.polls = exits, descendants, True, 0
        self.output = b""

    def poll(self):
        self.polls += 1
        if self.exits and self.polls > 1:
            self.returncode = 0
            self.live = self.descendants
        return self.returncode

    def wait(self, timeout=None):
        self.live = False
        self.returncode = -signal.SIGTERM
        return self.returncode

    def communicate(self, timeout=None):
        if self.poll() is None:
            raise qualification.subprocess.TimeoutExpired("fake", timeout, output=self.output)
        return self.output, None


class SupervisorTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.clock = FakeClock()
        self.process = FakeProcess()
        self.calls, self.signals = [], []
        self.state = {"steps": []}

    def popen(self, *args, **kwargs):
        self.calls.append((args, kwargs))
        return self.process

    def killpg(self, group, number):
        self.signals.append((group, number))
        if not self.process.live:
            raise ProcessLookupError(errno.ESRCH, "gone")
        if number:
            self.process.live = False
            self.process.returncode = -number

    def supervisor(self):
        return qualification.Supervisor(Path(self.temporary.name), {"PATH": "/tools"}, 42,
            1, 10, self.state, lambda: None, clock=self.clock, sleep=self.clock.sleep,
            popen=self.popen, killpg=self.killpg)

    def test_children_inherit_lock_and_have_owned_process_group(self):
        supervisor = self.supervisor()
        code, _ = supervisor.command("fake", ["fake-binary"], self.temporary.name)
        self.assertEqual(code, 0)
        kwargs = self.calls[0][1]
        self.assertEqual(kwargs["pass_fds"], (42,))
        self.assertTrue(kwargs["start_new_session"])
        self.assertEqual(supervisor.groups, {})

    def test_deadline_stops_owned_group_and_records_incomplete_step(self):
        self.process.exits = False
        supervisor = self.supervisor()
        with self.assertRaises(qualification.Interrupted):
            supervisor.command("fake", ["fake-binary"], self.temporary.name)
        self.assertIn((self.process.pid, signal.SIGTERM), self.signals)
        self.assertEqual(supervisor.groups, {})
        self.assertIn("ended_utc", self.state["steps"][0])
        self.assertIsNone(self.state["steps"][0]["exit"])

    def test_descendant_survival_disqualifies_even_successful_leader(self):
        self.process.descendants = True
        with self.assertRaises(qualification.Refused):
            self.supervisor().command("fake", ["fake-binary"], self.temporary.name)
        self.assertIn((self.process.pid, signal.SIGTERM), self.signals)

    def test_signal_prevents_next_workload_but_allows_cleanup(self):
        supervisor = self.supervisor()
        supervisor.interrupted(signal.SIGTERM, None)
        with self.assertRaises(qualification.Refused):
            supervisor.command("next-workload", ["fake"], self.temporary.name)
        supervisor.command("cleanup", ["fake"], self.temporary.name, cleanup=True)
        self.assertEqual(len(self.calls), 1)

    def test_custody_uses_memory_and_still_inspects_when_log_open_would_fail(self):
        supervisor = self.supervisor()
        self.process.output = b"owned-container\n"
        with patch.object(Path, "open", side_effect=OSError("evidence filesystem unavailable")):
            code, response = supervisor.command("lookup", ["fake"], self.temporary.name, custody=True)
        self.assertEqual((code, response), (0, "owned-container"))
        self.assertEqual(self.calls[0][1]["stdout"], qualification.subprocess.PIPE)
        self.assertEqual(self.calls[0][1]["pass_fds"], (42,))
        self.assertIsNone(self.state["steps"][0]["log"])
        self.assertEqual(supervisor.groups, {})

    def test_custody_survives_progress_fsync_failure_and_prevents_more_work(self):
        supervisor = self.supervisor()
        supervisor.save = lambda: qualification.atomic_json(Path(self.temporary.name) / "progress.json", self.state)
        with patch.object(qualification.os, "fsync", side_effect=OSError("disk failed")):
            self.assertEqual(supervisor.command("lookup", ["fake"], self.temporary.name, custody=True)[0], 0)
        self.assertTrue(self.state["evidence_io_failed"])
        self.assertEqual(self.state["verdict"], "fail")
        self.assertEqual(supervisor.groups, {})
        with self.assertRaises(qualification.Refused):
            supervisor.command("next-workload", ["fake"], self.temporary.name)

    def test_closed_evidence_stream_failure_also_cannot_escape_best_effort_persistence(self):
        state = {"verdict": "running"}
        save = lambda: (_ for _ in ()).throw(ValueError("I/O operation on closed file"))
        self.assertFalse(qualification.best_effort_save(save, state))
        self.assertEqual(state["verdict"], "fail")
        self.assertTrue(state["evidence_io_failed"])

    def test_custody_timeout_does_not_convert_failed_lookup_to_empty_absence(self):
        supervisor = self.supervisor()
        self.process.exits = False
        supervisor.save = lambda: (_ for _ in ()).throw(OSError("disk failed"))
        with self.assertRaises(qualification.Refused):
            supervisor.command("lookup", ["fake"], self.temporary.name, timeout=0.1, custody=True)
        self.assertEqual(supervisor.groups, {})
        self.assertTrue(self.state["evidence_io_failed"])


class CleanupAndOrderTest(unittest.TestCase):
    def make_driver(self, directory):
        args = argparse.Namespace(repo=directory, evidence_dir=directory / "evidence", source_sha=SHA,
            source_tree=TREE, campaign="manual-test", budget_seconds=10800, cleanup_seconds=600,
            cpuset="0-3", bash=None)
        args.evidence_dir.mkdir(mode=0o700)
        driver = qualification.Qualification(args)
        driver.project_preflight_passed = True
        driver.image_id = IMAGE
        driver.image_ref = REFERENCE
        clock = FakeClock()
        driver.supervisor = argparse.Namespace(stop_all=lambda: None, clock=clock,
            sleep=clock.sleep, cleanup_deadline=600)
        return driver

    def test_initializer_refusal_retains_inactive_resources_without_delete(self):
        with tempfile.TemporaryDirectory() as temporary:
            driver = self.make_driver(Path(temporary))
            driver.resources = lambda **_: {"containers": ["1" * 64], "volumes": ["private-state"], "networks": ["private-net"]}
            driver.identities = lambda *_, **__: [identity(driver, "false")]
            driver.tool = lambda *_, **__: self.fail("inactive refusal must never issue Docker mutation")
            driver.cleanup()
            self.assertEqual(driver.state["cleanup"]["disposition"], "retained_inactive")

    def test_unsupported_compose_fails_and_seals_without_project_or_workload_access(self):
        with tempfile.TemporaryDirectory() as temporary:
            driver = self.make_driver(Path(temporary))
            driver.project_preflight_passed = False
            driver.preflight = lambda: qualification.require_compose_version("6.0.0")
            driver.resources = lambda **_: self.fail("version refusal must not inspect a project")
            driver.image = lambda: self.fail("version refusal must not build an image")
            driver.call = lambda *_args, **_kwargs: self.fail("version refusal must not launch workloads")
            with patch.object(qualification.signal, "signal", return_value=signal.SIG_DFL):
                self.assertEqual(driver.execute(42), 1)
            receipt = qualification.read_json(driver.evidence / "progress.json")
            self.assertEqual(receipt["verdict"], "fail")
            self.assertEqual(receipt["cleanup"]["disposition"], "not_started")
            self.assertEqual(receipt["scenario_verdicts"], {})
            self.assertIn("progress.json", (driver.evidence / "SHA256SUMS").read_text())

    def test_stop_only_verified_owned_container_then_preserve_state(self):
        with tempfile.TemporaryDirectory() as temporary:
            driver = self.make_driver(Path(temporary))
            running = [True]
            driver.resources = lambda **_: {"containers": ["1" * 64], "volumes": ["state"], "networks": []}
            driver.identities = lambda *_, **__: [identity(driver, "true" if running[0] else "false")]
            calls = []

            def stop(*args, **_):
                calls.append(args)
                running[0] = False
            driver.tool = stop
            driver.cleanup()
            self.assertEqual(calls, [("docker", "stop", "--time", "45", "1" * 64)])
            self.assertEqual(driver.state["cleanup"]["disposition"], "retained_inactive")

    def test_unknown_running_identity_keeps_custody_until_operator_resolves(self):
        with tempfile.TemporaryDirectory() as temporary:
            driver = self.make_driver(Path(temporary))
            checks = [0]
            driver.resources = lambda **_: {"containers": ["1" * 64] if checks[0] == 0 else [], "volumes": [], "networks": []}
            foreign = identity(driver)
            foreign[1:3] = ["foreign", "foreign"]
            driver.identities = lambda ids, **_: [foreign] if ids else []
            driver.supervisor.sleep = lambda _: checks.__setitem__(0, checks[0] + 1)
            driver.tool = lambda *_, **__: self.fail("foreign container must not be stopped")
            driver.cleanup()
            self.assertEqual(checks[0], 1)
            self.assertEqual(driver.state["cleanup"]["disposition"], "absent")

    def test_cleanup_keeps_real_private_lock_through_save_log_and_lookup_failures(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            driver = self.make_driver(root)
            path = root / "custody.lock"
            checks = [0]
            driver.save = lambda: (_ for _ in ()).throw(OSError("evidence unavailable"))
            foreign = identity(driver)
            foreign[1] = "foreign"

            def resources(**_):
                with self.assertRaises(BlockingIOError):
                    with qualification.HostLock(path):
                        self.fail("custody lock released before inactivity proof")
                checks[0] += 1
                if checks[0] == 1:
                    raise OSError("Docker lookup temporarily unavailable")
                return {"containers": ["1" * 64] if checks[0] == 2 else [], "volumes": [], "networks": []}
            driver.resources = resources
            driver.identities = lambda ids, **_: [foreign] if ids else []
            driver.tool = lambda *_, **__: self.fail("foreign resources must not be stopped")
            sleeps = [0]
            def bounded_sleep(_):
                sleeps[0] += 1
                self.assertLess(sleeps[0], 4, "fake custody did not reach inactivity proof")
            driver.supervisor.sleep = bounded_sleep
            with qualification.HostLock(path), patch("builtins.print", side_effect=OSError("console unavailable")):
                driver.cleanup()
                self.assertEqual(checks[0], 3)
                self.assertEqual(driver.state["cleanup"]["disposition"], "absent")
            self.assertTrue(driver.state["evidence_io_failed"])
            self.assertEqual(driver.state["verdict"], "fail")
            with qualification.HostLock(path):
                pass

    def test_cleanup_before_project_start_keeps_custody_until_owned_children_stop(self):
        with tempfile.TemporaryDirectory() as temporary:
            driver = self.make_driver(Path(temporary))
            driver.project_preflight_passed = False
            attempts = [0]
            def stop():
                attempts[0] += 1
                if attempts[0] == 1:
                    raise OSError("child stop not yet proven")
            driver.supervisor.stop_all = stop
            driver.save = lambda: (_ for _ in ()).throw(OSError("disk unavailable"))
            driver.resources = lambda **_: self.fail("no project resource is owned")
            driver.cleanup()
            self.assertEqual(attempts[0], 2)
            self.assertEqual(driver.state["cleanup"]["disposition"], "not_started")
            self.assertTrue(driver.state["evidence_io_failed"])

    def test_prometheus_requires_frozen_reference_immutable_image_and_user_before_stop(self):
        with tempfile.TemporaryDirectory() as temporary:
            driver = self.make_driver(Path(temporary))
            driver.prometheus_ref = "prom/prometheus:fixture"
            row = identity(driver, service="prometheus")
            row[1], row[2], row[7] = "sha256:" + "f" * 64, "nobody", driver.prometheus_ref
            commands = []

            def inspect(*args, **_):
                commands.append(args)
                return row[1] + "|nobody"
            driver.tool = inspect
            self.assertTrue(driver.owned_row(row))
            self.assertEqual(len(commands), 1)
            self.assertTrue(driver.owned_row(row))
            for field in (1, 2, 4, 7):
                changed = list(row)
                changed[field] = "foreign"
                self.assertFalse(driver.owned_row(changed))

    def test_security_initializer_requires_name_owner_nonce_oneoff_and_exact_command(self):
        with tempfile.TemporaryDirectory() as temporary:
            driver = self.make_driver(Path(temporary))
            row = identity(driver)
            row[7:14] = [IMAGE, "/manual-test-lantern-0-security-init", "compose-security-initializer",
                         "True", "lantern-owned-init.ABC123", '["/app/lantern"]', '["init-security"]']
            self.assertTrue(driver.owned_row(row))
            for field in (7, 8, 9, 10, 11, 12, 13):
                changed = list(row)
                changed[field] = ""
                self.assertFalse(driver.owned_row(changed), field)

    def test_root_volume_initializer_requires_scoped_nonce_name_image_and_redacted_command_shape(self):
        with tempfile.TemporaryDirectory() as temporary:
            driver = self.make_driver(Path(temporary))
            row = identity(driver, service="volume-init")
            row[2] = "0:0"
            row[7:14] = [IMAGE, "/manual-test-volume-init", "compose-volume-initializer", "True",
                "lantern-owned-init.ABC123", '["/bin/sh"]', json.dumps({"argc": 5, "kind": "-c",
                    "fixture": "fixture", "owner": driver.owner, "count": "9"})]
            self.assertTrue(driver.owned_row(row))
            for field in (1, 2, 4, 7, 8, 9, 10, 11, 12, 13):
                changed = list(row)
                changed[field] = ""
                self.assertFalse(driver.owned_row(changed), field)
            row[13] = "[]"
            self.assertFalse(driver.owned_row(row))

    def test_bind_probe_requires_full_identity_fixed_busybox_command_and_resource_profile(self):
        with tempfile.TemporaryDirectory() as temporary:
            driver = self.make_driver(Path(temporary))
            for service in qualification.SERVICES:
                for pairs in (1, 2):
                    with self.subTest(service=service, pairs=pairs):
                        row = bind_probe_identity(driver, pairs, service)
                        self.assertTrue(driver.owned_row(row))
                        profile = json.loads(row[13])
                        profile["CapAdd"] = []
                        profile["Mounts"].reverse()
                        profile["HostMounts"].reverse()
                        for mount in profile["Mounts"]:
                            mount["Mode"] = "ro"
                        for mount in profile["HostMounts"]:
                            mount["BindOptions"]["CreateMountpoint"] = False
                        row[13] = json.dumps(profile)
                        self.assertTrue(driver.owned_row(row))
            row = bind_probe_identity(driver)
            for field, value in ((0, "1" * 12), (0, "G" * 64), (1, "foreign"), (2, "0:0"),
                    (4, "other-project"), (5, "other-bind-proof"), (7, REFERENCE),
                    (8, "/manual-test-other-bind-proof"), (9, "other-owner"), (10, "False"),
                    (11, "other-nonce.ABC123"), (12, '["/bin/sh"]'), (13, "null"), (13, "invalid")):
                with self.subTest(field=field, value=value):
                    changed = list(row)
                    changed[field] = value
                    self.assertFalse(driver.owned_row(changed))
            for key in json.loads(row[13]):
                with self.subTest(missing=key):
                    profile = json.loads(row[13])
                    del profile[key]
                    changed = list(row)
                    changed[13] = json.dumps(profile)
                    self.assertFalse(driver.owned_row(changed))

    def test_bind_probe_refuses_different_commands_mounts_and_privileges(self):
        with tempfile.TemporaryDirectory() as temporary:
            driver = self.make_driver(Path(temporary))
            row = bind_probe_identity(driver)
            cases = {"NetworkMode": "bridge", "ReadonlyRootfs": False, "Privileged": True,
                "AutoRemove": True, "CapDrop": [], "CapAdd": ["SYS_ADMIN"], "SecurityOpt": [],
                "Memory": 0, "NanoCpus": 0, "PidsLimit": 0,
                "VolumesFromCount": 1, "TmpfsCount": 1, "ConfigVolumeCount": 1,
                "DeviceCount": 1, "DeviceRequestCount": 1, "DeviceCgroupRuleCount": 1,
                "PidMode": "host", "UTSMode": "host", "UsernsMode": "host",
                "PublishAllPorts": True, "PortBindingCount": 1, "BindCount": 2,
                "RestartPolicy": {"Name": "always", "MaximumRetryCount": 0}, "unexpected": True}
            for key, value in cases.items():
                with self.subTest(profile=key):
                    profile = json.loads(row[13])
                    profile[key] = value
                    changed = list(row)
                    changed[13] = json.dumps(profile)
                    self.assertFalse(driver.owned_row(changed))
            for change in ("writable", "missing", "duplicate", "foreign-target", "type", "create-source", "option", "extra-key"):
                with self.subTest(host_mount=change):
                    profile = json.loads(row[13])
                    mount = profile["HostMounts"][0]
                    if change == "missing":
                        profile["HostMounts"].pop()
                    elif change == "duplicate":
                        profile["HostMounts"][1] = dict(mount)
                    elif change == "create-source":
                        mount["BindOptions"]["CreateMountpoint"] = True
                    else:
                        key, value = {"writable": ("ReadOnly", False), "foreign-target": ("Target", "/unapproved"),
                            "type": ("Type", "volume"), "option": ("UnsupportedOptions", True),
                            "extra-key": ("Source", "/unapproved")}[change]
                        mount[key] = value
                    changed = list(row)
                    changed[13] = json.dumps(profile)
                    self.assertFalse(driver.owned_row(changed))
            commands = [["sh", "-c", "stat"], ["stat", "-c", "%d:%i:%F", "/etc/passwd"],
                ["stat", "-c", "%d:%i:%F", "/proof/clone-0", "/proof/original-0"],
                ["stat", "-c", "%d:%i:%F", "/proof/original-2", "/proof/clone-2"],
                json.loads(row[13])["Cmd"] + ["/proof/original-1"]]
            for command in commands:
                with self.subTest(command=command):
                    profile = json.loads(row[13])
                    profile["Cmd"] = command
                    changed = list(row)
                    changed[13] = json.dumps(profile)
                    self.assertFalse(driver.owned_row(changed))
            for change in ("writable", "missing", "duplicate", "foreign-target", "type", "mode", "propagation", "extra-key"):
                with self.subTest(mount=change):
                    profile = json.loads(row[13])
                    mount = profile["Mounts"][0]
                    if change == "missing":
                        profile["Mounts"].pop()
                    elif change == "duplicate":
                        profile["Mounts"][1] = dict(mount)
                    else:
                        key, value = {"writable": ("RW", True), "foreign-target": ("Destination", "/etc/passwd"),
                            "type": ("Type", "volume"), "mode": ("Mode", "rw"),
                            "propagation": ("Propagation", "shared"), "extra-key": ("Source", "/unapproved")}[change]
                        mount[key] = value
                    changed = list(row)
                    changed[13] = json.dumps(profile)
                    self.assertFalse(driver.owned_row(changed))

    def test_cleanup_stops_only_verified_probe_and_retains_inactive_proof_resources(self):
        with tempfile.TemporaryDirectory() as temporary:
            driver = self.make_driver(Path(temporary))
            row = bind_probe_identity(driver, pairs=2)
            driver.resources = lambda **_: {"containers": [row[0]], "volumes": [], "networks": []}
            driver.identities = lambda *_, **__: [row]
            calls = []

            def stop(*args, **kwargs):
                calls.append((args, kwargs))
                row[3] = "false"
            driver.tool = stop
            driver.cleanup()
            self.assertEqual(calls, [(("docker", "stop", "--time", "45", row[0]), {"timeout": 60, "cleanup": True})])
            self.assertEqual(driver.state["cleanup"]["disposition"], "retained_inactive")

    def test_unknown_probe_arguments_keep_custody_without_mutation(self):
        with tempfile.TemporaryDirectory() as temporary:
            driver = self.make_driver(Path(temporary))
            row = bind_probe_identity(driver)
            profile = json.loads(row[13])
            profile["Cmd"].append("/unapproved")
            row[13] = json.dumps(profile)
            checks = [0]
            driver.resources = lambda **_: {"containers": [row[0]] if checks[0] == 0 else [], "volumes": [], "networks": []}
            driver.identities = lambda ids, **_: [row] if ids else []
            driver.supervisor.sleep = lambda _: checks.__setitem__(0, checks[0] + 1)
            driver.tool = lambda *_, **__: self.fail("unrecognized probe must not be stopped")
            driver.cleanup()
            self.assertEqual(checks[0], 1)
            self.assertEqual(driver.state["cleanup"]["disposition"], "absent")

    def test_probe_inspection_uses_existing_call_without_exporting_env_or_host_source(self):
        with tempfile.TemporaryDirectory() as temporary:
            driver = self.make_driver(Path(temporary))
            row = bind_probe_identity(driver)
            calls = []

            def inspect(*args, **kwargs):
                calls.append((args, kwargs))
                return "|".join(row)
            driver.tool = inspect
            self.assertEqual(driver.identities([row[0]], custody=True), [row])
            self.assertEqual(len(calls), 1)
            args, kwargs = calls[0]
            self.assertEqual(args[:3], ("docker", "inspect", "--format"))
            self.assertNotIn(".Config.Env", args[3])
            self.assertNotIn(".Source", args[3])
            self.assertEqual(kwargs, {"timeout": 15, "cleanup": True, "custody": True})

    def test_volume_or_network_presence_and_lookup_failure_refuse_next_phase(self):
        with tempfile.TemporaryDirectory() as temporary:
            driver = self.make_driver(Path(temporary))
            for kind in ("volumes", "networks"):
                value = {"containers": [], "volumes": [], "networks": []}
                value[kind] = ["retained"]
                driver.resources = lambda: value
                with self.assertRaises(qualification.Refused):
                    driver.absent()
            def unavailable():
                raise qualification.Refused("Docker lookup failed")
            driver.resources = unavailable
            with self.assertRaises(qualification.Refused):
                driver.absent()

    def test_failed_aggregate_never_launches_receipts_and_still_hashes_failure(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            driver = self.make_driver(root)
            calls = []
            driver.preflight = lambda: None
            driver.image = lambda: None
            driver.absent = lambda: None
            driver.cleanup = lambda: driver.state.update(cleanup={"disposition": "absent"})
            driver.tools = {"bash": "fake-bash"}

            def call(name, args, **_):
                calls.append((name, args))
                raise qualification.Refused("aggregate8 failed")
            driver.call = call
            driver.state["tools"] = {}
            # Empty-source fake preflight still supplies a minimal review config.
            with patch.object(qualification, "atomic_json", wraps=qualification.atomic_json), patch.object(
                    qualification.signal, "signal", return_value=signal.SIG_DFL):
                result = driver.execute(42)
            self.assertEqual(result, 1)
            self.assertEqual([name for name, _ in calls], ["canonical-sweep"])
            self.assertEqual(calls[0][1][1], "testbed/bench/release.sh")
            self.assertEqual(qualification.read_json(driver.evidence / "progress.json")["verdict"], "fail")
            self.assertIn("progress.json", (driver.evidence / "SHA256SUMS").read_text())

    def test_successful_aggregate_precedes_each_sequential_receipt_and_failure_stops_successors(self):
        for failure in (None, qualification.RECEIPTS[1], "retained-" + qualification.RECEIPTS[1]):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as temporary:
                driver = self.make_driver(Path(temporary))
                calls = []
                driver.preflight = lambda: driver.state.update(tools={})
                driver.image = lambda: None
                driver.absent = lambda: None
                driver.cleanup = lambda: driver.state.update(cleanup={"disposition": "absent"})
                driver.tools = {"bash": "fake-bash"}
                driver.documents = {}
                def check(names):
                    calls.append(("checked-retained" if driver.using_retained else "checked", tuple(names)))
                    if driver.using_retained:
                        driver.state.setdefault("retained_pairs", {}).update({name: {"verdict": "pass"} for name in names})
                driver.check_runs = check
                def retained_image():
                    calls.append(("retained-image", None))
                    driver.using_retained = True
                driver.retained_image = retained_image

                def call(name, args, **_):
                    calls.append((name, None))
                    if name == failure:
                        raise qualification.Refused("declared family failed")
                    if name == "canonical-sweep":
                        (driver.evidence / "bench-report.md").write_text("fake reviewed report")
                driver.call = call
                with patch.object(qualification, "validate_summary"), patch.object(
                        qualification.signal, "signal", return_value=signal.SIG_DFL):
                    result = driver.execute(42)
                self.assertEqual(calls[:2], [("canonical-sweep", None), ("checked", qualification.CANONICAL)])
                launched = [name for name, _ in calls if name in qualification.RECEIPTS]
                expected = qualification.RECEIPTS[:2] if failure == qualification.RECEIPTS[1] else qualification.RECEIPTS
                self.assertEqual(launched, list(expected))
                tagged = [name for name, _ in calls if name.startswith("retained-") and name != "retained-image"]
                expected_tagged = ([] if failure == qualification.RECEIPTS[1] else
                                   ["retained-" + name for name in (qualification.RECEIPTS[:2] if failure else qualification.RECEIPTS)])
                self.assertEqual(tagged, expected_tagged)
                if tagged:
                    self.assertLess(calls.index((qualification.RECEIPTS[-1], None)), calls.index(("retained-image", None)))
                self.assertEqual(result, 0 if failure is None else 1)


if __name__ == "__main__":
    unittest.main()
