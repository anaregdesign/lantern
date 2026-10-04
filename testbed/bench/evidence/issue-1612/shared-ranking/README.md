# Shared ranking statistics: local component evidence (#1612)

Status: diagnostic measurements of staged source on Apple M3 Max, darwin/arm64,
on 2026-10-03. The production OIDC configuration remains guarded. These results
are not final merged-source, whole-wire OFF/OIDC, HA, provider, or physical-device
acceptance. No merge or deployment was performed.

## Implemented contract

Search and graph TF-IDF/BM25 reuse existing corpus statistics. Search candidates
are constrained before scoring/top-k; graph paths and PPR normalization/community
cuts exclude denied vertices/edges. Actual counts and degree aggregates remain
scoped. Private same-corpus changes may affect visible scores/order. Independent
GraphCache/index instances and native sys metadata do not join a corpus.

Per-scope ranking-statistics walks, caches, mutation journals, revision observers,
and their invalidation machinery were removed. The matching posting/admitted
bitmap intersection remains. Core receives generic immutable ranges/predicates;
Server interprets OIDC and Role policy. Server's compiled authorization-scope
cache and protected result/page caches remain distinct from ranking statistics.

## Method and source custody

Both fixtures contain 20,000 records: Search has 10,000 visible and 10,000 hidden
documents, with the same 100 matching visible documents in both compared paths.
Graph has a 10,000-edge visible chain and 10,000 private incoming edges. BFS
visits three hops; PPR uses top-10, alpha 0.2, epsilon 1e-3. Both use BM25.
Graph mutations occur away from the short seed path. Search updates alternate
payload lengths; Put/Delete workloads alternate a single identity.

`constrained=false` executes without a QueryView. `constrained=true` uses one
visible-prefix generic Core view. These labels do not represent end-to-end
OIDC OFF/ON. Policy compilation, token verification, HTTP, concurrent writers,
and full export/CDC are outside these component timings. Reused scope does not
warm a permission-statistics cache: none exists. First-scope iterations create
fresh immutable ranges and context over the same preindexed corpus. The separate
1x run records a first query of each independently built sub-benchmark fixture;
it is not process cold-start/index-build latency or a statistical latency gate.

The fixed matrix uses 1% (every 100 queries) and 100% (every query) mutation
cadences. Mutation time and allocations are included. All three 200 ms samples
are retained; tables report their median and full time range. GC thresholds were
unchanged. Explicit GC runs before/after, outside the timed loop, to measure a
signed process heap delta. Negative deltas are measurement noise, not savings;
this is neither isolated per-index retained memory nor peak RSS. B/op reports
allocation traffic, not retained capacity. Search's conservative index-retained
estimate and a deterministic 1,000-distinct-filter test complement these deltas.

The initial complete matrix finished before a traversal work-budget review fix:
rejected edges had been omitted from physical scan charging/cancellation polling.
The fix charges before filtering, leaves hidden paths excluded, and redacts
physical work counters from public authorized failures. Both PPR/community failure
contracts pass over real Connect. Only the affected traversal matrix was repeated;
all pre-repair samples remain in `workloads-before-budget-repair.txt`. Search
source was unchanged. The only Core manifest differences are
`graphcache/traversal.go` and `graphcache/query_view_test.go`.

Commands (run separately, without other CPU test workloads):

```sh
# Initial full component matrix, including still-valid Search samples:
(cd core && go test ./graphcache -run '^$' \
  -bench 'BenchmarkAuthorization(Search|Traversal)Workloads' -benchtime=200ms -count=3)
# Traversal repeat after the relevant hot-path repair:
(cd core && go test ./graphcache -run '^$' \
  -bench 'BenchmarkAuthorizationTraversalWorkloads' -benchtime=200ms -count=3)
# First request on final source:
(cd core && go test ./graphcache -run '^$' \
  -bench 'BenchmarkAuthorization(Search|Traversal)Workloads' -benchtime=1x -count=1)
```

Core file manifests were captured before each measured source and checked after
the final run. Parent HEAD is `2b9db94e00b48957afc1a084b8a61ce44379c35f`;
measurements include uncommitted implementation, so HEAD alone does not identify
these binaries. Exact file hashes, raw outputs, and checksum manifest are retained
alongside this report. No workload, GC threshold, or acceptance floor was changed
to obtain a passing sample.

## Search: all declared workloads

Time cells are median [min–max] in microseconds. Byte/allocation cells are medians.

| Workload | Unconstrained µs | Constrained µs | Unconstrained B/allocs | Constrained B/allocs |
| --- | ---: | ---: | ---: | ---: |
| `first_scope` | 69.552 [68.404–69.901] | 81.513 [80.909–83.128] | 3880/73 | 5336/84 |
| `reused_scope` | 67.998 [67.118–70.442] | 82.191 [81.144–84.406] | 3880/73 | 5176/80 |
| `visible_update_100` | 71.859 [70.434–72.412] | 86.821 [84.240–86.993] | 4062/74 | 5356/81 |
| `hidden_update_100` | 73.100 [73.000–73.720] | 85.864 [83.541–86.762] | 4126/74 | 5420/81 |
| `visible_update_1` | 83.588 [82.255–84.592] | 101.391 [96.558–101.794] | 22544/213 | 23840/220 |
| `hidden_update_1` | 93.130 [87.628–96.391] | 99.653 [99.409–99.802] | 28992/230 | 30288/237 |
| `visible_put_delete_100` | 70.525 [70.229–72.596] | 84.616 [83.943–85.752] | 3934/73 | 5230/80 |
| `hidden_put_delete_100` | 69.337 [68.996–71.224] | 81.411 [80.986–82.708] | 3955/73 | 5251/80 |
| `visible_put_delete_1` | 74.059 [71.826–75.010] | 85.529 [85.005–86.516] | 9311/105 | 10609/112 |
| `hidden_put_delete_1` | 73.448 [73.120–74.356] | 86.853 [85.521–87.364] | 11532/114 | 12827/121 |

## Graph BFS: all declared workloads

| Workload | Unconstrained µs | Constrained µs | Unconstrained B/allocs | Constrained B/allocs |
| --- | ---: | ---: | ---: | ---: |
| `first_scope` | 2.453 [2.411–2.468] | 2.638 [2.637–2.670] | 4280/33 | 4440/37 |
| `reused_scope` | 2.469 [2.407–2.471] | 2.546 [2.545–2.590] | 4280/33 | 4280/33 |
| `visible_put_delete_100` | 2.530 [2.502–2.547] | 2.591 [2.559–2.647] | 4284/33 | 4284/33 |
| `hidden_put_delete_100` | 2.482 [2.460–2.504] | 2.566 [2.558–2.599] | 4284/33 | 4284/33 |
| `visible_put_delete_1` | 2.939 [2.912–2.959] | 3.066 [3.033–3.073] | 4772/40 | 4772/40 |
| `hidden_put_delete_1` | 2.909 [2.908–2.919] | 3.009 [2.986–3.038] | 4772/40 | 4772/40 |
| `first_scope` | 2.453 [2.411–2.468] | 2.638 [2.637–2.670] | 4280/33 | 4440/37 |
| `reused_scope` | 2.469 [2.407–2.471] | 2.546 [2.545–2.590] | 4280/33 | 4280/33 |
| `visible_put_delete_100` | 2.530 [2.502–2.547] | 2.591 [2.559–2.647] | 4284/33 | 4284/33 |
| `hidden_put_delete_100` | 2.482 [2.460–2.504] | 2.566 [2.558–2.599] | 4284/33 | 4284/33 |
| `visible_put_delete_1` | 2.939 [2.912–2.959] | 3.066 [3.033–3.073] | 4772/40 | 4772/40 |
| `hidden_put_delete_1` | 2.909 [2.908–2.919] | 3.009 [2.986–3.038] | 4772/40 | 4772/40 |

## Graph PPR: all declared workloads

| Workload | Unconstrained µs | Constrained µs | Unconstrained B/allocs | Constrained B/allocs |
| --- | ---: | ---: | ---: | ---: |
| `first_scope` | 15.632 [15.471–15.716] | 17.231 [17.184–17.546] | 16248/92 | 17176/128 |
| `reused_scope` | 15.585 [15.547–15.611] | 17.164 [17.099–17.230] | 16248/92 | 17016/124 |
| `visible_put_delete_100` | 15.700 [15.680–15.734] | 17.785 [17.591–17.843] | 16252/92 | 17020/124 |
| `hidden_put_delete_100` | 15.802 [15.693–15.913] | 17.758 [17.525–17.866] | 16252/92 | 17020/124 |
| `visible_put_delete_1` | 16.282 [16.258–16.680] | 17.928 [17.895–17.932] | 16739/99 | 17507/131 |
| `hidden_put_delete_1` | 16.265 [16.240–16.392] | 17.759 [17.677–18.168] | 16739/99 | 17508/131 |
| `first_scope` | 15.632 [15.471–15.716] | 17.231 [17.184–17.546] | 16248/92 | 17176/128 |
| `reused_scope` | 15.585 [15.547–15.611] | 17.164 [17.099–17.230] | 16248/92 | 17016/124 |
| `visible_put_delete_100` | 15.700 [15.680–15.734] | 17.785 [17.591–17.843] | 16252/92 | 17020/124 |
| `hidden_put_delete_100` | 15.802 [15.693–15.913] | 17.758 [17.525–17.866] | 16252/92 | 17020/124 |
| `visible_put_delete_1` | 16.282 [16.258–16.680] | 17.928 [17.895–17.932] | 16739/99 | 17507/131 |
| `hidden_put_delete_1` | 16.265 [16.240–16.392] | 17.759 [17.677–18.168] | 16739/99 | 17508/131 |

## First-request observations

Single observations from the final 1x run; do not infer a relative speedup from
this order-sensitive sample. Only first_scope is shown here; all 44 sub-benchmark
first requests are preserved in `first-request-final.txt`.

| Family | Unconstrained µs | Constrained µs | Unconstrained B/allocs | Constrained B/allocs |
| --- | ---: | ---: | ---: | ---: |
| Search | 170.417 | 116.167 | 3880/73 | 5336/84 |
| BM25 BFS | 52.042 | 15.208 | 4288/33 | 4448/37 |
| BM25 PPR | 78.375 | 35.459 | 16248/92 | 17176/128 |

## Memory and interpretation

| Family | All-sample unconstrained retained delta B | All-sample constrained retained delta B |
| --- | ---: | ---: |
| Search | -3464…4448 | -3784…3640 |
| BM25 BFS | -3048…13296 | -3624…-1680 |
| BM25 PPR | -3288…-1536 | -3432…-1984 |

The shared Search index estimate was 7781312–7781992 bytes
(~7.42 MiB) across all samples; unconstrained and constrained read-only paths
both retained exactly 7,781,736 estimated index bytes. A test executes 1,000
independent candidate filters and verifies unchanged retained-index estimate and
generation. No per-filter ranking-statistics state survives a query.

For this sparse matched corpus, reused-scope Search costs ~14.2 µs and 1,296
allocated bytes more than the unconstrained path. Reused-scope BFS costs ~0.08 µs
with the same allocation traffic. Reused-scope PPR costs ~1.58 µs and 768 more
allocated bytes, including deterministic admitted-edge ordering. Fresh Core
scope construction adds 160 B and 4 allocations. Update cost remains ordinary
shared-index/edge maintenance rather than rebuilding a visible corpus. These
numbers do not predict broad postings, dense frontiers, concurrent lock delay,
or production token/lease overhead.

The earlier permission-statistics prototype had millisecond-scale first scans
and a warm-cache fast path. Its workload/output sets differed; it is not a valid
paired ratio against this matrix. Its warmed results do not justify restoring
those caches under the new contract.

## Verification and remaining acceptance

Core tests check shared scores against the same whole-corpus reference under OR,
AND, prefix/fuzzy, phrase, and top-k, plus update, Delete, query-time TTL purge,
compaction, restore, independent corpus isolation and distinct-filter retention.
Graph tests check shared structural DF/N/edge totals after Put/overwrite/Delete,
TTL GC and restore, while excluding hidden bridges and edge-denied endpoints.
Graph structural statistics keep existing bucket/GC semantics; search statistics
purge expired documents at the query liveness instant.

Real Connect tests retain scoped counts/scans/prefix deletes, full-vertex Search
projection, cursor ownership and Role-revision invalidation, actual Degree/
WeightedDegree, all three traversal families, denied seed/prefix, and sys-only
business-score/export isolation. The shared-statistics change does not loosen
CDC or receipt authorization; those unfinished features remain separate blockers
on the driving epic.

Targeted Core and real-Connect race checks pass, as do full root and Core
module tests. The initial full Server run was interrupted with SIGQUIT after
260.613 seconds: a stale prefix-count test fixture awaited an override that the
context-aware Backend path never called. #1619 tracks its test-only repair and
bounded probe wait. The repaired probe passes with the race detector. The failed
run/stack is preserved as `server-full-interrupted.txt`; the full Server-module
retry passes (`go test ./... -timeout=120s`, service package 30.440 seconds),
recorded separately in `server-full-repaired.txt`. All three affected Go modules
(root/Core/Server) pass their full suites; the benchmark YAML schema gate passes
in the root run. The final test-only repair does not affect benchmark source.
`workspace-source-final.sha256` identifies the full Go/proto working source.
Formatting and diff whitespace checks are clean. The all-language pre-push
gate has not been run, and no push was performed.

The schema-checked `shared_ranking.yaml` joins benchmark coverage for mixed
corpus mutations and BM25 BFS/PPR/community. It is diagnostic, with no release
floors, and has not been run on a production OIDC deployment. Final acceptance
still needs immutable final-source OFF/namespace-OFF/OIDC comparisons, real
wire/HA lifecycle and concurrency, broad matches/dense paths, capacity/peak
memory and lock-delay evidence, and final provider/device evidence. The epic's
merged-source, final-source, publication and human/device exits remain open.
