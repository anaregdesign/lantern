#!/usr/bin/env python3
"""Bound iOS CI output without making phase detection depend on retained logs."""

import os
from pathlib import Path
import re
import sys

MAX_FILE_BYTES = 262144
MAX_ARTIFACT_BYTES = 2097152
MAX_FILES = 32
MAX_LINE_BYTES = 8192
FINALIZED = b"bounded=true redacted=true\n"
MARKERS = {
    b"Running Xcode build...": "build_started",
    b"Xcode build done.": "build_done",
    b"MOBILE_SMOKE_BODY_STARTED": "body_started",
    b"MOBILE_SMOKE_PASS ": "smoke_pass",
    b"All tests passed!": "tests_passed",
}
REDACTIONS = (
    (re.compile(rb"https?://[^\s<>]+"), b"<redacted-url>"),
    (
        re.compile(rb"(Authorization:?[ \t]*(?:Bearer[ \t]*)?)[^\s]+", re.I),
        rb"\1<redacted>",
    ),
    (re.compile(rb"((?:LANTERN_)?TOKEN|token)[=:][^\s]+"), rb"\1=<redacted>"),
    (re.compile(rb"mobile-smoke:[^\s]+"), b"mobile-smoke:<redacted>"),
    (
        re.compile(rb"[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}"),
        b"<redacted-jwt>",
    ),
)


def text_tail(data, limit):
    """Keep the newest bytes without leaving a partial UTF-8 character."""
    return data[-limit:].decode("utf-8", errors="ignore").encode("utf-8") if limit else b""


class Redactor:
    """Only publish complete lines; omit oversized lines rather than leak fragments."""

    def __init__(self):
        self.pending = b""
        self.discarding = False

    def feed(self, chunk, final=False):
        output = bytearray()
        for part in chunk.splitlines(keepends=True):
            ended = part.endswith((b"\n", b"\r"))
            if not self.discarding:
                if len(self.pending) + len(part) > MAX_LINE_BYTES:
                    self.pending = b""
                    self.discarding = True
                    output.extend(b"[ios smoke: oversized log line omitted]\n")
                else:
                    self.pending += part
            if ended:
                if not self.discarding:
                    output.extend(self.redact(self.pending))
                self.pending = b""
                self.discarding = False
        if final and self.pending:
            output.extend(self.redact(self.pending))
            self.pending = b""
        return bytes(output)

    @staticmethod
    def redact(line):
        for pattern, replacement in REDACTIONS:
            line = pattern.sub(replacement, line)
        return line.decode("utf-8", errors="replace").encode("utf-8")


def stream(destination, phases=None):
    redactor = Redactor()
    retained = b""
    overlap = b""
    seen = set()
    console_bytes = 0
    console_suppressed = False
    overlap_bytes = max(map(len, MARKERS)) - 1
    if phases is not None:
        phases.mkdir(parents=True, exist_ok=True)
        for phase in MARKERS.values():
            (phases / phase).unlink(missing_ok=True)
    with destination.open("wb", buffering=0) as output:
        while True:
            chunk = os.read(sys.stdin.fileno(), 65536)
            if phases is not None:
                observed = overlap + chunk
                for marker, phase in MARKERS.items():
                    if phase not in seen and marker in observed:
                        # A separate sticky flag survives log rotation and SIGTERM.
                        (phases / phase).write_bytes(b"seen\n")
                        seen.add(phase)
                        sys.stdout.buffer.write(b"[ios smoke phase] " + marker + b"\n")
                overlap = observed[-overlap_bytes:]
            safe = redactor.feed(chunk, final=not chunk)
            if safe:
                retained = text_tail(retained + safe, MAX_FILE_BYTES)
                # No raw spool or replacement file: disk usage is always <= the cap.
                output.seek(0)
                output.write(retained)
                output.truncate()
                if phases is not None:
                    remaining = MAX_FILE_BYTES - console_bytes
                    if remaining:
                        visible = safe[:remaining].decode("utf-8", errors="ignore").encode("utf-8")
                        sys.stdout.buffer.write(visible)
                        console_bytes += len(visible)
                    if len(safe) > remaining and not console_suppressed:
                        sys.stdout.buffer.write(b"\n[ios smoke: further live output suppressed; bounded artifact retained]\n")
                        console_suppressed = True
            if phases is not None:
                sys.stdout.buffer.flush()
            if not chunk:
                break


def sanitize(path):
    redactor = Redactor()
    retained = b""
    with path.open("rb") as source:
        while True:
            chunk = source.read(65536)
            retained = text_tail(retained + redactor.feed(chunk, final=not chunk), MAX_FILE_BYTES)
            if not chunk:
                break
    path.write_bytes(retained)


def priority(path):
    if path.name == "classification.txt":
        return 0
    if path.parent.name == "phases" and path.name in (*MARKERS.values(), "tests_failed"):
        return 1
    return {
        "flutter.log": 2,
        "driver.log": 3,
        "app.log": 4,
        "process-tree-before-stop.txt": 5,
        "simulator.log": 6,
        "app-container.txt": 7,
        "installed-apps.txt": 8,
    }.get(path.name, 7)


def finalize(root):
    root.mkdir(parents=True, exist_ok=True)
    files = []
    for path in root.rglob("*"):
        if path.is_symlink() or not (path.is_file() or path.is_dir()):
            raise ValueError("diagnostics contain a non-regular entry")
        if path.is_file():
            if path.suffix in (".raw", ".bounded", ".sanitized") or path.name == "finalized.txt":
                path.unlink()
            else:
                files.append(path)
    # Reserve one file and its bytes for finalization metadata. Keep classifications
    # and phase flags first, then useful logs from both attempts, deterministically.
    files.sort(key=lambda path: (priority(path), str(path)))
    for path in files[MAX_FILES - 1:]:
        path.unlink()
    files = files[:MAX_FILES - 1]
    budget = MAX_ARTIFACT_BYTES - len(FINALIZED)
    logs = []
    for path in files:
        sanitize(path)
        if priority(path) <= 1:
            path.write_bytes(text_tail(path.read_bytes(), 1024))
            budget -= path.stat().st_size
        else:
            logs.append(path)
    # Small files keep their contents; large files share the remaining allowance.
    # Trimming sanitized tails retains diagnostics instead of rejecting the upload.
    logs.sort(key=lambda path: (path.stat().st_size, str(path)))
    for index, path in enumerate(logs):
        allowance = min(MAX_FILE_BYTES, budget // (len(logs) - index))
        path.write_bytes(text_tail(path.read_bytes(), allowance))
        budget -= path.stat().st_size
    (root / "finalized.txt").write_bytes(FINALIZED)


def main():
    command, *args = sys.argv[1:]
    if command == "stream" and len(args) in (1, 2):
        stream(Path(args[0]), Path(args[1]) if len(args) == 2 else None)
    elif command == "sanitize" and len(args) == 1:
        sanitize(Path(args[0]))
    elif command == "finalize" and len(args) == 1:
        finalize(Path(args[0]))
    else:
        raise ValueError("usage: ios_smoke_log.py stream <log> [phases] | sanitize <file> | finalize <root>")


if __name__ == "__main__":
    main()
