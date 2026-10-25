#!/usr/bin/env python3
"""One bounded host-loopback point observation, never an app RPC retry.

The before point precedes the attempt's native build too. Neither point proves
continuous backend health or connectivity from the Simulator.
"""

import http.client
import json
import os
from pathlib import Path
import re
import signal
import socket
import sys
import time


CONTROL_TIMEOUT_SECONDS = 10
MAX_RESPONSE_BYTES = 1024
MAX_RECORD_BYTES = 1024
SLOTS = {f"{attempt}-{phase}.json"
         for attempt in ("initial", "retry") for phase in ("before", "after")}
HEALTH_PATH = "/grpc.health.v1.Health/Check"


class ControlTimeout(Exception):
    """The owned whole-observation deadline expired."""


def _expired(_signal, _frame):
    raise ControlTimeout


def _supported_platform():
    return (os.name == "posix" and hasattr(signal, "SIGALRM")
            and hasattr(signal, "setitimer") and hasattr(signal, "getitimer")
            and hasattr(signal, "ITIMER_REAL"))


def _pid_presence(path):
    try:
        with Path(path).open("rb") as source:
            raw = source.read(33)
    except FileNotFoundError:
        return "pid_missing"
    except OSError:
        return "unavailable"
    if not re.fullmatch(rb"[1-9][0-9]{0,9}\n?", raw):
        return "pid_invalid"
    pid = int(raw)
    if pid > 2147483647:
        return "pid_invalid"
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return "absent"
    except OSError:
        return "unavailable"
    return "present"


def _health_object(pairs):
    if len(pairs) != 1 or pairs[0][0] != "status":
        raise ValueError
    return {"status": pairs[0][1]}


def _health(record):
    connection = http.client.HTTPConnection(
        "127.0.0.1", 6380, timeout=CONTROL_TIMEOUT_SECONDS,
    )
    try:
        connection.request(
            "POST", HEALTH_PATH, body=b"{}",
            headers={"Content-Type": "application/json",
                     "Connect-Protocol-Version": "1"},
        )
        response = connection.getresponse()
        status = response.status
        if type(status) is not int or not 100 <= status <= 599:
            record["health_outcome"] = "invalid_http_status"
            return
        record["http_status"] = status
        if status != 200:
            record["health_outcome"] = "http_error"
            return
        body = response.read(MAX_RESPONSE_BYTES + 1)
        if len(body) > MAX_RESPONSE_BYTES:
            record["health_outcome"] = "oversized"
            return
        try:
            message = json.loads(body.decode("utf-8"), object_pairs_hook=_health_object)
        except (UnicodeError, ValueError):
            record["health_outcome"] = "malformed"
            return
        if not isinstance(message, dict) or type(message.get("status")) is not str:
            record["health_outcome"] = "malformed"
            return
        if message["status"] in ("SERVING", "SERVING_STATUS_SERVING"):
            record["health_status"] = "serving"
            record["health_outcome"] = "serving"
        else:
            record["health_status"] = "not_serving"
            record["health_outcome"] = "not_serving"
    finally:
        connection.close()


def observe(attempt, phase, pid_file):
    if attempt not in ("initial", "retry") or phase not in ("before", "after"):
        raise ValueError
    record = {
        "schema": 1, "attempt": attempt, "phase": phase,
        "scope": "host_loopback_point", "continuous_health": False,
        "pid_presence": "unobserved", "http_status": None,
        "health_status": "unavailable", "health_outcome": "unavailable",
        "elapsed_ms": 0,
    }
    started = time.monotonic()
    try:
        # Windows kill(pid, 0) is not a read-only presence check.
        if not _supported_platform():
            record["health_outcome"] = "unsupported"
            return record
        previous_handler = signal.getsignal(signal.SIGALRM)
        previous_timer = signal.getitimer(signal.ITIMER_REAL)
        # This standalone helper owns its timer; never suspend another owner.
        if previous_timer != (0.0, 0.0):
            record["health_outcome"] = "control_unavailable"
            return record
        installed = False
        try:
            signal.signal(signal.SIGALRM, _expired)
            installed = True
            signal.setitimer(signal.ITIMER_REAL, CONTROL_TIMEOUT_SECONDS)
            record["pid_presence"] = _pid_presence(pid_file)
            _health(record)
        except (ControlTimeout, socket.timeout, TimeoutError):
            record["health_outcome"] = "timeout"
            record["health_status"] = "unavailable"
        except (OSError, http.client.HTTPException):
            record["health_outcome"] = "transport_error"
            record["health_status"] = "unavailable"
        except Exception:
            record["health_outcome"] = "control_error"
            record["health_status"] = "unavailable"
        finally:
            if installed:
                try:
                    signal.setitimer(signal.ITIMER_REAL, 0)
                finally:
                    signal.signal(signal.SIGALRM, previous_handler)
                    signal.setitimer(signal.ITIMER_REAL, *previous_timer)
    finally:
        record["elapsed_ms"] = max(0, int((time.monotonic() - started) * 1000))
    return record


def _destination(directory, attempt, phase):
    directory = Path(directory)
    if not directory.is_absolute() or directory.is_symlink():
        raise ValueError
    directory.mkdir(parents=True, exist_ok=True)
    for entry in directory.iterdir():
        if (entry.name not in SLOTS or entry.is_symlink() or not entry.is_file()
                or entry.stat().st_size > MAX_RECORD_BYTES):
            raise ValueError
    destination = directory / f"{attempt}-{phase}.json"
    if destination.exists():
        raise ValueError
    return destination


def main(arguments=None):
    arguments = sys.argv[1:] if arguments is None else arguments
    try:
        if (len(arguments) != 4 or arguments[0] not in ("initial", "retry")
                or arguments[1] not in ("before", "after")):
            raise ValueError
        attempt, phase, pid_file, directory = arguments
        destination = _destination(directory, attempt, phase)
        record = observe(attempt, phase, pid_file)
        encoded = (json.dumps(record, sort_keys=True, separators=(",", ":")) + "\n").encode()
        if len(encoded) > MAX_RECORD_BYTES:
            raise ValueError
        with destination.open("xb") as output:
            output.write(encoded)
        print(encoded.decode(), end="")
        # An after point records what happened; it never reclassifies the app.
        return int(phase == "before" and not (
            record["pid_presence"] == "present" and record["health_outcome"] == "serving"
        ))
    except Exception:
        print("ios_backend_observation_failed", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
