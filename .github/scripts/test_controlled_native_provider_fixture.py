"""UNIT-only real-native profile contracts. All processes/providers are mocked."""

import contextlib
import base64
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


def owner_plan(output="/UNIT_ONLY_NOT_ACTUAL/output"):
    return {**plan(output), "owner_window": dict(profile.OWNER_WINDOW), "lifetime_seconds": 1800,
            "planned_stop_at": "2026-11-12T00:30:00Z", "cleanup_by": "2026-11-12T00:30:20Z"}


def publisher_config():
    return {"Version": 1, "End": "2026-11-12T01:00:00Z", "CleanupBy": "2026-11-12T01:00:20Z", "Interval": 60_000_000_000, "Lifetime": 600_000_000_000,
            "Streams": [{"Kind": kind, "KeyFile": f"/operator/custody/{kind}-signing-key.pem", "RootsFile": "/operator/custody/roots.pem", "Initial": "VU5JVF9PTkxZ",
                         "Targets": [f"/material/identity/nodes/node-{i}/membership/{kind}.json" for i in (1, 2, 3)],
                         "Certificates": [f"/operator/custody/certificates/node-{i}.pem" for i in (1, 2, 3)]} for kind in ("control", "data")]}


def capacity(p):
    return {"schema": 1, "kind": "controlled-native-provider-renewal-capacity", "revision": p["source"]["head"], "nonce": p["cohort"],
            "planned_stop_at": p["planned_stop_at"], "cleanup_by": p["cleanup_by"], "publisher_end": profile.instant(profile.utc(p["cleanup_by"]) + 5),
            "publisher_cleanup_by": profile.instant(profile.utc(p["cleanup_by"]) + 10), "tls_capacity_end": "2026-11-12T01:00:00Z",
            "lease_seconds": 600, "refresh_seconds": 60, "target_count": 3,
            "control_profile_sha256": profile.digest(b"lantern.peer.control.profile.v1\0" + profile.go_json(unit_control_profile())),
            "data_domain_sha256": profile.digest(b"lantern.peer.domain.v1\0" + profile.go_json(unit_data_domain())),
            "control_operator_sha256": "2" * 64, "data_operator_sha256": "3" * 64}


def unit_control_profile():
    return {"profile_version": 1, "lineage": {"deployment": [1] * 16, "instance": [2] * 16}, "namespace_format": "UNIT_NAMESPACE",
            "protocol_scope": [3] * 32, "trust_digest": [4] * 32, "voters": [{"UNIT": "NOT_SIGNED_AUTHORITY"}]}


def unit_data_domain():
    return {"deployment": [1] * 16, "namespace_format": "UNIT_NAMESPACE", "auth_mode": "oidc", "security_generation": [2] * 16,
            "writer_public_key": [0] * 32, "trust_digest": [4] * 32, "current_profile": "current-v2:" + "5" * 64}


def lease_rows(p, joined):
    base = profile.utc("2026-11-12T00:00:00Z")
    samples = range(0, 1801, 60) if joined else (0,)
    rows = []
    for second in samples:
        row = {"observed_at": profile.instant(base + second + (0 if joined and second else .5))}
        for kind in ("control", "data"):
            payload = {"version": second // 60 + 1, **({"profile": unit_control_profile()} if kind == "control" else {"domain": unit_data_domain()}),
                       "issued_at": profile.instant(base + second), "expires_at": profile.instant(min(base + second + 600, profile.utc(p["cleanup_by"]) + 5))}
            if kind == "data":
                payload["members"] = [{"UNIT": "NOT_REAL_MEMBERSHIP"}]
            # Typed public envelope fixture; no actual signature claim in a Python unit.
            raw = profile.go_json({"payload": payload, "signature": base64.b64encode(b"UNIT" * 16).decode()})
            row.update({kind + suffix: value for suffix, value in (("_version", payload["version"]), ("_issued_at", payload["issued_at"]),
                       ("_expires_at", payload["expires_at"]), ("_sha256", profile.digest(raw)), ("_manifest", base64.b64encode(raw).decode()))})
        row.update(control_profile_sha256=capacity(p)["control_profile_sha256"], data_domain_sha256=capacity(p)["data_domain_sha256"])
        rows.append(row)
    if not joined:
        final = copy.deepcopy(rows[0])
        final["observed_at"] = "2026-11-12T00:00:01.200000Z"  # Monitor closes after InitializeSecurity's interval-end.
        rows.append(final)
    else:
        first_native = copy.deepcopy(rows[0])
        first_native["observed_at"] = "2026-11-12T00:00:01.500000Z"
        rows.insert(1, first_native)
        for row in rows[1:]:
            observed = profile.utc(row["observed_at"])
            row.update(native_low=profile.instant(observed-.25), native_high=profile.instant(observed+.25))
        final = copy.deepcopy(rows[-1])
        final.pop("native_low")
        final.pop("native_high")
        final["observed_at"] = "2026-11-12T00:30:00.500000Z"
        rows.append(final)  # Post-close signed-envelope observation is explicitly host-only.
    return rows


def owner_records(p, mode, node, config, joined=False):
    values, raw = records(p, mode, node, config, joined), lines(lease_rows(p, joined))
    # Genuine admission reads the first signed cut BEFORE constructor/initializer entry.
    values[0]["host_seed_interval_from"] = "2026-11-12T00:00:00.600000Z"
    if mode == "initialize" or joined:
        terminal = values[2] if joined else values[0]
        last = lease_rows(p, joined)[-1]
        terminal.update(owner_window=dict(profile.OWNER_WINDOW), owner_capacity_sha256=profile.digest(profile.encoded(capacity(p))),
                        owner_lease_transcript_sha256=profile.digest(raw), owner_lease_observations=len(lease_rows(p, joined)),
                        owner_lease_through=profile.instant(profile.utc(last["control_expires_at"]) - 2),
                        owner_lease_time_scope="native-serving-with-host-cleanup" if joined else "host-auxiliary", owner_native_lease_observations=0)
        if joined:
            native = [row for row in lease_rows(p, True) if "native_low" in row]
            terminal.update(stopped_at=profile.instant(profile.utc(p["planned_stop_at"]) + 1), owner_native_lease_from=native[0]["observed_at"],
                            owner_native_lease_until=native[-1]["observed_at"], owner_native_lease_observations=len(native))
    return values, raw


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
            if "owner_window" in self.p:
                self.clock.elapsed += 1  # Owned native close/auxiliary teardown interval.
            if self.failure == "cleanup_deadline":
                self.clock.elapsed += 100
            return b"", 0
        if verb == "inspect":
            selected = argv[6:]
            return b"".join(profile.encoded({"Running": not self.stopped and not (self.failure == "publisher_exit" and "-publisher-" in self.owned[i]["name"] and self.clock.elapsed > 5),
                                           "Pid": 0 if self.stopped else 7, "Restarting": False, "Paused": False,
                                           "Status": "exited" if self.stopped else "running", "ExitCode": 0, "OOMKilled": False, "Error": ""}) for i in selected), 0
        child = self.owned[argv[-1]]
        if "-description-" in child["name"]:
            return profile.encoded({"kind": "UNIT_SAME_IMAGE_DESCRIPTION"}), 0
        if "-provision-" in child["name"]:
            for index in (1, 2, 3):
                path = self.output / "material" / "identity" / "nodes" / f"node-{index}"
                path.mkdir(mode=0o700, parents=True)
                profile.new_file(path / "unit.json", {"kind": "UNIT_NOT_REAL_MATERIAL"})
                if "owner_window" in self.p:
                    (path / "membership").mkdir(mode=0o700)
                    for kind in ("control", "data"):
                        profile.new_file(path / "membership" / (kind + ".json"), {"kind": "UNIT_SIGNED_ENVELOPE_STANDIN"})
            if "owner_window" in self.p:
                custody = self.output / "operator" / "custody"
                (custody / "certificates").mkdir(mode=0o700, parents=True)
                for name in ("control-signing-key.pem", "data-signing-key.pem", "roots.pem", "ca-signing-key.pem"):
                    profile.new_file(custody / name, b"UNIT_NOT_REAL_KEY_OR_CERTIFICATE")
                for i in (1, 2, 3):
                    profile.new_file(custody / "certificates" / f"node-{i}.pem", b"UNIT_CERTIFICATE")
                profile.new_file(custody / "publisher.json", publisher_config())
            return b"", 0
        if "-publisher-" in child["name"]:
            public = self.output / "publisher-public" / "capacity.json"
            if verb == "start":
                profile.new_file(public, b"" if self.failure == "capacity_publication_in_progress" else capacity(self.p))
                self.publisher_reads = 0
                return b"", 0
            if self.failure == "capacity_publication_in_progress" and not self.publisher_reads:
                self.publisher_reads += 1
                self.capacity_visible_before_ready = public.read_bytes() == b""
                return b"", 0  # Created path is not yet a synced/public-ready capacity.
            if self.failure == "capacity_publication_in_progress" and public.read_bytes() == b"":
                public.write_bytes(profile.encoded(capacity(self.p)))
            status = {"failure": "none", "end": capacity(self.p)["publisher_end"], "cleanup_by": capacity(self.p)["publisher_cleanup_by"],
                      "streams": [{"kind": kind, "journal_version": 1, "pending": False,
                                   "targets": [{"ordinal": i, "failure": "none", "candidate_matches": True, "observed_version": 1} for i in (1, 2, 3)]} for kind in ("control", "data")]}
            base = {"schema": 1, "profile": profile.PROFILE, "revision": self.p["source"]["head"], "nonce": self.p["cohort"], "pid": 13, "status": status}
            values = [{**base, "event": "publisher_ready", "capacity_sha256": profile.digest(public.read_bytes())}]
            if self.stopped:
                values.append({**base, "event": "publisher_joined", "ok": True, "stopped_at": profile.instant(self.clock.wall()),
                               "owner_stop_sha256": profile.digest((self.output / "owner-control/stop.json").read_bytes())})
            return lines(values), 0
        if verb == "start" and "--attach" not in argv:
            return b"", 0
        mode = "initialize" if "-initialize-" in child["name"] else "resume"
        index = int(child["name"].rsplit("-", 1)[1])
        config = dict(line.split("=", 1) for line in Path(child["env"]).read_text().splitlines())["LANTERN_SECURITY_CONFIG_JSON"]
        if "owner_window" in self.p:
            values, raw = owner_records(self.p, mode, f"node-{index+1}", profile.digest(config.encode()), self.stopped)
            if mode == "resume" and self.stopped:
                values[2]["owner_stop_sha256"] = profile.digest((self.output / "owner-control/stop.json").read_bytes())
                if self.failure == "owner_ack_missing":
                    values[2].pop("owner_stop_sha256")
                if self.failure == "owner_ack_wrong":
                    values[2]["owner_stop_sha256"] = "0" * 64
            path = self.output / "state" / str(index) / f"controlled-native-provider-owner-leases-{mode}.jsonl"
            if mode == "initialize" or self.stopped:
                profile.new_file(path, raw)
            return lines(values), 0
        return lines(records(self.p, mode, f"node-{index+1}", profile.digest(config.encode()), self.stopped)), 0


@contextlib.contextmanager
def mocked_window(directory, failure=None, owner=False, bounded_commands=False):
    p = (owner_plan if owner else plan)(str(Path(directory).resolve() / "UNIT_NEW_OUTPUT"))
    clock, made = Clock(), []
    approval = {"kind": profile.PROFILE + "-owner-dispatch", "status": "APPROVE", "owner_present": True, "plan_sha256": "4" * 64,
                "seed_verification_sha256": p["seed"]["verification"]["sha256"], "secret_and_tls_whole_window_verified": True,
                "approved_public_origin": p["public"]["origin"], "admin_and_edge_source_reviewed": True}
    original_commands = profile.Commands
    def command(output, window):
        value = Commands(output, window, p, clock, failure)
        made.append(value)
        if bounded_commands:
            value.actual_commands = original_commands(output, window)
            value.transport_calls = []
            return value.actual_commands
        return value
    def transport(argv, **kwargs):
        value = made[-1]
        value.cleanup = value.actual_commands.cleanup
        value.transport_calls.append({"argv": argv, "at": clock.monotonic(), "cleanup": value.cleanup})
        raw, code = value.run(argv, timeout=kwargs["timeout"], check=False)
        kwargs["stdout"].write(raw)
        return SimpleNamespace(returncode=code)
    original_window = profile.Window
    with mock.patch.object(profile, "admitted_inputs", return_value=admitted(p)), \
            mock.patch.object(profile, "source_snapshot", return_value={**p["source"], "clean": ""}), \
            mock.patch.object(profile, "input_snapshot", return_value={"UNIT": "unchanged"}), \
            mock.patch.object(profile, "reference", return_value=profile.encoded({"kind": "UNIT_SAME_IMAGE_DESCRIPTION"})), \
            mock.patch.object(profile, "Window", side_effect=lambda p: original_window(p, clock.wall, clock.monotonic)), \
            mock.patch.object(profile, "Commands", side_effect=command), mock.patch.object(profile.signal, "signal"), \
            mock.patch.object(profile.time, "time", side_effect=clock.wall), mock.patch.object(profile.time, "sleep", side_effect=clock.sleep), \
            (mock.patch("controlled_google_fixture.subprocess.run", side_effect=transport) if bounded_commands else contextlib.nullcontext()):
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

    def test_owner_window_is_explicit_exact_and_keeps_original_monotonic_stop(self):
        p, clock = owner_plan(), Clock()
        profile.resolved(p)
        window = profile.Window(p, clock.wall, clock.monotonic)
        clock.elapsed = 100
        self.assertEqual(window.left(), 1700)
        self.assertEqual(window.left(cleanup=True), 1720)
        self.assertEqual(window.command_timeout(2000), 1685)  # Initial dispatch still reserves15s.
        window.monitoring = True
        self.assertEqual(window.command_timeout(2000), 1700)  # Monitoring consumes only the original remaining interval.
        for change in (lambda p: p.pop("owner_window"), lambda p: p.update(lifetime_seconds=1799), lambda p: p.update(lifetime_seconds=1801),
                       lambda p: p.update(lifetime_seconds=True), lambda p: p["owner_window"].update(lease_seconds=601),
                       lambda p: p["owner_window"].update(refresh_seconds=True), lambda p: p["owner_window"].update(mode="extend-expired"),
                       lambda p: p["owner_window"].update(extra=True), lambda p: p.update(cleanup_by="2026-11-12T00:30:21Z")):
            v = copy.deepcopy(p)
            change(v)
            with self.assertRaises(ValueError):
                profile.resolved(v)
        for stop in ("2026-11-12T00:30:01Z", "2026-11-11T23:59:00Z"):
            v = {**p, "planned_stop_at": stop, "cleanup_by": profile.instant(profile.utc(stop) + 20)}
            with self.assertRaises(ValueError):
                profile.Window(v, Clock().wall, Clock().monotonic)

    def test_owner_requires_new_same_binary_description_and_public_capacity(self):
        p = owner_plan()
        rows = admission_records(p)
        with admission_inputs(rows), self.assertRaises(ValueError):
            profile.admitted_inputs(p)  # Historical static description cannot admit owner30m.
        rows["description"].update(owner_window=dict(profile.OWNER_WINDOW), owner_window_lifetime_seconds=1800)
        with admission_inputs(rows):
            profile.admitted_inputs(p)
        config = profile.configurations(p, admitted(p))[0]
        with self.assertRaises(ValueError):
            profile.child_input(p, config, admitted(p), "resume")
        reference = {"path": "/owner-capacity.json", "sha256": "8" * 64}
        child = profile.child_input(p, config, admitted(p), "resume", reference)
        self.assertEqual(child["owner_window"], profile.OWNER_WINDOW)
        self.assertEqual(child["owner_capacity"], reference)
        self.assertNotIn("owner_window", profile.child_input(plan(), config, admitted(p), "resume"))
        profile.renewal_capacity(profile.encoded(capacity(p)), p)
        normalized = copy.deepcopy(p)
        normalized.update(planned_stop_at="2026-11-12T00:30:00.000000Z", cleanup_by="2026-11-12T00:30:20.000000Z")
        profile.renewal_capacity(profile.encoded(capacity(p)), normalized)  # Go time.Time omits trailing fractional zeros.
        for key, wrong in (("nonce", "8" * 64), ("target_count", 6), ("lease_seconds", 1800), ("tls_capacity_end", p["cleanup_by"])):
            with self.assertRaises(ValueError):
                profile.renewal_capacity(profile.encoded({**capacity(p), key: wrong}), p)

    def test_publisher_exact_targets_keys_and_separate_mutability(self):
        p, original = owner_plan(), publisher_config()
        rewritten = profile.publisher_configuration(p, original)
        self.assertEqual(original, publisher_config())
        self.assertEqual(rewritten["End"], "2026-11-12T00:30:25Z")
        self.assertEqual(rewritten["CleanupBy"], "2026-11-12T00:30:30Z")
        for i, kind in enumerate(("control", "data")):
            self.assertEqual(rewritten["Streams"][i]["Targets"], [f"/targets/node-{n}/{kind}.json" for n in (1, 2, 3)])
        for change in (lambda v: v["Streams"][0].update(Targets=["/state/authority.json"]),
                       lambda v: v["Streams"][0].update(KeyFile="/operator/custody/ca-signing-key.pem"), lambda v: v.update(Lifetime=1800_000_000_000)):
            value = publisher_config()
            change(value)
            with self.assertRaises(ValueError):
                profile.publisher_configuration(p, value)
        mounts = profile.publisher_mounts(Path("/UNIT_ONLY_NOT_ACTUAL"))
        writable = {target for _, target, rw in mounts if rw}
        self.assertEqual(writable, {"/publisher-journal", "/publisher-public", "/targets/node-1", "/targets/node-2", "/targets/node-3"})
        self.assertIn((Path("/UNIT_ONLY_NOT_ACTUAL/owner-control"), "/owner-control", False), mounts)
        self.assertFalse(any("ca-signing-key" in str(path) or "workload-key" in str(path) or target in ("/operator", "/inputs", "/state") for path, target, _ in mounts))

    def test_native_lease_chain_refuses_gap_rollback_replacement_and_false_coverage(self):
        p = owner_plan()
        values, raw = owner_records(p, "resume", "node-1", "3" * 64, True)
        records = profile.child_records(lines(values), p, "resume", "node-1", "3" * 64, joined=True)
        sha = profile.digest(profile.encoded(capacity(p)))
        result = profile.lease_transcript(raw, p, records, sha, capacity(p))
        self.assertEqual(result["observations"], 33)
        self.assertEqual(result["native_observations"], 31)
        for change in (lambda v: v[2].update(control_version=0), lambda v: v[2].update(control_version=v[1]["control_version"]),
                       lambda v: v[2].update(control_issued_at=v[1]["control_expires_at"]),
                       lambda v: v[-1].update(data_expires_at=p["planned_stop_at"])):
            rows, altered = lease_rows(p, True), copy.deepcopy(records)
            change(rows)
            bad = lines(rows)
            altered[-1]["owner_lease_transcript_sha256"] = profile.digest(bad)
            with self.assertRaises(ValueError):
                profile.lease_transcript(bad, p, altered, sha, capacity(p))
        altered = copy.deepcopy(records)
        altered[-1]["owner_lease_through"] = "2026-11-12T01:00:00Z"
        with self.assertRaises(ValueError):
            profile.lease_transcript(raw, p, altered, sha, capacity(p))

    def test_resume_requires_actual_contiguous_native_block_and_bounded_native_time(self):
        p = owner_plan()
        values, raw = owner_records(p, "resume", "node-1", "3" * 64, True)
        records = profile.child_records(lines(values), p, "resume", "node-1", "3" * 64, joined=True)
        sha = profile.digest(profile.encoded(capacity(p)))
        # A genuine refreshed interval may narrow its upper endpoint. The
        # production owner requires overlap, not per-endpoint monotonicity.
        rows, narrowed_records = lease_rows(p, True), copy.deepcopy(records)
        refinement = {**rows[1], "observed_at": "2026-11-12T00:00:01.600000Z",
                      "native_low": "2026-11-12T00:00:01.300000Z", "native_high": "2026-11-12T00:00:01.700000Z"}
        self.assertLess(profile.utc(refinement["native_high"]), profile.utc(rows[1]["native_high"]))
        rows.insert(2, refinement)
        refined = lines(rows)
        narrowed_records[-1].update(owner_lease_transcript_sha256=profile.digest(refined), owner_lease_observations=len(rows), owner_native_lease_observations=32)
        self.assertEqual(profile.lease_transcript(refined, p, narrowed_records, sha, capacity(p))["native_observations"], 32)
        single, single_records = lease_rows(p, True), copy.deepcopy(records)
        for row in single[2:]:
            row.pop("native_low", None)
            row.pop("native_high", None)
        single_raw = lines(single)
        single_records[-1].update(owner_lease_transcript_sha256=profile.digest(single_raw), owner_native_lease_observations=1,
                                  owner_native_lease_until=single[1]["observed_at"])
        with self.assertRaises(ValueError):
            profile.lease_transcript(single_raw, p, single_records, sha, capacity(p))
        for change in (lambda rows: [row.pop(k, None) for row in rows for k in ("native_low", "native_high")],
                       lambda rows: rows[3].pop("native_low"),
                       lambda rows: (rows[3].pop("native_low"), rows[3].pop("native_high")),
                       lambda rows: rows[3].update(native_high=rows[3]["control_expires_at"])):
            rows, altered = lease_rows(p, True), copy.deepcopy(records)
            change(rows)
            bad = lines(rows)
            altered[-1]["owner_lease_transcript_sha256"] = profile.digest(bad)
            with self.assertRaises(ValueError):
                profile.lease_transcript(bad, p, altered, sha, capacity(p))
        for key, value in (("owner_lease_time_scope", "host-auxiliary"), ("owner_native_lease_observations", 0),
                           ("owner_native_lease_from", "2026-11-12T00:00:03Z"), ("owner_native_lease_until", p["cleanup_by"])):
            altered = copy.deepcopy(records)
            altered[-1][key] = value
            with self.assertRaises(ValueError):
                profile.lease_transcript(raw, p, altered, sha, capacity(p))

    def test_owner_full_1800_seconds_uses_real_command_accounting(self):
        with tempfile.TemporaryDirectory() as directory, mocked_window(directory, owner=True, bounded_commands=True) as (p, approval, clock, made):
            result = profile.run(p, "4" * 64, approval, "8" * 64)
            self.assertEqual(result["status"], "PASS")
            self.assertTrue(result["cleanup_known"])
            self.assertTrue(result["immutable_after_verified"])
            self.assertEqual(len(result["containers"]), 11)
            self.assertEqual(len(result["owner_lease_transcripts"]), 6)
            commands = made[0].actual_commands
            self.assertGreater(len(commands.rows), 224)
            self.assertEqual(commands.command_limit(), 2086)  # 224 + 1800 states + 30 status + 32 cleanup.
            self.assertLess(len(commands.rows), commands.command_limit())
            self.assertTrue(all(row["exit"] == 0 for row in commands.rows))
            self.assertEqual(len(list(Path(p["output"]).glob("command-*.log"))), len(commands.rows))
            calls = made[0].transport_calls
            states = [row for row in calls if not row["cleanup"] and row["argv"][3:4] == ["inspect"] and len(row["argv"][6:]) == 4]
            self.assertGreater(len(states), 1750)
            self.assertTrue(all(1 <= after["at"]-before["at"] < 1.01 for before, after in zip(states, states[1:])))
            publisher = next(identity for identity, child in made[0].owned.items() if "-publisher-" in child["name"])
            statuses = [row for row in calls if not row["cleanup"] and row["argv"][3:4] == ["logs"]
                        and row["argv"][-1] == publisher and row["at"] >= states[0]["at"]]
            self.assertEqual(len(statuses), 30)
            self.assertTrue(all(60 <= after["at"]-before["at"] < 62 for before, after in zip(statuses, statuses[1:])))
            self.assertEqual(next(row["at"] for row in calls if row["cleanup"]), 1800)
            self.assertLess(clock.elapsed, 1820)
            self.assertEqual(commands.window.stop, 1800)
            self.assertEqual(commands.window.cleanup, 1820)

    def test_command_quota_keeps_static_limits_and_exact_cleanup_reserve(self):
        for owner, monitoring, limit in ((False, False, 224), (True, False, 224), (True, True, 2054)):
            with self.subTest(owner=owner, monitoring=monitoring), tempfile.TemporaryDirectory() as directory:
                p, clock = (owner_plan if owner else plan)(), Clock()
                window = profile.Window(p, clock.wall, clock.monotonic)
                window.monitoring = monitoring
                commands = profile.Commands(Path(directory).resolve(), window)
                self.assertEqual(commands.command_limit(), limit)
                commands.rows = [{}] * limit
                with mock.patch("controlled_google_fixture.subprocess.run", return_value=SimpleNamespace(returncode=0)) as process:
                    with self.assertRaisesRegex(ValueError, "bounded profile command budget exhausted"):
                        commands.run(["UNIT_NO_REAL_COMMAND"])
                    process.assert_not_called()
                    commands.cleanup = True
                    self.assertEqual(commands.command_limit(), limit + 32)
                    for _ in range(32):
                        commands.run(["UNIT_NO_REAL_COMMAND"])
                    with self.assertRaisesRegex(ValueError, "bounded profile command budget exhausted"):
                        commands.run(["UNIT_NO_REAL_COMMAND"])
                    self.assertEqual(process.call_count, 32)

    def test_owner_command_quota_cannot_extend_original_deadlines(self):
        with tempfile.TemporaryDirectory() as directory:
            p, clock = owner_plan(), Clock()
            window = profile.Window(p, clock.wall, clock.monotonic)
            window.monitoring = True
            commands = profile.Commands(Path(directory).resolve(), window)
            clock.elapsed = 1800
            with mock.patch("controlled_google_fixture.subprocess.run", return_value=SimpleNamespace(returncode=0)) as process:
                with self.assertRaisesRegex(ValueError, "original owner stop reached"):
                    commands.run(["UNIT_NO_REAL_COMMAND"])
                process.assert_not_called()
                commands.cleanup = True
                commands.run(["UNIT_NO_REAL_COMMAND"])
                self.assertEqual(process.call_args.kwargs["timeout"], 20)
                clock.elapsed = 1820
                with self.assertRaisesRegex(ValueError, "absolute profile command budget exhausted"):
                    commands.run(["UNIT_NO_REAL_COMMAND"])
                self.assertEqual(process.call_count, 1)

    def test_owner_mocked_renewal_cleanup_and_only_six_public_files_mutable(self):
        with tempfile.TemporaryDirectory() as directory, mocked_window(directory, "capacity_publication_in_progress", owner=True) as (p, approval, _clock, made):
            result = profile.run(p, "4" * 64, approval, "8" * 64)
            self.assertEqual(result["status"], "PASS")
            self.assertTrue(made[0].capacity_visible_before_ready)
            self.assertTrue(result["cleanup_known"])
            self.assertEqual(len(result["containers"]), 11)
            self.assertEqual(len(result["owner_lease_transcripts"]), 6)
            creates = [r["argv"] for r in made[0].rows if r["argv"][3] == "create"]
            serving = [row for row in creates if "-resume-" in row[row.index("--name") + 1]]
            self.assertEqual(len(serving), 3)
            for row in serving:
                mounts = [row[i+1] for i, item in enumerate(row) if item == "--mount"]
                self.assertTrue(any("target=/owner-capacity.json,readonly" in m for m in mounts))
                self.assertFalse(any("target=/operator" in m or "signing-key.pem" in m for m in mounts))
            output = Path(p["output"])
            before = profile.cohort_material_snapshot(output, True, result["owner_stop"])
            target = output / "views/0/identity/nodes/node-1/membership/control.json"
            target.write_bytes(b"UNIT_ATOMIC_RENEWAL_STANDIN")
            self.assertEqual(profile.cohort_material_snapshot(output, True, result["owner_stop"]), before)
            key = output / "operator/custody/control-signing-key.pem"
            original_key = key.read_bytes()
            key.write_bytes(b"UNIT_TAMPER")
            self.assertNotEqual(profile.cohort_material_snapshot(output, True, result["owner_stop"]), before)
            key.write_bytes(original_key)
            self.assertEqual(profile.cohort_material_snapshot(output, True, result["owner_stop"]), before)
            profile.new_file(target.with_name("unexpected.json"), {"UNIT": True})
            self.assertNotEqual(profile.cohort_material_snapshot(output, True, result["owner_stop"]), before)
            profile.new_file(output / "owner-control/unexpected.json", {"UNIT": True})
            with self.assertRaises(ValueError):
                profile.cohort_material_snapshot(output, True, result["owner_stop"])

    def test_publisher_live_failure_and_causal_join_ack_required(self):
        p = owner_plan()
        status = {"failure": "none", "end": capacity(p)["publisher_end"], "cleanup_by": capacity(p)["publisher_cleanup_by"],
                  "streams": [{"kind": kind, "journal_version": 1, "pending": False,
                               "targets": [{"ordinal": i, "failure": "none", "candidate_matches": True, "observed_version": 1} for i in (1, 2, 3)]} for kind in ("control", "data")]}
        base = {"schema": 1, "profile": profile.PROFILE, "revision": p["source"]["head"], "nonce": p["cohort"], "pid": 13, "status": status}
        ready = {**base, "event": "publisher_ready", "capacity_sha256": "8" * 64}
        joined = {**base, "event": "publisher_joined", "ok": True, "stopped_at": p["planned_stop_at"], "owner_stop_sha256": "9" * 64,
                  "status": {**status, "failure": "stopped"}}
        profile.publisher_records(lines([ready, joined]), p, "8" * 64, joined=True, stop_sha256="9" * 64)
        # Causal acknowledgement, not an unqualified Mac-to-VM wall comparison,
        # proves stop ordering even if the VM's timestamp precedes a Mac sample.
        offset = {**joined, "stopped_at": profile.instant(profile.utc(p["planned_stop_at"]) - 1)}
        profile.publisher_records(lines([ready, offset]), p, "8" * 64, joined=True, stop_sha256="9" * 64)
        with self.assertRaises(ValueError):
            profile.publisher_records(lines([ready, joined]), p, "8" * 64)
        for actual in (None, "0" * 64):
            with self.assertRaises(ValueError):
                profile.publisher_records(lines([ready, joined]), p, "8" * 64, joined=True, stop_sha256=actual)
        for failure in ("stopped", "delivery_retry", "journal_uncertain"):
            altered = {**base, "event": "publisher_status", "status": {**status, "failure": failure}}
            with self.assertRaises(ValueError):
                profile.publisher_records(lines([ready, altered]), p, "8" * 64)

    def test_owner_refuses_publisher_exit_or_missing_or_wrong_causal_stop_ack(self):
        for failure in ("publisher_exit", "owner_ack_missing", "owner_ack_wrong", "owner_stop_write"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory, mocked_window(directory, failure, owner=True) as (p, approval, clock, _made):
                original_new_file = profile.new_file
                def publish(path, value):
                    if failure == "owner_stop_write" and path.name == "stop.json":
                        raise OSError("UNIT_OWNER_STOP_WRITE_FAILED")
                    return original_new_file(path, value)
                with mock.patch.object(profile, "new_file", side_effect=publish):
                    result = profile.run(p, "4" * 64, approval, "8" * 64)
                self.assertEqual(result["status"], "FAIL")
                self.assertEqual(result["cleanup_known"], failure == "publisher_exit")
                self.assertTrue(_made[0].stopped)  # Stop-record publication failure must still stop all owned children.
                if failure == "publisher_exit":
                    self.assertLess(clock.elapsed, 600)  # Last genuine lease could still be current; no continued serving.
                    self.assertTrue(all(row["normal_exit"] for row in result["containers"]))

    def test_cleanup_inspects_every_owner_after_early_middle_and_late_proof_errors(self):
        original_child, original_publisher = profile.child_records, profile.publisher_records
        for target in ("publisher", "node-2", "node-3"):
            with self.subTest(target=target), tempfile.TemporaryDirectory() as directory, mocked_window(directory, owner=True) as (p, approval, _clock, made):
                def child(raw, plan, mode, node, config, **options):
                    if options.get("joined") and node == target:
                        raise ValueError("UNIT_PRIVATE_CREDENTIAL_UID_AND_PATH_MUST_NOT_APPEAR")
                    return original_child(raw, plan, mode, node, config, **options)
                def publisher(raw, plan, capacity_sha, **options):
                    if options.get("joined") and target == "publisher":
                        raise ValueError("UNIT_PRIVATE_CREDENTIAL_UID_AND_PATH_MUST_NOT_APPEAR")
                    return original_publisher(raw, plan, capacity_sha, **options)
                with mock.patch.object(profile, "child_records", side_effect=child), mock.patch.object(profile, "publisher_records", side_effect=publisher):
                    result = profile.run(p, "4" * 64, approval, "8" * 64)
                self.assertEqual(result["status"], "FAIL")
                self.assertFalse(result["cleanup_known"])
                self.assertTrue(result["all_owned_containers_stopped"])
                self.assertEqual(len(result["containers"]), 11)
                self.assertTrue(all(row["normal_exit"] for row in result["containers"]))
                self.assertEqual(len(result["cleanup_errors"]), 1)
                error = result["cleanup_errors"][0]
                self.assertEqual(error["stage"], "publisher_proof" if target == "publisher" else "child_proof")
                self.assertEqual(error["exception_class"], "ValueError")
                self.assertEqual(result["cleanup_failure_class"], "ValueError")
                rows = made[0].rows
                after_stop = rows[next(i for i, row in enumerate(rows) if row["argv"][3] == "stop") + 1:]
                self.assertEqual([row["argv"][-1] for row in after_stop if row["argv"][3] == "inspect"], list(made[0].owned))
                retained = profile.decode((Path(p["output"]) / "lifecycle.json").read_bytes())
                self.assertEqual(retained["cleanup_errors"], result["cleanup_errors"])
                self.assertNotIn("UNIT_PRIVATE_CREDENTIAL_UID_AND_PATH", json.dumps(retained))

    def test_cleanup_inspect_failures_do_not_skip_later_owned_containers(self):
        original_run = Commands.run
        for kind, position in (("exception", 0), ("nonzero", 5), ("malformed", 10), ("null", 4), ("array", 7)):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as directory, mocked_window(directory, owner=True) as (p, approval, _clock, made):
                def run(command, argv, timeout=20, check=True):
                    raw, code = original_run(command, argv, timeout, check)
                    if command.cleanup and argv[3] == "inspect" and argv[-1] == list(command.owned)[position]:
                        if kind == "exception":
                            raise OSError("UNIT_PRIVATE_PATH_MUST_NOT_APPEAR")
                        if kind in ("null", "array"):
                            return (b"null" if kind == "null" else b"[]"), 0
                        return (b"", 1) if kind == "nonzero" else (b"{", 0)
                    return raw, code
                with mock.patch.object(Commands, "run", run):
                    result = profile.run(p, "4" * 64, approval, "8" * 64)
                self.assertEqual(result["status"], "FAIL")
                self.assertFalse(result["cleanup_known"])
                self.assertFalse(result["all_owned_containers_stopped"])
                self.assertEqual(len(result["containers"]), 11)
                self.assertEqual(sum(row["stopped"] for row in result["containers"]), 10)
                rows = made[0].rows
                after_stop = rows[next(i for i, row in enumerate(rows) if row["argv"][3] == "stop") + 1:]
                self.assertEqual([row["argv"][-1] for row in after_stop if row["argv"][3] == "inspect"], list(made[0].owned))
                self.assertTrue(all(row["stage"] == "inspect" for row in result["cleanup_errors"]))
                self.assertNotIn("UNIT_PRIVATE_PATH", json.dumps(result))

    def test_cleanup_stop_exception_still_inspects_all_owners_and_retains_fd(self):
        class PrivateException(Exception):
            pass
        class Retained(Exception):
            pass
        original_run = Commands.run
        with tempfile.TemporaryDirectory() as directory, mocked_window(directory, owner=True) as (p, approval, _clock, made):
            def run(command, argv, timeout=20, check=True):
                raw, code = original_run(command, argv, timeout, check)
                if command.cleanup and argv[3] == "stop":
                    raise PrivateException("UNIT_PRIVATE_CREDENTIAL_UID_AND_PATH_MUST_NOT_APPEAR")
                return raw, code
            with mock.patch.object(Commands, "run", run):
                result = profile.run(p, "4" * 64, approval, "8" * 64)
            self.assertEqual(result["status"], "FAIL")
            self.assertFalse(result["cleanup_known"])
            self.assertTrue(result["all_owned_containers_stopped"])
            self.assertEqual(len(result["containers"]), 11)
            self.assertEqual(result["cleanup_errors"], [{"id": None, "stage": "stop", "exception_class": "OtherException"}])
            self.assertNotIn("PrivateException", json.dumps(result))
            self.assertNotIn("UNIT_PRIVATE_CREDENTIAL_UID_AND_PATH", json.dumps(result))
            with mock.patch.object(profile.fcntl, "flock") as unlock, mock.patch.object(profile.os, "close") as close, \
                    mock.patch.object(profile.time, "sleep", side_effect=Retained), contextlib.redirect_stdout(io.StringIO()):
                with self.assertRaises(Retained):
                    profile.retain_or_release(7, result)
                unlock.assert_not_called()
                close.assert_not_called()

    def test_real_finalization_restoration_failure_publishes_nonacceptance_and_retains_original_fd(self):
        restored = []
        def restore(number, handler):
            if handler == "UNIT_OLD_HANDLER":
                restored.append(number)
                if len(restored) == 1:
                    raise ValueError("UNIT_RESTORE_FAILURE")
        with tempfile.TemporaryDirectory() as directory, mocked_window(directory) as (p, approval, _clock, _made), \
                mock.patch.object(profile.signal, "getsignal", return_value="UNIT_OLD_HANDLER"), mock.patch.object(profile.signal, "signal", side_effect=restore):
            result = profile.run(p, "4" * 64, approval, "8" * 64)
            self.assertEqual(restored, [profile.signal.SIGINT, profile.signal.SIGTERM])
            self.assertEqual(result["status"], "FAIL")
            self.assertFalse(result["cleanup_known"])
            self.assertEqual(result["signal_restoration_failure_class"], "ValueError")
            self.assertTrue(all(v["normal_exit"] for v in result["containers"]))
            self.assertEqual(profile.decode((Path(p["output"]) / "lifecycle.json").read_bytes())["status"], "FAIL")
        class Retained(Exception):
            pass
        restored.clear()
        p = plan()
        approval = {"kind": profile.PROFILE + "-owner-dispatch", "status": "APPROVE", "owner_present": True, "plan_sha256": "4" * 64,
                    "seed_verification_sha256": p["seed"]["verification"]["sha256"], "secret_and_tls_whole_window_verified": True,
                    "approved_public_origin": p["public"]["origin"], "admin_and_edge_source_reviewed": True}
        def hold(lock, value):
            self.assertEqual(lock, 7)
            self.assertEqual(value["status"], "FAIL")
            self.assertFalse(value["cleanup_known"])
            raise Retained
        info = SimpleNamespace(st_mode=profile.stat.S_IFREG | 0o600, st_uid=profile.os.getuid(), st_nlink=1)
        with mock.patch.object(profile, "reference", side_effect=lambda r: profile.encoded(p if r["path"].endswith("plan.json") else approval)), \
                mock.patch.object(profile.os, "open", return_value=7), mock.patch.object(profile.os, "fstat", return_value=info), \
                mock.patch.object(profile.os, "close") as close, mock.patch.object(profile.fcntl, "flock") as flock, \
                mock.patch.object(profile.signal, "getsignal", return_value="UNIT_OLD_HANDLER"), mock.patch.object(profile.signal, "signal", side_effect=restore), \
                mock.patch.object(profile, "run", return_value={"status": "PASS", "cleanup_known": True}), \
                mock.patch.object(profile, "new_file") as publish, mock.patch.object(profile, "retain_or_release", side_effect=hold):
            with self.assertRaises(Retained):
                profile.main(["--execute-reviewed-owner-window", "--plan", "/UNIT/plan.json", "--plan-sha256", "4" * 64,
                              "--root-review", "/UNIT/review.json", "--root-review-sha256", "8" * 64])
            self.assertEqual(restored, [profile.signal.SIGINT, profile.signal.SIGTERM])
            self.assertEqual(publish.call_args.args[0].name, "NON_ACCEPTING_POST_PUBLICATION.json")
            self.assertEqual(flock.call_count, 1)  # Original acquisition only; no release or second FD.
            close.assert_not_called()


if __name__ == "__main__":
    unittest.main()
