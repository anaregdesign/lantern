#!/usr/bin/env python3
"""Bounded private control for the measurement-tag-only heap observer.

No public endpoint or profiling escape. A signal is only a wakeup: only an
identity-bound target completion proves the two collections. The existing
owned Compose/host owner retains resource and process-group cleanup custody.
"""
import argparse
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import selectors
import stat
import subprocess
import time

SCHEMA = 1
PROTOCOL = "symmetric_post_gc_2_v1"
TARGET_DIRECTORY = "/run/lantern-bench-heap"
INPUT_LIMIT = 4096
OUTPUT_LIMIT = 32768
CONTROLLER_NS = 30_000_000_000
BINARY_LIMIT = 128 << 20
CONFIG_FILE = "heap-control.json"
HEX64 = re.compile(r"[0-9a-f]{64}\Z")
HEX40 = re.compile(r"[0-9a-f]{40}\Z")
INSPECT_FORMAT = '[{{json .Id}},{{json .Image}},{{json .State.Status}},{{json .State.Running}},{{.RestartCount}},{{json .Config.User}},{{json .State.StartedAt}},{{json .Mounts}}]'


def digest(data):
    return hashlib.sha256(data).hexdigest()


def json_bytes(value):
    return (json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False) + "\n").encode()


def _pairs(items):
    value = {}
    for key, child in items:
        if key in value:
            raise ValueError("duplicate JSON field")
        value[key] = child
    return value


def decode(data):
    return json.loads(data, object_pairs_hook=_pairs, parse_constant=lambda _: (_ for _ in ()).throw(ValueError("nonfinite JSON")))


def private_directory(path):
    path = Path(path)
    if not path.is_absolute() or path.resolve() != path:
        raise ValueError("control directory must be an absolute physical path")
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o700 or info.st_uid != os.getuid() or info.st_gid != os.getgid():
        raise ValueError("control directory ownership differs")
    return path


def read_private(path, limit, private=True):
    path = Path(path)
    if path.resolve() != path:
        raise ValueError("evidence path contains symlink")
    before = path.lstat()
    if not stat.S_ISREG(before.st_mode) or (private and stat.S_IMODE(before.st_mode) != 0o600) or before.st_uid != os.getuid() or before.st_nlink != 1 or not 0 < before.st_size <= limit:
        raise ValueError("invalid bounded evidence file")
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        opened = os.fstat(fd)
        if not stat.S_ISREG(opened.st_mode) or (private and stat.S_IMODE(opened.st_mode) != 0o600) or opened.st_uid != os.getuid() or opened.st_nlink != 1 or not 0 < opened.st_size <= limit or (opened.st_dev, opened.st_ino) != (before.st_dev, before.st_ino):
            raise ValueError("evidence file identity changed")
        with os.fdopen(fd, "rb", closefd=False) as source:
            data = source.read(limit + 1)
    finally:
        os.close(fd)
    after = path.lstat()
    fingerprint = lambda info: (info.st_dev, info.st_ino, info.st_mode, info.st_uid, info.st_gid, info.st_nlink, info.st_size, info.st_mtime_ns, info.st_ctime_ns)
    if len(data) != before.st_size or len(data) > limit or fingerprint(before) != fingerprint(opened) or fingerprint(before) != fingerprint(after):
        raise ValueError("evidence file changed")
    return data


def write_private(path, value, limit=OUTPUT_LIMIT):
    data = json_bytes(value)
    if len(data) > limit:
        raise ValueError("output exceeds bound")
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        with os.fdopen(fd, "wb", closefd=False) as target:
            target.write(data)
            target.flush()
            os.fsync(fd)
    finally:
        os.close(fd)
    parent = os.open(Path(path).parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(parent)
    finally:
        os.close(parent)
    return {"path": str(path), "sha256": digest(data)}


def raw_reference(path, limit=OUTPUT_LIMIT):
    data = read_private(path, limit)
    return decode(data), {"path": str(path), "sha256": digest(data)}


def docker(argv, deadline):
    # Reserve one second INSIDE the controller budget for direct reap. The
    # enclosing original PG/Compose owner remains the cleanup authority.
    work_end = deadline - 1_000_000_000
    if time.monotonic_ns() >= work_end:
        raise TimeoutError("controller deadline before dispatch")
    child = subprocess.Popen(["docker", *argv], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    selector = selectors.DefaultSelector()
    streams = {"stdout": bytearray(), "stderr": bytearray()}
    for name, limit in (("stdout", 128 << 10), ("stderr", 32 << 10)):
        stream = getattr(child, name)
        os.set_blocking(stream.fileno(), False)
        selector.register(stream, selectors.EVENT_READ, (name, limit))
    try:
        while selector.get_map() or child.poll() is None:
            remaining = (work_end - time.monotonic_ns()) / 1e9
            if remaining <= 0:
                raise TimeoutError("uncertain Docker command")
            for key, _ in selector.select(min(remaining, 0.05)):
                name, limit = key.data
                data = os.read(key.fd, min(8192, limit - len(streams[name]) + 1))
                if not data:
                    selector.unregister(key.fileobj)
                else:
                    streams[name].extend(data)
                    if len(streams[name]) > limit:
                        raise ValueError("Docker output exceeded evidence bound")
        if child.wait(timeout=max(0.001, (deadline-time.monotonic_ns()) / 1e9)) != 0:
            raise ValueError("Docker command failed")
        return bytes(streams["stdout"])
    except (OSError, ValueError, TimeoutError):
        if child.poll() is None:
            try:
                child.kill()
            except ProcessLookupError:
                pass
        try:
            child.wait(timeout=max(0.001, (deadline-time.monotonic_ns()) / 1e9))
        except subprocess.TimeoutExpired:
            raise TimeoutError("Docker child custody unproven") from None
        raise
    finally:
        selector.close()
        child.stdout.close()
        child.stderr.close()


def inspect_nodes(config, state, deadline, runner=docker):
    nodes = config["nodes"]
    raw = runner(["inspect", "--format", INSPECT_FORMAT, *[n["session"]["container_id"] for n in nodes]], deadline)
    rows = [decode(line) for line in raw.splitlines()]
    if len(rows) != 3:
        raise ValueError("three exact containers required")
    for node, row in zip(nodes, rows, strict=True):
        if not isinstance(row, list) or len(row) != 8:
            raise ValueError("invalid container readback")
        cid, image, status, running, restarts, user, started, mounts = row
        if cid != node["session"]["container_id"] or image != config["context"]["image_id"] or status != state or running is not (state == "running") or type(restarts) is not int or restarts != 0 or user != config["context"]["runtime_user"]:
            raise ValueError("container identity/lifecycle differs")
        if state == "created" and started not in ("", "0001-01-01T00:00:00Z"):
            raise ValueError("container was previously started")
        expected = [m for m in mounts if m.get("Destination") == TARGET_DIRECTORY]
        if len(expected) != 1 or expected[0].get("Type") != "bind" or expected[0].get("Source") != node["directory"] or expected[0].get("RW") is not True:
            raise ValueError("exact private control mount differs")


def initialize(root):
    root = Path(root)
    if not root.is_absolute() or root.parent.resolve() != root.parent:
        raise ValueError("control root path differs")
    # Only freshly created control directories are normalized. A setgid
    # parent may assign its group instead of the driver's primary group.
    for directory in (root, *(root / f"lantern-{index}" for index in range(3))):
        directory.mkdir(mode=0o700)
        os.chown(directory, os.getuid(), os.getgid(), follow_symlinks=False)
        os.chmod(directory, 0o700)
        private_directory(directory)


def prepare(args, runner=docker):
    root = private_directory(args.directory)
    out = root.parent
    containers = args.containers.split(",")
    endpoints = args.endpoints.split(",")
    if len(containers) != 3 or len(set(containers)) != 3 or any(not HEX64.fullmatch(c) for c in containers) or len(endpoints) != 3 or len(set(endpoints)) != 3:
        raise ValueError("invalid exact container/endpoint cohort")
    if not HEX40.fullmatch(args.source_commit) or not HEX40.fullmatch(args.source_tree) or not re.fullmatch(r"sha256:[0-9a-f]{64}", args.image_id) or not re.fullmatch(r"[1-9][0-9]*:[0-9]+", args.runtime_user) or int(args.runtime_user.split(":")[0]) != os.getuid():
        raise ValueError("invalid source/image/runtime identity")
    scenario = decode(read_private(args.scenario_json, 1 << 20, private=False))
    scenario_hash = digest(json.dumps(scenario, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode())
    config = {"schema": SCHEMA, "protocol": PROTOCOL, "context": {"run_id": args.run_id, "source_commit": args.source_commit, "source_tree": args.source_tree, "image_id": args.image_id, "scenario_sha256": scenario_hash, "runtime_user": args.runtime_user, "cpuset": args.cpuset}, "nodes": []}
    run_nonce = secrets.token_hex(32)
    for index, (cid, endpoint) in enumerate(zip(containers, endpoints, strict=True)):
        directory = private_directory(root / f"lantern-{index}")
        if list(directory.iterdir()):
            raise ValueError("control directory is not fresh")
        session = {"schema": SCHEMA, "protocol": PROTOCOL, "run_nonce": run_nonce, "source_commit": args.source_commit, "source_tree": args.source_tree, "image_id": args.image_id, "container_id": cid, "binary_sha256": "", "uid": os.getuid()}
        config["nodes"].append({"endpoint": endpoint, "directory": str(directory), "session": session})
    deadline = time.monotonic_ns() + CONTROLLER_NS
    inspect_nodes(config, "created", deadline, runner)
    binary = out / "heap-observer-binary"
    if binary.exists() or binary.is_symlink():
        raise ValueError("binary evidence already exists")
    runner(["cp", containers[0] + ":/app/lantern", str(binary)], deadline)
    info = binary.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_uid != os.getuid() or not 0 < info.st_size <= BINARY_LIMIT:
        raise ValueError("binary evidence ownership/size differs")
    binary_hash = digest(binary.read_bytes())
    os.chmod(binary, 0o600)
    config["binary_file"] = {"path": str(binary), "sha256": binary_hash, "bytes": info.st_size}
    for node in config["nodes"]:
        node["session"]["binary_sha256"] = binary_hash
        write_private(Path(node["directory"]) / "session.json", node["session"], INPUT_LIMIT)
    write_private(out / CONFIG_FILE, config)


def load_config(root):
    root = private_directory(root)
    config = decode(read_private(root.parent / CONFIG_FILE, OUTPUT_LIMIT))
    if config.get("schema") != SCHEMA or config.get("protocol") != PROTOCOL or len(config.get("nodes", [])) != 3:
        raise ValueError("control configuration differs")
    for index, node in enumerate(config["nodes"]):
        if node["directory"] != str(root / f"lantern-{index}"):
            raise ValueError("control configuration directory differs")
        private_directory(node["directory"])
        session, _ = raw_reference(Path(node["directory"]) / "session.json", INPUT_LIMIT)
        if session != node["session"]:
            raise ValueError("immutable session changed")
    return root, config


def completed_work(case, phase):
    counts = [0, 0]
    for stage in (("warmup",) if phase == "baseline" else ("warmup", "steady")):
        for index, name in enumerate(("receipt_admission", "receipt_lookup")):
            value = decode(read_private(case / f"ghz_{stage}_{index}_{name}.json", 1 << 20, private=False))
            count = value.get("count")
            if type(count) is not int or not 0 < count <= 8000 or value.get("statusCodeDistribution") != {"OK": count}:
                raise ValueError("workload did not complete successfully")
            counts[index] += count
    if counts[0] != counts[1] or counts[0] > 8000:
        raise ValueError("admission/status counts differ")
    return counts


def ready_request(node, phase, expected_receipts):
    directory = Path(node["directory"])
    ready, ready_ref = raw_reference(directory / "ready.json")
    if set(ready) != {"schema", "protocol", "identity"} or ready["schema"] != SCHEMA or ready["protocol"] != PROTOCOL:
        raise ValueError("invalid ready schema")
    identity = ready["identity"]
    if set(identity) != {"session", "pid", "start_nonce"} or identity["session"] != node["session"] or type(identity["pid"]) is not int or identity["pid"] != 1 or not HEX64.fullmatch(identity["start_nonce"]):
        raise ValueError("ready process identity differs")
    request = {"schema": SCHEMA, "protocol": PROTOCOL, "identity": identity, "phase": phase, "ordinal": 1 if phase == "baseline" else 2, "cut_nonce": secrets.token_hex(32), "expected_receipts": expected_receipts}
    if phase == "post":
        baseline, _ = raw_reference(directory / "baseline.complete.json")
        if baseline.get("status") != "complete" or baseline.get("request", {}).get("identity") != identity:
            raise ValueError("baseline missing or target restarted")
    elif (directory / "post.request.json").exists():
        raise ValueError("phase replay")
    return request, ready_ref


def validate_completion(value, request, expected_entries):
    if set(value) != {"schema", "protocol", "request", "status", "reason", "elapsed_ns", "before", "after", "collections", "memory"} or value["schema"] != 1 or value["protocol"] != PROTOCOL or value["request"] != request or value["status"] != "complete" or value["reason"] != "" or type(value["elapsed_ns"]) is not int or not 0 < value["elapsed_ns"] <= 25_000_000_000:
        raise ValueError("incomplete target cut")
    native = value["before"]
    native_keys = {"generation", "faulted", "pending_count", "pending_bytes", "pending_local", "snapshot_active", "origins", "log_sequence", "log_length", "log_capacity", "log_evicted", "receipt_entries", "receipt_bytes", "receipt_high_water_ms", "receipt_oldest_deadline_ms", "vertices", "edges", "public", "peer_unary", "peer_subscribe", "apply"}
    if set(native) != native_keys or native != value["after"] or any(native[k] is not False for k in ("faulted", "pending_local", "snapshot_active")) or native["pending_count"] != 0 or native["pending_bytes"] != 0 or native["receipt_entries"] < expected_entries:
        raise ValueError("native bracket drift/work mismatch")
    numeric = native_keys - {"faulted", "pending_local", "snapshot_active", "origins", "public", "peer_unary", "peer_subscribe", "apply"}
    if any(type(native[k]) is not int or not 0 <= native[k] < 1 << 64 for k in numeric) or not 0 < native["log_length"] <= native["log_capacity"] or native["receipt_high_water_ms"] <= 0 or native["receipt_oldest_deadline_ms"] <= native["receipt_high_water_ms"]:
        raise ValueError("invalid native counters")
    for name in ("public", "peer_unary", "peer_subscribe", "apply"):
        c = native[name]
        if set(c) != {"begun", "completed", "active"} or any(type(c[k]) is not int or c[k] < 0 for k in c) or c["begun"] - c["completed"] != c["active"] or (name != "peer_subscribe" and c["active"] != 0):
            raise ValueError("active native work")
    origins = native["origins"]
    if not isinstance(origins, list) or not 0 < len(origins) <= 16:
        raise ValueError("missing bounded origins")
    previous = ""
    for origin in origins:
        if set(origin) != {"id", "sequence", "wall_ns", "logical"} or not re.fullmatch(r"[0-9a-f]{32}", origin["id"]) or origin["id"] <= previous or type(origin["sequence"]) is not int or origin["sequence"] <= 0 or type(origin["wall_ns"]) is not int or origin["wall_ns"] <= 0 or type(origin["logical"]) is not int or not 0 <= origin["logical"] <= 0xffffffff:
            raise ValueError("invalid origin frontier")
        previous = origin["id"]
    def memory(m):
        keys = {"heap_alloc", "heap_objects", "heap_inuse", "num_gc", "num_forced_gc", "last_gc", "next_gc", "marked_live", "metric_cycles", "metric_forced", "cycle_after"}
        if set(m) != keys or any(type(m[k]) is not int or m[k] < 0 for k in keys) or m["num_gc"] != m["metric_cycles"] or m["num_gc"] != m["cycle_after"] or m["num_forced_gc"] != m["metric_forced"] or not 0 < m["heap_alloc"] <= (1 << 63) - 1 or m["marked_live"] <= 0:
            raise ValueError("incoherent memory read")
    collections = value["collections"]
    if not isinstance(collections, list) or len(collections) != 2:
        raise ValueError("two completed collections required")
    for index, gc in enumerate(collections):
        if set(gc) != {"started_ns", "returned_ns", "before", "after"} or type(gc["started_ns"]) is not int or type(gc["returned_ns"]) is not int or not 250_000_000 <= gc["started_ns"] < gc["returned_ns"] <= value["elapsed_ns"]:
            raise ValueError("collection return missing")
        memory(gc["before"])
        memory(gc["after"])
        if gc["after"]["num_gc"] <= gc["before"]["num_gc"] or gc["after"]["num_forced_gc"] - gc["before"]["num_forced_gc"] != 1 or gc["after"]["last_gc"] <= 0:
            raise ValueError("collection counters differ")
        if index and (gc["started_ns"] < collections[index-1]["returned_ns"] or gc["before"]["num_gc"] < collections[index-1]["after"]["num_gc"] or gc["before"]["num_forced_gc"] != collections[index-1]["after"]["num_forced_gc"]):
            raise ValueError("collection sequence drift")
    memory(value["memory"])
    if value["memory"] != collections[1]["after"] or collections[1]["after"]["num_forced_gc"] - collections[0]["before"]["num_forced_gc"] != 2:
        raise ValueError("immediate memory differs")


def capture(args, runner=docker, clock=time.monotonic_ns, pause=time.sleep):
    started = clock()
    deadline = started + CONTROLLER_NS
    root, config = load_config(args.directory)
    if not 0 < args.admission <= 8000 or args.lookup != args.admission:
        raise ValueError("completed admission/status counts required")
    result = {"schema": SCHEMA, "protocol": PROTOCOL, "phase": args.phase, "status": "incomplete", "controller_monotonic_start_ns": started, "controller_monotonic_end_ns": started, "context": dict(config["context"], workload={"admission": args.admission, "lookup": args.lookup}), "replicas": []}
    out = root.parent / ("retained_heap_pre.json" if args.phase == "baseline" else "retained_heap_post.json")
    try:
        scenario = decode(read_private(root.parent / "scenario.resolved.json", 1 << 20, private=False))
        if digest(json.dumps(scenario, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()) != config["context"]["scenario_sha256"] or completed_work(root.parent, args.phase) != [args.admission, args.lookup]:
            raise ValueError("scenario/workload context changed")
        inspect_nodes(config, "running", deadline, runner)
        pending = []
        for node in config["nodes"]:
            request, ready_ref = ready_request(node, args.phase, args.admission)
            pending.append((node, request, ready_ref))
        # Persist every request before any daemon mutation. No signal is sent
        # when pre-dispatch file admission fails; existing files never replace.
        for node, request, _ in pending:
            directory = Path(node["directory"])
            if (directory / (args.phase + ".started.json")).exists() or (directory / (args.phase + ".complete.json")).exists() or (directory / "refusal.json").exists():
                raise ValueError("unexpected phase output exists")
            write_private(directory / (args.phase + ".request.json"), request, INPUT_LIMIT)
        signal_failures = []
        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
            futures = [pool.submit(runner, ["kill", "--signal=USR1", node["session"]["container_id"]], deadline) for node, _, _ in pending]
            for future in futures:
                try:
                    future.result()
                except (OSError, ValueError, TimeoutError):
                    signal_failures.append(True)
        for node, request, ready_ref in pending:
            directory = Path(node["directory"])
            if (directory / "refusal.json").exists() or (directory / "refusal.json").is_symlink():
                raise ValueError("target refused phase")
            complete_path = directory / (args.phase + ".complete.json")
            while not complete_path.exists() and clock() < deadline:
                if (directory / "refusal.json").exists():
                    raise ValueError("target refused phase")
                pause(min(0.05, max(0, (deadline - clock()) / 1e9)))
            completion, complete_ref = raw_reference(complete_path)
            begin, begin_ref = raw_reference(directory / (args.phase + ".started.json"))
            if completion.get("schema") != SCHEMA or completion.get("protocol") != PROTOCOL or completion.get("status") != "complete" or completion.get("request") != request or begin != {"request": request}:
                raise ValueError("target phase incomplete or identity differs")
            validate_completion(completion, request, args.admission)
            session, session_ref = raw_reference(directory / "session.json", INPUT_LIMIT)
            actual_request, request_ref = raw_reference(directory / (args.phase + ".request.json"), INPUT_LIMIT)
            if session != node["session"] or actual_request != request:
                raise ValueError("phase input changed")
            result["replicas"].append({"endpoint": node["endpoint"], "completion": completion, "session_file": session_ref, "ready_file": ready_ref, "request_file": request_ref, "started_file": begin_ref, "completion_file": complete_ref})
        inspect_nodes(config, "running", deadline, runner)
        if any((Path(n["directory"]) / "refusal.json").exists() or (Path(n["directory"]) / "refusal.json").is_symlink() for n in config["nodes"]):
            raise ValueError("target refused phase")
        if any(n["completion"]["before"]["origins"] != result["replicas"][0]["completion"]["before"]["origins"] for n in result["replicas"]):
            raise ValueError("replica committed frontiers differ")
        if signal_failures or clock() >= deadline:
            raise ValueError("uncertain signal or controller deadline")
        # GC/native semantics are independently enforced by the Go evaluator
        # and final pair consumer, not inferred from CLI signal success.
        result["status"] = "complete"
    except (OSError, ValueError, KeyError, TypeError, TimeoutError):
        result["status"] = "incomplete"
    finally:
        result["controller_monotonic_end_ns"] = clock()
        write_private(out, result, 256 << 10)
    return 0 if result["status"] == "complete" else 1


def parser():
    p = argparse.ArgumentParser(description=__doc__)
    sub = p.add_subparsers(dest="command", required=True)
    init = sub.add_parser("init")
    init.add_argument("--directory", required=True)
    prep = sub.add_parser("prepare")
    for name in ("directory", "containers", "endpoints", "source-commit", "source-tree", "image-id", "runtime-user", "scenario-json", "run-id"):
        prep.add_argument("--" + name, required=True)
    prep.add_argument("--cpuset", default="")
    cut = sub.add_parser("capture")
    cut.add_argument("--directory", required=True)
    cut.add_argument("--phase", choices=("baseline", "post"), required=True)
    cut.add_argument("--admission", type=int, required=True)
    cut.add_argument("--lookup", type=int, required=True)
    return p


def main(argv=None):
    args = parser().parse_args(argv)
    try:
        if args.command == "init":
            initialize(args.directory)
        elif args.command == "prepare":
            prepare(args)
        else:
            return capture(args)
        return 0
    except (OSError, ValueError, KeyError, TypeError, TimeoutError):
        print("heap control incomplete; original owner retains cleanup custody", file=__import__("sys").stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
