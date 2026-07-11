/**
 * Declarative catalog of the curated Ops Metrics panels.
 *
 * Each {@link PanelSpec} maps one or more PromQL queries onto a single
 * time-series chart. Expressions use the `$__rate` placeholder wherever a
 * `rate()` window is required; the selector layer
 * (`selectors.ts#resolveExpr`) substitutes a concrete window derived from
 * the active step before the query is sent to Prometheus.
 *
 * Every metric name referenced here is a real series exported by the
 * Lantern server (see `server/provider/*` and `server/metrics/*`) plus the
 * standard `process_*` / `go_*` collectors registered in
 * `NewPrometheusRegistry`. Do not invent metric names — add the exporter
 * first, then a panel.
 */

/** Logical grouping used to lay panels out under section headings. */
export type PanelGroup =
  | "cache"
  | "throughput"
  | "latency"
  | "ttl"
  | "backpressure"
  | "replication"
  | "rejections"
  | "runtime";

/**
 * How a panel's values should be formatted on axes / legends. Drives the
 * value formatter in `selectors.ts`.
 */
export type MetricUnit = "count" | "rate" | "ratio" | "seconds" | "bytes";

export interface PanelQuery {
  /**
   * The **inner** PromQL expression — a raw gauge or a `rate()` series,
   * with **no** outer replica/cluster aggregation. May contain the literal
   * token `$__rate`, which the selector replaces with a concrete
   * range-vector window (e.g. `1m`). The selector layer
   * (`selectors.ts#composeQuery`) wraps this in `sum by (…)` so the same
   * query renders per-replica or as a cluster total depending on the active
   * aggregation mode — keep the aggregation OUT of the catalog.
   */
  expr: string;
  /**
   * Optional legend template. `{{label}}` tokens are filled from each
   * returned series' label set. When omitted the legend is derived from the
   * series labels (or the panel title for single-series queries). In
   * per-replica mode the selector prefixes the resolved label with the
   * series' short replica alias (`r0 · …`).
   */
  legend?: string;
  /**
   * Secondary grouping labels preserved in **both** aggregation modes
   * (e.g. `["grpc_method"]`). The selector adds `instance` to this set in
   * per-replica mode and drops it in cluster-sum mode. Omit (or `[]`) for a
   * single undifferentiated series per replica.
   */
  by?: readonly string[];
  /**
   * How the inner expression is aggregated. `"sum"` (the default) wraps it
   * in `sum by (…)`. `{ quantile }` wraps it in
   * `histogram_quantile(q, sum by (le, …) (…))` for `_bucket` series.
   */
  agg?: "sum" | { quantile: number };
}

export interface PanelSpec {
  /** Stable kebab-case id — React key, reducer key, and `data-testid` stem. */
  id: string;
  group: PanelGroup;
  title: string;
  description: string;
  unit: MetricUnit;
  queries: PanelQuery[];
}

/** Section groups in display order, with human-readable headings. */
export const PANEL_GROUPS: ReadonlyArray<{ id: PanelGroup; label: string }> = [
  { id: "cache", label: "Cache" },
  { id: "throughput", label: "Throughput" },
  { id: "latency", label: "Latency" },
  { id: "ttl", label: "TTL reaping" },
  { id: "backpressure", label: "Back-pressure" },
  { id: "replication", label: "Replication" },
  { id: "rejections", label: "Rejections" },
  { id: "runtime", label: "Process / Go runtime" },
];

/**
 * The curated panel set. Ordering within a group is preserved in the UI.
 */
export const METRIC_PANELS: readonly PanelSpec[] = [
  {
    id: "cache-size",
    group: "cache",
    title: "Cache size",
    description: "Live vertices and edges currently held in the store.",
    unit: "count",
    queries: [
      { expr: "lantern_vertices", legend: "vertices" },
      { expr: "lantern_edges", legend: "edges" },
    ],
  },
  {
    id: "write-throughput",
    group: "throughput",
    title: "Write throughput",
    description: "Mutation-log appends per second.",
    unit: "rate",
    queries: [
      {
        expr: "rate(lantern_mutation_log_entries_total[$__rate])",
        legend: "appends/s",
      },
    ],
  },
  {
    id: "rpc-throughput",
    group: "throughput",
    title: "RPC throughput",
    description: "Completed RPCs per second, by method.",
    unit: "rate",
    queries: [
      {
        expr: "rate(grpc_server_handled_total[$__rate])",
        by: ["grpc_method"],
        legend: "{{grpc_method}}",
      },
    ],
  },
  {
    id: "traversal-outcomes",
    group: "throughput",
    title: "Illuminate outcomes",
    description:
      "Successful, failed, and timed-out traversals by family, phase, and Connect code.",
    unit: "rate",
    queries: [
      {
        expr: "rate(lantern_illuminate_calls_total[$__rate])",
        by: ["algorithm", "phase", "code"],
        legend: "{{algorithm}} · {{phase}} · {{code}}",
      },
    ],
  },
  {
    id: "rpc-latency",
    group: "latency",
    title: "RPC latency (p50 / p99)",
    description: "Server handling-time quantiles across all methods.",
    unit: "seconds",
    queries: [
      {
        expr: "rate(grpc_server_handling_seconds_bucket[$__rate])",
        agg: { quantile: 0.5 },
        legend: "p50",
      },
      {
        expr: "rate(grpc_server_handling_seconds_bucket[$__rate])",
        agg: { quantile: 0.99 },
        legend: "p99",
      },
    ],
  },
  {
    id: "traversal-latency",
    group: "latency",
    title: "Illuminate & scan latency (p99)",
    description: "99th-percentile traversal and scan durations.",
    unit: "seconds",
    queries: [
      {
        expr: "rate(lantern_illuminate_duration_seconds_bucket[$__rate])",
        agg: { quantile: 0.99 },
        legend: "illuminate",
      },
      {
        expr: "rate(lantern_scan_duration_seconds_bucket[$__rate])",
        by: ["op"],
        agg: { quantile: 0.99 },
        legend: "scan {{op}}",
      },
    ],
  },
  {
    id: "gc-duration",
    group: "latency",
    title: "GC duration (p99)",
    description: "99th-percentile graph-cache GC sweep duration.",
    unit: "seconds",
    queries: [
      {
        expr: "rate(lantern_gc_duration_seconds_bucket[$__rate])",
        agg: { quantile: 0.99 },
        legend: "p99",
      },
    ],
  },
  {
    id: "ttl-reaping",
    group: "ttl",
    title: "TTL reaping",
    description: "Expired vertices and edges reaped per second, by kind.",
    unit: "rate",
    queries: [
      {
        expr: "rate(lantern_ttl_expirations_total[$__rate])",
        by: ["kind"],
        legend: "{{kind}}",
      },
    ],
  },
  {
    id: "mutation-log-fill",
    group: "backpressure",
    title: "Mutation-log fill ratio",
    description: "Fraction of the mutation-log ring buffer in use (0–1).",
    unit: "ratio",
    queries: [
      { expr: "lantern_mutation_log_fill_ratio", legend: "fill ratio" },
    ],
  },
  {
    id: "mutation-log-eviction",
    group: "backpressure",
    title: "Mutation-log eviction rate",
    description: "Entries evicted from the mutation log per second.",
    unit: "rate",
    queries: [
      {
        expr: "rate(lantern_mutation_log_evicted_total[$__rate])",
        legend: "evicted/s",
      },
    ],
  },
  {
    id: "subscribe-streams",
    group: "backpressure",
    title: "Active subscribe streams",
    description: "Open replication Subscribe streams.",
    unit: "count",
    queries: [{ expr: "lantern_subscribe_active_streams", legend: "streams" }],
  },
  {
    id: "replication-lag",
    group: "replication",
    title: "Replication lag",
    description: "Per-peer sequence lag behind each origin.",
    unit: "count",
    queries: [
      {
        expr: "lantern_replication_lag_seq",
        by: ["peer", "origin"],
        legend: "{{peer}} ← {{origin}}",
      },
    ],
  },
  {
    id: "peer-connected",
    group: "replication",
    title: "Peer connectivity",
    description: "1 when a peer link is up, 0 when down.",
    unit: "count",
    queries: [
      { expr: "lantern_peer_connected", by: ["peer"], legend: "{{peer}}" },
    ],
  },
  {
    id: "replication-apply",
    group: "replication",
    title: "Replication apply / drop rate",
    // The apply counter also carries an `op` label (11 RPC kinds). Breaking
    // the panel down by `op` multiplies by replica count (33+ lines here) and
    // is illegible as a legend; per-op apply rates belong in an ad-hoc query,
    // not an at-a-glance health panel. We sum over `op` to show one applied
    // line per replica, and keep the low-cardinality `reason` split on drops
    // (where "why" is the actionable signal — e.g. self_echo suppression).
    description: "Remote mutations applied per replica, and drops by reason.",
    unit: "rate",
    queries: [
      {
        expr: "rate(lantern_replication_apply_total[$__rate])",
        legend: "applied",
      },
      {
        expr: "rate(lantern_replication_dropped_total[$__rate])",
        by: ["reason"],
        legend: "dropped {{reason}}",
      },
    ],
  },
  {
    id: "rejections",
    group: "rejections",
    title: "Rejections",
    description:
      "Validation, rate-limit, and tombstone-clamp rejections per second.",
    unit: "rate",
    queries: [
      {
        expr: "rate(lantern_validation_rejected_total[$__rate])",
        legend: "validation",
      },
      {
        expr: "rate(lantern_rate_limit_rejected_total[$__rate])",
        legend: "rate limit",
      },
      {
        expr: "rate(lantern_tombstone_clamp_rejected_total[$__rate])",
        legend: "tombstone clamp",
      },
    ],
  },
  {
    id: "process-memory",
    group: "runtime",
    title: "Memory",
    description: "Process resident set size and Go heap in-use.",
    unit: "bytes",
    queries: [
      { expr: "process_resident_memory_bytes", legend: "RSS" },
      { expr: "go_memstats_heap_inuse_bytes", legend: "heap in-use" },
    ],
  },
  {
    id: "goroutines",
    group: "runtime",
    title: "Goroutines",
    description: "Live goroutine count.",
    unit: "count",
    queries: [{ expr: "go_goroutines", legend: "goroutines" }],
  },
];
