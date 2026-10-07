#!/usr/bin/env python3
"""Fail closed on mismatched OFF/ON preparation reports; no final perf verdict."""
import argparse
import hashlib
import json
import re


def compare(off, on):
    for report, mode, reader, writer in (
        (off, "off", "unauthenticated_off", "unauthenticated_off"),
        (on, "oidc", "synthetic_local_end_user_bearer_jwt_role_bound", "named_machine_token_all_data_role"),
    ):
        if (report.get("schema_version") != 1 or report.get("qualification") != "preparation_only"
                or report.get("action") != "measure" or report.get("mode") != mode
                or report.get("passed") is not True or report.get("transport") != "verified_tls_http2"
                or report.get("topology") != "standalone_broad_illuminate_with_hidden_bridge"
                or report.get("reader_actor") != reader or report.get("writer_actor") != writer):
            raise ValueError("unqualified preparation report or actor/transport mismatch")
        source, binary = report.get("driver_source", {}), report.get("server_binary", {})
        if (not re.fullmatch(r"[0-9a-f]{40}", source.get("revision", ""))
                or source.get("modified") != "false" or binary.get("modified") != "false"
                or not re.fullmatch(r"[0-9a-f]{64}", source.get("sha256", ""))
                or binary.get("revision") != source["revision"]
                or not re.fullmatch(r"[0-9a-f]{64}", binary.get("sha256", ""))):
            raise ValueError("matching immutable source/binary provenance required")
        load = report.get("load", {})
        config = load.get("offered_load", {})
        if (config.get("family") not in ("search", "bfs", "ppr", "community")
                or config.get("phase") not in ("first", "warm", "update-mixed")):
            raise ValueError("unknown family or phase")
        for name in ("reader", "writer"):
            producer = load.get(name, {})
            samples = producer.get("samples") or []
            if (producer.get("offered") != len(samples)
                    or any(sample.get("slot") != slot or sample.get("status") != "ok"
                           for slot, sample in enumerate(samples))):
                raise ValueError("missing offered slot, failure or saturation")
        if load["reader"]["offered"] != config.get("query_count"):
            raise ValueError("query offered count mismatch")
        count, rps, writer_rps = config["query_count"], config.get("query_rps"), config.get("writer_rps")
        if (type(count) is not int or count < 1 or type(rps) is not int or rps < 1
                or type(writer_rps) is not int or writer_rps < 0):
            raise ValueError("invalid offered load")
        expected_writer = (count * writer_rps + rps - 1) // rps
        if load["writer"]["offered"] != expected_writer:
            raise ValueError("independent writer offered count mismatch")
        if ((config["phase"] == "update-mixed") != (writer_rps > 0)
                or config["phase"] == "first" and count != 1):
            raise ValueError("phase offered load mismatch")
    for key in ("topology", "corpus_sha256", "driver_source", "server_binary", "measurements"):
        if off.get(key) != on.get(key):
            raise ValueError("OFF/ON comparison input mismatch: " + key)
    if not re.fullmatch(r"[0-9a-f]{64}", off.get("corpus_sha256", "")):
        raise ValueError("logical corpus not pinned")
    for key in ("offered_load", "interval"):
        if off["load"].get(key) != on["load"].get(key):
            raise ValueError("OFF/ON load/interval mismatch")
    return {"schema_version": 1, "qualification": "preparation_pair_only", "matched": True,
            "source": off["driver_source"]["revision"], "corpus_sha256": off["corpus_sha256"],
            "offered_load": off["load"]["offered_load"],
            "reader": {"off_p99_ns": off["load"]["reader"]["p99_ns"], "on_p99_ns": on["load"]["reader"]["p99_ns"]},
            "writer_rpc": {"off_p99_ns": off["load"]["writer"]["p99_ns"], "on_p99_ns": on["load"]["writer"]["p99_ns"]},
            "measurements": off["measurements"], "final_performance_acceptance": "not_qualified"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("off")
    parser.add_argument("on")
    args = parser.parse_args()
    reports, digests = [], []
    for path in (args.off, args.on):
        with open(path, "rb") as file:
            raw = file.read(32 * 1024 * 1024 + 1)
        if len(raw) > 32 * 1024 * 1024:
            parser.error("report exceeds bounded input")
        reports.append(json.loads(raw))
        digests.append(hashlib.sha256(raw).hexdigest())
    try:
        result = compare(*reports)
    except (KeyError, TypeError, ValueError) as error:
        parser.error(str(error))
    result["raw_report_sha256"] = digests
    print(json.dumps(result, sort_keys=True))


if __name__ == "__main__":
    main()
