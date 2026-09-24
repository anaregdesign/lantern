# Bench report — `periodic_backup` @ `20260924T051256Z`

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

Synthetic host run at SHA `d3ceef7729a54212cb1958a19299993d02deedec`: 100000 vertices / 3200000 edges, 30000 ms backup interval, 200 offered calls/s per named read and 150 writes/s; 3/3 periodic attempts completed. Result: `not_fired`. A window needs 100 successful calls and no errors; otherwise its p99 is shown for context but the comparison is inconclusive. A failed attempt breaks a three-tick streak.

| periodic tick | duration ms | materialization ms | send ms | finalization ms | vertices / edges |
| --- | ---: | ---: | ---: | ---: | ---: |
| `1790226821904278000` | 1793.00 | 540.81 | 1247.53 | 3.51 | 100000 / 3200000 |
| `1790226851903503000` | 1949.36 | 594.48 | 1347.14 | 5.94 | 100000 / 3200000 |
| `1790226881902750000` | 1697.35 | 551.16 | 1140.95 | 4.93 | 100000 / 3200000 |

| periodic tick | producer | before p99 ms / count / error rate | during p99 ms / count / error rate | after p99 ms / count / error rate | comparable |
| --- | --- | ---: | ---: | ---: | --- |
| `1790226821904278000` | `GetVertex` | 5.63 / 357 / 0.000 | 1.06 / 351 / 0.000 | 5.35 / 359 / 0.000 | true |
| `1790226821904278000` | `ScanVertices` | 5.57 / 357 / 0.000 | 1.07 / 246 / 0.000 | 5.39 / 359 / 0.000 | true |
| `1790226821904278000` | `Illuminate` | 5.55 / 357 / 0.000 | 1.21 / 246 / 0.000 | 5.66 / 359 / 0.000 | true |
| `1790226821904278000` | `PutVertex` | 4.97 / 268 / 0.000 | 1.10 / 184 / 0.000 | 3.41 / 268 / 0.000 | true |
| `1790226851903503000` | `GetVertex` | 3.29 / 386 / 0.000 | 0.99 / 381 / 0.000 | 5.32 / 390 / 0.000 | true |
| `1790226851903503000` | `ScanVertices` | 3.13 / 386 / 0.000 | 1.27 / 265 / 0.000 | 5.46 / 390 / 0.000 | true |
| `1790226851903503000` | `Illuminate` | 3.90 / 386 / 0.000 | 1.26 / 265 / 0.000 | 4.94 / 390 / 0.000 | true |
| `1790226851903503000` | `PutVertex` | 3.27 / 291 / 0.000 | 1.14 / 200 / 0.000 | 5.28 / 292 / 0.000 | true |
| `1790226881902750000` | `GetVertex` | 3.90 / 337 / 0.000 | 0.91 / 331 / 0.000 | 3.61 / 337 / 0.000 | true |
| `1790226881902750000` | `ScanVertices` | 4.29 / 337 / 0.000 | 1.15 / 224 / 0.000 | 4.21 / 337 / 0.000 | true |
| `1790226881902750000` | `Illuminate` | 4.27 / 337 / 0.000 | 1.10 / 224 / 0.000 | 5.03 / 337 / 0.000 | true |
| `1790226881902750000` | `PutVertex` | 3.69 / 253 / 0.000 | 1.69 / 168 / 0.000 | 3.11 / 254 / 0.000 | true |

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
