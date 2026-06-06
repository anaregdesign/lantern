# lantern bench harness (`testbed/bench/`)

Reusable performance + memory-leak harness for the HA `docker compose`
cluster (see `deploy/compose/`). Drives the cluster with [`ghz`][ghz],
captures Prometheus range queries + Go pprof snapshots, applies a
per-scenario leak gate, and renders a Markdown report.

> **PR CI does not run this harness.** Per the parent epic [#237], the
> default PR pipeline runs only fast functional checks; this harness is a
> local / on-demand tool meant to be used by maintainers when investigating
> performance or leak regressions. Its outputs (under `testbed/bench/out/`)
> are git-ignored.
>
> **Release CI runs the full canonical sweep.** On every `vX.Y.Z` tag
> push, the `Release` workflow runs `./testbed/bench/release.sh` — a
> driver that sweeps the scenarios listed in `release-scenarios.txt` (the
> three `smoke_*` variants as a fast head-of-sweep signal, then the full
> canonical scenarios across read/write/batch/scan/edge/TTL/illuminate
> variants/fan-out) and produces a fixed-format `bench-report.md` that
> is spliced into the GitHub Release notes. The bench job is
> `continue-on-error`, so a noisy runner cannot block a release. See
> issues [#256], [#262].

[#256]: https://github.com/anaregdesign/lantern/issues/256
[#262]: https://github.com/anaregdesign/lantern/issues/262

## Prerequisites

- Docker + `docker compose` v2
- [`ghz`][ghz] ≥ 0.117 — drives load over gRPC wire frames. The Lantern
  server is Connect-only (see [#335][i335]), but the Connect-Go handlers
  accept gRPC, gRPC-Web, and Connect on the same h2c socket, and
  `connectrpc.com/grpcreflect` exposes the standard
  `grpc.reflection.v1*` service the harness uses for descriptor
  discovery. ghz keeps working unchanged. See [#383][i383] for the
  verification log.
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
```

The exit code mirrors the leak-gate verdict (`0`=pass, `1`=fail). Run
artifacts are written under
`testbed/bench/out/<scenario>/<UTC-timestamp>/`.

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
| `ttl_churn.yaml`   | short TTLs + steady writes (expirer + timer wheel) |
| `replication_soak.yaml`   | producer on r0, `Subscribe` consumers on r1/r2 for 10m |
| `chaos_kill_replica.yaml` | mid-load `docker kill` + restart of one replica |
| `many_subscribers.yaml`   | N concurrent `Subscribe` streams across replicas (#240 pubsub probe) |

Each YAML declares the phases (`warmup`, `steady`, `cooldown`), the ghz
target (`call` + `data_template`), optional `subscribe` and `chaos`
blocks, and the leak-gate thresholds (`goroutine_max_delta`,
`heap_alloc_max_delta_mb`). The gate evaluates against `heap_alloc`
(post-GC live bytes), forcing a `runtime.GC()` via
`/debug/pprof/heap?gc=1` before each snapshot so the reading reflects
live memory rather than span-level allocator headroom. The legacy field
name `heap_inuse_max_delta_mb` is still accepted for backward
compatibility but produced false-positive verdicts under sustained
churn — see issue #248.

## Output layout

```
testbed/bench/out/<scenario>/<ts>/
├── report.md                       # rendered summary (start here)
├── leak_gate.json                  # verdict + per-replica deltas + thresholds
├── runtime_pre.json                # per-replica go_goroutines + heap_alloc/heap_inuse/heap_objects, after warmup
├── runtime_post.json               # same, after cooldown
├── ghz_warmup_<endpoint>.json      # raw ghz results, one per invocation
├── ghz_steady_<...>.json
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

## Why not in CI?

This harness needs real wall-clock time (multi-minute steady phases, a
10-minute soak), a healthy Docker daemon, and stable host conditions to
produce trustworthy numbers. Running it in CI would be both slow and
flaky. The parent epic ([#237]) explicitly carves out this harness as a
maintainer-driven tool; functional regressions are covered by the
existing unit + integration suites that CI already runs.

[ghz]: https://ghz.sh/
[yq]: https://github.com/mikefarah/yq
[#237]: https://github.com/anaregdesign/lantern/issues/237
