#!/usr/bin/env python3
"""Drain ghz stderr; retain only bounded fixed text-family observations."""
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import sys


CAP = 65536
MAX_BYTES = (1 << 64) - 1
STAGES = {
    "metadata": rb"\bmetadata\b", "reflection": rb"\breflection\b",
    "service_descriptor": rb"\bservice[ _-]+descriptor\b",
    "dial": rb"\bdial(?:ing)?\b", "connect": rb"\bconnect(?:ing|ion)?\b",
}
CLASSES = {
    "context_deadline": rb"\bcontext deadline exceeded\b",
    "connection_refused": rb"\bconnection refused\b",
    "tls": rb"\btls\b|\bx509\b",
}
CODES = ("OK", "Canceled", "Unknown", "InvalidArgument", "DeadlineExceeded",
         "NotFound", "AlreadyExists", "PermissionDenied", "ResourceExhausted",
         "FailedPrecondition", "Aborted", "OutOfRange", "Unimplemented",
         "Internal", "Unavailable", "DataLoss", "Unauthenticated")
CODE_PATTERN = rb"\bcode\s*[:=]\s*(" + b"|".join(x.encode() for x in CODES) + rb")\b"


def utc():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def drain(stream):
    prefix, count, saturated = bytearray(), 0, False
    while True:
        chunk = stream.read(CAP)
        if not chunk:
            return prefix, count, saturated
        saturated = saturated or len(chunk) > MAX_BYTES - count
        count = min(MAX_BYTES, count + len(chunk))
        prefix.extend(chunk[:max(0, CAP - len(prefix))])


def classify(prefix):
    stages = [name for name, pattern in STAGES.items() if re.search(pattern, prefix, re.I)]
    classes = [name for name, pattern in CLASSES.items() if re.search(pattern, prefix, re.I)]
    found = {match.group(1).lower() for match in re.finditer(CODE_PATTERN, prefix, re.I)}
    codes = [code for code in CODES if code.lower().encode() in found]
    if codes:
        classes.append("grpc_status")
    patterns = list(STAGES.values()) + list(CLASSES.values()) + [CODE_PATTERN]
    unknown = sum(bool(line.strip()) and not any(re.search(p, line, re.I) for p in patterns)
                  for line in prefix.splitlines())
    return {"stage": stages[0] if stages else "unclassified" if prefix else "none",
            "class": classes[0] if classes else "unclassified" if prefix else "empty",
            "observed_stages": stages, "observed_classes": classes,
            "grpc_codes": codes, "unclassified_line_count": unknown}


def binary_sha(path):
    fd = os.open(path, os.O_RDONLY | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as stream:
        if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode):
            raise ValueError("regular binary required")
        digest = hashlib.sha256()
        for chunk in iter(lambda: stream.read(CAP), b""):
            digest.update(chunk)
        return digest.hexdigest()


def write_private(path, record):
    path = Path(path)
    directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    created = False
    try:
        info = os.fstat(directory)
        if info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o700:
            raise ValueError("private owned directory required")
        fd = os.open(path.name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW,
                     0o600, dir_fd=directory)
        created = True
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(record, stream, sort_keys=True, allow_nan=False)
            stream.write("\n")
    except BaseException:
        if created:
            os.unlink(path.name, dir_fd=directory)
        raise
    finally:
        os.close(directory)


def main(argv=None, stdin=None, errors=None):
    argv = sys.argv[1:] if argv is None else argv
    if argv == ["--check"]:
        return 0  # Syntax/import startup check; never reads stdin or creates files.
    stream = sys.stdin.buffer if stdin is None else stdin
    errors = sys.stderr if errors is None else errors
    started = utc()
    try:
        prefix, count, saturated = drain(stream)  # Always EOF before hash/write errors.
        if len(argv) != 2:
            raise ValueError("two arguments required")
        record = {"schema": 1, "raw_stderr_saved": False,
                  "classification_scope": "captured_prefix_only",
                  "capture_cap_bytes": CAP, "captured_prefix_bytes": len(prefix),
                  "observed_stderr_bytes": count, "byte_count_saturated": saturated,
                  "truncated": count > CAP, "started_utc": started,
                  "observed_ghz_binary_sha256": binary_sha(argv[1]),
                  "semantics": "Fixed text families only; not product cause, RPC result, or execution-stage proof.",
                  **classify(prefix), "finished_utc": utc()}
        write_private(argv[0], record)
        return 0
    except Exception:
        try:
            errors.write("write_failure\n")
        except Exception:
            pass
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
