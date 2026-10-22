#!/usr/bin/env python3
"""Fixed, separate natural-GC observation after an immutable formal result."""
import argparse
import decimal
import hashlib
import json
import math
import os
from pathlib import Path
import re
import selectors
import signal
import stat
import subprocess
import sys
import tempfile
import time
from urllib.parse import urlsplit

DURATION, INTERVAL, ROUNDS, MAX_REQUESTS, TIMEOUT, RESERVE = 180, 10, 18, 54, 2, 120
MAX_BODY = 1 << 20
NAMES = (
    "go_goroutines", "go_memstats_heap_alloc_bytes", "go_memstats_heap_inuse_bytes",
    "go_memstats_heap_objects", "go_gc_duration_seconds_count", "go_gc_duration_seconds_sum",
    "go_memstats_last_gc_time_seconds", "go_memstats_next_gc_bytes", "lantern_receipt_entries",
    "lantern_receipt_bytes", "lantern_receipt_oldest_deadline_seconds", "lantern_mutation_log_capacity",
    "lantern_mutation_log_fill_ratio", "lantern_mutation_log_evicted_total",
)
FRACTIONS = {NAMES[i] for i in (5, 6, 10, 12)}
REQUIRED = ("runtime_pre.json", "runtime_steady.json", "runtime_post.json", "leak_gate.json",
            "perf_gate.json", "workload_gate.json", "scenario.resolved.yaml")
OPTIONAL = ("metric_gate.json", "semantic_gate.json")
GATE_NAMES = ("leak", "metric", "semantic", "perf", "workload")


class Invalid(Exception):
    def __init__(self, code):
        self.code = code if code in {
            "invalid_config", "insufficient_window", "seal_invalid", "seal_changed", "formal_mismatch",
            "output_error", "formal_ineligible", "metrics_invalid", "body_limit", "fetch_failed", "http_status",
            "timeout", "cancelled", "deadline", "window_end", "missed_round", "late", "reap_failed",
        } else "internal_error"


class Clock:
    def now(self):
        return time.time(), time.monotonic()

    def sleep(self, seconds):
        time.sleep(seconds)


def endpoints(raw):
    values = raw.split(",")
    if len(values) != 3:
        raise Invalid("invalid_config")
    for value, port in zip(values, (9390, 9391, 9392)):
        try:
            u = urlsplit(value)
            valid = (u.scheme == "http" and u.hostname in ("localhost", "127.0.0.1") and u.port == port
                     and u.netloc == u.hostname + ":" + str(port) and u.path == "/metrics"
                     and not u.query and not u.fragment and u.username is None)
        except ValueError:
            valid = False
        if not valid:
            raise Invalid("invalid_config")
    return values


def validate_config(source, run, urls, utc, mono, clock, minimum=DURATION + RESERVE):
    if not re.fullmatch(r"[0-9a-f]{40}", source) or not re.fullmatch(r"[a-z0-9][a-z0-9_-]{0,127}", run):
        raise Invalid("invalid_config")
    values = endpoints(urls)
    if not all(math.isfinite(x) and x > 0 for x in (utc, mono)):
        raise Invalid("invalid_config")
    now_utc, now_mono = clock.now()
    if min(utc - now_utc, mono - now_mono) < minimum:
        raise Invalid("insufficient_window")
    return values


def private_read(path, limit=16 << 20):
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        with os.fdopen(fd, "rb") as f:
            s = os.fstat(f.fileno())
            if not stat.S_ISREG(s.st_mode) or s.st_uid != os.geteuid() or s.st_nlink != 1 or s.st_size > limit:
                raise Invalid("seal_invalid")
            value = f.read(limit + 1)
    except OSError:
        raise Invalid("seal_invalid") from None
    if len(value) > limit:
        raise Invalid("seal_invalid")
    return value


def atomic_write(path, value):
    path = Path(path)
    if path.is_symlink():
        raise Invalid("output_error")
    fd, temporary = tempfile.mkstemp(prefix=".natural-gc-", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as f:
            os.fchmod(f.fileno(), 0o600)
            json.dump(value, f, sort_keys=True, allow_nan=False)
            f.write("\n")
            f.flush()
            os.fsync(f.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def digest(value):
    return hashlib.sha256(value).hexdigest()


def create_seal(path, source, run, formal_exit, verdicts):
    path = Path(path)
    gates = dict(zip(GATE_NAMES, verdicts.split(",")))
    if len(verdicts.split(",")) != 5 or any(v not in ("pass", "fail", "skipped") for v in gates.values()):
        raise Invalid("formal_mismatch")
    artifacts = []
    for name in REQUIRED + tuple(n for n in OPTIONAL if (path.parent / n).exists()):
        raw = private_read(path.parent / name)
        if name.endswith("_gate.json"):
            try:
                actual = json.loads(raw)["verdict"]
            except (ValueError, KeyError, TypeError):
                raise Invalid("formal_mismatch") from None
            if actual != gates[name.removesuffix("_gate.json")]:
                raise Invalid("formal_mismatch")
        artifacts.append(dict(path=name, sha256=digest(raw)))
    if formal_exit not in (0, 1) or (formal_exit == 0) != all(v != "fail" for v in gates.values()):
        raise Invalid("formal_mismatch")
    raw_report = private_read(path.parent / "report.md")
    try:
        fd = os.open(path.parent / "formal_report.md", os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "wb") as f:
            f.write(raw_report)
            f.flush()
            os.fsync(f.fileno())
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    except OSError:
        raise Invalid("seal_invalid") from None
    artifacts.append(dict(path="formal_report.md", sha256=digest(raw_report)))
    seal = dict(schema_version=1, source_id=source, run_id=run, formal_exit_status=formal_exit, gates=gates, artifacts=artifacts)
    with os.fdopen(fd, "w") as f:
        json.dump(seal, f, sort_keys=True)
        f.write("\n")
        f.flush()
        os.fsync(f.fileno())
    return seal


def load_seal(path, source, run):
    raw = private_read(path, 1 << 20)
    try:
        seal = json.loads(raw)
        names = [a["path"] for a in seal["artifacts"]]
        if (seal["schema_version"] != 1 or seal["source_id"] != source or seal["run_id"] != run
                or len(names) != len(set(names)) or set(names) - set(REQUIRED + OPTIONAL + ("formal_report.md",))
                or not set(REQUIRED + ("formal_report.md",)).issubset(names)
                or set(seal["gates"]) != set(GATE_NAMES) or seal["formal_exit_status"] not in (0, 1)
                or any(v not in ("pass", "fail", "skipped") for v in seal["gates"].values())
                or (seal["formal_exit_status"] == 0) != all(v != "fail" for v in seal["gates"].values())):
            raise Invalid("seal_invalid")
        for a in seal["artifacts"]:
            value = private_read(Path(path).parent / a["path"])
            if digest(value) != a["sha256"]:
                raise Invalid("seal_changed")
            if a["path"].endswith("_gate.json") and json.loads(value)["verdict"] != seal["gates"][a["path"].removesuffix("_gate.json")]:
                raise Invalid("formal_mismatch")
    except (ValueError, KeyError, TypeError):
        raise Invalid("seal_invalid") from None
    return seal, digest(raw)


def eligible(seal, directory):
    gates = seal["gates"]
    if any(gates[n] not in ("pass", "skipped") for n in ("metric", "semantic")) or any(gates[n] != "pass" for n in ("perf", "workload")):
        return False
    if seal["formal_exit_status"] == 0:
        return gates["leak"] == "pass"
    leak = json.loads(private_read(Path(directory) / "leak_gate.json"))
    failures = leak.get("failures", [])
    return (seal["formal_exit_status"] == 1 and gates["leak"] == "fail" and leak.get("verdict") == "fail"
            and leak.get("snapshot_protocol") == "natural_gc_single" and len(leak.get("replicas", [])) == 3
            and leak.get("steady_sample_count", 0) >= 9
            and leak.get("thresholds") == dict(goroutine_max_delta=15, heap_alloc_max_delta_mb=32, steady_heap_alloc_max_delta_mb=40)
            and bool(failures) and all(re.fullmatch(r"localhost:939[0-2] post-cooldown HeapAlloc growth exceeds \+32 MiB", x) for x in failures)
            and all(max(r["goroutine_delta"], r["goroutine_peak_delta"]) <= 15 and r["heap_alloc_peak_delta_bytes"] <= 40 * (1 << 20) for r in leak["replicas"]))


def parse_metrics(raw):
    if len(raw) > MAX_BODY:
        raise Invalid("body_limit")
    result = {}
    try:
        for line in raw.decode("utf-8").splitlines():
            if not line or line.startswith("#"):
                continue
            fields = line.split()
            if not fields:
                continue
            name = fields[0].split("{", 1)[0]
            if name not in NAMES:
                continue
            if fields[0] != name or len(fields) != 2 or name in result or len(fields[1]) > 128:
                raise Invalid("metrics_invalid")
            value = decimal.Decimal(fields[1])
            if not value.is_finite() or value < 0:
                raise Invalid("metrics_invalid")
            if name not in FRACTIONS:
                if value > (1 << 63) - 1 or value != value.to_integral_value():
                    raise Invalid("metrics_invalid")
                result[name] = int(value)
            else:
                number = float(value)
                if not math.isfinite(number) or (number == 0 and value != 0) or (name.endswith("fill_ratio") and value > 1):
                    raise Invalid("metrics_invalid")
                result[name] = number
    except (UnicodeError, decimal.InvalidOperation, OverflowError):
        raise Invalid("metrics_invalid") from None
    if set(result) != set(NAMES):
        raise Invalid("metrics_invalid")
    return result


def curl_fetch(url, cancelled=lambda: False, clock=None):
    clock = clock or Clock()
    started = clock.now()[1]
    argv = ["/usr/bin/curl", "--disable", "--silent", "--show-error", "--fail", "--noproxy", "*",
            "--proto", "=http", "--max-time", "2", "--write-out", "\n%{http_code}", "--", url]
    process = None
    try:
        process = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        os.set_blocking(process.stdout.fileno(), False)
        body = bytearray()
        with selectors.DefaultSelector() as selector:
            selector.register(process.stdout, selectors.EVENT_READ)
            while selector.get_map() or process.poll() is None:
                if cancelled():
                    raise Invalid("cancelled")
                remaining = TIMEOUT - (clock.now()[1] - started)
                if remaining <= 0:
                    raise Invalid("timeout")
                for key, _ in selector.select(min(remaining, 0.1)):
                    chunk = os.read(key.fileobj.fileno(), min(65536, MAX_BODY + 5 - len(body)))
                    if not chunk:
                        selector.unregister(key.fileobj)
                    body.extend(chunk)
                    if len(body) > MAX_BODY + 4:
                        raise Invalid("body_limit")
        if process.returncode != 0:
            raise Invalid("timeout" if process.returncode == 28 else "fetch_failed")
        if not body.endswith(b"\n200"):
            raise Invalid("http_status")
        return parse_metrics(body[:-4])
    except OSError:
        raise Invalid("fetch_failed") from None
    finally:
        if process is not None:
            try:
                if process.poll() is None:
                    process.kill()
                process.wait(timeout=1)
            except (OSError, subprocess.TimeoutExpired):
                raise Invalid("reap_failed") from None
            finally:
                process.stdout.close()


def observe(path, output, source, run, urls, deadline_utc, deadline_mono, clock=None, fetch=None, cancelled=lambda: False):
    clock = clock or Clock()
    fetch = fetch or (lambda url: curl_fetch(url, cancelled, clock))
    if Path(output).exists() or Path(output).is_symlink():
        raise Invalid("output_error")
    report = dict(schema_version=1, source_id=source, run_id=run, status="refused", reason="seal_invalid",
                  formal_exit_status=None, formal_gates=None, seal_sha256=None, requests_attempted=0, complete_rounds=0, samples=[])
    write = lambda: atomic_write(output, report)
    try:
        seal, seal_hash = load_seal(path, source, run)
        if Path(output).resolve() in [Path(path).resolve()] + [(Path(path).parent / a["path"]).resolve() for a in seal["artifacts"]]:
            raise Invalid("output_error")
        report.update(formal_exit_status=seal["formal_exit_status"], formal_gates=seal["gates"], seal_sha256=seal_hash)
        if not eligible(seal, Path(path).parent):
            raise Invalid("formal_ineligible")
        values = validate_config(source, run, urls, deadline_utc, deadline_mono, clock)
        start_utc, start_mono = clock.now()
        end_utc, end_mono = start_utc + DURATION, start_mono + DURATION
        report.update(status="running", reason="started", window=dict(duration_seconds=DURATION, interval_seconds=INTERVAL,
                      scheduled_rounds=ROUNDS, max_requests=MAX_REQUESTS, request_timeout_seconds=TIMEOUT,
                      cleanup_reserve_seconds=RESERVE, started_utc=start_utc, started_monotonic=start_mono,
                      end_utc=end_utc, end_monotonic=end_mono, utc_window_is_nominal=True, scheduling_clock="monotonic",
                      outer_deadline_utc=deadline_utc, outer_deadline_monotonic=deadline_mono))
        write()

        def guard():
            utc, mono = clock.now()
            if cancelled():
                raise Invalid("cancelled")
            if utc >= deadline_utc - RESERVE or mono >= deadline_mono - RESERVE:
                raise Invalid("deadline")
            return utc, mono

        def wait(target):
            while True:
                utc, mono = guard()
                if mono >= target:
                    return
                clock.sleep(min(0.1, target - mono))

        for index in range(ROUNDS):
            wait(start_mono + index * INTERVAL)
            if load_seal(path, source, run)[1] != seal_hash:
                raise Invalid("seal_changed")
            utc, mono = guard()
            if mono >= end_mono:
                raise Invalid("window_end")
            round_value = dict(round_index=index, scheduled_elapsed_seconds=index * INTERVAL, status="incomplete", replicas=[])
            report["samples"].append(round_value)
            write()
            if mono >= start_mono + (index + 1) * INTERVAL:
                round_value["reason"] = "missed_round"
                write()
                raise Invalid("missed_round")
            for replica, url in enumerate(values):
                utc, mono = guard()
                if utc + TIMEOUT > deadline_utc - RESERVE or mono + TIMEOUT > min(end_mono, deadline_mono - RESERVE):
                    raise Invalid("window_end")
                value = dict(replica_index=replica, request_start_utc=utc, request_start_monotonic=mono, status="error", reason="fetch_failed", metrics=None)
                round_value["replicas"].append(value)
                report["requests_attempted"] += 1
                try:
                    metrics = fetch(url)
                    end_request_utc, end_request_mono = guard()
                    if end_request_mono - mono > TIMEOUT or end_request_utc - utc > TIMEOUT:
                        raise Invalid("late")
                    if set(metrics) != set(NAMES):
                        raise Invalid("metrics_invalid")
                    if any(not isinstance(v, (int, float)) or isinstance(v, bool) or not math.isfinite(v) or v < 0 for v in metrics.values()):
                        raise Invalid("metrics_invalid")
                    value.update(status="ok", reason="ok", metrics=metrics)
                except Invalid as error:
                    value["reason"] = error.code
                    raise
                except Exception:
                    value["reason"] = "fetch_failed"
                    raise Invalid("fetch_failed") from None
                finally:
                    value["request_end_utc"], value["request_end_monotonic"] = clock.now()
                    write()
            if all(v["status"] == "ok" for v in round_value["replicas"]) and len(round_value["replicas"]) == 3:
                round_value["status"] = "complete"
                report["complete_rounds"] += 1
            if load_seal(path, source, run)[1] != seal_hash:
                raise Invalid("seal_changed")
            write()
        wait(end_mono)
        if load_seal(path, source, run)[1] != seal_hash:
            raise Invalid("seal_changed")
        report.update(status="complete" if report["complete_rounds"] == ROUNDS else "incomplete", reason="ok" if report["complete_rounds"] == ROUNDS else "sample_failed")
    except (Invalid, ValueError, KeyError, TypeError) as error:
        if isinstance(error, Invalid) and error.code == "output_error":
            raise
        report.update(status="incomplete" if report["status"] == "running" else "refused", reason=error.code if isinstance(error, Invalid) else "seal_invalid")
    report["finished_utc"], report["finished_monotonic"] = clock.now()
    write()
    return report


class Parser(argparse.ArgumentParser):
    def error(self, _message):
        raise Invalid("invalid_config")


def main(argv=None):
    try:
        parser = Parser()
        for name in ("source-id", "run-id", "endpoints", "output", "seal", "create-seal", "gate-verdicts"):
            parser.add_argument("--" + name)
        parser.add_argument("--deadline-utc", type=float)
        parser.add_argument("--deadline-monotonic", type=float)
        parser.add_argument("--formal-exit-status", type=int)
        parser.add_argument("--check-config", action="store_true")
        args = parser.parse_args(argv)
        clock = Clock()
        if args.check_config:
            validate_config(args.source_id or "", args.run_id or "", args.endpoints or "", args.deadline_utc or 0, args.deadline_monotonic or 0, clock, DURATION + RESERVE + 65)
            return 0
        if not re.fullmatch(r"[0-9a-f]{40}", args.source_id or "") or not re.fullmatch(r"[a-z0-9][a-z0-9_-]{0,127}", args.run_id or ""):
            raise Invalid("invalid_config")
        if args.create_seal:
            create_seal(args.create_seal, args.source_id, args.run_id, args.formal_exit_status, args.gate_verdicts or "")
            return 0
        if not args.seal or not args.output:
            raise Invalid("invalid_config")
        stopped = [False]
        def stop(_signum, _frame):
            stopped[0] = True
        signal.signal(signal.SIGTERM, stop)
        signal.signal(signal.SIGINT, stop)
        result = observe(args.seal, args.output, args.source_id, args.run_id, args.endpoints or "", args.deadline_utc or 0, args.deadline_monotonic or 0, clock, cancelled=lambda: stopped[0])
        print(json.dumps(dict(status=result["status"], reason=result["reason"])))
        return 0 if result["status"] == "complete" else 1
    except (Invalid, OSError) as error:
        print("natural_gc_observation: " + (error.code if isinstance(error, Invalid) else "io_error"), file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
