# Bench report — `periodic_backup` @ `20260924T050950Z`

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

_no direct GC tick artifacts found_

## Periodic backup/read correlation

Synthetic host run at SHA `d3ceef7729a54212cb1958a19299993d02deedec`: 100000 vertices / 3200000 edges, 30000 ms backup interval, 200 offered calls/s per named read and 50 writes/s; 3/3 periodic attempts completed. Result: `not_fired`. A window needs 100 successful calls and no errors; otherwise its p99 is shown for context but the comparison is inconclusive. A failed attempt breaks a three-tick streak.

| periodic tick | duration ms | materialization ms | send ms | finalization ms | vertices / edges |
| --- | ---: | ---: | ---: | ---: | ---: |
| `1790226626945322000` | 2448.32 | 645.75 | 1782.82 | 19.19 | 100000 / 3200000 |
| `1790226656944575000` | 2478.12 | 676.13 | 1778.30 | 8.83 | 100000 / 3200000 |
| `1790226686943810000` | 2382.57 | 872.56 | 1473.54 | 2.75 | 100000 / 3200000 |

| periodic tick | producer | before p99 ms / count / error rate | during p99 ms / count / error rate | after p99 ms / count / error rate | comparable |
| --- | --- | ---: | ---: | ---: | --- |
| `1790226626945322000` | `GetVertex` | 3.68 / 487 / 0.000 | 1.00 / 484 / 0.000 | 6.45 / 484 / 0.000 | true |
| `1790226626945322000` | `ScanVertices` | 4.14 / 488 / 0.000 | 1.25 / 360 / 0.000 | 6.72 / 486 / 0.000 | true |
| `1790226626945322000` | `Illuminate` | 4.37 / 487 / 0.000 | 1.14 / 361 / 0.000 | 6.72 / 481 / 0.000 | true |
| `1790226626945322000` | `PutVertex` | 4.11 / 122 / 0.000 | 627.12 / 92 / 0.000 | 5.71 / 122 / 0.000 | false |
| `1790226656944575000` | `GetVertex` | 2.87 / 495 / 0.000 | 1.14 / 489 / 0.000 | 1.09 / 496 / 0.000 | true |
| `1790226656944575000` | `ScanVertices` | 2.67 / 495 / 0.000 | 1.43 / 357 / 0.000 | 1.24 / 496 / 0.000 | true |
| `1790226656944575000` | `Illuminate` | 2.50 / 494 / 0.000 | 1.48 / 357 / 0.000 | 1.24 / 496 / 0.000 | true |
| `1790226656944575000` | `PutVertex` | 3.01 / 124 / 0.000 | 672.24 / 91 / 0.000 | 1.24 / 124 / 0.000 | false |
| `1790226686943810000` | `GetVertex` | 6.64 / 434 / 0.000 | 1.27 / 464 / 0.000 | 3.56 / 476 / 0.000 | true |
| `1790226686943810000` | `ScanVertices` | 6.72 / 434 / 0.000 | 1.64 / 292 / 0.000 | 3.87 / 476 / 0.000 | true |
| `1790226686943810000` | `Illuminate` | 6.49 / 436 / 0.000 | 1.88 / 292 / 0.000 | 3.28 / 476 / 0.000 | true |
| `1790226686943810000` | `PutVertex` | 6.95 / 120 / 0.000 | 867.12 / 77 / 0.000 | 3.20 / 119 / 0.000 | false |

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
