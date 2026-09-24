# Bench report — `edge_ttl_churn` @ `20260924T053424Z`

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
| `gc_hub_b10000_r1.json` | `50900a64efbbc3fa8a7be2bcbb3078463b0e0013` | `hub` | 10000 | 1000 | 100000 / 3199969 | 100 | 83.61 | 88.98 | 105.30 | 31688977 | 52500 | 50000 | 2500 / 64496 | 90000 | 10115.50 | 0 |
| `gc_hub_b10000_r2.json` | `50900a64efbbc3fa8a7be2bcbb3078463b0e0013` | `hub` | 10000 | 1000 | 100000 / 3199969 | 100 | 87.71 | 95.27 | 96.52 | 31688188 | 52500 | 50000 | 2500 / 64496 | 90000 | 10110.54 | 0 |
| `gc_hub_b10000_r3.json` | `50900a64efbbc3fa8a7be2bcbb3078463b0e0013` | `hub` | 10000 | 1000 | 100000 / 3199969 | 100 | 84.63 | 90.63 | 98.51 | 31686237 | 52500 | 50000 | 2500 / 64496 | 90000 | 10115.52 | 0 |
| `gc_uniform_b10000_r1.json` | `50900a64efbbc3fa8a7be2bcbb3078463b0e0013` | `uniform` | 10000 | 1000 | 100000 / 3200000 | 100 | 76.70 | 86.30 | 90.28 | 31689096 | 52500 | 50000 | 2500 / 64528 | 90000 | 10113.10 | 0 |
| `gc_uniform_b10000_r2.json` | `50900a64efbbc3fa8a7be2bcbb3078463b0e0013` | `uniform` | 10000 | 1000 | 100000 / 3200000 | 100 | 75.10 | 85.94 | 89.21 | 31689087 | 52500 | 50000 | 2500 / 64528 | 90000 | 10104.79 | 0 |
| `gc_uniform_b10000_r3.json` | `50900a64efbbc3fa8a7be2bcbb3078463b0e0013` | `uniform` | 10000 | 1000 | 100000 / 3200000 | 100 | 87.15 | 92.67 | 99.45 | 31689094 | 52500 | 50000 | 2500 / 64528 | 90000 | 10117.96 | 0 |

**Synthetic GC trigger:** direct `gc_p99 >= 200 ms` in at least two of three comparable runs, each with at least 100 ticks. A missing or undersampled run is `unknown`.

| shape | budget tails | interval ms | vertices / edges | runs | p99 >= 200 ms | verdict |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| `hub` | 10000 | 1000 | 100000 / 3199969 | 3 | 0 | `not_fired` |
| `uniform` | 10000 | 1000 | 100000 / 3200000 | 3 | 0 | `not_fired` |

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
