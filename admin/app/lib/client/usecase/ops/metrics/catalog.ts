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
   * A PromQL expression. May contain the literal token `$__rate`, which the
   * selector replaces with a concrete range-vector window (e.g. `1m`).
   */
  expr: string;
  /**
   * Optional legend template. `{{label}}` tokens are filled from each
   * returned series' label set. When omitted the legend is derived from the
   * series labels (or the panel title for single-series queries).
   */
  legend?: string;
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
        expr: "sum by (grpc_method) (rate(grpc_server_handled_total[$__rate]))",
        legend: "{{grpc_method}}",
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
        expr: "histogram_quantile(0.5, sum by (le) (rate(grpc_server_handling_seconds_bucket[$__rate])))",
        legend: "p50",
      },
      {
        expr: "histogram_quantile(0.99, sum by (le) (rate(grpc_server_handling_seconds_bucket[$__rate])))",
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
        expr: "histogram_quantile(0.99, sum by (le) (rate(lantern_illuminate_duration_seconds_bucket[$__rate])))",
        legend: "illuminate",
      },
      {
        expr: "histogram_quantile(0.99, sum by (le, op) (rate(lantern_scan_duration_seconds_bucket[$__rate])))",
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
        expr: "histogram_quantile(0.99, sum by (le) (rate(lantern_gc_duration_seconds_bucket[$__rate])))",
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
        expr: "sum by (kind) (rate(lantern_ttl_expirations_total[$__rate]))",
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
    queries: [{ expr: "lantern_peer_connected", legend: "{{peer}}" }],
  },
  {
    id: "replication-apply",
    group: "replication",
    title: "Replication apply / drop rate",
    description: "Mutations applied (by op) and dropped per second.",
    unit: "rate",
    queries: [
      {
        expr: "sum by (op) (rate(lantern_replication_apply_total[$__rate]))",
        legend: "apply {{op}}",
      },
      {
        expr: "sum(rate(lantern_replication_dropped_total[$__rate]))",
        legend: "dropped",
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
        expr: "sum(rate(lantern_validation_rejected_total[$__rate]))",
        legend: "validation",
      },
      {
        expr: "sum(rate(lantern_rate_limit_rejected_total[$__rate]))",
        legend: "rate limit",
      },
      {
        expr: "sum(rate(lantern_tombstone_clamp_rejected_total[$__rate]))",
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
