"""One owned normal-build App child and its bounded private pipe transcript."""

import os
from pathlib import Path
import queue
import select
import subprocess
import threading
import time

from receipt import sha, strict_json


class Child:
    def __init__(self, binary, slot, nonce, directory, started):
        self.slot, self.nonce, self.started = slot, nonce, started
        self.records, self.killed, self.error = [], False, None
        self.stop_ns = None
        self.send_lock = threading.Lock()
        self.queue = queue.Queue(16)
        self.stdout_path = Path(directory) / f"child-{slot}.stdout"
        self.stderr_path = Path(directory) / f"child-{slot}.stderr"
        env = {k: v for k, v in os.environ.items() if not k.upper().startswith("LANTERN_")}
        env["OFF_SOURCE_CHILD_NONCE"] = nonce
        env.pop("OFF_SOURCE_CHILD_IDENTITY", None)
        self.stderr = self.stderr_path.open("xb")
        self.launch_ns = time.monotonic_ns() - started
        try:
            self.process = subprocess.Popen([str(binary), "-test.run=^TestOFFConfiguredSourceChild$", "-test.parallel=1", "-test.timeout=120s"], env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.stderr, bufsize=-1)
        except Exception:
            self.stderr.close()
            raise
        try:
            os.set_blocking(self.process.stdin.fileno(), False)
            self.reader = threading.Thread(target=self._read, daemon=True)
            self.reader.start()
        except BaseException:
            try:
                self.process.kill()
                self.process.wait(timeout=5)
            finally:
                self.process.stdin.close()
                self.process.stdout.close()
                self.stderr.close()
            raise

    def _read(self):
        try:
            size = 0
            with self.stdout_path.open("xb") as output:
                while line := self.process.stdout.readline(65537):
                    size += len(line)
                    if len(line) > 65536 or size > 4 << 20:
                        raise ValueError("child output limit exceeded")
                    output.write(line)
                    if line.startswith(b"off_source "):
                        record = strict_json(line[len(b"off_source "):])
                        if record.get("pid") != self.process.pid or record.get("nonce") != self.nonce:
                            raise ValueError("unbound child output")
                        record["received_ns"] = time.monotonic_ns() - self.started
                        self.queue.put_nowait(record)
        except Exception as error:
            self.error = type(error).__name__

    def send(self, phase, deadline, **fields):
        while not self.send_lock.acquire(timeout=min(.05, deadline.check())):
            deadline.check()
        try:
            if phase == "stop" and self.stop_ns is not None:
                return
            self._send(phase, deadline, fields)
        finally:
            self.send_lock.release()

    def _send(self, phase, deadline, fields):
        import json
        raw = (json.dumps({"phase": phase, "nonce": self.nonce, **fields}, separators=(",", ":")) + "\n").encode()
        if len(raw) > 65536:
            raise ValueError("configuration too large")
        view = memoryview(raw)
        while view:
            remaining = deadline.check()
            if not select.select([], [self.process.stdin], [], min(remaining, .1))[1]:
                continue
            deadline.check()
            try:
                view = view[os.write(self.process.stdin.fileno(), view):]
            except BlockingIOError:
                continue
        if phase == "stop":
            self.stop_ns = time.monotonic_ns() - self.started

    def receive(self, event, deadline):
        while deadline.check() > 0:
            if self.error:
                raise ValueError("child transcript failed")
            try:
                record = self.queue.get(timeout=min(.1, deadline.check()))
            except queue.Empty:
                if self.process.poll() is not None:
                    self.reader.join(timeout=min(.05, deadline.check()))
                    if self.queue.empty():
                        raise ValueError("child exited without required phase")
                continue
            deadline.check()
            self.records.append(record)
            if record.get("event") != event:
                raise ValueError("unexpected child phase")
            return record
        raise TimeoutError("child output deadline")

    def terminate(self, kill=False):
        if self.process.poll() is None:
            self.killed = True
            self.process.kill() if kill else self.process.terminate()

    def wait(self, deadline, binary_sha256):
        while self.process.poll() is None:
            try:
                self.process.wait(timeout=min(.1, deadline.check()))
            except subprocess.TimeoutExpired:
                continue
        self.process.wait(timeout=deadline.check())
        while self.reader.is_alive():
            self.reader.join(timeout=min(.1, deadline.check()))
        deadline.check()
        self.process.stdin.close()
        self.process.stdout.close()
        self.stderr.close()
        if self.reader.is_alive() or self.error or not self.queue.empty():
            raise ValueError("child reader incomplete/extra output")
        return self._row(binary_sha256)

    def failure_row(self, binary_sha256):
        """Retain already validated queued frames after failure and full join."""
        if self.process.poll() is None or self.reader.is_alive():
            raise ValueError("failure transcript owner not joined")
        while True:
            try:
                self.records.append(self.queue.get_nowait())
            except queue.Empty:
                break
        return self._row(binary_sha256) | {"reader_error": self.error}

    def _row(self, binary_sha256):
        return {"slot": self.slot, "nonce": self.nonce, "pid": self.process.pid, "launch_ns": self.launch_ns, "stop_ns": self.stop_ns, "wait_ns": time.monotonic_ns() - self.started, "exit": self.process.returncode, "killed": self.killed, "binary_sha256": binary_sha256, "records": self.records, "stdout_sha256": sha(self.stdout_path.read_bytes()), "stderr_sha256": sha(self.stderr_path.read_bytes())}
