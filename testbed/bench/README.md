# lantern bench harness (`testbed/bench/`)

Reusable performance + memory-leak harness for the HA `docker compose`
cluster (see `deploy/compose/`). Drives the cluster with [`ghz`][ghz] or a
narrow scenario-owned Connect/h2c driver, captures Prometheus range queries +
Go pprof snapshots, applies per-scenario leak / lifecycle-metric / semantic /
producer-performance gates, and renders a Markdown report.

> **PR CI does not run the wall-clock scenarios.** The default PR pipeline
> runs their schema/contract tests, but not a Compose load run. On a root
> `vX.Y.Z` tag, however, `docker-publish.yml` runs the short
> `search_qualification` scenario plus targeted real-h2c Search tests as a
> blocking qualification. A failed or skipped stage prevents image and GitHub
> Release publication. Its bounded artifact records the tag, full commit SHA,
> every stage status, and the scenario report.
>
> **Release CI runs a compact canonical sweep.** On every `vX.Y.Z` tag
> push, the `Release` workflow runs `./testbed/bench/release.sh` — a
> driver that sweeps the scenarios listed in `release-scenarios.txt`. Each
> scenario owns a fresh Compose lifecycle so data, retained high-water state,
> and scenario-specific cluster env cannot leak into its successor (#1097). To
> keep wall-time bounded without losing coverage, the sweep is seven
> scenarios: three fan-out scenarios (`broad_rw`, `broad_mutate`,
> `broad_illuminate`) that
> exercise many call paths inside a single steady window (read / write /
> batch / scan / edge / delete / ScanVertexKeys / CountVerticesByPrefix /
> idempotent AddEdge, and the Illuminate algorithm/reduction × objective ×
> weighting axes) plus `ttl_churn`, `many_subscribers`, `search_churn` (index decay
> / thread-safety under churn, #703), and `replication_apply_churn`
> (vertexHLC map regression gate, #700 / #705). It produces a
> fixed-format `bench-report.md` that is spliced into the GitHub Release
> notes. This full profiling sweep remains `continue-on-error`, so a noisy
> runner cannot block a release; it is separate from the short blocking Search
> qualification above. See issues [#256], [#262], [#573], [#708], [#1063],
> [#1097].
>
> **Receipt qualification is nightly-only.** `bench-nightly.yml` runs
> `receipt_admission_lookup` after the canonical sweep as a separate blocking
> fresh-cluster gate. It is intentionally absent from
> `release-scenarios.txt`: five local runs on the synthetic-parent stack
> support provisional thresholds, **not final merged-stack evidence**.
> Fresh measurements after #1440 and the receipt-family/SDK merges, plus
> cross-runner nightly stability, are required before this durable-WAL
> workload can become a release blocker ([#1399]).

[#256]: https://github.com/anaregdesign/lantern/issues/256
[#262]: https://github.com/anaregdesign/lantern/issues/262
[#573]: https://github.com/anaregdesign/lantern/issues/573
[#708]: https://github.com/anaregdesign/lantern/issues/708
[#1063]: https://github.com/anaregdesign/lantern/issues/1063
[#1097]: https://github.com/anaregdesign/lantern/issues/1097
[#1399]: https://github.com/anaregdesign/lantern/issues/1399
[#1442]: https://github.com/anaregdesign/lantern/issues/1442

## Prerequisites

- Docker + `docker compose` v2
- [`ghz`][ghz] ≥ 0.117 — drives default scenarios over gRPC wire frames. The Lantern
  server is Connect-only (see [#335][i335]), but the Connect-Go handlers
  accept gRPC, gRPC-Web, and Connect on the same h2c socket, and
  `connectrpc.com/grpcreflect` exposes the standard
  `grpc.reflection.v1*` service the harness uses for descriptor
  discovery. ghz keeps working unchanged. See [#383][i383] for the
  verification log. The receipt scenario uses its dedicated Go driver instead.
- [`yq`][yq] v4 (Go reimplementation)
- `jq`, `curl`, `bash` ≥ 4
- Go (matching `go.mod` toolchain) — used to build the report renderer

[i335]: https://github.com/anaregdesign/lantern/issues/335
[i383]: https://github.com/anaregdesign/lantern/issues/383

## Quick start

```bash
# Run any scenario:
./testbed/bench/run.sh write_heavy

# Keep the cluster up after the run (useful for ad-hoc poking):
KEEP_UP=1 ./testbed/bench/run.sh read_heavy

# Reuse an already-running cluster (skip compose up / down):
SKIP_UP=1 KEEP_UP=1 ./testbed/bench/run.sh mixed_rw

# Also capture a 30s CPU profile per replica after the steady phase:
PPROF_CPU=1 ./testbed/bench/run.sh addedge_contention

# Exercise durable Edge Delete receipt admission plus exact-ID lookup:
LANTERN_IMAGE=lantern:local ./testbed/bench/run.sh receipt_admission_lookup
```

The exit code folds together the leak gate and any declared metric, semantic,
and perf gates (`0` = all pass, `1` = at least one failed). Unless `KEEP_UP=1`,
teardown of the named Compose project must also succeed; a teardown failure
disqualifies the run without masking an earlier gate failure. Run artifacts
are written under `testbed/bench/out/<scenario>/<UTC-timestamp>/`.

## Host-only GC and periodic-backup stress (#1183)

`gc_stress.sh` and `backup_stress.sh` are opt-in measurements outside the
Compose release sweep. They seed an in-process 100,000-vertex, approximately
3.2-million-edge graph on the host, because a container VM with less than that
working-set capacity would change the result. Both scripts require committed
source and record its exact SHA, Go version, CPU/RAM, container-VM memory (when
available), scale, intervals, and offered rates in `host.txt`. They write
content-free timing/count artifacts under ignored `testbed/bench/out/`.
There is currently no running Lantern deployment, so these runs establish
synthetic signals only; observed deployment scale, production latency, and
operator RPO remain unavailable.

```bash
./testbed/bench/gc_stress.sh
./testbed/bench/backup_stress.sh
```

The GC script runs three repetitions of at least 100 direct `GraphCache.Watch`
ticks per configuration: uniform degree 32 or a skewed graph with one
100,000-head tail, crossed with a full sweep or a 5,000-tail budget. Uniform
seeds 3,200,000 edges; skew seeds 3,199,969 because the hub has 100,000
heads and each other tail has 31. This exposes how a tail budget handles one
high-degree tail at nearly identical total edge count. Five expiry bands add
live-edge contributions and short-only buckets; two vertex-deletion bands
create dangling edges. The JSON retains every timestamped raw tick duration,
scanned tail/edge count, expired contributions (including those compacted in
still-live buckets), zero/dangling bucket removals, backlog, and a sampled
expiry-to-physical-reclamation lag. `report.md` recomputes `gc_p99` from the
raw ticks, separately for every run. The script also keeps ten target-scale
single-flush benchmark repetitions and `benchstat.txt`; those benchmarks are
not substitutes for a tick p99. `benchstat` must be installed in `PATH`.

The backup script drives the actual periodic `Backupper.Run` for four
30-second intervals while named `GetVertex`, `ScanVertices`, and `Illuminate`
read producers each offer 200 calls/s, and `PutVertex` offers 50 calls/s, over
the real Connect/h2c path. The JSON records each completed periodic tick's
start/end and materialization/send/finalization durations and every call's
start/end, latency, and success status. Each tick is paired with equal-length
before/during/after windows per producer. A comparison requires at least 100
successful calls and no errors in each window; missing data yields `unknown`,
and a failed periodic attempt breaks a three-tick streak.
The synthetic read trigger fires only if one named read producer's during p99
exceeds twice its matched before p99 for three consecutive completed ticks.
The local results do not establish an operator RPO or deletion-survives-restore
requirement and cannot by themselves justify WAL.

The environment variables `GC_VERTICES`, `GC_DEGREE`, `GC_TICKS`,
`GC_INTERVAL_MS`, `GC_REPETITIONS`, `GC_SHAPES`, `GC_BUDGETS`, and the
optional `GC_RESOURCE_TRACE=1` (compile the test once, then capture per-run
stress-process CPU/maximum-RSS via `/usr/bin/time`), plus the
corresponding `BACKUP_VERTICES`, `BACKUP_DEGREE`, `BACKUP_INTERVAL_MS`,
`BACKUP_READ_RPS`, `BACKUP_WRITE_RPS` override script defaults for a
predeclared run or smoke check. If target-scale setup exceeds host resources,
record that limit explicitly; do not treat a smaller run as equivalent.

## Mixed edge reset/Add microbench (#1203)

The content-free [raw baseline](evidence/issue-1203/base.txt) and
[candidate](evidence/issue-1203/candidate.txt) outputs were captured on an
Apple M3 Max (`darwin/arm64`, one benchmark CPU, three 500ms repetitions),
comparing main `40b5127` with the #1203 candidate `5b0f8a4`. They are
exploratory host measurements, not a release floor or production RSS sample.

| Case | Baseline median | Candidate median | Baseline B/op | Candidate B/op |
| --- | ---: | ---: | ---: | ---: |
| Local `AddEdge`, 1k vertices | 316.8 ns | 316.8 ns | 233 | 312 |
| Local `AddEdge`, 10k vertices | 427.8 ns | 443.8 ns | 265 | 400 |
| Local `AddEdge`, 100k vertices | 601.9 ns | 644.7 ns | 322 | 432 |
| Existing hot edge `AddEdge` | 283.4 ns | 266.6 ns | 348 | 504 |
| Causal Add batch of 64 | — | 5.566 µs | — | 12,256 |
| Replication Snapshot, 1k edges × (Put + 3 Adds) | — | 918.2 µs | — | 1,248,440 |

The local Add allocation count stayed at two per operation; the HLC stored
with every contribution increased allocation bytes. One HLC occupies 32 bytes,
so 3.2 million retained single-contribution edges add about 102 MB of row
payload before slice-capacity and allocator effects. The three samples do not
establish a significant latency change. The on-demand
[`mixed_edge_reset_add.yaml`](scenarios/mixed_edge_reset_add.yaml) scenario
exercises the same operation mix over three replicas and is intentionally not
in the short release sweep until a stable post-change baseline exists.

## Receipt admission and lookup gate (#1442, preparatory for #1399)

[`receipt_admission_lookup.yaml`](scenarios/receipt_admission_lookup.yaml)
offers 100 operation pairs/s across three replicas. Every pair performs a
receipt-bearing `DeleteEdge` followed immediately by `GetReceiptStatus` on the
same endpoint with the exact operation ID. The lookup must return `CONFIRMED`,
the matching operation/call IDs, intent digest, deadline and item coordinates,
and the presence-preserving original result `delete_edge_existed=false`.
Semantic mismatches become non-OK producer outcomes and fail the run.

ghz cannot safely generate one canonical binary operation ID and reuse it in
another producer, so this scenario selects the allow-listed
`receipt_edge_delete` driver. It still emits the ghz summary shape consumed by
the existing perf evaluator and report renderer. The harness provisions a
fresh authenticated durable-WAL cluster with fixed per-replica node IDs and
rejects `SKIP_UP=1`. Before startup it removes only the named bench Compose
project's containers and volumes, so a preceding `KEEP_UP=1` run cannot leak
retained receipts into the measurement; unrelated volumes are untouched.
Existing scenarios retain graph-only defaults.

The receipt leak gate samples `go_goroutines` and
`go_memstats_heap_alloc_bytes` on **all three replicas** every 5s while the
steady producer runs, without forcing GC during load. It requires complete,
finite, integral readings throughout the measured window (at least nine rounds
over 45s, with no interval gap above 7.5s); a failed or incomplete scrape fails
the run. Each replica's observed peak must remain within +15 goroutines and
+32 MiB `heap_alloc` of its post-warmup GC baseline. The existing
post-cooldown/post-warmup GC live-set delta must **also** stay within those
bounds. The report shows both independently; `LEAK_GATE_ONLY=1` skips optional
profiles and Prometheus range queries, not steady resource sampling.
For receipt runs, a failed forced-GC request on any replica or round
disqualifies the pre/post live-set snapshots even when `/metrics` responds.
Each pre/post `/metrics` scrape must also succeed and report finite,
nonnegative integral goroutine, heap-alloc, heap-inuse, and heap-object gauges
on every replica and round; a fractional or malformed reading cannot round to
zero.

Five preliminary Compose runs on the synthetic-parent stack (Apple M3 Max,
`darwin/arm64`), recorded in
[local scenario evidence](evidence/issue-1399/scenario.txt), sustained
98.05-99.97 operations/s per producer with zero non-OK results. Admission p99
ranged from 5.24-171.02 ms and lookup p99 from 2.85-62.59 ms; the fourth run
captured substantial local container-host contention without losing
throughput or semantic correctness. The producer floors of 75 operations/s
leave 25% offered-rate headroom. The 500 ms admission and 200 ms lookup
ceilings retain 2.9x and 3.2x headroom over those worst local p99s, following
the harness's ≥2x shared-runner rule. These are step-change gates, not
production capacity claims.

Direct service benchmarks repeatedly delete a missing edge in the same
durable FileWAL runtime and exclude request construction from timed work.
They quantify absent-edge/no-op admission overhead, not live-edge delete
costs. The five-run medians in
[local benchmark evidence](evidence/issue-1399/direct.txt) were 9.494 ms/op for
receipt-less Edge Delete, 14.271 ms/op for admitted receipt Edge Delete
(+4.777 ms, +50.3%), and 5.065 ms/op for confirmed receipt lookup. These
host-only numbers quantify local overhead; the real-h2c nightly scenario owns
the enforceable thresholds.
Neither these direct timings nor the Compose runs above qualify the final
merged receipt/SDK stack; they are historical synthetic-parent calibration
and predate the steady-window resource gate. Fresh uncontended measurements
after the remaining receipt families merge are required for #1399.

```bash
(cd server && go test ./service -run '^$' \
  -bench 'Benchmark(PublicReceiptEdgeDeleteAdmission|ReceiptStatusLookup)$' \
  -benchmem -benchtime=500ms -count=5)
```

## Scenarios

| File | What it stresses |
| --- | --- |
| `write_heavy.yaml` | sustained `PutVertex` from many clients |
| `read_heavy.yaml`  | sustained `GetVertex` on pre-warmed keys |
| `mixed_rw.yaml`    | 50/50 `PutVertex`/`GetVertex` (two parallel ghz runs) |
| `batch_put_vertices.yaml` | bulk write path via `PutVertices` |
| `addedge_contention.yaml` | repeated `AddEdge` on the same (tail, head) — block/mutex hotspot probe |
| `illuminate.yaml`  | graph traversal under moderate RPS |
| `scan_prefix.yaml` | paginated prefix scans |
| `ttl_churn.yaml`   | bounded born-expired Put/Delete churn over a 4096-key ring; proves causal-floor retention, slot reuse, and heap stability under the explicit causal-metadata budget (#1204) |
| `replication_soak.yaml`   | producer on r0, `Subscribe` consumers on r1/r2 for 10m |
| `chaos_kill_replica.yaml` | mid-load `docker kill` + restart of one replica |
| `many_subscribers.yaml`   | N concurrent `Subscribe` streams across replicas (#240 pubsub probe) |
| `search_churn.yaml`       | PutVertex (short TTL) + all advanced SearchVertices families; per-producer perf plus semantic/index-decay gates (#1048) |
| `search_qualification.yaml` | short fresh-cluster tag-SHA gate; deterministic Search semantics, TTL cleanup, metric, leak, and producer perf contracts (#1063) |
| `prefix_surface.yaml`     | ScanVertexKeys + ScanEdges + CountVerticesByPrefix + DeleteVerticesByPrefix fan-out (#704) |
| `replication_apply_churn.yaml` | replicated write churn; asserts `lantern_vertex_hlc_entries` returns to baseline (#700, #705) |
| `edge_contrib_idempotent.yaml` | AddEdge/AddEdges with repeated ContribIDs; verifies at-most-once dedup stays bounded (#706) |
| `mixed_edge_reset_add.yaml` | on-demand three-replica Put/Delete/Add churn on bounded edge identities; measures reset-aware contribution cost (#1203) |
| `receipt_admission_lookup.yaml` | nightly-only durable Edge Delete receipt admission plus exact-operation confirmed lookup over real h2c (#1399) |
| `backup_under_load.yaml`  | BackupSnapshot concurrent with sustained writes — on-demand only, not in release sweep (#707) |
| `broad_illuminate.yaml` | Six named traversal producers over a verified 64-way/3-hop walk and planted dense communities; preflight rejects a collapsed topology (#994) |

Each YAML declares the phases (`warmup`, `steady`, `cooldown`), the load
target (`call` + `data_template` for ghz, or an allow-listed custom driver),
optional `subscribe` and `chaos`
blocks, and the leak-gate thresholds (`goroutine_max_delta`,
`heap_alloc_max_delta_mb`). The gate evaluates against `heap_alloc`
(post-GC live bytes), forcing a `runtime.GC()` via
`/debug/pprof/heap?gc=1` before each snapshot so the reading reflects
live memory rather than span-level allocator headroom. The legacy field
name `heap_inuse_max_delta_mb` is still accepted for backward
compatibility but produced false-positive verdicts under sustained
churn — see issue #248.

For fan-out targets, each `target.calls[]` entry may declare its own positive
integer `rps`. Otherwise the phase RPS is divided equally. Use explicit RPS
when one producer must not borrow throughput from another; `search_churn` does
this so a fast `PutVertex` cannot conceal a slow Search family.

RPC benchmark payloads that omit `Vertex.expiration` / `Edge.expiration`
write permanent entries. `cluster.default_ttl_seconds` configures the server's
default TTL for cache-default paths; it does not rewrite explicit RPC payloads.
Scenarios that need decay through `ghz` should include an `expiration` template
in `data_template`.

Every ghz `data_template` is schema-checked at ordinary `go test ./...` time by
`testbed/bench/scenarios_gate_test.go`: it renders each template with
ghz-style data and protojson-unmarshals the result against the request
message resolved from the `call` name, so a proto change that orphans a
scenario fails the schema PR instead of the next nightly (#934). If you
retire or rename a wire field, migrate every scenario that sends it in the
same PR. The receipt driver's template-less calls still resolve against the
wire descriptors, and a dedicated contract test pins its driver, two RPCs,
capacity, bounded phases, perf gates, fresh-cluster restriction, nightly
wiring, and release-list exclusion.

`broad_illuminate` additionally has a semantic topology gate. Before warmup,
the harness seeds a deterministic graph, then verifies the requested 64-way
three-hop BFS shape, tuned PPR touch count, and a 32-member weak-bridge
community reduced to a directed arborescence. Its six named producers are
gated independently as well as in aggregate, so a cheap BFS cannot hide a
PPR or community regression.

### Perf gate (`perf_gate:` block)

A scenario MAY additionally declare throughput/latency floors over its
steady-phase producers (#935):

```yaml
perf_gate:
  min_steady_rps_total: 1500   # floor on SUM of producer rps
  max_p99_ms: 500              # ceiling on the WORST producer p99
  max_non_ok_ratio: 0.02       # ceiling on Σnon-OK / Σcount
```

Every key is individually optional — gate only the metrics that are stable
for the scenario. The aggregation matches the release summary table
(`testbed/bench/release`), the verdict lands in `perf_gate.json`, and a
`fail` folds into run.sh's exit code exactly like the leak gate: the
blocking nightly (`bench-nightly.yml`) enforces it, the release-time bench
stays advisory (`continue-on-error`, #256/#394).

For a fan-out scenario, `perf_gate.producers` may define the same thresholds
per named `target.calls[]` producer:

```yaml
perf_gate:
  producers:
    ppr_tuned:
      min_steady_rps: 50
      max_p99_ms: 1000
      max_non_ok_ratio: 0.02
```

All named producer gates are conjunctive with the aggregate gate and appear
as separate rows in `perf_gate.json` and the rendered report. A nonempty
producer summary without p99, or with duplicate or invalid latency percentiles,
fails closed rather than treating missing latency as zero. A reported throughput
must also agree with whether the producer recorded any calls. Null status counts
and duplicate JSON keys (including nested percentile fields) are rejected
before status and latency gates are evaluated.

#### Typed recovery lifecycle (`lifecycle_gate:` block)

A scenario that intentionally drives a bounded fail-closed recovery may split
one typed Search lifecycle reason from the generic error budget without
weakening `max_non_ok_ratio`. `search_churn` uses this for derived-index gap
recovery:

```yaml
lifecycle_gate:
  reason: SEARCH_INDEX_INCOMPLETE
  max_ratio: 0.10
  producers:
    broad_posting:
      metric_labels:
        mode: server
        phrase: "no"
        fuzziness: "0"
        prefix_terms: "no"
        prefix_present: "no"
```

The harness snapshots the bounded `lantern_search_calls_total` counter
immediately before and after steady load. For each configured producer, its
request-option labels and assigned replica select the
`outcome="failed_precondition",reason="index_incomplete"` delta. That typed
count is accepted only when it fits inside the producer's ghz
`FailedPrecondition` count; missing snapshots, decreasing counters, invalid
selectors, or an overlarge join fail closed. No human-readable error message is
parsed.

The verified typed count is checked against `max_ratio` both per producer and
in aggregate over lifecycle-eligible producers. It does not consume
`max_non_ok_ratio`; every unmatched `FailedPrecondition`, every other status,
and end-of-run transport error remains in that unchanged unexpected-error
budget. `perf_gate.json` and `report.md` retain raw, expected-lifecycle, and
unexpected counts so a green verdict remains diagnosable.

### Lifecycle metric gate (`metric_gate:` block)

`metric_gate.metrics` compares each replica's unlabeled gauge in the pre/post
runtime snapshots. Thresholds on a metric are conjunctive:

```yaml
metric_gate:
  metrics:
    lantern_search_index_docs:
      max_increase: 256
      max_ratio: 1.25
    lantern_search_index_expiration_purged:
      min_increase: 1
    lantern_search_index_retained_ratio:
      max_post: 3
    lantern_search_index_healthy:
      min_post: 1
      max_post: 1
```

Available comparisons are `max_increase`, `max_ratio`, `min_increase`,
`min_post`, and `max_post`. Missing or non-finite samples fail closed. Ratio
growth from a zero baseline also fails when post is positive; pair ratio and
absolute limits intentionally rather than using ratio as a noise filter. The
verdict is written to `metric_gate.json` and folds into `run.sh`'s exit code.

### Search semantic gate (`semantic_gate:` block)

`semantic_gate.kind: search` seeds a deterministic fixture before warmup and
checks every replica both before and after steady load. It proves a persistent
hit, deleted/expired absence, stable ordering, key-prefix filtering, phrase,
fuzzy-1/2, prefix-term, ALL, and MIN_SHOULD behavior. It also walks a five-page
endpoint-sticky cursor chain with FULL_VERTEX snapshots, rejecting duplicate,
gap, truncated-tail, or projection races under concurrent index churn. An
HTTP-OK response with empty hits therefore fails. The bounded `semantic_pre.json` and
`semantic_post.json` artifacts contain only phase, replica endpoint, check
count, and verdict—never query text, prefixes, keys, or values.

Sizing rules (all seven release scenarios carry a block sized this way):

- Floors are **ratchet floors with ≥2x headroom** over the worst nightly
  baseline — they exist to catch step-change regressions (accidental O(n²),
  lock convoy, a dropped fan-out), NOT single-digit-% drift, which shared
  `ubuntu-latest` runners cannot resolve. Do not tighten them to "just below
  last night's number".
- **Gate p99 only where it is stable.** Scenarios whose offered load
  deliberately saturates the runner (`broad_rw`, `broad_mutate`) have
  queue-depth p99 — observed varying 7x night-over-night — so they gate rps
  and non-OK only.
- **Re-baseline after changing a scenario's mix.** Adding/removing a
  `target.calls` entry re-splits RPS for entries without an explicit `rps` and
  always shifts the offered mix; drop the affected ceilings in the same PR and
  restore them (from fresh nightly numbers, with headroom) after a few green
  nightlies.
- When a perf floor legitimately moves (accepted throughput/latency
  trade-off), adjust it **in the same PR** and say so in the PR body — same
  contract as the coverage-floor ratchet in CONTRIBUTING.md.

## Output layout

```
testbed/bench/out/<scenario>/<ts>/
├── report.md                       # rendered summary (start here)
├── leak_gate.json                  # verdict + per-replica deltas + thresholds
├── metric_gate.json                # per-replica pre/post gauge contracts (when declared)
├── semantic_{pre,post}.json        # bounded Search semantic verdicts (when declared)
├── perf_gate.json                  # perf verdict + thresholds + observed (only when perf_gate: declared)
├── runtime_pre.json                # runtime values + unlabeled lifecycle gauges, after warmup
├── runtime_post.json               # same, after cooldown
├── ghz_warmup_<endpoint>.json      # raw ghz results, one per invocation
├── ghz_steady_<...>.json         # ghz or compatible custom-driver summaries
├── ghz_sub*_<n>.json               # if subscribers were launched
├── prom/
│   ├── _index.ndjson               # {query, file} pairs
│   └── q_NN.json                   # raw Prom /api/v1/query_range output
└── pprof/
    └── <endpoint>__{pre,post}__{heap,goroutine,allocs,mutex,block[,profile]}.pb.gz
```

## Drill-down workflow

1. **Verdict & deltas.** Open `report.md`. The leak-gate table shows the
   pre/post goroutines + heap_alloc + heap_inuse + heap_objects per
   replica with the Δ vs the scenario threshold. The verdict is driven
   by `heap_alloc` (post-GC live bytes); `heap_inuse` and `heap_objects`
   are shown for context. Any cell exceeding the threshold is the first
   place to look.
2. **Allocation source.** For a leaking replica, diff its heap profiles:

   ```bash
   go tool pprof -http=:0 \
     -base testbed/bench/out/<sc>/<ts>/pprof/<ep>__pre__heap.pb.gz \
            testbed/bench/out/<sc>/<ts>/pprof/<ep>__post__heap.pb.gz
   ```

3. **Stuck goroutines.** Open `<ep>__post__goroutine.pb.gz` and look for
   blocked stacks (e.g. on channel send/recv, on a sync.Mutex). For
   `addedge_contention` the mutex and block profiles (`<ep>__post__mutex.pb.gz`,
   `<ep>__post__block.pb.gz`) are the primary signal.
4. **Tail latency.** Cross-reference the ghz p99 column in `report.md`
   with the matching Prom histogram queries listed under
   "Prometheus range queries" (raw data in `prom/q_NN.json`).
5. **Pubsub (#240).** `many_subscribers` and `replication_soak` exercise
   the subscription queue. Inspect the per-policy
   `lantern_subscription_dropped_total` and
   `lantern_subscription_queue_depth_bucket` queries in `prom/`.

## Why blocking qualifications stay bounded

The full sweep needs multi-minute steady phases, a 10-minute soak, pprof and
Prometheus capture, a healthy Docker daemon, and stable host conditions. It is
therefore advisory at release time and blocking only in the untruncated
nightly. Root tags additionally run the deliberately short, tolerant
`search_qualification` scenario: deterministic semantic and lifecycle
failures block, while loose throughput/p99 ratchets reject only step changes
and tolerate normal hosted-runner jitter. Receipt admission/lookup is also
blocking nightly, but remains outside the release sweep until hosted-runner
history supports a release-safe threshold.

[ghz]: https://ghz.sh/
[yq]: https://github.com/mikefarah/yq
[#237]: https://github.com/anaregdesign/lantern/issues/237
