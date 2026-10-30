"""Read-only validation of the explicitly paired #1958 receipt measurements.

Normal-image checks and tagged retained-memory checks have separate scopes.
This consumer checks their literal evidence; it never runs GC or a workload.
"""

import datetime
import decimal
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


def read(root, path, limit=1024 * 1024, decode_json=True, modes=None):
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
        if modes is not None:
            need(before.st_uid == os.getuid() and stat.S_IMODE(before.st_mode) in modes,
                 "goroutine evidence ownership or mode differs")
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
    if not decode_json:
        return raw, hashlib.sha256(raw).hexdigest()
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


def mount_binding(root, replica, phase, context, index):
    """Recompute one immutable, case-local original-first directory proof."""
    base = Path(root).absolute() / "heap-mount-evidence" / f"lantern-{index}"
    seen = {}

    def raw(ref, json_value=True):
        need(isinstance(ref, dict) and set(ref) == {"path", "sha256"}
             and isinstance(ref["path"], str) and HEX64.fullmatch(ref.get("sha256", ""))
             and Path(ref["path"]).parent == base, "mount proof reference escapes node")
        value, digest = read(root, ref["path"], 128 << 10, decode_json=json_value)
        need(digest == ref["sha256"], "mount proof raw hash differs")
        info = Path(ref["path"]).lstat()
        need(info.st_uid == os.getuid() and stat.S_IMODE(info.st_mode) == 0o600,
             "mount proof file is not private")
        seen[ref["path"]] = info.st_size
        need(len(seen) <= 32 and sum(seen.values()) <= 2 << 20, "mount proof exceeds bounds")
        return value

    ref = replica.get("mount_binding_file")
    need(isinstance(ref, dict) and ref.get("path") == str(base / "binding.json"), "mount binder path differs")
    binding = raw(ref)
    keys = {"schema", "protocol", "context", "service", "session", "canonical_directory", "daemon_id",
            "host_precreate_file", "host_before_file", "host_after_file", "original_before_file",
            "original_after_file", "daemon_before_file", "daemon_after_file", "pairs_file", "probe",
            "clone_before_file", "clone_after_file"}
    need(isinstance(binding, dict) and set(binding) == keys and type(binding["schema"]) is int
         and binding["schema"] == 1 and binding["protocol"] == PROTOCOL, "mount binder schema differs")
    session = replica["completion"]["request"]["identity"]["session"]
    expected_context = {k: v for k, v in context.items() if k != "workload"}
    service = f"lantern-{index}"
    canonical = str(Path(root).absolute() / "heap-control-evidence" / service)
    need(binding["context"] == expected_context and binding["session"] == session
         and binding["service"] == service and binding["canonical_directory"] == canonical
         and isinstance(binding["daemon_id"], str) and 0 < len(binding["daemon_id"]) <= 128
         and all(32 < ord(c) < 127 for c in binding["daemon_id"]), "mount binder context differs")
    uid, gid = map(int, context["runtime_user"].split(":"))

    def host(value):
        need(isinstance(value, dict) and set(value) == {"device", "inode", "type", "uid", "gid", "mode"}
             and all(isinstance(value[k], str) and re.fullmatch(r"[1-9][0-9]*", value[k]) for k in ("device", "inode"))
             and value["type"] == "directory" and type(value["uid"]) is int and value["uid"] == uid
             and type(value["gid"]) is int and value["gid"] == gid
             and type(value["mode"]) is int and value["mode"] == 448, "mount host identity differs")
        return value

    fixed = {"host_precreate_file": "host-precreate.json", "host_before_file": "host-before.json",
             "host_after_file": "host-after.json", "original_before_file": "original-before.json",
             "original_after_file": "original-after.json", "daemon_before_file": "daemon-before",
             "daemon_after_file": "daemon-after", "pairs_file": "requested-pairs.json",
             "clone_before_file": "clone-before.json", "clone_after_file": "clone-after.json"}
    need(all(binding[k].get("path") == str(base / name) for k, name in fixed.items()), "mount raw filename differs")
    identity = host(raw(binding["host_precreate_file"]))
    need(host(raw(binding["host_before_file"])) == identity and host(raw(binding["host_after_file"])) == identity,
         "mount host directory replaced")
    daemon = (binding["daemon_id"] + "\n").encode()
    need(raw(binding["daemon_before_file"], False) == daemon
         and raw(binding["daemon_after_file"], False) == daemon, "mount daemon changed")

    def observation(ref):
        value = raw(ref)
        keys = {"schema", "kind", "id", "name", "image", "user", "project", "service", "oneoff", "owner", "initializer", "entrypoint", "cmd", "working_dir", "host_config", "mounts", "state", "restart_count"}
        host_keys = {"network_mode", "binds", "mounts", "readonly_rootfs", "privileged", "auto_remove", "cap_add", "cap_drop", "security_opt", "pid_mode", "ipc_mode", "uts_mode", "userns_mode", "restart_policy", "tmpfs"}
        state_keys = {"status", "running", "pid", "started_at", "exit_code", "oom_killed", "restarting", "paused"}
        need(isinstance(value, dict) and set(value) == keys and type(value["schema"]) is int
             and value["schema"] == 1 and value["kind"] == "heap-mount-observation"
             and all(isinstance(value[k], str) for k in ("id", "name", "image", "user", "project", "service", "oneoff", "owner", "initializer", "working_dir"))
             and integer(value["restart_count"]) and isinstance(value["host_config"], dict)
             and set(value["host_config"]) == host_keys and isinstance(value["state"], dict)
             and set(value["state"]) == state_keys, "mount watched projection differs")
        hc, st = value["host_config"], value["state"]
        need(all(type(hc[k]) is bool for k in ("readonly_rootfs", "privileged", "auto_remove"))
             and all(isinstance(hc[k], str) for k in ("network_mode", "pid_mode", "ipc_mode", "uts_mode", "userns_mode"))
             and all(type(st[k]) is bool for k in ("running", "oom_killed", "restarting", "paused"))
             and integer(st["pid"]) and st["pid"] <= 2147483647 and integer(st["exit_code"]) and st["exit_code"] <= 255 and isinstance(st["status"], str)
             and isinstance(st["started_at"], str) and st["restarting"] is False and st["paused"] is False,
             "mount watched scalar types differ")
        for key in ("entrypoint", "cmd"):
            need(value[key] is None or (isinstance(value[key], list) and all(isinstance(x, str) for x in value[key])), "mount command type differs")
        hc_names = {"network_mode": "NetworkMode", "binds": "Binds", "mounts": "Mounts", "readonly_rootfs": "ReadonlyRootfs", "privileged": "Privileged", "auto_remove": "AutoRemove", "cap_add": "CapAdd", "cap_drop": "CapDrop", "security_opt": "SecurityOpt", "pid_mode": "PidMode", "ipc_mode": "IpcMode", "uts_mode": "UTSMode", "userns_mode": "UsernsMode", "restart_policy": "RestartPolicy", "tmpfs": "Tmpfs"}
        labels = {"com.docker.compose.project": value["project"], "com.docker.compose.service": value["service"], "com.docker.compose.oneoff": value["oneoff"], "lantern.owner": value["owner"], "lantern.initializer": value["initializer"]}
        return {"Id": value["id"], "Name": value["name"], "Image": value["image"], "Config": {"User": value["user"], "Labels": labels, "Entrypoint": value["entrypoint"], "Cmd": value["cmd"], "WorkingDir": value["working_dir"]}, "HostConfig": {hc_names[k]: v for k, v in hc.items()}, "Mounts": value["mounts"], "RestartCount": value["restart_count"], "State": {"Status": st["status"], "Running": st["running"], "Pid": st["pid"], "StartedAt": st["started_at"], "ExitCode": st["exit_code"], "OOMKilled": st["oom_killed"], "Restarting": st["restarting"], "Paused": st["paused"]}}

    def profile(obj, cid, state):
        need(isinstance(obj, dict) and obj.get("Id") == cid and obj.get("Image") == context["image_id"]
             and obj.get("Config", {}).get("User") == context["runtime_user"]
             and obj.get("Config", {}).get("Labels", {}).get("com.docker.compose.service") == service
             and obj.get("Config", {}).get("Labels", {}).get("com.docker.compose.project") == context["run_id"]
             and obj.get("RestartCount") == 0 and type(obj.get("RestartCount")) is int
             and obj.get("State", {}).get("Status") == state
             and obj.get("State", {}).get("Running") is (state == "running"), "mount container identity differs")
        st = obj["State"]
        if state == "created":
            need(st.get("StartedAt") in ("", "0001-01-01T00:00:00Z"), "mount original was started")
        if state == "exited":
            need(type(st.get("ExitCode")) is int and st["ExitCode"] == 0 and st.get("OOMKilled") is False,
                 "mount initializer did not exit cleanly")
        return obj

    def fingerprint(obj):
        need(all(k in obj for k in ("Id", "Name", "Image", "Config", "HostConfig", "Mounts")), "mount profile missing")
        return {k: (sorted(obj[k], key=lambda m: m["Destination"]) if k == "Mounts" else obj[k])
                for k in ("Id", "Name", "Image", "Config", "HostConfig", "Mounts")}

    def probe_fingerprint(obj):
        # Match only the maintained primitive's default OOM-killer field.
        # Keep raw objects/hashes intact and reject Python's int==False alias.
        value = obj.get("HostConfig", {}).get("OomKillDisable")
        need(value is None or type(value) is bool, "mount probe OomKillDisable type differs")
        result = fingerprint(obj)
        result["HostConfig"] = dict(result["HostConfig"], OomKillDisable=False if value is None else value)
        return result

    def bind_options(value):
        return (isinstance(value, dict) and value.get("Propagation") == "rprivate"
                and (set(value) == {"Propagation"} or (set(value) == {"Propagation", "CreateMountpoint"} and value["CreateMountpoint"] is False)))

    def mounts(obj, allowed=None):
        requested = obj.get("HostConfig", {}).get("Mounts")
        effective = obj.get("Mounts")
        need(isinstance(requested, list) and len(requested) == 3 and isinstance(effective, list) and len(effective) == 5,
             "mount membership differs")
        volumes = [r for r in requested if r.get("Type") == "volume"]
        need(len(volumes) == 2 and {r.get("Target") for r in volumes} == {"/data", "/state"}
             and all(set(r) == {"Source", "Target", "Type", "VolumeOptions"}
                     and isinstance(r["Source"], str) and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]+", r["Source"])
                     and isinstance(r["VolumeOptions"], dict) and set(r["VolumeOptions"]) == {"NoCopy"}
                     and r["VolumeOptions"]["NoCopy"] is True for r in volumes), "mount volume/NoCopy differs")
        binds = obj.get("HostConfig", {}).get("Binds")
        need(isinstance(binds, list) and len(binds) == 2
             and all(isinstance(v, str) and len(v.split(":")) == 3 and v.startswith("/") for v in binds)
             and sorted(v.split(":")[1:] for v in binds) == [["/etc/ntp.conf", "ro"], ["/run/lantern-config", "ro"]],
             "mount read-only auth binds differ")
        req = [r for r in requested if r.get("Target") == "/run/lantern-bench-heap"]
        eff = [r for r in effective if r.get("Destination") == "/run/lantern-bench-heap"]
        need(len(req) == len(eff) == 1, "mount target missing or duplicated")
        a, b = req[0], eff[0]
        need(set(a) in ({"Type", "Source", "Target", "BindOptions"}, {"Type", "Source", "Target", "ReadOnly", "BindOptions"})
             and a["Type"] == "bind" and ("ReadOnly" not in a or a["ReadOnly"] is False)
             and bind_options(a["BindOptions"])
             and set(b) == {"Type", "Source", "Destination", "Mode", "RW", "Propagation"}
             and b["Type"] == "bind" and b["RW"] is True and b["Mode"] in ("", "rw")
             and b["Propagation"] == "rprivate", "mount non-Source options differ")
        for value in (a["Source"], b["Source"]):
            need(isinstance(value, str) and value.startswith("/") and str(Path(value)) == value and ".." not in Path(value).parts
                 and not any(ord(c) < 32 or ord(c) == 127 or c in ',"' for c in value), "mount source unsafe")
            if allowed is not None:
                need(value in allowed, "mount Source outside frozen proof")
        return a, b

    def same_serving_profile(left, right):
        need(all(left.get(k) == right.get(k) for k in ("Id", "Name", "Image", "Config")), "mount serving identity changed")
        lh, rh = left["HostConfig"], right["HostConfig"]
        need(set(lh) == set(rh) and all(lh[k] == rh[k] for k in lh if k != "Mounts"), "mount serving hardening changed")
        for lrows, rrows, target_key in ((lh["Mounts"], rh["Mounts"], "Target"), (left["Mounts"], right["Mounts"], "Destination")):
            need(len(lrows) == len(rrows), "mount serving membership changed")
            ordered = sorted(lrows, key=lambda r: r[target_key])
            other = sorted(rrows, key=lambda r: r[target_key])
            for a, b in zip(ordered, other):
                need(a[target_key] == b[target_key], "mount serving target changed")
                if a[target_key] == "/run/lantern-bench-heap":
                    need({k: v for k, v in a.items() if k != "Source"} == {k: v for k, v in b.items() if k != "Source"}, "mount serving options changed")
                else:
                    need(a == b, "mount unrelated row changed")

    cid = session["container_id"]
    original = profile(observation(binding["original_before_file"]), cid, "created")
    original_after = profile(observation(binding["original_after_file"]), cid, "created")
    need(fingerprint(original) == fingerprint(original_after), "mount original fingerprint changed")
    need(original["Name"] == f"/{context['run_id']}-{service}-1"
         and original["Config"]["Labels"]["com.docker.compose.oneoff"] == "False"
         and original["Config"]["Labels"]["lantern.owner"] == original["Config"]["Labels"]["lantern.initializer"] == "", "mount original ownership differs")
    original_request, original_effective = mounts(original)
    sources = sorted({original_request["Source"], original_effective["Source"]} - {canonical})
    allowed = {canonical, original_request["Source"], original_effective["Source"]}
    pairs = raw(binding["pairs_file"])
    expected_pairs = [{"destination": "/run/lantern-bench-heap", "declared_source": canonical,
                       "original_source": canonical, "clone_source": source, "expected_type": "directory"}
                      for source in sources]
    need(pairs == expected_pairs and len(pairs) <= 2, "mount pair plan differs")
    probe = binding["probe"]
    need((probe is None) == (not pairs), "mount missing or unexpected probe")
    if pairs:
        need(isinstance(probe, dict) and set(probe) == {"created_file", "completed_file", "output_file", "wait_file", "remove_file", "environment_file"}, "mount probe references differ")
        probe_names = {"created_file": "probe-created.json", "completed_file": "probe-completed.json", "output_file": "probe-output.log", "wait_file": "probe-wait.log", "remove_file": "probe-remove.log", "environment_file": "image-environment.json"}
        need(all(probe[k].get("path") == str(base / name) for k, name in probe_names.items()), "mount probe filename differs")
        created, completed = raw(probe["created_file"]), raw(probe["completed_file"])
        pcid = created.get("Id")
        need(isinstance(pcid, str) and HEX64.fullmatch(pcid) and pcid != cid, "mount probe identity differs")
        environment = raw(probe["environment_file"])
        allowed_env = {"PATH", "LANTERN_DEFAULT_TTL_SECONDS", "LANTERN_PORT", "LANTERN_METRICS_ADDR", "LANTERN_LOG_FORMAT", "LANTERN_LOG_LEVEL", "LANTERN_VERSION", "LANTERN_COMMIT"}
        need(isinstance(environment, list) and all(isinstance(x, str) and "=" in x and x.split("=", 1)[0] in allowed_env
             and not any(ord(c) < 32 or ord(c) == 127 for c in x) for x in environment)
             and len({x.split("=", 1)[0] for x in environment}) == len(environment), "mount probe environment differs")
        targets = [f"/proof/{side}-{i}" for i in range(len(pairs)) for side in ("original", "clone")]
        for obj, state in ((created, "created"), (completed, "exited")):
            need(obj.get("Id") == pcid and obj.get("Image") == context["image_id"]
                 and obj.get("Config", {}).get("User") == context["runtime_user"]
                 and obj.get("Config", {}).get("Image") == context["image_id"]
                 and obj.get("Config", {}).get("Entrypoint") == ["/bin/busybox"]
                 and obj.get("Config", {}).get("Cmd") == ["stat", "-c", "%d:%i:%F", *targets]
                 and sorted(obj.get("Config", {}).get("Env", [])) == sorted(environment)
                 and obj.get("State", {}).get("Status") == state and obj["State"].get("Running") is False,
                 "mount probe execution differs")
            hc = obj.get("HostConfig", {})
            need(hc.get("NetworkMode") == "none" and hc.get("ReadonlyRootfs") is True
                 and hc.get("Privileged") is False and hc.get("AutoRemove") is False
                 and hc.get("CapDrop") == ["ALL"] and hc.get("CapAdd") in (None, [])
                 and hc.get("SecurityOpt") == ["no-new-privileges:true"]
                 and type(hc.get("Memory")) is int and hc["Memory"] == 33554432
                 and type(hc.get("NanoCpus")) is int and hc["NanoCpus"] == 250000000
                 and type(hc.get("PidsLimit")) is int and hc["PidsLimit"] == 16
                 and hc.get("RestartPolicy") == {"Name": "no", "MaximumRetryCount": 0}
                 and all(hc.get(k) in (None, []) for k in ("Binds", "VolumesFrom", "Devices", "DeviceRequests", "DeviceCgroupRules"))
                 and hc.get("Tmpfs") in (None, {}) and obj.get("Config", {}).get("Volumes") in (None, {})
                 and all(hc.get(k) == "" for k in ("PidMode", "UTSMode", "UsernsMode"))
                 and hc.get("PublishAllPorts") is False and hc.get("PortBindings") in (None, {}), "mount probe isolation differs")
            reqs, effs = hc.get("Mounts"), obj.get("Mounts")
            need(isinstance(reqs, list) and isinstance(effs, list) and len(reqs) == len(effs) == 2 * len(pairs), "mount probe membership differs")
            for i, target in enumerate(targets):
                req = [r for r in reqs if r.get("Target") == target]
                eff = [r for r in effs if r.get("Destination") == target]
                pair = pairs[i // 2]; ss = {pair["original_source"], pair["clone_source"]}
                need(len(req) == len(eff) == 1 and set(req[0]) == {"Type", "Source", "Target", "ReadOnly", "BindOptions"}
                     and req[0]["Type"] == "bind" and req[0]["ReadOnly"] is True and req[0]["Source"] in ss
                     and bind_options(req[0]["BindOptions"])
                     and set(eff[0]) == {"Type", "Source", "Destination", "Mode", "RW", "Propagation"}
                     and eff[0]["Type"] == "bind" and eff[0]["Source"] in ss and eff[0]["RW"] is False
                     and eff[0]["Mode"] in ("", "ro") and eff[0]["Propagation"] == "rprivate", "mount probe row differs")
        labels = created.get("Config", {}).get("Labels", {})
        need(created.get("Name") == f"/{context['run_id']}-{service}-bind-proof"
             and labels.get("com.docker.compose.project") == context["run_id"]
             and labels.get("com.docker.compose.service") == service + "-bind-proof"
             and labels.get("com.docker.compose.oneoff") == "True"
             and labels.get("lantern.owner") == "compose-bind-identity-probe"
             and isinstance(labels.get("lantern.initializer"), str)
             and re.fullmatch(r"lantern-owned-init\.[A-Za-z0-9]+", labels["lantern.initializer"])
             and type(created.get("RestartCount")) is int and created["RestartCount"] == 0
             and type(completed.get("RestartCount")) is int and completed["RestartCount"] == 0
             and created["State"].get("StartedAt") in ("", "0001-01-01T00:00:00Z"), "mount probe ownership differs")
        need(probe_fingerprint(created) == probe_fingerprint(completed) and type(completed["State"].get("ExitCode")) is int and completed["State"].get("ExitCode") == 0
             and completed["State"].get("OOMKilled") is False and raw(probe["wait_file"], False) == b"0\n"
             and raw(probe["remove_file"], False) == (pcid + "\n").encode(), "mount probe closure differs")
        output = raw(probe["output_file"], False)
        need(len(output) <= 4096, "mount stat output oversized")
        rows = output.decode().splitlines(keepends=True)
        need(len(rows) == 2 * len(pairs) and all(re.fullmatch(r"[1-9][0-9]*:[1-9][0-9]*:directory\n", x) for x in rows)
             and all(rows[i] == rows[i+1] for i in range(0, len(rows), 2)), "mount same-directory stat differs")
    clone_before, clone_after = observation(binding["clone_before_file"]), observation(binding["clone_after_file"])
    clone_id = clone_before.get("Id")
    need(isinstance(clone_id, str) and HEX64.fullmatch(clone_id) and clone_id != cid, "mount clone identity differs")
    profile(clone_before, clone_id, "created"); profile(clone_after, clone_id, "exited")
    labels = clone_before["Config"]["Labels"]
    need(clone_before["Name"] == f"/{context['run_id']}-{service}-security-init"
         and labels["com.docker.compose.oneoff"] == "True" and labels["lantern.owner"] == "compose-security-initializer"
         and re.fullmatch(r"lantern-owned-init\.[A-Za-z0-9]+", labels["lantern.initializer"]), "mount clone ownership differs")
    need(fingerprint(clone_before) == fingerprint(clone_after), "mount clone profile changed")
    for key in ("User", "WorkingDir"):
        need(clone_before["Config"].get(key) == original["Config"].get(key), "mount initializer config differs")
    need(clone_before["Config"].get("Entrypoint") == ["/app/lantern"] and clone_before["Config"].get("Cmd") == ["init-security"]
         and clone_before["HostConfig"].get("NetworkMode") == original["HostConfig"].get("NetworkMode"), "mount initializer command/network differs")
    original_hc, clone_hc = original["HostConfig"], clone_before["HostConfig"]
    need(set(original_hc) == set(clone_hc) and all(original_hc[k] == clone_hc[k] for k in original_hc if k not in ("Binds", "Mounts")), "mount clone hardening changed")
    for left, right, key in ((original_hc["Mounts"], clone_hc["Mounts"], "Target"), (original["Mounts"], clone_before["Mounts"], "Destination")):
        need(len(left) == len(right), "mount clone membership changed")
        for a, b in zip(sorted(left, key=lambda r: r[key]), sorted(right, key=lambda r: r[key])):
            need(a[key] == b[key], "mount clone target changed")
            if a[key] in ("/run/lantern-bench-heap", "/run/lantern-config", "/etc/ntp.conf"):
                need({k: v for k, v in a.items() if k != "Source"} == {k: v for k, v in b.items() if k != "Source"}, "mount clone non-Source row changed")
            else:
                need(a == b, "mount clone volume changed")
    ca, cb = mounts(clone_before, allowed)
    need({k: v for k, v in ca.items() if k != "Source"} == {k: v for k, v in original_request.items() if k != "Source"}
         and {k: v for k, v in cb.items() if k != "Source"} == {k: v for k, v in original_effective.items() if k != "Source"}, "mount clone options changed")
    cut_process = None
    for position in ("before", "after"):
        snapshot_ref = replica.get(f"mount_inspect_{position}_file")
        need(isinstance(snapshot_ref, dict) and snapshot_ref.get("path") == str(base / f"{phase}.{position}.json"), "mount snapshot filename differs")
        snap = raw(snapshot_ref)
        need(isinstance(snap, dict) and set(snap) == {"schema", "protocol", "phase", "position", "service", "host_identity", "inspect_file", "daemon_file"}
             and type(snap["schema"]) is int and snap["schema"] == 1 and snap["protocol"] == PROTOCOL
             and snap["phase"] == phase and snap["position"] == position and snap["service"] == service
             and host(snap["host_identity"]) == identity and raw(snap["daemon_file"], False) == daemon,
             "mount cut snapshot differs")
        need(snap["inspect_file"].get("path") == str(base / f"{phase}.{position}.inspect.json")
             and snap["daemon_file"].get("path") == str(base / f"{phase}.{position}.daemon"), "mount snapshot raw filename differs")
        observed = profile(observation(snap["inspect_file"]), cid, "running")
        same_serving_profile(original, observed)
        mounts(observed, allowed)
        process = (observed["State"]["Pid"], observed["State"]["StartedAt"])
        need(integer(process[0], 1) and process[1] not in ("", "0001-01-01T00:00:00Z")
             and (cut_process is None or cut_process == process), "mount process changed within cut")
        cut_process = process
    parent = base.lstat()
    need(stat.S_ISDIR(parent.st_mode) and parent.st_uid == os.getuid() and stat.S_IMODE(parent.st_mode) == 0o700, "mount proof directory is not private")
    files = list(base.iterdir())
    need(len(files) <= 32, "mount proof file count exceeded")
    total = 0
    for path in files:
        info = path.lstat()
        need(stat.S_ISREG(info.st_mode) and info.st_uid == os.getuid() and info.st_nlink == 1
             and stat.S_IMODE(info.st_mode) == 0o600 and info.st_size <= 128 << 10, "mount proof member unsafe")
        total += info.st_size
    need(total <= 2 << 20, "mount proof total exceeded")
    return binding, identity


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
    mount_proofs = [mount_binding(root, row, phase, context, index) for index, row in enumerate(replicas)]
    need(len({b["daemon_id"] for b, _ in mount_proofs}) == 1
         and len({(h["device"], h["inode"]) for _, h in mount_proofs}) == 3, "mount cohort daemon/directory identity differs")
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


def normal_observations(root, report, operative=False):
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
        if not operative:
            need(max(peaks_goroutines[slot], post["goroutines"]) - pre["goroutines"] <= 15,
                 "normal goroutine growth exceeds 15")
        need(peaks_heap[slot] - pre["heap_alloc_bytes"] <= 40 * MIB,
             "normal natural steady growth exceeds 40 MiB")
        observed_post_failure |= post["heap_alloc_bytes"] - pre["heap_alloc_bytes"] > 32 * MIB
    need(report.get("natural_post_heap_observation_verdict") == ("fail" if observed_post_failure else "pass"),
         "normal post observation was reclassified")


# The old NORMAL_PROTOCOL reader remains an immutable interpretation of saved
# evidence. Only fresh normal runs use this independently recomputed component.
OPERATIVE_NORMAL_PROTOCOL = "natural_reference_v2"
GOROUTINE_PROTOCOL = "receipt_goroutine_operational_v1"
RECEIPT_FAMILIES = {
    "receipt_vertex_put_admission_lookup": ("receipt_vertex_put", "PutVertex"),
    "receipt_vertex_delete_admission_lookup": ("receipt_vertex_delete", "DeleteVertex"),
    "receipt_admission_lookup": ("receipt_edge_delete", "DeleteEdge"),
    "receipt_edge_add_admission_lookup": ("receipt_edge_add", "AddEdge"),
}


def _policy_keys(value, required, optional=()):
    need(type(value) is dict and set(required) <= set(value)
         and set(value) <= set(required) | set(optional), "goroutine policy JSON shape differs")


def _policy_equal(left, right):
    # In particular, JSON false is not an integer zero and aliases cannot
    # silently stand in for a canonical field.
    return json.dumps(left, sort_keys=True, separators=(",", ":"), allow_nan=False) == json.dumps(
        right, sort_keys=True, separators=(",", ":"), allow_nan=False)


def _policy_ref(root, ref, *, private=True, raw=False, external=False, name=None):
    _policy_keys(ref, ("path", "sha256"))
    need(isinstance(ref["path"], str) and isinstance(ref["sha256"], str)
         and HEX64.fullmatch(ref["sha256"]), "invalid goroutine reference")
    path = Path(ref["path"])
    need(path.is_absolute() and str(path) == ref["path"] and ".." not in path.parts
         and (external or path.parent == root) and (name is None or path.name == name),
         "goroutine reference is outside the physical current case root")
    value, digest = read(path.parent if external else root, path, 1 << 20,
                         decode_json=not raw, modes=(0o600,) if private else (0o600, 0o644))
    need(digest == ref["sha256"], "goroutine raw evidence hash differs")
    return value


def _policy_file(root, name, *, private=True, raw=False):
    return read(root, name, 1 << 20, decode_json=not raw,
                modes=(0o600,) if private else (0o600, 0o644))


def _policy_scenario(scenario):
    _policy_keys(scenario, ("name", "description", "target", "cluster", "phases", "perf_gate",
                            "leak_gate", "goroutine_policy"))
    need(scenario["name"] in RECEIPT_FAMILIES and isinstance(scenario["description"], str),
         "goroutine scenario/family is not declared")
    family, method = RECEIPT_FAMILIES[scenario["name"]]
    expected = {
        "target": {"driver": family, "endpoints": ["localhost:6380", "localhost:6381", "localhost:6382"],
                   "calls": [{"name": "receipt_admission", "call": "graph.v1.LanternService/" + method, "rps": 100},
                             {"name": "receipt_lookup", "call": "graph.v1.LanternService/GetReceiptStatus", "rps": 100}]},
        "cluster": {"receipt_wal": {"enabled": True, "retention": "1h", "max_entries": 8000, "max_bytes": 16777216}},
        "phases": {"warmup": {"duration": "10s", "rps": 200, "concurrency": 12},
                   "steady": {"duration": "45s", "rps": 200, "concurrency": 12}, "cooldown": "10s"},
        "perf_gate": {"min_steady_rps_total": 150, "max_p99_ms": 500, "max_non_ok_ratio": 0,
                      "producers": {"receipt_admission": {"min_steady_rps": 75, "max_p99_ms": 500, "max_non_ok_ratio": 0},
                                    "receipt_lookup": {"min_steady_rps": 75, "max_p99_ms": 200, "max_non_ok_ratio": 0}}},
        "leak_gate": {"goroutine_max_delta": 15, "heap_alloc_max_delta_mb": 32,
                      "steady_heap_alloc_max_delta_mb": 40, "steady_sample_interval": "5s"},
        "goroutine_policy": {"protocol": GOROUTINE_PROTOCOL, "sampled_absolute_max": 128, "post_max_delta": 15,
                             "post_offsets_seconds": [5, 10], "capture_window_seconds": 3},
    }
    need(all(_policy_equal(scenario[key], value) for key, value in expected.items()),
         "goroutine reviewed workload/resource profile differs")
    return family


def _policy_time(value):
    need(isinstance(value, str) and re.fullmatch(
        r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?(?:Z|[+-]\d\d:\d\d)", value),
        "goroutine process/capture clock identity missing")
    datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))


def _policy_profile(value, baseline, expected, scenario, scenario_sha):
    keys = ("schema", "protocol", "scenario", "family", "image_scope", "measurement_protocol",
            "source_commit", "source_tree", "image_id", "config_sha256", "scenario_sha256",
            "observer_sha256", "observer_go_version", "metrics_transport", "bridge_pid", "bridge_ports",
            "cpuset", "runtime_user", "nodes")
    _policy_keys(value, keys)
    need(type(value["schema"]) is int and value["schema"] == 1 and value["protocol"] == GOROUTINE_PROTOCOL
         and value["scenario"] == scenario["name"] and value["family"] == RECEIPT_FAMILIES[scenario["name"]][0]
         and value["image_scope"] == "normal" and value["measurement_protocol"] == "none"
         and value["source_commit"] == expected["source_commit"] and value["source_tree"] == expected["source_tree"]
         and HEX40.fullmatch(value["source_commit"]) and HEX40.fullmatch(value["source_tree"])
         and IMAGE.fullmatch(value["image_id"]) and value["image_id"] == expected["normal_image_id"]
         and value["cpuset"] == expected["cpuset"] == "0-3"
         and value["runtime_user"] == expected["runtime_user"]
         and isinstance(value["runtime_user"], str) and re.fullmatch(r"[1-9][0-9]*:[0-9]+", value["runtime_user"])
         and value["scenario_sha256"] == scenario_sha
         and all(isinstance(value[k], str) and HEX64.fullmatch(value[k])
                 for k in ("config_sha256", "scenario_sha256", "observer_sha256"))
         and isinstance(value["observer_go_version"], str) and value["observer_go_version"].startswith("go1.")
         and value["metrics_transport"] == "operator_docker_exec" and integer(value["bridge_pid"], 1)
         and value["bridge_ports"] == [9390, 9391, 9392]
         and all(type(x) is int for x in value["bridge_ports"])
         and isinstance(value["nodes"], list) and len(value["nodes"]) == 3,
         "goroutine source/image/observer/native profile differs")
    for i, node in enumerate(value["nodes"]):
        _policy_keys(node, ("endpoint", "container_id", "pid", "started_at", "expected_peers") +
                     (("passive_metrics",) if baseline else ()))
        need(node["endpoint"] == ENDPOINTS[i] and isinstance(node["container_id"], str)
             and HEX64.fullmatch(node["container_id"]) and integer(node["pid"], 1)
             and isinstance(node["expected_peers"], list) and len(node["expected_peers"]) == 2
             and len(set(node["expected_peers"])) == 2
             and all(isinstance(x, str) and 0 < len(x) <= 512 and not any(c in x for c in "\r\n\x00")
                     for x in node["expected_peers"]), "goroutine three-node/peer identity incomplete")
        _policy_time(node["started_at"])
    need(len({n["container_id"] for n in value["nodes"]}) == 3
         and len({n["pid"] for n in value["nodes"]}) == 3, "goroutine duplicate target identity")
    stripped = json.loads(json.dumps(value))
    for node in stripped["nodes"]:
        node.pop("passive_metrics", None)
    return stripped


def _policy_passive(value, node):
    _policy_keys(value, ("subscribe_active", "changes_active", "anti_entropy_cycles", "peers"))
    need(integer(value["subscribe_active"]) and value["subscribe_active"] == 2
         and integer(value["changes_active"]) and value["changes_active"] == 0
         and integer(value["anti_entropy_cycles"]) and type(value["peers"]) is dict
         and set(value["peers"]) == set(node["expected_peers"])
         and all(type(x) is int and x == 1 for x in value["peers"].values()),
         "goroutine passive peer/stream profile differs")


def _policy_passive_text(raw, node):
    selected, peers = {}, {}
    names = {"lantern_subscribe_active_streams": "subscribe_active",
             "lantern_changes_active_streams": "changes_active",
             "lantern_anti_entropy_cycles_total": "anti_entropy_cycles"}
    for line in raw.decode().splitlines():
        need(len(line) <= 64 << 10, "passive metric line bound")
        fields = line.split()
        if not fields or fields[0].startswith("#"):
            continue
        name = fields[0]
        if name not in names and not name.startswith("lantern_peer_connected"):
            continue
        need(len(fields) == 2, "passive metric syntax differs")
        number = decimal.Decimal(fields[1])
        need(number.is_finite() and 0 <= number <= 9223372036854775807 and number == number.to_integral_value(),
             "passive metric is not a nonnegative integer")
        if name in names:
            key = names[name]
            need(key not in selected, "duplicate passive metric")
            selected[key] = int(number)
        else:
            match = re.fullmatch(r'lantern_peer_connected\{peer=("(?:[^"\\]|\\.)*")\}', name)
            need(match is not None, "passive peer label differs")
            peer = json.loads(match[1])
            need(peer not in peers, "duplicate passive peer")
            peers[peer] = int(number)
    selected["peers"] = peers
    _policy_passive(selected, node)
    return selected


def _validate_normal_policy(root, scenario, expected, report):
    root = Path(root).absolute()
    fd = directory(root)
    os.close(fd)
    family = _policy_scenario(scenario)
    need(report.get("snapshot_protocol") == OPERATIVE_NORMAL_PROTOCOL and report.get("verdict") == "pass"
         and report.get("natural_steady_memory_verdict") == "pass" and report.get("goroutine_checks_verdict") == "pass"
         and type(report.get("goroutine_sampled_peak_limit")) is int and report["goroutine_sampled_peak_limit"] == 128
         and type(report.get("goroutine_post_residual_limit")) is int and report["goroutine_post_residual_limit"] == 15,
         "normal operative component verdict incomplete")
    need(_policy_equal(report.get("thresholds"), {"goroutine_max_delta": 15, "heap_alloc_max_delta_mb": 32,
                         "steady_heap_alloc_max_delta_mb": 40}), "normal report thresholds differ")
    normal_observations(root, report, operative=True)
    resolved, scenario_sha = _policy_file(root, "scenario.resolved.json", private=False)
    need(_policy_equal(resolved, scenario), "resolved goroutine scenario differs")
    input_ref = report.get("goroutine_policy_input")
    data = _policy_ref(root, input_ref, name="goroutine_policy_input.json")
    _policy_keys(data, ("schema", "protocol", "pre", "steady", "legacy_post", "baseline_profile", "before_post_profile",
                        "barrier", "posts", "workload_report", "owner_evidence"))
    need(type(data["schema"]) is int and data["schema"] == 1 and data["protocol"] == GOROUTINE_PROTOCOL,
         "goroutine input protocol missing")
    baseline = _policy_ref(root, data["baseline_profile"], name="goroutine_policy_baseline_profile.json")
    identity = _policy_profile(baseline, True, expected, scenario, scenario_sha)
    before = _policy_ref(root, data["before_post_profile"], name="goroutine_policy_before_post_profile.json")
    need(_policy_profile(before, False, expected, scenario, scenario_sha) == identity,
         "goroutine before-post process/config profile changed")
    observer, _ = _policy_file(root, "goroutine_policy_observer.json")
    _policy_keys(observer, ("source_commit", "source_tree", "observer_sha256", "observer_go_version", "buildinfo_sha256"))
    version, _ = _policy_file(root, "goroutine_policy_go_version.txt", private=False, raw=True)
    _, build_sha = _policy_file(root, "goroutine_policy_buildinfo.txt", private=False, raw=True)
    need(observer["source_commit"] == expected["source_commit"] and observer["source_tree"] == expected["source_tree"]
         and observer["observer_sha256"] == baseline["observer_sha256"]
         and observer["observer_go_version"] == baseline["observer_go_version"] == version.decode().split()[2]
         and observer["buildinfo_sha256"] == build_sha, "prebuilt goroutine observer evidence differs")
    source_names = ("server/internal/security/control_renewal_network_test.go",
                    "server/service/replication_delivery_test.go", "server/replication/pump_test.go")
    need(isinstance(data["owner_evidence"], list) and len(data["owner_evidence"]) == 3,
         "source-lifecycle reference scope missing")
    for ref, suffix in zip(data["owner_evidence"], source_names):
        need(isinstance(ref.get("path"), str) and ref["path"].endswith("/" + suffix),
             "source-test reference is outside declared lifecycle scope")
        _policy_ref(root, ref, private=False, raw=True, external=True)
    workload = _policy_ref(root, data["workload_report"], private=False, name="workload_gate.json")
    need(workload.get("verdict") == "pass", "normal workload did not pass")
    barrier = _policy_ref(root, data["barrier"], name="goroutine_policy_barrier.json")
    _policy_keys(barrier, ("schema", "protocol", "scope", "t0_ns", "semantic_complete", "pending_rpc", "owned_work", "work_reports"))
    need(type(barrier["schema"]) is int and barrier["schema"] == 1 and barrier["protocol"] == GOROUTINE_PROTOCOL
         and barrier["scope"] == "declared_client_work_only_not_global_idle" and integer(barrier["t0_ns"], 1)
         and barrier["semantic_complete"] is True and type(barrier["pending_rpc"]) is int and barrier["pending_rpc"] == 0,
         "declared client work barrier incomplete")
    need(isinstance(barrier["owned_work"], list) and len(barrier["owned_work"]) == 3, "owned work inventory incomplete")
    owned = {}
    for row in barrier["owned_work"]:
        _policy_keys(row, ("kind", "declared", "reaped", "pending_rpc", "exit_failures"))
        need(row["kind"] in ("producer", "subscriber", "chaos") and row["kind"] not in owned
             and all(integer(row[k]) for k in ("declared", "reaped", "pending_rpc", "exit_failures"))
             and row["declared"] == row["reaped"] and row["pending_rpc"] == row["exit_failures"] == 0,
             "declared client work did not join")
        owned[row["kind"]] = row["declared"]
    need(owned == {"producer": 2, "subscriber": 0, "chaos": 0}
         and isinstance(barrier["work_reports"], list) and len(barrier["work_reports"]) == 2,
         "declared client work profile differs")
    phases = {}
    for row in barrier["work_reports"]:
        _policy_keys(row, ("phase", "admission", "lookup", "coordinator_return"))
        phase = row["phase"]
        need(phase in ("warmup", "steady") and phase not in phases, "duplicate or missing client phase")
        returned = _policy_ref(root, row["coordinator_return"], name=f"goroutine_policy_phase_{phase}_return.json")
        _policy_keys(returned, ("schema", "protocol", "phase", "scope", "started_ns", "finished_ns", "exit_code", "coordinator_returned"))
        need(type(returned["schema"]) is int and returned["schema"] == 1 and returned["protocol"] == GOROUTINE_PROTOCOL
             and returned["phase"] == phase and returned["scope"] == "synchronous_receipt_phase_only"
             and integer(returned["started_ns"], 1) and integer(returned["finished_ns"], 1)
             and returned["started_ns"] < returned["finished_ns"] <= barrier["t0_ns"]
             and type(returned["exit_code"]) is int and returned["exit_code"] == 0
             and returned["coordinator_returned"] is True, "phase coordinator return incomplete")
        a = _policy_ref(root, row["admission"], private=False, name=f"ghz_{phase}_0_receipt_admission.json")
        b = _policy_ref(root, row["lookup"], private=False, name=f"ghz_{phase}_1_receipt_lookup.json")
        need(integer(a.get("count"), 1) and integer(b.get("count"), 1) and a["count"] == b.get("count")
             and _policy_equal(a.get("statusCodeDistribution"), {"OK": a["count"]})
             and _policy_equal(b.get("statusCodeDistribution"), {"OK": a["count"]}),
             "raw admission/lookup work incomplete")
        phases[phase] = returned
    need(phases["warmup"]["finished_ns"] <= phases["steady"]["started_ns"], "client phase return order differs")
    pre = _policy_ref(root, data["pre"], private=False, name="runtime_pre.json")
    steady = _policy_ref(root, data["steady"], private=False, name="runtime_steady.json")
    need(integer(steady.get("duration_ms"), 45000) and steady["duration_ms"] <= 55000,
         "steady sampling duration outside the existing coverage bound")
    post = _policy_ref(root, data["legacy_post"], private=False, name="runtime_post.json")
    peaks, bases = [], []
    passive = []
    for i, node in enumerate(baseline["nodes"]):
        need(pre[i]["endpoint"] == post[i]["endpoint"] == node["endpoint"]
             and integer(pre[i].get("goroutines"), 1) and integer(post[i].get("goroutines"), 1),
             "original PRE/POST target differs")
        bases.append(pre[i]["goroutines"])
        peaks.append(max(pre[i]["goroutines"], post[i]["goroutines"],
                         *(x["replicas"][i]["goroutines"] for x in steady["samples"])))
        need(post[i]["goroutines"] - bases[i] <= 15, "original POST goroutine residual exceeds 15")
        raw = _policy_ref(root, node["passive_metrics"], raw=True, name=f"goroutine_policy_pre_{9390+i}.prom")
        passive.append(_policy_passive_text(raw, node))
    need(isinstance(data["posts"], list) and len(data["posts"]) == 2, "fixed goroutine post observations missing")
    residuals = []
    cycles = [value["anti_entropy_cycles"] for value in passive]
    for i, row in enumerate(data["posts"]):
        offset = 5 + i * 5
        _policy_keys(row, ("offset_seconds", "start_ns", "end_ns", "capture", "profile"))
        need(type(row["offset_seconds"]) is int and row["offset_seconds"] == offset
             and integer(row["start_ns"]) and integer(row["end_ns"])
             and barrier["t0_ns"] + offset * 10**9 <= row["start_ns"] <= row["end_ns"]
             and row["end_ns"] <= barrier["t0_ns"] + (offset + 3) * 10**9,
             "fixed goroutine window late, duplicate or unordered")
        timing, _ = _policy_file(root, f"goroutine_policy_timing_{offset}.json")
        need(_policy_equal(timing, {k: row[k] for k in ("offset_seconds", "start_ns", "end_ns")}),
             "goroutine host call bracket differs")
        control, _ = _policy_file(root, f"goroutine_policy_control_{offset}.json")
        need(_policy_equal(control, {"schema": 1, "protocol": GOROUTINE_PROTOCOL, "offset_seconds": offset,
             "metrics_urls": ["http://" + x + "/metrics" for x in ENDPOINTS], "operator_loopback": True,
             "profile": data["before_post_profile"]}), "goroutine collector route/control differs")
        profile = _policy_ref(root, row["profile"], name=f"goroutine_policy_post_{offset}_profile.json")
        need(_policy_profile(profile, False, expected, scenario, scenario_sha) == identity,
             "goroutine target restarted or profile changed")
        capture = _policy_ref(root, row["capture"], name=f"goroutine_policy_post_{offset}.json")
        _policy_keys(capture, ("schema", "protocol", "policy_role", "qualification", "release_admission", "offset_seconds",
                              "profile", "started_utc", "duration_ns", "replicas", "verdict"), ("failure",))
        need(type(capture["schema"]) is int and capture["schema"] == 1 and capture["protocol"] == GOROUTINE_PROTOCOL
             and capture["policy_role"] == "operative_normal_component" and capture["qualification"] is False
             and capture["release_admission"] is False and type(capture["offset_seconds"]) is int
             and capture["offset_seconds"] == offset and capture["profile"] == data["before_post_profile"]
             and capture["verdict"] == "captured" and not capture.get("failure")
             and integer(capture["duration_ns"]) and capture["duration_ns"] <= min(3 * 10**9, row["end_ns"] - row["start_ns"])
             and isinstance(capture["replicas"], list) and len(capture["replicas"]) == 3,
             "goroutine collector flight incomplete or foreign")
        _policy_time(capture["started_utc"])
        values = []
        for slot, replica in enumerate(capture["replicas"]):
            _policy_keys(replica, ("runtime", "passive"))
            runtime = replica["runtime"]
            _policy_keys(runtime, ("endpoint", "goroutines", "heap_alloc_bytes"), ("metrics",))
            need(runtime["endpoint"] == ENDPOINTS[slot] and integer(runtime["goroutines"], 1)
                 and integer(runtime["heap_alloc_bytes"], 1), "goroutine post target/runtime differs")
            _policy_passive(replica["passive"], baseline["nodes"][slot])
            need(replica["passive"]["anti_entropy_cycles"] >= cycles[slot],
                 "passive cycle counter rolled back")
            cycles[slot] = replica["passive"]["anti_entropy_cycles"]
            peaks[slot] = max(peaks[slot], runtime["goroutines"])
            values.append(runtime["goroutines"] - bases[slot])
            need(values[-1] <= 15, "fixed POST goroutine residual exceeds 15")
        residuals.append(values)
    need(all(x <= 128 for x in peaks), "normal sampled goroutines exceed absolute 128")
    for suffix in ("pre", "post"):
        provenance, _ = _policy_file(root, f"image_provenance_{suffix}.json", private=False)
        need(provenance.get("source_commit") == expected["source_commit"]
             and provenance.get("image_id") == expected["normal_image_id"]
             and provenance.get("runtime_user") == expected["runtime_user"], "normal provenance differs")
        rows = {x["service"]: x for x in provenance["replicas"]}
        need(len(provenance["replicas"]) == 3 and len(rows) == 3 and all(rows[f"lantern-{i}"]["container_id"] == node["container_id"]
             and rows[f"lantern-{i}"]["image_id"] == baseline["image_id"]
             and rows[f"lantern-{i}"]["runtime_user"] == baseline["runtime_user"]
             and rows[f"lantern-{i}"]["cpuset"] == baseline["cpuset"]
             for i, node in enumerate(baseline["nodes"])), "normal profile/provenance container differs")
    return {"protocol": GOROUTINE_PROTOCOL, "verdict": "pass", "sampled_peak_limit": 128, "post_residual_limit": 15,
            "sampled_peaks": peaks, "original_post_deltas": [post[i]["goroutines"] - bases[i] for i in range(3)],
            "fixed_post_deltas": residuals, "goroutine_policy_input": input_ref,
            "scope": "normal sampled ceiling and declared-client residual screen; source-test refs are not runtime joins"}


def validate_normal_policy(root, scenario, expected, report):
    """Pure independent raw validation; a sidecar PASS is never sufficient."""
    try:
        return _validate_normal_policy(root, scenario, expected, report)
    except Incomplete:
        raise
    except (OSError, KeyError, TypeError, AttributeError, ValueError, IndexError, UnicodeError, decimal.InvalidOperation) as error:
        raise Incomplete("missing or malformed operative goroutine evidence") from error


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
    need(normal.get("snapshot_protocol") == OPERATIVE_NORMAL_PROTOCOL and normal.get("verdict") == "pass"
         and normal.get("retained_verdict") == "requires_pair"
         and normal.get("pair_required") is True and normal.get("overall_memory_verdict") == "requires_pair",
         "normal lane cannot independently qualify retained memory")
    normal_policy = validate_normal_policy(normal_root, scenario, expected, normal)
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
        need(pre["mount_binding_file"] == post["mount_binding_file"], "mount binding changed between cuts")
        snapshots = [reference(retained_root, row[key], 128 << 10)
                     for row in (pre, post) for key in ("mount_inspect_before_file", "mount_inspect_after_file")]
        processes = [reference(retained_root, x["inspect_file"], 128 << 10)["state"] for x in snapshots]
        need(len({(x["pid"], x["started_at"]) for x in processes}) == 1, "mount process changed between cuts")
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
    return {"schema": 1, "protocol": "source_paired_retained_v2", "verdict": "pass",
            "scope": "normal-image checks plus same-source tagged retained measurement; no binary equivalence",
            "source_commit": expected["source_commit"], "source_tree": expected["source_tree"],
            "scenario_sha256": canonical_sha(scenario), "normal_image_id": expected["normal_image_id"],
            "retained_image_id": expected["retained_image_id"], "goroutine_policy": normal_policy, "replicas": deltas}


def validate_pair(normal_root, retained_root, scenario, expected):
    try:
        return _validate_pair(normal_root, retained_root, scenario, expected)
    except Incomplete:
        raise
    except (OSError, KeyError, TypeError, AttributeError, ValueError) as error:
        raise Incomplete("missing or malformed paired evidence") from error
