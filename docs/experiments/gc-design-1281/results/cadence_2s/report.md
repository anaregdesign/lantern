# Bench report — `edge_ttl_churn` @ `20260924T054750Z`

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
| `gc_hub_b10000_r1.json` | `50900a64efbbc3fa8a7be2bcbb3078463b0e0013` | `hub` | 10000 | 2000 | 100000 / 3199969 | 100 | 84.12 | 87.45 | 103.34 | 31688978 | 52500 | 50000 | 2500 / 64496 | 90000 | 20116.40 | 0 |
| `gc_uniform_b10000_r1.json` | `50900a64efbbc3fa8a7be2bcbb3078463b0e0013` | `uniform` | 10000 | 2000 | 100000 / 3200000 | 100 | 76.88 | 81.57 | 82.04 | 31689089 | 52500 | 50000 | 2500 / 64528 | 90000 | 20112.37 | 0 |

**Synthetic GC trigger:** direct `gc_p99 >= 200 ms` in at least two of three comparable runs, each with at least 100 ticks. A missing or undersampled run is `unknown`.

| shape | budget tails | interval ms | vertices / edges | runs | p99 >= 200 ms | verdict |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| `hub` | 10000 | 2000 | 100000 / 3199969 | 1 | 0 | `unknown` |
| `uniform` | 10000 | 2000 | 100000 / 3200000 | 1 | 0 | `unknown` |

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
