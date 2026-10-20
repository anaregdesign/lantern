import copy
import json
from pathlib import Path
import tempfile
import unittest

from receipt import EVENTS, SCHEMA, sha, strict_json, verify


class ReceiptTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.prepared = {"digest": "preparation", "source": {"head": "a" * 40}, "target": ["linux", "arm64"], "child_sha256": "binary"}
        native = {"system": "Linux", "kernel": "synthetic-unit-fixture", "machine": "aarch64", "boot_sha256": "a" * 64, "namespace": "time:[1]", "offsets_sha256": "b" * 64, "source_path": "/etc/ntp.conf", "source_sha256": sha(b"server time.asia.apple.com\n"), "source_read_only": True, "capabilities": ["CapEff:\t0"]}
        self.receipt = {"schema": SCHEMA, "status": "PASS", "source": self.prepared["source"], "prepared_sha256": "preparation", "before": native, "after": native.copy(), "elapsed_ns": 2000, "final_elapsed_ns": 2500, "boottime_elapsed_ns": 3000, "wire": {"public_off": True, "replication": True, "private": True}, "children": []}
        for slot in range(1, 4):
            nonce = f"{slot:064x}"
            records = [{"event": event, "nonce": nonce, "pid": slot, "elapsed_ns": 10 * i, "received_ns": 100 * slot + 10 * i} for i, event in enumerate(EVENTS)]
            records[1].update(mode="off", initializers=1, revision="a" * 40, goos="linux", goarch="arm64")
            records[2]["ok"] = records[4]["ok"] = True
            counts = {"attempts": 1, "writes": 1, "installations": 1, "in_flight": False, "failed": False, "denied": False}
            records[-1].update(mode="off", counters=counts, repeat=counts.copy(), closed_refused=True, app_ok=True)
            self.receipt["children"].append({"slot": slot, "pid": slot, "nonce": nonce, "exit": 0, "killed": False, "binary_sha256": "binary", "stop_ns": 500, "wait_ns": 1000, "launch_ns": 0, "records": records})
        self.receipt["launch_ledger"] = [{k: v for k, v in row.items() if k in ("slot", "pid", "nonce", "exit", "killed", "launch_ns", "stop_ns")} | {"final_counters_available": True} for row in self.receipt["children"]]
        self.raw(self.receipt)

    def raw(self, receipt):
        for row in receipt["children"]:
            records = [{k: v for k, v in r.items() if k != "received_ns"} for r in row["records"]]
            stdout = b"".join(b"off_source " + json.dumps(r).encode() + b"\n" for r in records)
            for stream, value in (("stdout", stdout), ("stderr", b"")):
                (self.directory / f"child-{row['slot']}.{stream}").write_bytes(value)
                row[stream + "_sha256"] = sha(value)

    def test_complete_final_joined_rows(self):
        self.assertEqual(verify(self.receipt, self.prepared, self.directory), {"owners": 3, "attempts": 3, "writes": 3})

    def test_refusal_vectors(self):
        def child(receipt): return receipt["children"][0]
        def final(receipt): return child(receipt)["records"][-1]
        def alter_counts(receipt, key, value):
            final(receipt)["counters"][key] = value
            final(receipt)["repeat"][key] = value
        cases = {
            "failed receipt": lambda r: r.update(status="FAIL"),
            "wrong source": lambda r: r.update(source={"head": "b" * 40}),
            "untrusted preparation": lambda r: r.update(prepared_sha256="other"),
            "source drift": lambda r: r.update(after={}),
            "invented environment": lambda r: r.update(before={"invalid": True}, after={"invalid": True}),
            "late cohort": lambda r: r.update(elapsed_ns=120_000_000_001),
            "sleep time omitted": lambda r: r.update(boottime_elapsed_ns=120_000_000_001),
            "late final completion": lambda r: r.update(final_elapsed_ns=120_000_000_001),
            "missing wire": lambda r: r.update(wire={}),
            "missing owner": lambda r: r["children"].pop(),
            "extra owner": lambda r: r["children"].append(copy.deepcopy(child(r))),
            "missing launch ledger": lambda r: r.update(launch_ledger=[]),
            "hidden fourth launch": lambda r: r["launch_ledger"].append(copy.deepcopy(r["launch_ledger"][0])),
            "unavailable failed counts": lambda r: r["launch_ledger"][0].update(final_counters_available=False),
            "reused PID": lambda r: r["children"][1].update(pid=1),
            "invalid nonce": lambda r: child(r).update(nonce="0" * 64),
            "killed": lambda r: child(r).update(killed=True),
            "failed exit": lambda r: child(r).update(exit=-9),
            "boolean exit": lambda r: child(r).update(exit=False),
            "wrong binary": lambda r: child(r).update(binary_sha256="other"),
            "late stop": lambda r: child(r).update(stop_ns=90_000_000_001),
            "missing wait": lambda r: child(r).update(wait_ns=None),
            "late wait": lambda r: child(r).update(wait_ns=3000),
            "missing cleanup": lambda r: child(r)["records"].pop(5),
            "duplicate phase": lambda r: child(r)["records"].append(copy.deepcopy(final(r))),
            "different mode": lambda r: child(r)["records"][1].update(mode="oidc"),
            "wrong target": lambda r: child(r)["records"][1].update(goarch="386"),
            "two initializers": lambda r: child(r)["records"][1].update(initializers=2),
            "boolean initializer": lambda r: child(r)["records"][1].update(initializers=True),
            "probe failure": lambda r: child(r)["records"][2].update(ok=False),
            "App failure": lambda r: child(r)["records"][4].update(ok=False),
            "capability survived": lambda r: final(r).update(closed_refused=False),
            "unstable counts": lambda r: final(r)["repeat"].update(attempts=2),
            "missing counters": lambda r: final(r).update(counters={}),
            "zero anchor": lambda r: alter_counts(r, "installations", 0),
            "more installations": lambda r: alter_counts(r, "installations", 2),
            "boolean counter": lambda r: alter_counts(r, "attempts", True),
            "in flight": lambda r: alter_counts(r, "in_flight", True),
            "denied": lambda r: alter_counts(r, "denied", True),
            "failed owner": lambda r: alter_counts(r, "failed", True),
            "aggregate budget": lambda r: [row["records"][-1][field].update(attempts=5, writes=5) for row in r["children"] for field in ("counters", "repeat")],
            "phase time reversal": lambda r: child(r)["records"][3].update(received_ns=0),
        }
        for label, mutate in cases.items():
            with self.subTest(label):
                receipt = copy.deepcopy(self.receipt)
                mutate(receipt)
                self.raw(receipt)
                with self.assertRaises(ValueError):
                    verify(receipt, self.prepared, self.directory)

    def test_complete_raw_output_is_required(self):
        (self.directory / "child-1.stdout").write_bytes(b"changed")
        with self.assertRaises(ValueError):
            verify(self.receipt, self.prepared, self.directory)

    def test_json_rejects_duplicate_and_oversize_fields(self):
        for value in (b'{"event":"final","event":"identity"}', b" " * 65537):
            with self.assertRaises(ValueError):
                strict_json(value)
