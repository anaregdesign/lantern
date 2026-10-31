import copy
import hashlib
import json
import os
from pathlib import Path
import tempfile
import unittest

import bench_time_profile as profile


def write(path, value, raw=False):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(value if raw else (json.dumps(value) + "\n").encode())
    path.chmod(0o600)
    return {"path": str(path), "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}


def fixture(root, expected=None, receipt=True):
    """Synthetic Docker/status bytes; no target, socket or subprocess is used."""
    expected = dict(expected or {}, time_profile=profile.PROFILE)
    defaults = {"source_commit": "a" * 40, "source_tree": "b" * 40,
                "image_id": "sha256:" + "c" * 64, "image_scope": "normal",
                "runtime_user": "1000:1000", "cpuset": "0-3", "scenario": "receipt_admission_lookup" if receipt else "broad_rw",
                "run_id": root.name, "receipt": receipt, "goarch": "arm64", "project": "benchcase",
                "helper_sources": {"testbed/bench/ntpfixture/main.go": "d" * 64,
                                   "testbed/bench/ntpfixture/relay.go": "f" * 64}, "go_tool_sha256": "e" * 64,
                "diagnostic": {"image_id": "sha256:" + "9" * 64, "runtime_user": "nobody"}}
    for k, v in defaults.items():
        expected.setdefault(k, v)
    old_provenance = {phase: json.loads((root / f"image_provenance_{phase}.json").read_text())
                      for phase in ("pre", "post") if (root / f"image_provenance_{phase}.json").exists()}
    helper_path = root / "time-source-evidence/ntpfixture.bin"
    executable = bytearray(32)
    executable[:6] = b"\x7fELF\x02\x01"
    executable[18:20] = {"arm64": 183, "amd64": 62}[expected["goarch"]].to_bytes(2, "little")
    helper = write(helper_path, bytes(executable), True)
    helper_path.chmod(0o755)
    helper.update(sources=expected["helper_sources"], go_tool_sha256=expected["go_tool_sha256"],
                  go_version="go1.27.3", goos="linux", goarch=expected["goarch"],
                  build_flags=["-trimpath", "-buildvcs=false"], cgo_enabled="0")
    executable_ref = write(root / "time-source-evidence/helper-executable.sha256",
                           (helper["sha256"] + "  /proc/1/exe\n").encode(), True)
    config_ref = write(root / "time-source-evidence/ntp.conf", b"server time.asia.apple.com\n", True)
    name = expected["project"] + "_default"
    retained = expected["image_scope"] == "retained"
    subnet = "172.22.0." if retained else "172.20.0."
    network_id = ("a" if retained else "8") * 64
    ip = subnet + "2"
    source_configs = [{"service": s, "path": str(root.parent / "tls" / s / "ntp.conf"),
                       "sha256": config_ref["sha256"]} for s in profile.SERVICES]
    base = {"Id": ("e" if expected["image_scope"] == "retained" else "f") * 64,
            "Name": f"/{expected['project']}-time-fixture", "Image": expected["image_id"],
            "Config": {"Image": expected["image_id"], "User": expected["runtime_user"], "Entrypoint": ["/fixture/ntpfixture"],
                       "Cmd": ["-listen", ":123", "-status", ":8080"],
                       "Labels": {"com.docker.compose.project": expected["project"],
                                  "com.docker.compose.service": "time-fixture", "io.lantern.bench-time.profile": profile.PROFILE,
                                  "com.docker.compose.oneoff": "False", "lantern.owner": "bench-local-udp-fixture"}},
            "State": {"Status": "running", "Running": True, "Pid": 98, "StartedAt": "2026-10-30T00:00:00Z",
                      "OOMKilled": False, "ExitCode": 0}, "RestartCount": 0,
            "HostConfig": {"NetworkMode": name, "ExtraHosts": [], "ReadonlyRootfs": True,
                           "CapDrop": ["ALL"], "SecurityOpt": ["no-new-privileges:true"],
                           "Sysctls": {"net.ipv4.ip_unprivileged_port_start": "0"}, "CpusetCpus": "4",
                           "NanoCpus": 250000000, "Memory": 67108864, "PidsLimit": 64,
                           "PortBindings": {},
                           "Mounts": [{"Type": "bind", "Source": str(helper_path), "Target": "/fixture/ntpfixture", "ReadOnly": True}],
                           "Binds": [], "Privileged": False, "AutoRemove": False, "PidMode": "", "IpcMode": "private",
                           "UTSMode": "", "UsernsMode": "", "RestartPolicy": {"Name": "no", "MaximumRetryCount": 0},
                           "CapAdd": None, "Devices": [], "DeviceRequests": None, "DeviceCgroupRules": None,
                           "VolumesFrom": None, "Tmpfs": {}, "PublishAllPorts": False},
            "Mounts": [{"Type": "bind", "Source": "/host_mnt" + str(helper_path), "Destination": "/fixture/ntpfixture",
                        "Mode": "ro", "RW": False, "Propagation": "rprivate"}],
            "NetworkSettings": {"Networks": {name: {"NetworkID": network_id, "IPAddress": ip}},
                                "Ports": {"6380/tcp": None, "8080/tcp": None}}}
    serving, initializers = [], []
    for i, service in enumerate(profile.SERVICES):
        row = copy.deepcopy(base)
        row.update(Id=str(i + 1) * 64, Name=f"/{expected['project']}-{service}-1")
        row["Config"].update(Entrypoint=["/app/lantern"], Cmd=None)
        row["Config"]["Image"] = old_provenance.get("pre", {}).get("image_ref", expected["image_id"])
        row["Config"]["Labels"]["com.docker.compose.service"] = service
        row["Config"]["Labels"].pop("lantern.owner")
        row["State"]["Pid"] = 100 + i
        row["HostConfig"].update(CpusetCpus="0-3", ExtraHosts=[profile.HOST + ":" + ip], Mounts=[],
                                  Binds=[source_configs[i]["path"] + ":/etc/ntp.conf:ro"])
        row["Mounts"] = [{"Type": "bind", "Source": source_configs[i]["path"], "Destination": "/etc/ntp.conf",
                          "RW": False, "Mode": "ro", "Propagation": "rprivate"}]
        row["NetworkSettings"]["Networks"][name]["IPAddress"] = subnet + str(i + 3)
        serving.append(row)
        init = copy.deepcopy(row)
        init.update(Id=str(i + 4) * 64, Name=f"/{expected['project']}-{service}-security-init")
        init["Config"]["Cmd"] = ["init-security"]
        init["Config"]["Image"] = expected["image_id"]
        init["Config"]["Labels"].update({"lantern.owner": "compose-security-initializer", "com.docker.compose.oneoff": "True"})
        init["State"].update(Status="exited", Running=False, Pid=0)
        init["NetworkSettings"]["Networks"][name].update(NetworkID="", IPAddress="")
        initializers.append(init)
    diagnostic = copy.deepcopy(serving[0])
    diagnostic.update(Id="7" * 64, Name=f"/{expected['project']}-prometheus-1", Image=expected["diagnostic"]["image_id"])
    diagnostic["Config"].update(Image=expected["diagnostic"]["image_id"], User=expected["diagnostic"]["runtime_user"], Entrypoint=["/bin/prometheus"],
                                Cmd=["--config.file=/etc/prometheus/prometheus.yml", "--storage.tsdb.retention.time=1h"])
    diagnostic["Config"]["Labels"]["com.docker.compose.service"] = "prometheus"
    diagnostic["State"]["Pid"] = 103
    diagnostic["HostConfig"]["ExtraHosts"] = []
    prometheus_config = str(root.parent / "prometheus.native.yml")
    volume = "a" * 64
    diagnostic["HostConfig"].update(Binds=[prometheus_config + ":/etc/prometheus/prometheus.yml:ro"], Mounts=None)
    # Match the safe Docker projection: Config.Volumes is deliberately absent.
    diagnostic["Mounts"] = [
        {"Type": "bind", "Source": prometheus_config, "Destination": "/etc/prometheus/prometheus.yml",
         "Mode": "ro", "RW": False, "Propagation": "rprivate"},
        {"Type": "volume", "Name": volume, "Source": "/var/lib/docker/volumes/" + volume + "/_data",
         "Destination": "/prometheus", "Driver": "local", "Mode": "", "RW": True, "Propagation": ""}]
    diagnostic["NetworkSettings"]["Networks"][name]["IPAddress"] = subnet + "6"
    relay = copy.deepcopy(base)
    relay.update(Id=("c" if retained else "d") * 64, Name=f"/{expected['project']}-time-relay")
    relay["Config"]["Labels"].update({"com.docker.compose.service": "time-relay", "lantern.owner": "bench-local-tcp-relay"})
    relay["Config"]["Cmd"] = ["-relay-config", "/fixture/relay.json"]
    relay["State"]["Pid"] = 104
    routes = [{"listen_port": 6380 + i, "backend_ip": subnet + str(i + 3), "backend_port": 6380} for i in range(3)]
    if not receipt:
        routes += [{"listen_port": 9090 + i, "backend_ip": subnet + str(i + 3), "backend_port": 9090} for i in range(3)]
        routes += [{"listen_port": 9093, "backend_ip": subnet + "6", "backend_port": 9090}]
    relay_config = {"schema": 1, "protocol": profile.TRANSPORT, "max_connections": 4096,
                    "frontend_ipv6": False, "backend_ipv6": False, "routes": routes}
    relay_config_ref = write(root / "time-source-evidence/relay.json", relay_config)
    relay_executable_ref = write(root / "time-source-evidence/relay-executable.sha256",
                                 (helper["sha256"] + "  /proc/1/exe\n").encode(), True)
    relay["HostConfig"].update(Sysctls={"net.ipv4.ip_forward": "0", "net.ipv6.conf.all.forwarding": "0"},
                               NanoCpus=1000000000, Memory=536870912, PidsLimit=128)
    relay["HostConfig"]["Mounts"].append({"Type": "bind", "Source": relay_config_ref["path"], "Target": "/fixture/relay.json", "ReadOnly": True})
    relay["Mounts"].append({"Type": "bind", "Source": "/host_mnt" + relay_config_ref["path"], "Destination": "/fixture/relay.json",
                            "Mode": "ro", "RW": False, "Propagation": "rprivate"})
    publishers = {str(r["listen_port"]) + "/tcp": [{"HostIp": "127.0.0.1", "HostPort":
                  str(r["listen_port"] if r["listen_port"] < 9000 else 9390 + r["listen_port"] - 9090 if r["listen_port"] < 9093 else 9091)}]
                  for r in routes}
    relay["HostConfig"]["PortBindings"] = publishers
    relay["NetworkSettings"]["Ports"] = copy.deepcopy(publishers)
    relay["NetworkSettings"]["Networks"][name]["IPAddress"] = subnet + "7"
    front_name = expected["project"] + "_frontend"
    front_ip = "172.23.0.2" if retained else "172.21.0.2"
    front_id = ("b" if retained else "9") * 64
    relay["NetworkSettings"]["Networks"][front_name] = {"NetworkID": front_id, "IPAddress": front_ip}
    frontend = {"Id": front_id, "Name": front_name, "Driver": "bridge", "Internal": False, "EnableIPv6": False,
                "Labels": {"com.docker.compose.project": expected["project"], "com.docker.compose.network": "frontend"},
                "Containers": {relay["Id"]: {"Name": relay["Name"][1:], "IPv4Address": front_ip + "/16"}}}
    network = {"Id": network_id, "Name": name, "Driver": "bridge", "Internal": True, "EnableIPv6": False,
               "Labels": {"com.docker.compose.project": expected["project"], "com.docker.compose.network": "default"},
               "Containers": {r["Id"]: {"Name": r["Name"][1:], "IPv4Address":
                   r["NetworkSettings"]["Networks"][name]["IPAddress"] + "/16"}
                   for r in [base, relay] + serving + ([] if receipt else [diagnostic])}}
    init_refs = [write(root / f"time-source-evidence/init-{i}.json", row) for i, row in enumerate(initializers)] if receipt else []
    for phase in ("pre", "post"):
        path = root / f"time-source-evidence/{phase}"
        status = {"schema": 1, "protocol": "lantern_bench_ntp_fixture_v1", "pid": 1, "start_nonce": ("c" if retained else "a") * 64,
                  "clock_kind": "seed_utc_plus_monotonic", "seed_utc": "2026-10-30T00:00:00Z",
                  "elapsed_ns": 10 if phase == "pre" else 100, "requests": 6, "responses": 6, "invalid": 0, "clock_failure": 0}
        relay_status = {"schema": 1, "protocol": profile.TRANSPORT, "pid": 1,
                        "start_nonce": ("d" if retained else "b") * 64, "config_sha256": relay_config_ref["sha256"],
                        "ipv4_forwarding": "0\n", "ipv6_forwarding": "0\n",
                        "accepted": 4 if phase == "pre" else 14, "active": 1 if phase == "pre" else 2,
                        "completed": 3 if phase == "pre" else 12, "rejected": 1, "dial_failure": 2, "copy_failure": 3}
        packet = {"schema": 1, "profile": profile.PROFILE, "phase": phase,
                  **{k: expected[k] for k in ("source_commit", "source_tree", "scenario", "run_id", "image_id", "runtime_user")},
                  "scope": expected["image_scope"], "server_cpuset": "0-3", "fixture_placement": "isolated",
                  "helper": helper, "helper_executable": executable_ref, "ntp_config": config_ref, "source_configs": source_configs,
                  "transport_profile": profile.TRANSPORT, "relay_config": relay_config_ref, "relay_executable": relay_executable_ref,
                  "relay": write(path / "relay.json", relay), "frontend": write(path / "frontend.json", frontend),
                  "relay_status": write(path / "relay-status.json", relay_status),
                  "fixture": write(path / "fixture.json", base), "network": write(path / "network.json", network),
                  "status": write(path / "status.json", status),
                  "serving": [write(path / f"serving-{i}.json", row) for i, row in enumerate(serving)],
                  "initializers": init_refs, "diagnostics": [] if receipt else [write(path / "diagnostic-prometheus.json", diagnostic)]}
        write(root / f"time_source_{phase}.json", packet)
        if phase not in old_provenance:
            write(root / f"image_provenance_{phase}.json", {"source_commit": expected["source_commit"],
              "image_id": expected["image_id"], "runtime_user": expected["runtime_user"],
              "replicas": [{"service": s, "container_id": r["Id"]} for s, r in zip(profile.SERVICES, serving)]})
    return expected


def ntp_binding(root, expected, index=1, initializer_alias=False, structured=False):
    """Raw fixed-utility file-pair proof; no Docker, subprocess or target is used."""
    packet = json.loads((root / "time_source_pre.json").read_text())
    service = profile.SERVICES[index]
    canonical = packet["source_configs"][index]["path"]
    alternate = "/observed-daemon" + canonical
    base = root / "time-source-evidence/ntp-mounts" / service
    base.mkdir(parents=True, exist_ok=True); base.chmod(0o700)
    for phase in ("pre", "post"):
        path = root / f"time_source_{phase}.json"; value = json.loads(path.read_text())
        role = "initializers" if initializer_alias else "serving"
        target = value[role][index]
        row = json.loads(Path(target["path"]).read_text())
        row["HostConfig"].update(Binds=None if structured else [alternate + ":/etc/ntp.conf:ro"],
            Mounts=[{"Type": "bind", "Source": alternate, "Target": "/etc/ntp.conf", "ReadOnly": True, "BindOptions": {}}] if structured else [])
        row["Mounts"][0]["Source"] = alternate
        value[role][index] = write(Path(target["path"]), row)
        write(path, value)
    packet = json.loads((root / "time_source_pre.json").read_text())
    original = json.loads(Path(packet["serving"][index]["path"]).read_text())
    original["State"].update(Status="created", Running=False, Pid=0, StartedAt="0001-01-01T00:00:00Z")
    alias = copy.deepcopy(original)
    if initializer_alias:
        alias = json.loads(Path(packet["initializers"][index]["path"]).read_text())
        alias["State"].update(Status="created", Running=False, Pid=0, StartedAt="0001-01-01T00:00:00Z")
    identity = {"schema": 1, "profile": profile.NTP_BIND_PROFILE, "path": canonical, "device": 1, "inode": 123,
                "type": "regular file", "uid": os.getuid(), "gid": os.getgid(), "mode": 0o644, "nlink": 1,
                "size": 27, "mtime_ns": 1, "ctime_ns": 1, "sha256": profile.NTP_SHA}
    daemon = b"test-daemon-identity\n"
    environment = ["PATH=/usr/bin:/bin", "LANTERN_COMMIT=" + expected["source_commit"]]
    probe = {"Id": "0" * 64, "Name": f"/{expected['project']}-{service}-bind-proof", "Image": expected["image_id"],
             "Config": {"Image": expected["image_id"], "User": expected["runtime_user"], "Entrypoint": ["/bin/busybox"],
                        "Cmd": ["stat", "-c", "%d:%i:%F", "/proof/original-0", "/proof/clone-0"], "Env": environment,
                        "Volumes": None, "Labels": {"com.docker.compose.project": expected["project"],
                            "com.docker.compose.service": service + "-bind-proof", "com.docker.compose.oneoff": "True",
                            "lantern.owner": "compose-bind-identity-probe", "lantern.initializer": "lantern-owned-init.test123"}},
             "HostConfig": {"NetworkMode": "none", "ReadonlyRootfs": True, "Privileged": False, "AutoRemove": False,
                            "CapDrop": ["ALL"], "CapAdd": None, "SecurityOpt": ["no-new-privileges:true"], "Memory": 33554432,
                            "NanoCpus": 250000000, "PidsLimit": 16, "RestartPolicy": {"Name": "no", "MaximumRetryCount": 0},
                            "Binds": None, "VolumesFrom": None, "Devices": None, "DeviceRequests": None, "DeviceCgroupRules": None,
                            "PidMode": "", "UTSMode": "", "UsernsMode": "", "IpcMode": "private", "Tmpfs": {},
                            "PublishAllPorts": False, "PortBindings": {}, "Mounts": []},
             "Mounts": [], "RestartCount": 0, "State": {"Status": "created", "Running": False, "Pid": 0,
                    "StartedAt": "0001-01-01T00:00:00Z", "OOMKilled": False, "ExitCode": 0, "Paused": False, "Restarting": False}}
    for side, path in (("original", canonical), ("clone", alternate)):
        target = "/proof/" + side + "-0"
        probe["HostConfig"]["Mounts"].append({"Type": "bind", "Source": path, "Target": target, "ReadOnly": True,
                                             "BindOptions": {"Propagation": "rprivate", "CreateMountpoint": False}})
        # The daemon may collapse both views to A. Only raw stat proves identity.
        probe["Mounts"].append({"Type": "bind", "Source": alternate, "Destination": target, "Mode": "ro",
                                "RW": False, "Propagation": "rprivate"})
    completed = copy.deepcopy(probe)
    completed["State"].update(Status="exited", StartedAt="2026-10-30T00:00:00Z")
    binding = {"schema": 1, "profile": profile.NTP_BIND_PROFILE,
               **{k: expected[k] for k in ("source_commit", "source_tree", "scenario", "project", "image_id", "runtime_user")},
               "run_id": root.name, "scope": expected["image_scope"], "service": service,
               "original_container_id": original["Id"], "canonical_source": canonical, "alternate_source": alternate,
               "config_sha256": profile.NTP_SHA, "alias_role": "initializer" if initializer_alias else "serving",
               "daemon_id": daemon.decode().strip()}
    values = {"host_before": ("host-before.json", identity), "host_after": ("host-after.json", identity),
              "original_before": ("original-before.json", original), "original_after": ("original-after.json", original),
              "alias_target_before": ("alias-target-before.json", alias), "daemon_before": ("daemon-before.txt", daemon),
              "daemon_after": ("daemon-after.txt", daemon), "image_environment": ("image-environment.json", environment),
              "pairs_file": ("requested-pairs.json", [{"original_source": canonical, "clone_source": alternate, "expected_type": "regular file"}]),
              "probe_created": ("probe-created.json", probe), "probe_completed": ("probe-completed.json", completed),
              "probe_output": ("probe-output.log", b"99:456:regular file\n99:456:regular file\n"),
              "probe_wait": ("probe-wait.log", b"0\n"), "probe_remove": ("probe-remove.log", (probe["Id"] + "\n").encode())}
    for key, (filename, value) in values.items():
        binding[key] = write(base / filename, value, isinstance(value, bytes))
    # The existing convenience summary is deliberately not qualification input.
    write(base / "identity.json", {"status": "ignored"})
    bound = write(base / "binding.json", binding)
    for phase in ("pre", "post"):
        path = root / f"time_source_{phase}.json"; packet = json.loads(path.read_text())
        entries = [None] * 3
        entries[index] = {"binding": bound, "host": write(root / f"time-source-evidence/{phase}/ntp-host-{index}.json", identity),
                          "daemon": write(root / f"time-source-evidence/{phase}/ntp-daemon.txt", daemon, True)}
        packet["source_config_bindings"] = entries; write(path, packet)
    return base / "binding.json"


def alter_ntp_binding(root, binding_path, key, change):
    binding = json.loads(binding_path.read_text())
    if key in binding and isinstance(binding[key], dict) and set(binding[key]) == {"path", "sha256"}:
        path = Path(binding[key]["path"])
        value = json.loads(path.read_text()); change(value)
        binding[key] = write(path, value)
    else:
        change(binding)
    ref = write(binding_path, binding)
    for phase in ("pre", "post"):
        path = root / f"time_source_{phase}.json"; packet = json.loads(path.read_text())
        packet["source_config_bindings"][1]["binding"] = ref; write(path, packet)


class TimeProfileTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve() / "case"
        self.expected = fixture(self.root)

    def alter(self, phase, field, change):
        path = self.root / f"time_source_{phase}.json"
        packet = json.loads(path.read_text())
        ref = packet[field]
        value = json.loads(Path(ref["path"]).read_text())
        change(value)
        packet[field] = write(Path(ref["path"]), value)
        write(path, packet)

    def test_native_image_local_profile_and_alias_content_proof(self):
        proof = profile.validate_case(self.root, self.expected)
        self.assertEqual(proof["verdict"], "pass")
        self.assertFalse(proof["external_time_qualified"])
        self.assertEqual(proof["pairing"]["helper_sha256"], hashlib.sha256((self.root / "time-source-evidence/ntpfixture.bin").read_bytes()).hexdigest())

    def test_off_cohort_accounts_for_existing_diagnostic(self):
        root = self.root.parent / "off"
        expected = fixture(root, receipt=False)
        self.assertEqual(profile.validate_case(root, expected)["verdict"], "pass")

    def test_diagnostic_real_config_and_anonymous_data_shape_ignore_only_effective_order(self):
        root = self.root.parent / "diagnostic-order"; expected = fixture(root, receipt=False)
        path = root / "time_source_post.json"; packet = json.loads(path.read_text())
        target = Path(packet["diagnostics"][0]["path"]); row = json.loads(target.read_text())
        self.assertNotIn("Volumes", row["Config"])
        row["Mounts"].reverse(); packet["diagnostics"][0] = write(target, row); write(path, packet)
        raw = target.read_bytes()
        self.assertEqual(profile.validate_case(root, expected)["verdict"], "pass")
        self.assertEqual(target.read_bytes(), raw)
        network = json.loads(Path(packet["network"]["path"]).read_text())
        original = copy.deepcopy(row)
        normalized = profile._diagnostic(row, network, expected)
        self.assertEqual(row, original)
        self.assertIsNot(normalized, row)
        self.assertEqual([x["Destination"] for x in normalized["Mounts"]], ["/etc/prometheus/prometheus.yml", "/prometheus"])
        # Diagnostic Sources are observed spellings, not an NTP same-file proof.
        # Keep both phases stable while requested/effective paths differ.
        for phase in ("pre", "post"):
            path = root / f"time_source_{phase}.json"; packet = json.loads(path.read_text())
            target = Path(packet["diagnostics"][0]["path"]); row = json.loads(target.read_text())
            row["HostConfig"]["Binds"] = ["/Users/operator/prometheus.yml:/etc/prometheus/prometheus.yml:ro"]
            next(x for x in row["Mounts"] if x["Type"] == "bind")["Source"] = "/host_mnt/Users/operator/prometheus.yml"
            packet["diagnostics"][0] = write(target, row); write(path, packet)
        self.assertEqual(profile.validate_case(root, expected)["verdict"], "pass")

    def test_diagnostic_foreign_extra_writable_and_substituted_mounts_refuse(self):
        def foreign_config(row):
            row["HostConfig"]["Binds"] = ["/foreign/prometheus.native.yml:/etc/prometheus/prometheus.yml:ro"]
            row["Mounts"][0]["Source"] = "/foreign/prometheus.native.yml"
        def substituted_data(row):
            row["Mounts"][1].update(Name="b" * 64, Source="/var/lib/docker/volumes/" + "b" * 64 + "/_data")
        changes = [foreign_config, substituted_data,
            lambda x: x["Mounts"][0].update(RW=True), lambda x: x["Mounts"][0].update(Mode="rw"),
            lambda x: x["Mounts"][0].update(Propagation="rshared"),
            lambda x: x["Mounts"][1].update(Name="foreign-volume"), lambda x: x["Mounts"][1].update(Driver="nfs"),
            lambda x: x["Mounts"][1].update(Source="/var/lib/docker/volumes/foreign/_data"),
            lambda x: x["Mounts"][1].update(RW=False), lambda x: x["Mounts"][1].update(Mode="ro"),
            lambda x: x["Mounts"].append(dict(x["Mounts"][1])),
            lambda x: x["Mounts"][1].update(Destination="/unexpected"),
            lambda x: x["HostConfig"].update(Binds=["/config:/etc/prometheus/prometheus.yml:rw"]),
            lambda x: x["HostConfig"].update(Binds=["/config:/unexpected:ro"]),
            lambda x: x["Mounts"][0].update(Source="/config/../unexpected"),
            lambda x: x["HostConfig"]["Binds"].append("foreign:/prometheus:rw"),
            lambda x: x["HostConfig"].update(Mounts=[{"Type": "volume", "Source": "foreign", "Target": "/prometheus"}])]
        for i, change in enumerate(changes):
            with self.subTest(change=i):
                root = self.root.parent / f"diagnostic-refusal-{i}"; expected = fixture(root, receipt=False)
                path = root / "time_source_post.json"; packet = json.loads(path.read_text())
                target = Path(packet["diagnostics"][0]["path"]); row = json.loads(target.read_text()); change(row)
                packet["diagnostics"][0] = write(target, row); write(path, packet)
                with self.assertRaises(profile.Incomplete): profile.validate_case(root, expected)

    def test_relay_effective_order_copy_preserves_exact_mount_continuity(self):
        for receipt in (False, True):
            with self.subTest(receipt=receipt):
                root = self.root.parent / f"relay-order-{receipt}"; expected = fixture(root, receipt=receipt)
                path = root / "time_source_post.json"; packet = json.loads(path.read_text())
                target = Path(packet["relay"]["path"]); row = json.loads(target.read_text())
                row["Mounts"].reverse(); packet["relay"] = write(target, row); write(path, packet)
                raw = target.read_bytes()
                self.assertEqual(profile.validate_case(root, expected)["verdict"], "pass")
                self.assertEqual(target.read_bytes(), raw)
                # Normalization must preserve the complete row, including Source.
                for field, value in (("Source", "/other/helper"), ("Destination", "/other/target"), ("RW", True)):
                    with self.subTest(field=field):
                        changed = copy.deepcopy(row); changed["Mounts"][0][field] = value
                        packet["relay"] = write(target, changed); write(path, packet)
                        with self.assertRaises(profile.Incomplete): profile.validate_case(root, expected)

    def test_fixture_restart_or_clock_and_counter_rollback(self):
        for field, changed in (("start_nonce", "b" * 64), ("seed_utc", "2026-10-31T00:00:00Z"),
                               ("elapsed_ns", 1), ("responses", 5), ("clock_failure", 1), ("invalid", 1), ("pid", True)):
            with self.subTest(field=field):
                fixture(self.root)
                self.alter("post", "status", lambda x: x.update({field: changed}))
                with self.assertRaises(profile.Incomplete):
                    profile.validate_case(self.root, self.expected)

    def test_network_egress_foreign_member_and_route_guard(self):
        for change in (lambda x: x.update(Internal=False),
                       lambda x: x["Containers"].update({"0" * 64: {"Name": "foreign", "IPv4Address": "172.20.0.20/16"}}),
                       lambda x: x["Labels"].update({"com.docker.compose.project": "foreign"})):
            fixture(self.root)
            self.alter("post", "network", change)
            with self.assertRaises(profile.Incomplete):
                profile.validate_case(self.root, self.expected)

    def test_missing_wrong_or_additional_initializer_mapping(self):
        for mapping in ([], [profile.HOST + ":172.20.0.99"],
                        [profile.HOST + ":172.20.0.2", "other.invalid:8.8.8.8"]):
            fixture(self.root)
            packet = json.loads((self.root / "time_source_pre.json").read_text())
            ref = packet["initializers"][0]
            row = json.loads(Path(ref["path"]).read_text()); row["HostConfig"]["ExtraHosts"] = mapping
            replacement = write(Path(ref["path"]), row)
            for phase in ("pre", "post"):
                path = self.root / f"time_source_{phase}.json"
                packet = json.loads(path.read_text()); packet["initializers"][0] = replacement; write(path, packet)
            with self.assertRaises(profile.Incomplete): profile.validate_case(self.root, self.expected)

    def test_exact_config_bytes_hash_and_requested_source_are_all_bound(self):
        for kind in ("bytes", "hash", "source"):
            fixture(self.root)
            path = self.root / "time_source_pre.json"; packet = json.loads(path.read_text())
            if kind == "bytes": packet["ntp_config"] = write(Path(packet["ntp_config"]["path"]), b"server other.invalid\n", True)
            if kind == "hash": packet["source_configs"][0]["sha256"] = "0" * 64
            if kind == "source": packet["source_configs"][0]["path"] = "/different/lantern-0/ntp.conf"
            write(path, packet)
            with self.assertRaises(profile.Incomplete): profile.validate_case(self.root, self.expected)

    def test_literal_requested_and_effective_sources_are_both_required(self):
        for phase in ("pre", "post"):
            fixture(self.root)
            packet_path = self.root / f"time_source_{phase}.json"; packet = json.loads(packet_path.read_text())
            row_ref = packet["serving"][1]; row = json.loads(Path(row_ref["path"]).read_text())
            row["Mounts"][0]["Source"] = "/host_mnt" + row["Mounts"][0]["Source"]
            packet["serving"][1] = write(Path(row_ref["path"]), row); write(packet_path, packet)
            with self.assertRaises(profile.Incomplete): profile.validate_case(self.root, self.expected)
            if phase == "pre":
                with self.assertRaises(profile.Incomplete): profile.validate_start(self.root, self.expected)
            else:
                self.assertTrue(profile.validate_start(self.root, self.expected)["admitted"])

    def test_ntp_same_file_proof_covers_both_lanes_roles_and_real_pre_admission(self):
        for scope in ("normal", "retained"):
            for receipt in (False, True):
                for alias_role in ((False, True) if receipt else (False,)):
                    for structured in (False, True):
                        with self.subTest(scope=scope, receipt=receipt, initializer_alias=alias_role, structured=structured):
                            root = self.root.parent / f"{scope}-{receipt}-{alias_role}-{structured}"
                            expected = fixture(root, {"image_scope": scope}, receipt=receipt)
                            ntp_binding(root, expected, initializer_alias=alias_role, structured=structured)
                            self.assertEqual(profile.validate_case(root, expected)["verdict"], "pass")
                            result = profile.validate_start(root, expected)
                            self.assertTrue(result["admitted"])
                            self.assertFalse(result["qualification"])
                            self.assertEqual(result["scope"], "time-config-start-admission")
                            (root / "time_source_post.json").unlink()
                            self.assertTrue(profile.validate_start(root, expected)["admitted"])
                            with self.assertRaises(profile.Incomplete): profile.validate_case(root, expected)

    def test_ntp_raw_identity_custody_and_origin_forgery_refuse_after_hashes_rebound(self):
        mutations = [
            ("context", lambda x: x.update(schema=True)),
            ("context", lambda x: x.update(source_tree="0" * 40)),
            ("context", lambda x: x.update(run_id="an-old-case")),
            ("context", lambda x: x.update(original_container_id="9" * 64)),
            ("context", lambda x: x.update(project="foreign")),
            ("context", lambda x: x.update(service="lantern-2")),
            ("context", lambda x: x.update(canonical_source="/other/lantern-1/ntp.conf")),
            ("host_after", lambda x: x.update(inode=x["inode"] + 1)),
            ("host_after", lambda x: x.update(sha256="0" * 64)),
            ("host_after", lambda x: x.update(mode=0o666)),
            ("host_after", lambda x: x.update(uid=x["uid"] + 1)),
            ("host_after", lambda x: x.update(gid=x["gid"] + 1)),
            ("original_after", lambda x: x["HostConfig"].update(ExtraHosts=[profile.HOST + ":172.20.0.99"])),
            ("probe_completed", lambda x: x["Config"].update(Cmd=["true"])),
            ("probe_completed", lambda x: x.update(Image="sha256:" + "f" * 64)),
            ("probe_completed", lambda x: x["HostConfig"].update(NetworkMode="bridge")),
            ("probe_completed", lambda x: x["HostConfig"].update(Privileged=True)),
            ("probe_completed", lambda x: x["State"].update(ExitCode=1)),
            ("probe_created", lambda x: x["State"].update(Pid=42)),
            ("probe_created", lambda x: x["State"].update(OOMKilled=True)),
            ("probe_completed", lambda x: x["State"].update(Pid=42)),
            ("probe_completed", lambda x: x["State"].update(Pid=False)),
            ("probe_completed", lambda x: x["State"].update(Paused=True)),
            ("probe_completed", lambda x: x["State"].update(Restarting=True)),
            ("probe_completed", lambda x: x["State"].update(StartedAt="0001-01-01T00:00:00Z")),
            ("probe_completed", lambda x: x["State"].update(StartedAt=False)),
            ("probe_completed", lambda x: x["Mounts"][0].update(Source="/third/lantern-1/ntp.conf")),
            ("probe_completed", lambda x: x["Mounts"][0].update(RW=True)),
            ("probe_completed", lambda x: x["Mounts"][0].update(Destination="/elsewhere")),
            ("probe_completed", lambda x: x["HostConfig"]["Mounts"][0]["BindOptions"].update(CreateMountpoint=True)),
        ]
        for i, (key, change) in enumerate(mutations):
            with self.subTest(key=key, mutation=i):
                root = self.root.parent / f"forgery-{i}"; expected = fixture(root)
                binding = ntp_binding(root, expected)
                alter_ntp_binding(root, binding, key, change)
                with self.assertRaises(profile.Incomplete): profile.validate_start(root, expected)
                with self.assertRaises(profile.Incomplete): profile.validate_case(root, expected)

    def test_ntp_stat_and_terminal_raw_failures_are_not_replaced_by_convenience_pass(self):
        changes = (("probe_output", b"99:456:regular file\n99:457:regular file\n"),
                   ("probe_output", b"99:456:directory\n99:456:directory\n"),
                   ("probe_output", b"99:456:regular file\n"),
                   ("probe_output", b"99:456:regular file\n99:456:regular file"),
                   ("probe_wait", b"1\n"), ("probe_remove", b"not-the-owned-container\n"),
                   ("daemon_after", b"different-daemon\n"))
        for i, (key, raw) in enumerate(changes):
            root = self.root.parent / f"raw-forgery-{i}"; expected = fixture(root)
            path = ntp_binding(root, expected); binding = json.loads(path.read_text())
            binding[key] = write(Path(binding[key]["path"]), raw, True)
            bound = write(path, binding)
            write(path.parent / "identity.json", {"status": "PASS"})
            for phase in ("pre", "post"):
                packet_path = root / f"time_source_{phase}.json"; packet = json.loads(packet_path.read_text())
                packet["source_config_bindings"][1]["binding"] = bound; write(packet_path, packet)
            with self.assertRaises(profile.Incomplete): profile.validate_start(root, expected)

    def test_ntp_phase_host_daemon_missing_foreign_and_third_source_are_refused(self):
        for kind in ("host", "daemon", "missing", "foreign", "third", "initializer-replay"):
            root = self.root.parent / kind; expected = fixture(root)
            binding = ntp_binding(root, expected, initializer_alias=kind == "initializer-replay")
            for phase in ("pre", "post"):
                packet_path = root / f"time_source_{phase}.json"; packet = json.loads(packet_path.read_text())
                entry = packet["source_config_bindings"][1]
                if kind == "host":
                    value = json.loads(Path(entry["host"]["path"]).read_text()); value["ctime_ns"] += 1
                    entry["host"] = write(Path(entry["host"]["path"]), value)
                elif kind == "daemon": entry["daemon"] = write(Path(entry["daemon"]["path"]), b"another-daemon\n", True)
                elif kind == "missing": packet.pop("source_config_bindings")
                elif kind == "foreign": entry["binding"]["path"] = str(self.root.parent / "foreign/binding.json")
                else:
                    target = packet["initializers" if kind == "initializer-replay" else "serving"][1]
                    row = json.loads(Path(target["path"]).read_text())
                    if kind == "third":
                        row["Mounts"][0]["Source"] = "/third/lantern-1/ntp.conf"
                    else: row["Id"] = "9" * 64
                    packet["initializers" if kind == "initializer-replay" else "serving"][1] = write(Path(target["path"]), row)
                write(packet_path, packet)
            with self.assertRaises(profile.Incomplete): profile.validate_start(root, expected)
            with self.assertRaises(profile.Incomplete): profile.validate_case(root, expected)

    def test_ntp_proof_hardlink_and_oversized_raw_input_are_refused(self):
        for kind in ("hardlink", "oversized", "extra-file"):
            root = self.root.parent / f"unsafe-{kind}"; expected = fixture(root)
            binding_path = ntp_binding(root, expected); binding = json.loads(binding_path.read_text())
            if kind == "hardlink":
                path = Path(binding["probe_output"]["path"])
                os.link(path, root / "another-stat-link")
            elif kind == "oversized":
                binding["probe_output"] = write(Path(binding["probe_output"]["path"]), b"x" * 4097, True)
                ref = write(binding_path, binding)
                for phase in ("pre", "post"):
                    path = root / f"time_source_{phase}.json"; packet = json.loads(path.read_text())
                    packet["source_config_bindings"][1]["binding"] = ref; write(path, packet)
            else:
                write(binding_path.parent / "unrequested.json", {})
            with self.assertRaises(profile.Incomplete): profile.validate_start(root, expected)
            with self.assertRaises(profile.Incomplete): profile.validate_case(root, expected)

    def test_short_search_start_admission_keeps_shared_placement_and_no_tree_qualification(self):
        root = self.root.parent / "search"; expected = fixture(root, {"scenario": "search_qualification", "source_tree": ""}, receipt=False)
        for phase in ("pre", "post"):
            packet_path = root / f"time_source_{phase}.json"; packet = json.loads(packet_path.read_text())
            packet["fixture_placement"] = "shared_correctness_only"
            row = json.loads(Path(packet["fixture"]["path"]).read_text()); row["HostConfig"]["CpusetCpus"] = "0"
            packet["fixture"] = write(Path(packet["fixture"]["path"]), row); write(packet_path, packet)
        (root / "time_source_post.json").unlink()
        self.assertTrue(profile.validate_start(root, expected)["admitted"])
        with self.assertRaises(profile.Incomplete): profile.validate_case(root, expected)

    def test_start_admission_preserves_documented_normal_bench_usage(self):
        for scenario in ("write_heavy", "read_heavy", "addedge_contention", "broad_rw"):
            with self.subTest(scenario=scenario):
                root = self.root.parent / scenario
                expected = fixture(root, {"source_tree": "", "scenario": scenario}, receipt=False)
                self.assertTrue(profile.validate_start(root, expected)["admitted"])
                self.assertFalse(profile.validate_start(root, expected)["qualification"])
                with self.assertRaises(profile.Incomplete): profile.validate_case(root, expected)

    def test_start_admission_rejects_unknown_protected_family_and_invalid_context(self):
        for i, (scope, scenario, receipt) in enumerate((
            ("normal", "receipt_admission_lookup", True),
            ("retained", "broad_rw", False), ("retained", "receipt_admission_lookup", True),
            ("retained", "search_qualification", False))):
            with self.subTest(scope=scope, scenario=scenario):
                root = self.root.parent / f"empty-tree-{i}"
                expected = fixture(root, {"source_tree": "", "scenario": scenario, "image_scope": scope}, receipt=receipt)
                with self.assertRaises(profile.Incomplete): profile.validate_start(root, expected)
        for i, (scope, scenario, tree) in enumerate((("normal", "../write_heavy", ""),
             ("normal", "write-heavy", ""), ("normal", "write_heavy", "invalid"),
             ("normal", "receipt_unknown", "b" * 40), ("retained", "write_heavy", "b" * 40))):
            root = self.root.parent / f"invalid-context-{i}"
            expected = fixture(root, {"source_tree": tree, "scenario": scenario, "image_scope": scope}, receipt=False)
            with self.assertRaises(profile.Incomplete): profile.validate_start(root, expected)

    def test_fifo_is_refused_without_opening_a_blocking_stream(self):
        path = self.root / "time-source-evidence/pre/status.json"
        path.unlink(); os.mkfifo(path, 0o600)
        with self.assertRaises(profile.Incomplete): profile.validate_case(self.root, self.expected)

    def test_helper_hash_source_tool_mount_and_placement_refusals(self):
        for kind in ("hash", "source", "tool", "mount", "placement"):
            with self.subTest(kind=kind):
                fixture(self.root)
                path = self.root / "time_source_pre.json"
                packet = json.loads(path.read_text())
                if kind == "hash": packet["helper"]["sha256"] = "0" * 64
                if kind == "source": packet["helper"]["sources"] = {}
                if kind == "tool": packet["helper"]["go_tool_sha256"] = "0" * 64
                if kind == "placement": packet["fixture_placement"] = "shared_correctness_only"
                if kind == "mount":
                    row = json.loads(Path(packet["fixture"]["path"]).read_text())
                    row["HostConfig"]["Mounts"][0]["ReadOnly"] = False
                    packet["fixture"] = write(Path(packet["fixture"]["path"]), row)
                write(path, packet)
                with self.assertRaises(profile.Incomplete): profile.validate_case(self.root, self.expected)

    def test_missing_foreign_duplicate_retyped_and_symlink_evidence(self):
        for kind in ("missing", "foreign", "duplicate", "bool", "symlink"):
            with self.subTest(kind=kind):
                fixture(self.root)
                path = self.root / "time_source_pre.json"
                if kind == "missing": path.unlink()
                elif kind == "duplicate": path.write_text('{"schema":1,"schema":1}')
                else:
                    packet = json.loads(path.read_text())
                    if kind == "foreign": packet["status"]["path"] = str(self.root.parent / "other/status.json")
                    if kind == "bool": packet["schema"] = True
                    if kind == "symlink":
                        status = Path(packet["status"]["path"]); target = status.with_name("actual.json")
                        status.rename(target); status.symlink_to(target)
                    write(path, packet)
                with self.assertRaises(profile.Incomplete): profile.validate_case(self.root, self.expected)

    def test_workload_provenance_cannot_be_replaced_by_producer_pass(self):
        path = self.root / "image_provenance_post.json"
        value = json.loads(path.read_text()); value["replicas"][0]["container_id"] = "0" * 64
        write(path, value)
        with self.assertRaises(profile.Incomplete): profile.validate_case(self.root, self.expected)

    def test_cleanup_only_adopts_the_exact_owned_fixture_profile(self):
        source = self.root / "source"
        root = source / "testbed/bench/out/receipt_admission_lookup/20261030T000000Z"
        expected = fixture(root)
        row = json.loads((root / "time-source-evidence/pre/fixture.json").read_text())
        binding = dict(expected, container_id=row["Id"], source_root=str(source), helper_sha256=None)
        self.assertTrue(profile.owned_fixture(row, binding))
        for field, value in (("CapAdd", ["NET_ADMIN"]), ("CpusetCpus", "0"), ("Privileged", True),
                             ("NetworkMode", "host")):
            bad = copy.deepcopy(row); bad["HostConfig"][field] = value
            self.assertFalse(profile.owned_fixture(bad, binding))

    def test_reserved_addresses_and_public_or_udp_publication_refuse(self):
        for ip in ("192.0.2.1", "198.51.100.1", "203.0.113.1", "127.0.0.1", "169.254.1.1", "8.8.8.8"):
            with self.subTest(ip=ip), self.assertRaises(profile.Incomplete): profile._address(ip)
        for field, value in (("123/udp", [{"HostIp": "127.0.0.1", "HostPort": "123"}]),
                             ("8080/tcp", [{"HostIp": "0.0.0.0", "HostPort": "32323"}])):
            fixture(self.root)
            self.alter("pre", "fixture", lambda row: row["HostConfig"]["PortBindings"].update({field: value}))
            with self.assertRaises(profile.Incomplete): profile.validate_case(self.root, self.expected)

    def test_relay_role_targets_raw_config_and_forwarding_guards(self):
        for kind in ("public-target", "metrics-in-receipt", "unknown-key", "forwarding", "ipv6", "frontend-member", "wrong-image", "restart"):
            with self.subTest(kind=kind):
                fixture(self.root)
                if kind in ("public-target", "metrics-in-receipt", "unknown-key"):
                    path = self.root / "time_source_pre.json"; packet = json.loads(path.read_text())
                    target = Path(packet["relay_config"]["path"]); value = json.loads(target.read_text())
                    if kind == "public-target": value["routes"][0]["backend_ip"] = "8.8.8.8"
                    if kind == "metrics-in-receipt": value["routes"].append({"listen_port": 9090, "backend_ip": "172.20.0.3", "backend_port": 9090})
                    if kind == "unknown-key": value["proxy"] = "http://outside.invalid"
                    packet["relay_config"] = write(target, value); write(path, packet)
                elif kind == "forwarding": self.alter("pre", "relay_status", lambda x: x.update(ipv4_forwarding="1\n"))
                elif kind == "ipv6": self.alter("post", "frontend", lambda x: x.update(EnableIPv6=True))
                elif kind == "frontend-member": self.alter("post", "frontend", lambda x: x["Containers"].update({"0" * 64: {"Name": "foreign", "IPv4Address": "172.21.0.4/16"}}))
                elif kind == "wrong-image": self.alter("pre", "relay", lambda x: x.update(Image="sha256:" + "0" * 64))
                elif kind == "restart": self.alter("post", "relay_status", lambda x: x.update(start_nonce="0" * 64))
                with self.assertRaises(profile.Incomplete): profile.validate_case(self.root, self.expected)

    def test_relay_failure_counters_are_observations_not_new_zero_thresholds(self):
        proof = profile.validate_case(self.root, self.expected)
        self.assertEqual(proof["verdict"], "pass")
        self.assertGreater(proof["relay_counter_observations"]["post"]["copy_failure"], 0)
        self.alter("post", "relay_status", lambda x: x.update(copy_failure=2))
        with self.assertRaises(profile.Incomplete): profile.validate_case(self.root, self.expected)

    def test_relay_config_duplicate_case_alias_and_hash_replacement_refuse(self):
        for raw in (b'{"schema":1,"schema":1}', b'{"Schema":1}', b'{}'):
            fixture(self.root)
            packet_path = self.root / "time_source_pre.json"; packet = json.loads(packet_path.read_text())
            packet["relay_config"] = write(Path(packet["relay_config"]["path"]), raw, True)
            write(packet_path, packet)
            with self.assertRaises(profile.Incomplete): profile.validate_case(self.root, self.expected)

    def test_relay_cleanup_has_the_same_narrow_owned_shape(self):
        source = self.root / "source"
        root = source / "testbed/bench/out/receipt_admission_lookup/20261030T000000Z"
        expected = fixture(root)
        row = json.loads((root / "time-source-evidence/pre/relay.json").read_text())
        binding = dict(expected, container_id=row["Id"], source_root=str(source), helper_sha256=None)
        self.assertTrue(profile.owned_relay(row, binding))
        for key, changed in (("Privileged", True), ("Sysctls", {"net.ipv4.ip_forward": "0"}),
                             ("NetworkMode", "host"), ("CapAdd", ["NET_ADMIN"])):
            value = copy.deepcopy(row); value["HostConfig"][key] = changed
            self.assertFalse(profile.owned_relay(value, binding))

    def test_exact_relay_memory_is_bound_in_both_lanes_and_phases(self):
        for scope in ("normal", "retained"):
            for receipt in (True, False):
                root = self.root.parent / (scope + ("-receipt" if receipt else "-canonical"))
                expected = fixture(root, {"image_scope": scope}, receipt=receipt)
                proof = profile.validate_case(root, expected)
                self.assertEqual(proof["pairing"]["relay_resources"], {
                    "nano_cpus": 1000000000, "memory": 536870912,
                    "pids": 128, "max_connections": 4096})
        for phase in ("pre", "post"):
            for memory in (134217728, None, 536870911, 1073741824, "536870912", True):
                with self.subTest(phase=phase, memory=memory):
                    fixture(self.root)
                    self.alter(phase, "relay", lambda x: x["HostConfig"].update(Memory=memory))
                    with self.assertRaises(profile.Incomplete):
                        profile.validate_case(self.root, self.expected)
            fixture(self.root)
            self.alter(phase, "relay", lambda x: x["HostConfig"].pop("Memory"))
            with self.subTest(phase=phase, missing=True), self.assertRaises(profile.Incomplete):
                profile.validate_case(self.root, self.expected)

    def test_cleanup_relay_memory_and_other_resource_limits_stay_exact(self):
        source = self.root / "source"
        root = source / "testbed/bench/out/receipt_admission_lookup/20261030T000000Z"
        expected = fixture(root)
        row = json.loads((root / "time-source-evidence/pre/relay.json").read_text())
        binding = dict(expected, container_id=row["Id"], source_root=str(source), helper_sha256=None)
        self.assertEqual(row["HostConfig"]["Memory"], 536870912)
        self.assertTrue(profile.owned_relay(row, binding))
        for key, changed in (("Memory", 134217728), ("Memory", None), ("Memory", 536870911),
                             ("Memory", "536870912"), ("NanoCpus", 2000000000),
                             ("PidsLimit", 256), ("CpusetCpus", "0-3")):
            with self.subTest(key=key, changed=changed):
                value = copy.deepcopy(row); value["HostConfig"][key] = changed
                self.assertFalse(profile.owned_relay(value, binding))
        value = copy.deepcopy(row); value["HostConfig"].pop("Memory")
        self.assertFalse(profile.owned_relay(value, binding))

    def test_actual_relay_publishers_are_required_and_servers_stay_internal(self):
        for kind in ("server-publisher", "wrong-published-port", "public-host", "protected-metrics"):
            fixture(self.root)
            if kind == "server-publisher":
                packet_path = self.root / "time_source_pre.json"; packet = json.loads(packet_path.read_text())
                ref = packet["serving"][0]; row = json.loads(Path(ref["path"]).read_text())
                row["HostConfig"]["PortBindings"] = {"6380/tcp": [{"HostIp": "127.0.0.1", "HostPort": "6380"}]}
                packet["serving"][0] = write(Path(ref["path"]), row); write(packet_path, packet)
            else:
                def change(row):
                    if kind == "wrong-published-port": row["NetworkSettings"]["Ports"]["6380/tcp"][0]["HostPort"] = "6389"
                    if kind == "public-host": row["HostConfig"]["PortBindings"]["6380/tcp"][0]["HostIp"] = "0.0.0.0"
                    if kind == "protected-metrics": row["HostConfig"]["PortBindings"]["9090/tcp"] = [{"HostIp": "127.0.0.1", "HostPort": "9390"}]
                self.alter("pre", "relay", change)
            with self.subTest(kind=kind), self.assertRaises(profile.Incomplete): profile.validate_case(self.root, self.expected)

    def test_exact_container_forwarding_settings_and_actual_zero_read_facts(self):
        self.assertEqual(profile.validate_case(self.root, self.expected)["verdict"], "pass")
        expected = {"net.ipv4.ip_forward": "0", "net.ipv6.conf.all.forwarding": "0"}
        for settings in (None, {}, {"net.ipv4.ip_forward": "0"},
                         {**expected, "net.ipv4.ip_forward": "1"},
                         {**expected, "net.ipv6.conf.all.forwarding": "1"},
                         {**expected, "net.ipv4.ip_forward": 0},
                         {**expected, "net.ipv4.conf.all.forwarding": "0"}):
            with self.subTest(settings=settings):
                fixture(self.root)
                self.alter("pre", "relay", lambda x: x["HostConfig"].update(Sysctls=settings))
                with self.assertRaises(profile.Incomplete): profile.validate_case(self.root, self.expected)
        for key, actual in (("ipv4_forwarding", "1\n"), ("ipv6_forwarding", "1\n"),
                            ("ipv6_forwarding", "absent"), ("ipv4_forwarding", "0")):
            fixture(self.root)
            self.alter("pre", "relay_status", lambda x: x.update({key: actual}))
            with self.subTest(key=key, actual=actual), self.assertRaises(profile.Incomplete):
                profile.validate_case(self.root, self.expected)


if __name__ == "__main__":
    unittest.main()
