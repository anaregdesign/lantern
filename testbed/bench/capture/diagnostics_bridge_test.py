import http.client
import io
import json
import subprocess
import threading
import unittest
from unittest import mock

from diagnostics_bridge import DiagnosticsHandler, DiagnosticsServer


class DiagnosticsBridgeTest(unittest.TestCase):
    def test_http_boundary_and_collection_failure(self):
        requested = []
        records = []

        def fetch(path):
            requested.append(path)
            if path == "/readyz":
                raise OSError("owned container unavailable")
            return b"go_goroutines 5\n"

        server = DiagnosticsServer(("127.0.0.1", 0), "a" * 64, fetch, log_sink=records.append)
        thread = threading.Thread(target=server.serve_forever)
        thread.start()
        try:
            for path, method, status in (("/metrics", "GET", 200),
                                         ("/readyz", "GET", 503),
                                         ("/debug/pprof/heap?gc=1", "GET", 404),
                                         ("/metrics?query=x", "GET", 404),
                                         ("/metrics", "POST", 405),
                                         ("/metrics", "PUT", 501)):
                with self.subTest(path=path, method=method):
                    client = http.client.HTTPConnection(*server.server_address, timeout=2)
                    client.request(method, path)
                    response = client.getresponse()
                    body = response.read()
                    self.assertEqual(response.status, status)
                    if status == 200:
                        self.assertEqual(body, b"go_goroutines 5\n")
                        self.assertEqual(response.headers["Cache-Control"], "no-store")
                    client.close()
            self.assertEqual(requested, ["/metrics", "/readyz"])
            ends = [r for r in self.records(records) if r["event"] == "request_end"]
            self.assertEqual([r["status"] for r in ends], [200, 503, 404, 404, 405, 501])
            self.assertEqual([r["reason"] for r in ends],
                             ["ok", "fetch_error", "path_not_allowed", "path_not_allowed", "method_not_allowed", "status"])
            self.assertNotIn("query=x", "".join(records))
        finally:
            server.shutdown()
            thread.join()
            server.server_close()

    def test_rejects_remote_listener_and_mutable_container_name(self):
        for address, container in (("0.0.0.0", "a" * 64), ("127.0.0.1", "lantern-0")):
            with self.assertRaises(ValueError):
                DiagnosticsServer((address, 0), container)

    def records(self, lines):
        for line in lines:
            self.assertTrue(line.endswith("\n"))
            self.assertLessEqual(len(line.encode("utf-8")), 1024)
        return [json.loads(line) for line in lines]

    def handler(self, fetch=None, log_sink=None, monotonic=None, path="/metrics"):
        server = DiagnosticsServer(("127.0.0.1", 0), "a" * 64, fetch,
                                   log_sink=log_sink, monotonic=monotonic)
        self.addCleanup(server.server_close)
        handler = object.__new__(DiagnosticsHandler)
        handler.server, handler.command, handler.path = server, "GET", path
        handler.wfile = io.BytesIO()
        handler.send_response = mock.Mock()
        handler.send_header = mock.Mock()
        handler.end_headers = mock.Mock()
        handler.send_error = mock.Mock()
        return handler

    def assert_slots_available(self, server):
        acquired = 0
        try:
            for _ in range(4):
                self.assertTrue(server.slots.acquire(blocking=False))
                acquired += 1
            self.assertFalse(server.slots.acquire(blocking=False))
        finally:
            for _ in range(acquired):
                server.slots.release()

    def test_docker_intervals_and_private_failures(self):
        private = b"private body bearer token http://secret.invalid/path"
        cases = [
            ("ok", subprocess.CompletedProcess(["private command"], 0, stdout=private), None, "ok", 200),
            ("timeout", None, subprocess.TimeoutExpired(["private command"], 5,
                                                       output=private, stderr=private), "timeout", 503),
            ("nonzero", None, subprocess.CalledProcessError(7, ["private command"],
                                                           output=private, stderr=private), "nonzero", 503),
            ("os error", None, OSError(private.decode()), "fetch_error", 503),
            ("maximum response", subprocess.CompletedProcess([], 0, stdout=b"x" * (2 * 1024 * 1024)),
             None, "ok", 200),
            ("response limit", subprocess.CompletedProcess([], 0, stdout=b"x" * (2 * 1024 * 1024 + 1)),
             None, "response_limit", 503),
        ]
        for name, result, error, reason, status in cases:
            with self.subTest(name=name):
                lines, clock = [], [10.0]
                handler = self.handler(log_sink=lines.append, monotonic=lambda: clock[0])

                def run(*_args, **_kwargs):
                    clock[0] += 2.75
                    if error is not None:
                        raise error
                    return result

                with mock.patch("diagnostics_bridge.subprocess.run", side_effect=run) as command:
                    handler.do_GET()
                command.assert_called_once_with(
                    ["docker", "exec", "a" * 64, "wget", "-qO-", "-T", "3", "http://127.0.0.1:9090/metrics"],
                    stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=5, check=True,
                )
                records = self.records(lines)
                self.assertEqual([r["event"] for r in records],
                                 ["request_start", "fetch_start", "docker_exec_start", "docker_exec_end",
                                  "fetch_end", "request_end"])
                self.assertEqual(records[-2]["reason"], reason)
                self.assertEqual(records[-1]["status"], status)
                self.assertEqual(records[-1]["reason"], reason)
                self.assertEqual(records[3]["duration_ms"], 2750)
                self.assertEqual(records[-1]["elapsed_ms"], 2750)
                self.assertEqual({r["request_seq"] for r in records}, {1})
                self.assertEqual({r["listener_port"] for r in records}, {handler.server.server_address[1]})
                self.assertTrue(records[0]["started_utc"].endswith("+00:00"))
                self.assertTrue(all(r["utc"].endswith("+00:00") for r in records))
                for secret in (private.decode(), "private command", "a" * 64, "http://"):
                    self.assertNotIn(secret, "".join(lines))
                if status == 200:
                    self.assertEqual(handler.wfile.getvalue(), result.stdout)
                else:
                    handler.send_error.assert_called_once_with(503)
                self.assert_slots_available(handler.server)

    def test_slow_fetch_client_disconnect_and_slot_release(self):
        for disconnect in (BrokenPipeError, ConnectionResetError):
            with self.subTest(disconnect=disconnect):
                lines, clock = [], [0.0]
                private = b"private response body"

                def fetch(_path):
                    clock[0] += 3
                    return private

                handler = self.handler(fetch, lines.append, lambda: clock[0])
                handler.wfile = mock.Mock()
                handler.wfile.write.side_effect = disconnect("private client error")
                handler.do_GET()
                records = self.records(lines)
                self.assertEqual(records[-2]["reason"], "ok")
                self.assertEqual(records[-2]["duration_ms"], 3000)
                self.assertEqual(records[-1]["delivery"], "broken_pipe")
                self.assertEqual(records[-1]["status"], 200)
                self.assertEqual(records[-1]["elapsed_ms"], 3000)
                self.assertNotIn("private", "".join(lines))
                self.assert_slots_available(handler.server)

    def test_rejected_requests_never_fetch_or_release_another_slot(self):
        lines = []
        fetch = mock.Mock(return_value=b"private body")
        handler = self.handler(fetch, lines.append)
        for _ in range(4):
            self.assertTrue(handler.server.slots.acquire(blocking=False))
        try:
            handler.do_GET()
            handler.send_error.assert_called_once_with(503)
            fetch.assert_not_called()
            self.assertFalse(handler.server.slots.acquire(blocking=False))
        finally:
            for _ in range(4):
                handler.server.slots.release()
        self.assertEqual(self.records(lines)[-1]["reason"], "slots_full")
        self.assert_slots_available(handler.server)
        handler.path = "/metrics?token=private-token"
        handler.send_error.reset_mock()
        handler.do_GET()
        handler.send_error.assert_called_once_with(404)
        fetch.assert_not_called()
        self.assertNotIn("private-token", "".join(lines))
        self.assertEqual(self.records(lines)[-1]["path"], "other")
        self.assertEqual(self.records(lines)[-1]["request_seq"], 2)

    def test_failed_log_sink_does_not_change_response_or_slot_release(self):
        def failed_sink(_line):
            raise OSError("private sink error")

        handler = self.handler(lambda _path: b"body", failed_sink)
        handler.do_GET()
        self.assertEqual(handler.wfile.getvalue(), b"body")
        self.assert_slots_available(handler.server)


if __name__ == "__main__":
    unittest.main()
