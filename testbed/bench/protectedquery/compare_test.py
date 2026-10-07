import copy
import unittest

from compare import compare


def reports():
    sample = {"slot": 0, "status": "ok"}
    producer = {"offered": 1, "samples": [sample], "p99_ns": 1000}
    off = {"schema_version": 1, "qualification": "preparation_only", "passed": True,
           "action": "measure", "mode": "off", "transport": "verified_tls_http2",
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
              writer_actor="named_machine_token_all_data_role")
    return off, on


class ComparisonContract(unittest.TestCase):
    def test_matching_pair_keeps_final_acceptance_open(self):
        result = compare(*reports())
        self.assertEqual(result["final_performance_acceptance"], "not_qualified")
        self.assertEqual(result["measurements"]["internal_writer_lock_wait"], "not_measured")

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
