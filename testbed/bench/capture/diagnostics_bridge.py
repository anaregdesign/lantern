#!/usr/bin/env python3
"""Owned operator-local metrics bridge; never changes Server admission rules."""
import argparse
import http.server
import json
import re
import signal
import subprocess
import threading


class DiagnosticsServer(http.server.ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, address, container, fetch=None):
        if address[0] != "127.0.0.1" or not re.fullmatch(r"[0-9a-f]{64}", container):
            raise ValueError("fixed loopback listener and immutable container ID required")
        self.container = container
        self.slots = threading.BoundedSemaphore(4)
        self.fetch = fetch or self.read_container
        super().__init__(address, DiagnosticsHandler)

    def read_container(self, path):
        result = subprocess.run(
            ["docker", "exec", self.container, "wget", "-qO-", "-T", "3",
             "http://127.0.0.1:9090" + path],
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=5, check=True,
        )
        if len(result.stdout) > 2 * 1024 * 1024:
            raise ValueError("diagnostics response exceeds limit")
        return result.stdout


class DiagnosticsHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_POST(self):
        self.send_error(405)

    def do_GET(self):
        if self.path not in ("/metrics", "/readyz"):
            self.send_error(404)
            return
        if not self.server.slots.acquire(blocking=False):
            self.send_error(503)
            return
        try:
            body = self.server.fetch(self.path)
        except (OSError, ValueError, subprocess.SubprocessError):
            self.send_error(503)
        else:
            self.send_response(200)
            self.send_header("Content-Type", "text/plain; charset=utf-8")
            self.send_header("Cache-Control", "no-store")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        finally:
            self.server.slots.release()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--containers", required=True)
    parser.add_argument("--ports", required=True)
    parser.add_argument("--ready-file", required=True)
    args = parser.parse_args()
    containers, raw_ports = args.containers.split(","), args.ports.split(",")
    if len(containers) != 3 or len(raw_ports) != 3:
        parser.error("exactly three containers and ports required")
    ports = [int(value) for value in raw_ports]
    if len(set(ports)) != 3 or any(str(port) != raw or not 1 <= port <= 65535
                                          for port, raw in zip(ports, raw_ports)):
        parser.error("distinct canonical ports required")
    servers = []
    stopped = threading.Event()
    signal.signal(signal.SIGTERM, lambda *_: stopped.set())
    signal.signal(signal.SIGINT, lambda *_: stopped.set())
    try:
        for container, port in zip(containers, ports):
            server = DiagnosticsServer(("127.0.0.1", port), container)
            servers.append(server)
            threading.Thread(target=server.serve_forever, daemon=True).start()
        with open(args.ready_file, "x", encoding="utf-8") as output:
            json.dump({"transport": "operator_docker_exec", "ports": ports}, output)
        stopped.wait()
    finally:
        for server in servers:
            server.shutdown()
            server.server_close()


if __name__ == "__main__":
    main()
