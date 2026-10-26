#!/usr/bin/env python3
"""Manual, non-publishing exact-source local 8+4 qualification (#1891).

Example (after review/merge, from a clean checkout):
  python3 .github/scripts/local_release_qualification.py --source-sha FULL_SHA \
    --source-tree FULL_TREE --campaign local-20261026 --evidence-dir /private/path/new-run

The default budget is 10800 seconds plus 600 seconds for cleanup. release.sh
retains its aggregate-eight failure behavior; receipts start only after all
eight qualify. No retries, workload changes, extra GC, NTP probes, or publication.
Canonical OFF pre/post GC remains unchanged, outside the steady window.

SIGINT/TERM stop owned process groups. The host lock is inherited by children;
SIGKILL cannot be trapped and daemon containers do not inherit file descriptors.
Fresh-state checks therefore remain mandatory after any killed driver. If owned
resources cannot be proven inactive, this manual process retains lock custody
and performs read-only checks until the operator resolves them. It never resets
an initializer refusal or deletes retained state. Raw evidence is private.

Actual container IDs/runtime users/CPU sets come from the harness's existing
pre/post inspections. The driver adds no periodic observer during measured load.
Only a completed, verified SHA256SUMS plus exit 0 qualifies; progress alone is
not a completed qualification.
"""

import argparse
import datetime
import fcntl
import hashlib
import json
import math
import os
from pathlib import Path
import re
import shutil
import signal
import stat
import subprocess
import sys
import tarfile
import time


CANONICAL = ("broad_rw", "broad_illuminate", "broad_mutate", "edge_contrib_idempotent",
             "ttl_churn", "many_subscribers", "search_churn", "replication_apply_churn")
RECEIPTS = ("receipt_vertex_put_admission_lookup", "receipt_vertex_delete_admission_lookup",
            "receipt_admission_lookup", "receipt_edge_add_admission_lookup")
SERVICES = ("lantern-0", "lantern-1", "lantern-2")
LOCK_PATH = Path("/private/tmp/lantern-heavy-local.lock")
HEX40 = re.compile(r"[0-9a-f]{40}\Z")
HEX64 = re.compile(r"[0-9a-f]{64}\Z")
IMAGE_ID = re.compile(r"sha256:[0-9a-f]{64}\Z")
PRIVATE_MESSAGE = re.compile(
    r"^run\.sh: private (?:startup/runtime|bridge|ghz producer diagnostics) evidence retained at (.+)$")


class Refused(RuntimeError):
    pass


class Interrupted(Refused):
    pass


def require(condition, message):
    if not condition:
        raise Refused(message)


def require_compose_version(value):
    # Compose v5 retains the v2 CLI; Desktop stable builds add -desktop.N.
    require(re.fullmatch(
        r"v?(?:2|5)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)"
        r"(?:-desktop\.(?:0|[1-9][0-9]*))?", value),
        "stable Docker Compose v2 or v5 CLI required")


def utc():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def digest(path):
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


def regular(path):
    require(stat.S_ISREG(path.lstat().st_mode), f"regular file required: {path.name}")
    return path


def atomic_json(path, value):
    temporary = path.with_name(path.name + ".tmp")
    with temporary.open("w", encoding="utf-8") as stream:
        os.chmod(temporary, 0o600)
        json.dump(value, stream, indent=2, sort_keys=True, allow_nan=False)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())
    temporary.replace(path)


def best_effort_save(save, state):
    """Evidence storage must never control release of unproven lock custody."""
    try:
        save()
        return True
    except Exception:
        state["evidence_io_failed"] = True
        state["verdict"] = "fail"
        return False


def read_json(path):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            require(key not in result, "duplicate JSON key")
            result[key] = value
        return result
    regular(path)
    require(path.stat().st_size <= 64 * 1024 * 1024, "oversized JSON artifact")
    return json.loads(path.read_text(), object_pairs_hook=unique,
                      parse_constant=lambda _: (_ for _ in ()).throw(Refused("non-finite JSON")))


def positive(value):
    return (type(value) is int and 0 < value < 2 ** 53 or
            type(value) is float and math.isfinite(value) and value > 0)


def positive_integer(value):
    return type(value) is int and 0 < value < 2 ** 53


def clean_environment(original, source, campaign, cpuset):
    env = dict(original)
    removed = []
    exact = {"SNAPSHOT_ROUNDS", "PPROF_CPU", "SEARCH_PROBE_TIMEOUT", "KEEP_UP", "SKIP_UP",
             "KEEP_OUT", "LEAK_GATE_ONLY", "RELEASE_BENCH_BUDGET_SECONDS", "PROM_URL", "TAG",
             "COMMIT", "RUNNER", "COMPOSE_PROJECT_NAME", "COMPOSE_FILE", "COMPOSE_PROFILES",
             "COMPOSE_ENV_FILES", "COMPOSE_PATH_SEPARATOR", "BASH_ENV", "ENV", "SHELLOPTS",
             "BASHOPTS", "CDPATH", "GOFLAGS", "GOWORK", "GOENV", "GOOS", "GOARCH",
             "GOEXPERIMENT", "GO111MODULE", "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE",
             "GIT_COMMON_DIR", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT"}
    exact.update({"PYTHONPATH", "PYTHONHOME", "PYTHONUSERBASE", "PYTHONSTARTUP", "PYTHONINSPECT",
                  "PYTHONOPTIMIZE", "PYTHONPYCACHEPREFIX", "PYTHONWARNINGS"})
    for key in list(env):
        if key in exact or key.startswith(("LANTERN_", "EXPECTED_LANTERN_", "GIT_CONFIG_KEY_",
                                          "GIT_CONFIG_VALUE_", "BASH_FUNC_")):
            removed.append(key)
            del env[key]
    env.update(GOFLAGS="", GOWORK=str(source / "go.work"), GOENV="off", PYTHONNOUSERSITE="1",
               COMPOSE_PROJECT_NAME=campaign, LANTERN_BENCH_CPUSET=cpuset,
               KEEP_OUT="1", LEAK_GATE_ONLY="1", RELEASE_BENCH_BUDGET_SECONDS="0",
               KEEP_UP="0", SKIP_UP="0", PPROF_CPU="0", SNAPSHOT_ROUNDS="3",
               LANTERN_BENCH_NATURAL_GC_OBSERVE="0", TAG="local-qualification")
    return env, sorted(removed)


class HostLock:
    def __init__(self, path=LOCK_PATH):
        self.path = path
        self.fd = None

    def __enter__(self):
        self.fd = os.open(self.path, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
        try:
            info = os.fstat(self.fd)
            require(stat.S_ISREG(info.st_mode) and info.st_uid == os.getuid(), "unsafe host lock")
            fcntl.flock(self.fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BaseException:
            os.close(self.fd)
            self.fd = None
            raise
        return self

    def __exit__(self, *_):
        # Do not explicitly LOCK_UN: inherited descriptors keep the same flock
        # if an untrappable parent death leaves an owned child alive.
        os.close(self.fd)
        self.fd = None


class Supervisor:
    def __init__(self, directory, env, lock_fd, budget, cleanup, state, save,
                 clock=time.monotonic, sleep=time.sleep, popen=subprocess.Popen,
                 killpg=os.killpg):
        self.directory, self.env, self.lock_fd = directory, env, lock_fd
        self.clock, self.sleep, self.popen, self.killpg = clock, sleep, popen, killpg
        self.deadline = clock() + budget
        self.cleanup_deadline = self.deadline + cleanup
        self.state, self.save = state, save
        self.groups = {}
        self.cancelled = None
        self.counter = 0

    def interrupted(self, number, _frame):
        self.cancelled = number

    def alive(self, group):
        try:
            self.killpg(group, 0)
            return True
        except ProcessLookupError:
            return False

    def stop_all(self):
        for group in self.groups:
            if self.alive(group):
                self.killpg(group, signal.SIGTERM)
        end = min(self.clock() + 30, self.cleanup_deadline)
        while self.clock() < end:
            for process in self.groups.values():
                process.poll()
            if not any(self.alive(group) for group in self.groups):
                break
            self.sleep(0.1)
        for group, process in list(self.groups.items()):
            if self.alive(group):
                self.killpg(group, signal.SIGKILL)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                pass
            if not self.alive(group):
                del self.groups[group]
        require(not self.groups, "owned child shutdown remains unproven")

    def command(self, name, args, cwd, timeout=None, cleanup=False, custody=False,
                allow_failure=False):
        require(cleanup or custody or not self.cancelled, "campaign interrupted")
        require(cleanup or custody or not self.state.get("evidence_io_failed"), "evidence storage failed")
        end = self.clock() + (timeout if timeout is not None else 10800)
        if not custody:
            end = min(end, self.cleanup_deadline if cleanup else self.deadline)
        require(end > self.clock(), "campaign budget exhausted")
        self.counter += 1
        log = self.directory / f"{self.counter:03d}-{name}.log"
        record = {"name": name, "started_utc": utc(), "log": log.name, "exit": None}
        self.state["steps"].append(record)
        self.state["active"] = name
        if cleanup or custody:
            return self.custody_command(record, args, cwd, end, allow_failure)
        self.save()
        with log.open("wb") as stream:
            process = self.popen(list(map(str, args)), cwd=cwd, env=self.env,
                                 stdout=stream, stderr=subprocess.STDOUT,
                                 start_new_session=True, pass_fds=(self.lock_fd,))
            self.groups[process.pid] = process
            try:
                while process.poll() is None:
                    if (self.cancelled and not (cleanup or custody)) or self.clock() >= end:
                        raise Interrupted("interrupted or command deadline exceeded: " + name)
                    self.sleep(0.1)
                record["exit"] = process.returncode
                require(not self.alive(process.pid), "owned descendants outlived command: " + name)
                del self.groups[process.pid]
            except BaseException:
                self.stop_all()
                raise
            finally:
                record["ended_utc"] = utc()
                self.save()
        require(allow_failure or process.returncode == 0, name + " failed; raw log retained")
        return process.returncode, log.read_text(errors="replace").strip()

    def custody_command(self, record, args, cwd, end, allow_failure):
        # Cleanup lookup/stop responses are small identity records. Capture them
        # in memory so disk-full, save/fsync or log-open failures cannot disable
        # supervision or make a failed lookup look like absence. Never fall back
        # to /dev/null: an actual response is still required for inactivity proof.
        record.update(log=None, capture="bounded_memory")
        best_effort_save(self.save, self.state)
        process = self.popen(list(map(str, args)), cwd=cwd, env=self.env,
                             stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                             start_new_session=True, pass_fds=(self.lock_fd,))
        self.groups[process.pid] = process
        output = b""
        try:
            while True:
                require(self.clock() < end, "cleanup command deadline exceeded")
                try:
                    output, _ = process.communicate(timeout=min(0.1, end - self.clock()))
                    break
                except subprocess.TimeoutExpired as error:
                    require(len(error.output or b"") <= 1024 * 1024, "oversized cleanup response")
                    self.sleep(0.05)
            require(len(output) <= 1024 * 1024, "oversized cleanup response")
            record["exit"] = process.returncode
            require(not self.alive(process.pid), "owned cleanup descendants outlived command")
            del self.groups[process.pid]
            record["output"] = output.decode(errors="replace").strip()
            require(allow_failure or process.returncode == 0, "cleanup command failed: " + record["name"])
            return process.returncode, record["output"]
        except BaseException:
            self.stop_all()
            raise
        finally:
            record["ended_utc"] = utc()
            best_effort_save(self.save, self.state)


def source_inventory(source):
    result = {}
    for path in sorted(source.rglob("*")):
        if path.is_symlink():
            result[str(path.relative_to(source))] = {"link": os.readlink(path)}
        elif path.is_file():
            result[str(path.relative_to(source))] = {
                "sha256": digest(path), "mode": stat.S_IMODE(path.stat().st_mode)}
        elif path.is_dir():
            result[str(path.relative_to(source))] = {"directory": True,
                "mode": stat.S_IMODE(path.stat().st_mode)}
        else:
            raise Refused("unsupported frozen source entry")
    return result


def verify_inventory(source, inventory):
    generated = source / "testbed/bench/out"
    require(not generated.is_symlink() and (not generated.exists() or generated.is_dir()),
            "generated evidence namespace must be a real directory")
    for path in source.rglob("*"):
        name = str(path.relative_to(source))
        if path == generated or path.is_relative_to(generated):
            continue
        require(name in inventory, "added frozen source input: " + name)
    for name, expected in inventory.items():
        path = source / name
        if "link" in expected:
            require(path.is_symlink() and os.readlink(path) == expected["link"], "source symlink drift")
        elif expected.get("directory"):
            require(not path.is_symlink() and path.is_dir() and
                    stat.S_IMODE(path.stat().st_mode) == expected["mode"], "frozen source directory drift")
        else:
            regular(path)
            require(digest(path) == expected["sha256"] and
                    stat.S_IMODE(path.stat().st_mode) == expected["mode"], "frozen source drift: " + name)


def validate_runtime(value):
    require(isinstance(value, list) and len(value) == 3, "runtime must contain three replicas")
    require({v.get("endpoint") for v in value} == {f"localhost:{p}" for p in (9390, 9391, 9392)},
            "runtime replica identity differs")
    for replica in value:
        for key in ("goroutines", "heap_alloc_bytes", "heap_inuse_bytes", "heap_objects"):
            require(positive_integer(replica.get(key)), "missing/zero/non-integral runtime evidence: " + key)


def validate_steady(value):
    require(value.get("interval_ms") == 5000 and 45000 <= value.get("duration_ms", 0) <= 55000,
            "receipt steady sampling duration/interval differs")
    endpoints = [f"localhost:{p}" for p in (9390, 9391, 9392)]
    require(value.get("endpoints") == endpoints and len(value.get("samples", [])) >= 9
            and not value.get("failure"), "receipt steady sample coverage missing")
    previous = 0
    for index, row in enumerate(value["samples"]):
        elapsed = row.get("elapsed_ms")
        require(type(elapsed) is int and elapsed >= 0 and (index == 0 or elapsed > previous)
                and elapsed - previous <= 7500 and elapsed <= value["duration_ms"] + 7500,
                "receipt steady sampling gap")
        replicas = row.get("replicas", [])
        require([r.get("endpoint") for r in replicas] == endpoints and
                all(positive_integer(r.get("goroutines")) and positive_integer(r.get("heap_alloc_bytes")) for r in replicas),
                "receipt steady replica missing/invalid")
        previous = elapsed
    require(value["duration_ms"] - previous <= 7500, "receipt steady tail missing")


def validate_provenance(value, sha, image_ref, image_id, owner, receipt):
    require(value.get("source_commit") == sha and value.get("image_id") == image_id and
            value.get("image_ref") == image_ref and value.get("runtime_user") == owner,
            "image/source/runtime provenance differs")
    replicas = value.get("replicas", [])
    require(len(replicas) == 3 and {v.get("service") for v in replicas} == set(SERVICES),
            "missing/duplicate image replica")
    for replica in replicas:
        require(HEX64.fullmatch(replica.get("container_id", "")) and
                replica.get("image_id") == image_id and replica.get("image_ref") == image_ref and
                replica.get("runtime_user") == owner and replica.get("cpuset") == "0-3",
                "replica image provenance differs")
        spool = replica.get("graph_snapshot_spool")
        if receipt:
            require(spool is None, "receipt unexpectedly has graph staging")
        else:
            require(isinstance(spool, dict) and spool.get("runtime_user") == owner and
                    spool.get("mode") == "0700" and spool.get("read_write") is True and
                    spool.get("configured_nocopy") is True and
                    spool.get("target") == "/var/lib/lantern-snapshot-spool" and spool.get("volume"),
                    "graph staging provenance incomplete")
    if not receipt:
        require(len({r["graph_snapshot_spool"]["volume"] for r in replicas}) == 3, "shared graph staging")


def validate_summary(report, documents):
    require("## Truncated" not in report and report.count("## Summary\n") == 1,
            "missing/duplicate/truncated aggregate summary")
    summary = report.split("## Summary\n", 1)[1].split("\n## ", 1)[0]
    rows = [[cell.strip().strip("`") for cell in line.split("|")[1:-1]]
            for line in summary.splitlines() if line.startswith("| `")]
    require([row[0] for row in rows] == list(CANONICAL), "aggregate scenario order/count differs")
    for row, scenario in zip(rows, CANONICAL):
        require(len(row) == 8 and row[1] == row[4] == "pass", "aggregate gate failed")
        document = documents[scenario]
        require(row[2] == ("pass" if document.get("metric_gate") else "—"), "aggregate metric missing")
        require(row[3] == ("pass" if document.get("semantic_gate") else "—"), "aggregate semantic missing")


def validate_lifecycle(directory, document, perf):
    contract = document["lifecycle_gate"]
    require(perf.get("lifecycle_reason") == contract["reason"] and
            perf.get("thresholds", {}).get("max_expected_lifecycle_ratio") == contract["max_ratio"],
            "authoritative perf gate omitted lifecycle qualification")
    for producer in perf["producer_results"]:
        if producer["name"] in contract["producers"]:
            require(producer.get("lifecycle_reason") == contract["reason"] and
                    producer.get("thresholds", {}).get("max_expected_lifecycle_ratio") == contract["max_ratio"],
                    "named producer omitted lifecycle qualification")
    for phase in ("pre", "post"):
        proof = read_json(directory / f"lifecycle_{phase}.json")
        replicas = proof.get("replicas", [])
        require(proof.get("metric") == "lantern_search_calls_total" and
                [r.get("index") for r in replicas] == [0, 1, 2] and
                [r.get("endpoint") for r in replicas] == [f"http://localhost:{p}/metrics" for p in (9390, 9391, 9392)],
                "lifecycle counter identity incomplete")
        for replica in replicas:
            require(isinstance(replica.get("series"), list) and all(
                isinstance(row.get("labels"), dict) and type(row.get("value")) in (int, float)
                and math.isfinite(row["value"]) and row["value"] >= 0 for row in replica["series"]),
                "lifecycle counter samples invalid")


def validate_run(directory, document, sha, image_ref, image_id, owner, receipt):
    report = regular(directory / "report.md").read_text()
    require("unqualified" not in report.lower(), "report records unqualified execution")
    verdicts = {}
    gates = ["leak_gate", "perf_gate", "workload_gate"]
    if document.get("metric_gate"):
        gates.append("metric_gate")
    for gate in gates:
        value = read_json(directory / (gate + ".json"))
        require(value.get("verdict") == "pass", gate + " did not pass")
        verdicts[gate] = "pass"
        if gate == "workload_gate":
            require(all(type(value.get(key)) is int and value[key] == 0 for key in ("producer_failed", "producer_diagnostics_failed",
                        "failed_consumers", "chaos_failed")), "incomplete workload evidence")
        if gate == "perf_gate":
            names = [call.get("name", "producer-" + str(i)) for i, call in
                     enumerate(document["target"].get("calls", []))] or ["primary"]
            producers = value.get("producer_results", [])
            require([p.get("name") for p in producers] == names and
                    all(p.get("verdict") == "pass" and positive(p.get("observed", {}).get("count"))
                        for p in producers), "missing/failed named producer evidence")
            for index, _ in enumerate(names):
                pattern = f"ghz_steady_{index}_*.json" if document["target"].get("calls") else "ghz_steady_*.json"
                raw = list(directory.glob(pattern))
                require(len(raw) == 1 and positive(read_json(raw[0]).get("count")), "raw producer evidence missing")
        if gate == "leak_gate" and receipt:
            require(value.get("snapshot_protocol") == "natural_gc_single" and
                    value.get("steady_sample_interval") == "5s" and value.get("steady_sample_count", 0) >= 9,
                    "receipt natural-GC sampling incomplete")
    for phase in ("pre", "post"):
        validate_runtime(read_json(directory / f"runtime_{phase}.json"))
    before, after = [read_json(directory / f"image_provenance_{phase}.json") for phase in ("pre", "post")]
    for value in (before, after):
        validate_provenance(value, sha, image_ref, image_id, owner, receipt)
    require(before == after, "image/container/native staging identity changed")
    if document.get("semantic_gate"):
        for phase in ("pre", "post"):
            proof = read_json(directory / f"semantic_{phase}.json")
            require(proof.get("phase") == phase and proof.get("verdict") == "pass" and
                    len(proof.get("replicas", [])) == 3 and
                    len({r.get("endpoint") for r in proof["replicas"]}) == 3 and
                    all(r.get("verdict") == "pass" and positive(r.get("checks")) for r in proof["replicas"]),
                    "semantic phase incomplete")
        verdicts["semantic_gate"] = "pass"
    if document.get("preflight"):
        proof = read_json(directory / "topology_preflight.json")
        expected = {"walk_vertices": 193, "walk_edges": 8256, "walk_root_degree": 64,
                    "walk_layer1_vertices": 64, "walk_layer2_vertices": 64, "walk_layer3_vertices": 64,
                    "community_members": 32, "community_edges": 31}
        require(all(proof.get(key) == value for key, value in expected.items()) and
                positive_integer(proof.get("ppr_vertices")) and proof["ppr_vertices"] >= 17,
                "topology preflight incomplete/collapsed")
        verdicts["topology_preflight"] = "pass"
    if document.get("lifecycle_gate"):
        validate_lifecycle(directory, document, read_json(directory / "perf_gate.json"))
        verdicts["lifecycle_gate"] = "pass"
    if receipt:
        tls = [read_json(directory / f"peer_tls_{phase}.json") for phase in ("pre", "post")]
        require(tls[0] == tls[1] and HEX64.fullmatch(tls[0].get("ca_sha256", "")) and
                len(tls[0].get("peers", [])) == 3 and
                {p.get("service") for p in tls[0]["peers"]} == set(SERVICES) and
                all(HEX64.fullmatch(p.get("certificate_sha256", "")) for p in tls[0]["peers"]),
                "receipt TLS identity incomplete")
        require(positive(read_json(directory / "membership_renewal.json").get("renewals")),
                "membership renewal evidence missing")
        validate_steady(read_json(directory / "runtime_steady.json"))
        for phase in ("warmup", "steady"):
            summaries = [read_json(directory / f"ghz_{phase}_{i}_{name}.json")
                         for i, name in enumerate(("receipt_admission", "receipt_lookup"))]
            require(positive(summaries[0].get("count")) and summaries[0].get("count") == summaries[1].get("count")
                    and all(s.get("statusCodeDistribution") == {"OK": s["count"]} for s in summaries),
                    "receipt admission/lookup coverage differs or non-OK")
    return verdicts


def capture_private_outputs(logs, destination, temp_roots):
    """Copy only exact harness-referenced, current-user private regular files."""
    retained = []
    for log in logs:
        for line in regular(log).read_text(errors="replace").splitlines():
            # ghz's historical message omits the extra word 'evidence'.
            match = PRIVATE_MESSAGE.fullmatch(line) or re.fullmatch(
                r"run\.sh: private ghz producer diagnostics retained at (.+)", line) or re.fullmatch(
                r"(?:owned_compose\.sh: )?initializer private evidence retained at (.+)", line)
            if not match:
                continue
            path = Path(match[1])
            require(path.is_absolute() and not path.is_symlink() and path.parent.resolve() in temp_roots
                    and re.fullmatch(r"lantern-(?:native-failure|ghz-producer|owned-init)\.[A-Za-z0-9]+", path.name),
                    "unsafe referenced private output directory")
            info = path.lstat()
            require(stat.S_ISDIR(info.st_mode) and info.st_uid == os.getuid() and
                    stat.S_IMODE(info.st_mode) == 0o700, "private evidence ownership differs")
            target = destination / path.name
            if target.exists():
                continue
            target.mkdir(mode=0o700)
            for item in path.iterdir():
                metadata = item.lstat()
                require(stat.S_ISREG(metadata.st_mode) and metadata.st_uid == os.getuid(),
                        "private evidence must contain owned regular files only")
                fd = os.open(item, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
                metadata = os.fstat(fd)
                if not (stat.S_ISREG(metadata.st_mode) and metadata.st_uid == os.getuid()):
                    os.close(fd)
                    raise Refused("private file changed during capture")
                with os.fdopen(fd, "rb") as src, (target / item.name).open("xb") as dst:
                    shutil.copyfileobj(src, dst)
                os.chmod(target / item.name, 0o600)
            retained.append(str(path))
    return retained


class Qualification:
    def __init__(self, args):
        self.args = args
        self.repo, self.evidence = args.repo.resolve(), args.evidence_dir.absolute()
        self.source = self.evidence / "source"
        self.owner = f"{os.getuid()}:{os.getgid()}"
        self.state = {"verdict": "not_run", "steps": [], "scenario_verdicts": {}, "started_utc": utc(),
                      "source_sha": args.source_sha, "source_tree": args.source_tree,
                      "campaign": args.campaign, "work_budget_seconds": args.budget_seconds,
                      "cleanup_budget_seconds": args.cleanup_seconds,
                      "scope": {"canonical": CANONICAL, "receipts": RECEIPTS, "attempts": 1,
                                "publication": "not performed by this driver", "existing_main_ci_sdk4": "not_repeated",
                                "gc": "unchanged OFF pre/post; no extra/manual/receipt/steady GC",
                                "limits": "platform-scoped 8+4 only; separate peak RSS/CPU/provider/device/SLO gates"}}
        self.tools = {}
        self.image_ref, self.image_id = "", ""
        self.inventory = {}
        self.auxiliary_images = {}
        self.prometheus_ref = None
        self.project_preflight_passed = False

    def save(self):
        atomic_json(self.evidence / "progress.json", self.state)

    def save_cleanup(self):
        return best_effort_save(self.save, self.state)

    def call(self, name, args, cwd=None, **kwargs):
        return self.supervisor.command(name, args, cwd or self.source, **kwargs)[1]

    def tool(self, name, *arguments, **kwargs):
        return self.call(name, [self.tools[name], *arguments], **kwargs)

    def resources(self, custody=False):
        result = {}
        for kind, args in (("containers", ["ps", "-aq"]), ("volumes", ["volume", "ls", "-q"]),
                           ("networks", ["network", "ls", "-q"])):
            raw = self.tool("docker", *args, "--filter", "label=com.docker.compose.project=" + self.args.campaign,
                            timeout=15, cleanup=True, custody=custody)
            result[kind] = raw.splitlines() if raw else []
        return result

    def absent(self):
        snapshot = self.resources()
        require(not any(snapshot.values()), "named project is present; no reset/cleanup attempted")
        self.state["cleanup"] = {"disposition": "absent", "resources": snapshot}
        self.save()

    def owned_row(self, row):
        if len(row) != 14 or row[4] != self.args.campaign:
            return False
        if row[5] == "volume-init" and row[9] == "compose-volume-initializer":
            try:
                command = json.loads(row[13])
            except (TypeError, ValueError):
                return False
            if not isinstance(command, dict):
                return False
            return (row[1] == self.image_id and row[2] == "0:0" and row[7] == self.image_id
                    and row[8] == f"/{self.args.campaign}-volume-init" and row[10] == "True"
                    and re.fullmatch(r"lantern-owned-init\.[A-Za-z0-9]+", row[11]) is not None
                    and row[12] == '["/bin/sh"]' and command.get("argc") == 5
                    and command.get("kind") == "-c" and command.get("fixture") == "fixture"
                    and command.get("owner") == self.owner and command.get("count") in ("6", "9"))
        if row[5] in SERVICES and row[1] == self.image_id and row[2] == self.owner:
            if row[9] == "compose-security-initializer":
                return (row[7] == self.image_id and row[8] == f"/{self.args.campaign}-{row[5]}-security-init"
                        and row[10] == "True" and re.fullmatch(r"lantern-owned-init\.[A-Za-z0-9]+", row[11]) is not None
                        and row[12] == '["/app/lantern"]' and row[13] == '["init-security"]')
            return row[7] == self.image_ref and row[10] != "True"
        if row[5] == "prometheus" and self.prometheus_ref and row[7] == self.prometheus_ref:
            if self.prometheus_ref not in self.auxiliary_images:
                proof = self.tool("docker", "image", "inspect", "--format", "{{.Id}}|{{.Config.User}}",
                                  self.prometheus_ref, timeout=15, cleanup=True, custody=True).split("|")
                require(len(proof) == 2 and IMAGE_ID.fullmatch(proof[0]), "Prometheus image pin missing")
                self.auxiliary_images[self.prometheus_ref] = {"id": proof[0], "runtime_user": proof[1]}
                self.state["auxiliary_images"] = self.auxiliary_images
                self.save_cleanup()
            pin = self.auxiliary_images[self.prometheus_ref]
            return row[1] == pin["id"] and row[2] == pin["runtime_user"] and row[10] != "True"
        return False

    def identities(self, ids, custody=False):
        if not ids:
            return []
        require(all(re.fullmatch(r"[0-9a-f]{12,64}", value) for value in ids), "invalid container identity")
        template = ('{{.Id}}|{{.Image}}|{{.Config.User}}|{{.State.Running}}|'
                    '{{index .Config.Labels "com.docker.compose.project"}}|'
                    '{{index .Config.Labels "com.docker.compose.service"}}|{{.HostConfig.CpusetCpus}}|'
                    '{{.Config.Image}}|{{.Name}}|{{index .Config.Labels "lantern.owner"}}|'
                    '{{index .Config.Labels "com.docker.compose.oneoff"}}|'
                    '{{index .Config.Labels "lantern.initializer"}}|{{json .Config.Entrypoint}}|'
                    '{{if eq (index .Config.Labels "lantern.owner") "compose-volume-initializer"}}'
                    '{"argc":{{len .Config.Cmd}},"kind":{{json (index .Config.Cmd 0)}},'
                    '"fixture":{{json (index .Config.Cmd 2)}},"owner":{{json (index .Config.Cmd 3)}},'
                    '"count":{{json (index .Config.Cmd 4)}}}{{else}}{{json .Config.Cmd}}{{end}}')
        rows = self.tool("docker", "inspect", "--format", template, *ids,
                         timeout=15, cleanup=True, custody=custody).splitlines()
        values = [row.split("|") for row in rows]
        require(len(values) == len(ids) and all(len(row) == 14 for row in values), "incomplete container inspection")
        return values

    def cleanup(self):
        # A preflight/tool failure before any project launch owns no Docker
        # resource. Do not query or mutate an unrelated pre-existing project.
        while True:
            try:
                self.supervisor.stop_all()
                if not self.project_preflight_passed:
                    self.state["cleanup"] = {"disposition": "not_started"}
                    self.save_cleanup()
                    return
                resources = self.resources(custody=True)
                identities = self.identities(resources["containers"], custody=True)
                running = [row for row in identities if row[3] != "false"]
                if not running:
                    self.state["cleanup"] = {"disposition": "retained_inactive" if any(resources.values()) else "absent",
                                             "resources": resources}
                    self.save_cleanup()
                    return
                owned = [row[0] for row in running if self.owned_row(row)]
                if len(owned) == len(running) and self.supervisor.clock() < self.supervisor.cleanup_deadline:
                    self.tool("docker", "stop", "--time", "45", *owned, timeout=60, cleanup=True)
                    continue
                raise Refused("named running resources cannot be safely stopped within cleanup budget")
            except Exception as error:
                self.state["cleanup"] = {"disposition": "cleanup_blocked", "reason": str(error)}
                self.save_cleanup()
                # Manual lock custody, not another workload attempt. The only
                # commands after the deadline are bounded read-only inspections.
                if not getattr(self, "custody_announced", False):
                    try:
                        print("Cleanup unproven; retaining host lock for operator review.", flush=True)
                    except Exception:
                        self.state["evidence_io_failed"] = True
                        self.state["verdict"] = "fail"
                    self.custody_announced = True
                self.supervisor.sleep(5)

    def preflight(self):
        for name in ("git", "docker", "ghz", "yq", "jq", "curl", "go", "bash", "python3"):
            selected = self.args.bash if name == "bash" and self.args.bash else shutil.which(name)
            require(selected is not None, "missing tool: " + name)
            self.tools[name] = str(regular(Path(selected).resolve()))
        # A directory per selected binary cannot pin name resolution when an
        # earlier directory also contains a different Python/Go/bash executable.
        shims = self.evidence / "tools"
        shims.mkdir(mode=0o700)
        for name, path in self.tools.items():
            (shims / name).symlink_to(path)
        self.supervisor.env["PATH"] = str(shims) + os.pathsep + self.supervisor.env.get("PATH", "")
        self.state["tools"] = {name: {"path": path, "sha256": digest(Path(path))}
                               for name, path in self.tools.items()}
        for expression, expected in (("HEAD", self.args.source_sha), (self.args.source_sha + "^{tree}", self.args.source_tree)):
            require(self.tool("git", "-c", "core.fsmonitor=false", "rev-parse", expression, cwd=self.repo,
                              timeout=30) == expected, "source SHA/tree differs")
        require(not self.tool("git", "-c", "core.fsmonitor=false", "status", "--porcelain", "--untracked-files=all",
                              cwd=self.repo, timeout=30), "clean checkout required")
        archive = self.evidence / "source.tar"
        self.tool("git", "archive", "--format=tar", "--output=" + str(archive), self.args.source_sha,
                  cwd=self.repo, timeout=60)
        self.state["source_archive_sha256"] = digest(archive)
        self.source.mkdir(mode=0o700)
        with tarfile.open(archive) as stream:
            stream.extractall(self.source, filter="data")
        self.inventory = source_inventory(self.source)
        require(not (self.source / "testbed/bench/out").exists(), "archive includes prior benchmark output")
        frozen_driver = self.source / ".github/scripts/local_release_qualification.py"
        require(digest(regular(frozen_driver)) == digest(Path(__file__)), "driver differs from reviewed source")
        shutil.copyfile(__file__, self.evidence / "driver.py")
        atomic_json(self.evidence / "source-inventory.json", self.inventory)
        scenarios = (self.source / "testbed/bench/release-scenarios.txt").read_text().splitlines()
        require(tuple(line.split("#", 1)[0].strip() for line in scenarios if line.split("#", 1)[0].strip()) == CANONICAL,
                "canonical scenario declaration differs; review driver contract")
        versions = {}
        for name, args in {"bash": ["--version"], "ghz": ["--version"], "yq": ["--version"],
                           "jq": ["--version"], "curl": ["--version"], "go": ["version"],
                           "git": ["--version"], "python3": ["--version"],
                           "docker": ["version", "--format", "client={{.Client.Version}} server={{.Server.Version}}"]}.items():
            versions[name] = self.tool(name, *args, timeout=30)
        require(re.search(r"version ([4-9]|[1-9][0-9]+)\.", versions["bash"]), "bash >=4 required")
        require(re.search(r"version v?4\.", versions["yq"]), "Go yq v4 required")
        ghz_version = re.search(r"(\d+)\.(\d+)\.(\d+)", versions["ghz"])
        require(ghz_version and tuple(map(int, ghz_version.groups())) >= (0, 117, 0), "ghz >=0.117 required")
        go_pin = re.search(r"^go (\S+)$", (self.source / "go.mod").read_text(), re.M).group(1)
        require("go" + go_pin + " " in versions["go"], "Go toolchain differs from source pin")
        versions["compose"] = self.tool("docker", "compose", "version", "--short", timeout=30)
        require_compose_version(versions["compose"])
        self.state["tool_versions"] = versions
        self.tool("docker", "info", "--format", "{{json .ClientInfo.Plugins}}", timeout=30)
        plugins = read_json(self.evidence / self.state["steps"][-1]["log"])
        compose = [plugin for plugin in plugins if plugin.get("Name", plugin.get("name")) == "compose"]
        require(len(compose) == 1, "Docker Compose plugin identity missing")
        plugin_path = regular(Path(compose[0].get("Path", compose[0].get("path", ""))).resolve())
        self.state["tools"]["compose-plugin"] = {"path": str(plugin_path), "sha256": digest(plugin_path)}
        self.state["host"] = {"platform": sys.platform, "uname": list(os.uname()),
                              "generator_placement": "native host outside Docker cpuset; shares physical host",
                              "contention_policy": "ordinary-use host; no quiet-host prerequisite or unrelated workload stopping",
                              "shared_replica_cpuset": self.args.cpuset,
                              "runtime_provenance": "existing harness pre/post inspect only; no periodic driver observer",
                              "docker": self.tool("docker", "info", "--format",
                                  "engine={{.ServerVersion}} os={{.OSType}} arch={{.Architecture}} cpus={{.NCPU}} memory={{.MemTotal}}", timeout=30)}
        self.absent()
        self.project_preflight_passed = True
        self.documents = {}
        self.prometheus_ref = self.tool("yq", "-r", ".services.prometheus.image",
            self.source / "testbed/bench/compose.native.yml", timeout=30)
        require(re.fullmatch(r"[A-Za-z0-9._/:@-]+", self.prometheus_ref), "unexpected auxiliary image reference")
        for name in CANONICAL + RECEIPTS:
            path = self.source / "testbed/bench/scenarios" / (name + ".yaml")
            self.tool("yq", "-o=json", ".", path, timeout=30)
            self.documents[name] = read_json(self.evidence / self.state["steps"][-1]["log"])
        self.save()

    def image(self):
        self.image_ref = "lantern:local-qualification-" + self.args.campaign
        self.tool("docker", "build", "--build-arg", "VERSION=local-qualification", "--build-arg",
                  "COMMIT=" + self.args.source_sha, "-t", self.image_ref, ".")
        self.image_id = self.tool("docker", "image", "inspect", "--format", "{{.Id}}", self.image_ref, timeout=30)
        require(IMAGE_ID.fullmatch(self.image_id), "invalid immutable image ID")
        commit = self.tool("docker", "image", "inspect", "--format",
                           '{{range .Config.Env}}{{println .}}{{end}}', self.image_id, timeout=30)
        require(commit.splitlines().count("LANTERN_COMMIT=" + self.args.source_sha) == 1, "image commit differs")
        self.state["image"] = {"reference": self.image_ref, "id": self.image_id,
                               "platform": self.tool("docker", "image", "inspect", "--format",
                                                    "{{.Os}}/{{.Architecture}}", self.image_id, timeout=30)}
        self.supervisor.env.update(LANTERN_IMAGE=self.image_ref, EXPECTED_LANTERN_IMAGE_ID=self.image_id,
                                   EXPECTED_LANTERN_COMMIT=self.args.source_sha, COMMIT=self.args.source_sha)
        self.save()

    def check_runs(self, names):
        for name in names:
            parent = self.source / "testbed/bench/out" / name
            runs = list(parent.iterdir())
            require(len(runs) == 1 and runs[0].is_dir() and not runs[0].is_symlink(), "run count differs: " + name)
            resolved = self.tool("yq", "-o=json", ".", runs[0] / "scenario.resolved.yaml", timeout=30)
            require(json.loads(resolved) == self.documents[name], "resolved workload/configuration changed")
            self.state["scenario_verdicts"][name] = validate_run(
                runs[0], self.documents[name], self.args.source_sha, self.image_ref, self.image_id,
                self.owner, name in RECEIPTS)
            self.save()
        self.absent()
        require(self.tool("docker", "image", "inspect", "--format", "{{.Id}}", self.image_ref, timeout=30) == self.image_id,
                "image reference changed")

    def execute(self, lock_fd):
        env, removed = clean_environment(os.environ, self.source, self.args.campaign, self.args.cpuset)
        self.state["removed_environment_names"] = removed
        self.supervisor = Supervisor(self.evidence, env, lock_fd, self.args.budget_seconds,
                                     self.args.cleanup_seconds, self.state, self.save)
        self.state["deadline_monotonic"] = self.supervisor.deadline
        self.state["cleanup_deadline_monotonic"] = self.supervisor.cleanup_deadline
        self.state["deadline_utc"] = (datetime.datetime.now(datetime.timezone.utc) +
                                      datetime.timedelta(seconds=self.args.budget_seconds)).isoformat()
        self.state["driver_sha256"] = digest(Path(__file__))
        previous = {number: signal.signal(number, self.supervisor.interrupted) for number in (signal.SIGINT, signal.SIGTERM)}
        self.state["verdict"] = "running"
        self.save()
        try:
            self.preflight()
            atomic_json(self.evidence / "configuration.json", {
                "source_sha": self.args.source_sha, "source_tree": self.args.source_tree,
                "driver_sha256": self.state["driver_sha256"], "tools": self.state["tools"],
                "work_budget_seconds": self.args.budget_seconds, "cleanup_budget_seconds": self.args.cleanup_seconds,
                "scope": self.state["scope"], "environment": {key: value for key, value in env.items()
                    if key.startswith("LANTERN_BENCH_") or key in ("GOWORK", "GOFLAGS", "GOENV", "KEEP_OUT",
                       "LEAK_GATE_ONLY", "RELEASE_BENCH_BUDGET_SECONDS", "KEEP_UP", "SKIP_UP", "PPROF_CPU",
                       "SNAPSHOT_ROUNDS", "COMPOSE_PROJECT_NAME", "TAG")}})
            self.state["configuration_sha256"] = digest(self.evidence / "configuration.json")
            self.image()
            self.absent()
            self.call("canonical-sweep", [self.tools["bash"], "testbed/bench/release.sh", "--out",
                                         self.evidence / "bench-report.md"])
            self.check_runs(CANONICAL)
            validate_summary(regular(self.evidence / "bench-report.md").read_text(), self.documents)
            for scenario in RECEIPTS:
                self.absent()
                self.call(scenario, [self.tools["bash"], "testbed/bench/run.sh", scenario])
                self.check_runs((scenario,))
            verify_inventory(self.source, self.inventory)
            for value in self.state["tools"].values():
                require(digest(Path(value["path"])) == value["sha256"], "tool binary changed")
            require(not self.supervisor.cancelled and self.supervisor.clock() < self.supervisor.deadline,
                    "campaign interrupted or budget exhausted")
            require(not self.state.get("evidence_io_failed"), "evidence storage failed")
            self.state["verdict"] = "pass"
        except BaseException as error:
            self.state["verdict"] = "interrupted" if self.supervisor.cancelled or isinstance(error, Interrupted) else "fail"
            self.state["error"] = str(error)
        finally:
            # No PASS survives cleanup failure, retained resources or incomplete evidence.
            try:
                self.supervisor.cleanup_deadline = min(self.supervisor.cleanup_deadline,
                    self.supervisor.clock() + self.args.cleanup_seconds)
                self.state["actual_cleanup_deadline_monotonic"] = self.supervisor.cleanup_deadline
                self.cleanup()
                if self.state["cleanup"]["disposition"] != "absent":
                    self.state["verdict"] = "fail"
                private = self.evidence / "private-failures"
                private.mkdir(mode=0o700)
                roots = {Path("/tmp").resolve(), Path("/private/tmp").resolve(),
                         Path(os.environ.get("TMPDIR", "/tmp")).resolve()}
                self.state["private_failure_originals"] = capture_private_outputs(
                    self.evidence.glob("*.log"), private, roots)
            except BaseException as error:
                self.state["verdict"] = "fail"
                self.state["finalization_error"] = str(error)
            if self.supervisor.cancelled:
                self.state["verdict"] = "interrupted"
            self.state["active"] = None
            self.state["ended_utc"] = utc()
            self.save()
            checksums = self.evidence / "SHA256SUMS"
            temporary = checksums.with_name("SHA256SUMS.tmp")
            try:
                while True:
                    sums = []
                    for path in sorted(self.evidence.rglob("*")):
                        if path.is_symlink():
                            continue  # symlink identities are in source-inventory.json/tool pins
                        if path.is_file() and path not in (checksums, temporary):
                            sums.append(digest(path) + "  " + str(path.relative_to(self.evidence)))
                    if self.supervisor.cancelled and self.state["verdict"] != "interrupted":
                        self.state["verdict"] = "interrupted"
                        self.save()
                        continue
                    with temporary.open("w") as stream:
                        stream.write("\n".join(sums) + "\n")
                        stream.flush()
                        os.fsync(stream.fileno())
                    if self.supervisor.cancelled and self.state["verdict"] != "interrupted":
                        self.state["verdict"] = "interrupted"
                        self.save()
                        continue
                    temporary.replace(checksums)
                    break
            except BaseException as error:
                self.state["verdict"] = "fail"
                self.state["finalization_error"] = "artifact hashing incomplete: " + str(error)
                self.save()
                raise
            for number, handler in previous.items():
                signal.signal(number, handler)
        print("verdict=" + self.state["verdict"] + " SHA256SUMS sha256=" + digest(checksums), flush=True)
        return 0 if self.state["verdict"] == "pass" else 1


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--repo", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--source-tree", required=True)
    parser.add_argument("--campaign", required=True)
    parser.add_argument("--evidence-dir", type=Path, required=True)
    parser.add_argument("--bash", help="absolute bash >=4 executable; put it on PATH otherwise")
    parser.add_argument("--cpuset", default="0-3", choices=("0-3",))
    parser.add_argument("--budget-seconds", type=int, default=10800)
    parser.add_argument("--cleanup-seconds", type=int, default=600)
    args = parser.parse_args(argv)
    require(os.name == "posix" and os.getuid() != 0, "non-root POSIX host required")
    require(HEX40.fullmatch(args.source_sha) and HEX40.fullmatch(args.source_tree), "full SHA/tree required")
    require(re.fullmatch(r"[a-z][a-z0-9-]{2,47}", args.campaign), "safe unique campaign name required")
    require(3600 <= args.budget_seconds <= 86400 and 120 <= args.cleanup_seconds <= 3600,
            "inadequate or excessive finite campaign/cleanup budget")
    require(not args.evidence_dir.exists() and not args.evidence_dir.is_symlink(), "new private evidence directory required")
    # The selected output must not make the checkout dirty during preflight.
    require(not args.evidence_dir.absolute().is_relative_to(args.repo.resolve()), "evidence must be outside checkout")
    args.evidence_dir.mkdir(mode=0o700, parents=False)
    old_umask = os.umask(0o077)
    try:
        with HostLock() as lock:
            return Qualification(args).execute(lock.fd)
    finally:
        os.umask(old_umask)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, Refused) as error:
        print("local qualification refused: " + str(error), file=sys.stderr)
        sys.exit(1)
