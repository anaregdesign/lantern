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
        row["Mounts"] = [{"Type": "bind", "Source": "/private/example/ntp.conf", "Destination": "/etc/ntp.conf", "RW": False}]
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
    diagnostic["Mounts"] = [{"Type": "bind", "Destination": "/etc/prometheus/prometheus.yml", "RW": False}]
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
                               NanoCpus=1000000000, Memory=134217728, PidsLimit=128)
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
