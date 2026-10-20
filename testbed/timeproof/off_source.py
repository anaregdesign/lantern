#!/usr/bin/env python3
"""Prepare, run once, or verify the separate finite OFF configured-source proof."""

import argparse
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import secrets
import ssl
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request

from process import Child
from deadline import Window, watch_deadlines
from receipt import SCHEMA, sha, strict_json, verify


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


def snapshot(root):
    def git(*args):
        return subprocess.check_output(["git", *args], cwd=root)
    if git("status", "--porcelain", "--untracked-files=all").strip():
        raise ValueError("clean committed source required")
    head = git("rev-parse", "HEAD").decode().strip()
    tree = git("rev-parse", "HEAD^{tree}").decode().strip()
    inputs = {}
    for entry in git("ls-tree", "-rz", "HEAD").split(b"\0"):
        if not entry:
            continue
        metadata, name = entry.split(b"\t", 1)
        mode, kind, oid = metadata.decode().split()
        path = root / name.decode()
        if kind != "blob" or mode not in ("100644", "100755") or path.is_symlink() or not path.is_file():
            raise ValueError("unsupported tracked source")
        raw = path.read_bytes()
        actual = hashlib.sha1(f"blob {len(raw)}\0".encode() + raw).hexdigest()
        if actual != oid or bool(path.stat().st_mode & 0o111) != (mode == "100755"):
            raise ValueError("tracked bytes/mode differ")
        inputs[name.decode()] = sha(raw)
    if git("rev-parse", "HEAD").decode().strip() != head:
        raise ValueError("source changed during snapshot")
    return {"head": head, "tree": tree, "inputs": inputs}


def checked_environment():
    for key in ("GOFLAGS", "GOEXPERIMENT", "GODEBUG", "LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "NODE_OPTIONS", "BUN_OPTIONS", "RUSTFLAGS"):
        if os.environ.get(key):
            raise ValueError("unsupported execution override: " + key)


@contextmanager
def tracked_build(root, output, source):
    """Build only immutable Git objects; ignored/working-tree files never enter."""
    with tempfile.TemporaryDirectory(prefix="tracked-source-", dir=output) as temporary:
        build = Path(temporary)
        directories = {build}
        entries = subprocess.check_output(["git", "ls-tree", "-rz", source["head"]], cwd=root).split(b"\0")
        blobs = subprocess.Popen(["git", "cat-file", "--batch"], cwd=root, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            seen = set()
            for entry in entries:
                if not entry:
                    continue
                metadata, raw_name = entry.split(b"\t", 1)
                mode, kind, oid = metadata.decode().split()
                name = raw_name.decode()
                if kind != "blob" or mode not in ("100644", "100755") or name not in source["inputs"]:
                    raise ValueError("unexpected Git export input")
                blobs.stdin.write((oid+"\n").encode())
                blobs.stdin.flush()
                header = blobs.stdout.readline(256).decode().split()
                if len(header) != 3 or header[:2] != [oid, "blob"]:
                    raise ValueError("invalid Git object export")
                raw = blobs.stdout.read(int(header[2]))
                if blobs.stdout.read(1) != b"\n" or sha(raw) != source["inputs"][name]:
                    raise ValueError("exported source differs")
                destination = build / name
                destination.parent.mkdir(parents=True, exist_ok=True)
                directories.update(destination.parents)
                destination.write_bytes(raw)
                destination.chmod(0o555 if mode == "100755" else 0o444)
                seen.add(name)
            if seen != set(source["inputs"]):
                raise ValueError("incomplete tracked export")
            for directory in directories:
                if directory == build or build in directory.parents:
                    directory.chmod(0o555)
            yield build
        finally:
            blobs.stdin.close()
            blobs.stdout.close()
            try:
                blobs.wait(timeout=30)
            except subprocess.TimeoutExpired:
                blobs.kill()
                blobs.wait(timeout=5)
            blobs.stderr.close()
            for directory in sorted(directories, key=lambda p: len(p.parts)):
                if directory == build or build in directory.parents:
                    directory.chmod(0o700)


def prepare(root, output, go):
    checked_environment()
    source = snapshot(root)
    if output == root or root in output.parents:
        raise ValueError("preparation must be outside the checkout")
    output.mkdir(mode=0o700, exist_ok=False)
    with tracked_build(root, output, source) as build:
        return prepare_export(root, output, go, source, build)


def prepare_export(root, output, go, source, build):
    env = os.environ | {"GOTOOLCHAIN": "local", "GOWORK": str(build / "go.work")}
    settings = strict_json(subprocess.check_output([go, "env", "-json", "GOFLAGS", "GOEXPERIMENT", "GOWORK", "GOTOOLCHAIN", "GOROOT", "GOMODCACHE", "CGO_ENABLED", "GOVERSION"], cwd=build, env=env))
    if settings["GOFLAGS"] or settings["GOEXPERIMENT"] or settings["GOWORK"] != str(build/"go.work") or settings["GOTOOLCHAIN"] != "local":
        raise ValueError("unsupported effective Go settings")
    with (output / "module-integrity.log").open("xb") as log:
        subprocess.run([go, "mod", "verify"], cwd=build, env=env, stdout=log, stderr=subprocess.STDOUT, check=True, timeout=120)
    child, fixture = output / "off-child.test", output / "authfixture"
    command = [go, "test", "-c", "-ldflags=-X github.com/anaregdesign/lantern/server/cmd.offSourceRevision=" + source["head"], "-o", str(child), "./server/cmd"]
    with (output / "build-child.log").open("xb") as log:
        subprocess.run(command, cwd=build, env=env, stdout=log, stderr=subprocess.STDOUT, check=True, timeout=300)
    with (output / "build-fixture.log").open("xb") as log:
        subprocess.run([go, "build", "-o", str(fixture), "./server/cmd/authfixture"], cwd=build, env=env, stdout=log, stderr=subprocess.STDOUT, check=True, timeout=300)
    nonce = secrets.token_hex(32)
    identity = subprocess.run([str(child), "-test.run=^TestOFFConfiguredSourceChild$", "-test.timeout=10s"], cwd=build, env=env | {"OFF_SOURCE_CHILD_NONCE": nonce, "OFF_SOURCE_CHILD_IDENTITY": "1"}, capture_output=True, check=True, timeout=15)
    (output / "identity.stdout").write_bytes(identity.stdout)
    records = [strict_json(line[len(b"off_source "):]) for line in identity.stdout.splitlines() if line.startswith(b"off_source ")]
    if len(records) != 1 or records[0].get("event") != "identity" or records[0].get("revision") != source["head"] or records[0].get("binary_sha256") != sha(child.read_bytes()) or records[0].get("normal_build") is not True:
        raise ValueError("normal binary identity not established")
    info = records[0]
    target = [info["goos"], info["goarch"]]
    if target not in (["linux", "arm64"], ["linux", "amd64"], ["darwin", "arm64"]):
        raise ValueError("unsupported prepared target")
    version = re.search(r"^go (\S+)$", (root / "server/go.mod").read_text(), re.M)
    if not version or info.get("go_version") != "go" + version[1]:
        raise ValueError("unmatched pinned Go version")
    if snapshot(root) != source:
        raise ValueError("source changed during build")
    manifest = {"schema": SCHEMA, "source": source, "target": target, "go_version": info["go_version"], "go_settings": settings, "build_command": command, "child_sha256": sha(child.read_bytes()), "fixture_sha256": sha(fixture.read_bytes()), "go_binary_sha256": sha(Path(go).read_bytes()), "build_logs": {name: sha((output/name).read_bytes()) for name in ("module-integrity.log", "build-child.log", "build-fixture.log", "identity.stdout")}, "owner_lifetimes_started": 0}
    write_json(output / "prepared.json", manifest)
    return sha((output / "prepared.json").read_bytes())


def environment():
    # This collector currently selects one Linux environment. It does not alter
    # the production target policy or qualify another host through this result.
    if platform.system() != "Linux" or platform.machine() not in ("aarch64", "arm64", "x86_64", "amd64"):
        raise ValueError("execution requires the selected supported Linux environment")
    source = Path("/etc/ntp.conf")
    if source.is_symlink() or source.read_bytes() != b"server time.asia.apple.com\n" or not os.statvfs(source).f_flag & os.ST_RDONLY:
        raise ValueError("exact read-only source input required")
    namespaces = {os.readlink(p) for p in ("/proc/self/ns/time", "/proc/self/ns/time_for_children", "/proc/thread-self/ns/time", "/proc/thread-self/ns/time_for_children")}
    offsets = Path("/proc/self/timens_offsets").read_bytes()
    if len(namespaces) != 1 or offsets.split() != [b"monotonic", b"0", b"0", b"boottime", b"0", b"0"]:
        raise ValueError("native namespace premises not established")
    return {"system": platform.system(), "kernel": platform.release(), "machine": platform.machine(), "boot_sha256": sha(Path("/proc/sys/kernel/random/boot_id").read_bytes()), "namespace": namespaces.pop(), "offsets_sha256": sha(offsets), "source_sha256": sha(source.read_bytes()), "source_path": "/etc/ntp.conf", "source_read_only": True, "capabilities": [line for line in Path("/proc/self/status").read_text().splitlines() if line.startswith(("Cap", "NoNewPrivs:"))]}


def load_prepared(root, directory, digest):
    checked_environment()
    path = directory / "prepared.json"
    if path.stat().st_size > 8 << 20:
        raise ValueError("preparation manifest too large")
    raw = path.read_bytes()
    if sha(raw) != digest:
        raise ValueError("untrusted preparation manifest")
    prepared = strict_json(raw, 8 << 20)
    if prepared.get("schema") != SCHEMA or snapshot(root) != prepared.get("source"):
        raise ValueError("preparation belongs to another source")
    for name, key in (("off-child.test", "child_sha256"), ("authfixture", "fixture_sha256")):
        if (directory / name).is_symlink() or sha((directory / name).read_bytes()) != prepared.get(key):
            raise ValueError("prepared executable changed")
    for name in ("module-integrity.log", "build-child.log", "build-fixture.log", "identity.stdout"):
        if sha((directory/name).read_bytes()) != prepared.get("build_logs", {}).get(name):
            raise ValueError("preparation execution log changed")
    prepared["digest"] = digest
    return prepared


def request(opener, url, deadline, payload):
    timeout = min(3, deadline.check())
    raw = json.dumps(payload).encode()
    req = urllib.request.Request(url, data=raw, headers={"Content-Type": "application/json", "Connect-Protocol-Version": "1"})
    with opener.open(req, timeout=timeout) as response:
        deadline.check()
        raw = response.read(65537)
        deadline.check()
        if len(raw) > 65536:
            raise ValueError("wire response too large")
        return {} if not raw else json.loads(raw)


def wire_smoke(fixture, children, deadline):
    context = ssl.create_default_context(cafile=fixture["ca_file"])
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=context))
    nodes = fixture["nodes"]
    for node in nodes:
        while True:
            try:
                health = request(opener, node["public_origin"] + "/grpc.health.v1.Health/Check", deadline, {})
                caps = request(opener, node["public_origin"] + "/graph.v1.LanternSecurityService/GetAuthCapabilities", deadline, {})
                if health.get("status") == "SERVING_STATUS_SERVING" and caps.get("mode") == "AUTH_MODE_OFF" and caps.get("ready") is True and caps.get("protocolVersion") == 1:
                    break
            except (urllib.error.URLError, TimeoutError, json.JSONDecodeError):
                if deadline.remaining() <= 0:
                    raise TimeoutError("OFF readiness deadline")
            deadline.pause(.05)
        request(opener, node["public_origin"] + "/graph.v1.LanternService/GetServerStatus", deadline, {})
    key = "off-source:" + secrets.token_hex(16)
    request(opener, nodes[0]["public_origin"] + "/graph.v1.LanternService/PutVertex", deadline, {"vertex": {"key": key, "string": "proof"}})
    for node in nodes:
        while True:
            try:
                result = request(opener, node["public_origin"] + "/graph.v1.LanternService/GetVertex", deadline, {"key": key})
                if result.get("vertex", {}).get("string") == "proof":
                    break
            except urllib.error.URLError:
                pass
            deadline.pause(.05)
    for child in children:
        child.send("probe", deadline)
    for child in children:
        probe = child.receive("probe", deadline)
        if probe.get("ok") is not True:
            raise ValueError(f"private source smoke failed (slot {child.slot}): " + probe.get("error", "probe detail unavailable"))
    return {"public_off": True, "replication": True, "private": True}


def run(root, directory, output, prepared):
    if output == root or root in output.parents:
        raise ValueError("evidence must be outside the checkout")
    before = environment() # Metadata only; never probes DNS/NTP or sets the OS.
    target = ["linux", "arm64" if platform.machine() in ("aarch64", "arm64") else "amd64"]
    if prepared["target"] != target:
        raise ValueError("prepared target differs from selected environment")
    output.mkdir(mode=0o700, exist_ok=False)
    # Atomic one-use allocation for these prepared artifacts. Failure consumes
    # the allocation too; the program has no restart/rerun path.
    with (directory / "OFF-ALLOCATION-CONSUMED").open("x") as token:
        token.write(str(output) + "\n")
    children, rows = [], []
    window = Window()
    started = window.started
    work, deadline = window.limit(89), window.limit(120)
    finished = threading.Event()
    receipt = {"schema": SCHEMA, "status": "FAIL", "source": prepared["source"], "prepared_sha256": prepared["digest"], "before": before, "children": rows, "wire": {}}

    def stop_all():
        for child in list(children):
            try:
                if child.stop_ns is None:
                    child.send("stop", window.limit(90).cap(.5))
            except (OSError, TimeoutError, ValueError):
                child.terminate()

    actions = ((89, stop_all), (105, lambda: [c.terminate() for c in list(children)]), (115, lambda: [c.terminate(kill=True) for c in list(children)]))
    watcher = threading.Thread(target=watch_deadlines, args=(finished, window, actions), daemon=True)
    watcher.start()
    try:
        for slot in range(1, 4):
            work.check()
            child = Child(directory / "off-child.test", slot, secrets.token_hex(32), output, started)
            children.append(child)
            child.send("reserve", work.cap(10), private=True)
        reservations = [child.receive("reserved", work.cap(10)) for child in children]
        public = ",".join(r["public"].rsplit(":", 1)[1] for r in reservations)
        peers = ",".join(r["peer"].rsplit(":", 1)[1] for r in reservations)
        material = output / "material"
        with (output / "provision.log").open("xb") as log, (output / "provision.stderr").open("xb") as errors:
            subprocess.run([str(directory / "authfixture"), "-directory", str(material), "-mode", "off", "-public-ports", public, "-peer-ports", peers], env={k: v for k, v in os.environ.items() if not k.upper().startswith("LANTERN_")}, stdout=log, stderr=errors, check=True, timeout=min(10, work.check()))
        work.check()
        fixture = strict_json((output / "provision.log").read_bytes())
        if len(fixture["nodes"]) != 3:
            raise ValueError("fixture topology differs")
        for node, child in zip(fixture["nodes"], children):
            env = node["environment"] | {"LANTERN_AUTH_MODE": "off", "LANTERN_DRAIN_DELAY_SECONDS": "0", "LANTERN_BACKUP_ENABLED": "false", "LANTERN_BACKUP_RESTORE_ON_START": "false"}
            child.send("configure", work.cap(5), environment=[k + "=" + v for k, v in sorted(env.items())])
        for child in children:
            child.receive("adopted", work.cap(45))
        receipt["wire"] = wire_smoke(fixture, children, window.limit(85))
        stop_all()
        for child in children:
            for event in ("stop_requested", "app_returned", "cleanup_returned", "final"):
                child.receive(event, deadline)
            rows.append(child.wait(deadline, prepared["child_sha256"]))
        receipt["after"] = environment()
        receipt["elapsed_ns"], receipt["boottime_elapsed_ns"] = window.elapsed()
        receipt["final_elapsed_ns"] = max(window.elapsed())
        receipt["launch_ledger"] = [{"slot": c.slot, "pid": c.process.pid, "nonce": c.nonce, "exit": c.process.returncode, "killed": c.killed, "launch_ns": c.launch_ns, "stop_ns": c.stop_ns, "final_counters_available": True} for c in children]
        receipt["status"] = "PASS"
        receipt["totals"] = verify(receipt, prepared, output)
        if snapshot(root) != prepared["source"]:
            raise ValueError("source changed during proof")
    except Exception as error:
        receipt["status"], receipt["failure"] = "FAIL", type(error).__name__
        receipt["failure_detail"] = str(error)
    finally:
        stop_all()
        for child in children:
            if child.process.poll() is None:
                try:
                    child.process.wait(timeout=min(2, deadline.check()))
                except (subprocess.TimeoutExpired, TimeoutError):
                    child.terminate(kill=True)
                    child.process.wait(timeout=5)
            child.reader.join(timeout=2)
            child.process.stdin.close()
            child.process.stdout.close()
            child.stderr.close()
            if receipt["status"] == "FAIL" and not any(row["slot"] == child.slot for row in rows):
                try:
                    rows.append(child.failure_row(prepared["child_sha256"]))
                except Exception as error:
                    receipt.setdefault("cleanup_failures", []).append({"slot": child.slot, "error": type(error).__name__})
        finished.set()
        watcher.join(timeout=1)
        receipt["final_elapsed_ns"] = max(window.elapsed())
        receipt["launch_ledger"] = [{"slot": c.slot, "pid": c.process.pid, "nonce": c.nonce, "exit": c.process.returncode, "killed": c.killed, "launch_ns": c.launch_ns, "stop_ns": c.stop_ns, "final_counters_available": any(r.get("event") == "final" for r in c.records)} for c in children]
        if receipt["final_elapsed_ns"] > 120_000_000_000 or watcher.is_alive():
            receipt["status"] = "FAIL"
        write_json(output / "receipt.json", receipt)
    return receipt


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("prepare", "run", "verify"))
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--prepared", type=Path, required=True)
    parser.add_argument("--go")
    parser.add_argument("--prepared-sha256")
    parser.add_argument("--evidence", type=Path)
    parser.add_argument("--trusted-receipt-sha256")
    parser.add_argument("--execute", action="store_true")
    args = parser.parse_args()
    root, directory = args.root.resolve(), args.prepared.resolve()
    if args.action == "prepare":
        if not args.go or not Path(args.go).is_absolute():
            parser.error("prepare requires the absolute pinned Go executable")
        print(json.dumps({"prepared_sha256": prepare(root, directory, args.go)}))
        return
    if not args.prepared_sha256 or not args.evidence:
        parser.error("trusted preparation digest and evidence directory required")
    prepared = load_prepared(root, directory, args.prepared_sha256)
    if args.action == "run":
        if not args.execute:
            parser.error("run starts the allocated real source owners; explicit --execute required")
        result = run(root, directory, args.evidence.resolve(), prepared)
        print(json.dumps({"status": result["status"], "receipt_sha256": sha((args.evidence / "receipt.json").read_bytes())}))
        raise SystemExit(0 if result["status"] == "PASS" else 1)
    path = args.evidence / "receipt.json"
    if path.stat().st_size > 8 << 20:
        raise ValueError("receipt too large")
    raw = path.read_bytes()
    if not args.trusted_receipt_sha256 or sha(raw) != args.trusted_receipt_sha256:
        raise ValueError("trusted same-run receipt digest required")
    receipt = strict_json(raw, 8 << 20)
    if environment() != receipt.get("before"):
        raise ValueError("receipt belongs to another selected environment")
    print(json.dumps(verify(receipt, prepared, args.evidence)))


if __name__ == "__main__":
    main()
