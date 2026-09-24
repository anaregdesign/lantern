# Bench report — `edge_ttl_churn` @ `20260924T044600Z`

**Leak gate verdict:** `(no leak gate captured)`

**Metric gate verdict:** `(no metric gate configured)`

**Semantic gate verdict:** `(no semantic gate configured)`

**Perf gate verdict:** `(no perf gate configured)`

## Leak gate

_not captured_

## Metric gate

_not configured_

## Semantic gate

_not configured_

## Perf gate

_not configured_

## GC ticks

`gc_p99` is nearest-rank p99 of direct raw `GraphCache.Watch` tick durations, recomputed from each file's samples. It is not RPC latency or a histogram bucket estimate. Each row is one repetition; compare only matching scale, shape, interval, and host.

| file | SHA | shape | budget tails | interval ms | vertices / edges | ticks | gc_p95 ms | gc_p99 ms | max ms | scanned edges | expired contributions | compacted in live buckets | zero / dangling removed | max backlog tails | max reclaim lag ms | unreclaimed probes |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `gc_hub_b0_r1.json` | `42a8d707b6d3a19e0d6b90a5dae79984c2f81771` | `hub` | 0 | 1000 | 100000 / 3199969 | 100 | 443.06 | 492.69 | 910.75 | 316285401 | 52500 | 50000 | 2500 / 64496 | 0 | 436.31 | 0 |
| `gc_hub_b0_r2.json` | `42a8d707b6d3a19e0d6b90a5dae79984c2f81771` | `hub` | 0 | 1000 | 100000 / 3199969 | 100 | 424.86 | 444.92 | 446.14 | 316249780 | 52500 | 50000 | 2500 / 64496 | 0 | 456.49 | 0 |
| `gc_hub_b0_r3.json` | `42a8d707b6d3a19e0d6b90a5dae79984c2f81771` | `hub` | 0 | 1000 | 100000 / 3199969 | 100 | 405.70 | 432.57 | 451.51 | 316286676 | 52500 | 50000 | 2500 / 64496 | 0 | 434.59 | 0 |
| `gc_hub_b5000_r1.json` | `42a8d707b6d3a19e0d6b90a5dae79984c2f81771` | `hub` | 5000 | 1000 | 100000 / 3199969 | 100 | 68.69 | 79.89 | 104.09 | 15877915 | 52500 | 50000 | 2500 / 64496 | 95000 | 29083.52 | 0 |
| `gc_hub_b5000_r2.json` | `42a8d707b6d3a19e0d6b90a5dae79984c2f81771` | `hub` | 5000 | 1000 | 100000 / 3199969 | 100 | 61.11 | 69.58 | 75.66 | 15879056 | 52500 | 50000 | 2500 / 64496 | 95000 | 27059.06 | 0 |
| `gc_hub_b5000_r3.json` | `42a8d707b6d3a19e0d6b90a5dae79984c2f81771` | `hub` | 5000 | 1000 | 100000 / 3199969 | 100 | 63.86 | 76.42 | 78.43 | 15878218 | 52500 | 50000 | 2500 / 64496 | 95000 | 28080.35 | 0 |
| `gc_uniform_b0_r1.json` | `42a8d707b6d3a19e0d6b90a5dae79984c2f81771` | `uniform` | 0 | 1000 | 100000 / 3200000 | 100 | 412.77 | 498.52 | 633.99 | 316281664 | 52500 | 50000 | 2500 / 64528 | 0 | 444.55 | 0 |
| `gc_uniform_b0_r2.json` | `42a8d707b6d3a19e0d6b90a5dae79984c2f81771` | `uniform` | 0 | 1000 | 100000 / 3200000 | 100 | 401.65 | 413.25 | 418.89 | 316287568 | 52500 | 50000 | 2500 / 64528 | 0 | 430.66 | 0 |
| `gc_uniform_b0_r3.json` | `42a8d707b6d3a19e0d6b90a5dae79984c2f81771` | `uniform` | 0 | 1000 | 100000 / 3200000 | 100 | 411.24 | 421.81 | 424.69 | 316287568 | 52500 | 50000 | 2500 / 64528 | 0 | 428.77 | 0 |
| `gc_uniform_b5000_r1.json` | `42a8d707b6d3a19e0d6b90a5dae79984c2f81771` | `uniform` | 5000 | 1000 | 100000 / 3200000 | 100 | 56.11 | 87.37 | 95.36 | 15878532 | 52500 | 50000 | 2500 / 64528 | 95000 | 24062.09 | 0 |
| `gc_uniform_b5000_r2.json` | `42a8d707b6d3a19e0d6b90a5dae79984c2f81771` | `uniform` | 5000 | 1000 | 100000 / 3200000 | 100 | 51.88 | 53.79 | 71.28 | 15878156 | 52500 | 50000 | 2500 / 64528 | 95000 | 30074.77 | 0 |
| `gc_uniform_b5000_r3.json` | `42a8d707b6d3a19e0d6b90a5dae79984c2f81771` | `uniform` | 5000 | 1000 | 100000 / 3200000 | 100 | 55.24 | 59.31 | 62.66 | 15878368 | 52500 | 50000 | 2500 / 64528 | 95000 | 30051.84 | 0 |

**Synthetic GC trigger:** direct `gc_p99 >= 200 ms` in at least two of three comparable runs, each with at least 100 ticks. A missing or undersampled run is `unknown`.

| shape | budget tails | interval ms | vertices / edges | runs | p99 >= 200 ms | verdict |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| `hub` | 0 | 1000 | 100000 / 3199969 | 3 | 3 | `fired` |
| `hub` | 5000 | 1000 | 100000 / 3199969 | 3 | 0 | `not_fired` |
| `uniform` | 0 | 1000 | 100000 / 3200000 | 3 | 3 | `fired` |
| `uniform` | 5000 | 1000 | 100000 / 3200000 | 3 | 0 | `not_fired` |

## Periodic backup/read correlation

_no periodic backup artifact found_

## ghz runs

_no ghz artifacts found_

## Prometheus range queries

_no prom queries captured_

## pprof profiles

_no pprof profiles captured_

## Drill-down

1. Compare `Δ` cells in the leak gate. Any cell exceeding its threshold is the first place to look.
2. For a suspect replica, diff `pprof/<replica>__pre__heap.pb.gz` against `pprof/<replica>__post__heap.pb.gz`:
   `go tool pprof -http=:0 -base <pre> <post>`
3. For elevated tail latency, cross-reference the matching Prom histogram query with `<replica>__post__goroutine.pb.gz` (look for blocked stacks).
