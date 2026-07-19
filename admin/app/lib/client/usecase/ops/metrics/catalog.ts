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
  | "storage"
  | "requests"
  | "illuminate"
  | "search-requests"
  | "search-index"
  | "maintenance"
  | "replication"
  | "guardrails"
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
   * How the inner expression is aggregated. `"sum"` (the default), `"avg"`,
   * `"min"`, or `"max"` wraps it in the matching `… by (…)`. `{ quantile }` wraps it in
   * `histogram_quantile(q, sum by (le, …) (…))` for `_bucket` series.
   */
  agg?: "sum" | "avg" | "min" | "max" | { quantile: number };
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

/** Section groups in display order, with operator-oriented headings. */
export const PANEL_GROUPS: ReadonlyArray<{
  id: PanelGroup;
  label: string;
  description: string;
}> = [
  {
    id: "storage",
    label: "Store inventory",
    description: "Current graph volume and the objects that occupy memory.",
  },
  {
    id: "requests",
    label: "Request traffic",
    description: "RPC volume and latency, separated by operational workload.",
  },
  {
    id: "illuminate",
    label: "Illuminate",
    description: "Traversal latency and terminal outcomes for each algorithm.",
  },
  {
    id: "search-requests",
    label: "Search requests",
    description: "Search outcomes, latency, result volume, and bounded work.",
  },
  {
    id: "search-index",
    label: "Search index",
    description:
      "Index readiness, population, structures, and memory retention.",
  },
  {
    id: "maintenance",
    label: "Maintenance",
    description: "TTL cleanup and graph-cache garbage collection.",
  },
  {
    id: "replication",
    label: "Replication",
    description: "Mutation-log pressure, peer health, lag, applies, and drops.",
  },
  {
    id: "guardrails",
    label: "Guardrails",
    description: "Rejected work separated by the subsystem that refused it.",
  },
  {
    id: "runtime",
    label: "Process / Go runtime",
    description: "Process memory and scheduler load.",
  },
];

/**
 * The curated panel set. Ordering within a group is preserved in the UI.
 */
export const METRIC_PANELS: readonly PanelSpec[] = [
  {
    id: "cache-size",
    group: "storage",
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
    group: "requests",
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
    id: "rpc-read-throughput",
    group: "requests",
    title: "Read RPC throughput",
    description: "Point reads, scans, counts, and degree queries by method.",
    unit: "rate",
    queries: [
      {
        expr: 'rate(grpc_server_handled_total{grpc_method=~"Get(Vertex|Vertices|Edge|Edges)|ScanVertices|ScanVertexKeys|ScanEdges|CountVerticesByPrefix|TopVerticesByDegree"}[$__rate]) > 0',
        by: ["grpc_method"],
        legend: "{{grpc_method}}",
      },
    ],
  },
  {
    id: "rpc-write-throughput",
    group: "requests",
    title: "Write RPC throughput",
    description: "Put, add, and delete operations by method.",
    unit: "rate",
    queries: [
      {
        expr: 'rate(grpc_server_handled_total{grpc_method=~"Put.*|Add.*|Delete.*"}[$__rate]) > 0',
        by: ["grpc_method"],
        legend: "{{grpc_method}}",
      },
    ],
  },
  {
    id: "rpc-query-throughput",
    group: "requests",
    title: "Query RPC throughput",
    description: "Illuminate and SearchVertices completions by method.",
    unit: "rate",
    queries: [
      {
        expr: 'rate(grpc_server_handled_total{grpc_method=~"Illuminate|SearchVertices"}[$__rate]) > 0',
        by: ["grpc_method"],
        legend: "{{grpc_method}}",
      },
    ],
  },
  {
    id: "rpc-status-throughput",
    group: "requests",
    title: "Status RPC throughput",
    description: "Server and replication status probes by method.",
    unit: "rate",
    queries: [
      {
        expr: 'rate(grpc_server_handled_total{grpc_method=~"GetServerStatus|GetReplicationStatus"}[$__rate]) > 0',
        by: ["grpc_method"],
        legend: "{{grpc_method}}",
      },
    ],
  },
  {
    id: "illuminate-outcomes-bfs",
    group: "illuminate",
    title: "Illuminate outcomes · BFS",
    description: "Non-zero terminal BFS outcomes by phase and Connect code.",
    unit: "rate",
    queries: [
      {
        expr: 'rate(lantern_illuminate_calls_total{algorithm="bfs"}[$__rate]) > 0',
        by: ["phase", "code"],
        legend: "{{phase}} · {{code}}",
      },
    ],
  },
  {
    id: "illuminate-outcomes-ppr",
    group: "illuminate",
    title: "Illuminate outcomes · PageRank",
    description:
      "Non-zero terminal Personalized PageRank outcomes by phase and Connect code.",
    unit: "rate",
    queries: [
      {
        expr: 'rate(lantern_illuminate_calls_total{algorithm="ppr"}[$__rate]) > 0',
        by: ["phase", "code"],
        legend: "{{phase}} · {{code}}",
      },
    ],
  },
  {
    id: "illuminate-outcomes-community",
    group: "illuminate",
    title: "Illuminate outcomes · Community",
    description:
      "Non-zero terminal local-community outcomes by phase and Connect code.",
    unit: "rate",
    queries: [
      {
        expr: 'rate(lantern_illuminate_calls_total{algorithm="community"}[$__rate]) > 0',
        by: ["phase", "code"],
        legend: "{{phase}} · {{code}}",
      },
    ],
  },
  {
    id: "rpc-latency",
    group: "requests",
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
    id: "illuminate-latency",
    group: "illuminate",
    title: "Illuminate latency (p99)",
    description: "99th-percentile end-to-end traversal duration.",
    unit: "seconds",
    queries: [
      {
        expr: "rate(lantern_illuminate_duration_seconds_bucket[$__rate])",
        agg: { quantile: 0.99 },
        legend: "p99",
      },
    ],
  },
  {
    id: "scan-latency",
    group: "requests",
    title: "Scan latency (p99)",
    description: "99th-percentile scan duration by operation.",
    unit: "seconds",
    queries: [
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
    group: "maintenance",
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
    id: "search-successes",
    group: "search-requests",
    title: "Search successes",
    description: "Successful and zero-hit searches by matching mode.",
    unit: "rate",
    queries: [
      {
        expr: 'rate(lantern_search_calls_total{outcome="ok"}[$__rate]) > 0',
        by: ["mode", "reason"],
        legend: "{{mode}} · {{reason}}",
      },
    ],
  },
  {
    id: "search-interruptions",
    group: "search-requests",
    title: "Search interruptions",
    description: "Canceled and deadline-exceeded searches by mode.",
    unit: "rate",
    queries: [
      {
        expr: 'rate(lantern_search_calls_total{outcome=~"canceled|deadline_exceeded"}[$__rate]) > 0',
        by: ["mode", "outcome"],
        legend: "{{mode}} · {{outcome}}",
      },
    ],
  },
  {
    id: "search-refusals",
    group: "search-requests",
    title: "Search refusals",
    description:
      "Invalid, unavailable, and budget-exhausted searches by reason.",
    unit: "rate",
    queries: [
      {
        expr: 'rate(lantern_search_calls_total{outcome=~"invalid_argument|failed_precondition|resource_exhausted"}[$__rate]) > 0',
        by: ["outcome", "reason"],
        legend: "{{outcome}} · {{reason}}",
      },
    ],
  },
  {
    id: "search-internal-failures",
    group: "search-requests",
    title: "Search internal failures",
    description: "Unexpected internal failures by matching mode.",
    unit: "rate",
    queries: [
      {
        expr: 'rate(lantern_search_calls_total{outcome="internal"}[$__rate]) > 0',
        by: ["mode"],
        legend: "{{mode}}",
      },
    ],
  },
  {
    id: "search-success-latency",
    group: "search-requests",
    title: "Successful search latency (p50 / p99)",
    description: "End-to-end latency for successful searches by mode.",
    unit: "seconds",
    queries: [
      {
        expr: 'rate(lantern_search_duration_seconds_bucket{outcome="ok"}[$__rate])',
        by: ["mode"],
        agg: { quantile: 0.5 },
        legend: "p50 · {{mode}}",
      },
      {
        expr: 'rate(lantern_search_duration_seconds_bucket{outcome="ok"}[$__rate])',
        by: ["mode"],
        agg: { quantile: 0.99 },
        legend: "p99 · {{mode}}",
      },
    ],
  },
  {
    id: "search-failure-latency",
    group: "search-requests",
    title: "Failed search latency (p50 / p99)",
    description: "End-to-end latency for non-success outcomes by mode.",
    unit: "seconds",
    queries: [
      {
        expr: 'rate(lantern_search_duration_seconds_bucket{outcome!="ok"}[$__rate])',
        by: ["mode", "outcome"],
        agg: { quantile: 0.5 },
        legend: "p50 · {{mode}} · {{outcome}}",
      },
      {
        expr: 'rate(lantern_search_duration_seconds_bucket{outcome!="ok"}[$__rate])',
        by: ["mode", "outcome"],
        agg: { quantile: 0.99 },
        legend: "p99 · {{mode}} · {{outcome}}",
      },
    ],
  },
  {
    id: "search-analysis-latency",
    group: "search-requests",
    title: "Search analysis latency (p99)",
    description: "Query analysis time by matching mode.",
    unit: "seconds",
    queries: [
      {
        expr: 'rate(lantern_search_phase_duration_seconds_bucket{phase="analysis"}[$__rate])',
        by: ["mode"],
        agg: { quantile: 0.99 },
        legend: "{{mode}}",
      },
    ],
  },
  {
    id: "search-expansion-latency",
    group: "search-requests",
    title: "Search expansion latency (p99)",
    description: "Dictionary and fuzzy-expansion time by matching mode.",
    unit: "seconds",
    queries: [
      {
        expr: 'rate(lantern_search_phase_duration_seconds_bucket{phase="expansion"}[$__rate])',
        by: ["mode"],
        agg: { quantile: 0.99 },
        legend: "{{mode}}",
      },
    ],
  },
  {
    id: "search-selection-latency",
    group: "search-requests",
    title: "Search selection latency (p99)",
    description: "Candidate scoring and selection time by matching mode.",
    unit: "seconds",
    queries: [
      {
        expr: 'rate(lantern_search_phase_duration_seconds_bucket{phase="selection"}[$__rate])',
        by: ["mode"],
        agg: { quantile: 0.99 },
        legend: "{{mode}}",
      },
    ],
  },
  {
    id: "search-hits",
    group: "search-requests",
    title: "Successful search hits (p50 / p99)",
    description: "Returned hit-count distribution for successful searches.",
    unit: "count",
    queries: [
      {
        expr: 'rate(lantern_search_results_bucket{outcome="ok"}[$__rate])',
        by: ["mode"],
        agg: { quantile: 0.5 },
        legend: "p50 · {{mode}}",
      },
      {
        expr: 'rate(lantern_search_results_bucket{outcome="ok"}[$__rate])',
        by: ["mode"],
        agg: { quantile: 0.99 },
        legend: "p99 · {{mode}}",
      },
    ],
  },
  {
    id: "search-query-work",
    group: "search-requests",
    title: "Search query work (p99)",
    description: "Query bytes, tokens, clauses, and terms by matching mode.",
    unit: "count",
    queries: [
      {
        expr: 'rate(lantern_search_work_bucket{kind=~"query_bytes|query_tokens|query_clauses|query_terms"}[$__rate])',
        by: ["mode", "kind"],
        agg: { quantile: 0.99 },
        legend: "{{mode}} · {{kind}}",
      },
    ],
  },
  {
    id: "search-expansion-work",
    group: "search-requests",
    title: "Search expansion work (p99)",
    description: "Dictionary visits and retained expansions by matching mode.",
    unit: "count",
    queries: [
      {
        expr: 'rate(lantern_search_work_bucket{kind=~"dictionary_visits|expansion_retained"}[$__rate])',
        by: ["mode", "kind"],
        agg: { quantile: 0.99 },
        legend: "{{mode}} · {{kind}}",
      },
    ],
  },
  {
    id: "search-index-work",
    group: "search-requests",
    title: "Search index work (p99)",
    description: "Posting, position, and expiration visits by matching mode.",
    unit: "count",
    queries: [
      {
        expr: 'rate(lantern_search_work_bucket{kind=~"posting_visits|position_visits|expiration_visits"}[$__rate])',
        by: ["mode", "kind"],
        agg: { quantile: 0.99 },
        legend: "{{mode}} · {{kind}}",
      },
    ],
  },
  {
    id: "search-candidate-work",
    group: "search-requests",
    title: "Search candidate work (p99)",
    description: "Candidate visits and skips by matching mode.",
    unit: "count",
    queries: [
      {
        expr: 'rate(lantern_search_work_bucket{kind=~"candidate_visits|candidate_skips"}[$__rate])',
        by: ["mode", "kind"],
        agg: { quantile: 0.99 },
        legend: "{{mode}} · {{kind}}",
      },
    ],
  },
  {
    id: "search-index-state",
    group: "search-index",
    title: "Index state",
    description: "One-hot local index readiness state.",
    unit: "count",
    queries: [
      {
        expr: "lantern_search_index_state",
        by: ["state"],
        agg: "max",
        legend: "{{state}}",
      },
    ],
  },
  {
    id: "search-config-agreement",
    group: "search-index",
    title: "Search config agreement",
    description: "Whether each peer reports the same search configuration.",
    unit: "count",
    queries: [
      {
        expr: "lantern_search_config_match",
        by: ["peer"],
        agg: "min",
        legend: "{{peer}}",
      },
    ],
  },
  {
    id: "search-index-documents",
    group: "search-index",
    title: "Index documents",
    description: "Live graph vertices and logical/physical index documents.",
    unit: "count",
    queries: [
      { expr: "lantern_vertices", legend: "live vertices" },
      { expr: "lantern_search_index_docs", legend: "live documents" },
      {
        expr: "lantern_search_index_physical_documents",
        legend: "physical documents",
      },
    ],
  },
  {
    id: "search-expiration-backlog",
    group: "search-index",
    title: "Index expiration backlog",
    description: "Expired documents retained and queued for cleanup.",
    unit: "count",
    queries: [
      {
        expr: "lantern_search_index_expired_documents",
        legend: "expired documents",
      },
      {
        expr: "lantern_search_index_expiration_queue_entries",
        legend: "expiration queue",
      },
    ],
  },
  {
    id: "search-index-dictionary",
    group: "search-index",
    title: "Index dictionary",
    description: "Live terms and posting-list entries.",
    unit: "count",
    queries: [
      { expr: "lantern_search_index_terms", legend: "live terms" },
      { expr: "lantern_search_index_postings", legend: "postings" },
    ],
  },
  {
    id: "search-index-positions",
    group: "search-index",
    title: "Index positions",
    description: "Position entries and retained document ordinals.",
    unit: "count",
    queries: [
      {
        expr: "lantern_search_index_position_entries",
        legend: "positions",
      },
      {
        expr: "lantern_search_index_retained_ordinals",
        legend: "retained ordinals",
      },
    ],
  },
  {
    id: "search-index-memory",
    group: "search-index",
    title: "Search index memory",
    description: "Estimated live and retained bytes held by the derived index.",
    unit: "bytes",
    queries: [
      {
        expr: "lantern_search_index_estimated_live_bytes",
        legend: "live bytes",
      },
      {
        expr: "lantern_search_index_estimated_retained_bytes",
        legend: "retained bytes",
      },
    ],
  },
  {
    id: "search-index-retention",
    group: "search-index",
    title: "Search retention ratio",
    description:
      "Retained-to-live index bytes; sustained growth indicates compaction pressure.",
    unit: "ratio",
    queries: [
      {
        expr: "lantern_search_index_retained_ratio",
        agg: "avg",
        legend: "retained / live",
      },
    ],
  },
  {
    id: "search-rejections",
    group: "guardrails",
    title: "Search rejections",
    description: "Rejected SearchVertices attempts by bounded reason.",
    unit: "rate",
    queries: [
      {
        expr: "rate(lantern_search_rejections_total[$__rate]) > 0",
        by: ["reason"],
        legend: "{{reason}}",
      },
    ],
  },
  {
    id: "ttl-reaping",
    group: "maintenance",
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
    group: "replication",
    title: "Mutation-log fill ratio",
    description: "Fraction of the mutation-log ring buffer in use (0–1).",
    unit: "ratio",
    queries: [
      { expr: "lantern_mutation_log_fill_ratio", legend: "fill ratio" },
    ],
  },
  {
    id: "mutation-log-eviction",
    group: "replication",
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
    group: "replication",
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
    title: "Replication apply rate",
    // The apply counter also carries an `op` label (11 RPC kinds). Breaking
    // the panel down by `op` multiplies by replica count (33+ lines here) and
    // is illegible as a legend; per-op apply rates belong in an ad-hoc query,
    // not an at-a-glance health panel. We sum over `op` to show one applied
    // line per replica; drop reasons have their own focused panel below.
    description: "Remote mutations applied per replica, summed over operation.",
    unit: "rate",
    queries: [
      {
        expr: "rate(lantern_replication_apply_total[$__rate]) > 0",
        legend: "applied",
      },
    ],
  },
  {
    id: "replication-drops",
    group: "replication",
    title: "Replication drop rate",
    description: "Remote mutations dropped per second by bounded reason.",
    unit: "rate",
    queries: [
      {
        expr: "rate(lantern_replication_dropped_total[$__rate]) > 0",
        by: ["reason"],
        legend: "{{reason}}",
      },
    ],
  },
  {
    id: "validation-rejections",
    group: "guardrails",
    title: "Validation rejections",
    description: "Requests rejected by input validation, separated by reason.",
    unit: "rate",
    queries: [
      {
        expr: "rate(lantern_validation_rejected_total[$__rate]) > 0",
        by: ["reason"],
        legend: "{{reason}}",
      },
    ],
  },
  {
    id: "rate-limit-rejections",
    group: "guardrails",
    title: "Rate-limit rejections",
    description: "Requests refused by the process-wide token bucket.",
    unit: "rate",
    queries: [
      {
        expr: "rate(lantern_rate_limit_rejected_total[$__rate])",
        legend: "rejected",
      },
    ],
  },
  {
    id: "tombstone-clamp-rejections",
    group: "guardrails",
    title: "Tombstone-clamp rejections",
    description: "Causally late replicated mutations rejected by LWW guards.",
    unit: "rate",
    queries: [
      {
        expr: "rate(lantern_tombstone_clamp_rejected_total[$__rate])",
        legend: "rejected",
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
