# Head-managed candidate shared-ranking measurements (#1610 / #1612)

Local candidate component diagnostic at `6323e91ef1fe323842239c2c8f6c7ff65738b3a8` on darwin/arm64. Source tree `ca9668de47e34c4f8295f74aaffe7c75e80ec7a1`. All 64 processes and 176 predeclared observations completed. This is candidate source, not final merged source, production OIDC OFF/ON, stable-clock HA, external-provider or physical-device qualification. No merge, deployment or publication occurred.

The existing unchanged Search/BFS/PPR/Community benchmark fixtures reuse corpus DF/N/document-length statistics. Search has 10,000 visible and 10,000 hidden documents; Graph has a 10,000-edge visible chain and 10,000 private incoming edges. Generic constrained views filter candidates before top-k and exclude hidden exploration paths. The compared paths may visit different graphs and yield different final scores, so faster constrained observations do not demonstrate a universal authorization speedup.

Eight first-query observations each use a fresh process and one operation. Warm/mixed cases use one fresh process per workload/scope, preserving all three 200ms samples. The existing setup and retained-heap measurements call GC outside the timed loop; no measured-loop collection, threshold change, permission-statistics cache or sample selection was added. Visible/hidden updates and Put/Delete at 1% and 100% retain the exact existing workloads. Index construction and policy interpretation/token verification/HTTP are outside the timed operation.

One immutable compiled test binary served the entire matrix: SHA-256 `9b7aa89b8f30ad962a449f701e7cd4844f07245d664fb78a02d26ff9f8546c52`. Core source digest `fbd75e3116d957db18bff5482c67876cd5ab45b1a1ce9352d8e4e024252de090`. Exact commands, environment defaults and every exit/observation count are in `provenance.json`; every raw observation and `/usr/bin/time -l` RSS report is preserved. Maximum RSS includes fixture setup and all samples in a process. Signed retained-heap deltas are GC-sampling noise, not isolated per-policy retained memory. `B/op` measures allocation traffic.

Owned validation fixtures and CPU test/build jobs were stopped. Family preflights captured shared-host process CPU totals of 336–504% and one-minute load averages 5.87–8.17; user-owned background workloads remained running. These measurements are useful diagnostics with recorded contention, not production capacity/latency acceptance or a universal overhead percentage. No additional run was selected to improve them.

| Family / workload | Unconstrained median µs | Constrained median µs | Unconstrained max RSS MiB | Constrained max RSS MiB | Unconstrained B/op | Constrained B/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `first-search` | 179.875 | 174.458 | 48.750 | 48.797 | 3880 | 5368 |
| `search/first_scope` | 70.357 | 83.699 | 50.172 | 49.375 | 3880 | 5368 |
| `search/reused_scope` | 68.327 | 82.939 | 51.234 | 50.344 | 3880 | 5176 |
| `search/visible_update_100` | 69.663 | 86.031 | 50.359 | 49.047 | 4063 | 5359 |
| `search/hidden_update_100` | 69.631 | 85.286 | 50.312 | 49.516 | 4126 | 5420 |
| `search/visible_update_1` | 82.167 | 97.619 | 50.219 | 50.703 | 22541 | 23837 |
| `search/hidden_update_1` | 85.183 | 99.645 | 50.672 | 50.828 | 28992 | 30288 |
| `search/visible_put_delete_100` | 69.159 | 83.771 | 49.781 | 50.594 | 3934 | 5230 |
| `search/hidden_put_delete_100` | 70.937 | 85.532 | 49.797 | 49.906 | 3957 | 5254 |
| `search/visible_put_delete_1` | 75.328 | 89.912 | 49.797 | 49.500 | 9313 | 10607 |
| `search/hidden_put_delete_1` | 76.850 | 92.043 | 50.281 | 50.406 | 11532 | 12827 |
| `first-bfs` | 50.709 | 35.917 | 39.188 | 38.906 | 4288 | 4480 |
| `bfs/first_scope` | 2.657 | 2.869 | 67.391 | 67.547 | 4280 | 4472 |
| `bfs/reused_scope` | 2.633 | 2.648 | 67.422 | 66.484 | 4280 | 4280 |
| `bfs/visible_put_delete_100` | 2.551 | 2.769 | 66.484 | 66.844 | 4284 | 4284 |
| `bfs/hidden_put_delete_100` | 2.645 | 2.738 | 66.562 | 66.922 | 4284 | 4284 |
| `bfs/visible_put_delete_1` | 3.114 | 3.187 | 66.375 | 66.531 | 4772 | 4772 |
| `bfs/hidden_put_delete_1` | 3.022 | 3.179 | 66.469 | 66.859 | 4772 | 4772 |
| `first-ppr` | 77.875 | 84.584 | 38.594 | 39.359 | 16248 | 17208 |
| `ppr/first_scope` | 16.443 | 18.105 | 65.562 | 65.484 | 16248 | 17208 |
| `ppr/reused_scope` | 16.354 | 17.592 | 66.594 | 65.234 | 16248 | 17016 |
| `ppr/visible_put_delete_100` | 16.483 | 18.020 | 66.312 | 65.906 | 16252 | 17020 |
| `ppr/hidden_put_delete_100` | 16.428 | 18.100 | 65.859 | 66.438 | 16253 | 17020 |
| `ppr/visible_put_delete_1` | 17.376 | 18.772 | 65.438 | 65.719 | 16740 | 17508 |
| `ppr/hidden_put_delete_1` | 17.410 | 18.496 | 65.766 | 65.750 | 16740 | 17508 |
| `first-community` | 89.667 | 102.709 | 39.297 | 39.500 | 28456 | 30216 |
| `community/first_scope` | 37.695 | 32.420 | 65.562 | 65.641 | 28456 | 30216 |
| `community/reused_scope` | 36.974 | 32.602 | 65.469 | 66.000 | 28456 | 30024 |
| `community/visible_put_delete_100` | 37.879 | 32.983 | 66.219 | 65.547 | 28461 | 30028 |
| `community/hidden_put_delete_100` | 37.504 | 32.601 | 66.219 | 66.016 | 28460 | 30029 |
| `community/visible_put_delete_1` | 37.094 | 33.448 | 65.891 | 66.297 | 28948 | 30516 |
| `community/hidden_put_delete_1` | 37.815 | 34.893 | 65.562 | 65.859 | 28948 | 30515 |

The first-query rows have one observation per scope; they have no statistical speedup interpretation. Search's retained index estimate is identical for the two scopes at 7,781,736 bytes in the stable fixture; Put/Delete changes it only with the actual corpus. The constrained reused Search operation allocates 5,176 B versus 3,880 B, while BFS allocates 4,280 B on both paths. PPR/Community retain the generic view/filter overhead and authorized induced-graph behavior. No Role-specific DF/N/length copies or ranking-statistics invalidation structures are maintained.

Required access-control/count/TTL/delete/restore correctness remains covered by the complete Core/Server and real-wire suites, separately from these timings. This experiment does not measure concurrent writer-lock delay, renewal/revocation bounds, provider latency, a whole-Server unsaturated/capacity pair, or physical-device memory. Those final acceptance exits remain explicit in #1599/#1610.
