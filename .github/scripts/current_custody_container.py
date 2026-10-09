#!/usr/bin/env python3
"""Bounded #1727 orderly-custody campaign; only new labeled task resources.

Three product processes, one public TLS controller, immutable local binaries and
a cached runtime image. No pulls, host clock/trust edits, capabilities, deployment
or shared-resource pruning. Named volumes are retained as private test evidence.
"""
import argparse
import datetime
import hashlib
import ipaddress
import json
import pathlib
import subprocess
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("server", "exporter", "driver", "config", "evidence"):
        parser.add_argument("--" + name, type=pathlib.Path, required=True)
    for name in ("image", "name", "head", "tree"):
        parser.add_argument("--" + name, required=True)
    args = parser.parse_args()
    if not args.image.startswith("sha256:") or len(args.image) != 71:
        parser.error("an immutable cached runtime image ID is required")
    if not args.name.startswith("lantern-1727-") or not all(c.isalnum() or c == "-" for c in args.name):
        parser.error("a new task-owned resource prefix is required")
    for value in (args.head, args.tree):
        if len(value) != 40 or any(c not in "0123456789abcdef" for c in value):
            parser.error("exact HEAD/tree required")
    args.evidence.mkdir(parents=True, exist_ok=False)
    receipt = {
        "status": "RUNNING", "started": utc(), "head": args.head, "tree": args.tree,
        "image": args.image, "scope": "three product containers; normal intact restart, partition/reconnect and pre-CLEAN kill; no physical reboot/power-loss claim",
        "binaries": {name: digest(getattr(args, name)) for name in ("server", "exporter", "driver")},
        "native_source_config_sha256": digest(args.config), "events": [], "phases": [], "volumes": {},
    }
    containers, networks = [], []
    sequence = 0

    def save():
        (args.evidence / "receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")

    def docker(*values, check=True, record=True, timeout=45, stdin=None):
        result = subprocess.run(["docker", *values], capture_output=True, text=True, timeout=timeout, input=stdin)
        if record:
            receipt["events"].append({"utc": utc(), "args": values, "exit": result.returncode, "stdout": result.stdout, "stderr": result.stderr})
            save()
        if check and result.returncode:
            raise RuntimeError(f"docker {values}: {result.stderr}")
        return result

    def inspect(name):
        return json.loads(docker("inspect", name, record=False).stdout)[0]

    def marker(name, path, timeout=150):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if not inspect(name)["State"]["Running"]:
                raise RuntimeError(f"{name} exited before {path}")
            result = docker("exec", name, "cat", path, check=False, record=False)
            if result.returncode == 0:
                return json.loads(result.stdout)
            time.sleep(0.25)
        raise RuntimeError(f"{name}: marker timeout {path}")

    def finish(name, expected=0, timeout=100):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            state = inspect(name)["State"]
            if not state["Running"]:
                if state["ExitCode"] != expected:
                    raise RuntimeError(f"{name}: exit {state['ExitCode']}, expected {expected}")
                return state
            time.sleep(0.25)
        raise RuntimeError(f"{name}: bounded process finish timed out")

    def collect(name, label):
        log = docker("logs", name, check=False, record=False)
        (args.evidence / (label + ".log")).write_text(log.stdout + log.stderr)
        record = inspect(name)
        (args.evidence / (label + "-inspect.json")).write_text(json.dumps(record, indent=2) + "\n")
        return log.stdout + log.stderr

    def make_volume(suffix):
        volume = args.name + "-" + suffix
        if docker("volume", "inspect", volume, check=False).returncode == 0:
            raise RuntimeError("refusing an existing volume: " + volume)
        docker("volume", "create", "--label", "lantern.task=1727-custody", volume)
        # Only this newly created empty volume is changed. Product workers run
        # unprivileged; neither host directories nor shared caches are touched.
        docker("run", "--rm", "--pull=never", "--network", "none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--mount", f"type=volume,src={volume},dst=/owned", args.image, "chmod", "1777", "/owned")
        docker("run", "--rm", "--pull=never", "--network", "none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user", "65534:65534", "--mount", f"type=volume,src={volume},dst=/owned", args.image, "mkdir", "-m", "700", "/owned/private")
        receipt["volumes"][suffix] = json.loads(docker("volume", "inspect", volume, record=False).stdout)[0]
        save()
        return volume

    def common(name):
        for key in ("server", "exporter", "driver"):
            if digest(getattr(args, key)) != receipt["binaries"][key]:
                raise RuntimeError("frozen binary changed: " + key)
        if digest(args.config) != receipt["native_source_config_sha256"]:
            raise RuntimeError("selected native source config changed")
        return ["run", "-d", "--pull=never", "--name", name, "--label", "lantern.task=1727-custody", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user", "65534:65534", "--pids-limit", "256", "--memory", "768m", "--cpus", "2", "--tmpfs", "/tmp:rw,nosuid,nodev,size=128m,mode=1777", "--mount", f"type=bind,src={args.config.resolve()},dst=/etc/ntp.conf,readonly"]

    def phase(name):
        nonlocal sequence
        sequence += 1
        path = args.evidence / "command.json"
        path.write_text(json.dumps({"Sequence": sequence, "Phase": name}) + "\n")
        docker("exec", "-i", controller, "sh", "-c", "cat > /control/command.next && mv /control/command.next /control/command.json", stdin=path.read_text())
        result = marker(controller, f"/control/result-{sequence:02d}.json", timeout=220)
        (args.evidence / f"phase-{sequence:02d}-{name}.json").write_text(json.dumps(result, indent=2) + "\n")
        receipt["phases"].append(result)
        save()
        if not result["pass"]:
            raise RuntimeError("public phase failed: " + name)
        print("PASS", name, flush=True)

    def start_node(node, mode, suffix="", isolated=False):
        name = args.name + f"-node-{node}" + suffix
        network = ["--network", egress] if isolated else ["--network", cluster, "--ip", hosts[node - 1]]
        values = common(name) + network + [
            "--mount", volume_mount(provision, "/provision", readonly=True),
            "--mount", volume_mount(journals[node], "/journal"),
            "--mount", volume_mount(custodies[node], "/custody"),
            "--mount", f"type=bind,src={args.server.resolve()},dst=/server,readonly",
            "--mount", f"type=bind,src={args.driver.resolve()},dst=/probe,readonly"]
        settings = {
            "LANTERN_AUTH_MODE": "oidc", "LANTERN_SECURITY_PROFILE": "current-v2",
            "LANTERN_SECURITY_CURRENT_CONFIG_FILE": f"/provision/node-{node}/node.json",
            "LANTERN_SECURITY_STORE_MODE": mode, "LANTERN_OIDC_BROWSER_ORIGIN": "https://admin.example",
            "LANTERN_OIDC_ROOT_CA_FILE": "/provision/oidc.pem",
            "LANTERN_OIDC_PRIVATE_ORIGINS": json.dumps({issuer: [driver_ip + "/32"]}, separators=(",", ":")),
            "LANTERN_TLS_CERT_FILE": "/provision/public.pem", "LANTERN_TLS_KEY_FILE": "/provision/public.key",
            "LANTERN_PORT": "6380", "LANTERN_METRICS_ADDR": "", "LANTERN_NODE_ID": f"{node:032x}",
            "LANTERN_RECEIPT_WAL_MODE": "fresh" if mode == "fresh" else "restart",
            "LANTERN_RECEIPT_WAL_PATH": "/journal/business.wal", "LANTERN_RECEIPT_EPOCH": "42424242424242424242424242424242",
            "LANTERN_RECEIPT_RETENTION": "1h", "LANTERN_RECEIPT_MAX_ENTRIES": "128", "LANTERN_RECEIPT_MAX_BYTES": "1048576",
            "LANTERN_BACKUP_ENABLED": "false", "LANTERN_BACKUP_RESTORE_ON_START": "false", "LANTERN_DRAIN_DELAY_SECONDS": "1",
            "LANTERN_CURRENT_CUSTODY_GATE": "1", "LANTERN_CURRENT_CUSTODY_NODE_URL": f"https://{hosts[node-1]}:6380",
        }
        for key, value in settings.items():
            values += ["--env", key + "=" + value]
        docker(*values, args.image, "/server")
        containers.append(name)
        if node == 3 and not isolated:
            docker("network", "connect", "--gw-priority", "-1", egress, name)
        return name

    def checkpoint(name, label, cycle):
        target = args.evidence / label
        target.mkdir()
        for filename in ("floors.json", "floors.json.state"):
            docker("cp", f"{name}:/custody/{filename}", str(target / filename))
        checked_checkpoint(target / "floors.json.state", target / "floors.json", cycle)
        log = collect(name, label)
        if "server stopped cleanly" not in log:
            raise RuntimeError("process did not report success after checkpoint: " + label)
        receipt["phases"].append({"phase": label, "pass": True, "cycle": cycle, "floor_sha256": digest(target / "floors.json"), "state_sha256": digest(target / "floors.json.state")})
        save()

    def stop_node(node, cycle, label):
        name = nodes[node]
        docker("stop", "--timeout", "25", name, timeout=35)
        finish(name)
        checkpoint(name, label, cycle)
        docker("rm", name)
        containers.remove(name)

    def local_probe(label):
        result = docker("exec", nodes[3], "/probe", "-test.v", "-test.run=^TestCurrentCustodyLocalProbe$", "-test.timeout=90s", check=False, record=False, timeout=95)
        (args.evidence / (label + ".log")).write_text(result.stdout + result.stderr)
        if result.returncode or "--- PASS: TestCurrentCustodyLocalProbe" not in result.stdout or "--- SKIP:" in result.stdout:
            raise RuntimeError("isolated local TLS probe failed: " + label)
        receipt["phases"].append({"phase": label, "pass": True, "exit": result.returncode})
        save()

    try:
        docker("image", "inspect", args.image)
        cluster, egress = args.name + "-cluster", args.name + "-time-egress"
        for network in (cluster, egress):
            if docker("network", "inspect", network, check=False).returncode == 0:
                raise RuntimeError("refusing an existing network")
            docker("network", "create", "--label", "lantern.task=1727-custody", network)
            networks.append(network)
        network_info = json.loads(docker("network", "inspect", cluster, record=False).stdout)[0]
        subnet = ipaddress.ip_network(network_info["IPAM"]["Config"][0]["Subnet"])
        driver_ip = str(subnet.network_address + 10)
        hosts = [str(subnet.network_address + n) for n in (11, 12, 13)]
        receipt["network"] = network_info
        provision, control = make_volume("provision"), make_volume("control")
        journals = {node: make_volume(f"node-{node}-journal") for node in (1, 2, 3)}
        custodies = {node: make_volume(f"node-{node}-custody") for node in (1, 2, 3)}
        controller = args.name + "-controller"
        docker(*(common(controller) + ["--network", cluster, "--ip", driver_ip,
            "--mount", volume_mount(provision, "/provision"), "--mount", volume_mount(control, "/control"),
            "--mount", f"type=bind,src={args.exporter.resolve()},dst=/exporter,readonly", "--mount", f"type=bind,src={args.driver.resolve()},dst=/driver,readonly",
            "--tmpfs", "/journal:rw,nosuid,nodev,size=1m,mode=1777", "--tmpfs", "/custody:rw,nosuid,nodev,size=1m,mode=1777",
            "--env", "LANTERN_CURRENT_CUSTODY_GATE=1", "--env", "LANTERN_CURRENT_FIXTURE_HOSTS=" + ",".join(hosts),
            "--env", "LANTERN_CURRENT_CUSTODY_DRIVER=" + driver_ip]), args.image, "/driver", "-test.v", "-test.run=^TestCurrentCustodyProcessGate$", "-test.timeout=20m")
        containers.append(controller)
        initialized = marker(controller, "/control/initialized.json")
        issuer = initialized["issuer"]
        docker("cp", controller + ":/control/exporter.log", str(args.evidence / "exporter.log"))
        docker("cp", controller + ":/control/provision-manifest.json", str(args.evidence / "provision-manifest.json"))
        # Native constructor failure proves RUNNING precedes even M resume/open
        # ownership. Its separate disposable volumes are never repaired/reused.
        barrier = args.name + "-running-barrier"
        barrier_journal, barrier_custody = make_volume("barrier-journal"), make_volume("barrier-custody")
        docker(*(common(barrier) + ["--network", cluster, "--ip", hosts[0],
            "--mount", volume_mount(provision, "/provision", readonly=True), "--mount", volume_mount(barrier_journal, "/journal"), "--mount", volume_mount(barrier_custody, "/custody"),
            "--mount", f"type=bind,src={args.exporter.resolve()},dst=/exporter,readonly",
            "--env", "LANTERN_CURRENT_CUSTODY_GATE=1", "--env", "LANTERN_SECURITY_CURRENT_CONFIG_FILE=/provision/node-1/node.json"]), args.image, "/exporter", "-test.v", "-test.run=^TestCurrentCustodyRunningBarrierNativeGate$", "-test.timeout=70s")
        containers.append(barrier)
        finish(barrier, timeout=80)
        if "--- PASS: TestCurrentCustodyRunningBarrierNativeGate" not in collect(barrier, "running-barrier"):
            raise RuntimeError("native RUNNING barrier test not executed")
        docker("rm", barrier)
        containers.remove(barrier)
        nodes = {1: start_node(1, "fresh")}
        phase("single-before-quorum")
        for node in (2, 3):
            nodes[node] = start_node(node, "fresh")
        phase("fresh")
        for cycle in (1, 2):
            for node in (1, 2, 3):
                stop_node(node, cycle, f"cycle-{cycle}-node-{node}")
            # First resumed node has intact CLEAN/floors but cannot reuse any
            # prior serving permission while the other two processes are absent.
            nodes[1] = start_node(1, "resume")
            phase("single-before-quorum")
            for node in (2, 3):
                nodes[node] = start_node(node, "resume")
            phase("resume-one" if cycle == 1 else "resume-two")
        docker("network", "disconnect", cluster, nodes[3])
        before = time.monotonic_ns()
        time.sleep(18)
        receipt["first_partition_ns"] = time.monotonic_ns() - before
        local_probe("partition-expiry-refusal")
        phase("partition-change")
        docker("network", "connect", "--ip", hosts[2], cluster, nodes[3])
        phase("reconnected")
        docker("network", "disconnect", cluster, nodes[3])
        stop_node(3, 3, "isolated-orderly-node-3")
        nodes[3] = start_node(3, "resume", isolated=True)
        local_probe("isolated-resume-refusal")
        state = marker(nodes[3], "/custody/floors.json.state")
        if state["Phase"] != "RUNNING" or state["Cycle"] != 4:
            raise RuntimeError("isolated resume did not consume CLEAN normally")
        docker("network", "connect", "--ip", hosts[2], cluster, nodes[3])
        phase("isolated-restart-reconnected")
        docker("kill", "--signal", "KILL", nodes[3])
        finish(nodes[3], expected=137)
        collect(nodes[3], "pre-clean-kill")
        docker("cp", nodes[3] + ":/custody/floors.json.state", str(args.evidence / "killed-state.json"))
        if json.loads((args.evidence / "killed-state.json").read_text())["Phase"] != "RUNNING":
            raise RuntimeError("kill did not precede CLEAN")
        docker("rm", nodes[3])
        containers.remove(nodes[3])
        nodes[3] = start_node(3, "resume", "-refusal", isolated=True)
        finish(nodes[3], expected=1)
        log = collect(nodes[3], "killed-resume-refusal")
        if "custody is not a valid orderly restart checkpoint" not in log or "server stopped cleanly" in log:
            raise RuntimeError("killed resume failed for an unrelated reason")
        for node in (1, 2):
            stop_node(node, 3, f"final-normal-node-{node}")
        phase("finish")
        finish(controller)
        log = collect(controller, "controller")
        if "--- PASS: TestCurrentCustodyProcessGate" not in log or "--- SKIP:" in log:
            raise RuntimeError("controller did not complete all required phases")
        receipt["status"], receipt["skips"] = "PASS", 0
        receipt["counts"] = {"controller_phases": sequence, "orderly_checkpoints": 9, "native_running_barrier": 1, "isolated_tls_probes": 2, "sigkill_exit_137": 1, "running_resume_exit_1": 1}
        if sequence != 10:
            raise RuntimeError("required controller phase count changed")
    except BaseException as exc:
        receipt["status"], receipt["error"] = "FAIL", repr(exc)
        print("FAIL", repr(exc), flush=True)
    finally:
        for name in reversed(containers):
            try:
                if inspect(name)["State"].get("Running"):
                    docker("kill", name, check=False)
                collect(name, name + "-final")
                docker("rm", name, check=False)
            except Exception as exc:
                receipt.setdefault("cleanup_errors", []).append(repr(exc))
        for network in reversed(networks):
            docker("network", "rm", network, check=False)
        receipt["finished"], receipt["private_volumes_retained"] = utc(), True
        receipt["artifacts"] = {p.name: digest(p) for p in sorted(args.evidence.iterdir()) if p.is_file() and p.name != "receipt.json"}
        save()
    return 0 if receipt["status"] == "PASS" else 1


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def volume_mount(volume, destination, readonly=False):
    return f"type=volume,src={volume},dst={destination},volume-subpath=private" + (",readonly" if readonly else "")


def checked_checkpoint(state_path, floor_path, cycle):
    state = json.loads(state_path.read_text())
    if state.get("Version") != 1 or state.get("Phase") != "CLEAN" or state.get("Cycle") != cycle or bytes(state.get("Floors", [])).hex() != digest(floor_path):
        raise RuntimeError("incomplete normal checkpoint: " + str(state_path))
    return state


def utc():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


if __name__ == "__main__":
    raise SystemExit(main())
