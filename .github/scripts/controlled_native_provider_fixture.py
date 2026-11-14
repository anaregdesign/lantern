#!/usr/bin/env python3
"""Explicit real Google/Apple native profile and same-source Admin, controlled UTC.

PLAN_ONLY by default. No management mutation, credential export or provider claim.
The older Google-only controller and normal native fixture remain independent.
"""

import argparse
import base64
import copy
import fcntl
import ipaddress
import inspect
import json
import os
from pathlib import Path
import re
import shutil
import signal
import stat
import sys
import time
from urllib.parse import urlsplit

from controlled_google_fixture import (CLIENT, HEX40, HEX64, ROOT, SOURCE_FIELDS, Commands as StaticCommands, Window as StaticWindow,
                                       counters, instant, material_snapshot, new_file, reference,
                                       source_snapshot)
from physical_fixture_provenance import canonical, decode, digest, encoded, read, require, utc


PROFILE = "controlled-real-native-providers"
KIND = PROFILE + "-serving"
CHILD = "TestControlledNativeProviderChild"
PUBLISHER_CHILD = "TestControlledNativeProviderPublisherChild"
OWNER_WINDOW = {"mode": "membership-renewal", "lease_seconds": 600, "refresh_seconds": 60}
OWNER_STATE_INTERVAL_SECONDS = 1
GOOGLE, APPLE = "https://accounts.google.com", "https://appleid.apple.com"
PROFILES = ("google_android", "google_ios", "apple_android", "apple_ios")
CLIENTS = ("google_browser", "google_native", "google_android", "google_ios", "apple_android", "apple_ios")
SECRETS = ("browser", "apple_android", "apple_ios")
IDENTIFIER = re.compile(r"[a-z0-9][a-z0-9_-]{0,63}\Z")
APP_ID = re.compile(r"[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)+\Z")
DOCKER = ["docker", "--context", "desktop-linux"]


def renewing_membership(plan):
    if "owner_window" not in plan:
        return False
    require(type(plan["owner_window"]) is dict and plan["owner_window"] == OWNER_WINDOW
            and all(type(plan["owner_window"][key]) is int for key in ("lease_seconds", "refresh_seconds"))
            and type(plan.get("lifetime_seconds")) is int and plan["lifetime_seconds"] == 1800,
            "explicit owner30m renewal contract required")
    return True


class Window(StaticWindow):
    """The opt-in owner interval keeps the original absolute stop/cleanup."""
    def __init__(self, plan, wall=None, monotonic=None):
        self.monitoring = False
        self.renewing = renewing_membership(plan)
        self.lifetime_seconds = plan["lifetime_seconds"]
        if not self.renewing:
            super().__init__(plan, wall, monotonic)
            return
        self.wall, self.monotonic = wall or time.time, monotonic or time.monotonic
        self.started_wall, self.started_mono = self.wall(), self.monotonic()
        remaining = utc(plan["planned_stop_at"]) - self.started_wall
        require(15 <= remaining <= 1800 and utc(plan["cleanup_by"]) - utc(plan["planned_stop_at"]) == 20,
                "fixed owner30m interval unavailable")
        self.stop = self.started_mono + remaining
        self.cleanup = self.started_mono + utc(plan["cleanup_by"]) - self.started_wall

    def command_timeout(self, requested, cleanup=False):
        if self.renewing and self.monitoring and not cleanup:
            remaining = self.left()
            require(remaining > 0, "original owner stop reached")
            return min(requested, remaining)
        return super().command_timeout(requested, cleanup)


class Commands(StaticCommands):
    def command_limit(self):
        extra = 0
        if self.window.renewing and self.window.monitoring:
            # The admitted owner30m interval adds one state inspect per second
            # and one publisher log read per refresh; retain the static setup
            # allowance and its separate 32-command cleanup reserve.
            lifetime = self.window.lifetime_seconds
            state_samples = (lifetime + OWNER_STATE_INTERVAL_SECONDS - 1) // OWNER_STATE_INTERVAL_SECONDS
            refresh = OWNER_WINDOW["refresh_seconds"]
            status_samples = (lifetime + refresh - 1) // refresh
            extra = state_samples + status_samples
        return super().command_limit() + extra


def references(plan):
    return [plan[section][key] for section, keys in (
        ("source", ("proof", "review")), ("build", ("receipt", "description", "public_roots")),
        ("seed", ("material", "verification")), ("public", ("certificate", "key")),
        ("network", ("admission",)), ("admin", ("receipt",)), ("edge", ("admission",))) for key in keys] + [plan["issuer_intent"]]


def resolved(plan):
    """Pure admission first: no PENDING pin can reach input/Git/Docker reads."""
    require(isinstance(plan, dict) and type(plan.get("schema")) is int and plan["schema"] == 1
            and plan.get("kind") == PROFILE + "-plan", "explicit native provider plan required")
    source = plan.get("source", {})
    require(HEX40.fullmatch(source.get("head", "")) and HEX40.fullmatch(source.get("tree", ""))
            and HEX64.fullmatch(source.get("source_sha256", "")), "final source pins pending")
    renewing = renewing_membership(plan)
    require(set(plan) == {"schema", "kind", "source", "build", "seed", "issuer_intent", "clients", "profile_ids", "secrets",
                         "android_package", "public", "network", "admin", "edge", "output", "cohort", "lifetime_seconds", "planned_stop_at", "cleanup_by"}
            | ({"owner_window"} if renewing else set()), "unknown profile input refused")
    require(set(source) == {"head", "tree", "source_sha256", "proof", "review"}, "exact main source admission required")
    require(type(plan["lifetime_seconds"]) is int and (plan["lifetime_seconds"] == 1800 if renewing else 15 <= plan["lifetime_seconds"] <= 540)
            and utc(plan["cleanup_by"]) - utc(plan["planned_stop_at"]) == 20 and HEX64.fullmatch(plan["cohort"]), "fixed finite window required")
    clients = plan["clients"]
    require(set(clients) == set(CLIENTS) and all(CLIENT.fullmatch(clients[k]) for k in CLIENTS[:4])
            and len({clients[k] for k in CLIENTS[1:4]}) == 3
            and len({CLIENT.fullmatch(clients[k]).group(1) for k in CLIENTS[1:4]}) == 1, "exact native project and independent Browser client required")
    require(all(APP_ID.fullmatch(clients[k]) for k in CLIENTS[4:]) and clients["apple_android"] != clients["apple_ios"]
            and APP_ID.fullmatch(plan["android_package"]), "exact Apple clients and Android package required")
    require(set(plan["profile_ids"]) == set(PROFILES) and len(set(plan["profile_ids"].values())) == 4
            and all(IDENTIFIER.fullmatch(v) for v in plan["profile_ids"].values()), "four exact native profiles required")
    require(set(plan["secrets"]) == set(SECRETS) and len({v["ref"] for v in plan["secrets"].values()}) == 3
            and all(set(v) == {"ref", "value_sha256"} and IDENTIFIER.fullmatch(v["ref"])
                    and HEX64.fullmatch(v["value_sha256"]) for v in plan["secrets"].values()), "three exact private secret destinations required")
    public = plan["public"]
    require(set(public) == {"origin", "lan_ip", "certificate", "key"}, "exact public TLS input required")
    origin = urlsplit(public["origin"])
    require(origin.scheme == "https" and origin.hostname and re.fullmatch(r"[a-z0-9][a-z0-9.-]*", origin.hostname)
            and origin.port == 24443 and not origin.username and not origin.password and not origin.path and not origin.query
            and not origin.fragment and origin.netloc == origin.netloc.lower() and origin.geturl() == public["origin"], "approved canonical HTTPS port24443 required")
    require(origin.hostname not in ("accounts.google.com", "oauth2.googleapis.com", "www.googleapis.com", "appleid.apple.com"),
            "public backend alias cannot replace a production provider hostname")
    address = ipaddress.ip_address(public["lan_ip"])
    require(address.version == 4 and address.is_private and not address.is_loopback and not address.is_unspecified, "approved Mac LAN IPv4 required")
    require(set(plan["network"]) == {"id", "name", "admission"} and HEX64.fullmatch(plan["network"]["id"]), "new dedicated bridge pending")
    for section, keys in (("build", {"image", "receipt", "description", "public_roots"}), ("admin", {"image", "receipt"}), ("edge", {"image", "admission"})):
        require(set(plan[section]) == keys and re.fullmatch(r"sha256:[0-9a-f]{64}", plan[section]["image"]), "immutable image binding pending")
    require(set(plan["seed"]) == {"material", "verification"}, "verified retained seed required")
    for ref in references(plan):
        require(isinstance(ref, dict) and set(ref) == {"path", "sha256"} and HEX64.fullmatch(ref["sha256"])
                and Path(ref["path"]).is_absolute(), "immutable profile reference pending")
    require(not any(c in plan["output"] for c in ",\r\n\0"), "unambiguous new private output required")
    canonical(plan["output"])
    return plan


def issuer_configuration(plan, intent):
    """Select only exact normal-parser Issuer intent; never reuse roots or state."""
    require(isinstance(intent.get("issuers"), list) and len(intent["issuers"]) == 2
            and intent.get("public_origin") == plan["public"]["origin"], "reviewed normal public-origin/Issuer intent required")
    issuers = copy.deepcopy(intent["issuers"])
    google = next((v for v in issuers if v.get("issuer") in ("google", GOOGLE)), None)
    apple = next((v for v in issuers if v.get("issuer") == APPLE), None)
    require(google is not None and apple is not None, "only genuine Google and Apple Issuers required")
    clients, ids, secrets = plan["clients"], plan["profile_ids"], plan["secrets"]
    browser_ref = google.get("id") or digest((GOOGLE + "\0browser").encode())
    require(browser_ref == secrets["browser"]["ref"], "normal compiler Browser SecretRef mismatch")
    def secret(value, name):
        require(isinstance(value, str) and value and "\n" not in value and "\r" not in value
                and digest(value.encode()) == secrets[name]["value_sha256"], "exact private secret bytes required")
        return value
    expected_google = {"issuer": google["issuer"], "enabled": True, "client_id": clients["google_browser"], "algorithms": ["RS256"],
                       "token_origin": "https://oauth2.googleapis.com", "client_secret": secret(google.get("client_secret"), "browser"),
                       "native_clients": [{"id": ids[k], "kind": "google_id_token", "client_id": clients["google_native"], "presenters": [clients[k]]} for k in PROFILES[:2]]}
    if google.get("id"):
        expected_google["id"] = google["id"]
    # The normal Google preset permits omission of these two pinned defaults.
    normalized = copy.deepcopy(google)
    normalized.setdefault("algorithms", ["RS256"])
    normalized.setdefault("token_origin", "https://oauth2.googleapis.com")
    expected_apple = {"issuer": APPLE, "enabled": True, "algorithms": ["RS256"], "native_clients": []}
    native = apple.get("native_clients", [])
    require(len(native) == 2, "exact Apple native profiles required")
    for key, kind in (("apple_android", "apple_android_web"), ("apple_ios", "apple_native")):
        item = next((v for v in native if v.get("id") == ids[key]), {})
        expected = {"id": ids[key], "kind": kind, "client_id": clients[key], "secret_id": secrets[key]["ref"],
                    "token_origin": APPLE, "client_secret": secret(item.get("client_secret"), key)}
        if key == "apple_android":
            expected["android_package"] = plan["android_package"]
        expected_apple["native_clients"].append(expected)
    # Order is not trust: the normal compiler sorts native profiles by ID.
    for value in (normalized, expected_google, apple, expected_apple):
        value["native_clients"] = sorted(value["native_clients"], key=lambda v: v["id"])
    require(normalized == expected_google and apple == expected_apple, "extra Issuer/profile/trust input refused")
    return [google, apple]


def admitted_inputs(plan):
    resolved(plan)
    source = plan["source"]
    proof, review = (decode(reference(source[k])) for k in ("proof", "review"))
    require(proof.get("schema") == 1 and proof.get("status") == "PASS" and proof.get("selected_ci_verified") is True
            and proof.get("required_configured_source_verified") is True and proof.get("source_head") == source["head"]
            and proof.get("source_tree") == source["tree"] and review.get("status") == "APPROVE", "actual exact-main admission required")
    for record in (proof, review):
        require(all(record.get(k, record.get("sourceSha256") if k == "source_sha256" else None) == source[k] for k in SOURCE_FIELDS), "source admission mismatch")
    build, description = (decode(reference(plan["build"][k])) for k in ("receipt", "description"))
    require(build.get("schema") == 1 and build.get("kind") == PROFILE + "-build" and build.get("status") == "PASS"
            and all(build.get(k) == source[k] for k in SOURCE_FIELDS) and build.get("image") == plan["build"]["image"]
            and build.get("build_tags") == ["lantern_test_time"], "same-source controlled build required")
    require(description.get("schema") == 1 and description.get("kind") == "controlled-real-native-provider-profile-description"
            and description.get("revision") == source["head"] and description.get("profile") == PROFILE
            and description.get("provider_source") == "real-google-apple" and description.get("native_profile_count") == 4
            and description.get("registration_at_genesis") is False and description.get("synthetic_utc") is True
            and description.get("build_tags") == ["lantern_test_time"] and HEX64.fullmatch(description.get("roles_sha256", ""))
            and build.get("roles_sha256") == description["roles_sha256"], "actual native4 same-binary description required")
    if renewing_membership(plan):
        require(description.get("owner_window") == OWNER_WINDOW and description.get("owner_window_lifetime_seconds") == 1800,
                "same-binary owner renewal capability required")
    require(HEX64.fullmatch(build.get("binary_sha256", "")) and description.get("binary_sha256") == build["binary_sha256"], "same binary binding required")
    argv = build.get("compile_argv", [])
    require(len(argv) >= 8 and argv[:4] == ["go", "test", "-c", "-tags=lantern_test_time"] and argv[-1] == "./server/cmd"
            and "-X github.com/anaregdesign/lantern/server/cmd.controlledNativeProviderRevision=" + source["head"] in argv, "qualified linked profile build required")
    roots = reference(plan["build"]["public_roots"])
    require(b"-----BEGIN CERTIFICATE-----" in roots and build.get("public_roots_sha256") == digest(roots)
            and description.get("public_roots_sha256") == digest(roots), "ordinary public HTTPS roots required")
    material, verified = (decode(reference(plan["seed"][k])) for k in ("material", "verification"))
    require(verified.get("status") == "APPROVE_VERIFIED_RETAINED_SEED" and verified.get("provider_ownership_verified") is True
            and verified.get("material_sha256") == plan["seed"]["material"]["sha256"]
            and verified.get("subject_source") == "retained-material.initial_admin" and verified.get("basis_refs"), "independently admitted retained seed ownership required")
    for ref in verified["basis_refs"]:
        reference(ref)
    seed = material.get("initial_admin")
    require(isinstance(seed, dict) and set(seed) == {"issuer", "subject"} and seed["issuer"] == GOOGLE
            and isinstance(seed["subject"], str) and 0 < len(seed["subject"]) <= 255, "exact retained Google seed required")
    network = decode(reference(plan["network"]["admission"]))
    require(network.get("status") == "APPROVE_EMPTY_DEDICATED_BRIDGE" and all(network.get(k) == plan["network"][k] for k in ("id", "name"))
            and network.get("cohort") == plan["cohort"], "new dedicated bridge admission required")
    admin = decode(reference(plan["admin"]["receipt"]))
    require(admin.get("kind") == PROFILE + "-admin-build" and admin.get("status") == "PASS"
            and all(admin.get(k) == source[k] for k in SOURCE_FIELDS) and admin.get("image") == plan["admin"]["image"], "exact-source Admin build required")
    for field, path in (("dockerfile_sha256", "admin/Dockerfile"), ("entrypoint_sha256", "admin/docker-entrypoint.sh"), ("caddyfile_sha256", "admin/Caddyfile")):
        require(admin.get(field) == digest((ROOT / path).read_bytes()), "current Admin route/build source mismatch")
    require(admin.get("build_args") == {"COMMIT": source["head"]} and admin.get("dockerfile") == "admin/Dockerfile"
            and admin.get("context_source_sha256") == source["source_sha256"] and HEX64.fullmatch(admin.get("index_html_sha256", "")),
            "actual current-source Admin build and index provenance required")
    edge = decode(reference(plan["edge"]["admission"]))
    require(edge.get("status") == "APPROVE_FIXED_CADDY_EDGE_IMAGE" and edge.get("image") == plan["edge"]["image"]
            and edge.get("binary_path") == "/usr/bin/caddy", "admitted immutable edge required")
    return {"seed": seed, "roles_sha256": description["roles_sha256"], "issuers": issuer_configuration(plan, decode(reference(plan["issuer_intent"]))),
            "admin_index_sha256": admin["index_html_sha256"],
            "certificate": reference(plan["public"]["certificate"]).decode(), "key": reference(plan["public"]["key"]).decode()}


def input_snapshot(plan, extra=()):
    refs = references(plan) + decode(reference(plan["seed"]["verification"]))["basis_refs"] + list(extra)
    return {ref["path"]: digest(reference(ref)) for ref in refs}


def configurations(plan, admitted):
    prefix = "cnp-" + plan["cohort"][:16]
    nodes = [{"name": f"node-{i}", "control_origin": f"https://{prefix}-{i}:6381", "data_origin": f"https://{prefix}-{i}:6382",
              "control_listen": "0.0.0.0:6381", "data_listen": "0.0.0.0:6382"} for i in (1, 2, 3)]
    base = {"public_origin": plan["public"]["origin"], "material_root": "/material/identity", "persistence_root": "/state",
            "target_platform": "linux", "tls_termination": "server", "initial_admin": admitted["seed"], "nodes": nodes,
            "issuers": admitted["issuers"], "tls": {"certificate_pem": admitted["certificate"], "private_key_pem": admitted["key"]}}
    return [{**base, "node": f"node-{i}"} for i in (1, 2, 3)]


def child_input(plan, config, admitted, mode, capacity=None):
    result = {"schema": 1, "profile": PROFILE, "mode": mode, "revision": plan["source"]["head"], "nonce": plan["cohort"],
              "config_sha256": digest(json.dumps(config, separators=(",", ":")).encode()),
              "seed_identity_sha256": digest((GOOGLE + "\0" + admitted["seed"]["subject"]).encode()), "roles_sha256": admitted["roles_sha256"],
              "service_origin": plan["public"]["origin"], "android_package": plan["android_package"],
              "profile_ids": [plan["profile_ids"][p] for p in PROFILES], "lifetime_seconds": plan["lifetime_seconds"],
              "planned_stop_at": plan["planned_stop_at"], "cleanup_by": plan["cleanup_by"],
              "secret_value_sha256": {v["ref"]: v["value_sha256"] for v in plan["secrets"].values()}}
    result.update({(key.removeprefix("google_") if key.startswith("google_") else key) + "_client_id": value
                   for key, value in plan["clients"].items()})
    result.update({key + "_secret_ref": plan["secrets"][key]["ref"] for key in SECRETS})
    if renewing_membership(plan):
        require(isinstance(capacity, dict) and set(capacity) == {"path", "sha256"}
                and capacity["path"] == "/owner-capacity.json" and HEX64.fullmatch(capacity["sha256"]), "verified public owner capacity required")
        result.update(owner_window=dict(OWNER_WINDOW), owner_capacity=capacity)
    return result


def publisher_configuration(plan, original):
    """Rebind only six public destinations and the original absolute budget."""
    require(renewing_membership(plan) and set(original) == {"Version", "End", "CleanupBy", "Interval", "Lifetime", "Streams"}
            and original["Version"] == 1 and original["Interval"] == 60_000_000_000 and original["Lifetime"] == 600_000_000_000
            and isinstance(original["Streams"], list) and len(original["Streams"]) == 2, "exact provisioned publisher required")
    value = copy.deepcopy(original)
    value.update(End=instant(utc(plan["cleanup_by"]) + 5), CleanupBy=instant(utc(plan["cleanup_by"]) + 10))
    for stream, kind in zip(value["Streams"], ("control", "data")):
        require(set(stream) == {"Kind", "KeyFile", "RootsFile", "Initial", "Targets", "Certificates"} and stream["Kind"] == kind
                and stream["KeyFile"] == f"/operator/custody/{kind}-signing-key.pem" and stream["RootsFile"] == "/operator/custody/roots.pem"
                and isinstance(stream["Initial"], str) and stream["Initial"]
                and stream["Targets"] == [f"/material/identity/nodes/node-{i}/membership/{kind}.json" for i in (1, 2, 3)]
                and stream["Certificates"] == [f"/operator/custody/certificates/node-{i}.pem" for i in (1, 2, 3)], "exact provisioned publisher stream required")
        stream["Targets"] = [f"/targets/node-{i}/{kind}.json" for i in (1, 2, 3)]
    return value


def publisher_mounts(output):
    custody = output / "operator" / "custody"
    return [(custody / name, "/operator/custody/" + name, False)
            for name in ("control-signing-key.pem", "data-signing-key.pem", "roots.pem", "certificates")] + [
        (output / "inputs" / "publisher-config.json", "/publisher-config.json", False),
        (output / "inputs" / "publisher-input.json", "/publisher-input.json", False),
        (output / "owner-control", "/owner-control", False),
        (output / "publisher-journal", "/publisher-journal", True), (output / "publisher-public", "/publisher-public", True)] + [
        (output / "views" / str(i-1) / "identity" / "nodes" / f"node-{i}" / "membership", f"/targets/node-{i}", True) for i in (1, 2, 3)]


def renewal_capacity(raw, plan):
    value = decode(raw)
    require(set(value) == {"schema", "kind", "revision", "nonce", "planned_stop_at", "cleanup_by", "publisher_end", "publisher_cleanup_by",
                          "tls_capacity_end", "lease_seconds", "refresh_seconds", "target_count", "control_profile_sha256", "data_domain_sha256",
                          "control_operator_sha256", "data_operator_sha256"} and type(value["schema"]) is int and value["schema"] == 1
            and value["kind"] == "controlled-native-provider-renewal-capacity" and value["revision"] == plan["source"]["head"]
            and value["nonce"] == plan["cohort"] and utc(value["planned_stop_at"]) == utc(plan["planned_stop_at"])
            and utc(value["cleanup_by"]) == utc(plan["cleanup_by"])
            and utc(value["publisher_end"]) == utc(plan["cleanup_by"]) + 5 and utc(value["publisher_cleanup_by"]) == utc(plan["cleanup_by"]) + 10
            and utc(value["tls_capacity_end"]) > utc(value["publisher_cleanup_by"]) + 2
            and all(type(value[k]) is int and value[k] == n for k, n in (("lease_seconds", 600), ("refresh_seconds", 60), ("target_count", 3)))
            and all(HEX64.fullmatch(value[k]) for k in ("control_profile_sha256", "data_domain_sha256", "control_operator_sha256", "data_operator_sha256")),
            "verified complete owner capacity required")
    return value


def publisher_records(raw, plan, capacity_sha256, *, joined=False, stop_sha256=None):
    records = [decode(line) for line in raw.splitlines() if line.startswith(b"{")]
    require(0 < len(records) <= 128 and all(type(v.get("schema")) is int and v["schema"] == 1 and v.get("profile") == PROFILE and v.get("revision") == plan["source"]["head"]
            and v.get("nonce") == plan["cohort"] and type(v.get("pid")) is int and v["pid"] > 0 for v in records)
            and len({v["pid"] for v in records}) == 1 and records[0].get("event") == "publisher_ready"
            and records[0].get("capacity_sha256") == capacity_sha256, "actual owned publisher ready required")
    events = [v.get("event") for v in records]
    require(all(event == "publisher_status" for event in (events[1:-1] if joined else events[1:])), "unexpected publisher lifecycle")
    if joined:
        require(events[-1] == "publisher_joined" and len(records) >= 2 and records[-1].get("ok") is True
                and isinstance(stop_sha256, str) and HEX64.fullmatch(stop_sha256) and records[-1].get("owner_stop_sha256") == stop_sha256
                and utc(records[-1]["stopped_at"]) <= utc(plan["cleanup_by"]), "normal publisher join with actual owned stop acknowledgement required")
    for value in records:
        status = value.get("status", {})
        require(status.get("failure") in (("none", "stopped") if joined and value is records[-1] else ("none",))
                and utc(status["end"]) == utc(plan["cleanup_by"]) + 5 and utc(status["cleanup_by"]) == utc(plan["cleanup_by"]) + 10
                and len(status.get("streams", [])) == 2 and {s.get("kind") for s in status["streams"]} == {"control", "data"}, "publisher failure or budget drift")
        for stream in status["streams"]:
            require(type(stream.get("journal_version")) is int and stream["journal_version"] > 0 and stream.get("pending") is False
                    and len(stream.get("targets", [])) == 3 and {t.get("ordinal") for t in stream["targets"]} == {1, 2, 3}
                    and all(t.get("failure") in ("", "none") and t.get("candidate_matches") is True
                            and t.get("observed_version") == stream["journal_version"] for t in stream["targets"]), "incomplete membership publication")
    return records


def owner_stop_record(plan):
    return {"schema": 1, "kind": "controlled-native-provider-owner-stop", "revision": plan["source"]["head"], "nonce": plan["cohort"],
            "planned_stop_at": plan["planned_stop_at"], "cleanup_by": plan["cleanup_by"]}


def go_json(value):
    # These are decoded Go struct projections, retaining their declaration order.
    raw = json.dumps(value, separators=(",", ":"), ensure_ascii=False, allow_nan=False)
    for before, after in (("&", "\\u0026"), ("<", "\\u003c"), (">", "\\u003e"), ("\u2028", "\\u2028"), ("\u2029", "\\u2029")):
        raw = raw.replace(before, after)
    return raw.encode()


def lease_transcript(raw, plan, records, capacity_sha256, public_capacity):
    """Replay real envelope bytes/intervals; the native child verifies signatures."""
    terminal, first = records[-1], records[0]
    require(0 < len(raw) <= 4 << 20, "bounded native lease transcript required")
    rows = [decode(line) for line in raw.splitlines()]
    require(1 <= len(rows) <= 128
            and terminal.get("owner_window") == OWNER_WINDOW and terminal.get("owner_capacity_sha256") == capacity_sha256
            and terminal.get("owner_lease_transcript_sha256") == digest(raw)
            and type(terminal.get("owner_lease_observations")) is int and terminal["owner_lease_observations"] == len(rows), "actual native lease transcript required")
    keys = {"observed_at", "control_profile_sha256", "data_domain_sha256", "control_manifest", "data_manifest"} | {
        kind + suffix for kind in ("control", "data") for suffix in ("_version", "_issued_at", "_expires_at", "_sha256")}
    previous = None
    stable = None
    for row in rows:
        has_native = "native_low" in row or "native_high" in row
        require(set(row) == keys | ({"native_low", "native_high"} if has_native else set()), "exact lease observation required")
        observed = utc(row["observed_at"])
        require(previous is None or observed >= utc(previous["observed_at"]), "lease observation time rollback")
        low, high = (utc(row["native_low"]), utc(row["native_high"])) if has_native else (observed, observed)
        if has_native:
            require(low <= high, "ordered owned native bounds required")
        for kind in ("control", "data"):
            version, issued, expires, sha = (row[kind + suffix] for suffix in ("_version", "_issued_at", "_expires_at", "_sha256"))
            require(type(version) is int and version > 0 and HEX64.fullmatch(sha)
                    and 0 < utc(expires) - utc(issued) <= 600 and utc(issued) - 2 <= low <= high < utc(expires) - 2,
                    "current genuine ten-minute lease cut required")
            envelope = base64.b64decode(row[kind + "_manifest"], validate=True)
            require(0 < len(envelope) <= 64 << 10 and base64.b64encode(envelope).decode() == row[kind + "_manifest"]
                    and digest(envelope) == sha, "actual signed envelope digest required")
            signed = decode(envelope)
            require(set(signed) == {"payload", "signature"} and len(base64.b64decode(signed["signature"], validate=True)) == 64,
                    "actual signed envelope shape required")
            require(go_json(signed) == envelope, "canonical native signed envelope required")
            payload = signed["payload"]
            require(set(payload) == ({"version", "profile", "issued_at", "expires_at"} if kind == "control" else {"version", "domain", "issued_at", "expires_at", "members"})
                    and payload["version"] == version and utc(payload["issued_at"]) == utc(issued) and utc(payload["expires_at"]) == utc(expires), "lease payload/observation mismatch")
            name, prefix = ("profile", b"lantern.peer.control.profile.v1\0") if kind == "control" else ("domain", b"lantern.peer.domain.v1\0")
            field = "control_profile_sha256" if kind == "control" else "data_domain_sha256"
            require(digest(prefix + go_json(payload[name])) == row[field] == public_capacity[field], "stable signed profile/domain required")
            projection = (payload["profile"] if kind == "control" else (payload["domain"], payload["members"]))
            if stable is None:
                stable = {}
            require(kind not in stable or projection == stable[kind], "signed cohort/generation/member drift")
            stable[kind] = projection
            if kind == "data":
                require(payload["domain"].get("current_profile") == first["authority_profile"], "actual native authority profile drift")
            if previous is not None:
                require(version >= previous[kind + "_version"] and utc(issued) >= utc(previous[kind + "_issued_at"])
                        and utc(issued) - 2 < utc(previous[kind + "_expires_at"]) - 2, "signed renewal gap or rollback")
                if version == previous[kind + "_version"]:
                    require(all(row[kind + suffix] == previous[kind + suffix] for suffix in ("_issued_at", "_expires_at", "_sha256")), "same-version lease replacement")
        previous = row
    begin = utc(first["host_seed_interval_from"])
    end = utc(terminal["stopped_at"] if terminal.get("event") == "joined" else terminal["host_seed_interval_until"])
    through = min(utc(rows[-1][kind + "_expires_at"]) for kind in ("control", "data")) - 2
    require(utc(rows[0]["observed_at"]) <= begin <= end and utc(rows[-1]["observed_at"]) <= utc(plan["cleanup_by"])
            and (terminal.get("event") != "joined" or utc(rows[-1]["observed_at"]) <= end)
            and all(utc(rows[0][kind + "_issued_at"]) - 2 <= begin < utc(rows[0][kind + "_expires_at"]) - 2 for kind in ("control", "data"))
            and utc(terminal["owner_lease_through"]) == through and end < through and end <= utc(plan["cleanup_by"]), "actual initialized/serving interval not covered")
    native_indices = [i for i, row in enumerate(rows) if "native_low" in row]
    if terminal.get("event") == "joined":
        require(len(native_indices) >= 2 and native_indices == list(range(native_indices[0], native_indices[-1]+1))
                and terminal.get("owner_lease_time_scope") == "native-serving-with-host-cleanup"
                and type(terminal.get("owner_native_lease_observations")) is int and terminal["owner_native_lease_observations"] == len(native_indices)
                and utc(terminal["owner_native_lease_from"]) == utc(rows[native_indices[0]]["observed_at"])
                and utc(terminal["owner_native_lease_until"]) == utc(rows[native_indices[-1]]["observed_at"])
                and begin <= utc(terminal["owner_native_lease_from"]) <= utc(records[1]["ready_at"])
                and utc(terminal["owner_native_lease_from"]) <= utc(terminal["owner_native_lease_until"]) <= end, "actual owned native serving witness block required")
    else:
        require(not native_indices and terminal.get("owner_lease_time_scope") == "host-auxiliary"
                and type(terminal.get("owner_native_lease_observations")) is int and terminal["owner_native_lease_observations"] == 0
                and "owner_native_lease_from" not in terminal and "owner_native_lease_until" not in terminal
                and "owner_stop_sha256" not in terminal, "initializer must not claim native serving time/stop acknowledgement")
    return {"sha256": digest(raw), "observations": len(rows), "through": terminal["owner_lease_through"],
            "time_scope": terminal["owner_lease_time_scope"], "native_observations": len(native_indices)}


def cohort_material_snapshot(output, renewing, owner_stop=None):
    value = material_snapshot(output)
    if renewing:
        for i in (1, 2, 3):
            for kind in ("control", "data"):
                name = f"views/{i-1}/identity/nodes/node-{i}/membership/{kind}.json"
                require(name in value and "sha256" in value[name], "exact public membership target missing")
                del value[name]  # ONLY these six files may be atomically renewed.
        public = output / "publisher-public"
        require({p.name for p in public.iterdir()} == {"capacity.json"}, "unexpected public capacity material")
        info = public.stat()
        require(stat.S_ISDIR(info.st_mode) and stat.S_IMODE(info.st_mode) == 0o700 and info.st_uid == os.getuid(), "owned public capacity directory required")
        capacity = public / "capacity.json"
        info = capacity.lstat()
        require(stat.S_ISREG(info.st_mode) and stat.S_IMODE(info.st_mode) == 0o600 and info.st_uid == os.getuid() and info.st_nlink == 1,
                "immutable unique public capacity required")
        value["publisher-public/capacity.json"] = {"sha256": digest(read(capacity)), "mode": 0o600}
        control = canonical(output / "owner-control")
        info = control.stat()
        require(stat.S_ISDIR(info.st_mode) and stat.S_IMODE(info.st_mode) == 0o700 and info.st_uid == os.getuid(), "owned stop control directory required")
        require({p.name for p in control.iterdir()} == ({"stop.json"} if owner_stop else set()), "unexpected owner control material")
        if owner_stop:
            info = (control / "stop.json").lstat()
            require(stat.S_ISREG(info.st_mode) and info.st_nlink == 1 and owner_stop["path"] == str(control / "stop.json")
                    and digest(read(control / "stop.json", maximum=4096)) == owner_stop["sha256"], "exact create-only owner stop record required")
        value["owner-control"] = {"directory": True, "mode": 0o700}
    return value


def child_records(raw, plan, mode, node, config_sha256, *, joined=False):
    records, traces = [], []
    for line in raw.splitlines():
        if not line.startswith(b"{"):
            continue
        value = decode(line)
        if value.get("kind") == "controlled_public_trace":
            traces.append(value)
        elif value.get("profile") == PROFILE:
            require(value.get("nonce") == plan["cohort"] and value.get("revision") == plan["source"]["head"] and value.get("mode") == mode
                    and value.get("node") == node and type(value.get("pid")) is int and value["pid"] > 0
                    and value.get("config_sha256") == config_sha256 and re.fullmatch(r"current-v2:[0-9a-f]{64}", value.get("authority_profile", "")), "actual native4 child binding mismatch")
            records.append(value)
    require(len({r["pid"] for r in records}) <= 1 and len({r["authority_profile"] for r in records}) <= 1, "same owned App/cohort required")
    if mode == "initialize":
        require(len(records) == 1 and records[0].get("event") == "initialized" and records[0].get("ok") is True
                and counters(records[0].get("counters")) and "repeat" not in records[0] and not traces, "actual initializer terminal counters required")
    elif joined:
        require([r.get("event") for r in records] == ["constructed", "ready", "joined"], "exact owned lifecycle required")
        before, ready, after = records
        require(counters(before.get("counters")) and counters(after.get("counters")) and after.get("repeat") == after["counters"]
                and after.get("ok") is True and all(after["counters"][k] >= before["counters"][k] for k in ("attempts", "writes", "installations")), "settled stable owner counters required")
        require(len(traces) == 1 and traces[0].get("dropped") == 0 and type(traces[0].get("dropped")) is int
                and isinstance(traces[0].get("events"), list), "complete bounded typed decision trace required")
        require(utc(ready["ready_at"]) < utc(after["stopped_at"]) <= utc(plan["cleanup_by"]), "actual stop interval refused")
    else:
        require([r.get("event") for r in records] in ([], ["constructed"], ["constructed", "ready"]), "unexpected pre-ready lifecycle")
    if records:
        require(utc(records[0]["host_seed_interval_from"]) <= utc(records[0]["host_seed_interval_until"]), "enclosing synthetic host seed interval required")
    return records


def routes(plan):
    """Caddy accepts only generated new-cohort aliases, never caller upstream text."""
    prefix, public = "cnp-" + plan["cohort"][:16], plan["public"]
    host = urlsplit(public["origin"]).hostname
    # A scoped network alias resolves this public cert name ONLY to new node1.
    # It is not a provider hostname override; Google/Apple DNS is untouched.
    edge = ("{\n admin off\n persist_config off\n}\n:8443 {\n tls /edge-tls/server.pem /edge-tls/server-key.pem\n"
            " @wire path /graph.v1.*/* /grpc.health.v1.Health/*\n handle @wire {\n"
            f"  reverse_proxy https://{host}:6380 {{\n   header_up Host {{hostport}}\n"
            "   transport http {\n    tls_trust_pool file /edge-tls/server.pem\n   }\n  }\n }\n"
            f" handle {{\n  reverse_proxy {prefix}-admin:8080\n }}\n}}\n")
    admin_env = {"LANTERN_ADMIN_SERVER_UPSTREAM": f"https://{host}:6380", "LANTERN_ADMIN_SERVER_CA_FILE": "/server-trust/server.pem"}
    return edge.encode(), admin_env


def container_argv(plan, role, name, mounts, env_file=None, *, alias=None, network="none"):
    args = DOCKER + ["create", "--pull=never", "--name", name, "--label", "lantern.task=2149-controlled-native-providers",
                    "--label", "lantern.cohort=" + plan["cohort"], "--network", network, "--read-only", "--cap-drop=ALL",
                    "--security-opt=no-new-privileges", "--user", f"{os.getuid()}:{os.getgid()}", "--pids-limit=128", "--memory=768m", "--cpus=1",
                    "--log-driver=json-file", "--log-opt=max-size=2m", "--log-opt=max-file=1", "--tmpfs", "/tmp:rw,nosuid,nodev,size=64m,mode=1777"]
    for value in alias or []:
        args += ["--network-alias", value]
    for source, target, writable in mounts:
        args += ["--mount", f"type=bind,source={source},target={target}" + ("" if writable else ",readonly")]
    if role == "admin":
        args += ["--tmpfs", "/etc/caddy/conf.d:rw,nosuid,nodev,size=1m,mode=1777", "--tmpfs", "/config:rw,nosuid,nodev,size=1m,mode=1777", "--tmpfs", "/data:rw,nosuid,nodev,size=1m,mode=1777"]
    if role == "edge":
        args += ["--publish", f"{plan['public']['lan_ip']}:24443:8443/tcp", "--entrypoint", "/usr/bin/caddy"]
    if env_file:
        args += ["--env-file", str(env_file)]
    return args + [plan[role if role in ("admin", "edge") else "build"]["image"]]


def descriptor(plan, plan_sha256, started, ready, authority, capacity=None):
    require(re.fullmatch(r"current-v2:[0-9a-f]{64}", authority), "original signed cohort required")
    renewing = renewing_membership(plan)
    require(15 <= utc(plan["planned_stop_at"]) - ready <= plan["lifetime_seconds"] <= (1800 if renewing else 540), "finite actual serving interval required")
    value = {"schema": 1, "kind": KIND, "profile": PROFILE, "provider_source": "real-google-apple", "time_source": "controlled-source",
            "synthetic_utc": True, "build_kind": "test-only", "build_tags": ["lantern_test_time"], **{k: plan["source"][k] for k in SOURCE_FIELDS},
            "plan_sha256": plan_sha256, "cohort": plan["cohort"], "authority_profile": authority, "image": plan["build"]["image"],
            "admin_image": plan["admin"]["image"], "edge_image": plan["edge"]["image"], "public_origin": plan["public"]["origin"],
            "admin_backend": "new-cohort-node-1-only", "setup_from": instant(started), "serving_from": instant(ready), "serving_until": plan["planned_stop_at"],
            "cleanup_by": plan["cleanup_by"], "registration_at_genesis": False, "management_mutations_by_controller": 0,
            "provider_components_passed": 0, "full_provider_cells_passed": 0, "signup_passed": 0, "real_utc_claim": False,
            "normal_qualification_claim": False, "admin_strong_reauthentication_claim": False,
            "route_classes": ["compiled-loopback-udp-time", "production-public-https-provider", "same-origin-admin-bff-new-node1"],
            "input_refs": references(plan)}
    if renewing:
        require(isinstance(capacity, dict) and set(capacity) == {"path", "sha256"} and HEX64.fullmatch(capacity["sha256"]), "owner capacity descriptor required")
        value.update(owner_window=dict(OWNER_WINDOW), owner_capacity=capacity, owned_container_count=11)
    return value


def validate_descriptor(value, plan, plan_sha256, authority, capacity=None):
    require(encoded(value) == encoded(descriptor(plan, plan_sha256, utc(value["setup_from"]), utc(value["serving_from"]), authority, capacity)),
            "native provider descriptor drift/extra credential refused")
    return value


def scratch_metadata(fields):
    require(set(fields) == {"entrypoint", "cmd", "env_count"} and fields["entrypoint"] == ["/controlled-native-providers.test"]
            and fields["cmd"] in (None, []) and type(fields["env_count"]) is int and fields["env_count"] == 0,
            "exact Scratch test entrypoint/empty inherited env required")


def frontend_probe(origin, lan_ip, public_roots, index_sha256):
    """Isolated bounded child: exact public DNS/TLS Health + immutable Admin bytes.

    Parent Commands owns its whole timeout (including DNS), kills/joins this
    direct Python process on expiry. No provider, cookie or management request.
    """
    import hashlib
    import json
    import socket
    import ssl
    import urllib.request
    from urllib.parse import urlsplit
    host = urlsplit(origin).hostname
    addresses = sorted({row[4][0] for row in socket.getaddrinfo(host, 24443, socket.AF_UNSPEC, socket.SOCK_STREAM)})
    if addresses != [lan_ip]:
        raise ValueError("public origin does not resolve to the approved LAN address")
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *_args, **_kwargs):
            raise ValueError("frontend redirect refused")
    tls = ssl.create_default_context(cafile=public_roots)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=tls), NoRedirect())
    health = urllib.request.Request(origin + "/grpc.health.v1.Health/Check", data=b"{}", method="POST",
                                    headers={"Content-Type": "application/json", "Connect-Protocol-Version": "1", "Accept-Encoding": "identity"})
    def unique(pairs):
        value = {}
        for key, item in pairs:
            if key in value:
                raise ValueError("ambiguous public Health JSON")
            value[key] = item
        return value
    with opener.open(health, timeout=5) as response:
        body = response.read(1025)
        health_value = json.loads(body, object_pairs_hook=unique)
        if response.status != 200 or len(body) > 1024 or not isinstance(health_value, dict) or set(health_value) != {"status"} or health_value["status"] not in ("SERVING", "SERVING_STATUS_SERVING"):
            raise ValueError("owned public Health not SERVING")
    request = urllib.request.Request(origin + "/index.html", headers={"Accept-Encoding": "identity"})
    with opener.open(request, timeout=5) as response:
        body = response.read((2 << 20) + 1)
        if response.status != 200 or len(body) > 2 << 20 or hashlib.sha256(body).hexdigest() != index_sha256:
            raise ValueError("exact-source Admin index content mismatch")
    return {"origin": origin, "dns_addresses": addresses, "health_status": health_value["status"], "admin_index_sha256": index_sha256}


def frontend_probe_argv(plan, admitted):
    # The reviewed source function is the only child code. -I ignores Python
    # injection paths; no operational controller/helper is imported in the child.
    code = "import json,sys\n" + inspect.getsource(frontend_probe) + "\nprint(json.dumps(frontend_probe(*sys.argv[1:])))\n"
    return [sys.executable, "-I", "-B", "-c", code, plan["public"]["origin"], plan["public"]["lan_ip"],
            plan["build"]["public_roots"]["path"], admitted["admin_index_sha256"]]


def approved_dispatch(plan, plan_sha256, approval):
    require(approval.get("kind") == PROFILE + "-owner-dispatch" and approval.get("status") == "APPROVE"
            and approval.get("owner_present") is True and approval.get("plan_sha256") == plan_sha256
            and approval.get("seed_verification_sha256") == plan["seed"]["verification"]["sha256"], "single reviewed owner-present dispatch required")
    require(approval.get("secret_and_tls_whole_window_verified") is True and approval.get("approved_public_origin") == plan["public"]["origin"]
            and approval.get("admin_and_edge_source_reviewed") is True, "actual secret/TLS/callback/Admin whole-window admission pending")


def run(plan, plan_sha256, approval, approval_sha256, dispatch_refs=()):
    resolved(plan)
    approved_dispatch(plan, plan_sha256, approval)
    admitted, window = admitted_inputs(plan), Window(plan)
    renewing = renewing_membership(plan)
    source_before, inputs_before = source_snapshot(plan, window), input_snapshot(plan, dispatch_refs)
    output = canonical(plan["output"])
    output.mkdir(mode=0o700)
    commands = Commands(output, window)
    names, identities, serving = [], [], []
    configs, authority, immutable = configurations(plan, admitted), None, None
    publisher, capacity, capacity_ref, capacity_data, owner_stop = None, None, None, None, None
    started, ready_at, interrupted = time.time(), None, set()
    previous = {n: signal.getsignal(n) for n in (signal.SIGINT, signal.SIGTERM)}
    for number in previous:
        signal.signal(number, lambda n, _f: interrupted.add(n))
    result = {"schema": 1, "kind": KIND + "-lifecycle", "status": "FAIL", "plan_sha256": plan_sha256, "cohort": plan["cohort"],
              "head": plan["source"]["head"], "cleanup_known": False, "driver_sent_sigkill": False, "retry": False}

    def create(stage, index, mounts, env_file, tail, *, role="build", aliases=None, network="none"):
        require(not interrupted and window.left(setup=True) > 0, "late/interrupted dispatch refused")
        name = f"cnp-{plan['cohort'][:16]}-{stage}-{index}"
        argv = container_argv(plan, role, name, mounts, env_file, alias=aliases, network=network) + tail
        names.append(name)
        new_file(output / f"create-attempt-{len(names):02d}.json", {"name": name, "argv": argv})
        identity = commands.run(argv)[0].decode().strip()
        require(HEX64.fullmatch(identity), "exact created identity required")
        identities.append(identity)
        return identity

    def environment(index, mode, label=None):
        values = {"LANTERN_AUTH_MODE": "oidc", "LANTERN_SECURITY_CONFIG_JSON": json.dumps(configs[index], separators=(",", ":")),
                  "LANTERN_PORT": "6380", "LANTERN_METRICS_ADDR": "127.0.0.1:9090", "LANTERN_BACKUP_ENABLED": "false",
                  "LANTERN_BACKUP_RESTORE_ON_START": "false", "LANTERN_DRAIN_DELAY_SECONDS": "0", "LANTERN_LOG_LEVEL": "warn"}
        if not renewing or label != "provision":
            child = child_input(plan, configs[index], admitted, mode, capacity)
            path = output / "inputs" / f"{index}-{label or mode}.json"
            new_file(path, child)
            values.update(CONTROLLED_NATIVE_PROVIDER_CHILD_INPUT="/inputs/" + path.name,
                          CONTROLLED_NATIVE_PROVIDER_CHILD_INPUT_SHA256=digest(path.read_bytes()))
        env = output / "inputs" / f"{index}-{label or mode}.env"
        new_file(env, ("\n".join(k + "=" + v for k, v in values.items()) + "\n").encode())
        return env

    def publisher_running(*, include_apps=False, with_records=True):
        selected = [publisher] + (serving[:3] if include_apps else [])
        states = [decode(line) for line in commands.run(DOCKER + ["inspect", "--format", "{{json .State}}", *selected])[0].splitlines()]
        require(len(states) == len(selected) and all(state.get("Running") is True and state.get("Status") == "running"
                and type(state.get("Pid")) is int and state["Pid"] > 0 and state.get("OOMKilled") is False and state.get("Error") == "" for state in states),
                "owned membership publisher/App exited before stop")
        if with_records:
            return publisher_records(commands.run(DOCKER + ["logs", publisher])[0], plan, capacity["sha256"])

    try:
        new_file(output / "STARTED.json", {"schema": 1, "kind": PROFILE + "-start", "status": "SETUP", "driver_pid": os.getpid(),
                 "source": plan["source"], "cohort": plan["cohort"], "plan_sha256": plan_sha256, "root_dispatch_sha256": approval_sha256,
                 "started_at": instant(started), "planned_stop_at": plan["planned_stop_at"], "cleanup_by": plan["cleanup_by"]})
        if renewing:
            result["owner_window"] = dict(OWNER_WINDOW)
        for role in ("build", "admin", "edge"):
            require(commands.run(DOCKER + ["image", "inspect", "--format", "{{.Id}}", plan[role]["image"]])[0].decode().strip() == plan[role]["image"], "admitted immutable image missing")
        fields = decode(commands.run(DOCKER + ["image", "inspect", "--format", '{"entrypoint":{{json .Config.Entrypoint}},"cmd":{{json .Config.Cmd}},"env_count":{{len .Config.Env}}}', plan["build"]["image"]])[0])
        scratch_metadata(fields)
        revision = commands.run(DOCKER + ["image", "inspect", "--format", '{{index .Config.Labels "org.opencontainers.image.revision"}}', plan["admin"]["image"]])[0].decode().strip()
        require(revision == plan["source"]["head"], "actual Admin image source label mismatch")
        require(commands.run(DOCKER + ["network", "inspect", "--format", "{{.Id}}", plan["network"]["id"]])[0].decode().strip() == plan["network"]["id"]
                and commands.run(DOCKER + ["network", "inspect", "--format", "{{.Name}}", plan["network"]["id"]])[0].decode().strip() == plan["network"]["name"]
                and decode(commands.run(DOCKER + ["network", "inspect", "--format", "{{json .Containers}}", plan["network"]["id"]])[0]) == {}, "new empty dedicated bridge required")
        for name in ("material", "operator", "state", "inputs", "views"):
            (output / name).mkdir(mode=0o700)
        env = output / "inputs" / "description.env"
        new_file(env, b"CONTROLLED_NATIVE_PROVIDER_DESCRIBE=1\n" + (b"CONTROLLED_NATIVE_PROVIDER_DESCRIBE_OWNER_WINDOW=1\n" if renewing else b""))
        identity = create("description", 0, [], env, ["-test.v", "-test.run=^TestControlledNativeProviderProfileDescription$", "-test.timeout=20s"])
        raw = commands.run(DOCKER + ["start", "--attach", identity], timeout=25)[0]
        require([decode(line) for line in raw.splitlines() if line.startswith(b"{")] == [decode(reference(plan["build"]["description"]))], "actual same-image description missing")
        identity = create("provision", 0, [(output / "material", "/material", True), (output / "operator", "/operator", True)],
                          environment(0, "initialize", "provision"), ["provision-security", "--operator-directory", "/operator/custody", "--valid-for", "1h"])
        commands.run(DOCKER + ["start", "--attach", identity], timeout=60)
        if renewing:
            # Finish all views before the publisher takes its six stable leases.
            for index in range(3):
                view = output / "views" / str(index)
                (view / "identity" / "nodes").mkdir(mode=0o700, parents=True)
                shutil.copytree(output / "material" / "identity" / "nodes" / f"node-{index+1}", view / "identity" / "nodes" / f"node-{index+1}")
                (output / "state" / str(index)).mkdir(mode=0o700)
            for name in ("publisher-journal", "publisher-public", "owner-control"):
                (output / name).mkdir(mode=0o700)
            config = publisher_configuration(plan, decode(read(output / "operator" / "custody" / "publisher.json")))
            config_path = output / "inputs" / "publisher-config.json"
            new_file(config_path, config)
            publisher_input = {"schema": 1, "profile": PROFILE, "revision": plan["source"]["head"], "nonce": plan["cohort"],
                               "config_path": "/publisher-config.json", "config_sha256": digest(read(config_path)), "state_path": "/publisher-journal/state.json",
                               "capacity_path": "/publisher-public/capacity.json", "planned_stop_at": plan["planned_stop_at"], "cleanup_by": plan["cleanup_by"]}
            new_file(output / "inputs" / "publisher-input.json", publisher_input)
            env = output / "inputs" / "publisher.env"
            new_file(env, ("CONTROLLED_NATIVE_PROVIDER_PUBLISHER_INPUT=/publisher-input.json\nCONTROLLED_NATIVE_PROVIDER_PUBLISHER_INPUT_SHA256="
                           + digest(read(output / "inputs" / "publisher-input.json")) + "\n").encode())
            publisher = create("publisher", 0, publisher_mounts(output), env, ["-test.v", "-test.run=^" + PUBLISHER_CHILD + "$", "-test.timeout=32m"])
            commands.run(DOCKER + ["start", publisher])
            # O_EXCL makes the path visible before Write/Sync/Close. Only the
            # producer's subsequent ready event admits a capacity-file read.
            while True:
                require(not interrupted and window.left(setup=True) > 0 and window.monotonic()-window.started_mono < 90, "verified publisher capacity unavailable")
                state = decode(commands.run(DOCKER + ["inspect", "--format", "{{json .State}}", publisher])[0])
                require(state.get("Running") is True, "publisher failed before capacity")
                records = [decode(line) for line in commands.run(DOCKER + ["logs", publisher])[0].splitlines() if line.startswith(b"{")]
                if records:
                    require(records[0].get("event") == "publisher_ready", "publisher capacity not published")
                    break
                time.sleep(0.2)
            raw = read(output / "publisher-public" / "capacity.json")
            capacity_data = renewal_capacity(raw, plan)
            capacity_ref = {"path": str(output / "publisher-public" / "capacity.json"), "sha256": digest(raw)}
            capacity = {"path": "/owner-capacity.json", "sha256": digest(raw)}
            publisher_running()
        for index in range(3):
            view, state = output / "views" / str(index), output / "state" / str(index)
            if not renewing:
                (view / "identity" / "nodes").mkdir(mode=0o700, parents=True)
                shutil.copytree(output / "material" / "identity" / "nodes" / f"node-{index+1}", view / "identity" / "nodes" / f"node-{index+1}")
                state.mkdir(mode=0o700)
            mounts = [(view, "/material", False), (state, "/state", True), (output / "inputs", "/inputs", False)]
            if renewing:
                mounts.append((output / "publisher-public" / "capacity.json", "/owner-capacity.json", False))
            identity = create("initialize", index, mounts, environment(index, "initialize"), ["-test.v", "-test.run=^" + CHILD + "$", "-test.timeout=80s"])
            raw = commands.run(DOCKER + ["start", "--attach", identity], timeout=75)[0]
            records = child_records(raw, plan, "initialize", f"node-{index+1}", child_input(plan, configs[index], admitted, "initialize", capacity)["config_sha256"])
            if renewing:
                result.setdefault("owner_lease_transcripts", {})[f"node-{index+1}/initialize"] = lease_transcript(
                    read(state / "controlled-native-provider-owner-leases-initialize.jsonl", maximum=4 << 20), plan, records, capacity["sha256"], capacity_data)
                publisher_running()
            authority = authority or records[0]["authority_profile"]
            require(records[0]["authority_profile"] == authority, "initializer original cohort drift")
            aliases = [f"cnp-{plan['cohort'][:16]}-{index+1}"] + ([urlsplit(plan["public"]["origin"]).hostname] if index == 0 else [])
            if renewing:
                mounts.append((output / "owner-control", "/owner-control", False))
            identity = create("resume", index, mounts, environment(index, "resume"), ["-test.v", "-test.run=^" + CHILD + "$", "-test.timeout=" + ("32m" if renewing else "11m")], aliases=aliases, network=plan["network"]["id"])
            serving.append(identity)
        edge, admin_env = routes(plan)
        new_file(output / "inputs" / "edge.Caddyfile", edge)
        for filename, raw in (("server.pem", admitted["certificate"].encode()), ("server-key.pem", admitted["key"].encode())):
            new_file(output / "inputs" / filename, raw)
        admin_file = output / "inputs" / "admin.env"
        new_file(admin_file, ("\n".join(k + "=" + v for k, v in admin_env.items()) + "\n").encode())
        identity = create("admin", 0, [(output / "inputs" / "server.pem", "/server-trust/server.pem", False)], admin_file, [], role="admin",
                          aliases=[f"cnp-{plan['cohort'][:16]}-admin"], network=plan["network"]["id"])
        serving.append(identity)
        identity = create("edge", 0, [(output / "inputs" / "edge.Caddyfile", "/etc/caddy/Caddyfile", False),
                                     (output / "inputs" / "server.pem", "/edge-tls/server.pem", False),
                                     (output / "inputs" / "server-key.pem", "/edge-tls/server-key.pem", False)], None,
                          ["run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"], role="edge", network=plan["network"]["id"])
        serving.append(identity)
        immutable = cohort_material_snapshot(output, renewing)
        new_file(output / "immutable-before.json", {"source": source_before, "inputs": inputs_before, "material": immutable})
        for identity in serving:
            commands.run(DOCKER + ["start", identity])
        observed = set()
        while len(observed) < 3:
            require(not interrupted and window.left(setup=True) > 0 and window.monotonic()-window.started_mono < 90, "all current owners did not become ready")
            for index, identity in enumerate(serving[:3]):
                records = child_records(commands.run(DOCKER + ["logs", identity])[0], plan, "resume", f"node-{index+1}", child_input(plan, configs[index], admitted, "resume", capacity)["config_sha256"])
                require(all(r["authority_profile"] == authority for r in records), "resume original cohort drift")
                if len(records) == 2:
                    observed.add(index)
            time.sleep(1)
            if renewing:
                publisher_running()
        # Record exact owned frontend running states, not a provider or UI result.
        for identity in serving[3:]:
            state = decode(commands.run(DOCKER + ["inspect", "--format", "{{json .State}}", identity])[0])
            require(state.get("Running") is True and state.get("OOMKilled") is False and state.get("Error") == "", "owned Admin/edge not running")
        frontend = decode(commands.run(frontend_probe_argv(plan, admitted), timeout=15)[0])
        require(set(frontend) == {"origin", "dns_addresses", "health_status", "admin_index_sha256"} and frontend["health_status"] in ("SERVING", "SERVING_STATUS_SERVING")
                and frontend["origin"] == plan["public"]["origin"] and frontend["dns_addresses"] == [plan["public"]["lan_ip"]]
                and frontend["admin_index_sha256"] == admitted["admin_index_sha256"], "actual public Health/Admin probe binding mismatch")
        new_file(output / "frontend-readiness.json", frontend)
        ready_at = time.time()
        new_file(output / "fixture.json", descriptor(plan, plan_sha256, started, ready_at, authority, capacity_ref))
        new_file(output / "ready", (PROFILE + " schema1\n").encode())
        next_publisher_status = window.monotonic()
        window.monitoring = renewing
        while window.left() > 0:
            require(not interrupted, "controlled provider window interrupted")
            if renewing and window.left() > 2:
                status_due = window.monotonic() >= next_publisher_status
                publisher_running(include_apps=True, with_records=status_due)
                if status_due:
                    next_publisher_status = window.monotonic() + OWNER_WINDOW["refresh_seconds"]
            if (output / "release").exists():
                require(read(output / "release", maximum=128) == b"release\n", "exact private normal release required")
                break
            time.sleep(max(0, min(OWNER_STATE_INTERVAL_SECONDS, window.left())) if renewing else 0.2)
        result["status"] = "PASS"
    except BaseException as error:
        result["failure_class"] = type(error).__name__
    finally:
        commands.cleanup = True
        states, cleanup_errors = [], []

        def cleanup_error(identity, stage, error):
            error_class = type(error).__name__
            if error_class not in {"ValueError", "OSError", "RuntimeError", "TypeError", "KeyError", "TimeoutError",
                                   "FileNotFoundError", "PermissionError", "InterruptedError", "UnicodeDecodeError",
                                   "JSONDecodeError", "KeyboardInterrupt", "SystemExit", "AssertionError"}:
                error_class = "OtherException"
            cleanup_errors.append({"id": identity, "stage": stage, "exception_class": error_class})

        if renewing:
            try:
                path = output / "owner-control" / "stop.json"
                new_file(path, owner_stop_record(plan))
                raw = read(path, maximum=4096)
                require(raw == encoded(owner_stop_record(plan)), "actual bound owner stop record changed")
                owner_stop = {"path": str(path), "sha256": digest(raw)}
                result["owner_stop"] = owner_stop
            except BaseException as error:
                result.update(status="FAIL", owner_stop_publication_failure_class=type(error).__name__)
                # Still stop every owned container; no record failure may skip cleanup.
        if identities:
            try:
                commands.run(DOCKER + ["stop", "--time", "10", *identities], timeout=15, check=False)
            except BaseException as error:
                cleanup_error(None, "stop", error)
        for identity in identities:
            state = {}
            try:
                raw, code = commands.run(DOCKER + ["inspect", "--format", "{{json .State}}", identity], check=False)
                state = decode(raw) if code == 0 else {}
            except BaseException as error:
                cleanup_error(identity, "inspect", error)
            stopped = state.get("Running") is False and state.get("Pid") == 0 and state.get("Restarting") is False and state.get("Paused") is False
            normal = stopped and state.get("Status") == "exited" and state.get("ExitCode") == 0 and state.get("OOMKilled") is False and state.get("Error") == ""
            states.append({"id": identity, "stopped": stopped, "normal_exit": normal, "exit": state.get("ExitCode")})
            proof_stage = "child_proof" if identity in serving[:3] else "publisher_proof"
            try:
                if identity in serving[:3] and stopped:
                    records = child_records(commands.run(DOCKER + ["logs", identity])[0], plan, "resume", f"node-{serving.index(identity)+1}",
                                            child_input(plan, configs[serving.index(identity)], admitted, "resume", capacity)["config_sha256"], joined=True)
                    require(all(r["authority_profile"] == authority for r in records), "final original cohort drift")
                    if renewing:
                        require(owner_stop is not None and records[-1].get("owner_stop_sha256") == owner_stop["sha256"], "actual native stop acknowledgement required")
                        result.setdefault("owner_lease_transcripts", {})[f"node-{serving.index(identity)+1}/resume"] = lease_transcript(
                            read(output / "state" / str(serving.index(identity)) / "controlled-native-provider-owner-leases-resume.jsonl", maximum=4 << 20),
                            plan, records, capacity["sha256"], capacity_data)
                if identity == publisher and stopped:
                    records = publisher_records(commands.run(DOCKER + ["logs", identity])[0], plan, capacity["sha256"], joined=True,
                                                stop_sha256=owner_stop["sha256"] if owner_stop else None)
                    result["publisher_capacity"] = capacity_ref
                    result["publisher_observations"] = len(records)
            except BaseException as error:
                cleanup_error(identity, proof_stage, error)
        result["all_owned_containers_stopped"] = len(states) == len(identities) == len(names) and all(s["stopped"] for s in states)
        # Complete state evidence is diagnostic only. Any proof exception still
        # refuses acceptance and retains the original FD for Root resolution.
        result["cleanup_known"] = result["all_owned_containers_stopped"] and not cleanup_errors
        result["cleanup_errors"] = cleanup_errors
        if cleanup_errors:
            result["cleanup_failure_class"] = cleanup_errors[0]["exception_class"]
            result["status"] = "FAIL"
        try:
            require(source_snapshot(plan, window, cleanup=True) == source_before and input_snapshot(plan, dispatch_refs) == inputs_before, "immutable input/source drift")
            require(immutable is None or cohort_material_snapshot(output, renewing, owner_stop) == immutable, "immutable cohort/custody drift")
            result["immutable_after_verified"] = True
        except BaseException as error:
            result.update(status="FAIL", immutable_after_verified=False, final_fence_failure_class=type(error).__name__)
        for number, handler in previous.items():
            try:
                signal.signal(number, handler)
            except BaseException as error:
                result.update(status="FAIL", cleanup_known=False, signal_restoration_failure_class=type(error).__name__)
        if interrupted or window.left(cleanup=True) < 0 or not result["cleanup_known"] or any(not s["normal_exit"] for s in states):
            result["status"] = "FAIL"
        result.update(commands=commands.rows, attempted_names=names, containers=states, ready_at=instant(ready_at) if ready_at else None,
                      finished_at=instant(time.time()), provider_components_passed=0, full_provider_cells_passed=0, signup_passed=0)
        new_file(output / "lifecycle.json", result)
        if interrupted or window.left(cleanup=True) < 0:
            result["status"] = "FAIL"
            new_file(output / "NON_ACCEPTING_POST_PUBLICATION.json", {"status": "FAIL", "reason": "signal_or_deadline_after_publication"})
    return result


def retain_or_release(lock, result):
    if result is not None and result.get("cleanup_known") is True:
        fcntl.flock(lock, fcntl.LOCK_UN)
        os.close(lock)
        return
    try:
        print(json.dumps({"status": "CLEANUP_UNKNOWN", "holder_pid": os.getpid(), "shared_lock_retained": True}), flush=True)
    except BaseException:
        pass
    while True:
        time.sleep(1)  # Only Root's explicit owned-resource resolution ends this holder.


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--execute-reviewed-owner-window", action="store_true")
    for name in ("plan", "plan-sha256", "root-review", "root-review-sha256"):
        parser.add_argument("--" + name)
    args = parser.parse_args(argv)
    if not args.execute_reviewed_owner_window:
        print("PLAN_ONLY_NO_INPUT_NO_LOCK_NO_RUNTIME")
        return 0
    require(all((args.plan, args.plan_sha256, args.root_review, args.root_review_sha256)), "exact resolved review required")
    plan_ref, review_ref = {"path": args.plan, "sha256": args.plan_sha256}, {"path": args.root_review, "sha256": args.root_review_sha256}
    plan, approval = resolved(decode(reference(plan_ref))), decode(reference(review_ref))
    approved_dispatch(plan, args.plan_sha256, approval)
    lock = os.open("/private/tmp/lantern-heavy-local.lock", os.O_RDWR | os.O_NOFOLLOW)
    info = os.fstat(lock)
    require(stat.S_ISREG(info.st_mode) and info.st_uid == os.getuid() and info.st_nlink == 1 and stat.S_IMODE(info.st_mode) == 0o600, "owned original shared lock required")
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    result = None
    previous = {n: signal.getsignal(n) for n in (signal.SIGINT, signal.SIGTERM)}
    try:
        result = run(plan, args.plan_sha256, approval, args.root_review_sha256, (plan_ref, review_ref))
    finally:
        for number, handler in previous.items():
            try:
                signal.signal(number, handler)
            except BaseException as error:
                had_result = result is not None
                result = {**(result or {}), "status": "FAIL", "cleanup_known": False, "signal_restoration_failure_class": type(error).__name__}
                if had_result:
                    try:
                        new_file(canonical(plan["output"]) / "NON_ACCEPTING_POST_PUBLICATION.json",
                                 {"status": "FAIL", "reason": "outer_signal_restoration_failure"})
                    except BaseException:
                        pass  # The original FD is still retained below if publication fails.
        retain_or_release(lock, result)
    print(json.dumps({"status": result["status"], "cleanup_known": result["cleanup_known"], "provider_components_passed": 0}))
    return 0 if result["status"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
