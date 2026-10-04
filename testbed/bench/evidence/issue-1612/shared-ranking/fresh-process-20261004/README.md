# Fresh-process and update-mixed shared-ranking diagnostic (#1612)

Local dirty-branch component diagnostic on darwin/arm64. Parent HEAD `afcf4a57`; exact Core source and test-binary hashes are in `provenance.json`. This is not production OFF/OIDC, final source, CI, provider or device qualification. The shared host retains the user's unrelated development containers; no other owned CPU test workload ran during this matrix. A contemporaneous host-load snapshot was not captured, so no capacity or latency acceptance conclusion is drawn.

Each first query ran in a new process with one 20,000-record indexed fixture. Warm/update cases used a separate process per scope/workload, retaining all three 200ms samples. Search candidates and Graph paths/counts remain scoped; ranking DF/N/length sums reuse the same corpus. No GC runs inside measured loops. Existing benchmark setup/retained-heap sampling calls GC outside the loop; process maximum RSS includes setup and all three samples. This is not isolated per-policy memory.

| Family / workload | Unconstrained median µs | Constrained median µs | Unconstrained max RSS MiB | Constrained max RSS MiB |
| --- | ---: | ---: | ---: | ---: |
| `first-search` | 177.000 | 186.583 | 48.266 | 48.625 |
| `first-bfs` | 54.667 | 32.917 | 39.438 | 39.016 |
| `first-ppr` | 73.458 | 68.750 | 39.484 | 39.469 |
| `first-community` | 104.042 | 94.083 | 39.016 | 39.016 |
| `search-reused_scope` | 71.407 | 84.481 | 49.047 | 49.875 |
| `bfs-reused_scope` | 2.626 | 2.829 | 66.016 | 67.188 |
| `ppr-reused_scope` | 16.575 | 18.203 | 65.906 | 65.922 |
| `community-reused_scope` | 37.012 | 33.096 | 65.516 | 65.844 |
| `search-hidden_update_1` | 88.351 | 101.036 | 49.953 | 50.750 |
| `community-hidden_put_delete_1` | 38.541 | 34.235 | 66.109 | 65.734 |

First-query rows contain one observation per scope and have no statistical speedup interpretation. All visible/hidden updates and Put/Delete cadences (1% and 100%) remain in the raw files. Scope construction creates immutable ranges, not a ranking-statistics cache.

The staging constructor comparison retained zero allocations for Vertex Get/Put and unchanged 56 B / two allocations for Edge weight lookup. Median Vertex Get was 44.62 → 47.93 ns; Put 144.1 → 143.4 ns; weight lookup 180.0 → 180.9 ns. These small component observations do not qualify contention or production transport. Atomic Create followed by Delete was median 1.310 µs, 2,881 B and 29 allocations; it excludes Server policy, WAL, receipt storage and HTTP.

The first and warm/mixed families share one compiled test binary. Exact invocations and all sample outcomes are recorded in `provenance.json`; `/usr/bin/time -l` logs provide peak RSS. No workload, GC threshold, permission-statistics cache or floor was changed to obtain a pass.
