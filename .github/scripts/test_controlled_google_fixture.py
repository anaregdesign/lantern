"""UNIT-only controlled Google profile contracts; no provider/native/Docker work."""

import contextlib
import copy
import io
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock

import controlled_google_fixture as profile


def unit_ref(name):
    return {"path": "/UNIT_NOT_ACTUAL/" + name, "sha256": "d" * 64}


def unit_plan(output="/UNIT_NOT_ACTUAL/output"):
    return {"schema": 1, "kind": "controlled-real-google-plan",
            "source": {"head": "a" * 40, "tree": "b" * 40, "source_sha256": "c" * 64,
                       "proof": unit_ref("source-proof"), "review": unit_ref("source-review")},
            "build": {"image": "sha256:" + "e" * 64, "receipt": unit_ref("build"), "description": unit_ref("description"), "public_roots": unit_ref("public-roots")},
            "seed": {"material": unit_ref("seed-material"), "verification": unit_ref("seed-verification")},
            "clients": {key: "123456789012-unit" + key + ".apps.googleusercontent.com" for key in ("web", "android", "ios")},
            "profile_ids": {"android": "google_android", "ios": "google_ios"},
            "public": {"origin": "https://unit.invalid:24445", "lan_ip": "192.168.1.8", "certificate": unit_ref("certificate"), "key": unit_ref("tls-key")},
            "network": {"id": "f" * 64, "name": "UNIT-dedicated-bridge", "admission": unit_ref("network")},
            "output": output, "cohort": "1" * 64, "lifetime_seconds": 30,
            "planned_stop_at": "2026-11-11T00:00:30Z", "cleanup_by": "2026-11-11T00:00:50Z"}


def unit_counts():
    return {"attempts": 1, "writes": 1, "installations": 1, "in_flight": False, "failed": False, "denied": False}


def unit_records(plan, mode, node="node-1", joined=False):
    base = {"profile": profile.PROFILE, "nonce": plan["cohort"], "revision": plan["source"]["head"], "mode": mode,
            "pid": 7, "node": node, "config_sha256": "2" * 64, "authority_profile": "current-v2:" + "5" * 64}
    first = {**base, "event": "initialized" if mode == "initialize" else "constructed", "counters": unit_counts(),
             "host_seed_interval_from": "2026-11-11T00:00:00Z", "host_seed_interval_until": "2026-11-11T00:00:01Z"}
    if mode == "initialize":
        return [{**first, "ok": True}]
    values = [first, {**base, "event": "ready", "ready_at": "2026-11-11T00:00:01Z", "planned_stop_at": plan["planned_stop_at"]}]
    if joined:
        values += [{**base, "event": "joined", "ok": True, "counters": unit_counts(), "repeat": unit_counts(), "stopped_at": plan["planned_stop_at"]},
                   {"kind": "controlled_public_trace", "events": [], "dropped": 0}]
    return values


def lines(values):
    return b"".join(profile.encoded(value) for value in values)


class UnitClock:
    def __init__(self):
        self.elapsed = 0
        self.wall_offset = 0

    def wall(self):
        return profile.utc("2026-11-11T00:00:00Z") + self.elapsed + self.wall_offset

    def monotonic(self):
        return self.elapsed

    def sleep(self, seconds):
        self.elapsed += seconds


class UnitCommands:
    """Fake owned processes only. No subprocess, listener, provider or native owner."""
    def __init__(self, output, window, plan, clock, failure=None):
        self.output, self.window, self.plan, self.clock = output, window, plan, clock
        self.rows, self.owned = [], {}
        self.cleanup, self.stopped, self.failure = False, False, failure

    def run(self, args, timeout=20, check=True):
        bounded = self.window.command_timeout(timeout, self.cleanup)
        self.rows.append({"argv": args, "timeout_seconds": bounded, "cleanup_phase": self.cleanup, "exit": 0})
        self.clock.elapsed += 0.01
        verb = args[3]
        if verb == "image":
            if ".Config.Entrypoint" in args[-2]:
                return profile.encoded({"entrypoint": ["/UNIT-wrong-entry"] if self.failure == "image_entrypoint" else ["/controlled-google.test"],
                                        "cmd": ["UNIT_EXTRA_ARG"] if self.failure == "image_cmd" else None,
                                        "env_count": 1 if self.failure == "image_env" else 0}), 0
            return self.plan["build"]["image"].encode(), 0
        if verb == "network":
            if args[-2] == "{{.Name}}":
                return (b"UNIT-wrong-network" if self.failure == "network_name" else self.plan["network"]["name"].encode()), 0
            return (b"{}" if ".Containers" in args[-2] else self.plan["network"]["id"].encode()), 0
        if verb == "create":
            name = args[args.index("--name") + 1]
            if self.failure == "partial_create" and name.endswith("-initialize-1"):
                self.rows[-1]["exit"] = None
                raise ValueError("UNIT simulated CLI timeout with unknown daemon create")
            identity = profile.digest(name.encode())
            self.owned[identity] = {"name": name, "env": args[args.index("--env-file") + 1]}
            return identity.encode(), 0
        if verb == "stop":
            self.stopped = True
            if self.failure == "cleanup_deadline":
                self.clock.elapsed += 60
            return b"", 0
        if verb == "inspect":
            return profile.encoded({"Running": False, "Pid": 0, "Restarting": False, "Paused": False,
                                    "Status": "exited", "ExitCode": 0, "OOMKilled": False, "Error": ""}), 0
        child = self.owned[args[-1]]
        if "-description-" in child["name"]:
            return profile.encoded({"kind": "UNIT_ONLY_SAME_IMAGE_DESCRIPTION"}), 0
        if "-provision-" in child["name"]:
            for index in (1, 2, 3):
                root = self.output / "material" / "identity" / "nodes" / f"node-{index}"
                root.mkdir(parents=True, mode=0o700)
                profile.new_file(root / "UNIT-not-real-identity.json", {"kind": "UNIT_ONLY"})
            return b"", 0
        if verb == "start" and "--attach" not in args:
            return b"", 0
        mode = "initialize" if "-initialize-" in child["name"] else "resume"
        index = int(child["name"].rsplit("-", 1)[1])
        if self.failure == "initialize_timeout" and mode == "initialize" and index == 0:
            self.rows[-1]["exit"] = None
            raise ValueError("UNIT simulated initializer attach timeout")
        env = dict(line.split("=", 1) for line in Path(child["env"]).read_text().splitlines())
        config_sha = profile.digest(env["LANTERN_SECURITY_CONFIG_JSON"].encode())
        records = unit_records(self.plan, mode, node=f"node-{index + 1}", joined=self.stopped)
        for record in records:
            if "config_sha256" in record:
                record["config_sha256"] = config_sha
        return lines(records), 0


@contextlib.contextmanager
def unit_window(directory, failure=None, final_changed=False):
    clock, made = UnitClock(), []
    plan = unit_plan(str(Path(directory).resolve() / "UNIT-window"))
    approval = {"status": "APPROVE", "kind": "controlled-google-owner-dispatch", "plan_sha256": "4" * 64,
                "owner_present": True, "seed_verification_sha256": plan["seed"]["verification"]["sha256"]}
    admitted = {"seed": {"issuer": profile.ISSUER, "subject": "UNIT_ONLY_NOT_PROVIDER_VERIFIED"},
                "roles_sha256": "3" * 64, "certificate": "UNIT_CERT", "key": "UNIT_KEY"}
    original_window = profile.Window
    source = {**{key: plan["source"][key] for key in profile.SOURCE_FIELDS}, "clean": ""}

    def commands(output, window):
        value = UnitCommands(output, window, plan, clock, failure)
        made.append(value)
        return value

    with mock.patch.object(profile, "admitted_inputs", return_value=admitted), \
            mock.patch.object(profile, "input_snapshot", side_effect=[{"UNIT": "before"}, {"UNIT": "changed" if final_changed else "before"}]), \
            mock.patch.object(profile, "source_snapshot", return_value=source), \
            mock.patch.object(profile, "reference", return_value=profile.encoded({"kind": "UNIT_ONLY_SAME_IMAGE_DESCRIPTION"})), \
            mock.patch.object(profile, "Window", side_effect=lambda p: original_window(p, clock.wall, clock.monotonic)), \
            mock.patch.object(profile, "Commands", side_effect=commands), \
            mock.patch.object(profile.time, "time", side_effect=clock.wall), \
            mock.patch.object(profile.time, "monotonic", side_effect=clock.monotonic), \
            mock.patch.object(profile.time, "sleep", side_effect=clock.sleep), \
            mock.patch.object(profile.signal, "signal"), \
            mock.patch.object(profile.subprocess, "run", side_effect=AssertionError("UNIT unexpected actual process")), \
            mock.patch.object(profile.subprocess, "check_output", side_effect=AssertionError("UNIT unexpected actual Git/CLI")):
        yield plan, approval, clock, made


class ControlledGoogleUnitContracts(unittest.TestCase):
    def test_default_and_pending_never_read_private_inputs_or_dispatch(self):
        with mock.patch.object(profile, "reference", side_effect=AssertionError("UNIT unexpected input read")), \
                mock.patch.object(profile, "run", side_effect=AssertionError("UNIT unexpected dispatch")), contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(profile.main([]), 0)
        plan = unit_plan()
        plan["source"]["head"] = "PENDING_FINAL_SOURCE"
        with mock.patch.object(profile, "reference", side_effect=AssertionError("UNIT unexpected input read")):
            with self.assertRaises(ValueError):
                profile.admitted_inputs(plan)

    def test_plan_finite_route_and_profile_boundaries(self):
        profile.resolved(unit_plan())
        for mutate in (
            lambda p: p.update(schema=True),
            lambda p: p.update(lifetime_seconds=14), lambda p: p.update(lifetime_seconds=541),
            lambda p: p.update(cleanup_by="2026-11-11T00:00:49Z"), lambda p: p.update(accounts={"a": "UNIT_SENTINEL"}),
            lambda p: p["public"].update(origin="https://unit.invalid:24443"),
            lambda p: p["public"].update(lan_ip="0.0.0.0"), lambda p: p["clients"].update(web="987654321098-other.apps.googleusercontent.com"),
        ):
            plan = unit_plan()
            mutate(plan)
            with self.assertRaises(ValueError):
                profile.resolved(plan)

    def test_configuration_preserves_real_fetcher_default_and_no_uid_or_secrets(self):
        plan = unit_plan()
        admitted = {"seed": {"issuer": profile.ISSUER, "subject": "UNIT_ONLY_NOT_PROVIDER_VERIFIED"},
                    "roles_sha256": "3" * 64, "certificate": "UNIT_CERT", "key": "UNIT_LISTENER_KEY"}
        configs = profile.configurations(plan, admitted)
        self.assertEqual([c["node"] for c in configs], ["node-1", "node-2", "node-3"])
        for config in configs:
            self.assertTrue({"root_ca_pem", "private_origins", "secret_bindings", "registration_policy", "user_uid"}.isdisjoint(config))
            self.assertEqual(config["issuers"][0]["issuer"], profile.ISSUER)
            self.assertNotIn("client_secret", config["issuers"][0])
            self.assertEqual(config["issuers"][0]["client_id"], plan["clients"]["web"])
            self.assertTrue(all(c["kind"] == "google_id_token" and "client_secret" not in c for c in config["issuers"][0]["native_clients"]))
        argv = profile.container_argv(plan, "UNIT-name", [(Path("/UNIT/material"), "/material", False)], Path("/UNIT/private.env"),
                                      network=plan["network"]["id"], alias="cg-1111-1", publish=True)
        self.assertIn("192.168.1.8:24445:6380/tcp", argv)
        self.assertNotIn("--add-host", argv)
        self.assertTrue(all("ntp.conf" not in arg and "UNIT_LISTENER_KEY" not in arg for arg in argv))
        self.assertIn("type=bind,source=/UNIT/material,target=/material,readonly", argv)

    def test_descriptor_refuses_legacy_provenance_credentials_and_false_component_counts(self):
        plan = unit_plan()
        authority = "current-v2:" + "5" * 64
        value = profile.descriptor(plan, "4" * 64, profile.utc("2026-11-11T00:00:00Z"), profile.utc("2026-11-11T00:00:01Z"), authority)
        profile.validate_descriptor(value, plan, "4" * 64, authority)
        for key, bad in (("kind", "controlled-native-serving"), ("provider_source", "synthetic"), ("time_source", "configured-source"),
                         ("synthetic_utc", False), ("build_tags", []), ("provider_components_passed", 2), ("accounts", {})):
            changed = copy.deepcopy(value)
            changed[key] = bad
            with self.assertRaises(ValueError):
                profile.validate_descriptor(changed, plan, "4" * 64, authority)

    def test_actual_child_events_required_not_exit_code_or_fabricated_closed_init_repeat(self):
        plan = unit_plan()
        profile.validate_child(lines(unit_records(plan, "initialize")), plan, "initialize", "node-1")
        profile.validate_child(lines(unit_records(plan, "resume", joined=True)), plan, "resume", "node-1")
        for values, mode in (([], "initialize"), ([{"test_exit": 0}], "resume"), (unit_records(plan, "resume"), "resume")):
            with self.assertRaises(ValueError):
                profile.validate_child(lines(values), plan, mode, "node-1")
        changed = unit_records(plan, "initialize")
        changed[0]["repeat"] = unit_counts()
        with self.assertRaises(ValueError):
            profile.validate_child(lines(changed), plan, "initialize", "node-1")
        for mutate in (
            lambda v: v[0]["counters"].update(in_flight=True), lambda v: v[2]["repeat"].update(attempts=2),
            lambda v: v[3].update(dropped=1), lambda v: v[1].update(node="node-2"), lambda v: v[2].update(ok=False),
        ):
            changed = unit_records(plan, "resume", joined=True)
            mutate(changed)
            with self.assertRaises(ValueError):
                profile.validate_child(lines(changed), plan, "resume", "node-1")

    def test_actual_ready_interval_bounds(self):
        for duration in (15, 540):
            plan = unit_plan()
            start = profile.utc(plan["planned_stop_at"]) - duration
            plan["lifetime_seconds"] = 540
            authority = "current-v2:" + "5" * 64
            value = profile.descriptor(plan, "4" * 64, start - 2, start, authority)
            profile.validate_descriptor(value, plan, "4" * 64, authority)
        plan = unit_plan()
        for duration in (14, 541):
            start = profile.utc(plan["planned_stop_at"]) - duration
            value = profile.descriptor(plan, "4" * 64, start - 2, start, authority)
            with self.assertRaises(ValueError):
                profile.validate_descriptor(value, plan, "4" * 64, authority)

    def test_raw_reference_hash_mismatch_and_symlink_refused(self):
        with tempfile.TemporaryDirectory(prefix="UNIT_controlled_google_") as directory:
            path = Path(directory).resolve() / "UNIT.json"
            profile.new_file(path, {"kind": "UNIT_ONLY_NOT_ACTUAL_EVIDENCE"})
            ref = {"path": str(path), "sha256": profile.digest(path.read_bytes())}
            self.assertEqual(profile.reference(ref), path.read_bytes())
            with self.assertRaises(ValueError):
                profile.reference({**ref, "sha256": "0" * 64})
            link = path.with_name("UNIT-link")
            link.symlink_to(path)
            with self.assertRaises(ValueError):
                profile.reference({**ref, "path": str(link)})

    def test_immutable_material_fence_includes_empty_directories_but_not_native_state(self):
        with tempfile.TemporaryDirectory(prefix="UNIT_immutable_material_") as directory:
            root = Path(directory).resolve()
            for name in ("inputs", "material", "operator", "views", "state"):
                (root / name).mkdir(mode=0o700)
            profile.new_file(root / "inputs" / "UNIT.env", b"UNIT_ONLY_NOT_A_CREDENTIAL=1\n")
            before = profile.material_snapshot(root)
            profile.new_file(root / "state" / "UNIT-native-state", b"UNIT mutable native state\n")
            self.assertEqual(profile.material_snapshot(root), before)
            (root / "views" / "UNIT-added-empty-directory").mkdir(mode=0o700)
            self.assertNotEqual(profile.material_snapshot(root), before)
            link = root / "operator" / "UNIT-symlink"
            link.symlink_to(root / "inputs" / "UNIT.env")
            with self.assertRaises(ValueError):
                profile.material_snapshot(root)

    def test_external_seed_basis_and_dispatch_refs_are_fenced_again(self):
        plan, seen = unit_plan(), []
        basis, dispatch = unit_ref("UNIT-ownership-basis"), unit_ref("UNIT-dispatch")

        def reference(ref):
            seen.append(ref["path"])
            return profile.encoded({"basis_refs": [basis]}) if ref == plan["seed"]["verification"] else b"UNIT immutable bytes"

        with mock.patch.object(profile, "reference", side_effect=reference):
            snapshot = profile.input_snapshot(plan, [dispatch])
        self.assertIn(basis["path"], snapshot)
        self.assertIn(dispatch["path"], snapshot)
        self.assertIn(plan["public"]["key"]["path"], snapshot)
        self.assertEqual(len(snapshot), 12)

    def test_window_monotonic_bound_cannot_be_extended_by_wall_clock(self):
        clock, plan = UnitClock(), unit_plan()
        window = profile.Window(plan, clock.wall, clock.monotonic)
        self.assertEqual(window.command_timeout(75), 15)
        clock.wall_offset -= 3600
        clock.elapsed = 14
        self.assertEqual(window.command_timeout(20), 1)
        clock.elapsed = 15
        with self.assertRaises(ValueError):
            window.command_timeout(1)
        self.assertEqual(window.command_timeout(100, cleanup=True), 35)
        clock.elapsed = 50
        with self.assertRaises(ValueError):
            window.command_timeout(1, cleanup=True)

    def test_reused_source_fingerprint_preserves_bytes_and_forwards_optional_timeout(self):
        with tempfile.TemporaryDirectory(prefix="UNIT_source_fingerprint_") as directory:
            root = Path(directory).resolve()
            (root / "UNIT-file").write_bytes(b"UNIT_ONLY_SOURCE_BYTES\n")
            with mock.patch.object(profile.subprocess, "check_output", return_value=b"UNIT-file\0") as git:
                original = profile.native_login_gate.source_identity(root)
                self.assertNotIn("timeout", git.call_args.kwargs)
                bounded = profile.native_login_gate.source_identity(root, timeout=7)
                self.assertEqual(git.call_args.kwargs["timeout"], 7)
            self.assertEqual(original, bounded)

    def test_source_fence_bounds_the_reused_helpers_internal_git_call(self):
        clock, plan = UnitClock(), unit_plan()
        window = profile.Window(plan, clock.wall, clock.monotonic)
        with mock.patch.object(profile.subprocess, "check_output", side_effect=["a" * 40, "b" * 40, ""]), \
                mock.patch.object(profile.native_login_gate, "source_identity", side_effect=profile.subprocess.TimeoutExpired("UNIT_git", 15)) as fingerprint:
            with self.assertRaises(profile.subprocess.TimeoutExpired):
                profile.source_snapshot(plan, window)
        fingerprint.assert_called_once_with(profile.ROOT, timeout=15)

    def test_commands_timeout_is_retained_and_phase_budget_caps_actual_process(self):
        with tempfile.TemporaryDirectory(prefix="UNIT_controlled_commands_") as directory:
            clock, plan = UnitClock(), unit_plan()
            commands = profile.Commands(Path(directory).resolve(), profile.Window(plan, clock.wall, clock.monotonic))
            with mock.patch.object(profile.subprocess, "run", side_effect=profile.subprocess.TimeoutExpired("UNIT", 15)) as process:
                with self.assertRaises(ValueError):
                    commands.run(["UNIT_NO_REAL_CLI"], timeout=75)
            self.assertEqual(process.call_args.kwargs["timeout"], 15)
            self.assertEqual(len(commands.rows), 1)
            self.assertIsNone(commands.rows[0]["exit"])
            clock.elapsed = 50
            commands.cleanup = True
            with mock.patch.object(profile.subprocess, "run", side_effect=AssertionError("UNIT late dispatch")):
                with self.assertRaises(ValueError):
                    commands.run(["UNIT_NO_REAL_CLI"])
            self.assertEqual(len(commands.rows), 1)

    def test_mock_single_window_join_and_immutable_final_fence(self):
        for changed in (False, True):
            with self.subTest(final_input_changed=changed), tempfile.TemporaryDirectory(prefix="UNIT_controlled_window_") as directory:
                with unit_window(directory, final_changed=changed) as (plan, approval, _clock, made):
                    result = profile.run(plan, "4" * 64, approval, "6" * 64)
                self.assertEqual(result["status"], "FAIL" if changed else "PASS")
                self.assertTrue(result["cleanup_known"])
                self.assertEqual(result["immutable_after_verified"], not changed)
                verbs = [r["argv"][3] for r in made[0].rows]
                self.assertEqual(verbs.count("create"), 8)
                self.assertEqual(verbs.count("stop"), 1)
                self.assertNotIn("rm", verbs)
                self.assertEqual(result["provider_components_passed"], 0)

    def test_mock_partial_create_timeout_and_cleanup_deadline_never_pass_or_retry(self):
        for failure, cleanup_known in (("partial_create", False), ("initialize_timeout", True), ("cleanup_deadline", False)):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory(prefix="UNIT_controlled_failure_") as directory:
                with unit_window(directory, failure=failure) as (plan, approval, _clock, made):
                    result = profile.run(plan, "4" * 64, approval, "6" * 64)
                self.assertEqual(result["status"], "FAIL")
                self.assertEqual(result["cleanup_known"], cleanup_known)
                self.assertTrue((Path(plan["output"]) / "lifecycle.json").exists())
                verbs = [r["argv"][3] for r in made[0].rows]
                self.assertEqual(verbs.count("stop"), 1)
                self.assertNotIn("rm", verbs)
                self.assertNotIn("restart", verbs)
                if failure == "partial_create":
                    self.assertGreater(len(result["attempted_names"]), len(result["containers"]))
                    self.assertEqual(len(list(Path(plan["output"]).glob("create-attempt-*.json"))), len(result["attempted_names"]))

    def test_mock_image_overrides_and_bridge_name_refuse_before_any_create(self):
        for failure in ("image_entrypoint", "image_cmd", "image_env", "network_name"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory(prefix="UNIT_image_contract_") as directory:
                with unit_window(directory, failure=failure) as (plan, approval, _clock, made):
                    result = profile.run(plan, "4" * 64, approval, "6" * 64)
                self.assertEqual(result["status"], "FAIL")
                self.assertTrue(result["cleanup_known"])
                self.assertFalse(result["attempted_names"])
                self.assertNotIn("create", [r["argv"][3] for r in made[0].rows])

    def test_mock_broken_stdout_cannot_bypass_unknown_original_lock_holder(self):
        class UnitHolderObserved(BaseException):
            pass

        plan = unit_plan()
        approval = {"status": "APPROVE", "kind": "controlled-google-owner-dispatch", "plan_sha256": "4" * 64,
                    "owner_present": True, "seed_verification_sha256": plan["seed"]["verification"]["sha256"]}
        info = SimpleNamespace(st_mode=profile.stat.S_IFREG | 0o600, st_uid=profile.os.getuid(), st_nlink=1)
        with mock.patch.object(profile, "reference", side_effect=[profile.encoded(plan), profile.encoded(approval)]), \
                mock.patch.object(profile, "run", return_value=None), \
                mock.patch.object(profile.os, "open", return_value=123456) as opened, \
                mock.patch.object(profile.os, "fstat", return_value=info), \
                mock.patch.object(profile.os, "close") as closed, \
                mock.patch.object(profile.fcntl, "flock") as flock, \
                mock.patch.object(profile.signal, "signal"), \
                mock.patch("builtins.print", side_effect=BrokenPipeError("UNIT broken output")), \
                mock.patch.object(profile.time, "sleep", side_effect=UnitHolderObserved) as idle:
            with self.assertRaises(UnitHolderObserved):
                profile.main(["--execute-reviewed-owner-window", "--plan", "/UNIT-plan", "--plan-sha256", "4" * 64,
                              "--root-review", "/UNIT-root", "--root-review-sha256", "6" * 64])
        opened.assert_called_once()
        flock.assert_called_once_with(123456, profile.fcntl.LOCK_EX | profile.fcntl.LOCK_NB)
        closed.assert_not_called()
        idle.assert_called_once_with(1)


if __name__ == "__main__":
    unittest.main()
