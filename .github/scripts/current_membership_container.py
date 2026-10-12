#!/usr/bin/env python3
"""Bounded same-host membership publication through read-only directory mounts.

Uses one cached image, frozen binaries, synthetic fixture inputs, three product
containers and the existing TLS driver. Keeps private evidence on failure. No
image pull, production provisioning, clock/trust change, hard kill or auto-init.
"""
import argparse
import base64
import datetime
import hashlib
import ipaddress
import json
import os
import pathlib
import platform
import signal
import subprocess
import time


def utc():
    return datetime.datetime.now(datetime.timezone.utc)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def manifest(path):
    raw = path.read_bytes()
    if raw.startswith(b"LNPMS001"):
        raw = raw[8:]
    payload = json.loads(raw)["payload"]
    return payload["version"], datetime.datetime.fromisoformat(payload["expires_at"].replace("Z", "+00:00")), hashlib.sha256(raw).hexdigest()


def clean_checkpoint(directory):
    floors = directory / "floors.json"
    state = json.loads((directory / "floors.json.state").read_text())
    if state.get("Phase") != "CLEAN" or bytes(state.get("Floors", [])).hex() != digest(floors):
        raise RuntimeError("CLEAN floor hash mismatch")
    return {"clean": True, "floor_sha256": digest(floors), "cycle": state["Cycle"]}


def bind(source, target, readonly=False):
    return ["--mount", f"type=bind,src={source},dst={target}" + (",readonly" if readonly else "")]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("server", "publisher", "exporter", "driver", "config", "evidence"):
        parser.add_argument("--" + name, type=pathlib.Path, required=True)
    for name in ("image", "name", "head", "tree"):
        parser.add_argument("--" + name, required=True)
    args = parser.parse_args()
    if not args.name.startswith("lantern-1742-") or not all(c.isalnum() or c == "-" for c in args.name):
        parser.error("new task-owned resource name required")
    if not args.image.startswith("sha256:") or len(args.image) != 71:
        parser.error("immutable cached runtime image ID required")
    for value in (args.head, args.tree):
        if len(value) != 40 or any(c not in "0123456789abcdef" for c in value):
            parser.error("exact source head/tree required")
    if os.getuid() == 0:
        parser.error("nonroot host publisher required for the directory-write fault")
    args.evidence = args.evidence.resolve()
    args.evidence.mkdir(mode=0o700, parents=True, exist_ok=False)
    provision, control = args.evidence / "provision", args.evidence / "control"
    for path in (provision, control):
        path.mkdir(mode=0o700)
    journals, custody = {}, {}
    for node in (1, 2, 3):
        journals[node], custody[node] = args.evidence / f"journal-{node}", args.evidence / f"custody-{node}"
        journals[node].mkdir(mode=0o700)
        custody[node].mkdir(mode=0o700)
    receipt = {"status": "RUNNING", "started": utc().isoformat(), "head": args.head, "tree": args.tree,
               "image": args.image, "host_os": platform.system(), "scope": "same-host private directory publication; real native authority; bounded process lifecycle",
               "binaries": {n: digest(getattr(args, n)) for n in ("server", "publisher", "exporter", "driver")},
               "native_source_config_sha256": digest(args.config), "phases": [], "events": []}
    containers, nodes = [], {}
    network, publisher, logs, sequence, held = args.name + "-net", None, [], 0, None
    created_network = False
    deadline = time.monotonic() + 480

    def save():
        (args.evidence / "receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")

    def docker(*values, check=True):
        process = subprocess.Popen(["docker", *values], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            stdout, stderr = process.communicate(timeout=45)
        except subprocess.TimeoutExpired:
            process.terminate()
            try:
                process.communicate(timeout=5)
            except subprocess.TimeoutExpired:
                receipt["events"].append({"docker_cli_join_uncertain_pid": process.pid})
                save()
            raise RuntimeError("owned Docker CLI timed out; no hard kill issued")
        result = subprocess.CompletedProcess(process.args, process.returncode, stdout, stderr)
        if check and result.returncode:
            receipt["events"].append({"command": list(values), "exit": result.returncode, "stderr": result.stderr})
            save()
            raise RuntimeError("owned Docker operation failed; see private receipt")
        return result

    def inspect(name):
        return json.loads(docker("inspect", name).stdout)[0]

    def wait_for(predicate, seconds=100):
        end = min(deadline, time.monotonic() + seconds)
        while time.monotonic() < end:
            if predicate():
                return
            time.sleep(0.2)
        raise RuntimeError("bounded condition timed out")

    def finish(name):
        wait_for(lambda: not inspect(name)["State"]["Running"], 45)
        if inspect(name)["State"]["ExitCode"] != 0:
            raise RuntimeError("owned process did not exit normally")

    def common(name, entrypoint):
        for key in ("server", "publisher", "exporter", "driver"):
            if digest(getattr(args, key)) != receipt["binaries"][key]:
                raise RuntimeError("frozen binary changed")
        return ["run", "-d", "--pull=never", "--name", name, "--label", "lantern.task=1742-membership",
                "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user", f"{os.getuid()}:{os.getgid()}",
                "--pids-limit", "256", "--memory", "768m", "--tmpfs", "/tmp:rw,nosuid,nodev,size=128m,mode=1777",
                "--entrypoint", entrypoint] + bind(args.config.resolve(), "/etc/ntp.conf", True)

    def phase(name):
        nonlocal sequence
        sequence += 1
        pending = control / "command.next"
        pending.write_text(json.dumps({"Sequence": sequence, "Phase": name}))
        pending.replace(control / "command.json")
        result = control / f"result-{sequence:02d}.json"
        wait_for(result.is_file, 110)
        row = json.loads(result.read_text())
        receipt["phases"].append(row)
        save()
        if not row["pass"]:
            raise RuntimeError("public membership phase failed")
        print("PASS", name, flush=True)

    def start_publisher(config, mode):
        nonlocal publisher
        if digest(args.publisher) != receipt["binaries"]["publisher"]:
            raise RuntimeError("publisher binary changed")
        log = (args.evidence / f"publisher-{len(logs)}.log").open("xb")
        logs.append(log)
        publisher = subprocess.Popen([str(args.publisher.resolve()), "-config", str(config), "-state", str(args.evidence / "publisher-state.json"), "-mode", mode], stdout=log, stderr=log)
        receipt["events"].append({"publisher_started": publisher.pid, "mode": mode})
        save()

    def stop_publisher():
        nonlocal publisher
        if publisher is None:
            return
        if publisher.poll() is None:
            publisher.send_signal(signal.SIGTERM)
        code = publisher.wait(timeout=25)
        publisher = None
        if code != 0:
            raise RuntimeError("publisher normal stop failed")

    def start_node(node, mode):
        name = args.name + f"-node-{node}-" + mode
        values = common(name, "/server") + ["--network", network, "--ip", hosts[node-1]]
        values += bind(args.server.resolve(), "/server", True)
        values += bind(provision / f"node-{node}", f"/provision/node-{node}", True)
        for filename in ("genesis.json", "oidc.pem", "public.pem", "public.key", "cursor-keys.json", "data-operator.pub"):
            values += bind(provision / filename, "/provision/" + filename, True)
        values += bind(journals[node], "/journal") + bind(custody[node], "/custody")
        peer = json.loads((provision / f"node-{node}" / "peer.json").read_text())["Identity"]
        env = {"LANTERN_AUTH_MODE": "oidc", "LANTERN_SECURITY_PROFILE": "current-v2", "LANTERN_SECURITY_STORE_MODE": mode,
               "LANTERN_SECURITY_CURRENT_CONFIG_FILE": f"/provision/node-{node}/node.json", "LANTERN_OIDC_BROWSER_ORIGIN": "https://admin.example",
               "LANTERN_OIDC_ROOT_CA_FILE": "/provision/oidc.pem", "LANTERN_OIDC_PRIVATE_ORIGINS": json.dumps({issuer: [driver_ip+"/32"]}),
               "LANTERN_TLS_CERT_FILE": "/provision/public.pem", "LANTERN_TLS_KEY_FILE": "/provision/public.key", "LANTERN_CDC_CURSOR_KEY_RING_FILE": "/provision/cursor-keys.json",
               "LANTERN_PORT": "6380", "LANTERN_METRICS_ADDR": "127.0.0.1:9090", "LANTERN_NODE_ID": f"{node:032x}",
               "LANTERN_RECEIPT_WAL_MODE": "fresh" if mode == "fresh" else "restart", "LANTERN_RECEIPT_WAL_PATH": "/journal/business.wal",
               "LANTERN_RECEIPT_EPOCH": "42424242424242424242424242424242", "LANTERN_RECEIPT_RETENTION": "1h", "LANTERN_RECEIPT_MAX_ENTRIES": "128", "LANTERN_RECEIPT_MAX_BYTES": "1048576",
               "LANTERN_BACKUP_ENABLED": "false", "LANTERN_BACKUP_RESTORE_ON_START": "false", "LANTERN_DRAIN_DELAY_SECONDS": "1",
               "LANTERN_PEER_LISTEN_ADDR": "0.0.0.0:17380", "LANTERN_PEER_DEPLOYMENT": bytes(peer["Deployment"]).hex(),
               "LANTERN_PEER_MEMBERSHIP_MODE": mode, "LANTERN_PEER_MEMBERSHIP_STATE_FILE": "/journal/data-membership",
               "LANTERN_PEER_MEMBERSHIP_FILE": peer["ManifestFile"], "LANTERN_PEER_OPERATOR_PUBLIC_KEY_FILE": peer["OperatorKeyFile"],
               "LANTERN_PEER_WORKLOAD_ID": peer["SelfIdentity"], "LANTERN_PEER_CERT_FILE": peer["CertFile"], "LANTERN_PEER_KEY_FILE": peer["KeyFile"], "LANTERN_PEER_TRUST_CA_FILE": peer["CAFile"]}
        for key, value in env.items():
            values += ["--env", key+"="+value]
        docker(*values, args.image)
        containers.append(name)
        nodes[node] = name
        for path in ("/provision/control-operator.key", "/provision/data-operator.key", "/publisher-state.json", f"/provision/node-{node%3+1}/tls.key"):
            if docker("exec", name, "test", "-e", path, check=False).returncode != 1:
                raise RuntimeError("operator/foreign private input isolation not established")

    def consumed(minimum):
        try:
            rows = []
            for node in (1, 2, 3):
                for name in ("membership", "data-membership"):
                    version, _, sha = manifest(journals[node] / name)
                    if version < minimum:
                        return False
                    rows.append({"node": node, "stream": name, "version": version, "sha256": sha})
            receipt["latest_consumed"] = rows
            return True
        except (FileNotFoundError, json.JSONDecodeError):
            return False

    def stop_node(node):
        name = nodes[node]
        docker("kill", "--signal", "TERM", name)
        finish(name)
        proof = clean_checkpoint(custody[node])
        floors = json.loads((custody[node] / "floors.json").read_text())
        version, _, sha = manifest(journals[node] / "membership")
        if version < 3 or floors["M"]["Version"] != version or bytes(floors["M"]["Digest"]).hex() != sha:
            raise RuntimeError("CLEAN did not retain latest accepted membership")
        if proof["cycle"] != (2 if name.endswith("-resume") else 1):
            raise RuntimeError("unexpected custody cycle")
        proof["control_version"] = version
        result = docker("logs", name, check=False)
        (args.evidence / (name+".log")).write_text(result.stdout+result.stderr)
        receipt["phases"].append({"phase": "node-clean", "node": node, **proof})
        save()
        docker("rm", name)
        containers.remove(name)

    try:
        docker("image", "inspect", args.image)
        if docker("network", "inspect", network, check=False).returncode == 0:
            raise RuntimeError("refusing existing network")
        docker("network", "create", "--label", "lantern.task=1742-membership", network)
        created_network = True
        subnet = ipaddress.ip_network(json.loads(docker("network", "inspect", network).stdout)[0]["IPAM"]["Config"][0]["Subnet"])
        driver_ip = str(subnet.network_address+10)
        hosts = [str(subnet.network_address+n) for n in (11, 12, 13)]
        controller = args.name + "-driver"
        values = common(controller, "/driver") + ["--network", network, "--ip", driver_ip]
        values += bind(provision, "/provision") + bind(control, "/control") + bind(args.exporter.resolve(), "/exporter", True) + bind(args.driver.resolve(), "/driver", True)
        values += ["--tmpfs", "/journal:rw,size=1m,mode=1777", "--tmpfs", "/custody:rw,size=1m,mode=1777",
                   "--env", "LANTERN_CURRENT_CUSTODY_GATE=1", "--env", "LANTERN_CURRENT_MEMBERSHIP_CONTAINER=1",
                   "--env", "LANTERN_CURRENT_FIXTURE_HOSTS="+",".join(hosts), "--env", "LANTERN_CURRENT_CUSTODY_DRIVER="+driver_ip]
        docker(*values, args.image, "-test.v", "-test.run=^TestCurrentCustodyProcessGate$", "-test.timeout=8m")
        containers.append(controller)
        wait_for((control / "initialized.json").is_file, 75)
        issuer = json.loads((control / "initialized.json").read_text())["issuer"]
        config = json.loads((provision / "publisher.json").read_text())
        for stream in config["Streams"]:
            for key in ("KeyFile", "RootsFile"):
                stream[key] = str(provision / pathlib.PurePosixPath(stream[key]).relative_to("/provision"))
            for key in ("Targets", "Certificates"):
                stream[key] = [str(provision / pathlib.PurePosixPath(x).relative_to("/provision")) for x in stream[key]]
        # Preserve the ordinary bootstrap interval through native startup.
        # Only then begin the publisher's short successor intervals.
        for node in (1, 2, 3):
            start_node(node, "fresh")
        phase("ready")
        initial_end = utc() + datetime.timedelta(seconds=130)
        config["End"], config["CleanupBy"] = initial_end.isoformat(), (initial_end+datetime.timedelta(seconds=60)).isoformat()
        config_path = args.evidence / "window-1.json"
        config_path.write_text(json.dumps(config)); config_path.chmod(0o600)
        start_publisher(config_path, "init")
        wait_for(lambda: consumed(3))
        # Intact publisher stop, explicit advance, unchanged exact candidates.
        stop_publisher()
        state_path = args.evidence / "publisher-state.json"
        before = json.loads(state_path.read_text())
        next_config = dict(config)
        next_end = initial_end+datetime.timedelta(seconds=240)
        next_config["End"], next_config["CleanupBy"] = next_end.isoformat(), (next_end+datetime.timedelta(seconds=60)).isoformat()
        next_path = args.evidence / "window-2.json"
        next_path.write_text(json.dumps(next_config)); next_path.chmod(0o600)
        with (args.evidence / "advance.log").open("xb") as output:
            publisher = subprocess.Popen([str(args.publisher.resolve()), "-mode", "advance-window", "-config", str(config_path), "-next-config", str(next_path), "-state", str(state_path)], stdout=output, stderr=output)
            receipt["events"].append({"advance_started": publisher.pid})
            save()
            if publisher.wait(timeout=20):
                raise RuntimeError("explicit advance failed")
            publisher = None
        after = json.loads(state_path.read_text())
        if before["Binding"] == after["Binding"] or {k:v for k,v in before.items() if k!="Binding"} != {k:v for k,v in after.items() if k!="Binding"}:
            raise RuntimeError("advance changed more than binding")
        receipt["binding_only_advance"] = True
        start_publisher(next_path, "resume")
        held = provision / "node-1" / "data-input"
        held.chmod(0o500)
        wait_for(lambda: json.loads(state_path.read_text())["Streams"][1]["Pending"], 20)
        pending = json.loads(state_path.read_text())["Streams"][1]
        payload = json.loads(base64.b64decode(pending["Current"]))["payload"]
        pending_end = datetime.datetime.fromisoformat(payload["expires_at"].replace("Z", "+00:00"))
        wait_for(lambda: utc() > pending_end+datetime.timedelta(seconds=1), 45)
        if json.loads(state_path.read_text())["Streams"][1]["Current"] != pending["Current"]:
            raise RuntimeError("pending revision was re-signed")
        if json.loads(state_path.read_text())["Streams"][0]["Version"] <= before["Streams"][0]["Version"]:
            raise RuntimeError("pending data blocked independent control publication")
        for node in (2, 3):
            if (provision / f"node-{node}" / "data-input" / "data.signed").read_bytes() != base64.b64decode(pending["Current"]):
                raise RuntimeError("other target did not receive exact retained candidate")
        phase("delayed")
        held.chmod(0o700); held = None
        phase("recovered")
        wait_for(lambda: consumed(pending["Version"]+1), 45)
        wait_for(lambda: utc() > initial_end+datetime.timedelta(seconds=1), 140)
        phase("crossed-window")
        wait_for(lambda: consumed(pending["Version"]+2), 45)
        stop_node(2)
        saved_floor = digest(custody[2] / "floors.json")
        start_node(2, "resume")
        phase("resumed")
        receipt["resumed_clean_floor_sha256"] = saved_floor
        for node in (1, 2, 3):
            stop_node(node)
        stop_publisher()
        phase("finish")
        finish(controller)
        receipt["status"] = "PASS"
    except Exception as exc:
        receipt["status"] = "FAIL"
        receipt["failure"] = str(exc)
        raise
    finally:
        if held is not None:
            held.chmod(0o700)
        # Keep publication available until serving owners have joined. Stop
        # the controller last; it is not a substitute for node custody proof.
        for name in sorted(containers, key=lambda n: n.endswith("-driver")):
            try:
                if inspect(name)["State"]["Running"]:
                    docker("kill", "--signal", "TERM", name)
                    # Cleanup has its own finite observation allowance; no KILL.
                    end = time.monotonic()+30
                    while inspect(name)["State"]["Running"] and time.monotonic()<end:
                        time.sleep(0.2)
                result = docker("logs", name, check=False)
                (args.evidence / (name+".log")).write_text(result.stdout+result.stderr)
                state = inspect(name)["State"]
                receipt["events"].append({"container": name, "running": state["Running"], "exit": state["ExitCode"]})
                if receipt["status"] == "PASS" and not state["Running"]:
                    docker("rm", name)
            except Exception as exc:
                receipt["events"].append({"container": name, "collection_failure": str(exc)})
                receipt["status"] = "FAIL"
        try:
            stop_publisher()
        except Exception as exc:
            receipt["publisher_stop_failure"] = str(exc)
            receipt["status"] = "FAIL"
        for log in logs:
            log.close()
        if receipt["status"] == "PASS" and created_network:
            docker("network", "rm", network)
        receipt["finished"] = utc().isoformat()
        receipt["private_log_hashes"] = {p.name: digest(p) for p in args.evidence.glob("*.log")}
        save()
    return 0 if receipt["status"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
