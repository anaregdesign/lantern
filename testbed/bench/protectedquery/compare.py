#!/usr/bin/env python3
"""Fail closed on mismatched OFF/ON preparation reports; no final perf verdict."""
import argparse
import hashlib
import json
import re


def compare(off, on, off_shutdown=None, on_shutdown=None):
    profile = on.get("security_profile")
    if profile not in ("legacy-v1", "current-v2") or off.get("security_profile") != profile:
        raise ValueError("explicit matching security profile required")
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
        expected = ["bench:ranking:a", "bench:ranking:b"] + (["bench:private:best"] if mode == "off" else [])
        if (report.get("comparison_axis") != "end_to_end_mode_specific_authorized_results"
                or report.get("expected_search_keys") != expected
                or report.get("server_processes") != (3 if profile == "current-v2" else 1)):
            raise ValueError("mode-specific result contract or process layout mismatch")
        binding = report.get("current_profile_binding", "")
        if profile == "current-v2" and mode == "oidc":
            if not re.fullmatch(r"current-v2:[0-9a-f]{64}", binding):
                raise ValueError("current ON requires full profile binding")
        elif binding:
            raise ValueError("OFF/legacy cannot claim current authority")
        source, binary = report.get("driver_source", {}), report.get("server_binary", {})
        if (not re.fullmatch(r"[0-9a-f]{40}", source.get("revision", ""))
                or source.get("modified") != "false" or binary.get("modified") != "false"
                or not re.fullmatch(r"[0-9a-f]{64}", source.get("sha256", ""))
                or binary.get("revision") != source["revision"]
                or not re.fullmatch(r"[0-9a-f]{64}", binary.get("sha256", ""))):
            raise ValueError("matching immutable source/binary provenance required")
        if profile == "current-v2":
            validate_shutdown(report, off_shutdown if mode == "off" else on_shutdown)
            exporter = report.get("exporter_binary", {})
            if (exporter.get("revision") != source["revision"] or exporter.get("modified") != "false"
                    or not re.fullmatch(r"[0-9a-f]{64}", exporter.get("sha256", ""))):
                raise ValueError("matching immutable current exporter provenance required")
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
    for key in ("topology", "corpus_sha256", "driver_source", "server_binary", "exporter_binary", "measurements"):
        if off.get(key) != on.get(key):
            raise ValueError("OFF/ON comparison input mismatch: " + key)
    if not re.fullmatch(r"[0-9a-f]{64}", off.get("corpus_sha256", "")):
        raise ValueError("logical corpus not pinned")
    for key in ("offered_load", "interval"):
        if off["load"].get(key) != on["load"].get(key):
            raise ValueError("OFF/ON load/interval mismatch")
    return {"schema_version": 1, "qualification": "preparation_pair_only", "matched": True,
            "security_profile": profile, "comparison_axis": "end_to_end_mode_specific_authorized_results",
            "source": off["driver_source"]["revision"], "corpus_sha256": off["corpus_sha256"],
            "offered_load": off["load"]["offered_load"],
            "reader": {"off_p99_ns": off["load"]["reader"]["p99_ns"], "on_p99_ns": on["load"]["reader"]["p99_ns"]},
            "writer_rpc": {"off_p99_ns": off["load"]["writer"]["p99_ns"], "on_p99_ns": on["load"]["writer"]["p99_ns"]},
            "measurements": off["measurements"], "final_performance_acceptance": "not_qualified"}


def validate_shutdown(report, receipt):
    if (not isinstance(receipt, dict) or receipt.get("schema_version") != 1
            or receipt.get("security_profile") != "current-v2" or receipt.get("passed") is not True
            or receipt.get("mode") != report["mode"]
            or not re.fullmatch(r"[0-9a-f]{32}", report.get("fixture_id", ""))
            or receipt.get("fixture_id") != report["fixture_id"]
            or receipt.get("server_binary") != report["server_binary"]
            or receipt.get("exporter_binary") != report.get("exporter_binary")
            or receipt.get("current_profile_binding") != report.get("current_profile_binding")):
        raise ValueError("matching completed current fixture shutdown required")
    nodes = receipt.get("nodes")
    if not isinstance(nodes, list) or len(nodes) != 3:
        raise ValueError("three owned current product exits required")
    for number, node in enumerate(nodes, 1):
        if (node.get("node") != number or node.get("passed") is not True
                or node.get("exit_code") != 0 or node.get("failure")):
            raise ValueError("failed or incomplete current child shutdown")
        if report["mode"] == "oidc" and (node.get("cycle") != 1
                or not re.fullmatch(r"[0-9a-f]{64}", node.get("floors_sha256", ""))
                or not re.fullmatch(r"[0-9a-f]{64}", node.get("state_sha256", ""))):
            raise ValueError("missing original fresh CLEAN custody observation")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("off")
    parser.add_argument("on")
    parser.add_argument("--off-shutdown", help="matching current OFF query-shutdown.json")
    parser.add_argument("--on-shutdown", help="matching current OIDC query-shutdown.json")
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
        shutdowns=[]
        for path in (args.off_shutdown,args.on_shutdown):
            if path is None:
                shutdowns.append(None)
            else:
                with open(path,"rb") as file:
                    raw=file.read(64*1024+1)
                if len(raw)>64*1024: raise ValueError("shutdown receipt exceeds bound")
                shutdowns.append(json.loads(raw))
                digests.append(hashlib.sha256(raw).hexdigest())
        result = compare(*reports,*shutdowns)
    except (KeyError, TypeError, ValueError, OSError) as error:
        parser.error(str(error))
    result["raw_report_sha256"] = digests[:2]
    if len(digests)>2: result["shutdown_receipt_sha256"] = digests[2:]
    print(json.dumps(result, sort_keys=True))


if __name__ == "__main__":
    main()
