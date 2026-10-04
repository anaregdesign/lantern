"""Production Caddy route conformance; no IdP/device qualification claim."""
import http.server
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / 'docker-entrypoint.sh'
IMAGE = 'caddy:2.10-alpine'


class EntrypointTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix='lantern-caddy-routes-')
        self.addCleanup(self.directory.cleanup)
        self.conf = Path(self.directory.name) / 'conf'
        self.env = {key: value for key, value in os.environ.items()
                    if not key.startswith('LANTERN_ADMIN_')}
        self.env['LANTERN_ADMIN_CONFIG_DIR'] = str(self.conf)

    def generate(self, **settings):
        return subprocess.run(['sh', str(SCRIPT), 'true'], env=self.env | settings,
                              capture_output=True, text=True)

    def test_missing_upstream_rejects_api_and_diagnostics(self):
        self.conf.mkdir()
        (self.conf / 'prom.caddy').write_text('reverse_proxy attacker.example:80')
        result = self.generate()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((self.conf / 'prom.caddy').exists())
        text = (self.conf / 'routes.caddy').read_text()
        self.assertIn('/auth/* /browser/* /graph.v1.*/*', text)
        self.assertIn('503', text)
        self.assertNotIn('reverse_proxy', text)

    def test_rejects_injected_or_partial_proxy_configuration(self):
        cases = [
            {'LANTERN_ADMIN_PROMETHEUS_UPSTREAM': 'http://metrics:9090'},
            {'LANTERN_ADMIN_SERVER_UPSTREAM': 'http://user:secret@server:6380'},
            {'LANTERN_ADMIN_SERVER_UPSTREAM': 'http://server:6380/path'},
            {'LANTERN_ADMIN_SERVER_UPSTREAM': 'http://server:6380\nrespond 200'},
            {'LANTERN_ADMIN_SERVER_UPSTREAM': 'http://{host}:6380'},
            {'LANTERN_ADMIN_SERVER_UPSTREAM': 'http://server:65536'},
            {'LANTERN_ADMIN_SERVER_UPSTREAM': 'http://server:6380',
             'LANTERN_ADMIN_SERVER_CA_FILE': '/missing/ca.pem'},
        ]
        for setting in cases:
            with self.subTest(setting=list(setting)):
                self.assertNotEqual(self.generate(**setting).returncode, 0)

    def test_real_caddy_route_and_operations_admission(self):
        if not shutil.which('docker'):
            self.skipTest('Docker is required for the Caddy route gate')
        check = subprocess.run(['docker', 'image', 'inspect', IMAGE], capture_output=True)
        if check.returncode:
            self.skipTest('the pinned Caddy image is required')
        observed = []

        class Backend(http.server.BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                observed.append((self.path, dict(self.headers)))
                if self.path == '/auth/operations':
                    self.send_response(204 if self.headers.get('Cookie') == 'session=approved'
                                       else 403)
                    self.end_headers()
                elif self.path == '/api/v1/query?query=up':
                    self.send_response(200)
                    self.end_headers()
                    self.wfile.write(b'{"status":"success"}')
                else:
                    self.send_response(404)
                    self.end_headers()
                    self.wfile.write(b'backend API rejection')

        backend = http.server.ThreadingHTTPServer(('0.0.0.0', 0), Backend)
        self.addCleanup(backend.server_close)
        threading.Thread(target=backend.serve_forever, daemon=True).start()
        self.addCleanup(backend.shutdown)
        upstream = f'http://host.docker.internal:{backend.server_port}'
        generated = self.generate(LANTERN_ADMIN_SERVER_UPSTREAM=upstream,
                                  LANTERN_ADMIN_PROMETHEUS_UPSTREAM=upstream)
        self.assertEqual(generated.returncode, 0, generated.stderr)
        assets = Path(self.directory.name) / 'assets'
        assets.mkdir()
        (assets / 'index.html').write_text('SPA shell')
        with socket.socket() as reservation:
            reservation.bind(('127.0.0.1', 0))
            port = reservation.getsockname()[1]
        command = ['docker', 'run', '--rm', '--detach', '--add-host',
                   'host.docker.internal:host-gateway', '-p', f'127.0.0.1:{port}:8080',
                   '-v', f'{self.conf}:/etc/caddy/conf.d:ro',
                   '-v', f'{ROOT / "Caddyfile"}:/etc/caddy/Caddyfile:ro',
                   '-v', f'{assets}:/srv:ro', IMAGE, 'caddy', 'run', '--config',
                   '/etc/caddy/Caddyfile', '--adapter', 'caddyfile']
        container = subprocess.check_output(command, text=True).strip()
        self.addCleanup(lambda: subprocess.run(['docker', 'stop', container],
                                               capture_output=True, timeout=20))

        def get(path, headers=None, method='GET'):
            request = urllib.request.Request(f'http://127.0.0.1:{port}{path}',
                                             headers=headers or {}, method=method)
            try:
                response = urllib.request.urlopen(request, timeout=2)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                return response.status, response.read()

        for attempt in range(50):
            try:
                if get('/healthz')[0] == 200:
                    break
            except (OSError, urllib.error.URLError):
                pass
            time.sleep(.1)
        else:
            self.fail('Caddy readiness timed out')
        self.assertEqual(get('/cli'), (200, b'SPA shell'))
        self.assertEqual(get('/auth/session'), (404, b'backend API rejection'))
        self.assertEqual(get('/browser/graph.v1.LanternService/GetVertex'),
                         (404, b'backend API rejection'))
        self.assertEqual(get('/graph.v1.LanternService/GetVertex'),
                         (404, b'backend API rejection'))
        path = '/api/prom/api/v1/query?query=up'
        self.assertEqual(get(path)[0], 403)
        self.assertFalse(any(item[0].startswith('/api/v1/query') for item in observed))
        self.assertEqual(get(path, {'Cookie': 'session=approved',
                                   'Authorization': 'Bearer ignored'}),
                         (200, b'{"status":"success"}'))
        metric_headers = next(headers for path, headers in observed
                              if path.startswith('/api/v1/query'))
        self.assertNotIn('Cookie', metric_headers)
        self.assertNotIn('Authorization', metric_headers)
        auth_headers = [headers for path, headers in observed if path == '/auth/operations']
        self.assertTrue(all('Authorization' not in headers for headers in auth_headers))
        self.assertEqual(get(path, {'Cookie': 'session=approved'}, 'POST')[0], 405)


if __name__ == '__main__':
    unittest.main()
