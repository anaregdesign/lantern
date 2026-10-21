#!/usr/bin/env python3
"""Owned operator-local metrics bridge; never changes Server admission rules."""
import argparse
import datetime
import http.server
import json
import re
import signal
import subprocess
import sys
import threading
import time


_LOG_LOCK = threading.Lock()


class _ResponseTooLarge(ValueError):
    pass


class DiagnosticsServer(http.server.ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, address, container, fetch=None, log_sink=None, monotonic=None):
        if address[0] != "127.0.0.1" or not re.fullmatch(r"[0-9a-f]{64}", container):
            raise ValueError("fixed loopback listener and immutable container ID required")
        self.container = container
        self.slots = threading.BoundedSemaphore(4)
        self.fetch = fetch or self.read_container
        self.log_sink = log_sink if log_sink is not None else sys.stderr.write
        self.monotonic = monotonic or time.monotonic
        self.trace_local = threading.local()
        self.sequence_lock = threading.Lock()
        self.request_seq = 0
        super().__init__(address, DiagnosticsHandler)

    def begin_trace(self, method, path):
        with self.sequence_lock:
            self.request_seq += 1
            sequence = self.request_seq
        trace = {
            "request_seq": sequence,
            "listener_port": self.server_address[1],
            "method": method if method in ("GET", "POST") else "other",
            "path": {"/metrics": "metrics", "/readyz": "readyz"}.get(path, "other"),
            "started_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="microseconds"),
            "started_monotonic": self.monotonic(),
        }
        self.trace_local.trace = trace
        self.emit(trace, "request_start")
        return trace

    def emit(self, trace, event, **fields):
        if trace is None:
            return
        # Fixed labels/numbers only: never include a request target, command,
        # container ID, body, or exception text. No request history is retained.
        try:
            record = {key: value for key, value in trace.items() if key != "started_monotonic"}
            record.update(event=event,
                          utc=datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="microseconds"),
                          elapsed_ms=self.elapsed(trace["started_monotonic"]), **fields)
            line = json.dumps(record, separators=(",", ":"), allow_nan=False) + "\n"
            if len(line.encode("utf-8")) > 1024:
                return
            # Shared across all three listeners so stderr JSON lines cannot mix.
            with _LOG_LOCK:
                self.log_sink(line)
        except Exception:
            pass  # A failed diagnostic sink must not change fetch/slot ownership.

    def elapsed(self, started):
        return round(max(0, self.monotonic() - started) * 1000, 3)

    def end_trace(self, trace, **fields):
        try:
            self.emit(trace, "request_end", **fields)
        finally:
            del self.trace_local.trace

    def read_container(self, path):
        trace = getattr(self.trace_local, "trace", None)
        started, reason = self.monotonic(), "error"
        self.emit(trace, "docker_exec_start")
        try:
            result = subprocess.run(
                ["docker", "exec", self.container, "wget", "-qO-", "-T", "3",
                 "http://127.0.0.1:9090" + path],
                stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=5, check=True,
            )
            reason = "ok"
        except subprocess.TimeoutExpired:
            reason = "timeout"
            raise
        except subprocess.CalledProcessError:
            reason = "nonzero"
            raise
        finally:
            self.emit(trace, "docker_exec_end", reason=reason, duration_ms=self.elapsed(started))
        if len(result.stdout) > 2 * 1024 * 1024:
            raise _ResponseTooLarge("diagnostics response exceeds limit")
        return result.stdout


class DiagnosticsHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def send_error(self, code, message=None, explain=None):
        if getattr(self.server.trace_local, "trace", None) is not None:
            return super().send_error(code, message, explain)
        # BaseHTTPRequestHandler also rejects unsupported/malformed requests.
        # Preserve their status without copying its arbitrary message into logs.
        trace = self.server.begin_trace(getattr(self, "command", None), getattr(self, "path", ""))
        delivery = "complete"
        try:
            return super().send_error(code, message, explain)
        except (BrokenPipeError, ConnectionResetError):
            delivery = "broken_pipe"
        except OSError:
            delivery = "write_error"
        finally:
            self.server.end_trace(trace, status=code, reason="status", delivery=delivery)

    def do_POST(self):
        self.respond(False)

    def do_GET(self):
        self.respond(True)

    def fetch_response(self, trace):
        started = self.server.monotonic()
        self.server.emit(trace, "fetch_start")
        body, status, reason = b"", 503, "fetch_error"
        try:
            body = self.server.fetch(self.path)
            status, reason = 200, "ok"
        except subprocess.TimeoutExpired:
            reason = "timeout"
        except subprocess.CalledProcessError:
            reason = "nonzero"
        except _ResponseTooLarge:
            reason = "response_limit"
        except Exception:
            pass  # Classify without exposing arbitrary stderr/exception strings.
        finally:
            self.server.emit(trace, "fetch_end", reason=reason, duration_ms=self.server.elapsed(started))
        return body, status, reason

    def respond(self, method_allowed):
        trace = self.server.begin_trace(self.command, self.path)
        acquired, status, reason, delivery = False, 0, "fetch_error", "complete"
        try:
            if not method_allowed:
                status, reason = 405, "method_not_allowed"
            elif self.path not in ("/metrics", "/readyz"):
                status, reason = 404, "path_not_allowed"
            elif not self.server.slots.acquire(blocking=False):
                status, reason = 503, "slots_full"
            else:
                acquired = True
                body, status, reason = self.fetch_response(trace)
            if status != 200:
                self.send_error(status)
            else:
                self.send_response(200)
                self.send_header("Content-Type", "text/plain; charset=utf-8")
                self.send_header("Cache-Control", "no-store")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            delivery = "broken_pipe"
        except OSError:
            delivery = "write_error"
        finally:
            if acquired:
                self.server.slots.release()
            self.server.end_trace(trace, status=status, reason=reason, delivery=delivery)


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
