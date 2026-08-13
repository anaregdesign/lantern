# Observability design — metrics, logs & traces

> Status: **Draft** — owner-authored design note.
> Companions: [`replication.md`](replication.md) (the HA model the signals
> describe) and [`ha-runbook.md`](ha-runbook.md) (the operator runbook).
> The runbook's "what to watch" is the operational quick-reference; this
> document is the *why* behind it — who needs to answer what, and which
> signals earn their keep.
> Pre-v1.0.0: signal names (metric names, log keys, span names) are **not**
> stable. Prefer the cleanest design over backward compatibility, with one
> deliberate exception — the `grpc_server_*` RPC metric names are frozen for
> operator continuity (see §5.2).

---

## 1. Purpose & scope

Lantern is an in-memory graph KVS: a **leaderless, full-replica** cluster
(any pod accepts any read/write; writes fan out asynchronously to every peer),
holding vertices **and** edges that carry TTLs and **decay** over time, served
over Connect/HTTP-2. That shape dictates what is worth observing — there is no
leader to watch, no quorum to count, no disk to fsync; instead the interesting
questions are *"are the replicas converging?"*, *"is anything decaying that
shouldn't?"*, and *"is any single node falling behind?"*.

This document organises observability from the **application owner's seat**.
It deliberately starts from *who needs to answer what* (§2–§3), then derives
the signal catalogue (§5–§7), and only then judges which signals are worth
carrying (§8) and which are deliberately *not* built (§9). Current
implementation is cited throughout as the **baseline, not the boundary** —
where a useful signal is missing it is named in §8 rather than omitted.

**In scope:** the three pillars (metrics, logs, traces) plus health, framed
conceptually and mapped onto Lantern's subsystems.

**Out of scope:** concrete dashboards and alert thresholds (they live in the
runbook), the choice of metrics backend / exporter, SLO target numbers, and
any code or chart change — this is a design note, not an implementation.

---

## 2. Personas

Who consumes Lantern's signals, and the lens each one brings:

| # | Persona | Core question | Horizon | Primary pillar |
|---|---|---|---|---|
| P1 | **Cluster operator / SRE** | Is the cluster healthy, and is it safe to roll / scale? | now → hours | metrics + health |
| P2 | **On-call responder** | What broke, where, and what is the blast radius? | minutes | logs + traces |
| P3 | **Application developer** (SDK / CLI / MCP) | Are my reads/writes correct and fast; is my data still there? | now → days | metrics + traces |
| P4 | **Capacity / cost owner** | How is load growing, and what does the telemetry itself cost? | days → weeks | metrics (trends) |
| P5 | **Maintainer / perf engineer** | Are there leaks, regressions, or pathological internals? | release → release | metrics + traces |

These personas overlap in a small deployment (one person wears all five hats),
but separating the *questions* keeps the signal catalogue honest: every signal
should trace back to at least one persona's question.

---

## 3. User stories

The catalogue in §5–§7 exists to answer these. Each story is tagged with the
signal that answers it primarily; corroborating signals are noted in §5.

**P1 — Cluster operator / SRE**

- As an operator, I want to know **every pod is up and serving** so I can trust
  the cluster. → liveness (`up`, the `grpc.health.v1.Health` / `/healthz` /
  `/readyz` probes, `lantern_build_info`, and uptime derived from
  `process_start_time_seconds`).
- As an operator, I want to know **a rolling update recovered** — the new pod
  rejoined and re-synced — before I proceed to the next pod. →
  `lantern_peer_connected`, `lantern_snapshot_replayed_total`,
  `lantern_vertices` / `lantern_edges` re-converging.
- As an operator, I want to know **a pod death healed** (peer lost, then
  re-applied the backlog) without my intervention. → `lantern_peer_connected`
  (1→0→1), `lantern_replication_apply_total`.
- As an operator, I want **early warning that a replica is falling behind**
  before clients see stale reads. → `lantern_replication_lag_seq`,
  `lantern_anti_entropy_gaps_found_total`, `lantern_mutation_log_fill_ratio`.

**P2 — On-call responder**

- As a responder, I want to know **which RPC is erroring and with what code**
  so I can localise a fault. → `grpc_server_handled_total{grpc_code}`.
- As a responder, I want to know **why a write was rejected** (bad input, rate
  limit, tombstone clamp) so I can tell client bug from server back-pressure. →
  `lantern_validation_rejected_total{reason}`, `lantern_rate_limit_rejected_total`,
  `lantern_tombstone_clamp_rejected_total` + the matching slog lines.
- As a responder, I want to know **whether replicas diverged** and against which
  peer/origin. → `lantern_anti_entropy_gaps_found_total{peer,origin}` + the
  `anti-entropy: gap exceeds warn threshold` log.
- As a responder, I want a **per-request causal latency breakdown** for a slow
  call. → traces (§7).

**P3 — Application developer**

- As a developer, I want to know my **read recall** — how often a key I ask for
  is actually present (vs expired/absent). → `lantern_get_vertex_hits_total` /
  `_misses_total`, `lantern_get_edge_hits_total` / `_misses_total`.
- As a developer, I want to know **how aggressively my data is decaying** so I
  can tune TTLs. → `lantern_ttl_expirations_total{kind}`, `lantern_vertices` /
  `lantern_edges` trend.
- As a developer, I want **p50/p99 latency for the method I call** so I can size
  timeouts. → `grpc_server_handling_seconds{grpc_method}`.
- As a developer, I want to know **my `Subscribe`/CDC consumer isn't dropping
  events**. → `lantern_subscribe_dropped_total`,
  `lantern_subscription_dropped_total{policy}`, `lantern_mutation_log_evicted_total`.

**P4 — Capacity / cost owner**

- As a cost owner, I want **graph-size and throughput trends** to plan memory. →
  `lantern_vertices` / `lantern_edges`, the matching
  `lantern_*_causal_barrier_entries`, `process_resident_memory_bytes`, and
  `grpc_server_started_total` rate.
- As a cost owner, I want to know **what the telemetry itself costs** (series
  count) so I can prune. → cardinality of the scrape (see §10).

**P5 — Maintainer / perf engineer**

- As a maintainer, I want to catch **memory / map leaks** across releases. →
  `lantern_vertex_hlc_entries` / `_high_water`, `go_memstats_heap_inuse_bytes`,
  `go_goroutines`.
- As a maintainer, I want to distinguish an accidental leak from the deliberate
  indefinite retention of hidden Put floors. →
  `lantern_vertex_causal_barrier_entries`,
  `lantern_edge_causal_barrier_entries`.
- As a maintainer, I want to know **GC and hot-path cost** under load. →
  `lantern_gc_duration_seconds`, `lantern_illuminate_duration_seconds`,
  `lantern_scan_duration_seconds`.

---

## 4. The three pillars — division of labour

| Pillar | Shape | Answers | Cost driver | Don't use it for |
|---|---|---|---|---|
| **Metrics** | aggregated time series, bounded labels | "how much / how often / how fast / how full" — alerting & trends | series cardinality | per-event detail, anything keyed by a value |
| **Logs** | discrete, timestamped, context-rich events | "what exactly happened, with which inputs" — the triage grep target | volume × retention | counting (use a metric), high-frequency hot paths |
| **Traces** | causal spans across a request / propagation path | "where did the latency go, across which hop" | span volume × sampling | steady-state rates, sub-µs internal hops |
| **Health** | binary liveness / readiness | "should the orchestrator route to / restart this pod" | — | anything graded or historical |

**Guiding rule.** Every user story in §3 should have **one** primary pillar;
the others corroborate. The runbook already leans on this — e.g. replication
divergence *alerts* on a metric (`anti_entropy_gaps_found_total`) but is
*triaged* on a log (`anti-entropy: gap exceeds warn threshold`). Resist
re-implementing a log's job as a high-cardinality metric label, or a metric's
job as a log you have to count.

**Method lenses.** Two standard frames map cleanly onto Lantern:

- **RED** (Rate, Errors, Duration) for the request-driven data plane → §5.2.
- **USE** (Utilization, Saturation, Errors) for bounded resources — the
  mutation-log ring buffer, the subscribe fan-out, the heap → §5.4, §5.9.

The **four golden signals** (latency, traffic, errors, saturation) are the
operator-facing subset; §10 distils the catalogue down to them for alerting.

---

## 5. Metrics taxonomy

Organised by subsystem. Each group states the **question it answers**, the
**persona**, and its **role** (liveness = page on it; RCA = reach for it during
triage; capacity = trend it). Metric names below are the *current* ground truth
(`server/metrics/metrics.go` and `server/provider/connect_middleware.go`);
proposed gaps are deferred to §8. The exhaustive per-metric reference table
lives in [`../server/README.md`](../server/README.md) — this section is the
conceptual map, not a duplicate of it.

### 5.1 Identity & liveness — "is this the build I think it is, and is it up?"

*Persona P1 · role: liveness.* `lantern_build_info{version,commit,go_version}`
(always 1; the labels carry the truth), `process_start_time_seconds` (→ uptime),
the scrape's own `up`, and the `grpc.health.v1.Health` / `/healthz` / `/readyz`
endpoints (§ health). These are the cheapest, highest-value signals: a missing
`up` or a flapping `/readyz` is the first thing a pager should fire on.

### 5.2 Data plane / RPC — RED for every call

*Persona P2, P3 · role: liveness (errors) + RCA + capacity.* The Connect
interceptor reproduces the canonical **`grpc_server_*`** family — names
**frozen** for operator continuity even though the wire protocol is Connect, not
gRPC (the historical `grpc-ecosystem` middleware was removed in #337/#352 but
its vocabulary is an operator contract):

- `grpc_server_started_total{grpc_type,grpc_service,grpc_method}` — **traffic**.
- `grpc_server_handled_total{grpc_type,grpc_service,grpc_method,grpc_code}` —
  **traffic + errors** (the `grpc_code` label is the error signal:
  `not_found`, `resource_exhausted`, `invalid_argument`, …).
- `grpc_server_handling_seconds{…}` (histogram) — **duration** (p50/p99).

`grpc_type` is hard-coded `unary`; the streaming RPCs (`Subscribe`, `Snapshot`)
are **not** metered here — they are visible only as listener-level spans (§7)
and via the subscribe/snapshot domain metrics (§5.4). That asymmetry is a known
gap (§8).

### 5.3 Graph state & TTL decay — "what's in memory, and what's leaving?"

*Persona P3, P4 · role: capacity + RCA.* `lantern_vertices`, `lantern_edges`
(live counts — the headline gauges, but not the admission-cap footprint),
`lantern_vertex_causal_barrier_entries` /
`lantern_edge_causal_barrier_entries` (retained Put floors),
`lantern_ttl_expirations_total{kind=vertex|edge|dangling_edge}`
(the decay rate — a sudden spike means a TTL cliff), and
`lantern_gc_duration_seconds` (the cost of reaping). Together they answer "is
the working set growing, decaying, or churning, and is GC keeping up?". For
capacity alerting, sum each live gauge and its matching barrier gauge; equal
cardinalities across pods are a drift clue, not proof that identities/values
match.

### 5.4 Replication & HA — the heart of a leaderless cluster

*Persona P1, P2 · role: liveness + RCA.* This is where Lantern's signals matter
most, because correctness is *emergent* from convergence rather than enforced by
a leader. Grouped by concern:

- **Peering (liveness).** `lantern_peer_connected{peer}` — 1 while the local
  pump holds a `Subscribe` to `peer`, 0 after disconnect, back to 1 on
  reconnect. The single clearest "a peer died / healed" edge.
  `lantern_subscribe_active_streams` is the inbound counterpart.
- **Apply throughput (RCA).** `lantern_replication_apply_total{op}` (per
  received `MutationOp`) and
  `lantern_replication_applied_total{origin}` (per originating node). After a
  pod restart, watching every `op` row refill is how you *prove* the backlog
  re-applied. Accepted Put batches appear as `op="ReplicatedPutVertices"` or
  `op="ReplicatedPutEdges"`: each observation is one ordered authoritative
  batch, not one item. Live and causal-barrier entries remain interleaved in
  origin-accepted order, so metrics must not infer item outcomes or batch size
  from this counter alone.
- **Lag & convergence (liveness).** `lantern_replication_lag_seq{peer,origin}`
  (in mutation-**seq** units, not seconds — set from `PeerStatus` probes) and
  `lantern_anti_entropy_cycles_total` / `lantern_anti_entropy_gaps_found_total{peer,origin}`.
  A rising gap is *the* divergence alert.
- **Mutation-log back-pressure (saturation — USE).** `lantern_mutation_log_capacity`,
  `lantern_mutation_log_fill_ratio` (≥ 0.8 sustained ⇒ slow subscribers risk
  `ErrGapped` and a forced `Snapshot` reseed), `lantern_mutation_log_entries_total`,
  `lantern_mutation_log_evicted_total`, and the subscriber-drop counters
  `lantern_subscribe_dropped_total{reason}` /
  `lantern_mutationlog_subscriber_dropped_total{cause}`.
- **Bootstrap / reseed (RCA).** `lantern_snapshot_replayed_total{peer}` and the
  `lantern_snapshot_{vertices,edges,duration_seconds}{peer}` histograms — how a
  fresh or gapped pod re-seeded, and how expensive it was. Replication Snapshot
  migrates hidden expired/dangling Put floors and captures barrier plus live
  graph state atomically. A live additive edge coexisting with a barrier is
  emitted at the maximum bucket/barrier HLC so bootstrap cannot reopen an
  older-Put resurrection window.
- **Convergence boundary (RCA).** Zero lag and matching cardinality gauges do
  not prove equal edge weights for histories that arbitrarily mix Put/Add or
  Delete/Add delivery. Those histories remain unsupported pending
  [#1203](https://github.com/anaregdesign/lantern/issues/1203); compare the
  application-visible edge when diagnosing one, then quiesce it and reseed
  from the authoritative replica.
- **Dropped frames (RCA).** `lantern_replication_dropped_total{peer,reason}`
  (reasons: `self_echo`, `subscribe_failed`, `snapshot_failed`, `dial_failed`,
  `peerstatus_failed`, `catchup_failed`, `discovery_failed`, `ctx_cancel`, `clean`),
  `lantern_origin_states_count` (distinct writers ever seen ≈ peer-set size).

### 5.5 In-process pub/sub (client CDC) — "are local subscribers keeping up?"

*Persona P3 · role: RCA + saturation.* Distinct from the cross-pod replication
streams: `lantern_subscription_queue_depth` (histogram),
`lantern_subscription_dropped_total{policy=drop_newest|drop_oldest|drop_newest_after_oldest}`,
`lantern_subscription_dispatch_duration_seconds`. These answer whether a slow
CDC consumer is shedding events and under which `FullPolicy`.

### 5.6 Admission control & back-pressure — "what did we refuse, and why?"

*Persona P2, P3, P4 · role: RCA + saturation.*
`lantern_validation_rejected_total{reason}` (the
bounded reason set: `empty_key`, `key_too_long`, `empty_batch`,
`batch_too_large`, `nil_item`, `bad_weight`, `step_too_large`, `k_too_large`,
`bad_ttl`, `bad_cursor`, `capacity`, `empty_edge_prefix`, `order_mismatch`, plus
`unknown`), `lantern_rate_limit_rejected_total`
(token-bucket `ResourceExhausted`; registered even when the limiter is off so
deployments compare uniformly), `lantern_tombstone_clamp_rejected_total` (LWW
lost against a live tombstone, retained barrier, or newer live floor — a
sustained rate flags clock skew or causally-late frames). The first separates
*client bug* from *server saturation*; the last is a subtle correctness signal
unique to the HLC model.

For `reason="capacity"`, compare `lantern_capacity_limit{kind}` with live plus
matching causal-barrier gauges. The cap excludes Delete tombstones and is
enforced only at local write admission; replication and restore can exceed it.
An equal/newer exact Delete moves a barrier floor into a D4-bounded tombstone
and frees the admission slot, while prefix Delete cannot see a barrier-only
identity. Tombstones still consume heap, and no separate hard causal-metadata
count/byte budget exists yet; that follow-up is
[#1204](https://github.com/anaregdesign/lantern/issues/1204).

### 5.7 Query subsystems — illuminate / scan / search

*Persona P3, P5 · role: RCA + capacity.* Hot-path histograms, label-pre-warmed
so dashboards render the full variant set from process start:

- **Illuminate** (graph walk): `lantern_illuminate_visited_vertices` /
  `_visited_edges` / `_duration_seconds`, labelled by the orthogonal axes
  `algorithm × reduction × objective × weighting` (54 combos, #410/#963 —
  `algorithm` is the traversal family, `reduction` the post-traversal tree
  reduction) plus a `phase` (`traversal` | `optimize`) on duration.
- **Scan / count**: `lantern_scan_results{op}`, `lantern_scan_duration_seconds{op}`.
- **Search** (optional index): every handler exit produces exactly one
  `lantern_search_calls_total` observation with bounded request dimensions
  (`mode`, `phrase`, `fuzziness`, prefix booleans) and terminal
  `outcome`/`reason`. `lantern_search_duration_seconds{mode,outcome}` and
  `lantern_search_results{mode,outcome}` include successes, zero-hit calls,
  validation failures, admission failures, budget exhaustion, cancellation,
  deadlines, capability/index failures, and internal errors.
  `lantern_search_phase_duration_seconds{phase,mode}` separates `analysis`,
  `expansion`, and `selection`. `lantern_search_work{kind,mode}` separates
  query bytes/tokens/clauses, expansion/dictionary work, posting/position/
  expiration visits, and candidate selection. Together they answer whether a
  slow query grew before candidate retrieval, inside the index, or during
  ranking.
- **Search index health**: logical/physical/expired document gauges, live and
  retained structure sizes, `lantern_search_index_retained_ratio`, rebuild and
  purge gauges, and one-hot
  `lantern_search_index_state{state=disabled|healthy|incomplete}` expose
  retention leaks, degraded indexes, and rebuild activity without pprof.
  `lantern_search_config_match{peer}` remains the cross-replica configuration
  check. Raw query text, prefix values, matched keys, and value snippets are
  never metric labels.
- **Batch shape**: `lantern_batch_size{op}` — the plural-RPC item count, so a
  "slow write" can be split into "large batch" vs "slow per-item".

**Search SLO templates.** Use `outcome="ok"` (including `reason="no_hits"`)
as the successful request population and keep client/capability rejection
budgets separate from server reliability:

```promql
# Availability/error budget: internal + deadline failures among all attempts.
sum(rate(lantern_search_calls_total{outcome=~"internal|deadline_exceeded"}[30d]))
/
sum(rate(lantern_search_calls_total[30d]))

# Latency SLO: p99 of successful calls by request mode.
histogram_quantile(
  0.99,
  sum by (le, mode) (
    rate(lantern_search_duration_seconds_bucket{outcome="ok"}[5m])
  )
)

# Saturation budget, kept separate so traffic shaping does not masquerade as
# an availability failure.
sum(rate(lantern_search_rejections_total{reason=~"admission|.*_visits|query_(bytes|terms)"}[5m]))
/
sum(rate(lantern_search_calls_total[5m]))
```

Set the latency objective below `LANTERN_SEARCH_TIMEOUT_MS` and derive the
availability/saturation thresholds from the product's error budget. The Admin
Ops Search group renders these per replica by default, so a single degraded
replica is not hidden by cluster aggregation.

### 5.8 Recall quality & dedup — "is the cache actually answering?"

*Persona P3 · role: RCA + product-health.* `lantern_get_vertex_hits_total` /
`_misses_total` and the edge pair give recall ratio = `hits/(hits+misses)`; a
falling ratio means TTLs are too short or clients are asking for the wrong keys.
`lantern_edge_contrib_deduped_total` confirms idempotent `AddEdge` retries are
being suppressed (not double-counting weight, #588).

### 5.9 Internal health — leaks, GC, runtime

*Persona P5 · role: RCA + regression watch.* The per-structure cardinality
gauges `lantern_vertex_hlc_entries` and `lantern_vertex_hlc_entries_high_water`
(the LWW watermark map — monotonic growth across GC ticks was the #700 leak
fingerprint), alongside the standard `go_*` (goroutines, heap, GC) and
`process_*` (RSS, CPU, FDs) collectors. These rarely page, but they are the
first place a maintainer looks when "it gets slower / fatter over days".
The causal-barrier gauges separately track retained Put floors, including live
Put metadata migrated when its payload becomes expired, zero-weight, or
dangling. They are not expected to fall merely because a GC tick ran: each
floor remains until an equal/newer live Put supersedes it or an equal/newer
explicit Delete transitions it into a D4-bounded tombstone. Prefix Delete
cannot make that transition for a barrier-only identity. Sustained growth is a
workload-retention signal rather than a TTL-GC failure (see replication RFC
§4/§8). Tombstones are excluded from these gauges and the admission-cap
footprint, but still consume heap; trend RSS/heap until #1204 supplies an
independent hard metadata budget and matching visibility.

---

## 6. Logs

**Model.** Structured `log/slog`, JSON by default. Two tiers:

1. **Per-RPC** (`LoggingInterceptor`) — one `info` "started call" and one `info`
   "finished call" per unary RPC, with keys deliberately retaining the
   `grpc.*` prefix (`grpc.method`, `grpc.code`, `grpc.duration_ms`) for
   log-search continuity.
2. **Targeted channels** (#223) — discrete operational events worth grepping
   without a metrics stack. The current set (see [`../server/README.md`](../server/README.md)
   for the canonical table):

| Message | Level | Trigger | Key attrs |
|---|---|---|---|
| `slow rpc` | warn | handler exceeded `LANTERN_SLOW_RPC_THRESHOLD_MS` | `method`, `code`, `duration_ms`, `threshold_ms`; Search adds bounded mode/phrase/fuzziness/prefix-presence dimensions |
| `validation rejected` | debug | validation interceptor returned `InvalidArgument` | `reason`, `error` |
| `graph cache: gc tick` | info | every `GraphCache.Watch` tick | `vertices_expired`, `edges_expired`, `dangling_edges_removed`, `vertices_remaining`, `edges_remaining`, `duration_ms` |
| `anti-entropy: gap exceeds warn threshold` | warn | per-origin gap over threshold | `origin`, `gap`, `threshold` |
| `replication pump: peer transition` | info/warn | connect / disconnect / snapshot start / finish | `peer`, `transition`, `reason` |
| `pubsub: drop {newest,oldest}` | warn | subscription channel full | drop policy context |

**What logs answer that metrics can't.** The *specific* input that tripped a
rejection, the *exact* peer transition sequence during a flap, the error string
behind a code. Every targeted channel is paired with a metric (the metric
alerts; the log explains) — that pairing is the design intent, not redundancy.

**Correlation.** Attach bounded, joinable context: `grpc.method`, `grpc.code`,
`peer`, `origin`, and at most a **key *prefix*** — never the full key, and
**never the value**. Today logs carry no trace/span id, so logs↔traces cannot be
joined; closing that is a §8 proposal.

**Levels policy.** `error` = operator must act; `warn` = degraded but
self-healing (slow rpc, peer flap, drops); `info` = lifecycle + per-call;
`debug` = high-volume detail (validation rejects) off by default. **What not to
log:** full keys/values (cardinality + sensitivity), anything per-mutation on
the apply hot path (use a counter), secrets/TLS material.

---

## 7. Traces

**Model.** OpenTelemetry via `otelhttp.NewHandler` wrapped around the h2c
listener (`server/provider/lantern_listener.go`), so **every** request —
Connect, gRPC, gRPC-Web, health, reflection — gets a server-side span with no
per-handler wiring. Export is **opt-in and zero-overhead when off**: a provider
is installed only when `OTEL_EXPORTER_OTLP_ENDPOINT` (or the trace-specific
variant) is set; otherwise the global tracer stays noop. `OTEL_EXPORTER_OTLP_PROTOCOL`
selects `grpc` (default) or `http/protobuf`; W3C `tracecontext` + `baggage`
propagation is installed so spans link across services; the provider is flushed
on graceful shutdown (`server/provider/tracing.go`).

**What traces answer that metrics/logs can't.** The **causal latency
breakdown** of a single slow call, and — uniquely for Lantern — the propagation
path of a write as it crosses peers. Metrics tell you p99 rose; a trace tells
you *which hop* (handler vs lock vs fan-out) owns the regression.

**Current coverage & gaps.** Today there is exactly **one span per request** at
the listener; there are **no internal child spans** (illuminate phases, the
apply path, snapshot replay) and the streaming RPCs get only the outer span.
`SearchVertices` enriches that existing span with bounded request dimensions,
terminal outcome/reason, deterministic work counts, and
`analysis_duration_seconds` / `expansion_duration_seconds` /
`selection_duration_seconds`. That makes a slow search attributable without
creating hot-path child spans. Other RPCs still answer only "which RPC was
slow"; selective phase attributes or child spans remain a §8 candidate.

**When tracing earns its keep — and when it doesn't.** Lantern's hot paths are
sub-millisecond in-memory operations; a span per internal hop would cost more
than it reveals and inflate export volume. The high-value targets are the
**slow, branchy paths** (illuminate traversal+optimize, snapshot replay) and the
**cross-peer propagation** path, sampled at a low rate. The steady-state
per-mutation apply loop is explicitly **not** a tracing target (§9) — its health
is a counter (`lantern_replication_apply_total`), not a span.

---

## 8. Ideal vs current — proposed additions

Not bound by the current implementation. Each candidate names the question it
unlocks, the persona, the cost, and a verdict. **None are implemented here** —
they are the backlog this design argues for, each worth its own issue.

| # | Candidate signal | Question it answers | Persona | Cost | Verdict |
|---|---|---|---|---|---|
| A | **Replication propagation latency** (wall-clock, from write-accept on A to apply on B; derive from the HLC delta) | "How stale *in seconds* can a replica be?" — `lag_seq` only gives seq distance | P1 | medium (plumb HLC time into the apply hook) | **worth it** |
| B | **Per-method in-flight gauge** (`grpc_server_in_flight{grpc_method}`) | "Which handler is stuck / saturating right now?" | P2 | low (bounded by method count) | **worth it** |
| C | **trace_id / span_id in slog records** (from context) | "Jump from this log line to its trace" — joins two pillars | P2 | low | **worth it** |
| D | **Selective internal spans** (illuminate phases, snapshot replay) | "Which *phase* of a slow call owns the latency" | P3, P5 | medium; sample low | **worth it (selective)** |
| E | **Backup / restore metrics** (snapshot-to-disk count/bytes/duration, restore outcome) — currently a durability blind spot in metrics (logs only) | "Did the periodic dump succeed; did restore-on-start replay?" | P1 | low | **worth it** |
| F | **Subscribe fan-out backlog gauge** (per-subscriber lag, ahead of `ErrGapped`) | "Which subscriber is about to gap?" — earlier than the drop counter | P1, P3 | low (per-peer, bounded) | **maybe** |
| G | **Readiness-transition counter** (`/readyz` flips) | "Is readiness flapping?" — alert on the edge, not the level | P1 | low | **maybe** |
| H | **Cross-replica divergence gauge** (applied-seq spread per origin) | "Are replicas in sync, in one query?" | P1 | low–med | **maybe** — likely a Prometheus recording rule over existing signals, not a new metric |
| I | **Value-kind distribution** (mix of stored value types) | "What kind of data lives here?" | P4 | med (risk of cardinality) | **defer** — product analytic, better as a sampled log/exemplar than a metric |

Verdicts are deliberately conservative: B, C, E are cheap wins; A and D are the
high-value-but-non-trivial ones; H argues *against* a new metric when a derived
rule suffices.

---

## 9. Anti-patterns — signals we deliberately don't build

Choosing **not** to implement is part of the design. These are ruled out on
purpose:

- **Per-key / per-vertex / per-edge metric labels.** Unbounded cardinality —
  the fastest way to melt a Prometheus. Key-shaped questions belong in logs (as
  a *prefix*) or traces, never in a label.
- **Labels that grow with data, not with topology.** `peer` and `origin` are
  acceptable because they are bounded by cluster size; a label bounded by row
  count is not. New labels must be justified against a fixed, small domain.
- **Full keys or values in logs.** Cardinality *and* sensitivity. Log a prefix
  or a hash; never the payload.
- **A span per internal hot-path hop.** For sub-millisecond in-memory ops the
  span overhead and export volume dwarf the insight. Trace the slow, branchy
  paths only (§7).
- **Metrics with no owner and no action.** If no persona in §2 would alert or
  dashboard on it, it is noise that still costs ingestion. Every signal needs a
  question from §3.
- **Re-implementing a log as a high-cardinality metric** (or a metric as a log
  you have to count). Pick the right pillar once (§4).
- **Server-side per-client-call vanity metrics.** Client-side latency/retry
  accounting belongs in the SDK, not baked into every server label set.

---

## 10. Signal selection — liveness vs RCA vs cost

The catalogue is large by design; consumption should be tiered. Three views:

- **Liveness / alerting (page on these).** The four golden signals, distilled:
  `up` and health probes (availability); `grpc_server_handled_total{grpc_code}`
  error ratio + `grpc_server_handling_seconds` p99 (errors + latency);
  `lantern_peer_connected` and `lantern_replication_lag_seq` /
  `anti_entropy_gaps_found_total` (HA liveness); `lantern_mutation_log_fill_ratio`
  and `lantern_rate_limit_rejected_total` (saturation).
- **RCA / dashboards (reach for these during triage).** Everything in §5.4–§5.8
  plus the per-channel logs (§6) and, when enabled, traces (§7).
- **Capacity / cost (trend these).** `lantern_vertices` / `lantern_edges`, the
  matching causal-barrier gauges, RPC rate,
  `process_resident_memory_bytes`, and the leak gauges (§5.9). Because
  tombstones have no dedicated hard budget/gauge yet, RSS/heap remains the
  backstop for total causal metadata.

**The cost lens.** Counter-intuitively, the generic `go_*` / `process_*` /
`promhttp_*` collectors are *not* the bulk of the scrape. Measured on an idle
single pod they are only ~40 series (~4%), while the `lantern_*` families are
~95% of the ~960-series total — dominated by the illuminate/scan **histograms**,
whose `_bucket` series (`lantern_illuminate_duration_seconds`,
`lantern_illuminate_visited_vertices`, `lantern_illuminate_visited_edges`, plus
the `lantern_scan_*` / `lantern_batch_size` families) account for several hundred
series on their own. So a scrape-time **keep-allowlist** — every `lantern_*` and
`grpc_server_*` (the latter emitted *lazily*, only once the first unary RPC
creates its labelled children) plus a minimal runtime subset (`up`,
`go_goroutines`, `go_memstats_heap_inuse_bytes`, `process_resident_memory_bytes`,
`process_cpu_seconds_total`), dropping the rest — is **signal curation, not a
major ingestion-cost lever**: it removes the generic collectors that carry no
Lantern-specific signal, but since those are only ~4% of series it barely moves
the bill. The real cardinality dial is the histogram bucket layout, not the
collector set. The allowlist still earns its place — every liveness/RCA signal
above survives it — and wiring it into the deployment (a GMP `PodMonitoring`
`metricRelabeling`) is a deployment-layer change, kept out of this design note.

---

## 11. Open questions & next steps

- File one tracking issue per §8 candidate (B/C/E first — cheap wins).
- Decide §8-H (divergence) as metric vs recording rule before building it.
- Land the §10 signal-curation allowlist as a deployment-layer change once the
  signal set here is ratified (it curates the scrape to Lantern-specific signal;
  it is not a major ingestion-cost saving).
- Keep this note in sync with `server/metrics/metrics.go` and the runbook: when
  a signal is added or renamed pre-v1.0.0, amend §5 in the same PR.
