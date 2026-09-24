# 0009: Keep bounded GC sweeps as an opt-in pause/reclamation trade-off

Status: Accepted

## Context

At 100,000 vertices and about 3.2 million edges, #1183 measured direct
`GraphCache.Watch` full-sweep tick p99 above 200 ms in 3/3 runs of both
uniform and hub-shaped graphs. A 5,000-tail budget reduced p99 below 90 ms
but deferred sampled physical reclamation by 24–30 seconds at a one-second
tick. The server exposes both the tail budget and tick interval, but its
defaults remain a full sweep every 60 seconds. There is no running Lantern
deployment or operator pause/reclamation/heap target. Expired entries are
hidden by reads before their physical sweep.

#1281 compared the existing bounded-sweep setting before introducing an
expiration index. On the same host and fixture, a 10,000-tail budget at a
one-second tick met its explicit synthetic target: all three 100-tick runs
of each shape had direct tick p99 below 200 ms, sampled max
expiry-to-physical-reclamation lag below 20 seconds, and 100/100 tracked
short-only edges reclaimed. The [raw evidence and resource context](../experiments/gc-design-1281/README.md)
include per-run CPU/RSS, maximum tick, backlog, and scanned-edge counts.

| Existing setting | Direct tick p99 across qualifying runs | Sampled max physical-reclaim lag | Interpretation |
| --- | --- | --- | --- |
| Full sweep, 1 s | 413–499 ms uniform; 433–493 ms hub | about 0.43–0.46 s | Pause target fails |
| 5,000 tails, 1 s | 54–87 ms across shapes | 24–30 s | Reclaim target fails |
| 10,000 tails, 1 s | 86–95 ms across shapes | 10.105–10.118 s | Both synthetic targets met, 3/3 per shape |

At 10,000 tails with a two-second tick, one exploratory run per shape had
p99 of 81.57/87.45 ms and sampled lag of 20.112/20.116 seconds. It is not
a three-run verdict. Process CPU user time was similar to the one-second
configuration, but spread over twice the wall time; it illustrates why a
longer cadence reduces average scan cost while increasing reclaim lag.

## Decision

Retain the existing bounded tail sweep and configurable cadence. Do not
introduce an expiration heap, timing wheel, or a new per-edge sweep cursor
for this synthetic fixture: the current code meets the registered pause and
sampled-reclaim objectives when operators opt in to a 10,000-tail,
one-second configuration. Preserve the full-sweep/60-second server defaults
until a concrete deployment supplies pause, physical-reclamation, CPU, and
heap requirements. A local stress target is not a production SLO.

The sweep remains a correctness safety net for dangling in-edges, which have
no reverse adjacency index. Reads continue to hide fully expired and
dangling edges immediately. The tail cursor's 90,000-remaining peak backlog
at 10,000 tails/tick is expected at the start of a pass and must be interpreted
with the tick interval and physical-reclamation lag, not as a standalone
failure signal.

## Consequences and limits

- At one-second ticks and 100,000 tails, 10,000 tails/tick needs 10 ticks
  for one pass, and an expiry just after a tail was visited can wait nearly
  two passes. The fixture's tracked maximum was about 10.1 seconds; that
  does not cap all possible edges. At the default 60-second tick, the same
  budget would take 10 minutes per pass and could wait nearly 20 minutes.
  Therefore changing only the budget from the default is not justified.
- The hub run scanned up to 410,222 edges in one tick, versus 320,260 for
  uniform, despite the same 10,000-tail budget. One tail with more than
  100,000 heads remains an unbounded unit of work. A strict maximum pause
  requirement for arbitrary degree would need a separate edge-work budget
  or expiry-index investigation with a concrete target.
- The stress process used about 8.6–9.3 CPU user seconds per 100 seconds
  and reached about 975–1,090 MiB maximum RSS on an Apple M3 Max. RSS is a
  process-footprint proxy, not live Go heap. Neither number is a production
  capacity estimate.
- No running deployment supplied read latency, GC pause, memory pressure,
  or WAL RPO evidence. A future operator requirement should reopen this
  decision with a scoped Issue and measured traffic; it should not be
  inferred from the host fixture.

## Alternatives deferred

An edge-work cursor could bound the high-degree-tail tick but adds mutation
and iterator state. A heap or timing wheel would add per-expiration state,
stale-deadline handling, and lifecycle invariants for Vertex TTLs and
individual additive Edge contributions while still requiring a safety sweep
for dangling edges. None is justified by the current synthetic gate because
the existing knobs meet it. If a future requirement cannot be met by the
bounded sweep, compare these options under one expiry-index interface and
retain the low-frequency safety sweep, as #844 specified. WAL is a separate
durability question and is unaffected by this GC decision.
