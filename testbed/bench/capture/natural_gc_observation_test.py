import copy
import io
import json
from pathlib import Path
import tempfile
import types
import unittest
from unittest import mock

import natural_gc_observation as observation


SOURCE = "a" * 40
RUN = "lantern-natural-gc-local-test"
URLS = ",".join("http://127.0.0.1:%d/metrics" % p for p in (9390, 9391, 9392))


class FakeClock:
    def __init__(self):
        self.mono = 100.0
        self.shift = 0.0

    def now(self):
        return 1700000000.0 + self.mono - 100 + self.shift, self.mono

    def sleep(self, seconds):
        assert seconds >= 0
        self.mono += seconds


def metrics():
    values = {name: 1 for name in observation.NAMES}
    values["lantern_mutation_log_fill_ratio"] = 0.25
    return values


def exposition(values=None):
    return ("\n".join("%s %s" % item for item in (values or metrics()).items()) + "\n").encode()


class Fixture(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.clock = FakeClock()
        self.seal = self.root / "formal_result.json"
        self.output = self.root / "natural_gc_observation.json"

    def formal(self, failed=None):
        gates = {name: "skipped" if name in ("metric", "semantic") else "pass" for name in observation.GATE_NAMES}
        if failed:
            gates["leak" if failed in ("post", "steady", "goroutines", "coverage") else failed] = "fail"
        leak = dict(verdict=gates["leak"], snapshot_protocol="natural_gc_single", steady_sample_count=10,
                    thresholds=dict(goroutine_max_delta=15, heap_alloc_max_delta_mb=32, steady_heap_alloc_max_delta_mb=40),
                    replicas=[dict(goroutine_delta=0, goroutine_peak_delta=1, heap_alloc_delta_bytes=0,
                                   heap_alloc_peak_delta_bytes=0) for _ in range(3)])
        if failed in ("post", "steady", "goroutines", "coverage"):
            suffix = {"post": "post-cooldown HeapAlloc growth exceeds +32 MiB", "steady": "steady heap_alloc peak growth exceeds +40 MiB",
                      "goroutines": "goroutine post/steady growth exceeds +15", "coverage": "steady samples: missing round"}[failed]
            leak["failures"] = ["localhost:9392 " + suffix]
            if failed == "post":
                leak["replicas"][2]["heap_alloc_delta_bytes"] = 34 * (1 << 20)
        for name in observation.REQUIRED:
            value = leak if name == "leak_gate.json" else {"verdict": gates[name.removesuffix("_gate.json")]} if name.endswith("_gate.json") else {}
            (self.root / name).write_text(json.dumps(value))
        (self.root / "report.md").write_text("unchanged formal report\n")
        observation.create_seal(self.seal, SOURCE, RUN, int(bool(failed)), ",".join(gates[n] for n in observation.GATE_NAMES))
        return {p.name: p.read_bytes() for p in self.root.iterdir() if p.is_file()}

    def observe(self, fetch, **kwargs):
        utc, mono = self.clock.now()
        return observation.observe(self.seal, self.output, SOURCE, RUN, URLS, utc + 600, mono + 600,
                                   clock=self.clock, fetch=fetch, **kwargs)


class NaturalGCObservationTests(Fixture):
    def test_exact_18_rounds_54_requests_and_180_seconds_preserve_formal_fail(self):
        before = self.formal("post")
        calls = []
        def fetch(url):
            calls.append((url, self.clock.now()))
            self.clock.sleep(0.25)
            value = metrics()
            value["go_memstats_heap_alloc_bytes"] = len(calls)
            return value
        result = self.observe(fetch)
        self.assertEqual((result["status"], result["reason"], result["formal_exit_status"]), ("complete", "ok", 1))
        self.assertEqual((len(calls), result["requests_attempted"], result["complete_rounds"]), (54, 54, 18))
        self.assertEqual(self.clock.mono, 280)
        self.assertEqual([calls[i * 3][1][1] for i in range(18)], list(range(100, 280, 10)))
        self.assertEqual([r["metrics"]["go_memstats_heap_alloc_bytes"] for s in result["samples"] for r in s["replicas"]], list(range(1, 55)))
        self.assertEqual({name: (self.root / name).read_bytes() for name in before}, before)
        self.assertNotIn("http://", self.output.read_text())

    def test_collection_failure_preserves_original_pass_and_partial_samples(self):
        before = self.formal()
        calls = []
        def fetch(url):
            calls.append(url)
            if len(calls) == 2:
                raise observation.Invalid("timeout")
            return metrics()
        result = self.observe(fetch)
        self.assertEqual((result["status"], result["reason"], result["formal_exit_status"]), ("incomplete", "timeout", 0))
        self.assertEqual(result["requests_attempted"], 2)
        self.assertEqual([v["status"] for v in result["samples"][0]["replicas"]], ["ok", "error"])
        self.assertEqual({name: (self.root / name).read_bytes() for name in before}, before)

    def test_non_post_memory_failures_refuse_without_fetch(self):
        for failure in ("steady", "goroutines", "coverage", "perf", "workload"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                old_root = self.root
                self.root = Path(directory)
                self.seal, self.output = self.root / "formal_result.json", self.root / "natural_gc_observation.json"
                before = self.formal(failure)
                fetch = mock.Mock(side_effect=AssertionError("must not fetch"))
                result = self.observe(fetch)
                self.assertEqual((result["status"], result["reason"]), ("refused", "formal_ineligible"))
                fetch.assert_not_called()
                self.assertEqual({name: (self.root / name).read_bytes() for name in before}, before)
                self.root = old_root

    def test_full_window_both_deadlines_and_check_config_without_io(self):
        self.formal()
        utc, mono = self.clock.now()
        for du, dm in ((299, 600), (600, 299)):
            result = observation.observe(self.seal, self.output, SOURCE, RUN, URLS, utc + du, mono + dm,
                                         clock=self.clock, fetch=mock.Mock(side_effect=AssertionError()))
            self.assertEqual((result["status"], result["reason"]), ("refused", "insufficient_window"))
            self.output.unlink()
        args = ["--check-config", "--source-id", SOURCE, "--run-id", RUN, "--endpoints", URLS,
                "--deadline-utc", str(utc + 365), "--deadline-monotonic", str(mono + 365)]
        with mock.patch.object(observation, "Clock", return_value=self.clock), mock.patch.object(observation, "private_read", side_effect=AssertionError()):
            self.assertEqual(observation.main(args), 0)
            args[-1] = str(mono + 364)
            self.assertEqual(observation.main(args), 2)

    def test_no_catchup_late_values_cancellation_and_outer_utc_cutoff(self):
        for mode in ("late", "cancelled", "deadline", "malformed", "unexpected"):
            with self.subTest(mode=mode):
                if self.seal.exists():
                    for p in self.root.iterdir():
                        p.unlink()
                self.clock = FakeClock()
                self.formal()
                cancelled = [False]
                calls = []
                def fetch(url):
                    calls.append(url)
                    if mode == "late":
                        self.clock.sleep(2.01)
                    elif mode == "cancelled":
                        cancelled[0] = True
                    elif mode == "deadline":
                        self.clock.shift = 500
                    elif mode == "malformed":
                        return {}
                    elif mode == "unexpected":
                        raise RuntimeError("SECRET endpoint or body")
                    return metrics()
                result = self.observe(fetch, cancelled=lambda: cancelled[0])
                expected = "metrics_invalid" if mode == "malformed" else "fetch_failed" if mode == "unexpected" else mode
                self.assertEqual((result["status"], result["reason"], len(calls)), ("incomplete", expected, 1))
                self.assertIsNone(result["samples"][0]["replicas"][0]["metrics"])
                self.assertNotIn("SECRET", self.output.read_text())
        for p in self.root.iterdir():
            p.unlink()
        self.clock = FakeClock()
        self.formal()
        original_write = observation.atomic_write
        def delayed_write(path, value):
            original_write(path, value)
            if value["status"] == "running" and not value["samples"]:
                self.clock.sleep(11)
        with mock.patch.object(observation, "atomic_write", side_effect=delayed_write):
            fetch = mock.Mock(side_effect=AssertionError("no catchup"))
            result = self.observe(fetch)
        self.assertEqual((result["status"], result["reason"], result["requests_attempted"]), ("incomplete", "missed_round", 0))
        fetch.assert_not_called()

    def test_frozen_report_and_seal_tamper_and_unsafe_output_never_overwrite(self):
        before = self.formal()
        with (self.root / "report.md").open("a") as f:
            f.write("known cleanup note\n")
        observation.load_seal(self.seal, SOURCE, RUN)
        for name in ("formal_result.json", "formal_report.md", "leak_gate.json"):
            with self.subTest(name=name), self.assertRaises(observation.Invalid):
                observation.observe(self.seal, self.root / name, SOURCE, RUN, URLS, 1700000600, 700, clock=self.clock)
            self.assertEqual((self.root / name).read_bytes(), before[name])
        target = self.root / "formal_report.md"
        target.write_text("tampered")
        result = self.observe(mock.Mock(side_effect=AssertionError()))
        self.assertEqual((result["status"], result["reason"]), ("refused", "seal_changed"))

    def test_mid_observation_seal_change_stops_after_current_triplet(self):
        self.formal()
        calls = []
        def fetch(url):
            calls.append(url)
            if len(calls) == 1:
                (self.root / "formal_report.md").write_text("changed")
            return metrics()
        result = self.observe(fetch)
        self.assertEqual((result["status"], result["reason"], len(calls)), ("incomplete", "seal_changed", 3))

    def test_seal_creation_failure_and_identity_regular_file_restrictions(self):
        self.formal()
        for source, run in (("b" * 40, RUN), (SOURCE, "another-owned-run")):
            with self.subTest(source_binding=source == SOURCE), self.assertRaises(observation.Invalid):
                observation.load_seal(self.seal, source, run)
        self.seal.unlink()
        with self.assertRaises(observation.Invalid):
            observation.create_seal(self.seal, SOURCE, RUN, 1, "pass,skipped,skipped,pass,pass")
        self.assertFalse(self.seal.exists())
        (self.root / "workload_gate.json").write_text('{"verdict":"fail"}')
        with self.assertRaises(observation.Invalid):
            observation.create_seal(self.seal, SOURCE, RUN, 0, "pass,skipped,skipped,pass,pass")
        self.assertFalse(self.seal.exists())
        (self.root / "workload_gate.json").write_text('{"verdict":"pass"}')
        (self.root / "formal_report.md").unlink()
        (self.root / "runtime_pre.json").unlink()
        (self.root / "runtime_pre.json").symlink_to(self.root / "runtime_post.json")
        with self.assertRaises(observation.Invalid):
            observation.create_seal(self.seal, SOURCE, RUN, 0, "pass,skipped,skipped,pass,pass")
        self.assertFalse(self.seal.exists())


class ProjectionTests(unittest.TestCase):
    def test_exact_numeric_whitelist_and_unsafe_metrics(self):
        self.assertEqual(observation.parse_metrics(b" \n# comment\n" + exposition() + b"ignored SECRET\n"), metrics())
        cases = [exposition() + b"go_goroutines 2\n", exposition().replace(b"go_goroutines 1", b"go_goroutines{x=\"SECRET\"} 1"),
                 exposition().replace(b"go_goroutines 1", b"go_goroutines NaN"), exposition().replace(b"go_goroutines 1", b"go_goroutines 1.5"),
                 exposition().replace(b"go_goroutines 1", b"go_goroutines -1"), exposition().replace(b"go_goroutines 1", b"go_goroutines 1e999999"),
                 exposition().replace(b"go_gc_duration_seconds_sum 1", b"go_gc_duration_seconds_sum 1e-99999"),
                 exposition().replace(b"lantern_mutation_log_fill_ratio 0.25", b"lantern_mutation_log_fill_ratio 1.01"), b"go_goroutines 1\n", b"x" * (observation.MAX_BODY + 1)]
        for raw in cases:
            with self.subTest(raw_size=len(raw)), self.assertRaises(observation.Invalid):
                observation.parse_metrics(raw)

    def test_explicit_loopback_only_and_fixed_error_projection(self):
        self.assertEqual(observation.endpoints(URLS), URLS.split(","))
        for replacement in ("http://localhost.evil:9390/metrics", "http://user:SECRET@localhost:9390/metrics", "http://localhost:9390/debug/pprof/heap?gc=1", "http://[::1]:9390/metrics", "https://localhost:9390/metrics", "http://localhost:9391/metrics", "http://127.0.0.1:9390/metrics?x=SECRET"):
            with self.subTest(replacement=replacement), self.assertRaises(observation.Invalid):
                observation.endpoints(replacement + "," + ",".join(URLS.split(",")[1:]))
        self.assertEqual(observation.Invalid("SECRET raw response").code, "internal_error")


class CurlFetchTests(unittest.TestCase):
    def fake_fetch(self, payload, mode="success"):
        clock = FakeClock()
        process = mock.Mock()
        process.stdout.fileno.return_value = 9
        process.returncode = 0
        state = dict(data=payload, eof=False, registered=True)
        process.poll.side_effect = lambda: 0 if state["eof"] else None
        def select(_timeout):
            clock.sleep(0.11 if mode == "trickle" else 0.1 if mode in ("timeout", "cancel") else 0.01)
            return [] if mode in ("timeout", "cancel") else [(types.SimpleNamespace(fileobj=process.stdout), 1)]
        selector = mock.MagicMock()
        selector.__enter__.return_value = selector
        selector.get_map.side_effect = lambda: {9: 1} if state["registered"] else {}
        selector.select.side_effect = select
        selector.unregister.side_effect = lambda _f: state.update(registered=False)
        def read(_fd, count):
            size = 1 if mode == "trickle" else count
            result, state["data"] = state["data"][:size], state["data"][size:]
            if not result:
                state["eof"] = True
            return result
        with mock.patch.object(observation.subprocess, "Popen", return_value=process) as popen, \
             mock.patch.object(observation.selectors, "DefaultSelector", return_value=selector), \
             mock.patch.object(observation.os, "set_blocking"), mock.patch.object(observation.os, "read", side_effect=read):
            try:
                result = observation.curl_fetch("http://127.0.0.1:9390/metrics", lambda: mode == "cancel" and clock.mono > 100.1, clock)
            except observation.Invalid as error:
                result = error.code
        process.wait.assert_called_once_with(timeout=1)
        process.stdout.close.assert_called_once()
        args = popen.call_args.args[0]
        self.assertEqual(args[1], "--disable")
        self.assertEqual(args[args.index("--max-time") + 1], "2")
        self.assertEqual(args[args.index("--noproxy") + 1], "*")
        return result, process

    def test_bounded_body_global_timeout_cancellation_and_child_reap(self):
        result, _ = self.fake_fetch(exposition() + b"\n200")
        self.assertEqual(result, metrics())
        for mode in ("timeout", "trickle", "cancel"):
            with self.subTest(mode=mode):
                result, process = self.fake_fetch(exposition() + b"\n200", mode)
                self.assertEqual(result, "cancelled" if mode == "cancel" else "timeout")
                process.kill.assert_called_once()
        result, process = self.fake_fetch(b"x" * (observation.MAX_BODY + 5))
        self.assertEqual(result, "body_limit")
        process.kill.assert_called_once()
        self.assertEqual(self.fake_fetch(exposition() + b"\n302")[0], "http_status")


if __name__ == "__main__":
    unittest.main()
