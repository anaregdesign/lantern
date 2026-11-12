"""UNIT-only real-native profile contracts. All processes/providers are mocked."""

import contextlib
import copy
import io
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock

import controlled_native_provider_fixture as profile


def ref(name):
    return {"path": "/UNIT_ONLY_NOT_ACTUAL/" + name, "sha256": "d" * 64}


def plan(output="/UNIT_ONLY_NOT_ACTUAL/output"):
    values = {"browser": "UNIT_BROWSER_SECRET", "apple_android": "UNIT_APPLE_ANDROID_SECRET", "apple_ios": "UNIT_APPLE_IOS_SECRET"}
    return {"schema": 1, "kind": profile.PROFILE + "-plan", "source": {"head": "a" * 40, "tree": "b" * 40, "source_sha256": "c" * 64,
            "proof": ref("proof"), "review": ref("review")}, "build": {"image": "sha256:" + "e" * 64, "receipt": ref("build"), "description": ref("description"), "public_roots": ref("public-roots")},
            "seed": {"material": ref("material"), "verification": ref("verification")}, "issuer_intent": ref("issuer-intent"),
            "clients": {**{k: ("987654321000" if k == "google_browser" else "123456789000") + "-unit" + k.replace("_", "") + ".apps.googleusercontent.com" for k in profile.CLIENTS[:4]},
                        "apple_android": "invalid.unit.service", "apple_ios": "invalid.unit.bundle"},
            "profile_ids": {k: k for k in profile.PROFILES}, "secrets": {k: {"ref": "unit_" + k, "value_sha256": profile.digest(v.encode())} for k, v in values.items()},
            "android_package": "invalid.unit.android", "public": {"origin": "https://unit.invalid:24443", "lan_ip": "192.168.1.8", "certificate": ref("cert"), "key": ref("key")},
            "network": {"id": "f" * 64, "name": "UNIT_NEW_BRIDGE", "admission": ref("network")}, "admin": {"image": "sha256:" + "6" * 64, "receipt": ref("admin-build")},
            "edge": {"image": "sha256:" + "7" * 64, "admission": ref("edge")}, "output": output, "cohort": "1" * 64,
            "lifetime_seconds": 60, "planned_stop_at": "2026-11-12T00:01:00Z", "cleanup_by": "2026-11-12T00:01:20Z"}


def intent(p):
    ids, clients, secrets = p["profile_ids"], p["clients"], p["secrets"]
    return {"public_origin": p["public"]["origin"], "issuers": [
        {"issuer": "google", "id": secrets["browser"]["ref"], "enabled": True, "client_id": clients["google_browser"], "client_secret": "UNIT_BROWSER_SECRET",
         "native_clients": [{"id": ids[k], "kind": "google_id_token", "client_id": clients["google_native"], "presenters": [clients[k]]} for k in profile.PROFILES[:2]]},
        {"issuer": profile.APPLE, "enabled": True, "algorithms": ["RS256"], "native_clients": [
            {"id": ids["apple_android"], "kind": "apple_android_web", "client_id": clients["apple_android"], "android_package": p["android_package"],
             "secret_id": secrets["apple_android"]["ref"], "token_origin": profile.APPLE, "client_secret": "UNIT_APPLE_ANDROID_SECRET"},
            {"id": ids["apple_ios"], "kind": "apple_native", "client_id": clients["apple_ios"], "secret_id": secrets["apple_ios"]["ref"],
             "token_origin": profile.APPLE, "client_secret": "UNIT_APPLE_IOS_SECRET"}]}]}


def admitted(p):
    return {"seed": {"issuer": profile.GOOGLE, "subject": "UNIT_NOT_VERIFIED_BY_PROVIDER"}, "roles_sha256": "2" * 64,
            "issuers": profile.issuer_configuration(p, intent(p)), "certificate": "UNIT_PUBLIC_CERT", "key": "UNIT_PRIVATE_KEY_NOT_REAL",
            "admin_index_sha256": profile.digest(b"UNIT_EXACT_SOURCE_ADMIN_INDEX")}


def admission_records(p):
    """Virtual raw producer records, not an actual qualification or credential."""
    source = {k: p["source"][k] for k in profile.SOURCE_FIELDS}
    return {"proof": {"schema": 1, "status": "PASS", **source, "source_head": source["head"], "source_tree": source["tree"],
                      "selected_ci_verified": True, "required_configured_source_verified": True},
            "review": {"status": "APPROVE", **source}, "public-roots": b"-----BEGIN CERTIFICATE-----\nUNIT_NOT_CERTIFICATE\n",
            "build": {"schema": 1, "kind": profile.PROFILE + "-build", "status": "PASS", **source, "image": p["build"]["image"],
                      "build_tags": ["lantern_test_time"], "roles_sha256": "2" * 64, "binary_sha256": "3" * 64,
                      "public_roots_sha256": profile.digest(b"-----BEGIN CERTIFICATE-----\nUNIT_NOT_CERTIFICATE\n"),
                      "compile_argv": ["go", "test", "-c", "-tags=lantern_test_time", "-ldflags", "-X github.com/anaregdesign/lantern/server/cmd.controlledNativeProviderRevision=" + source["head"], "-o", "UNIT.test", "./server/cmd"]},
            "description": {"schema": 1, "kind": "controlled-real-native-provider-profile-description", "profile": profile.PROFILE, "revision": source["head"],
                            "provider_source": "real-google-apple", "native_profile_count": 4, "registration_at_genesis": False, "synthetic_utc": True,
                            "build_tags": ["lantern_test_time"], "roles_sha256": "2" * 64, "binary_sha256": "3" * 64,
                            "public_roots_sha256": profile.digest(b"-----BEGIN CERTIFICATE-----\nUNIT_NOT_CERTIFICATE\n")},
            "verification": {"status": "APPROVE_VERIFIED_RETAINED_SEED", "provider_ownership_verified": True, "material_sha256": p["seed"]["material"]["sha256"],
                             "subject_source": "retained-material.initial_admin", "basis_refs": [ref("UNIT-historical-basis")]},
            "material": {"initial_admin": {"issuer": profile.GOOGLE, "subject": "UNIT_NOT_VERIFIED_BY_PROVIDER"}}, "UNIT-historical-basis": {"unit": True},
            "network": {"status": "APPROVE_EMPTY_DEDICATED_BRIDGE", "id": p["network"]["id"], "name": p["network"]["name"], "cohort": p["cohort"]},
            "admin-build": {"kind": profile.PROFILE + "-admin-build", "status": "PASS", **source, "image": p["admin"]["image"],
                            **{k + "_sha256": profile.digest(b"UNIT_CURRENT_ADMIN_SOURCE") for k in ("dockerfile", "entrypoint", "caddyfile")},
                            "build_args": {"COMMIT": source["head"]}, "dockerfile": "admin/Dockerfile", "context_source_sha256": source["source_sha256"],
                            "index_html_sha256": profile.digest(b"UNIT_EXACT_SOURCE_ADMIN_INDEX")},
            "edge": {"status": "APPROVE_FIXED_CADDY_EDGE_IMAGE", "image": p["edge"]["image"], "binary_path": "/usr/bin/caddy"},
            "issuer-intent": intent(p), "cert": b"UNIT_CERT", "key": b"UNIT_KEY"}


@contextlib.contextmanager
def admission_inputs(rows):
    class UnitSource:
        def __truediv__(self, _part):
            return self

        def read_bytes(self):
            return b"UNIT_CURRENT_ADMIN_SOURCE"
    def reference(value):
        v = rows[Path(value["path"]).name]
        return v if isinstance(v, bytes) else profile.encoded(v)
    with mock.patch.object(profile, "reference", side_effect=reference), mock.patch.object(profile, "ROOT", UnitSource()):
        yield


def counts():
    return {"attempts": 1, "writes": 1, "installations": 1, "in_flight": False, "failed": False, "denied": False}


def records(p, mode, node, config, joined=False):
    base = {"profile": profile.PROFILE, "nonce": p["cohort"], "revision": p["source"]["head"], "mode": mode, "pid": 17, "node": node,
            "config_sha256": config, "authority_profile": "current-v2:" + "5" * 64}
    first = {**base, "event": "initialized" if mode == "initialize" else "constructed", "counters": counts(),
             "host_seed_interval_from": "2026-11-12T00:00:00Z", "host_seed_interval_until": "2026-11-12T00:00:01Z"}
    if mode == "initialize":
        return [{**first, "ok": True}]
    result = [first, {**base, "event": "ready", "ready_at": "2026-11-12T00:00:02Z"}]
    if joined:
        result += [{**base, "event": "joined", "counters": counts(), "repeat": counts(), "ok": True, "stopped_at": p["planned_stop_at"]},
                   {"kind": "controlled_public_trace", "events": [], "dropped": 0}]
    return result


def lines(values):
    return b"".join(profile.encoded(v) for v in values)


class Clock:
    elapsed = 0

    def wall(self):
        return profile.utc("2026-11-12T00:00:00Z") + self.elapsed

    def monotonic(self):
        return self.elapsed

    def sleep(self, amount):
        self.elapsed += amount


class Commands:
    def __init__(self, output, window, p, clock, failure):
        self.output, self.window, self.p, self.clock, self.failure = output, window, p, clock, failure
        self.cleanup, self.rows, self.owned, self.stopped = False, [], {}, False

    def run(self, argv, timeout=20, check=True):
        bound = self.window.command_timeout(timeout, self.cleanup)
        self.rows.append({"argv": argv, "timeout_seconds": bound, "exit": 0})
        self.clock.elapsed += .001
        if argv[0] != "docker":
            return profile.encoded({"origin": self.p["public"]["origin"], "dns_addresses": [self.p["public"]["lan_ip"]],
                                    "health_status": "SERVING_STATUS_SERVING", "admin_index_sha256": profile.digest(b"UNIT_EXACT_SOURCE_ADMIN_INDEX")}), 0
        verb = argv[3]
        if verb == "image":
            if "env_count" in argv[-2]:
                return profile.encoded({"entrypoint": ["/controlled-native-providers.test"], "cmd": None, "env_count": 0}), 0
            return (self.p["source"]["head"] if ".Config.Labels" in argv[-2] else argv[-1]).encode(), 0
        if verb == "network":
            return (b"{}" if ".Containers" in argv[-2] else (self.p["network"]["name"] if ".Name" in argv[-2] else self.p["network"]["id"]).encode()), 0
        if verb == "create":
            name = argv[argv.index("--name") + 1]
            if self.failure == "partial_create" and "-admin-" in name:
                raise ValueError("UNIT create response unknown")
            identity = profile.digest(name.encode())
            self.owned[identity] = {"name": name, "env": argv[argv.index("--env-file") + 1] if "--env-file" in argv else None}
            return identity.encode(), 0
        if verb == "stop":
            self.stopped = True
            if self.failure == "cleanup_deadline":
                self.clock.elapsed += 100
            return b"", 0
        if verb == "inspect":
            return profile.encoded({"Running": not self.stopped, "Pid": 0 if self.stopped else 7, "Restarting": False, "Paused": False,
                                    "Status": "exited" if self.stopped else "running", "ExitCode": 0, "OOMKilled": False, "Error": ""}), 0
        child = self.owned[argv[-1]]
        if "-description-" in child["name"]:
            return profile.encoded({"kind": "UNIT_SAME_IMAGE_DESCRIPTION"}), 0
        if "-provision-" in child["name"]:
            for index in (1, 2, 3):
                path = self.output / "material" / "identity" / "nodes" / f"node-{index}"
                path.mkdir(mode=0o700, parents=True)
                profile.new_file(path / "unit.json", {"kind": "UNIT_NOT_REAL_MATERIAL"})
            return b"", 0
        if verb == "start" and "--attach" not in argv:
            return b"", 0
        mode = "initialize" if "-initialize-" in child["name"] else "resume"
        index = int(child["name"].rsplit("-", 1)[1])
        config = dict(line.split("=", 1) for line in Path(child["env"]).read_text().splitlines())["LANTERN_SECURITY_CONFIG_JSON"]
        return lines(records(self.p, mode, f"node-{index+1}", profile.digest(config.encode()), self.stopped)), 0


@contextlib.contextmanager
def mocked_window(directory, failure=None):
    p = plan(str(Path(directory).resolve() / "UNIT_NEW_OUTPUT"))
    clock, made = Clock(), []
    approval = {"kind": profile.PROFILE + "-owner-dispatch", "status": "APPROVE", "owner_present": True, "plan_sha256": "4" * 64,
                "seed_verification_sha256": p["seed"]["verification"]["sha256"], "secret_and_tls_whole_window_verified": True,
                "approved_public_origin": p["public"]["origin"], "admin_and_edge_source_reviewed": True}
    def command(output, window):
        value = Commands(output, window, p, clock, failure)
        made.append(value)
        return value
    original_window = profile.Window
    with mock.patch.object(profile, "admitted_inputs", return_value=admitted(p)), \
            mock.patch.object(profile, "source_snapshot", return_value={**p["source"], "clean": ""}), \
            mock.patch.object(profile, "input_snapshot", return_value={"UNIT": "unchanged"}), \
            mock.patch.object(profile, "reference", return_value=profile.encoded({"kind": "UNIT_SAME_IMAGE_DESCRIPTION"})), \
            mock.patch.object(profile, "Window", side_effect=lambda p: original_window(p, clock.wall, clock.monotonic)), \
            mock.patch.object(profile, "Commands", side_effect=command), mock.patch.object(profile.signal, "signal"), \
            mock.patch.object(profile.time, "time", side_effect=clock.wall), mock.patch.object(profile.time, "sleep", side_effect=clock.sleep):
        yield p, approval, clock, made


class ControlledNativeProviderContracts(unittest.TestCase):
    def test_default_and_pending_before_any_input_or_lock(self):
        with mock.patch.object(profile, "reference", side_effect=AssertionError("UNIT input")), mock.patch.object(profile.os, "open", side_effect=AssertionError("UNIT lock")), contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(profile.main([]), 0)
            p = plan()
            p["source"]["head"] = "PENDING_FINAL_SOURCE"
            with self.assertRaises(ValueError):
                profile.admitted_inputs(p)

    def test_exact_plan_profiles_projects_origin_and_window(self):
        profile.resolved(plan())  # Browser deliberately belongs to another project.
        for change in (lambda p: p.update(kind="controlled-real-google-plan"), lambda p: p.update(lifetime_seconds=541), lambda p: p.update(lifetime_seconds=14),
                       lambda p: p["clients"].update(google_ios="999999999999-unit.apps.googleusercontent.com"), lambda p: p["profile_ids"].update(apple_ios="apple_android"),
                       lambda p: p["public"].update(origin="https://unit.invalid:24445"), lambda p: p.update(accounts={"UNIT": "FORBIDDEN"}),
                       lambda p: p["public"].update(origin="https://accounts.google.com:24443"),
                       lambda p: p["secrets"]["apple_ios"].update(ref="unit_apple_android")):
            p = plan()
            change(p)
            with self.subTest(change=change), self.assertRaises(ValueError):
                profile.resolved(p)

    def test_strict_normal_issuer_projection_and_secret_destinations(self):
        p = plan()
        self.assertEqual(len(profile.issuer_configuration(p, intent(p))), 2)
        changes = (lambda v: v["issuers"][0].update(api_audience="UNIT_API"), lambda v: v["issuers"][0].update(client_id=p["clients"]["google_native"]),
                   lambda v: v["issuers"][0]["native_clients"][0].update(client_secret="UNIT_FORBIDDEN"),
                   lambda v: v["issuers"][1]["native_clients"][0].update(token_origin="https://unit.invalid"),
                   lambda v: v["issuers"][1]["native_clients"][0].update(client_secret="UNIT_APPLE_IOS_SECRET"),
                   lambda v: v["issuers"][1]["native_clients"][0].update(android_package="invalid.unit.other"),
                   lambda v: v["issuers"][1]["native_clients"][1].update(redirect_uri="https://unit.invalid"))
        for change in changes:
            v = intent(p)
            change(v)
            with self.subTest(change=change), self.assertRaises(ValueError):
                profile.issuer_configuration(p, v)

    def test_source_build_seed_and_admin_authority_are_independent_requirements(self):
        p = plan()
        rows = admission_records(p)
        with admission_inputs(rows):
            self.assertEqual(profile.admitted_inputs(p)["seed"]["issuer"], profile.GOOGLE)
        changes = (lambda v: v["proof"].update(required_configured_source_verified=False), lambda v: v["review"].update(head="b" * 40),
                   lambda v: v["build"].update(build_tags=[]), lambda v: v["build"].update(compile_argv=["go", "test", "-c", "-tags=lantern_test_time", "-ldflags", "-X main.controlledNativeProviderRevision=" + "a" * 40, "-o", "UNIT.test", "./server/cmd"]),
                   lambda v: v["description"].update(provider_source="synthetic"), lambda v: v["description"].update(registration_at_genesis=True),
                   lambda v: v["verification"].update(status="PENDING_ROOT_CUSTODY_DERIVATION_REVIEW"), lambda v: v["verification"].update(provider_ownership_verified=False),
                   lambda v: v["admin-build"].update(head="b" * 40), lambda v: v["admin-build"].update(entrypoint_sha256="9" * 64),
                   lambda v: v["edge"].update(image="sha256:" + "8" * 64), lambda v: v["network"].update(cohort="9" * 64))
        for change in changes:
            values = copy.deepcopy(rows)
            change(values)
            with self.subTest(change=change), admission_inputs(values), self.assertRaises(ValueError):
                profile.admitted_inputs(p)

    def test_fresh_config_does_not_copy_old_state_or_policy(self):
        p, a = plan(), admitted(plan())
        configs = profile.configurations(p, a)
        self.assertEqual([c["node"] for c in configs], ["node-1", "node-2", "node-3"])
        self.assertTrue(all(c["material_root"] == "/material/identity" and c["persistence_root"] == "/state" for c in configs))
        self.assertTrue(all(not set(c) & {"principals", "roles", "registration_policy", "secret_bindings", "private_origins", "root_ca_pem"} for c in configs))
        child = profile.child_input(p, configs[0], a, "initialize")
        self.assertEqual(child["profile_ids"], list(profile.PROFILES))
        self.assertEqual(set(child["secret_value_sha256"]), {v["ref"] for v in p["secrets"].values()})
        self.assertNotIn("UNIT_BROWSER_SECRET", json.dumps(child))
        self.assertEqual(child["revision"], p["source"]["head"])

    def test_python_producer_matches_actual_go_decoder_fixture(self):
        # The Go Contracts group strictly decodes this SAME UNIT-only file.
        # This catches cross-language names rather than two matching Python
        # dictionaries. Empty config is hash input only, never runtime authority.
        p = plan()
        fixture = profile.decode((profile.ROOT / "server/cmd/testdata/controlled-native-provider-input.unit.json").read_bytes())
        self.assertEqual(profile.child_input(p, {}, admitted(p), "initialize"), fixture)

    def test_edge_and_admin_only_resolve_new_node1(self):
        p = plan()
        edge, admin = profile.routes(p)
        self.assertIn(b"https://unit.invalid:6380", edge)
        self.assertIn(("cnp-" + p["cohort"][:16] + "-admin:8080").encode(), edge)
        self.assertEqual(admin, {"LANTERN_ADMIN_SERVER_UPSTREAM": "https://unit.invalid:6380", "LANTERN_ADMIN_SERVER_CA_FILE": "/server-trust/server.pem"})
        for role in ("build", "admin", "edge"):
            argv = profile.container_argv(p, role, "UNIT-owned", [], "/UNIT_ONLY/env", network=p["network"]["id"])
            self.assertNotIn("--add-host", argv)
            self.assertNotIn("/etc/ntp.conf", " ".join(argv))
            self.assertEqual("--publish" in argv, role == "edge")

    def test_real_frontend_probe_binds_public_dns_health_tls_and_index(self):
        import socket
        import ssl
        import urllib.request
        p, a = plan(), admitted(plan())
        requests = []
        bodies = [b'{"status":"SERVING_STATUS_SERVING"}', b"UNIT_EXACT_SOURCE_ADMIN_INDEX"]
        def open_response(request, timeout):
            requests.append(request)
            body = bodies[len(requests) - 1]
            return contextlib.nullcontext(SimpleNamespace(status=200, read=lambda _maximum: body))
        opener = SimpleNamespace(open=open_response)
        addresses = [(None, None, None, None, (p["public"]["lan_ip"], 24443))]
        with mock.patch.object(socket, "getaddrinfo", return_value=addresses), mock.patch.object(ssl, "create_default_context") as tls, \
                mock.patch.object(urllib.request, "build_opener", return_value=opener) as build:
            value = profile.frontend_probe(p["public"]["origin"], p["public"]["lan_ip"], "/UNIT_ONLY/public-roots.pem", a["admin_index_sha256"])
            self.assertEqual(value["admin_index_sha256"], a["admin_index_sha256"])
            self.assertEqual(value["health_status"], "SERVING_STATUS_SERVING")
            tls.assert_called_once_with(cafile="/UNIT_ONLY/public-roots.pem")
            self.assertEqual([r.full_url for r in requests], [p["public"]["origin"] + "/grpc.health.v1.Health/Check", p["public"]["origin"] + "/index.html"])
            self.assertEqual(requests[0].data, b"{}")
            self.assertEqual(build.call_args.args[0].proxies, {})
            with self.assertRaises(ValueError):
                build.call_args.args[-1].redirect_request(None, None, None, None, None, None)
        for body in (b'{"status":"SERVING"}', b'{"status":"SERVING_STATUS_SERVING"}'):
            requests.clear()
            bodies[:] = [body, b"UNIT_EXACT_SOURCE_ADMIN_INDEX"]
            with mock.patch.object(socket, "getaddrinfo", return_value=addresses), mock.patch.object(ssl, "create_default_context"), \
                    mock.patch.object(urllib.request, "build_opener", return_value=opener):
                self.assertIn(profile.frontend_probe(p["public"]["origin"], p["public"]["lan_ip"], "/UNIT_ONLY/public-roots.pem", a["admin_index_sha256"])["health_status"], ("SERVING", "SERVING_STATUS_SERVING"))
        for failure in ("wrong_dns", "wrong_health", "stale_admin", "duplicate_json", "multiple_json", "malformed_json"):
            requests.clear()
            health_body = {"wrong_health": b'{"status":"SERVING_STATUS_NOT_SERVING"}', "duplicate_json": b'{"status":"NOT_SERVING","status":"SERVING"}',
                           "multiple_json": b'{"status":"SERVING"}{}', "malformed_json": b'{"status":'}.get(failure, b'{"status":"SERVING_STATUS_SERVING"}')
            bodies[:] = [health_body,
                         b"UNIT_STALE_ADMIN" if failure == "stale_admin" else b"UNIT_EXACT_SOURCE_ADMIN_INDEX"]
            selected = [(None, None, None, None, ("192.168.1.99", 24443))] if failure == "wrong_dns" else addresses
            with self.subTest(failure=failure), mock.patch.object(socket, "getaddrinfo", return_value=selected), \
                    mock.patch.object(ssl, "create_default_context"), mock.patch.object(urllib.request, "build_opener", return_value=opener), self.assertRaises(ValueError):
                profile.frontend_probe(p["public"]["origin"], p["public"]["lan_ip"], "/UNIT_ONLY/public-roots.pem", a["admin_index_sha256"])
        argv = profile.frontend_probe_argv(p, a)
        self.assertEqual(argv[1:4], ["-I", "-B", "-c"])
        self.assertEqual(argv[-4:], [p["public"]["origin"], p["public"]["lan_ip"], p["build"]["public_roots"]["path"], a["admin_index_sha256"]])

    def test_descriptor_has_no_provider_registration_or_utc_claim(self):
        p = plan()
        value = profile.descriptor(p, "4" * 64, profile.utc("2026-11-12T00:00:00Z"), profile.utc("2026-11-12T00:00:10Z"), "current-v2:" + "5" * 64)
        profile.validate_descriptor(value, p, "4" * 64, value["authority_profile"])
        for field in ("provider_components_passed", "full_provider_cells_passed", "signup_passed", "management_mutations_by_controller"):
            self.assertEqual(value[field], 0)
        for field, wrong in (("kind", "controlled-native-serving"), ("real_utc_claim", True), ("admin_backend", "OLD-node-1"), ("accounts", {"UNIT": "FORBIDDEN"})):
            altered = {**value, field: wrong}
            with self.assertRaises(ValueError):
                profile.validate_descriptor(altered, p, "4" * 64, value["authority_profile"])

    def test_actual_child_events_and_settled_trace_required(self):
        p = plan()
        values = records(p, "resume", "node-1", "3" * 64, True)
        profile.child_records(lines(values), p, "resume", "node-1", "3" * 64, joined=True)
        changes = (lambda v: v.pop(), lambda v: v[0]["counters"].update(in_flight=True), lambda v: v[2].update(repeat=counts() | {"writes": 2}),
                   lambda v: v[2].update(pid=18), lambda v: v[-1].update(dropped=1), lambda v: v[0].update(revision="b" * 40))
        for change in changes:
            v = copy.deepcopy(values)
            change(v)
            with self.assertRaises(ValueError):
                profile.child_records(lines(v), p, "resume", "node-1", "3" * 64, joined=True)

    def test_scratch_no_inherited_environment_or_foreign_entrypoint(self):
        value = {"entrypoint": ["/controlled-native-providers.test"], "cmd": None, "env_count": 0}
        profile.scratch_metadata(value)
        for field, wrong in (("entrypoint", ["/controlled-google.test"]), ("cmd", ["UNIT_EXTRA"]), ("env_count", 1), ("env_count", False)):
            with self.assertRaises(ValueError):
                profile.scratch_metadata({**value, field: wrong})

    def test_normal_mocked_owned_lifecycle_and_frontend_mounts(self):
        with tempfile.TemporaryDirectory() as directory, mocked_window(directory) as (p, approval, clock, made):
            result = profile.run(p, "4" * 64, approval, "8" * 64)
            self.assertEqual(result["status"], "PASS")
            self.assertTrue(result["cleanup_known"])
            self.assertEqual(len(result["containers"]), 10)  # description, provision, 3 init, 3 App, Admin, edge.
            creates = [r["argv"] for r in made[0].rows if r["argv"][3] == "create"]
            edge = next(a for a in creates if "-edge-" in a[a.index("--name")+1])
            self.assertFalse(any("target=/edge-tls," in a or "target=/operator" in a for a in edge))
            self.assertTrue(all(row["timeout_seconds"] > 0 for row in made[0].rows))

    def test_partial_create_and_deadline_never_report_clean_pass(self):
        for failure in ("partial_create", "cleanup_deadline"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory, mocked_window(directory, failure) as (p, approval, _clock, _made):
                result = profile.run(p, "4" * 64, approval, "8" * 64)
                self.assertEqual(result["status"], "FAIL")
                self.assertFalse(result["cleanup_known"])

    def test_unknown_holder_survives_broken_output_but_known_join_releases(self):
        class IdleSentinel(Exception):
            pass
        with mock.patch.object(profile.fcntl, "flock") as unlock, mock.patch.object(profile.os, "close") as close, \
                mock.patch("builtins.print", side_effect=BrokenPipeError), mock.patch.object(profile.time, "sleep", side_effect=IdleSentinel):
            with self.assertRaises(IdleSentinel):
                profile.retain_or_release(7, None)
            unlock.assert_not_called()
            close.assert_not_called()
            profile.retain_or_release(7, {"cleanup_known": True})
            unlock.assert_called_once_with(7, profile.fcntl.LOCK_UN)
            close.assert_called_once_with(7)


if __name__ == "__main__":
    unittest.main()
