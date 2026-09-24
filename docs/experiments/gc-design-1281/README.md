# #1281 bounded-GC design investigation

The [#1183 baseline](../gc-backup-1183/README.md) found that the 100,000-vertex,
about 3.2-million-edge full sweep crossed the pre-registered direct GC tick
p99 threshold of 200 ms, while the 5,000-tail budget held p99 below it at
the cost of 24–30 seconds sampled physical-reclamation lag at a one-second
tick. #1281 asks whether existing controls can meet both a synthetic
`p99 < 200 ms` and `sampled max lag <= 20 s` objective before designing an
expiration heap or timing wheel. This is **host stress**, not a deployed
Lantern SLO; no deployment is running.

This run used committed SHA `50900a64efbbc3fa8a7be2bcbb3078463b0e0013`
on an Apple M3 Max, 16 CPUs, 64 GiB RAM, Darwin arm64, Go 1.27.0. The
available Docker VM had 8.2 GiB, so the graph ran in host memory. The
only change from #1183's GC algorithm was stress-harness resource capture:
the script compiled the test binary once, then timed each stress **process**
with `/usr/bin/time -l`, excluding compiler memory and CPU. See
[`host.txt`](results/host.txt), the six `gc_*.json` raw tick files, their
matching `gc_*.resource.txt` process statistics, the
[`standard report`](results/report.md), and
[`SHA256SUMS`](results/SHA256SUMS). No vertex key/value or graph payload
is present.

The measured SHA was a **local commit before this branch was rebased** onto
main, and was never pushed as a remote ref. The rebase incorporated the
unrelated Go SDK timing-test fix #1286 and changed the branch commit IDs.
The exact measured code is identified by its Git object IDs below: each
entry was equal between `50900a6` and this PR's post-rebase branch. The
`core` tree includes the GC implementation, fixture, and module dependency
files. The reporter, stress script, workspace file, and root module file
were also byte-identical. The raw artifacts retain the actual measured
commit ID rather than a rewritten one.

| Measurement-relevant path | Object ID at measured commit and post-rebase branch |
| --- | --- |
| `core` tree | `734b0174c3c648acac3767aaae5cff73616b9c37` |
| `testbed/bench/gc_stress.sh` blob | `3bec953b9742b0f7fbb09c6a997d1ae4b6bce785` |
| `testbed/bench/report` tree | `3f7a0bbd115e92550a213abdb9a37ac88dfc54b7` |
| `go.work` blob | `078640dc844372aaebac8f41aafe3e554d1de04a` |
| root `go.mod` blob | `0436a6b3f31ed24438573cde32a251554291fb17` |

From the PR checkout, `git rev-parse HEAD:core
HEAD:testbed/bench/gc_stress.sh HEAD:testbed/bench/report HEAD:go.work
HEAD:go.mod` verifies the current side of this equality. #1286 changed
`sdks/go/search_incremental_test.go`; it is outside this measured workload.

For the 10,000-tail/one-second configuration, uniform has 3,200,000 edges;
hub has 3,199,969 edges with one 100,000-head tail. Each run records 100
unrounded, timestamped `GraphCache.Watch` ticks, five expiry groups, two
vertex-deletion bands, 52,500 expired contributions, 2,500 zero-edge
removals, dangling-edge reclamation, and 100 tracked short-only edge probes.
The p99 is nearest rank of the **direct tick duration**, independently per
run. The sampled lag is expiry deadline to observed physical removal for
those probes; it is not a worst-case bound for all edges.

| Shape | Tick p99 across 3 runs | Max tick | Max scanned edges/tick | Sampled max lag | Process CPU user time / 100 s | Max process RSS |
| --- | --- | ---: | ---: | --- | --- | --- |
| Uniform | 86.30 / 85.94 / 92.67 ms | 99.45 ms | 320,260 | 10.105–10.118 s | 8.62–9.28 s | 975–1,085 MiB |
| Hub | 88.98 / 95.27 / 90.63 ms | 105.30 ms | 410,222 | 10.111–10.116 s | 8.74–9.24 s | 1,054–1,090 MiB |

All six runs reclaimed 100/100 probes, with 90,000 maximum backlog tails.
The process RSS includes Go runtime and non-heap mappings, so it is a
**memory-footprint proxy**, not a measurement of live Go heap or an estimate
for another host. The 10,000-tail option met both synthetic targets in all
three runs of both shapes. The hub still demonstrates that a tail count is
not an edge-work bound; a still-higher-degree tail could take longer than
the measured maximum.

## Cadence sensitivity (exploratory)

Two additional **single** runs used the same code SHA and 10,000-tail budget
but a two-second tick. They are not three-run qualifying measurements. The
uniform and hub runs were executed separately to avoid overlap with another
host benchmark; the interrupted hub attempt from the first script invocation
is discarded. Each completed run has its own host record and raw JSON under
[`cadence_2s/`](results/cadence_2s/), plus a combined
[`report.md`](results/cadence_2s/report.md).

| Shape | Direct tick p99 / max | Sampled max lag | CPU user time / 200 s | Max process RSS |
| --- | --- | ---: | ---: | ---: |
| Uniform | 81.57 / 82.04 ms | 20.112 s | 8.71 s | 1,008 MiB |
| Hub | 87.45 / 103.34 ms | 20.116 s | 9.18 s | 954 MiB |

Doubling the interval roughly halved average scan CPU in this fixture (the
similar total user time was spread over 200 rather than 100 seconds), while
the tracked maximum lag rose to just over the **20-second** investigation
target in both runs. This is an interval-sensitivity observation, not a
two-second p99 verdict or a safe production cadence.

The existing server knobs support this as an **opt-in** combination:
`LANTERN_GC_EDGE_BUDGET=10000` and `LANTERN_GC_INTERVAL_SECONDS=1`.
It is not a recommended universal default. At the server's default
60-second interval, 100,000 tails require 10 ticks (10 minutes) for one
pass and can wait nearly 20 ticks (20 minutes) between visits. At one
second, that arithmetic becomes 10–20 seconds; the tracked samples in this
specific workload were about 10.1 seconds. A production pause, physical
reclamation, heap, and CPU requirement must be supplied before choosing
an operator setting or changing the default. Reads already hide expired
entries before physical GC, independently of this tuning.

Reproduce from a clean committed checkout with adequate host memory:

```bash
GC_BUDGETS=10000 GC_INTERVAL_MS=1000 GC_REPETITIONS=3 \
  GC_RESOURCE_TRACE=1 GC_SKIP_BENCHMARKS=1 \
  ./testbed/bench/gc_stress.sh
```

The exact command writes a timestamped directory under ignored
`testbed/bench/out/edge_ttl_churn/`. The six JSON artifacts here can be
rerendered with `go run ./testbed/bench/report -dir
docs/experiments/gc-design-1281/results -scenario edge_ttl_churn`.
For the exploratory cadence runs, use `GC_INTERVAL_MS=2000` and
`GC_REPETITIONS=1`, and keep their results explicitly separate from the
three-run decision set.
