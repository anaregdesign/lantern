#!/usr/bin/env python3
"""Bounded example checks, before and after an operator-managed CLEAN restart.

This driver never starts services or changes native state. Its synthetic graph
and MCP operations qualify only this local example, not a real OIDC provider or
device. Keep the private evidence when an operation has an uncertain outcome;
never rerun the before phase with a new evidence file to conceal a failure.
"""
from __future__ import annotations

import argparse
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import secrets
import signal
import ssl
import stat
import sys
import time
from urllib import error, parse, request


REQUEST_SECONDS = 5
READINESS_SECONDS = 90
MAX_RESPONSE = 4 * 1024 * 1024
MAX_PRIVATE_FILE = 4 * 1024 * 1024
PROTOCOL = "2025-06-18"
GRAPH = "/graph.v1.LanternService/"
CAPABILITIES = "/graph.v1.LanternSecurityService/GetAuthCapabilities"
IMMUTABLE_FILES = tuple(
    [f"config/lantern-{i}/{name}" for i in range(3) for name in ("startup.json", "receipt.env")]
    + [f"material/identity/nodes/node-{i}/{name}" for i in range(1, 4)
       for name in ("identity.json", "COMPLETE", "workload.pem", "workload-key.pem", "roots.pem", "data-operator.pem")]
)


class CheckFailure(Exception):
    """Only fixed, content-free categories may cross the CLI boundary."""


def require(condition: bool, category: str) -> None:
    if not condition:
        raise CheckFailure(category)


def clean_path(path: Path) -> Path:
    require(path.is_absolute() and path == path.resolve(strict=False), "private-input")
    return path


def private_read(path: Path, *, owner_only: bool = False) -> bytes:
    clean_path(path)
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(descriptor, "rb") as source:
        info = os.fstat(source.fileno())
        require(stat.S_ISREG(info.st_mode) and info.st_uid == os.getuid(), "private-input")
        require(not owner_only or not info.st_mode & 0o077, "private-input")
        require(0 < info.st_size <= MAX_PRIVATE_FILE, "private-input")
        data = source.read(MAX_PRIVATE_FILE + 1)
    require(len(data) <= MAX_PRIVATE_FILE, "private-input")
    return data


def file_hashes(directory: Path) -> dict[str, str]:
    return {name: hashlib.sha256(private_read(directory / name)).hexdigest() for name in IMMUTABLE_FILES}


def read_token(directory: Path) -> str:
    lines = private_read(directory / "mcp.env", owner_only=True).decode("utf-8").splitlines()
    entries = [line for line in lines if line and not line.startswith("#")]
    require(len(entries) == 1 and entries[0].startswith("LANTERN_TOKEN="), "private-credential")
    token = entries[0][len("LANTERN_TOKEN="):]
    require(re.fullmatch(r"[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+", token) is not None,
            "private-credential")
    return token


def endpoint(value: str, *, tls: bool, path: str = "") -> str:
    parsed = parse.urlsplit(value)
    require(parsed.scheme in (("https",) if tls else ("http", "https")), "endpoint")
    require(parsed.hostname in ("localhost", "127.0.0.1", "::1") and parsed.username is None
            and parsed.password is None and not parsed.query and not parsed.fragment
            and parsed.path in ((path,) if path else ("", "/")), "endpoint")
    try:
        port = parsed.port
    except ValueError:
        raise CheckFailure("endpoint") from None
    require(port is None or 1 <= port <= 65535, "endpoint")
    return value.rstrip("/")


class NoRedirect(request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Deadline:
    """Bound the entire local HTTP exchange, including a trickling SSE body."""

    def __init__(self, seconds: float = REQUEST_SECONDS):
        self.seconds = seconds

    def __enter__(self):
        self.previous = signal.signal(signal.SIGALRM, self.expired)
        signal.setitimer(signal.ITIMER_REAL, self.seconds)

    @staticmethod
    def expired(signum, frame):
        raise CheckFailure("request-timeout")

    def __exit__(self, kind, value, trace):
        signal.setitimer(signal.ITIMER_REAL, 0)
        signal.signal(signal.SIGALRM, self.previous)


def json_value(data: bytes):
    try:
        return json.loads(data)
    except (UnicodeError, ValueError, RecursionError):
        raise CheckFailure("response-format") from None


def read_jsonrpc(response, expected_id: int):
    content_type = response.headers.get("Content-Type", "").split(";", 1)[0].strip().lower()
    if content_type == "application/json":
        raw = response.read(MAX_RESPONSE + 1)
        require(len(raw) <= MAX_RESPONSE, "response-size")
        payload = json_value(raw)
        require(isinstance(payload, dict) and payload.get("id") == expected_id, "mcp-wire")
        return payload
    require(content_type == "text/event-stream", "mcp-wire")
    total, event, messages = 0, [], 0
    while True:
        line = response.readline(65537)
        total += len(line)
        require(len(line) <= 65536 and total <= MAX_RESPONSE, "response-size")
        require(bool(line), "mcp-wire")
        line = line.rstrip(b"\r\n")
        if line.startswith(b"data:"):
            event.append(line[5:].lstrip(b" "))
        elif not line and event:
            messages += 1
            require(messages <= 32, "mcp-wire")
            payload = json_value(b"\n".join(event))
            event = []
            require(isinstance(payload, dict), "mcp-wire")
            if payload.get("id") == expected_id:
                return payload


class HTTP:
    def __init__(self, ca_file: Path):
        # Load exactly the supplied public trust; never environment CA/proxy or
        # unverified TLS. The same edge CA may verify optional HTTPS loopback UIs.
        ca = private_read(ca_file).decode("ascii")
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
        context.minimum_version = ssl.TLSVersion.TLSv1_3
        context.load_verify_locations(cadata=ca)
        self.opener = request.build_opener(request.ProxyHandler({}), NoRedirect(),
                                          request.HTTPSHandler(context=context))
        self.deadline = None

    def call(self, url: str, *, payload=None, token: str | None = None,
             headers: dict[str, str] | None = None, rpc_id: int | None = None,
             notification: bool = False):
        wire_headers = {"Accept": "application/json"}
        wire_headers.update(headers or {})
        data = None
        if payload is not None:
            data = json.dumps(payload, separators=(",", ":")).encode("utf-8")
            wire_headers["Content-Type"] = "application/json"
        if token is not None:
            wire_headers["Authorization"] = "Bearer " + token
        req = request.Request(url, data=data, headers=wire_headers, method="POST" if data is not None else "GET")
        try:
            seconds = REQUEST_SECONDS
            if self.deadline is not None:
                seconds = min(seconds, self.deadline - time.monotonic())
                require(seconds > 0, "request-timeout")
            with Deadline(seconds):
                try:
                    response = self.opener.open(req, timeout=seconds)
                except error.HTTPError as rejected:
                    response = rejected
                with response:
                    status = response.status
                    response_headers = response.headers
                    if rpc_id is not None and status == 200:
                        body = read_jsonrpc(response, rpc_id)
                    else:
                        body = response.read(MAX_RESPONSE + 1)
                        require(len(body) <= MAX_RESPONSE, "response-size")
                    if notification:
                        require(status in (200, 202, 204) and not body, "mcp-wire")
                    return status, response_headers, body
        except CheckFailure:
            raise
        except (OSError, ValueError, error.URLError, ssl.SSLError, http.client.HTTPException):
            raise CheckFailure("transport") from None


def connect(http: HTTP, edge: str, method: str, payload: dict, token: str | None = None):
    status, _, raw = http.call(edge + method, payload=payload, token=token,
                               headers={"Connect-Protocol-Version": "1"})
    body = json_value(raw)
    require(isinstance(body, dict), "response-format")
    return status, body


def eventually(check, deadline: float, category: str) -> None:
    while True:
        require(time.monotonic() < deadline, category)
        try:
            if check():
                return
        except CheckFailure as failure:
            if str(failure) not in ("transport", "request-timeout"):
                raise
        remaining = deadline - time.monotonic()
        require(remaining > 0, category)
        time.sleep(min(2, remaining))


def auth_ready(http: HTTP, edge: str) -> bool:
    status, body = connect(http, edge, CAPABILITIES, {})
    if status in (429, 502, 503, 504):
        return False
    require(status == 200 and body.get("mode") == "AUTH_MODE_OIDC"
            and body.get("protocolVersion") == 2, "auth-capabilities")
    return body.get("ready") is True


def prometheus_ready(http: HTTP, url: str) -> bool:
    status, _, raw = http.call(url + "/api/v1/targets")
    if status in (429, 502, 503, 504):
        return False
    body = json_value(raw)
    require(status == 200 and isinstance(body, dict) and body.get("status") == "success", "prometheus")
    data = body.get("data")
    require(isinstance(data, dict) and isinstance(data.get("activeTargets"), list), "prometheus")
    targets = [target for target in data["activeTargets"]
               if isinstance(target, dict) and isinstance(target.get("labels"), dict)
               and target["labels"].get("job") == "lantern_oidc_ha"]
    require(all(isinstance(target["labels"].get("node"), str) for target in targets), "prometheus-targets")
    require(len(targets) == 3 and {target["labels"].get("node") for target in targets}
            == {"lantern-0", "lantern-1", "lantern-2"}, "prometheus-targets")
    return all(target.get("health") == "up" and not target.get("lastError") for target in targets)


class MCP:
    def __init__(self, http: HTTP, url: str):
        self.http, self.url, self.session, self.sequence = http, url, None, 0

    def call(self, method: str, params: dict, *, notification: bool = False):
        payload = {"jsonrpc": "2.0", "method": method, "params": params}
        headers = {"Accept": "application/json, text/event-stream", "MCP-Protocol-Version": PROTOCOL}
        if self.session is not None:
            headers["Mcp-Session-Id"] = self.session
        if not notification:
            self.sequence += 1
            payload["id"] = self.sequence
        status, response_headers, body = self.http.call(
            self.url, payload=payload, headers=headers,
            rpc_id=None if notification else self.sequence, notification=notification)
        if notification:
            return None
        require(status == 200 and isinstance(body, dict) and body.get("jsonrpc") == "2.0"
                and body.get("id") == self.sequence and "error" not in body
                and isinstance(body.get("result"), dict), "mcp-wire")
        if method == "initialize":
            session = response_headers.get("Mcp-Session-Id")
            require(isinstance(session, str) and 0 < len(session) <= 256
                    and re.fullmatch(r"[\x21-\x7e]+", session) is not None, "mcp-session")
            self.session = session
        return body["result"]

    def check(self, marker: str) -> None:
        initialized = self.call("initialize", {"protocolVersion": PROTOCOL, "capabilities": {},
                                               "clientInfo": {"name": "lantern-ha-example-check", "version": "1"}})
        require(initialized.get("protocolVersion") == PROTOCOL, "mcp-protocol")
        self.call("notifications/initialized", {}, notification=True)
        tools = self.call("tools/list", {})
        require(isinstance(tools.get("tools"), list) and {"announce", "list_agents", "track"}
                <= {tool.get("name") for tool in tools["tools"] if isinstance(tool, dict)
                    and isinstance(tool.get("name"), str)}, "mcp-tools")
        announced = self.call("tools/call", {"name": "announce", "arguments": {"task": marker}})
        require(not announced.get("isError") and isinstance(announced.get("structuredContent"), dict), "mcp-announce")
        agent_id = announced["structuredContent"].get("agent_id")
        require(isinstance(agent_id, str) and bool(agent_id), "mcp-announce")
        def listed() -> bool:
            result = self.call("tools/call", {"name": "list_agents", "arguments": {}})
            content = result.get("structuredContent")
            require(not result.get("isError") and isinstance(content, dict)
                    and isinstance(content.get("agents"), list), "mcp-list-agents")
            return any(isinstance(agent, dict) and agent.get("agent_id") == agent_id
                       and agent.get("task") == marker for agent in content["agents"])

        self.http.deadline = time.monotonic() + 15
        try:
            eventually(listed, self.http.deadline, "mcp-list-agents")
        finally:
            self.http.deadline = None
        denied = self.call("tools/call", {"name": "track", "arguments": {"resources": ["forbidden." + secrets.token_hex(16)]}})
        require(isinstance(denied.get("content"), list), "mcp-permission-denied")
        text = " ".join(item["text"] for item in denied["content"] if isinstance(item, dict)
                        and isinstance(item.get("text"), str))
        require(denied.get("isError") is True and "permission denied" in text.lower(), "mcp-permission-denied")


def evidence_read(path: Path) -> dict:
    document = json_value(private_read(path, owner_only=True))
    require(isinstance(document, dict) and set(document) == {"version", "complete", "key", "value", "files"}
            and document.get("version") == 1 and document.get("complete") is True
            and isinstance(document.get("key"), str)
            and re.fullmatch(r"demo\.ha_acceptance\.[a-f0-9]{32}", document["key"]) is not None
            and isinstance(document.get("value"), str)
            and re.fullmatch(r"ha-example-[a-f0-9]{32}", document["value"]) is not None
            and isinstance(document.get("files"), dict)
            and set(document["files"]) == set(IMMUTABLE_FILES)
            and all(isinstance(value, str) and re.fullmatch(r"[a-f0-9]{64}", value) is not None
                    for value in document["files"].values()), "evidence")
    return document


def write_evidence(output, document: dict) -> None:
    output.seek(0)
    output.write(json.dumps(document, sort_keys=True, indent=2) + "\n")
    output.truncate()
    output.flush()
    os.fsync(output.fileno())


def check(args) -> None:
    directory, evidence = clean_path(args.private_directory), clean_path(args.evidence)
    info = directory.stat()
    require(stat.S_ISDIR(info.st_mode) and info.st_uid == os.getuid() and not info.st_mode & 0o077, "private-input")
    edge = endpoint(args.edge_url, tls=True)
    mcp = endpoint(args.mcp_url, tls=False, path="/mcp")
    prometheus = endpoint(args.prometheus_url, tls=False)
    hashes, token = file_hashes(directory), read_token(directory)
    http = HTTP(directory / "tls/trust/ca.pem")
    output = None
    try:
        if args.phase == "before":
            # Reserve before any network mutation. An incomplete file preserves
            # uncertain outcomes and cannot be mistaken for a successful before.
            descriptor = os.open(evidence, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
            output = os.fdopen(descriptor, "w", encoding="utf-8")
            document = {"version": 1, "complete": False, "key": "demo.ha_acceptance." + secrets.token_hex(16),
                        "value": "ha-example-" + secrets.token_hex(16), "files": hashes}
            write_evidence(output, document)
        else:
            document = evidence_read(evidence)
            require(document["files"] == hashes, "immutable-input-changed")
        deadline = time.monotonic() + READINESS_SECONDS
        http.deadline = deadline
        eventually(lambda: auth_ready(http, edge), deadline, "readiness")
        eventually(lambda: prometheus_ready(http, prometheus), deadline, "prometheus-unready")
        http.deadline = None
        status, headers, html = http.call(edge + "/")
        require(status == 200 and "text/html" in headers.get("Content-Type", "").lower()
                and b"<html" in html.lower() and b"<script" in html.lower(), "admin")
        status, denied = connect(http, edge, GRAPH + "GetVertex", {"key": document["key"]})
        require(status == 401 and denied.get("code") == "unauthenticated", "anonymous-denial")
        if args.phase == "before":
            # The only permitted persistent write is attempted exactly once.
            status, put = connect(http, edge, GRAPH + "PutVertex",
                                  {"vertex": {"key": document["key"], "string": document["value"]}, "ifAbsent": True}, token)
            require(status == 200 and put.get("outcome") == "PUT_OUTCOME_APPLIED_AND_LIVE", "sentinel-put")
            status, denied = connect(http, edge, GRAPH + "PutVertex",
                                     {"vertex": {"key": "forbidden." + secrets.token_hex(16), "string": "ha-example-denied"}}, token)
            require(status == 403 and denied.get("code") == "permission_denied", "prefix-denial")

        def retained() -> bool:
            status, result = connect(http, edge, GRAPH + "GetVertex", {"key": document["key"]}, token)
            if status in (404, 429, 502, 503, 504):
                return False
            require(status == 200 and isinstance(result.get("vertex"), dict), "sentinel-get")
            require(result["vertex"].get("key") == document["key"]
                    and result["vertex"].get("string") == document["value"]
                    and not result["vertex"].get("expiration"), "sentinel-retained")
            return True

        http.deadline = time.monotonic() + 15
        eventually(retained, http.deadline, "sentinel-unavailable")
        http.deadline = None
        MCP(http, mcp).check("HA example bounded acceptance")
        require(file_hashes(directory) == hashes, "immutable-input-changed")
        if output is not None:
            document["complete"] = True
            write_evidence(output, document)
    finally:
        if output is not None:
            output.close()


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--phase", choices=("before", "after"), required=True)
    parser.add_argument("--private-directory", type=Path, required=True)
    parser.add_argument("--evidence", type=Path, required=True)
    parser.add_argument("--edge-url", default="https://localhost:8443")
    parser.add_argument("--mcp-url", default="http://localhost:6390/mcp")
    parser.add_argument("--prometheus-url", default="http://localhost:9091")
    args = parser.parse_args(argv)
    try:
        check(args)
    except CheckFailure as failure:
        print("example check failed: " + str(failure), file=sys.stderr)
        return 1
    except (OSError, ValueError, UnicodeError, RecursionError):
        print("example check failed: private-input", file=sys.stderr)
        return 1
    print("example check passed: " + args.phase + " (local synthetic operations only)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
