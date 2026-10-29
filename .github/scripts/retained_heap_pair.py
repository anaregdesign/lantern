"""Read-only validation of the explicitly paired #1958 receipt measurements.

Normal-image checks and tagged retained-memory checks have separate scopes.
This consumer checks their literal evidence; it never runs GC or a workload.
"""

import hashlib
import json
import os
from pathlib import Path
import re
import stat


PROTOCOL = "symmetric_post_gc_2_v1"
NORMAL_PROTOCOL = "natural_reference_v1"
ENDPOINTS = tuple(f"localhost:{port}" for port in (9390, 9391, 9392))
HEX40 = re.compile(r"[0-9a-f]{40}\Z")
HEX64 = re.compile(r"[0-9a-f]{64}\Z")
IMAGE = re.compile(r"sha256:[0-9a-f]{64}\Z")
MIB = 1024 * 1024


class Incomplete(ValueError):
    pass


def need(condition, message):
    if not condition:
        raise Incomplete(message)


def integer(value, minimum=0):
    return type(value) is int and value >= minimum


def canonical_sha(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":"),
                                     ensure_ascii=False, allow_nan=False).encode()).hexdigest()


def _object(pairs):
    value = {}
    for key, item in pairs:
        need(key not in value, "duplicate JSON key")
        value[key] = item
    return value


def _identity(info):
    return (info.st_dev, info.st_ino, info.st_mode, info.st_uid, info.st_gid,
            info.st_nlink, info.st_size, info.st_mtime_ns, info.st_ctime_ns)


def directory(path):
    """Pin a physical directory without following any ancestor symlink."""
    path = Path(path)
    need(path.is_absolute() and ".." not in path.parts, "physical absolute directory required")
    fd = os.open("/", os.O_RDONLY | os.O_DIRECTORY)
    try:
        for part in path.parts[1:]:
            child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
            os.close(fd)
            fd = child
        return fd
    except OSError as error:
        os.close(fd)
        raise Incomplete("missing or unsafe evidence directory") from error
    except BaseException:
        os.close(fd)
        raise


def read(root, path, limit=1024 * 1024):
    """Require a stable regular evidence file below the declared case root."""
    root, path = Path(root).absolute(), Path(path)
    if not path.is_absolute():
        path = root / path
    need(".." not in path.parts and path.is_relative_to(root), "evidence path escapes case")
    parent = directory(path.parent)
    try:
        before = os.stat(path.name, dir_fd=parent, follow_symlinks=False)
        need(stat.S_ISREG(before.st_mode) and before.st_nlink == 1 and before.st_size <= limit,
             "unsafe or oversized evidence file")
        fd = os.open(path.name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=parent)
        with os.fdopen(fd, "rb") as stream:
            opened = os.fstat(stream.fileno())
            need(_identity(opened) == _identity(before), "evidence changed before reading")
            raw = stream.read(limit + 1)
            need(len(raw) == before.st_size and _identity(os.fstat(stream.fileno())) == _identity(opened)
                 and _identity(os.stat(path.name, dir_fd=parent, follow_symlinks=False)) == _identity(before),
                 "evidence changed while reading")
        again = directory(path.parent)
        try:
            need(os.path.samestat(os.fstat(parent), os.fstat(again)), "evidence directory replaced")
        finally:
            os.close(again)
    except OSError as error:
        raise Incomplete("missing or unsafe evidence path") from error
    finally:
        os.close(parent)
    try:
        value = json.loads(raw, object_pairs_hook=_object,
                           parse_constant=lambda value: (_ for _ in ()).throw(Incomplete("nonfinite JSON")))
    except (UnicodeError, json.JSONDecodeError) as error:
        raise Incomplete("invalid evidence JSON") from error
    return value, hashlib.sha256(raw).hexdigest()


def reference(root, value, limit=32768):
    need(isinstance(value, dict) and set(value) == {"path", "sha256"}
         and isinstance(value["path"], str) and HEX64.fullmatch(value["sha256"]),
         "invalid raw evidence reference")
    actual, digest = read(root, value["path"], limit)
    need(digest == value["sha256"], "raw evidence hash differs")
    return actual


def memory(value):
    need(isinstance(value, dict), "missing target memory")
    fields = ("heap_alloc", "heap_objects", "heap_inuse", "num_gc", "num_forced_gc",
              "last_gc", "next_gc", "marked_live", "metric_cycles", "metric_forced", "cycle_after")
    need(all(integer(value.get(key)) for key in fields), "invalid target memory counters")
    need(value["heap_alloc"] > 0 and value["marked_live"] > 0
         and value["cycle_after"] == value["num_gc"] == value["metric_cycles"]
         and value["metric_forced"] == value["num_forced_gc"], "incoherent target GC cycle")


def native(value):
    numbers = ("generation", "pending_count", "pending_bytes", "log_sequence", "log_length", "log_capacity",
               "log_evicted", "receipt_entries", "receipt_bytes", "receipt_high_water_ms",
               "receipt_oldest_deadline_ms", "vertices", "edges")
    need(isinstance(value, dict) and all(integer(value.get(key)) for key in numbers)
         and value.get("faulted") is False and value.get("pending_count") == 0
         and type(value.get("pending_count")) is int and value.get("pending_bytes") == 0
         and type(value.get("pending_bytes")) is int and value.get("pending_local") is False
         and value.get("snapshot_active") is False, "native work is not drained")
    need(value["log_capacity"] > 0 and value["log_length"] <= value["log_capacity"], "invalid native log bounds")
    for key in ("public", "peer_unary", "apply", "peer_subscribe"):
        counter = value.get(key)
        need(isinstance(counter, dict) and set(counter) == {"begun", "completed", "active"}
             and all(integer(counter.get(field)) for field in counter), "invalid activity counter")
        need(counter["begun"] == counter["completed"] + counter["active"], "incoherent activity counter")
        if key != "peer_subscribe":
            need(counter["active"] == 0, "relevant request remains active")
    origins = value.get("origins")
    need(isinstance(origins, list) and 0 < len(origins) <= 16, "missing committed origins")
    ids = []
    for origin in origins:
        need(isinstance(origin, dict) and isinstance(origin.get("id"), str)
             and 0 < len(origin["id"]) <= 128
             and all(integer(origin.get(key)) for key in ("sequence", "wall_ns", "logical")),
             "invalid committed origin")
        ids.append(origin["id"])
    need(ids == sorted(set(ids)), "duplicate or unordered committed origins")


def completion(root, replica, phase, context):
    value = replica.get("completion")
    need(isinstance(value, dict) and type(value.get("schema")) is int and value.get("schema") == 1 and value.get("protocol") == PROTOCOL
         and value.get("status") == "complete" and value.get("reason") == ""
         and integer(value.get("elapsed_ns"), 1) and value["elapsed_ns"] <= 25_000_000_000,
         "target cut is incomplete")
    request = value.get("request", {})
    need(type(request.get("schema")) is int and request.get("schema") == 1 and request.get("protocol") == PROTOCOL
         and request.get("phase") == phase and type(request.get("ordinal")) is int
         and request.get("ordinal") == (1 if phase == "baseline" else 2)
         and integer(request.get("expected_receipts"), 1)
         and request["expected_receipts"] <= 8000
         and request["expected_receipts"] == context["workload"]["admission"]
         and isinstance(request.get("cut_nonce"), str) and HEX64.fullmatch(request["cut_nonce"]),
         "phase identity differs")
    identity = request.get("identity", {})
    session = identity.get("session", {})
    need(integer(identity.get("pid"), 1) and isinstance(identity.get("start_nonce"), str)
         and HEX64.fullmatch(identity["start_nonce"]), "missing target process identity")
    need(type(session.get("schema")) is int and session.get("schema") == 1 and session.get("protocol") == PROTOCOL
         and session.get("source_commit") == context["source_commit"]
         and session.get("source_tree") == context["source_tree"]
         and session.get("image_id") == context["image_id"]
         and session.get("uid") == int(context["runtime_user"].split(":")[0])
         and all(isinstance(session.get(key), str) and HEX64.fullmatch(session[key])
                 for key in ("run_nonce", "container_id", "binary_sha256")), "target session differs")
    need(reference(root, replica.get("session_file"), 4096) == session
         and reference(root, replica.get("ready_file")) == {"schema": 1, "protocol": PROTOCOL, "identity": identity}
         and reference(root, replica.get("request_file"), 4096) == request
         and reference(root, replica.get("started_file")) == {"request": request}
         and reference(root, replica.get("completion_file")) == value, "target raw records differ")
    native(value.get("before")); native(value.get("after"))
    need(value["before"] == value["after"], "native state changed within cut")
    collections = value.get("collections")
    need(isinstance(collections, list) and len(collections) == 2, "two completed target GC calls required")
    prior = 0
    for gc in collections:
        need(isinstance(gc, dict) and integer(gc.get("started_ns")) and integer(gc.get("returned_ns"), 1)
             and prior <= gc["started_ns"] < gc["returned_ns"] <= value["elapsed_ns"], "invalid GC return bracket")
        memory(gc.get("before")); memory(gc.get("after"))
        need(gc["after"]["num_gc"] > gc["before"]["num_gc"]
             and gc["after"]["num_forced_gc"] == gc["before"]["num_forced_gc"] + 1,
             "target GC completion counters differ")
        prior = gc["returned_ns"]
    need(collections[1]["before"]["num_gc"] >= collections[0]["after"]["num_gc"]
         and collections[1]["before"]["num_forced_gc"] == collections[0]["after"]["num_forced_gc"],
         "GC counters rolled back between collections")
    memory(value.get("memory"))
    need(value["memory"] == collections[1]["after"]
         and value["memory"]["num_forced_gc"] == collections[0]["before"]["num_forced_gc"] + 2,
         "capture is not the symmetric two-GC cut")
    return value


def cut(root, filename, phase, expected, scenario):
    value, digest = read(root, filename)
    need(isinstance(value, dict) and set(value) == {"schema", "protocol", "phase", "status",
         "controller_monotonic_start_ns", "controller_monotonic_end_ns", "context", "replicas"}
         and type(value["schema"]) is int and value["schema"] == 1
         and value["protocol"] == PROTOCOL and value["phase"] == phase and value["status"] == "complete",
         "aggregate cut is incomplete")
    start, end = value["controller_monotonic_start_ns"], value["controller_monotonic_end_ns"]
    need(integer(start, 1) and integer(end, 1) and 0 < end - start <= 30_000_000_000,
         "controller cut deadline exceeded")
    context = value["context"]
    need(isinstance(context, dict) and set(context) == {"run_id", "source_commit", "source_tree", "image_id",
         "scenario_sha256", "runtime_user", "cpuset", "workload"} and isinstance(context["run_id"], str)
         and 0 < len(context["run_id"]) <= 128 and context["scenario_sha256"] == canonical_sha(scenario)
         and all(context.get(key) == expected[key] for key in ("source_commit", "source_tree", "runtime_user", "cpuset"))
         and context["image_id"] == expected["retained_image_id"], "cut context differs")
    work = context["workload"]
    need(isinstance(work, dict) and set(work) == {"admission", "lookup"}
         and integer(work["admission"], 1) and work["admission"] == work["lookup"], "submitted work incomplete")
    replicas = value["replicas"]
    need(isinstance(replicas, list) and [row.get("endpoint") for row in replicas] == list(ENDPOINTS),
         "three unique ordered targets required")
    results = [completion(root, row, phase, context) for row in replicas]
    for index in range(3):
        fd = directory(Path(root).absolute() / "heap-control-evidence" / f"lantern-{index}")
        try:
            try:
                os.stat("refusal.json", dir_fd=fd, follow_symlinks=False)
            except FileNotFoundError:
                pass
            else:
                raise Incomplete("target has a refusal record")
        finally:
            os.close(fd)
    need(all(row["before"]["origins"] == results[0]["before"]["origins"] for row in results),
         "replica committed frontiers differ")
    need(len({row["request"]["identity"]["session"]["container_id"] for row in results}) == 3
         and len({row["request"]["identity"]["start_nonce"] for row in results}) == 3
         and len({row["request"]["identity"]["session"]["binary_sha256"] for row in results}) == 1,
         "duplicate target identity")
    need(all(row["before"].get("receipt_entries", -1) >= work["admission"] for row in results),
         "submitted receipt work is not present on every replica")
    return value, digest


def normal_observations(root, report):
    """Recompute the normal lane's mandatory natural checks from raw samples."""
    need(report.get("thresholds") == {"goroutine_max_delta": 15, "heap_alloc_max_delta_mb": 32,
         "steady_heap_alloc_max_delta_mb": 40}, "paired thresholds changed")
    before, after = [read(root, f"runtime_{phase}.json")[0] for phase in ("pre", "post")]
    steady = read(root, "runtime_steady.json")[0]
    for rows in (before, after):
        need(isinstance(rows, list) and [row.get("endpoint") for row in rows] == list(ENDPOINTS)
             and all(integer(row.get("heap_alloc_bytes"), 1) and integer(row.get("goroutines"), 1)
                     for row in rows), "invalid natural snapshot")
    need(steady.get("interval_ms") == 5000 and integer(steady.get("duration_ms"), 45000)
         and steady.get("endpoints") == list(ENDPOINTS) and not steady.get("failure"),
         "natural steady coverage differs")
    rounds = steady.get("samples")
    need(isinstance(rounds, list) and len(rounds) >= 9, "natural steady samples missing")
    previous = 0
    peaks_heap = [row["heap_alloc_bytes"] for row in before]
    peaks_goroutines = [row["goroutines"] for row in before]
    for index, row in enumerate(rounds):
        at, values = row.get("elapsed_ms"), row.get("replicas")
        need(integer(at) and (index == 0 or at > previous) and at - previous <= 7500
             and at <= steady["duration_ms"] + 7500 and isinstance(values, list)
             and [value.get("endpoint") for value in values] == list(ENDPOINTS), "natural steady sample gap")
        for slot, value in enumerate(values):
            need(integer(value.get("heap_alloc_bytes"), 1) and integer(value.get("goroutines"), 1),
                 "invalid natural steady counters")
            peaks_heap[slot] = max(peaks_heap[slot], value["heap_alloc_bytes"])
            peaks_goroutines[slot] = max(peaks_goroutines[slot], value["goroutines"])
        previous = at
    need(steady["duration_ms"] - previous <= 7500, "natural steady tail missing")
    observed_post_failure = False
    for slot, (pre, post) in enumerate(zip(before, after)):
        need(max(peaks_goroutines[slot], post["goroutines"]) - pre["goroutines"] <= 15,
             "normal goroutine growth exceeds 15")
        need(peaks_heap[slot] - pre["heap_alloc_bytes"] <= 40 * MIB,
             "normal natural steady growth exceeds 40 MiB")
        observed_post_failure |= post["heap_alloc_bytes"] - pre["heap_alloc_bytes"] > 32 * MIB
    need(report.get("natural_post_heap_observation_verdict") == ("fail" if observed_post_failure else "pass"),
         "normal post observation was reclassified")


def _validate_pair(normal_root, retained_root, scenario, expected):
    """Return one case's paired acceptance; any absent proof raises Incomplete."""
    need(HEX40.fullmatch(expected.get("source_commit", ""))
         and HEX40.fullmatch(expected.get("source_tree", ""))
         and all(IMAGE.fullmatch(expected.get(key, "")) for key in ("normal_image_id", "retained_image_id"))
         and expected["normal_image_id"] != expected["retained_image_id"]
         and re.fullmatch(r"[1-9][0-9]*:[0-9]+", expected.get("runtime_user", ""))
         and expected.get("cpuset") == "0-3", "unbound pair expectation")
    normal, _ = read(normal_root, "leak_gate.json")
    retained, _ = read(retained_root, "leak_gate.json")
    need(normal.get("snapshot_protocol") == NORMAL_PROTOCOL and normal.get("verdict") == "pass"
         and normal.get("natural_checks_verdict") == "pass" and normal.get("retained_verdict") == "requires_pair"
         and normal.get("pair_required") is True and normal.get("overall_memory_verdict") == "requires_pair",
         "normal lane cannot independently qualify retained memory")
    normal_observations(normal_root, normal)
    need(retained.get("snapshot_protocol") == PROTOCOL and retained.get("verdict") == "pass"
         and retained.get("retained_verdict") == "pass" and retained.get("pair_required") is True
         and retained.get("overall_memory_verdict") == "requires_pair", "retained lane did not pass")
    for root, image_id in ((normal_root, expected["normal_image_id"]), (retained_root, expected["retained_image_id"])):
        resolved, _ = read(root, "scenario.resolved.json")
        need(resolved == scenario, "paired scenario configuration differs")
        for filename in ("perf_gate.json", "workload_gate.json"):
            need(read(root, filename)[0].get("verdict") == "pass", "paired workload/performance incomplete")
        provenance = [read(root, f"image_provenance_{phase}.json")[0] for phase in ("pre", "post")]
        need(provenance[0] == provenance[1] and provenance[0].get("source_commit") == expected["source_commit"]
             and provenance[0].get("image_id") == image_id
             and provenance[0].get("runtime_user") == expected["runtime_user"], "paired image provenance differs")
        replicas = provenance[0].get("replicas", [])
        need(len(replicas) == 3 and {row.get("service") for row in replicas} == {"lantern-0", "lantern-1", "lantern-2"}
             and all(row.get("image_id") == image_id and row.get("runtime_user") == expected["runtime_user"]
                     and row.get("cpuset") == expected["cpuset"] and HEX64.fullmatch(row.get("container_id", ""))
                     for row in replicas), "paired replica provenance differs")
    before, before_hash = cut(retained_root, "retained_heap_pre.json", "baseline", expected, scenario)
    after, after_hash = cut(retained_root, "retained_heap_post.json", "post", expected, scenario)
    need(retained.get("retained_inputs") == {
        "pre": {"path": str(Path(retained_root) / "retained_heap_pre.json"), "sha256": before_hash},
        "post": {"path": str(Path(retained_root) / "retained_heap_post.json"), "sha256": after_hash}},
        "retained report is not bound to actual cuts")
    need(before["controller_monotonic_end_ns"] < after["controller_monotonic_start_ns"], "cut order differs")
    for key in ("run_id", "source_commit", "source_tree", "image_id", "scenario_sha256", "runtime_user", "cpuset"):
        need(before["context"][key] == after["context"][key], "cut identity changed")
    need(after["context"]["workload"]["admission"] > before["context"]["workload"]["admission"],
         "no completed steady work between cuts")
    counts = []
    for phase in ("warmup", "steady"):
        rows = [read(retained_root, f"ghz_{phase}_{index}_{name}.json")[0]
                for index, name in enumerate(("receipt_admission", "receipt_lookup"))]
        need(all(integer(row.get("count"), 1) and row.get("statusCodeDistribution") == {"OK": row["count"]}
                 for row in rows) and rows[0]["count"] == rows[1]["count"], "raw submitted work differs")
        counts.append(rows[0]["count"])
    need(before["context"]["workload"]["admission"] == counts[0]
         and after["context"]["workload"]["admission"] == sum(counts), "cut work does not match completed producers")
    tagged_provenance = read(retained_root, "image_provenance_pre.json")[0]
    containers = {row["service"]: row["container_id"] for row in tagged_provenance["replicas"]}
    deltas, nonces = [], set()
    for index, (pre, post) in enumerate(zip(before["replicas"], after["replicas"])):
        a, b = pre["completion"], post["completion"]
        need(a["request"]["identity"] == b["request"]["identity"], "target restarted between cuts")
        need(a["request"]["identity"]["session"]["container_id"] == containers[f"lantern-{index}"],
             "cut is not from the declared container")
        for row in (a, b):
            nonce = row["request"]["cut_nonce"]
            need(nonce not in nonces, "replayed cut nonce")
            nonces.add(nonce)
        need(b["collections"][0]["before"]["num_forced_gc"] == a["memory"]["num_forced_gc"],
             "undeclared forced GC between cuts")
        need(b["collections"][0]["before"]["num_gc"] >= a["memory"]["num_gc"],
             "GC counters rolled back between cuts")
        delta = b["memory"]["heap_alloc"] - a["memory"]["heap_alloc"]
        need(delta <= 32 * MIB, "retained HeapAlloc exceeds 32 MiB")
        deltas.append({"endpoint": pre["endpoint"], "heap_alloc_delta_bytes": delta})
    return {"schema": 1, "protocol": "source_paired_retained_v1", "verdict": "pass",
            "scope": "normal-image checks plus same-source tagged retained measurement; no binary equivalence",
            "source_commit": expected["source_commit"], "source_tree": expected["source_tree"],
            "scenario_sha256": canonical_sha(scenario), "normal_image_id": expected["normal_image_id"],
            "retained_image_id": expected["retained_image_id"], "replicas": deltas}


def validate_pair(normal_root, retained_root, scenario, expected):
    try:
        return _validate_pair(normal_root, retained_root, scenario, expected)
    except Incomplete:
        raise
    except (OSError, KeyError, TypeError, AttributeError, ValueError) as error:
        raise Incomplete("missing or malformed paired evidence") from error
