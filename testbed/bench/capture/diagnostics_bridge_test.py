import http.client
import threading
import unittest

from diagnostics_bridge import DiagnosticsServer


class DiagnosticsBridgeTest(unittest.TestCase):
    def test_http_boundary_and_collection_failure(self):
        requested = []

        def fetch(path):
            requested.append(path)
            if path == "/readyz":
                raise OSError("owned container unavailable")
            return b"go_goroutines 5\n"

        server = DiagnosticsServer(("127.0.0.1", 0), "a" * 64, fetch)
        thread = threading.Thread(target=server.serve_forever)
        thread.start()
        try:
            for path, method, status in (("/metrics", "GET", 200),
                                         ("/readyz", "GET", 503),
                                         ("/debug/pprof/heap?gc=1", "GET", 404),
                                         ("/metrics?query=x", "GET", 404),
                                         ("/metrics", "POST", 405)):
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
        finally:
            server.shutdown()
            thread.join()
            server.server_close()

    def test_rejects_remote_listener_and_mutable_container_name(self):
        for address, container in (("0.0.0.0", "a" * 64), ("127.0.0.1", "lantern-0")):
            with self.assertRaises(ValueError):
                DiagnosticsServer((address, 0), container)


if __name__ == "__main__":
    unittest.main()
