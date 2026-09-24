# #1183 synthetic GC and periodic-backup measurement

There is no running Lantern deployment (confirmed 2026-09-24). These are
**synthetic host measurements**, not observed production scale or a production
latency/RPO baseline. The [raw, content-free artifacts](results/) and the
scripts in [`testbed/bench`](../../../testbed/bench/README.md) allow the
pre-registered decisions in [#1183](https://github.com/anaregdesign/lantern/issues/1183)
to be checked without relying on rounded logs or RPC latency as a GC proxy.

The host was an Apple M3 Max (16 CPUs, 64 GiB RAM), Darwin arm64, Go 1.27.0.
The available Docker VM had only 8.2 GiB, so the 100,000-vertex / about
3.2-million-edge graph ran in host memory. [`gc_host.txt`](results/gc_host.txt),
[`backup_write150_host.txt`](results/backup_write150_host.txt), and every raw
artifact record their measured source SHA and parameters. The target graph
uses 80-byte keys; no key, value, or graph payload is present in the artifacts.

## Direct GC ticks

The GC code SHA was `42a8d707b6d3a19e0d6b90a5dae79984c2f81771`.
Each of the 12 runs has 100 timestamped, unrounded `GraphCache.Watch` tick
durations at a one-second interval. Uniform has 32 outgoing edges per vertex
(3,200,000 edges); hub has one 100,000-head tail and 31 outgoing edges per
other tail (3,199,969 edges). Five deadline groups expire 50,000
contributions in still-live buckets plus 2,500 short-only buckets. Two
deletion bands create dangling edges. The full, per-run counts, p95/p99/max,
backlog, and sampled physical-reclamation lag are in
[`gc_report.md`](results/gc_report.md); the `gc_*.json` files retain the raw
tick samples and 100 physical-reclamation probes per run.

| Shape | Tail budget per tick | Direct tick p99 across three runs | Pre-registered `>= 200 ms` signal |
| --- | ---: | --- | --- |
| Uniform | Full sweep (`0`) | 498.52, 413.25, 421.81 ms | Fired, 3/3 |
| Hub | Full sweep (`0`) | 492.69, 444.92, 432.57 ms | Fired, 3/3 |
| Uniform | 5,000 | 87.37, 53.79, 59.31 ms | Not fired, 0/3 |
| Hub | 5,000 | 79.89, 69.58, 76.42 ms | Not fired, 0/3 |

All runs reclaimed the 100 sampled short-only edges and recorded 52,500
expired contributions and 2,500 zero-edge removals. The 5,000-tail budget
reached 95,000 backlog tails and sampled maximum expiry-to-reclamation lag
of 24.06–30.07 seconds; reads hide expired entries immediately, so this is
physical reclamation lag. The hub's hottest budgeted tick still scanned about
255,100 edges because the budget limits **tails**, not edges. At the server's
60-second GC interval, a 100,000-tail graph needs at least 20 ticks (20
minutes) for one 5,000-tail cycle and could need nearly two cycles (40
minutes) to revisit a tail after expiry. The one-second stress result cannot
justify making 5,000 the server default without an explicit reclamation-lag
and heap budget. The server setting remains full sweep by default.

The ten-repeat, single-flush benchmarks are retained separately as
[`benchmark.txt`](results/benchmark.txt) and
[`benchstat.txt`](results/benchstat.txt): full median 622.8 ms/op and
incremental median 58.62 ms/op. They are comparison baselines, **not** the
direct tick p99 used for the decision.

## Periodic backup and named RPC producers

The backup code SHA was `d3ceef7729a54212cb1958a19299993d02deedec`.
The same 100,000/3,200,000 host graph drove the actual periodic
`Backupper.Run` at a 30-second interval over the real Connect/h2c path.
`GetVertex`, `ScanVertices`, and `Illuminate` each offered 200 calls/s;
`PutVertex` provided concurrent writes. Each call's start, end, latency, and
success status is in the compressed raw JSON. Equal-length before/during/after
windows are tied to each completed periodic tick. A comparison needs at least
100 successful calls and zero errors in **each** window; the p99 is nearest
rank over completed successful calls fully inside the window.

The first run offered 50 writes/s. Its three backup attempts completed and
all named read windows qualified, but each during-backup write window had
fewer than 100 calls (92, 91, 77). Its write p99 values therefore cannot
qualify a comparison. Before the second run, the method-only change to 150
offered writes/s was [recorded on #1183](https://github.com/anaregdesign/lantern/issues/1183#issuecomment-5808117719).
The graph, backup interval, read rates, and decision threshold stayed fixed.
Both runs and their reports are retained; the second is the complete-window
decision run.

At 150 offered writes/s, 3/3 periodic attempts completed (1.697–1.949
seconds per dump), all producer/window comparisons had at least 100 successes
and zero errors, and no named read producer's during-backup p99 exceeded 2×
its matched before-backup p99 on any of the three ticks. The pre-registered
three-tick **read p99 signal did not fire**. Per-tick counts, p99, durations,
and materialization/send split are in
[`backup_write150_report.md`](results/backup_write150_report.md);
[`backup_write50_report.md`](results/backup_write50_report.md) is retained as
the first, partly inconclusive run.

The p99 result does **not** mean there were no pauses. In the decision run,
each of `ScanVertices`, `Illuminate`, and `PutVertex` had one during-backup
call around 532–590 ms per tick; the during windows held only 168–265
completed calls for these producers, so one outlier did not enter nearest-rank
p99. Their complete call records remain in `backup_write150.json.gz`.
These isolated maxima merit a separately specified tail-sensitive check if a
real deployment establishes a latency requirement; they do not satisfy the
pre-registered p99 trigger. `GetVertex` did not show comparable maxima.

The synthetic backup result does not establish an operator RPO, RTO, fsync
budget, or deletion-survives-restore requirement. The WAL trigger in #844
remains unknown. Actual deployment GC and read latency are also unknown.

## Reproduce and inspect

Run from a clean committed checkout with adequate host memory and `benchstat`
in `PATH`:

```bash
./testbed/bench/gc_stress.sh
BACKUP_WRITE_RPS=150 ./testbed/bench/backup_stress.sh
```

The scripts record the exact measured SHA and write under the ignored
`testbed/bench/out/`. To rerender the checked-in GC report, pass the results
directory to the standard reporter. To rerender either backup report, expand
its raw JSON into a temporary directory as `backup_periodic.json` first:

```bash
go run ./testbed/bench/report -dir docs/experiments/gc-backup-1183/results -scenario edge_ttl_churn -timestamp 20260924T044600Z
tmpdir="$(mktemp -d)"
gzip -dc docs/experiments/gc-backup-1183/results/backup_write150.json.gz > "$tmpdir/backup_periodic.json"
go run ./testbed/bench/report -dir "$tmpdir" -scenario periodic_backup -timestamp 20260924T051256Z
rm -rf "$tmpdir"
```

[`SHA256SUMS`](results/SHA256SUMS) checks the 12 GC raw files and two
compressed backup raw files. Current checkout code includes the reporter,
but the recorded SHAs identify the two exact measurement builds.
