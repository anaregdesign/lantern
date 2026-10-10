import copy
import unittest


def shutdown_receipts(off, on):
    receipts=[]
    for index, report in enumerate((off,on),1):
        report["fixture_id"]=str(index)*32
        receipt={key:copy.deepcopy(report[key]) for key in ("mode","fixture_id","server_binary","exporter_binary","current_profile_binding")}
        receipt.update(schema_version=1,security_profile="current-v2",passed=True,
                       nodes=[{"node":number,"passed":True,"exit_code":0,"cycle":1,"floors_sha256":"1"*64,"state_sha256":"2"*64} for number in (1,2,3)])
        receipts.append(receipt)
    return receipts


from compare import compare


def reports():
    sample = {"slot": 0, "status": "ok"}
    producer = {"offered": 1, "samples": [sample], "p99_ns": 1000}
    off = {"schema_version": 1, "qualification": "preparation_only", "passed": True,
           "action": "measure", "mode": "off", "security_profile": "legacy-v1",
           "server_processes": 1, "comparison_axis": "end_to_end_mode_specific_authorized_results",
           "expected_search_keys": ["bench:ranking:a", "bench:ranking:b", "bench:private:best"], "transport": "verified_tls_http2",
           "reader_actor": "unauthenticated_off", "writer_actor": "unauthenticated_off",
           "driver_source": {"revision": "a" * 40, "modified": "false", "sha256": "d" * 64},
           "server_binary": {"revision": "a" * 40, "modified": "false", "sha256": "b" * 64},
           "topology": "standalone_broad_illuminate_with_hidden_bridge", "corpus_sha256": "c" * 64,
           "measurements": {"internal_writer_lock_wait": "not_measured"},
           "load": {"offered_load": {"family": "search", "phase": "update-mixed", "query_count": 1,
                                     "query_rps": 1, "writer_rps": 1}, "interval": "rpc_and_validation",
                    "reader": copy.deepcopy(producer), "writer": copy.deepcopy(producer)}}
    on = copy.deepcopy(off)
    on.update(mode="oidc", reader_actor="synthetic_local_end_user_bearer_jwt_role_bound",
              writer_actor="named_machine_token_all_data_role",
              expected_search_keys=["bench:ranking:a", "bench:ranking:b"])
    return off, on


class ComparisonContract(unittest.TestCase):
    def test_matching_pair_keeps_final_acceptance_open(self):
        result = compare(*reports())
        self.assertEqual(result["final_performance_acceptance"], "not_qualified")
        self.assertEqual(result["measurements"]["internal_writer_lock_wait"], "not_measured")

    def test_current_pair_preserves_different_expected_sets(self):
        off, on = reports()
        for report in (off, on):
            report.update(security_profile="current-v2", server_processes=3,
                          exporter_binary={"revision":"a"*40,"modified":"false","sha256":"e"*64},
                          current_profile_binding="")
        on["current_profile_binding"]="current-v2:"+"f"*64
        receipts=shutdown_receipts(off,on)
        self.assertTrue(compare(off, on,*receipts)["matched"])
        with self.assertRaises(ValueError): compare(off,on)
        for edit in (
            lambda r:r.update(passed=False),lambda r:r.update(fixture_id="9"*32),
            lambda r:r.update(nodes=[]),lambda r:r["nodes"][0].update(exit_code=1),
            lambda r:r["nodes"][0].update(cycle=2),lambda r:r["nodes"][0].update(state_sha256=""),
            lambda r:r["nodes"][0].update(failure="shutdown_deadline"),
        ):
            bad=copy.deepcopy(receipts[1]);edit(bad)
            with self.assertRaises(ValueError):compare(off,on,receipts[0],bad)
        for edit in (
            lambda r: r.update(security_profile="legacy-v1"),
            lambda r: r.update(security_profile=""),
            lambda r: r.update(current_profile_binding=""),
            lambda r: r.update(server_processes=1),
            lambda r: r.update(expected_search_keys=[]),
            lambda r: r.update(expected_search_keys=off["expected_search_keys"]),
            lambda r: r.update(comparison_axis="pure_authorization_overhead"),
            lambda r: r["exporter_binary"].update(modified="true"),
            lambda r: r["load"]["reader"].update(offered=0,samples=[]),
        ):
            bad=copy.deepcopy(on); edit(bad)
            with self.assertRaises(ValueError): compare(off,bad,*receipts)

    def test_rejects_mismatched_source_actor_tls_corpus_load_failure(self):
        for mutation in (
            lambda r: r.update(transport="plaintext"),
            lambda r: r.update(reader_actor="machine_token"),
            lambda r: r["driver_source"].update(modified="true"),
            lambda r: r.update(corpus_sha256="d" * 64),
            lambda r: r["load"]["offered_load"].update(query_rps=2),
            lambda r: r["load"]["writer"].update(samples=[]),
            lambda r: r["load"]["reader"]["samples"][0].update(status="producer_saturated"),
            lambda r: r.update(passed=False),
        ):
            with self.subTest(mutation=mutation):
                off, on = reports()
                mutation(on)
                with self.assertRaises(ValueError):
                    compare(off, on)


if __name__ == "__main__":
    unittest.main()
