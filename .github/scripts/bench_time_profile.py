"""Read-only, case-local verification of the controlled UDP benchmark source.

The ordinary Server image and native time construction remain unchanged. This
receipt proves the selected local test transport, never the external upstream.
No Docker, network, clock mutation or workload is performed by this reader.
"""

import datetime
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import stat


PROFILE = "bench_local_udp_v1"
HOST = "time.asia.apple.com"
HEX40 = re.compile(r"[0-9a-f]{40}\Z")
HEX64 = re.compile(r"[0-9a-f]{64}\Z")
IMAGE = re.compile(r"sha256:[0-9a-f]{64}\Z")
SERVICES = tuple(f"lantern-{i}" for i in range(3))
SCENARIOS = {"broad_rw", "broad_illuminate", "broad_mutate", "edge_contrib_idempotent", "ttl_churn",
             "many_subscribers", "search_churn", "replication_apply_churn", "receipt_vertex_put_admission_lookup",
             "receipt_vertex_delete_admission_lookup", "receipt_admission_lookup", "receipt_edge_add_admission_lookup"}


class Incomplete(ValueError):
    pass


def need(condition, message):
    if not condition:
        raise Incomplete(message)


def integer(value, minimum=0):
    return type(value) is int and value >= minimum


def keys(value, expected):
    need(type(value) is dict and set(value) == set(expected), "time source schema differs")


def _object(pairs):
    result = {}
    for name, value in pairs:
        need(name not in result, "duplicate time source JSON key")
        result[name] = value
    return result


def _identity(info):
    return (info.st_dev, info.st_ino, info.st_mode, info.st_uid, info.st_gid,
            info.st_nlink, info.st_size, info.st_mtime_ns, info.st_ctime_ns)


def _directory(path):
    path = Path(path)
    need(path.is_absolute() and ".." not in path.parts, "physical time source directory required")
    fd = os.open("/", os.O_RDONLY | os.O_DIRECTORY)
    try:
        for part in path.parts[1:]:
            child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
            os.close(fd)
            fd = child
        return fd
    except BaseException:
        os.close(fd)
        raise


def read(root, path, limit=128 << 10, decode=True, modes=(0o600,)):
    root, path = Path(root).absolute(), Path(path)
    if not path.is_absolute():
        path = root / path
    need(".." not in path.parts and path.is_relative_to(root), "time source path escapes case")
    parent = _directory(path.parent)
    try:
        before = os.stat(path.name, dir_fd=parent, follow_symlinks=False)
        need(stat.S_ISREG(before.st_mode) and before.st_nlink == 1
             and before.st_uid == os.getuid() and stat.S_IMODE(before.st_mode) in modes
             and 0 < before.st_size <= limit, "unsafe time source evidence file")
        fd = os.open(path.name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=parent)
        with os.fdopen(fd, "rb") as stream:
            opened = os.fstat(stream.fileno())
            need(_identity(opened) == _identity(before), "time source file replaced before read")
            raw = stream.read(limit + 1)
            need(len(raw) == before.st_size and _identity(os.fstat(stream.fileno())) == _identity(before)
                 and _identity(os.stat(path.name, dir_fd=parent, follow_symlinks=False)) == _identity(before),
                 "time source file changed while reading")
        again = _directory(path.parent)
        try:
            need(os.path.samestat(os.fstat(parent), os.fstat(again)), "time source directory replaced")
        finally:
            os.close(again)
    finally:
        os.close(parent)
    digest = hashlib.sha256(raw).hexdigest()
    if not decode:
        return raw, digest
    try:
        value = json.loads(raw, object_pairs_hook=_object,
                           parse_constant=lambda _: (_ for _ in ()).throw(Incomplete("nonfinite time source JSON")))
    except (UnicodeError, json.JSONDecodeError) as error:
        raise Incomplete("invalid time source JSON") from error
    return value, digest


def reference(root, value, path, limit=128 << 10, decode=True, modes=(0o600,)):
    keys(value, ("path", "sha256"))
    need(type(value["path"]) is str and value["path"] == str(Path(root).absolute() / path)
         and type(value["sha256"]) is str and HEX64.fullmatch(value["sha256"]),
         "time source raw reference differs")
    result, digest = read(root, value["path"], limit, decode, modes)
    need(digest == value["sha256"], "time source raw hash differs")
    return result


def timestamp(value):
    need(type(value) is str and re.fullmatch(
        r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?(?:Z|[+-]\d\d:\d\d)", value),
        "time source timestamp missing")
    return datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))


def _status(value):
    keys(value, ("schema", "protocol", "pid", "start_nonce", "clock_kind", "seed_utc",
                 "elapsed_ns", "requests", "responses", "invalid", "clock_failure"))
    need(type(value["schema"]) is int and value["schema"] == 1
         and value["protocol"] == "lantern_bench_ntp_fixture_v1" and value["pid"] == 1
         and type(value["pid"]) is int and type(value["start_nonce"]) is str
         and HEX64.fullmatch(value["start_nonce"]) and value["clock_kind"] == "seed_utc_plus_monotonic"
         and all(integer(value[k]) and value[k] < 2**64 for k in
                 ("elapsed_ns", "requests", "responses", "invalid", "clock_failure"))
         and value["requests"] >= value["responses"] > 0
         and value["invalid"] == value["clock_failure"] == 0,
         "time fixture health or counters differ")
    seed = timestamp(value["seed_utc"])
    need(datetime.datetime(2020, 1, 1, tzinfo=datetime.timezone.utc) <= seed <
         datetime.datetime(2080, 1, 1, tzinfo=datetime.timezone.utc), "time fixture UTC seed outside admitted era window")
    return value


def _inspect(value):
    keys(value, ("Id", "Name", "Image", "Config", "State", "HostConfig", "Mounts", "NetworkSettings", "RestartCount"))
    keys(value["Config"], ("Image", "User", "Entrypoint", "Cmd", "Labels"))
    keys(value["State"], ("Status", "Running", "Pid", "StartedAt", "OOMKilled", "ExitCode"))
    keys(value["HostConfig"], ("NetworkMode", "ExtraHosts", "ReadonlyRootfs", "CapDrop", "SecurityOpt",
                              "Sysctls", "CpusetCpus", "NanoCpus", "Memory", "PidsLimit", "PortBindings", "Mounts", "Binds",
                              "Privileged", "AutoRemove", "PidMode", "IpcMode", "UTSMode", "UsernsMode", "RestartPolicy",
                              "CapAdd", "Devices", "DeviceRequests", "DeviceCgroupRules", "VolumesFrom", "Tmpfs", "PublishAllPorts"))
    keys(value["NetworkSettings"], ("Networks", "Ports"))
    need(type(value["Id"]) is str and HEX64.fullmatch(value["Id"])
         and type(value["Image"]) is str and IMAGE.fullmatch(value["Image"])
         and type(value["Name"]) is str and value["Name"].startswith("/")
         and type(value["Config"]["Image"]) is str and value["Config"]["Image"]
         and type(value["Config"]["Labels"]) is dict and set(value["Config"]["Labels"]) <= {
             "com.docker.compose.project", "com.docker.compose.service", "com.docker.compose.oneoff",
             "lantern.owner", "io.lantern.bench-time.profile"}
         and all(type(x) is str for x in value["Config"]["Labels"].values())
         and type(value["State"]["Running"]) is bool and value["State"]["OOMKilled"] is False
         and integer(value["State"]["Pid"]) and type(value["HostConfig"]["ReadonlyRootfs"]) is bool
         and value["RestartCount"] == 0 and type(value["RestartCount"]) is int
         and value["State"]["ExitCode"] == 0 and type(value["State"]["ExitCode"]) is int
         and type(value["NetworkSettings"]["Networks"]) is dict,
         "time source watched container identity differs")
    timestamp(value["State"]["StartedAt"])
    return value


def _network(value):
    keys(value, ("Id", "Name", "Driver", "Internal", "Labels", "Containers"))
    need(type(value["Id"]) is str and HEX64.fullmatch(value["Id"])
         and type(value["Name"]) is str and value["Name"]
         and value["Driver"] == "bridge" and value["Internal"] is True,
         "time source network is not an internal bridge")
    keys(value["Labels"], ("com.docker.compose.project", "com.docker.compose.network"))
    need(all(type(x) is str and x for x in value["Labels"].values())
         and type(value["Containers"]) is dict, "time source network ownership missing")
    return value


def _address(value):
    need(type(value) is str, "time source private IPv4 missing")
    address = ipaddress.IPv4Address(value)
    need(str(address) == value and any(address in ipaddress.IPv4Network(cidr)
         for cidr in ("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16")),
         "time source address is not one private bridge IP")
    return value


def _attached(container, network, running):
    networks = container["NetworkSettings"]["Networks"]
    need(set(networks) == {network["Name"]}
         and container["HostConfig"]["NetworkMode"] in (network["Name"], network["Id"]),
         "time source container has another network")
    attachment = networks[network["Name"]]
    need(type(attachment) is dict and attachment.get("NetworkID") in
         ((network["Id"],) if running else (network["Id"], "")),
         "time source network ID differs")
    if running:
        ip = _address(attachment.get("IPAddress"))
        member = network["Containers"].get(container["Id"])
        keys(member, ("Name", "IPv4Address"))
        need(member["Name"] == container["Name"][1:]
             and str(ipaddress.IPv4Interface(member["IPv4Address"]).ip) == ip,
             "time source network membership differs")
        return ip
    return None


def _helper(root, value, expected):
    keys(value, ("path", "sha256", "sources", "go_tool_sha256", "go_version", "goos", "goarch",
                 "build_flags", "cgo_enabled"))
    need(value["path"] == str(root / "time-source-evidence/ntpfixture.bin")
         and type(value["sha256"]) is str and HEX64.fullmatch(value["sha256"])
         and type(value["sources"]) is dict and value["sources"] == expected["helper_sources"]
         and bool(value["sources"]) and all(type(k) is str and k.startswith("testbed/bench/ntpfixture/")
             and k.endswith(".go") and not k.endswith("_test.go") and ".." not in Path(k).parts
             and type(v) is str and HEX64.fullmatch(v) for k, v in value["sources"].items())
         and type(expected["go_tool_sha256"]) is str and HEX64.fullmatch(expected["go_tool_sha256"])
         and value["go_tool_sha256"] == expected["go_tool_sha256"]
         and type(value["go_version"]) is str and re.fullmatch(r"go1\.[0-9]+\.[0-9]+", value["go_version"])
         and value["goos"] == "linux" and value["goarch"] == expected["goarch"]
         and value["goarch"] in ("arm64", "amd64")
         and value["build_flags"] == ["-trimpath", "-buildvcs=false"] and value["cgo_enabled"] == "0",
         "time helper build/source provenance differs")
    raw, digest = read(root, value["path"], 16 << 20, False, modes=(0o700, 0o755))
    need(digest == value["sha256"] and len(raw) >= 20 and raw[:6] == b"\x7fELF\x02\x01"
         and int.from_bytes(raw[18:20], "little") == {"arm64": 183, "amd64": 62}[value["goarch"]],
         "time helper executable hash or architecture differs")
    return value


def _target(value, network, expected, service, initial=False):
    value = _inspect(value)
    labels = value["Config"]["Labels"]
    need(labels.get("com.docker.compose.project") == network["Labels"]["com.docker.compose.project"]
         and labels.get("com.docker.compose.service") == service and labels.get("io.lantern.bench-time.profile") == PROFILE
         and labels.get("com.docker.compose.oneoff") == ("True" if initial else "False")
         and labels.get("lantern.owner", "") == ("compose-security-initializer" if initial else "")
         and value["Image"] == expected["image_id"] and value["Config"]["User"] == expected["runtime_user"]
         and value["Config"]["Entrypoint"] == ["/app/lantern"]
         and value["HostConfig"]["ExtraHosts"] == [f"{HOST}:{expected['fixture_ip']}"]
         and value["State"]["Running"] is (not initial), "time source target identity or route differs")
    if initial:
        need(value["State"]["Status"] == "exited" and value["State"]["Pid"] == 0
             and value["Config"]["Image"] == expected["image_id"]
             and value["Config"]["Cmd"] == ["init-security"]
             and value["Name"] == f"/{labels['com.docker.compose.project']}-{service}-security-init",
             "time source initializer is not a completed native initializer")
    else:
        need(value["State"]["Status"] == "running" and value["State"]["Pid"] > 0
             and value["Config"]["Cmd"] in (None, [])
             and value["HostConfig"]["CpusetCpus"] == expected["cpuset"]
             and value["Name"] == f"/{labels['com.docker.compose.project']}-{service}-1",
             "time source serving target is not the frozen process")
    # These are safe time-config mount projections, not all application mounts.
    need(type(value["Mounts"]) is list and len(value["Mounts"]) == 1
         and value["Mounts"][0].get("Type") == "bind"
         and value["Mounts"][0].get("Destination") == "/etc/ntp.conf"
         and value["Mounts"][0].get("RW") is False, "time source configuration bind differs")
    source = expected["source_configs"][SERVICES.index(service)]["path"]
    hc = value["HostConfig"]
    bind = source + ":/etc/ntp.conf:ro"
    via_bind = hc["Binds"] == [bind] and hc["Mounts"] in (None, [])
    via_mount = (hc["Binds"] in (None, []) and type(hc["Mounts"]) is list and len(hc["Mounts"]) == 1
                 and hc["Mounts"][0].get("Type") == "bind" and hc["Mounts"][0].get("Source") == source
                 and hc["Mounts"][0].get("Target") == "/etc/ntp.conf" and hc["Mounts"][0].get("ReadOnly") is True)
    need(via_bind or via_mount, "time source hash is not bound to the requested config mount")
    _attached(value, network, not initial)
    return value


def _fixture(value, network, expected, helper):
    value = _inspect(value)
    labels, hc, st = value["Config"]["Labels"], value["HostConfig"], value["State"]
    need(labels == {"com.docker.compose.project": network["Labels"]["com.docker.compose.project"],
                    "com.docker.compose.service": "time-fixture", "io.lantern.bench-time.profile": PROFILE,
                    "com.docker.compose.oneoff": "False", "lantern.owner": "bench-local-udp-fixture"}
         and value["Name"] == f"/{labels['com.docker.compose.project']}-time-fixture"
         and value["Image"] == expected["image_id"] and value["Config"]["User"] == expected["runtime_user"]
         and value["Config"]["Image"] == expected["image_id"]
         and value["Config"]["Entrypoint"] == ["/fixture/ntpfixture"]
         and value["Config"]["Cmd"] == ["-listen", ":123", "-status", ":8080"]
         and st["Status"] == "running" and st["Running"] is True and st["Pid"] > 0,
         "time fixture process/image identity differs")
    need(hc["ReadonlyRootfs"] is True and hc["CapDrop"] == ["ALL"]
         and hc["Privileged"] is False and hc["AutoRemove"] is False
         and hc["PidMode"] == hc["UTSMode"] == hc["UsernsMode"] == ""
         and hc["IpcMode"] in ("private", "")
         and hc["RestartPolicy"] == {"Name": "no", "MaximumRetryCount": 0}
         and type(hc["RestartPolicy"].get("MaximumRetryCount")) is int
         and all(hc[k] in (None, []) for k in ("CapAdd", "Devices", "DeviceRequests", "DeviceCgroupRules", "VolumesFrom"))
         and hc["Tmpfs"] in (None, {}) and hc["PublishAllPorts"] is False
         and hc["SecurityOpt"] == ["no-new-privileges:true"]
         and hc["Sysctls"] == {"net.ipv4.ip_unprivileged_port_start": "0"}
         and hc["CpusetCpus"] == "4" and hc["NanoCpus"] == 250000000
         and type(hc["NanoCpus"]) is int and hc["Memory"] == 67108864 and type(hc["Memory"]) is int
         and hc["PidsLimit"] == 64 and type(hc["PidsLimit"]) is int
         and hc["ExtraHosts"] in (None, []),
         "time fixture resource/containment profile differs")
    requested, published = hc["PortBindings"], value["NetworkSettings"]["Ports"]
    need(type(requested) is dict and set(requested) == {"8080/tcp"}
         and type(published) is dict and "8080/tcp" in published
         and all(v in (None, []) for k, v in published.items() if k != "8080/tcp")
         and type(requested["8080/tcp"]) is list and len(requested["8080/tcp"]) == 1
         and type(published["8080/tcp"]) is list and len(published["8080/tcp"]) == 1,
         "time fixture may publish only loopback status TCP")
    request, actual = requested["8080/tcp"][0], published["8080/tcp"][0]
    keys(request, ("HostIp", "HostPort")); keys(actual, ("HostIp", "HostPort"))
    need(actual["HostIp"] == request["HostIp"] == "127.0.0.1" and type(actual["HostPort"]) is str
         and re.fullmatch(r"[1-9][0-9]{0,4}", actual["HostPort"]) and int(actual["HostPort"]) <= 65535
         and request["HostPort"] in ("", "0", actual["HostPort"]), "time fixture status port is not private")
    mounts = value["Mounts"]
    need(type(mounts) is list and len(mounts) == 1 and mounts[0].get("Type") == "bind"
         and mounts[0].get("Destination") == "/fixture/ntpfixture" and mounts[0].get("RW") is False
         and mounts[0].get("Propagation") == "rprivate", "time helper mount differs")
    requests = hc["Mounts"]
    need(hc["Binds"] in (None, []) and type(requests) is list and len(requests) == 1
         and type(requests[0]) is dict and set(requests[0]) <= {"Type", "Source", "Target", "ReadOnly", "Consistency", "BindOptions"}
         and requests[0].get("Type") == "bind" and requests[0].get("Source") == helper["path"]
         and requests[0].get("Target") == "/fixture/ntpfixture" and requests[0].get("ReadOnly") is True
         and requests[0].get("Consistency", "") == ""
         and requests[0].get("BindOptions", {}) in ({}, {"Propagation": "rprivate"},
             {"Propagation": "rprivate", "CreateMountpoint": False}),
         "time helper requested mount differs")
    return value, _attached(value, network, True)


def _diagnostic(value, network, expected):
    value = _inspect(value)
    project = network["Labels"]["com.docker.compose.project"]
    labels = value["Config"]["Labels"]
    need(labels.get("com.docker.compose.project") == project
         and labels.get("com.docker.compose.service") == "prometheus"
         and labels.get("com.docker.compose.oneoff") == "False" and labels.get("lantern.owner", "") == ""
         and labels.get("io.lantern.bench-time.profile", PROFILE) == PROFILE
         and value["Name"] == f"/{project}-prometheus-1"
         and value["Image"] == expected["diagnostic"]["image_id"]
         and value["Config"]["User"] == expected["diagnostic"]["runtime_user"]
         and value["Config"]["Entrypoint"] == ["/bin/prometheus"]
         and value["Config"]["Cmd"] == ["--config.file=/etc/prometheus/prometheus.yml", "--storage.tsdb.retention.time=1h"]
         and value["State"]["Status"] == "running" and value["State"]["Running"] is True
         and value["State"]["Pid"] > 0
         and value["HostConfig"]["ExtraHosts"] in (None, [])
         and type(value["Mounts"]) is list and len(value["Mounts"]) == 1
         and value["Mounts"][0].get("Destination") == "/etc/prometheus/prometheus.yml"
         and value["Mounts"][0].get("Type") == "bind" and value["Mounts"][0].get("RW") is False,
         "time network diagnostic is not the frozen Prometheus")
    _attached(value, network, True)
    return value


def _validate_case(root, expected):
    root = Path(root).absolute()
    need(expected.get("time_profile") == PROFILE and HEX40.fullmatch(expected.get("source_commit", ""))
         and HEX40.fullmatch(expected.get("source_tree", "")) and IMAGE.fullmatch(expected.get("image_id", ""))
         and re.fullmatch(r"[1-9][0-9]*:[0-9]+", expected.get("runtime_user", ""))
         and expected.get("cpuset") == "0-3" and expected.get("image_scope") in ("normal", "retained")
         and expected.get("run_id") == root.name and type(expected.get("receipt")) is bool
         and expected.get("scenario") in SCENARIOS
         and expected["receipt"] is expected["scenario"].startswith("receipt_"),
         "unbound controlled-local time profile")
    before, after, hashes = [], [], {}
    for phase, output in (("pre", before), ("post", after)):
        receipt, digest = read(root, f"time_source_{phase}.json")
        keys(receipt, ("schema", "profile", "phase", "source_commit", "source_tree", "scenario", "run_id", "scope",
                       "image_id", "runtime_user", "server_cpuset", "fixture_placement", "helper", "helper_executable", "ntp_config",
                       "fixture", "network", "status", "serving", "initializers", "diagnostics", "source_configs"))
        need(type(receipt["schema"]) is int and receipt["schema"] == 1 and receipt["profile"] == PROFILE
             and receipt["phase"] == phase
             and all(receipt[k] == expected[k] for k in
                     ("source_commit", "source_tree", "scenario", "run_id", "image_id", "runtime_user"))
             and receipt["scope"] == expected["image_scope"] and receipt["server_cpuset"] == expected["cpuset"],
             "time receipt case/source/scope differs")
        need(receipt["fixture_placement"] == "isolated", "correctness-only fixture placement cannot qualify performance")
        base = f"time-source-evidence/{phase}"
        helper = _helper(root, receipt["helper"], expected)
        need(reference(root, receipt["ntp_config"], "time-source-evidence/ntp.conf", 4096, False,
                       modes=(0o600, 0o644)) == ("server " + HOST + "\n").encode(),
             "time source declared configuration changed")
        source_configs = receipt["source_configs"]
        fixed_config_sha = hashlib.sha256(("server " + HOST + "\n").encode()).hexdigest()
        need(type(source_configs) is list and len(source_configs) == 3, "time source config cohort missing")
        for index, config in enumerate(source_configs):
            keys(config, ("service", "path", "sha256"))
            need(config["service"] == SERVICES[index] and config["sha256"] == fixed_config_sha
                 and type(config["path"]) is str and Path(config["path"]).is_absolute()
                 and ".." not in Path(config["path"]).parts and not any(c in config["path"] for c in "\r\n\x00:")
                 and Path(config["path"]).parts[-2:] == (SERVICES[index], "ntp.conf"),
                 "time source fixed per-node configuration differs")
        need(len({x["path"] for x in source_configs}) == 3, "duplicate time source config path")
        network = _network(reference(root, receipt["network"], f"{base}/network.json"))
        need(network["Labels"]["com.docker.compose.project"] == expected["project"]
             and network["Labels"]["com.docker.compose.network"] == "default"
             and network["Name"] == expected["project"] + "_default",
             "time source network belongs to another project")
        fixture, ip = _fixture(reference(root, receipt["fixture"], f"{base}/fixture.json"), network, expected, helper)
        status = _status(reference(root, receipt["status"], f"{base}/status.json"))
        need(status["responses"] >= (6 if expected["receipt"] else 3),
             "time fixture acquisition cohort observations incomplete")
        binding = dict(expected, fixture_ip=ip, source_configs=source_configs)
        need(type(receipt["serving"]) is list and len(receipt["serving"]) == 3
             and type(receipt["initializers"]) is list and len(receipt["initializers"]) == (3 if expected["receipt"] else 0),
             "time source cohort incomplete")
        serving = [_target(reference(root, row, f"{base}/serving-{i}.json"), network, binding, SERVICES[i])
                   for i, row in enumerate(receipt["serving"])]
        initializers = [_target(reference(root, row, f"time-source-evidence/init-{i}.json"), network, binding, SERVICES[i], True)
                        for i, row in enumerate(receipt["initializers"])]
        need(type(receipt["diagnostics"]) is list and len(receipt["diagnostics"]) == (0 if expected["receipt"] else 1),
             "time source diagnostic cohort differs")
        diagnostics = [_diagnostic(reference(root, row, f"{base}/diagnostic-prometheus.json"), network, expected)
                       for row in receipt["diagnostics"]]
        provenance = read(root, f"image_provenance_{phase}.json", modes=(0o600, 0o644))[0]
        need(provenance.get("source_commit") == expected["source_commit"]
             and provenance.get("image_id") == expected["image_id"]
             and provenance.get("runtime_user") == expected["runtime_user"]
             and type(provenance.get("replicas")) is list and len(provenance["replicas"]) == 3
             and {r.get("service"): r.get("container_id") for r in provenance["replicas"]} ==
                 {r["Config"]["Labels"]["com.docker.compose.service"]: r["Id"] for r in serving},
             "time receipt targets differ from actual workload provenance")
        need(all(row["Config"]["Image"] == provenance.get("image_ref", expected["image_id"]) for row in serving),
             "time source requested image differs from workload provenance")
        containers = [fixture] + serving + initializers + diagnostics
        need(len({row["Id"] for row in containers}) == len(containers)
             and len({row["State"]["Pid"] for row in [fixture] + serving + diagnostics}) == 4 + len(diagnostics)
             and set(network["Containers"]) <= {row["Id"] for row in containers},
             "time source network has duplicate or foreign members")
        executable = reference(root, receipt["helper_executable"],
                               "time-source-evidence/helper-executable.sha256", decode=False)
        need(executable == (helper["sha256"] + "  /proc/1/exe\n").encode(),
             "time fixture executed helper content differs")
        output.extend((receipt, fixture, network, status, serving, initializers, ip, diagnostics))
        hashes[phase] = {"path": str(root / f"time_source_{phase}.json"), "sha256": digest}
    pre, post = before[0], after[0]
    need(all(pre[k] == post[k] for k in pre if k not in ("phase", "fixture", "network", "status", "serving", "diagnostics"))
         and before[1] == after[1] and before[4:] == after[4:]
         and {k: v for k, v in before[2].items() if k != "Containers"} ==
             {k: v for k, v in after[2].items() if k != "Containers"}, "time source process/routing changed")
    need(all(before[3][k] == after[3][k] for k in ("pid", "start_nonce", "clock_kind", "seed_utc"))
         and after[3]["elapsed_ns"] > before[3]["elapsed_ns"]
         and all(after[3][k] >= before[3][k] for k in ("requests", "responses")),
         "time fixture restarted, clock/counters rolled back or no elapsed observation")
    return {"schema": 1, "profile": PROFILE, "verdict": "pass", "external_time_qualified": False,
            "receipts": hashes, "fixture_container_id": before[1]["Id"],
            "fixture_pid": before[1]["State"]["Pid"], "network_id": before[2]["Id"],
            "pairing": {"helper_sha256": pre["helper"]["sha256"], "sources": pre["helper"]["sources"],
                        "go_tool_sha256": pre["helper"]["go_tool_sha256"], "go_version": pre["helper"]["go_version"],
                        "goarch": pre["helper"]["goarch"], "clock_kind": before[3]["clock_kind"],
                        "profile": PROFILE, "fixture_cpuset": "4", "server_cpuset": "0-3"}}


def validate_case(root, expected):
    try:
        return _validate_case(root, expected)
    except Incomplete:
        raise
    except (OSError, KeyError, TypeError, AttributeError, ValueError, OverflowError) as error:
        raise Incomplete("missing or malformed controlled-local time evidence") from error


def owned_fixture(value, expected):
    """Ownership-only cleanup predicate; it never admits execution/qualification."""
    try:
        value = _inspect(value)
        need(value["Id"] == expected["container_id"], "fixture cleanup CID differs")
        requests = value["HostConfig"]["Mounts"]
        need(type(requests) is list and len(requests) == 1, "fixture cleanup helper mount missing")
        path = Path(requests[0]["Source"])
        out = Path(expected["source_root"]) / "testbed/bench/out"
        relative = path.relative_to(out)
        parts = relative.parts[1:] if relative.parts[0] == "retained" else relative.parts
        need(len(parts) == 4 and parts[0] in SCENARIOS
             and re.fullmatch(r"[0-9]{8}T[0-9]{6}Z", parts[1])
             and parts[2:] == ("time-source-evidence", "ntpfixture.bin"), "fixture cleanup path is not an owned case")
        _, digest = read(path.parent.parent, path, 16 << 20, False, modes=(0o700, 0o755))
        if expected.get("helper_sha256") is not None:
            need(digest == expected["helper_sha256"], "fixture cleanup helper changed")
        name = expected["project"] + "_default"
        attachment = value["NetworkSettings"]["Networks"][name]
        need(type(attachment.get("NetworkID")) is str and HEX64.fullmatch(attachment["NetworkID"]),
             "fixture cleanup network ID missing")
        network = {"Name": name, "Id": attachment["NetworkID"],
                   "Labels": {"com.docker.compose.project": expected["project"]},
                   "Containers": {value["Id"]: {"Name": value["Name"][1:],
                       "IPv4Address": attachment["IPAddress"] + "/32"}}}
        _fixture(value, network, expected, {"path": str(path)})
        return True
    except (Incomplete, OSError, KeyError, TypeError, AttributeError, ValueError, IndexError):
        return False
