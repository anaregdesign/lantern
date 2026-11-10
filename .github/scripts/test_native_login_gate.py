"""Exercise fixture isolation, finite required cases and source/evidence binding."""

import hashlib
import json
from contextlib import redirect_stderr, redirect_stdout
import io
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import native_login_gate as gate
from local_gate_plan import steps
from local_gate_session import ordinary_environment
from git_test_fixture import synchronous_git_maintenance


class NativeLoginGateTest(unittest.TestCase):
    def execute(self, mode, profile="native", time_source="configured-source"):
        serving = mode.startswith("serve_")
        controlled = serving and time_source == "controlled-source"
        outcome = mode.removeprefix("serve_")
        source_config = b"server unit-fixture.invalid\n"
        original_read_bytes = Path.read_bytes

        def fixture_read_bytes(path):
            if controlled and path == Path("/etc/ntp.conf"):
                raise AssertionError("controlled serving must not read NTP configuration")
            return source_config if path == Path("/etc/ntp.conf") else original_read_bytes(path)

        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory).resolve()
            root, binaries = base / "repo", base / "bin"
            root.mkdir()
            binaries.mkdir()
            (root / "source").write_text("original")
            subprocess.run(["git", "init", "-q"], cwd=root, check=True)
            synchronous_git_maintenance(root)
            for command in (["git", "add", "source"],
                            ["git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture"]):
                subprocess.run(command, cwd=root, check=True)
            log = "\n".join(f"--- PASS: {gate.ROOT_TEST}/{case} (0.01s)" for case in gate.CASES)
            log += f"\n--- PASS: {gate.ROOT_TEST} (0.1s)\nPASS\n"
            if serving:
                case = gate.SERVING_CASE if profile == "native" else gate.ROOT_TEST + "/physical_fixture_serving_export"
                marker = "native" if profile == "native" else "physical"
                ready = "accounts=2" if profile == "native" else "observer_credentials=2 writer_credentials=1"
                log = (gate.CONTROLLED_SOURCE_MARKER if controlled else gate.NORMAL_SOURCE_MARKER) + "\n"
                log += "\n".join(f"current_constructor node={i} mode={mode} passed=true" for mode in ("fresh", "resume") for i in (1, 2, 3))
                stopped_at = " stopped_at=2026-11-10T22:00:00.123456789Z" if controlled else ""
                log += f"\n{marker}_fixture_ready schema={2 if controlled else 1} {ready}\n{marker}_fixture_stopped reason=released{stopped_at}\n--- PASS: {case} (0.01s)\n--- PASS: {gate.ROOT_TEST} (0.1s)\nPASS\n"
            script = f"#!{sys.executable}\n" + '''\
import json, os
from pathlib import Path
import sys
name, args = Path(sys.argv[0]).name, sys.argv[1:]
with open(os.environ['CALLS'], 'a') as out:
    out.write(json.dumps([name, args]) + '\\n')
if name == 'go':
    if args != ['env', 'GOFLAGS']:
        binary = Path(args[args.index('-o') + 1])
        binary.write_text('test binary')
        binary.chmod(0o700)
elif args[:2] == ['image', 'inspect']:
    print('sha256:fixture')
elif args[0] == 'run':
    if os.environ['SERVING'] == '1':
        receipt = json.loads(Path(os.environ['READY_RECEIPT_SOURCE']).read_text())
        assert receipt['status'] == 'RUNNING'
        assert receipt['image'] == 'sha256:fixture'
        assert set(receipt['binaries']) == {'security.test', 'integration.test'}
        Path(os.environ['READY_RECEIPT']).write_text(json.dumps(receipt))
    text = os.environ['CASE_LOG']
    mode = os.environ['MODE'].removeprefix('serve_')
    if mode == 'missing':
        text = '\\n'.join(text.splitlines()[1:])
    if mode == 'skip':
        text += '\\n--- SKIP: fixture'
    if mode == 'changed':
        Path('source').write_text('changed while executing')
    print(text)
    if mode == 'failure':
        sys.exit(1)
'''
            for name in ("go", "docker"):
                path = binaries / name
                path.write_text(script)
                path.chmod(0o755)
            with patch.dict(os.environ, ordinary_environment(os.environ), clear=True), \
                    patch.object(Path, "read_bytes", fixture_read_bytes), patch.dict(os.environ, {"PATH": str(binaries) + os.pathsep + os.environ["PATH"],
                                         "CALLS": str(base / "calls"), "MODE": mode, "CASE_LOG": log,
                                         "SERVING": "1" if serving else "0",
                                         "READY_RECEIPT_SOURCE": str(base / "evidence/receipt.json"),
                                         "READY_RECEIPT": str(base / "ready-receipt.json"),
                                         "GOFLAGS": ""}):
                options = gate.ServingOptions("https://installed.example", 16443, 18443, profile=profile, time_source=time_source) if serving else None
                destinations = [base / "evidence"]
                if mode == "repeat":
                    destinations = []
                    saved = {}
                    for _ in range(2):
                        output = io.StringIO()
                        with patch.object(gate, "__file__", str(root / ".github/scripts/native_login_gate.py")), redirect_stdout(output):
                            gate.main(["--fresh-child", "--evidence", str(base / "evidence")])
                        receipt_path = Path(json.loads(output.getvalue())["receipt"])
                        destinations.append(receipt_path.parent)
                        saved[receipt_path] = receipt_path.read_bytes()
                    self.assertEqual(len(set(destinations)), 2)
                    self.assertTrue(all(p.parent == base / "evidence" for p in destinations))
                    for path, original in saved.items():
                        self.assertEqual(path.read_bytes(), original)
                    # Explicit single-run destinations still refuse replacement.
                    with self.assertRaises(FileExistsError):
                        gate.run(root, destinations[0])
                    self.assertEqual(next(iter(saved)).read_bytes(), next(iter(saved.values())))
                elif outcome == "pass":
                    gate.run(root, base / "evidence", options)
                else:
                    with self.assertRaises(RuntimeError):
                        gate.run(root, base / "evidence", options)
            receipt = json.loads((destinations[-1] / "receipt.json").read_text())
            self.assertEqual(receipt["status"], "PASS" if outcome in ("pass", "repeat") else "FAIL")
            calls = [json.loads(row) for row in (base / "calls").read_text().splitlines()]
            self.assertEqual([c[1] for c in calls if c[0] == "go" and c[1] != ["env", "GOFLAGS"]], [
                ["test", "-c", *(["-tags=lantern_test_time"] if not serving or controlled else []), "-o", str(destination / "build" / binary), package]
                for destination in destinations
                for package, binary in (("./server/internal/security", "security.test"), ("./tests/integration", "integration.test"))])
            run = next(args for name, args in reversed(calls) if name == "docker" and args[0] == "run")
            for flag in ("--network=bridge" if serving else "--network=none", "--read-only", "--cap-drop=ALL", "--pull=never",
                         "--security-opt=no-new-privileges", "--memory=2g", "--cpus=4", "--pids-limit=256"):
                self.assertIn(flag, run)
            self.assertNotIn("--privileged", run)
            self.assertEqual(run[run.index("--user") + 1], f"{os.getuid()}:{os.getgid()}" if serving else "65534:65534")
            environment = dict(run[i + 1].split("=", 1) for i, arg in enumerate(run) if arg == "--env")
            self.assertEqual(environment["LANTERN_CURRENT_PUBLIC_GATE"], "1")
            self.assertEqual(environment["LANTERN_NATIVE_LOGIN_GATE"], "1")
            self.assertEqual(environment["LANTERN_CURRENT_FIXTURE_EXPORTER"], "/security.test")
            self.assertNotIn("LANTERN_CURRENT_CONFIGURED_SOURCE_INTEGRATION", environment)
            if serving:
                self.assertEqual([run[i + 1] for i, arg in enumerate(run) if arg == "--mount"], [
                    *([] if controlled else ["type=bind,source=/etc/ntp.conf,target=/etc/ntp.conf,readonly"]),
                    f"type=bind,source={destinations[-1] / 'private'},target=/native-fixture"])
                self.assertEqual([run[i + 1] for i, arg in enumerate(run) if arg == "--publish"], ["127.0.0.1:18443:16443/tcp"])
                self.assertIn("lantern.task=2119-controlled-serving" if controlled else "lantern.task=1809-native-serving", run)
                self.assertIn("/work:rw,nosuid,nodev,size=512m,mode=1777", run)
                self.assertEqual(environment["LANTERN_NATIVE_FIXTURE_ORIGIN"], options.origin)
                self.assertEqual(environment["LANTERN_NATIVE_FIXTURE_PREFIX"], "orders:")
                self.assertEqual(environment["LANTERN_NATIVE_FIXTURE_EXPORT_DIR"], "/native-fixture")
                self.assertEqual(environment["LANTERN_NATIVE_FIXTURE_LIFETIME_SECONDS"], "540")
                self.assertEqual(environment["TMPDIR"], "/work")
                self.assertEqual(environment.get("LANTERN_NATIVE_FIXTURE_PROFILE", "native"), profile)
                self.assertEqual(receipt["serving_profile"], profile)
                self.assertEqual(receipt["source_kind"], time_source)
                self.assertEqual(receipt["build_kind"], "test-only" if controlled else "normal")
                self.assertEqual(receipt["build_tags"], ["lantern_test_time"] if controlled else [])
                self.assertEqual(receipt["synthetic_utc"], controlled)
                self.assertEqual(receipt["serving_lifetime_seconds"], 540)
                self.assertEqual(receipt["serving_origin_sha256"], hashlib.sha256(options.origin.encode()).hexdigest())
                ready_receipt = json.loads((base / "ready-receipt.json").read_text())
                for field in ("head", "tree", "source_sha256", "image", "binaries", "source_kind", "build_kind", "build_tags", "synthetic_utc", "serving_origin_sha256", "serving_lifetime_seconds"):
                    self.assertEqual(ready_receipt[field], receipt[field])
                if controlled:
                    self.assertNotIn("source_config_sha256", receipt)
                    self.assertEqual(environment["LANTERN_NATIVE_FIXTURE_TIME_SOURCE"], "controlled-source")
                else:
                    self.assertEqual(receipt["source_config_sha256"], hashlib.sha256(source_config).hexdigest())
                    self.assertNotIn("LANTERN_NATIVE_FIXTURE_TIME_SOURCE", environment)
                if outcome in ("pass", "changed"):
                    self.assertEqual(receipt["native_owner_constructors"], [
                        {"node": i, "phase": phase, "mode": mode}
                        for phase, mode in (("initialize", "fresh"), ("serve", "resume"))
                        for i in (1, 2, 3)
                    ])
                else:
                    self.assertNotIn("native_owner_constructors", receipt)
                self.assertIn("configured-source-integration/real provider/device acceptance NOT_RUN", receipt["proof"])
                self.assertNotIn(options.origin, (destinations[-1] / "receipt.json").read_text())
            else:
                self.assertNotIn("--mount", run)
                self.assertNotIn("--publish", run)
            self.assertEqual([run[i + 1] for i, arg in enumerate(run) if arg == "--add-host"],
                             [host + ":127.0.0.1" for host in gate.HOSTS])
            name = run[run.index("--name") + 1]
            self.assertEqual(calls[-2:], [["docker", ["rm", "--force", name]], ["docker", ["image", "rm", name]]])
            self.assertEqual(receipt["development_changes"], False)
            self.assertEqual(len(receipt["source_sha256"]), 64)
            self.assertTrue(all(len(command["sha256"]) == 64 for command in receipt["commands"]))
            for destination in destinations:
                self.assertEqual((destination / "build/Dockerfile").read_text().splitlines(), [
                    "FROM scratch",
                    "COPY --chmod=0555 security.test /security.test",
                    "COPY --chmod=0555 integration.test /integration.test",
                    'ENTRYPOINT ["/integration.test"]',
                ])
                for binary in ("security.test", "integration.test"):
                    self.assertEqual((destination / "build" / binary).stat().st_mode & 0o777, 0o700)
                for path in (destination, destination / "build", *((destination / "private",) if serving else ())):
                    self.assertEqual(path.stat().st_mode & 0o777, 0o700)
                for path in (destination / "receipt.json", *destination.glob("*.log")):
                    self.assertEqual(path.stat().st_mode & 0o777, 0o600)

    def test_pass_and_failures_preserve_isolation_and_cleanup(self):
        for mode in ("pass", "missing", "skip", "changed", "failure"):
            with self.subTest(mode=mode):
                self.execute(mode)

    def test_private_host_programs_are_executable_in_the_nonroot_image(self):
        previous = os.umask(0o077)
        try:
            self.execute("pass")
        finally:
            os.umask(previous)

    def test_repeated_local_invocations_preserve_distinct_evidence(self):
        self.execute("repeat")

    def test_normal_serving_preserves_isolation_privacy_failures_and_cleanup(self):
        for mode in ("pass", "missing", "skip", "changed", "failure"):
            with self.subTest(mode=mode):
                self.execute("serve_" + mode)

    def test_physical_serving_preserves_current_profile_isolation_and_failure_evidence(self):
        for mode in ("pass", "missing", "skip", "changed", "failure"):
            with self.subTest(mode=mode):
                self.execute("serve_" + mode, profile="physical")

    def test_controlled_serving_tags_both_binaries_without_ntp_config_and_retains_failures(self):
        for profile in ("native", "physical"):
            for mode in ("pass", "missing", "skip", "changed", "failure"):
                with self.subTest(profile=profile, mode=mode):
                    self.execute("serve_" + mode, profile=profile, time_source="controlled-source")

    def test_serving_options_are_explicit_canonical_and_bounded(self):
        valid = {"origin": "https://installed.example:18443", "listen_port": 16443, "publish_port": 18443}
        gate.ServingOptions(**valid).validate()
        for change in ({"origin": "http://installed.example"}, {"origin": "https://installed.example/"},
                       {"origin": "https://installed.example?"}, {"origin": "https://user@installed.example"},
                       {"origin": "https://Installed.example"}, {"origin": "https://installed.example:0"},
                       {"prefix": "other:"}, {"prefix": "orders:bad\n:"}, {"prefix": "orders:" + "x" * 128 + ":"},
                       {"listen_port": 443}, {"publish_port": 0}, {"lifetime_seconds": 0}, {"lifetime_seconds": 541},
                       {"profile": "unknown"}, {"profile": "physical", "prefix": "orders:custom:"},
                       {"time_source": "unknown"}):
            with self.subTest(change=change), self.assertRaises(ValueError):
                gate.ServingOptions(**(valid | change)).validate()
        for args in (["--service-origin", valid["origin"]], ["--serve"], ["--serve-controlled"],
                     ["--serve", "--serve-controlled"],
                     ["--serve", "--service-origin", valid["origin"], "--listen-port", "16443"]):
            with self.subTest(args=args), redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                gate.main(["--evidence", "unused", *args])
        with patch.object(gate, "run", return_value={"status": "PASS", "development_changes": False}) as run, redirect_stdout(io.StringIO()):
            gate.main(["--evidence", "unused", "--serve", "--service-origin", valid["origin"],
                       "--listen-port", "16443", "--publish-port", "18443", "--prefix", "orders:installed:",
                       "--lifetime-seconds", "180"])
            self.assertEqual(run.call_args.args[2], gate.ServingOptions(valid["origin"], 16443, 18443, 180, "orders:installed:"))
        with patch.object(gate, "run", return_value={"status": "PASS", "development_changes": False}) as run, redirect_stdout(io.StringIO()):
            gate.main(["--evidence", "unused", "--serve", "--serve-profile", "physical",
                       "--service-origin", valid["origin"], "--listen-port", "16443", "--publish-port", "18443"])
            self.assertEqual(run.call_args.args[2].profile, "physical")

        for profile in ("native", "physical"):
            with self.subTest(profile=profile), patch.object(gate, "run", return_value={"status": "PASS", "development_changes": False}) as run, redirect_stdout(io.StringIO()):
                gate.main(["--evidence", "unused", "--serve-controlled", "--serve-profile", profile,
                           "--service-origin", valid["origin"], "--listen-port", "16443", "--publish-port", "18443"])
                self.assertEqual(run.call_args.args[2], gate.ServingOptions(valid["origin"], 16443, 18443, profile=profile, time_source="controlled-source"))

    def test_serving_refuses_controlled_missing_or_destructive_evidence(self):
        log = gate.NORMAL_SOURCE_MARKER + "\n"
        log += "\n".join(f"current_constructor node={i} mode={mode} passed=true" for mode in ("fresh", "resume") for i in (1, 2, 3))
        log += f"\nnative_fixture_ready schema=1 accounts=2\nnative_fixture_stopped reason=timeout\n--- PASS: {gate.SERVING_CASE} (0.1s)\n--- PASS: {gate.ROOT_TEST} (0.1s)\n"
        gate.require_serving(log)
        physical = log.replace("native_fixture", "physical_fixture").replace("accounts=2", "observer_credentials=2 writer_credentials=1")
        gate.require_serving(physical, "physical")
        with self.assertRaises(RuntimeError):
            gate.require_serving(log, "physical")
        with self.assertRaises(RuntimeError):
            gate.require_serving(physical)
        for invalid in (log.replace(gate.NORMAL_SOURCE_MARKER, "test-build controlled source: fixture"),
                        log.replace("schema=1", "schema=2"),
                        log + "native_fixture_ready schema=2 accounts=2\n",
                        log + "native_fixture_stopped reason=unknown\n",
                        log + gate.CONTROLLED_SOURCE_MARKER + "\n",
                        log.replace("node=2", "node=1"), log.replace("reason=timeout", "reason=unknown"),
                        log.replace("current_constructor node=2 mode=fresh passed=true\n", ""),
                        log.replace("current_constructor node=3 mode=resume passed=true", "current_constructor node=3 mode=resume passed=false"),
                        log.replace("current_constructor node=1 mode=resume passed=true", "current_constructor node=1 mode=fresh passed=true"),
                        log.replace("current_constructor node=1 mode=fresh passed=true", "current_constructor node=1 mode=resume passed=true"),
                        log.replace("node=1 mode=fresh", "node=swap mode=fresh").replace("node=2 mode=fresh", "node=1 mode=fresh").replace("node=swap mode=fresh", "node=2 mode=fresh"),
                        log + f"--- PASS: {gate.ROOT_TEST}/{gate.CASES[0]} (0.1s)\n",
                        log + "--- SKIP: fixture\n", log + f"--- PASS: {gate.SERVING_CASE} (0.1s)\n"):
            with self.subTest(log=invalid), self.assertRaises(RuntimeError):
                gate.require_serving(invalid)

        controlled = log.replace(gate.NORMAL_SOURCE_MARKER, gate.CONTROLLED_SOURCE_MARKER).replace("schema=1", "schema=2").replace("reason=timeout", "reason=timeout stopped_at=2026-11-10T22:00:00.123456789Z")
        gate.require_serving(controlled, time_source="controlled-source")
        controlled_physical = controlled.replace("native_fixture", "physical_fixture").replace("accounts=2", "observer_credentials=2 writer_credentials=1")
        gate.require_serving(controlled_physical, "physical", "controlled-source")
        for invalid in (log, controlled.replace("schema=2", "schema=1"),
                        controlled + "native_fixture_ready schema=1 accounts=2\n",
                        controlled + gate.NORMAL_SOURCE_MARKER + "\n",
                        controlled + "test-build controlled source: another\n",
                        controlled.replace(" stopped_at=2026-11-10T22:00:00.123456789Z", ""),
                        controlled + "native_fixture_stopped reason=released stopped_at=2026-11-10T22:00:01Z\n",
                        controlled + "physical_fixture_stopped reason=unknown\n",
                        controlled.replace("node=2", "node=1"),
                        controlled.replace("reason=timeout", "reason=unknown"),
                        controlled + "--- SKIP: fixture\n",
                        controlled + f"--- PASS: {gate.ROOT_TEST}/{gate.CASES[0]} (0.1s)\n",
                        controlled_physical):
            with self.subTest(log=invalid), self.assertRaises(RuntimeError):
                gate.require_serving(invalid, time_source="controlled-source")
        with self.assertRaises(ValueError):
            gate.require_serving(controlled, time_source="unknown")

    def test_controlled_stop_timestamp_is_one_canonical_utc_observation(self):
        for timestamp in ("2026-11-10T22:00:00Z", "2026-11-10T22:00:00.1Z",
                          "2026-11-10T22:00:00.123456789Z"):
            for profile in ("native", "physical"):
                with self.subTest(timestamp=timestamp, profile=profile):
                    line = f"{profile}_fixture_stopped reason=released stopped_at={timestamp}\n"
                    self.assertEqual(gate.controlled_serving_stop(line, profile), timestamp)
        for timestamp in ("2026-02-30T22:00:00Z", "2026-11-10T25:00:00Z",
                          "2026-11-10T22:00:00+00:00", "2026-11-10T22:00:00.10Z",
                          "2026-11-10T22:00:00.000000000Z", "2026-11-10T22:00:00.1234567891Z",
                          "2026-11-10T22:00:00Z stopped_at=2026-11-10T22:00:01Z"):
            with self.subTest(timestamp=timestamp), self.assertRaises(RuntimeError):
                gate.controlled_serving_stop(f"native_fixture_stopped reason=timeout stopped_at={timestamp}\n")
        with self.assertRaises(RuntimeError):
            gate.controlled_serving_stop("physical_fixture_stopped reason=released stopped_at=2026-11-10T22:00:00Z\n")

    def test_serving_refuses_root_and_build_overrides(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "repo"
            root.mkdir()
            for time_source in ("configured-source", "controlled-source"):
                options = gate.ServingOptions("https://installed.example", 16443, 18443, time_source=time_source)
                with self.subTest(time_source=time_source):
                    with patch.object(gate.os, "getuid", return_value=0), self.assertRaises(ValueError):
                        gate.run(root, Path(directory) / "root-evidence", options)
                    with patch.object(gate.os, "getuid", return_value=501), self.assertRaises(ValueError):
                        gate.run(root, root / "private-evidence", options)
                for source in ("environment", "go environment"):
                    evidence = Path(directory) / f"{time_source}-{source}"
                    with self.subTest(time_source=time_source, source=source), patch.object(gate.os, "getuid", return_value=501), patch.dict(os.environ, {"GOFLAGS": "-tags=lantern_test_time" if source == "environment" else ""}), patch.object(gate.subprocess, "check_output", side_effect=lambda args, **kwargs: b"-tags=lantern_test_time" if args == ["go", "env", "GOFLAGS"] else "" if kwargs.get("text") else b""), self.assertRaises(ValueError):
                        gate.run(root, evidence, options)
                    self.assertEqual(json.loads((evidence / "receipt.json").read_text())["status"], "FAIL")
                    self.assertEqual(list((evidence / "build").iterdir()), [])

    def test_native_fixture_is_fresh_and_required_by_local_and_hosted_gates(self):
        plan = steps(Path("/tmp/evidence"))
        native = [step for step in plan if step.name == "native-login-wire"]
        self.assertEqual(len(native), 1)
        self.assertEqual(native[0].command, ("python3", "-B", ".github/scripts/native_login_gate.py", "--fresh-child", "--evidence", "/tmp/evidence/native-login"))
        workflow = (Path(__file__).resolve().parents[1] / "workflows/go.yml").read_text()
        body = workflow.split("      - name: Native provider session wire fixture\n", 1)[1].split("      - name:", 1)[0]
        self.assertIn("steps.toolchain.outcome == 'success'", body)
        self.assertIn('native_login_gate.py --evidence "$RUNNER_TEMP/native-login"', body)
        self.assertNotIn("continue-on-error", body)

    def test_serving_requires_existing_bounded_source_config(self):
        options = gate.ServingOptions("https://installed.example", 16443, 18443)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "repo"
            root.mkdir()
            for index, config in enumerate((None, b"", b"x" * 4097)):
                evidence = Path(directory) / f"evidence-{index}"
                reader = {"side_effect": FileNotFoundError("/etc/ntp.conf")} if config is None else {"return_value": config}
                with self.subTest(config=index), patch.object(gate.os, "getuid", return_value=501), patch.dict(os.environ, {"GOFLAGS": ""}), patch.object(gate.subprocess, "check_output", side_effect=lambda args, **kwargs: "" if kwargs.get("text") else b""), patch.object(Path, "read_bytes", **reader), self.assertRaises(FileNotFoundError if config is None else ValueError):
                    gate.run(root, evidence, options)
                receipt = json.loads((evidence / "receipt.json").read_text())
                self.assertEqual(receipt["status"], "FAIL")
                self.assertEqual(receipt["commands"], [])
                self.assertEqual(list((evidence / "build").iterdir()), [])


if __name__ == "__main__":
    unittest.main()
