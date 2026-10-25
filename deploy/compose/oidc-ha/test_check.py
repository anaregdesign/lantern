"""Network-free acceptance-driver contracts; no provider, Docker or NTP calls."""
from __future__ import annotations

from contextlib import redirect_stderr, redirect_stdout
import importlib.util
import http.client
import io
import json
import os
from pathlib import Path
import ssl
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock
from urllib import error


SPEC = importlib.util.spec_from_file_location("ha_check", Path(__file__).with_name("check.py"))
driver = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(driver)


class Response(io.BytesIO):
    def __init__(self, body, *, status=200, content_type="application/json"):
        super().__init__(body)
        self.status = status
        self.headers = {"Content-Type": content_type}


class ExampleHTTP:
    """Exact Connect/JSON-RPC fixture with retained data across driver phases."""

    def __init__(self):
        self.calls = []
        self.vertices = {}
        self.deadline = None
        self.fail_put = False
        self.allow_anonymous = False
        self.allow_forbidden = False
        self.track_error = "permission denied"
        self.task = None

    def call(self, url, *, payload=None, token=None, headers=None, rpc_id=None, notification=False):
        self.calls.append((url, payload, token, headers))
        if url.endswith(driver.CAPABILITIES):
            return 200, {}, self.raw({"mode": "AUTH_MODE_OIDC", "ready": True, "protocolVersion": 2})
        if url.endswith("/api/v1/targets"):
            return 200, {}, self.raw(targets())
        if url.endswith("/GetVertex"):
            if token is None and not self.allow_anonymous:
                return 401, {}, self.raw({"code": "unauthenticated"})
            vertex = self.vertices.get(payload["key"])
            return (200, {}, self.raw({"vertex": vertex})) if vertex else (404, {}, self.raw({"code": "not_found"}))
        if url.endswith("/PutVertex"):
            if self.fail_put:
                raise driver.CheckFailure("transport")
            vertex = payload["vertex"]
            if not vertex["key"].startswith("demo.") and not self.allow_forbidden:
                return 403, {}, self.raw({"code": "permission_denied"})
            self.vertices[vertex["key"]] = vertex
            return 200, {}, self.raw({"outcome": "PUT_OUTCOME_APPLIED_AND_LIVE"})
        if url.endswith("/mcp"):
            method = payload["method"]
            if notification:
                assert method == "notifications/initialized"
                return 202, {}, b""
            if method == "initialize":
                result = {"protocolVersion": driver.PROTOCOL}
            elif method == "tools/list":
                result = {"tools": [{"name": name} for name in ("announce", "list_agents", "track")]}
            else:
                name = payload["params"]["name"]
                if name == "announce":
                    self.task = payload["params"]["arguments"]["task"]
                    result = {"structuredContent": {"agent_id": "example-agent"}}
                elif name == "list_agents":
                    result = {"structuredContent": {"agents": [{"agent_id": "example-agent", "task": self.task}], "count": 1}}
                else:
                    assert name == "track"
                    assert payload["params"]["arguments"]["resources"][0].startswith("forbidden.")
                    result = {"isError": True, "content": [{"type": "text", "text": self.track_error}]}
            return 200, {"Mcp-Session-Id": "opaque-example-session"}, {"jsonrpc": "2.0", "id": rpc_id, "result": result}
        assert url.endswith("/")
        return 200, {"Content-Type": "text/html"}, b"<!doctype html><html><script src='/assets/app.js'></script></html>"

    @staticmethod
    def raw(body):
        return json.dumps(body).encode()


def targets():
    return {"status": "success", "data": {"activeTargets": [
        {"labels": {"job": "lantern_oidc_ha", "node": f"lantern-{i}"}, "health": "up", "lastError": ""}
        for i in range(3)]}}


class DriverTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name).resolve() / "private"
        self.directory.mkdir(mode=0o700)
        for name in driver.IMMUTABLE_FILES:
            path = self.directory / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("synthetic immutable fixture: " + name)
            path.chmod(0o600)
        (self.directory / "mcp.env").write_text("# Existing scoped API JWT, synthetic fixture only.\nLANTERN_TOKEN=header.payload.signature\n")
        (self.directory / "mcp.env").chmod(0o600)
        self.evidence = Path(self.temporary.name).resolve() / "evidence.json"
        self.args = SimpleNamespace(phase="before", private_directory=self.directory, evidence=self.evidence,
                                    edge_url="https://localhost:8443", mcp_url="http://localhost:6390/mcp",
                                    prometheus_url="http://localhost:9091")
        self.http = ExampleHTTP()

    def run_check(self):
        with mock.patch.object(driver, "HTTP", return_value=self.http):
            driver.check(self.args)

    def test_before_after_preserves_sentinel_and_checks_real_tool_contracts(self):
        self.run_check()
        document = driver.evidence_read(self.evidence)
        self.assertEqual(self.evidence.stat().st_mode & 0o777, 0o600)
        self.assertEqual(len(document["files"]), 24)
        self.assertNotIn("header.payload.signature", self.evidence.read_text())
        puts = [call for call in self.http.calls if call[0].endswith("/PutVertex")]
        self.assertEqual(len(puts), 2)
        self.assertTrue(puts[0][1]["ifAbsent"])
        self.assertNotIn("expiration", puts[0][1]["vertex"])
        mcp = [call for call in self.http.calls if call[0].endswith("/mcp")]
        self.assertEqual(mcp[0][1]["params"]["protocolVersion"], "2025-06-18")
        self.assertTrue(all(call[3]["Mcp-Session-Id"] == "opaque-example-session" for call in mcp[1:]))
        self.assertTrue(all(call[2] is None for call in mcp))
        original = self.evidence.read_bytes()
        self.args.phase = "after"
        self.run_check()
        self.assertEqual(original, self.evidence.read_bytes())
        self.assertEqual(len([call for call in self.http.calls if call[0].endswith("/PutVertex")]), 2)
        self.assertEqual(len(self.http.vertices), 1)

    def test_before_refuses_existing_evidence_before_network_calls(self):
        self.evidence.write_text("retain uncertain outcome")
        with self.assertRaises(FileExistsError):
            self.run_check()
        self.assertEqual(self.http.calls, [])

    def test_uncertain_write_is_never_retried_and_cannot_pass_after(self):
        self.http.fail_put = True
        with self.assertRaisesRegex(driver.CheckFailure, "^transport$"):
            self.run_check()
        self.assertEqual(len([call for call in self.http.calls if call[0].endswith("/PutVertex")]), 1)
        self.assertFalse(json.loads(self.evidence.read_text())["complete"])
        self.args.phase = "after"
        with self.assertRaisesRegex(driver.CheckFailure, "^evidence$"):
            self.run_check()

    def test_after_rejects_changed_identity_without_network_calls(self):
        self.run_check()
        self.http.calls.clear()
        (self.directory / driver.IMMUTABLE_FILES[-1]).write_text("changed immutable anchor")
        self.args.phase = "after"
        with self.assertRaisesRegex(driver.CheckFailure, "^immutable-input-changed$"):
            self.run_check()
        self.assertEqual(self.http.calls, [])

    def test_negative_authentication_is_required(self):
        self.http.allow_anonymous = True
        with self.assertRaisesRegex(driver.CheckFailure, "^anonymous-denial$"):
            self.run_check()
        self.assertEqual(len(self.http.vertices), 0)

    def test_forbidden_prefix_cannot_be_success_shaped(self):
        self.http.allow_forbidden = True
        with self.assertRaisesRegex(driver.CheckFailure, "^prefix-denial$"):
            self.run_check()
        self.assertEqual(len([call for call in self.http.calls if call[0].endswith("/PutVertex")]), 2)

    def test_mcp_transport_success_does_not_replace_permission_denial(self):
        self.http.track_error = "resource not found"
        with self.assertRaisesRegex(driver.CheckFailure, "^mcp-permission-denied$"):
            self.run_check()
        self.assertFalse(json.loads(self.evidence.read_text())["complete"])

    def test_symlink_or_public_evidence_is_refused(self):
        self.run_check()
        self.evidence.chmod(0o644)
        with self.assertRaisesRegex(driver.CheckFailure, "^private-input$"):
            driver.evidence_read(self.evidence)
        self.evidence.chmod(0o600)
        link = self.evidence.with_name("link.json")
        link.symlink_to(self.evidence)
        with self.assertRaisesRegex(driver.CheckFailure, "^private-input$"):
            driver.evidence_read(link)

    def test_token_is_exact_private_env_value(self):
        path = self.directory / "mcp.env"
        for value in ("LANTERN_TOKEN=REPLACE\n", "LANTERN_TOKEN=a.b.c\nLANTERN_TOKEN=d.e.f\n",
                      "LANTERN_TOKEN=a.b.c\nUNEXPECTED=1\n", "LANTERN_TOKEN='a.b.c'\n"):
            path.write_text(value)
            with self.assertRaisesRegex(driver.CheckFailure, "^private-credential$"):
                driver.read_token(self.directory)
        path.write_text("LANTERN_TOKEN=a.b.c\n")
        path.chmod(0o644)
        with self.assertRaisesRegex(driver.CheckFailure, "^private-input$"):
            driver.read_token(self.directory)

    def test_cli_failure_does_not_disclose_paths_credentials_or_bodies(self):
        self.http.fail_put = True
        stdout, stderr = io.StringIO(), io.StringIO()
        with mock.patch.object(driver, "HTTP", return_value=self.http), redirect_stdout(stdout), redirect_stderr(stderr):
            result = driver.main(["--phase", "before", "--private-directory", str(self.directory),
                                  "--evidence", str(self.evidence)])
        self.assertEqual(result, 1)
        self.assertEqual(stdout.getvalue(), "")
        self.assertEqual(stderr.getvalue(), "example check failed: transport\n")


class WireTests(unittest.TestCase):
    def test_json_and_bounded_sse_match_response_id(self):
        payload = {"jsonrpc": "2.0", "id": 7, "result": {"protocolVersion": driver.PROTOCOL}}
        self.assertEqual(driver.read_jsonrpc(Response(json.dumps(payload).encode()), 7), payload)
        stream = b": keepalive\r\n\r\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notice\"}\r\n\r\n"
        stream += b"event: message\r\ndata: " + json.dumps(payload).encode() + b"\r\n\r\n"
        self.assertEqual(driver.read_jsonrpc(Response(stream, content_type="text/event-stream; charset=utf-8"), 7), payload)

    def test_bad_or_unbounded_sse_is_refused(self):
        for content_type, body, category in (
            ("text/plain", b"not json", "mcp-wire"),
            ("text/event-stream", b"data: {}\n\n", "mcp-wire"),
            ("text/event-stream", b"data: " + b"x" * 65536 + b"\n\n", "response-size"),
            ("text/event-stream", b"data: invalid\n\n", "response-format"),
        ):
            with self.subTest(category=category), self.assertRaisesRegex(driver.CheckFailure, "^" + category + "$"):
                driver.read_jsonrpc(Response(body, content_type=content_type), 1)

    def test_prometheus_requires_exact_three_distinct_healthy_nodes(self):
        fixture = targets()
        http = mock.Mock()
        http.call.return_value = (200, {}, json.dumps(fixture).encode())
        self.assertTrue(driver.prometheus_ready(http, "http://localhost:9091"))
        fixture["data"]["activeTargets"][0]["health"] = "down"
        http.call.return_value = (200, {}, json.dumps(fixture).encode())
        self.assertFalse(driver.prometheus_ready(http, "http://localhost:9091"))
        fixture["data"]["activeTargets"].append(fixture["data"]["activeTargets"][1])
        http.call.return_value = (200, {}, json.dumps(fixture).encode())
        with self.assertRaisesRegex(driver.CheckFailure, "^prometheus-targets$"):
            driver.prometheus_ready(http, "http://localhost:9091")

    def test_remote_plaintext_userinfo_and_redirect_targets_are_not_allowed(self):
        for value in ("http://localhost:8443", "https://remote.example", "https://user:secret@localhost",
                      "https://localhost/#secret", "https://localhost/?token=secret", "https://localhost:0"):
            with self.subTest(value=value), self.assertRaises(driver.CheckFailure):
                driver.endpoint(value, tls=True)
        self.assertEqual(driver.endpoint("https://localhost:8443", tls=True), "https://localhost:8443")
        self.assertIsNone(driver.NoRedirect().redirect_request(None, None, 302, "", {}, "https://other.example"))

    def test_tls_uses_only_explicit_ca_and_cannot_fallback_after_wrong_ca(self):
        context, opener = mock.Mock(), mock.Mock()
        # Simulate an endpoint whose chain is rejected by the supplied root.
        opener.open.side_effect = error.URLError(ssl.SSLCertVerificationError("private endpoint/path rejected by wrong CA"))
        with mock.patch.object(driver, "private_read", return_value=b"synthetic public CA"), \
                mock.patch.object(driver.ssl, "SSLContext", return_value=context) as constructor, \
                mock.patch.object(driver.request, "build_opener", return_value=opener) as build:
            http = driver.HTTP(Path("/synthetic/ca.pem"))
            constructor.assert_called_once_with(ssl.PROTOCOL_TLS_CLIENT)
            self.assertEqual(context.minimum_version, ssl.TLSVersion.TLSv1_3)
            context.load_verify_locations.assert_called_once_with(cadata="synthetic public CA")
            self.assertEqual(build.call_args.args[0].proxies, {})
            with self.assertRaisesRegex(driver.CheckFailure, "^transport$"):
                http.call("https://localhost:8443/", token="secret-token")
            self.assertEqual(opener.open.call_count, 1)

    def test_request_budget_and_body_limit_are_enforced_without_retries(self):
        http = driver.HTTP.__new__(driver.HTTP)
        http.deadline = 11.25
        http.opener = mock.Mock()
        http.opener.open.return_value = Response(b"x" * 11)
        with mock.patch.object(driver.time, "monotonic", return_value=10), mock.patch.object(driver, "MAX_RESPONSE", 10), \
                mock.patch.object(driver, "Deadline") as deadline:
            with self.assertRaisesRegex(driver.CheckFailure, "^response-size$"):
                http.call("https://localhost:8443/")
            deadline.assert_called_once_with(1.25)
            self.assertEqual(http.opener.open.call_args.kwargs["timeout"], 1.25)
        self.assertEqual(http.opener.open.call_count, 1)

    def test_truncated_http_body_is_a_fixed_transport_failure(self):
        client = driver.HTTP.__new__(driver.HTTP)
        client.deadline = None
        client.opener = mock.Mock()
        response = mock.MagicMock()
        response.__enter__.return_value = response
        response.status = 200
        response.read.side_effect = http.client.IncompleteRead(b"secret-response")
        client.opener.open.return_value = response
        with self.assertRaisesRegex(driver.CheckFailure, "^transport$"):
            client.call("https://localhost:8443/")
        self.assertEqual(client.opener.open.call_count, 1)

    def test_malformed_prometheus_labels_are_a_fixed_failure(self):
        fixture = targets()
        fixture["data"]["activeTargets"][0]["labels"]["node"] = {"unexpected": "private-content"}
        client = mock.Mock()
        client.call.return_value = (200, {}, json.dumps(fixture).encode())
        with self.assertRaisesRegex(driver.CheckFailure, "^prometheus-targets$"):
            driver.prometheus_ready(client, "http://localhost:9091")

    def test_readiness_expiry_does_not_trigger_another_probe(self):
        probe = mock.Mock(return_value=False)
        with mock.patch.object(driver.time, "monotonic", side_effect=[0, 90]), mock.patch.object(driver.time, "sleep"):
            with self.assertRaisesRegex(driver.CheckFailure, "^readiness$"):
                driver.eventually(probe, 90, "readiness")
        probe.assert_called_once_with()


if __name__ == "__main__":
    unittest.main()
