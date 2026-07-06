// Package metrics defines the Lantern-specific Prometheus collectors exposed
// on the /metrics endpoint. It deliberately knows nothing about the cache
// implementation: callers wire it up by installing the returned sampler and
// hook callbacks on the GraphCache.
package metrics

import (
	"context"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/anaregdesign/lantern/core/concurrent/pubsub"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/prometheus/client_golang/prometheus"
)

// Sampler is the gauge-population callback installed by the server. It is
// invoked on a fixed cadence (defaults to the GC interval) and reads the live
// vertex/edge counts off the cache.
type Sampler func() (vertices int, edges int)

// MutationLogSampler reports the current ring-buffer occupancy. fill is
// the number of entries resident; capacity is the configured ring size.
// A nil sampler disables the lantern_mutation_log_fill_ratio gauge.
type MutationLogSampler func() (fill int, capacity int, evicted uint64)

// OriginStatesSampler reports the current cardinality of the per-origin
// watermark table maintained by the apply path. A nil sampler disables
// the lantern_origin_states_count gauge.
type OriginStatesSampler func() int

// SearchIndexSampler reports the current number of distinct terms and indexed
// documents in the optional search index. A nil sampler leaves
// lantern_search_index_terms and lantern_search_index_docs at 0.
type SearchIndexSampler func() (terms, docs int)

// VertexHLCSampler reports the current number of entries in the per-key LWW
// watermark map (vertexHLC). A nil sampler leaves lantern_vertex_hlc_entries
// unsampled.
type VertexHLCSampler func() int

// DomainMetrics owns the Lantern-specific collectors. Construct with New and
// pass the returned callbacks to GraphCache.SetGCHooks plus Start to begin
// gauge sampling.
type DomainMetrics struct {
	vertices            prometheus.Gauge
	edges               prometheus.Gauge
	expirations         *prometheus.CounterVec
	gcDuration          prometheus.Histogram
	buildInfo           *prometheus.GaugeVec
	mutationLogEntries  prometheus.Counter
	mutationLogCapacity prometheus.Gauge
	subscribeActive     prometheus.Gauge
	subscribeDropped    *prometheus.CounterVec

	// In-process pubsub subscription telemetry (#240). Wired by the
	// server-side pubsub.Observer adapter so the leaf core/concurrent/
	// pubsub package never imports server/metrics.
	pubsubQueueDepth       prometheus.Histogram
	pubsubDropped          *prometheus.CounterVec
	pubsubDispatchDuration prometheus.Histogram

	replicationApplied   *prometheus.CounterVec
	replicationDropped   *prometheus.CounterVec
	replicationLag       *prometheus.GaugeVec
	antiEntropyCycles    prometheus.Counter
	antiEntropyGapsFound *prometheus.CounterVec

	// Replication / mutation-log / back-pressure observability (#221).
	peerConnected         *prometheus.GaugeVec
	replicationApplyTotal *prometheus.CounterVec
	snapshotReplayedTotal *prometheus.CounterVec
	snapshotVertices      *prometheus.HistogramVec
	snapshotEdges         *prometheus.HistogramVec
	snapshotDuration      *prometheus.HistogramVec
	mutationLogFillRatio  prometheus.Gauge
	mutationLogEvicted    prometheus.Counter
	originStatesCount     prometheus.Gauge

	// Validation / back-pressure rejection counters (#222). Bumped on
	// every InvalidArgument / ResourceExhausted / tombstone-clamp drop
	// the server emits before the request reaches its handler
	// (validation, rate limit) or before the apply commits
	// (tombstone clamp). Pre-warmed so dashboards render the full reason
	// set as 0 from process start.
	validationRejected     *prometheus.CounterVec
	capacityLimit          *prometheus.GaugeVec
	authRejected           prometheus.Counter
	rateLimitRejected      prometheus.Counter
	tombstoneClampRejected prometheus.Counter

	// Mutation-log subscriber drop counter (#260). Incremented when the
	// mutationlog dispatcher's non-blocking send to a subscriber's
	// outbound channel fails and the subscriber is unregistered. The
	// existing lantern_subscription_dropped_total{policy} metric covers
	// the (currently dormant) core/concurrent/pubsub package and is
	// intentionally distinct from this one.
	mutationLogSubscriberDropped *prometheus.CounterVec

	// Hot-path RPC observability (#220). Wired by the service layer via
	// the HotPathMetrics interface in server/service. Histograms are
	// label-pre-warmed so dashboards render the full set of variants from
	// process start, before any traffic arrives.
	illuminateVisitedVertices *prometheus.HistogramVec
	illuminateVisitedEdges    *prometheus.HistogramVec
	illuminateDuration        *prometheus.HistogramVec
	scanResults               *prometheus.HistogramVec
	scanDuration              *prometheus.HistogramVec
	batchSize                 *prometheus.HistogramVec

	// Recall hit/miss counters (#539). Bumped by the service layer's
	// plural GetVertices / GetEdges implementations (the singular GetVertex
	// / GetEdge forwarders delegate to the plurals, so a read counts exactly
	// once). A "hit" is a key present at read time — including a
	// present-but-nil vertex value; a "miss" is a key absent or already
	// expired. Plain counters scrape as 0 from process start once
	// registered, so recall effectiveness = hits / (hits + misses) is
	// observable without any label pre-warming.
	getVertexHits   prometheus.Counter
	getVertexMisses prometheus.Counter
	getEdgeHits     prometheus.Counter
	getEdgeMisses   prometheus.Counter

	// Idempotent-AddEdge dedup counter (#588). Bumped by the service
	// layer's AddEdges implementation with the number of additive edge
	// contributions suppressed because an incoming client-supplied
	// ContribID matched a still-live prior contribution (a retried
	// idempotent Add). Plain counter: scrapes as 0 from process start.
	edgeContribDeduped prometheus.Counter

	// Search RPC observability (#703). searchResults and searchDuration
	// are separate histograms (not reusing the scan family) so
	// dashboards can alert on search-specific SLOs without filtering.
	searchResults  prometheus.Histogram
	searchDuration prometheus.Histogram

	// Search-index size gauges (#703). Sampled off InvertedIndex.Stats()
	// on the same cadence as lantern_vertices / lantern_edges. Both stay
	// 0 when LANTERN_SEARCH_ENABLED=false (sampler is nil).
	searchIndexTerms prometheus.Gauge
	searchIndexDocs  prometheus.Gauge

	// Per-structure cardinality gauge for the LWW watermark map (#705).
	// Sampled off GraphCache.VertexHLCCount(). Tracks the live replicated
	// key set; a value that grows monotonically across GC ticks signals
	// the vertexHLC leak (issue #700).
	vertexHLCEntries prometheus.Gauge

	// Companion high-water gauge for the LWW watermark map (#727). Sampled
	// off GraphCache.VertexHLCHighWater(): the per-GC-cycle peak len(vertexHLC)
	// before the sweep drains it. vertexHLCEntries reports the post-sweep len
	// (which reads low right after a drain); this records the peak that sizes
	// the map's retained bucket array, which Go never shrinks after delete. The
	// pairing confirms the born-expired ttl_churn heap retention (#700/#719).
	vertexHLCHighWater prometheus.Gauge

	sampleInterval           time.Duration
	sample                   Sampler
	mlogSample               MutationLogSampler
	originSample             OriginStatesSampler
	searchIndexSample        SearchIndexSampler
	vertexHLCSample          VertexHLCSampler
	vertexHLCHighWaterSample VertexHLCSampler
	lastEvicted              uint64 // last observed cumulative eviction count
}

// Hot-path label values. Exposed so the service layer can reference the
// canonical string set without importing prometheus.
//
// Illuminate metrics carry three orthogonal labels (#410, #846, #961):
//   - algorithm ∈ {none, mst, spt, ppr, community} — for the BFS family this
//     is the post-traversal reduction (none/mst/spt); for the non-BFS
//     families it is the traversal family itself (ppr #801, community #845).
//     A community walk's own reduction is not (yet) broken out here — it is
//     summarised as "community".
//   - objective ∈ {minimize, maximize} — direction for a mst/spt reduction
//     and the BFS per-hop pruning; constant/harmless when no reduction
//     applies (label value "none", or the ppr family, a fixed maximiser)
//     but still recorded for label-symmetric scraping
//   - weighting ∈ {raw, tfidf, bm25}   — edge-weight transform before BFS
//
// Service code resolves enum UNSPECIFIED values to their canonical
// defaults BEFORE calling OnIlluminate so the label space stays bounded
// at 5 × 2 × 3 = 30 combinations. Unknown enum values (a future axis
// added in proto without a metrics update) fall through to "unknown"
// so a new variant cannot break label pre-warming on existing dashboards.
var (
	algorithmLabels  = []string{"none", "mst", "spt", "ppr", "community"}
	objectiveLabels  = []string{"minimize", "maximize"}
	weightingLabels  = []string{"raw", "tfidf", "bm25"}
	illuminatePhases = []string{"traversal", "optimize"}
	scanOps          = []string{
		"ScanVertices",
		"ScanVertexKeys",
		"ScanEdges",
		"CountVerticesByPrefix",
		"DeleteVerticesByPrefix",
		"DeleteEdgesByPrefix",
		"TopVerticesByDegree",
	}
	batchOps = []string{
		"GetVertices",
		"PutVertices",
		"DeleteVertices",
		"GetEdges",
		"AddEdges",
		"PutEdges",
		"DeleteEdges",
	}
	// replicationApplyOps covers every pb.MutationOp oneof variant. The
	// service layer translates the proto-internal case selector into one
	// of these strings; unknown variants fall through to "unknown" so a
	// new MutationOp added in proto without a metrics update still
	// scrapes without exploding the registry.
	replicationApplyOps = []string{
		"PutVertex",
		"PutVertices",
		"DeleteVertex",
		"DeleteVertices",
		"DeleteVerticesByPrefix",
		"AddEdge",
		"AddEdges",
		"PutEdge",
		"PutEdges",
		"DeleteEdge",
		"DeleteEdges",
		"DeleteEdgesByPrefix",
	}
	// validationRejectReasons is the bounded reason set bumped on
	// lantern_validation_rejected_total. Sources:
	//   - ValidationInterceptor (server/provider/extra.go): empty_key,
	//     key_too_long, empty_batch, batch_too_large, nil_item,
	//     bad_weight, step_too_large, k_too_large
	//   - service.LanternService.validateExpiration: bad_ttl
	//   - service prefix-scan cursor decode: bad_cursor
	//   - service prefix-scan order-bound cursor check: order_mismatch
	// Unknown labels fall through to "unknown" via sanitizeLabel.
	validationRejectReasons = []string{
		"empty_key",
		"key_too_long",
		"empty_batch",
		"batch_too_large",
		"nil_item",
		"bad_weight",
		"step_too_large",
		"k_too_large",
		"bad_ttl",
		"bad_cursor",
		"capacity",
		"empty_edge_prefix",
		"order_mismatch",
	}
)

// Options configures DomainMetrics. Version/Commit fall back to debug.BuildInfo
// fields when left empty so a tagged release reports the right values without
// extra plumbing.
type Options struct {
	Version        string
	Commit         string
	SampleInterval time.Duration
}

// New registers the five `lantern_*` collectors on the supplied registerer and
// emits `lantern_build_info` immediately. Returns the DomainMetrics handle
// callers wire into the cache.
func New(reg prometheus.Registerer, opts Options) *DomainMetrics {
	if opts.SampleInterval <= 0 {
		opts.SampleInterval = 15 * time.Second
	}

	m := &DomainMetrics{
		vertices: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_vertices",
			Help: "Number of vertices currently held by the in-memory graph cache.",
		}),
		edges: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_edges",
			Help: "Number of edges currently held by the in-memory graph cache.",
		}),
		expirations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lantern_ttl_expirations_total",
			Help: "Total entries reaped by the periodic GC tick, partitioned by kind (vertex, edge, dangling_edge).",
		}, []string{"kind"}),
		gcDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "lantern_gc_duration_seconds",
			Help:    "Wall-clock duration of a single GraphCache GC tick.",
			Buckets: prometheus.ExponentialBuckets(0.0001, 4, 8), // 0.1ms .. ~1.6s
		}),
		buildInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lantern_build_info",
			Help: "Build metadata for the running Lantern server. Always 1; inspect labels for version/commit/go_version.",
		}, []string{"version", "commit", "go_version"}),
		mutationLogEntries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lantern_mutation_log_entries_total",
			Help: "Total mutations appended to the in-memory mutation log since process start.",
		}),
		mutationLogCapacity: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_mutation_log_capacity",
			Help: "Configured capacity (ring buffer slots) of the in-memory mutation log.",
		}),
		subscribeActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_subscribe_active_streams",
			Help: "Number of currently active LanternReplicationService.Subscribe streams.",
		}),
		subscribeDropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lantern_subscribe_dropped_total",
			Help: "Total Subscribe streams terminated abnormally, partitioned by reason (gapped, send_failed).",
		}, []string{"reason"}),
		pubsubQueueDepth: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "lantern_subscription_queue_depth",
			Help:    "Distribution of in-process pubsub subscription channel depth, sampled on every successful enqueue (#240). Aggregated across all subscriptions to avoid per-subscription cardinality.",
			Buckets: prometheus.ExponentialBuckets(1, 2, 12), // 1 .. 2048
		}),
		pubsubDropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lantern_subscription_dropped_total",
			Help: "Total pubsub messages dropped at the subscription enqueue boundary, partitioned by FullPolicy decision (drop_newest | drop_oldest | drop_newest_after_oldest). Each drop path increments exactly once.",
		}, []string{"policy"}),
		pubsubDispatchDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "lantern_subscription_dispatch_duration_seconds",
			Help:    "Distribution of pubsub dispatch latency measured from Publish to consumer return (#240). Aggregated across all subscriptions.",
			Buckets: prometheus.ExponentialBuckets(0.0001, 4, 8), // 0.1ms .. ~1.6s
		}),
		replicationApplied: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lantern_replication_applied_total",
			Help: "Total remote mutations applied locally via the replication apply path, partitioned by origin (HLC NodeID, lowercase hex).",
		}, []string{"origin"}),
		replicationDropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lantern_replication_dropped_total",
			Help: "Total replication frames or peer interactions dropped, partitioned by peer address and reason (self_echo, subscribe_failed, snapshot_failed, dial_failed, peerstatus_failed, catchup_failed, clean, ctx_cancel).",
		}, []string{"peer", "reason"}),
		replicationLag: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lantern_replication_lag_seq",
			Help: "Per-(peer, origin) replication lag in mutation seq units (peer last_seq minus local last_applied_seq). 0 means caught up; only set when the peer reports its own origin row.",
		}, []string{"peer", "origin"}),
		antiEntropyCycles: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lantern_anti_entropy_cycles_total",
			Help: "Total anti-entropy convergence ticks executed since process start (one per Interval).",
		}),
		antiEntropyGapsFound: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lantern_anti_entropy_gaps_found_total",
			Help: "Total anti-entropy ticks that observed a non-zero gap, partitioned by peer address and origin (HLC NodeID, lowercase hex).",
		}, []string{"peer", "origin"}),
		illuminateVisitedVertices: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_illuminate_visited_vertices",
			Help:    "Vertices in the subgraph returned by Illuminate, partitioned by post-traversal algorithm + objective + edge weighting (#410).",
			Buckets: prometheus.ExponentialBuckets(1, 4, 10), // 1 .. ~262K
		}, []string{"algorithm", "objective", "weighting"}),
		illuminateVisitedEdges: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_illuminate_visited_edges",
			Help:    "Edges in the subgraph returned by Illuminate, partitioned by post-traversal algorithm + objective + edge weighting (#410).",
			Buckets: prometheus.ExponentialBuckets(1, 4, 10),
		}, []string{"algorithm", "objective", "weighting"}),
		illuminateDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_illuminate_duration_seconds",
			Help:    "Wall-clock duration of Illuminate, partitioned by algorithm + objective + edge weighting and phase (traversal | optimize). #410.",
			Buckets: prometheus.ExponentialBuckets(0.0001, 4, 8), // 0.1ms .. ~1.6s
		}, []string{"algorithm", "objective", "weighting", "phase"}),
		scanResults: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_scan_results",
			Help:    "Number of results returned by a prefix scan or count RPC, partitioned by op (ScanVertices | ScanVertexKeys | ScanEdges | CountVerticesByPrefix | DeleteVerticesByPrefix | DeleteEdgesByPrefix).",
			Buckets: prometheus.ExponentialBuckets(1, 4, 10),
		}, []string{"op"}),
		scanDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_scan_duration_seconds",
			Help:    "Wall-clock duration of a prefix scan or count RPC, partitioned by op.",
			Buckets: prometheus.ExponentialBuckets(0.0001, 4, 8),
		}, []string{"op"}),
		batchSize: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_batch_size",
			Help:    "Item count of a single plural-RPC batch (PutVertices, PutEdges, AddEdges, GetVertices, GetEdges, DeleteVertices, DeleteEdges). Singular RPC forwarders are not double-counted.",
			Buckets: prometheus.ExponentialBuckets(1, 4, 10),
		}, []string{"op"}),
		getVertexHits: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lantern_get_vertex_hits_total",
			Help: "Total GetVertices key lookups that found a live vertex (present at read time, including a present-but-nil value). Bumped by the plural GetVertices implementation; the singular GetVertex forwards through it so reads count exactly once. Recall hit ratio = hits / (hits + misses).",
		}),
		getVertexMisses: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lantern_get_vertex_misses_total",
			Help: "Total GetVertices key lookups that found no live vertex (absent or already expired at read time). See lantern_get_vertex_hits_total for the hit side.",
		}),
		getEdgeHits: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lantern_get_edge_hits_total",
			Help: "Total GetEdges (tail,head) lookups that found a live edge. Bumped by the plural GetEdges implementation; the singular GetEdge forwards through it so reads count exactly once.",
		}),
		getEdgeMisses: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lantern_get_edge_misses_total",
			Help: "Total GetEdges (tail,head) lookups that found no live edge (absent or already expired at read time). See lantern_get_edge_hits_total for the hit side.",
		}),
		edgeContribDeduped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lantern_edge_contrib_deduped_total",
			Help: "Total additive edge contributions suppressed by client-supplied ContribID dedup (#588). A retried idempotent AddEdge/AddEdges re-sending the same ContribID bumps this instead of double-counting edge weight.",
		}),
		searchResults: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "lantern_search_results",
			Help:    "Number of hits returned by a SearchVertices RPC (#703). Separate from the scan family so search-specific SLOs can be alerted on independently.",
			Buckets: prometheus.ExponentialBuckets(1, 4, 10),
		}),
		searchDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "lantern_search_duration_seconds",
			Help:    "Wall-clock duration of a SearchVertices RPC (#703).",
			Buckets: prometheus.ExponentialBuckets(0.0001, 4, 8), // 0.1ms .. ~1.6s
		}),
		searchIndexTerms: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_search_index_terms",
			Help: "Current number of distinct terms in the search inverted index (#703). Sampled on the same cadence as lantern_vertices. Always 0 when LANTERN_SEARCH_ENABLED=false.",
		}),
		searchIndexDocs: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_search_index_docs",
			Help: "Current number of documents (indexed vertices) in the search inverted index (#703). Sampled on the same cadence as lantern_vertices. Always 0 when LANTERN_SEARCH_ENABLED=false.",
		}),
		vertexHLCEntries: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_vertex_hlc_entries",
			Help: "Current number of entries in the per-key LWW watermark map used by the replication apply path (#705). Tracks the live replicated-key set; a value growing monotonically across GC ticks signals the vertexHLC leak (issue #700). Always 0 on a single-node deployment.",
		}),
		vertexHLCHighWater: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_vertex_hlc_entries_high_water",
			Help: "Per-GC-cycle peak number of entries in the per-key LWW watermark map (vertexHLC), recorded at the start of each sweep before stale entries are drained (#727). Unlike lantern_vertex_hlc_entries (the post-sweep len, which reads low right after a drain), this is monotonic non-decreasing and records the all-time churn peak. Born-expired LWW churn (#700/#719) used to retain the map's bucket array at this size (Go never shrinks a map after delete); the sweep now reallocates the map after a large drain, so a high value here is a historical churn signal, not pinned heap. Always 0 on a single-node deployment.",
		}),
		peerConnected: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lantern_peer_connected",
			Help: "1 when the local replication pump currently holds an open Subscribe (or Subscribe+Snapshot) session to the named peer; 0 otherwise. Updated on every pump connect/disconnect lifecycle event.",
		}, []string{"peer"}),
		replicationApplyTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lantern_replication_apply_total",
			Help: "Total mutations applied locally via ApplyMutation, partitioned by MutationOp oneof variant. Both pump-delivered remote mutations and locally-originated ones increment this counter.",
		}, []string{"op"}),
		snapshotReplayedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lantern_snapshot_replayed_total",
			Help: "Total Snapshot RPC streams the pump has fully replayed from the named peer.",
		}, []string{"peer"}),
		snapshotVertices: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_snapshot_vertices",
			Help:    "Vertex count replayed from a single Snapshot stream, partitioned by source peer.",
			Buckets: prometheus.ExponentialBuckets(1, 4, 10),
		}, []string{"peer"}),
		snapshotEdges: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_snapshot_edges",
			Help:    "Edge count replayed from a single Snapshot stream, partitioned by source peer.",
			Buckets: prometheus.ExponentialBuckets(1, 4, 10),
		}, []string{"peer"}),
		snapshotDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_snapshot_duration_seconds",
			Help:    "Wall-clock duration of a single Snapshot stream replay, partitioned by source peer.",
			Buckets: prometheus.ExponentialBuckets(0.001, 4, 9), // 1ms .. ~65s
		}, []string{"peer"}),
		mutationLogFillRatio: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_mutation_log_fill_ratio",
			Help: "Current ring-buffer occupancy of the in-memory mutation log, expressed as fill/capacity in [0, 1]. Sampled on the same cadence as lantern_vertices / lantern_edges.",
		}),
		mutationLogEvicted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lantern_mutation_log_evicted_total",
			Help: "Total mutation-log entries dropped from the ring buffer because Append at full capacity displaced the oldest entry. A non-zero rate indicates subscribers cannot keep up with append throughput and risk hitting ErrGapped.",
		}),
		originStatesCount: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_origin_states_count",
			Help: "Number of distinct origin NodeIDs the local apply path has recorded in its per-origin watermark table.",
		}),
		validationRejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lantern_validation_rejected_total",
			Help: "Total requests rejected by server-side input validation, partitioned by reason (empty_key, key_too_long, empty_batch, batch_too_large, nil_item, bad_weight, step_too_large, k_too_large, bad_ttl, bad_cursor, order_mismatch). Counted before the handler runs (ValidationInterceptor) or during validateExpiration / cursor decode in the service layer.",
		}, []string{"reason"}),
		capacityLimit: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lantern_capacity_limit",
			Help: "Configured soft capacity cap per kind (vertex, edge) from LANTERN_MAX_VERTICES / LANTERN_MAX_EDGES (#848). 0 = unlimited. Divide lantern_vertices / lantern_edges by this for a fill ratio; alert above 0.8.",
		}, []string{"kind"}),
		authRejected: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lantern_auth_rejected_total",
			Help: "Requests rejected by the bearer-token auth interceptor (#850). Registered even when LANTERN_AUTH_TOKENS is unset so dashboards compare deployments uniformly.",
		}),
		rateLimitRejected: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lantern_rate_limit_rejected_total",
			Help: "Total RPCs rejected by the process-wide token-bucket rate limiter (codes.ResourceExhausted). Registered at 0 even when LANTERN_RATE_LIMIT_RPS=0 so dashboards can compare deployments uniformly.",
		}),
		tombstoneClampRejected: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lantern_tombstone_clamp_rejected_total",
			Help: "Total ApplyMutation Put/Add operations dropped on the tombstone-aware HLC path because the incoming mutation lost the LWW comparison against an existing tombstone or a strictly-newer live HLC. A sustained rate indicates causally-late replication frames or clock skew larger than LANTERN_TOMBSTONE_TTL.",
		}),
		mutationLogSubscriberDropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lantern_mutationlog_subscriber_dropped_total",
			Help: "Total mutation-log subscribers terminated by the dispatcher because their outbound channel could not absorb a live entry (#260). cause=\"buffer_full\" is the only label today; new causes may be added.",
		}, []string{"cause"}),
		sampleInterval: opts.SampleInterval,
	}

	reg.MustRegister(m.vertices, m.edges, m.expirations, m.gcDuration, m.buildInfo,
		m.mutationLogEntries, m.mutationLogCapacity, m.subscribeActive, m.subscribeDropped,
		m.pubsubQueueDepth, m.pubsubDropped, m.pubsubDispatchDuration,
		m.replicationApplied, m.replicationDropped, m.replicationLag,
		m.antiEntropyCycles, m.antiEntropyGapsFound,
		m.illuminateVisitedVertices, m.illuminateVisitedEdges, m.illuminateDuration,
		m.scanResults, m.scanDuration, m.batchSize,
		m.getVertexHits, m.getVertexMisses, m.getEdgeHits, m.getEdgeMisses,
		m.edgeContribDeduped,
		m.searchResults, m.searchDuration, m.searchIndexTerms, m.searchIndexDocs,
		m.vertexHLCEntries, m.vertexHLCHighWater,
		m.peerConnected, m.replicationApplyTotal, m.snapshotReplayedTotal,
		m.snapshotVertices, m.snapshotEdges, m.snapshotDuration,
		m.mutationLogFillRatio, m.mutationLogEvicted, m.originStatesCount,
		m.validationRejected, m.rateLimitRejected, m.tombstoneClampRejected,
		m.mutationLogSubscriberDropped, m.capacityLimit, m.authRejected)

	// Pre-create label rows so empty counters scrape as 0.
	for _, r := range []string{"gapped", "send_failed"} {
		m.subscribeDropped.WithLabelValues(r)
	}

	// Pre-warm the pubsub drop counter (#240) so every FullPolicy label
	// row scrapes as 0 from process start. Sourced from
	// pubsub.DropPolicies so adding a new policy in core/ surfaces here
	// at compile time without a metrics edit.
	for _, p := range pubsub.DropPolicies {
		m.pubsubDropped.WithLabelValues(p)
	}

	// Pre-create label rows so empty counters are still scraped as 0.
	for _, k := range []string{"vertex", "edge", "dangling_edge"} {
		m.expirations.WithLabelValues(k)
	}

	// Pre-create hot-path histogram label rows so /metrics renders the
	// full variant set on a fresh process. Histograms emit count/sum/
	// bucket families lazily per label; observing a no-op is the
	// idiomatic way to materialise the row. Per #410/#801 the Illuminate
	// label space is 4 × 2 × 3 = 24 combinations (algorithm × objective
	// × weighting), well below Prometheus cardinality concerns.
	for _, algo := range algorithmLabels {
		for _, obj := range objectiveLabels {
			for _, w := range weightingLabels {
				m.illuminateVisitedVertices.WithLabelValues(algo, obj, w)
				m.illuminateVisitedEdges.WithLabelValues(algo, obj, w)
				for _, ph := range illuminatePhases {
					m.illuminateDuration.WithLabelValues(algo, obj, w, ph)
				}
			}
		}
	}
	for _, op := range scanOps {
		m.scanResults.WithLabelValues(op)
		m.scanDuration.WithLabelValues(op)
	}
	for _, op := range batchOps {
		m.batchSize.WithLabelValues(op)
	}
	// Pre-warm the replication-apply counter so dashboards render every
	// MutationOp variant from process start. Per-peer collectors
	// (peer_connected, snapshot_*) cannot be pre-warmed because peer
	// addresses are discovered at runtime.
	for _, op := range replicationApplyOps {
		m.replicationApplyTotal.WithLabelValues(op)
	}
	// Pre-warm the validation-rejection counter (#222) so the full
	// reason set scrapes as 0 before any traffic arrives. Plus the
	// "unknown" bucket sanitizeLabel falls back to so a future reason
	// added without a metrics update still shows up.
	for _, r := range validationRejectReasons {
		m.validationRejected.WithLabelValues(r)
	}
	m.validationRejected.WithLabelValues("unknown")

	// Pre-warm the single known mutation-log drop cause so dashboards
	// render the series at 0 from process start (#260).
	m.mutationLogSubscriberDropped.WithLabelValues(mutationlog.DropCauseBufferFull)

	version, commit := opts.Version, opts.Commit
	if version == "" || commit == "" {
		v, c := readBuildInfo()
		if version == "" {
			version = v
		}
		if commit == "" {
			commit = c
		}
	}
	m.buildInfo.WithLabelValues(version, commit, runtime.Version()).Set(1)

	return m
}

// OnExpire records reaped entries from a single GC tick.
func (m *DomainMetrics) OnExpire(kind string, n int) {
	if n <= 0 {
		return
	}
	m.expirations.WithLabelValues(kind).Add(float64(n))
}

// OnGCDuration records the wall-clock duration of a single GC tick.
func (m *DomainMetrics) OnGCDuration(d time.Duration) {
	m.gcDuration.Observe(d.Seconds())
}

// OnMutationLogAppend increments the counter of successful mutation-log
// appends. Intended to be wired as a callback into the service layer.
func (m *DomainMetrics) OnMutationLogAppend() {
	m.mutationLogEntries.Inc()
}

// SetMutationLogCapacity reports the configured capacity (ring buffer
// slots) of the mutation log. Called once at startup.
func (m *DomainMetrics) SetMutationLogCapacity(n int) {
	m.mutationLogCapacity.Set(float64(n))
}

// OnSubscribeStarted increments the active-streams gauge. Pair with
// OnSubscribeEnded via defer.
func (m *DomainMetrics) OnSubscribeStarted() {
	m.subscribeActive.Inc()
}

// OnSubscribeEnded decrements the active-streams gauge.
func (m *DomainMetrics) OnSubscribeEnded() {
	m.subscribeActive.Dec()
}

// OnSubscribeDropped increments the dropped-streams counter for the given
// reason ("gapped" or "send_failed"). Other reasons are accepted but
// dashboards may not pre-render them.
func (m *DomainMetrics) OnSubscribeDropped(reason string) {
	m.subscribeDropped.WithLabelValues(reason).Inc()
}

// OnPubsubQueueDepth records a single in-process pubsub subscription
// channel-depth sample. Called once per successful enqueue by the
// pubsub.Observer adapter installed via WithObserver (#240).
func (m *DomainMetrics) OnPubsubQueueDepth(depth int) {
	m.pubsubQueueDepth.Observe(float64(depth))
}

// OnPubsubDrop increments the pubsub drop counter for the given
// FullPolicy decision. policy must be one of pubsub.DropPolicies; unknown
// labels will register lazily but are not pre-warmed.
func (m *DomainMetrics) OnPubsubDrop(policy string) {
	m.pubsubDropped.WithLabelValues(policy).Inc()
}

// OnPubsubDispatchDuration records a single Publish→consumer-return
// latency sample.
func (m *DomainMetrics) OnPubsubDispatchDuration(d time.Duration) {
	m.pubsubDispatchDuration.Observe(d.Seconds())
}

// PubsubObserver returns a pubsub.Observer adapter that fans out to the
// three pubsub collectors. Server code installs it with pubsub.WithObserver
// when constructing in-process subscriptions; the adapter keeps
// core/concurrent/pubsub free of any server/metrics import (#240).
func (m *DomainMetrics) PubsubObserver() pubsub.Observer {
	return pubsubObserver{m: m}
}

type pubsubObserver struct{ m *DomainMetrics }

func (o pubsubObserver) RecordEnqueueDepth(depth int)    { o.m.OnPubsubQueueDepth(depth) }
func (o pubsubObserver) RecordDrop(policy string)        { o.m.OnPubsubDrop(policy) }
func (o pubsubObserver) ObserveDispatch(d time.Duration) { o.m.OnPubsubDispatchDuration(d) }

// OnReplicationApplied increments lantern_replication_applied_total for
// a mutation accepted by the apply path. origin is the HLC NodeID of the
// originating node, lowercase-hex encoded.
func (m *DomainMetrics) OnReplicationApplied(origin string) {
	m.replicationApplied.WithLabelValues(origin).Inc()
}

// OnReplicationDropped increments lantern_replication_dropped_total for a
// dropped frame or failed peer interaction. peer is the peer's RPC
// address; reason is a short identifier (see metric Help text).
func (m *DomainMetrics) OnReplicationDropped(peer, reason string) {
	m.replicationDropped.WithLabelValues(peer, reason).Inc()
}

// SetReplicationLag sets the per-(peer, origin) replication lag gauge in
// mutation-seq units. Called from the anti-entropy driver after each
// PeerStatus probe; 0 means caught up.
func (m *DomainMetrics) SetReplicationLag(peer, origin string, lag uint64) {
	m.replicationLag.WithLabelValues(peer, origin).Set(float64(lag))
}

// OnAntiEntropyCycle increments lantern_anti_entropy_cycles_total. Called
// once per tick of the anti-entropy driver (i.e. per fan-out across all
// peers).
func (m *DomainMetrics) OnAntiEntropyCycle() {
	m.antiEntropyCycles.Inc()
}

// OnAntiEntropyGapFound increments
// lantern_anti_entropy_gaps_found_total{peer,origin} when a tick observes
// a non-zero gap against a peer's own origin row.
func (m *DomainMetrics) OnAntiEntropyGapFound(peer, origin string) {
	m.antiEntropyGapsFound.WithLabelValues(peer, origin).Inc()
}

// --- replication.AntiEntropyMetrics adapter ---
//
// The anti-entropy driver's narrow surface (server/replication.AntiEntropyMetrics)
// is satisfied by these methods so the driver can take *DomainMetrics
// directly without a wrapper type. Per-cycle and per-peer events map onto
// the collectors registered above; the per-peer "Tick" event is currently
// a no-op as the issue spec only requires per-cycle counts.

func (m *DomainMetrics) OnAntiEntropyTick(string) {}

func (m *DomainMetrics) OnAntiEntropyBehind(peer, origin string, gap uint64) {
	m.SetReplicationLag(peer, origin, gap)
	m.OnAntiEntropyGapFound(peer, origin)
}

func (m *DomainMetrics) OnAntiEntropyCaughtUp(peer, origin string, _ uint64) {
	// Reset the gauge to 0 — the peer is now caught up on the row we
	// were repairing. Dashboards see a clear edge transition.
	m.SetReplicationLag(peer, origin, 0)
}

func (m *DomainMetrics) OnAntiEntropyError(peer, reason string) {
	m.OnReplicationDropped(peer, reason)
}

// --- replication.Metrics (pump) adapter ---
//
// The pump's narrow Metrics surface is satisfied by these methods so
// provider/replication.go can pass *DomainMetrics directly. Connect /
// disconnect drive the per-peer lantern_peer_connected gauge; snapshot
// replay drives the lantern_snapshot_* family; the pump-apply hook is
// reserved for future per-peer accounting (the canonical apply counter
// is lantern_replication_apply_total, populated by OnReplicationApply
// from ApplyMutation itself).

func (m *DomainMetrics) OnPumpConnect(peer string) {
	m.peerConnected.WithLabelValues(peer).Set(1)
}

func (m *DomainMetrics) OnPumpDisconnect(peer, reason string) {
	m.peerConnected.WithLabelValues(peer).Set(0)
	m.OnReplicationDropped(peer, reason)
}

func (m *DomainMetrics) OnPumpApply(string) {}

func (m *DomainMetrics) OnPumpDropSelfEcho(peer string) {
	m.OnReplicationDropped(peer, "self_echo")
}

func (m *DomainMetrics) OnPumpSnapshotReplayed(peer string, vertices, edges uint64, duration time.Duration) {
	m.snapshotReplayedTotal.WithLabelValues(peer).Inc()
	m.snapshotVertices.WithLabelValues(peer).Observe(float64(vertices))
	m.snapshotEdges.WithLabelValues(peer).Observe(float64(edges))
	m.snapshotDuration.WithLabelValues(peer).Observe(duration.Seconds())
}

// OnReplicationApply increments lantern_replication_apply_total for one
// applied MutationOp. The op label is the proto oneof variant name
// (e.g. "PutVertex", "AddEdges"). Unknown labels are bucketed as
// "unknown" so a new MutationOp added without a metrics update cannot
// silently inflate the registry.
func (m *DomainMetrics) OnReplicationApply(op string) {
	o := sanitizeLabel(op, replicationApplyOps, "unknown")
	m.replicationApplyTotal.WithLabelValues(o).Inc()
}

// OnValidationRejected increments lantern_validation_rejected_total for
// one rejected request. reason must be one of validationRejectReasons;
// unknown values are bucketed as "unknown" to keep label cardinality
// bounded. Wired into ValidationInterceptor (server/provider) and into
// service.validateExpiration / prefix-scan cursor decode via
// WithValidationRejectHook.
func (m *DomainMetrics) OnValidationRejected(reason string) {
	r := sanitizeLabel(reason, validationRejectReasons, "unknown")
	m.validationRejected.WithLabelValues(r).Inc()
}

// OnAuthRejected increments lantern_auth_rejected_total when the bearer
// token interceptor rejects a request (#850).
func (m *DomainMetrics) OnAuthRejected() {
	m.authRejected.Inc()
}

// SetCapacityLimits publishes the configured aggregate soft caps (#848) as
// lantern_capacity_limit{kind}. Called once at wiring time; 0 means the cap
// is disabled. Exposing the limit (not just the counts) lets dashboards
// compute a fill ratio without knowing the deployment's env configuration.
func (m *DomainMetrics) SetCapacityLimits(maxVertices, maxEdges int) {
	m.capacityLimit.WithLabelValues("vertex").Set(float64(maxVertices))
	m.capacityLimit.WithLabelValues("edge").Set(float64(maxEdges))
}

// OnRateLimitRejected increments lantern_rate_limit_rejected_total when
// the token-bucket interceptor returns codes.ResourceExhausted. The
// counter is registered even when LANTERN_RATE_LIMIT_RPS=0 so dashboards
// can compare deployments uniformly.
func (m *DomainMetrics) OnRateLimitRejected() {
	m.rateLimitRejected.Inc()
}

// OnTombstoneClampRejected increments lantern_tombstone_clamp_rejected_total
// when an ApplyMutation Put/Add on the tombstone-aware HLC path is
// dropped because the incoming HLC lost the LWW comparison (either
// against a live tombstone or against a strictly-newer existing entry).
func (m *DomainMetrics) OnTombstoneClampRejected() {
	m.tombstoneClampRejected.Inc()
}

// OnMutationLogSubscriberDropped increments
// lantern_mutationlog_subscriber_dropped_total{cause} when the
// mutationlog dispatcher's non-blocking send to a subscriber's outbound
// channel fails and the subscriber is unregistered (#260). The "cause"
// label is sanitised against the known set
// (currently just mutationlog.DropCauseBufferFull); unknown values fall
// back to "unknown" so a future cause added without a metrics update
// still shows up.
func (m *DomainMetrics) OnMutationLogSubscriberDropped(cause string) {
	c := sanitizeLabel(cause, []string{mutationlog.DropCauseBufferFull}, "unknown")
	m.mutationLogSubscriberDropped.WithLabelValues(c).Inc()
}

// --- Hot-path RPC observability (#220) ---
//
// These methods implement the HotPathMetrics interface consumed by
// server/service.LanternService. Unknown label values fall back to a safe
// constant so a stale enum or typo cannot blow up the registry.

func sanitizeLabel(v string, allowed []string, fallback string) string {
	for _, a := range allowed {
		if a == v {
			return v
		}
	}
	return fallback
}

// OnIlluminate records one Illuminate RPC: visited vertex/edge counts and
// the wall-clock duration of the two phases (traversal: neighbour walk;
// optimize: post-processing such as spanning trees). optimize may be 0
// when no algorithm was requested. Per #410 the labels are the three
// orthogonal axes; service code is expected to have resolved UNSPECIFIED
// values to their canonical defaults already.
func (m *DomainMetrics) OnIlluminate(algorithm, objective, weighting string, visitedVertices, visitedEdges int, traversal, optimize time.Duration) {
	a := sanitizeLabel(algorithm, algorithmLabels, "unknown")
	o := sanitizeLabel(objective, objectiveLabels, "unknown")
	w := sanitizeLabel(weighting, weightingLabels, "unknown")
	m.illuminateVisitedVertices.WithLabelValues(a, o, w).Observe(float64(visitedVertices))
	m.illuminateVisitedEdges.WithLabelValues(a, o, w).Observe(float64(visitedEdges))
	m.illuminateDuration.WithLabelValues(a, o, w, "traversal").Observe(traversal.Seconds())
	if optimize > 0 {
		m.illuminateDuration.WithLabelValues(a, o, w, "optimize").Observe(optimize.Seconds())
	}
}

// OnScan records one prefix-scan RPC: result count and wall-clock
// duration, partitioned by op (ScanVertices | ScanEdges |
// DeleteVerticesByPrefix).
func (m *DomainMetrics) OnScan(op string, results int, duration time.Duration) {
	o := sanitizeLabel(op, scanOps, op) // pass-through unknowns to surface in dashboards
	m.scanResults.WithLabelValues(o).Observe(float64(results))
	m.scanDuration.WithLabelValues(o).Observe(duration.Seconds())
}

// OnBatch records the batch size for one plural RPC. Singular RPC
// forwarders (GetVertex / PutVertex / ...) must NOT call this — the
// canonical plural implementation owns the metric. See AGENTS.md for the
// invariant.
func (m *DomainMetrics) OnBatch(op string, size int) {
	o := sanitizeLabel(op, batchOps, op)
	m.batchSize.WithLabelValues(o).Observe(float64(size))
}

// OnGetVertices records the hit/miss split of one GetVertices RPC (#539):
// hits is the number of requested keys that resolved to a live vertex,
// misses the number absent or expired. Called once by the plural
// GetVertices implementation, so the singular GetVertex forwarder counts
// through it exactly once. Zero-valued sides are skipped so an all-hit or
// all-miss batch touches only the relevant counter.
func (m *DomainMetrics) OnGetVertices(hits, misses int) {
	if hits > 0 {
		m.getVertexHits.Add(float64(hits))
	}
	if misses > 0 {
		m.getVertexMisses.Add(float64(misses))
	}
}

// OnGetEdges records the hit/miss split of one GetEdges RPC (#539): hits is
// the number of requested (tail,head) pairs that resolved to a live edge,
// misses the number absent or expired. Called once by the plural GetEdges
// implementation, so the singular GetEdge forwarder counts through it
// exactly once.
func (m *DomainMetrics) OnGetEdges(hits, misses int) {
	if hits > 0 {
		m.getEdgeHits.Add(float64(hits))
	}
	if misses > 0 {
		m.getEdgeMisses.Add(float64(misses))
	}
}

// OnEdgeContribDeduped records that n additive edge contributions were
// suppressed by client-supplied ContribID dedup during one AddEdges RPC
// (#588). Called once per plural AddEdges; the singular AddEdge forwards
// through it. Zero is skipped so a fully-applied batch touches nothing.
func (m *DomainMetrics) OnEdgeContribDeduped(n int) {
	if n > 0 {
		m.edgeContribDeduped.Add(float64(n))
	}
}

// OnSearch records one SearchVertices RPC: the number of ranked hits returned
// and the wall-clock duration (#703). Called by the SearchVertices handler;
// results=0 is still observed (an empty result is a valid outcome).
func (m *DomainMetrics) OnSearch(results int, duration time.Duration) {
	m.searchResults.Observe(float64(results))
	m.searchDuration.Observe(duration.Seconds())
}

// BindSampler stores the gauge-population callback. Must be called before
// Run; safe to call exactly once during wiring.
func (m *DomainMetrics) BindSampler(s Sampler) {
	m.sample = s
}

// BindMutationLogSampler installs the mutation-log occupancy callback.
// Must be called before Run; safe to call exactly once during wiring.
// A nil sampler leaves lantern_mutation_log_fill_ratio /
// lantern_mutation_log_evicted_total unsampled.
func (m *DomainMetrics) BindMutationLogSampler(s MutationLogSampler) {
	m.mlogSample = s
}

// BindOriginStatesSampler installs the per-origin watermark cardinality
// callback. Must be called before Run; safe to call exactly once during
// wiring. A nil sampler leaves lantern_origin_states_count unsampled.
func (m *DomainMetrics) BindOriginStatesSampler(s OriginStatesSampler) {
	m.originSample = s
}

// BindSearchIndexSampler installs the search-index size callback.
// Must be called before Run; safe to call exactly once during wiring.
// A nil sampler leaves lantern_search_index_terms and
// lantern_search_index_docs at 0 (always the case when
// LANTERN_SEARCH_ENABLED=false).
func (m *DomainMetrics) BindSearchIndexSampler(s SearchIndexSampler) {
	m.searchIndexSample = s
}

// BindVertexHLCSampler installs the per-key LWW watermark count callback.
// Must be called before Run; safe to call exactly once during wiring.
// A nil sampler leaves lantern_vertex_hlc_entries unsampled (always 0 on
// a single-node deployment where no replicated writes arrive).
func (m *DomainMetrics) BindVertexHLCSampler(s VertexHLCSampler) {
	m.vertexHLCSample = s
}

// BindVertexHLCHighWaterSampler installs the per-cycle peak LWW watermark
// count callback (#727). Must be called before Run; safe to call exactly once
// during wiring. A nil sampler leaves lantern_vertex_hlc_entries_high_water
// unsampled (always 0 on a single-node deployment where no replicated writes
// arrive).
func (m *DomainMetrics) BindVertexHLCHighWaterSampler(s VertexHLCSampler) {
	m.vertexHLCHighWaterSample = s
}

// Run drives the gauge sampler on the configured cadence until ctx is done.
// Safe to launch as a goroutine. A nil sampler is treated as a no-op so
// tests can construct the collectors without wiring a cache.
func (m *DomainMetrics) Run(ctx context.Context) {
	if m.sample == nil && m.mlogSample == nil && m.originSample == nil &&
		m.searchIndexSample == nil && m.vertexHLCSample == nil &&
		m.vertexHLCHighWaterSample == nil {
		<-ctx.Done()
		return
	}
	// Take an immediate sample so /metrics has live values before the first tick.
	m.tick()
	t := time.NewTicker(m.sampleInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			m.tick()
		case <-ctx.Done():
			return
		}
	}
}

func (m *DomainMetrics) tick() {
	if m.sample != nil {
		v, e := m.sample()
		m.vertices.Set(float64(v))
		m.edges.Set(float64(e))
	}
	if m.mlogSample != nil {
		fill, capacity, evicted := m.mlogSample()
		if capacity > 0 {
			m.mutationLogFillRatio.Set(float64(fill) / float64(capacity))
		} else {
			m.mutationLogFillRatio.Set(0)
		}
		// Counter is monotonic; advance to the cumulative source-of-truth.
		// We use a delta-add against a remembered prior sample because
		// prometheus.Counter has no Set.
		delta := evicted - m.lastEvicted
		if evicted < m.lastEvicted { // sampler reset (e.g. log re-instantiated in tests)
			delta = evicted
		}
		if delta > 0 {
			m.mutationLogEvicted.Add(float64(delta))
		}
		m.lastEvicted = evicted
	}
	if m.originSample != nil {
		m.originStatesCount.Set(float64(m.originSample()))
	}
	if m.searchIndexSample != nil {
		terms, docs := m.searchIndexSample()
		m.searchIndexTerms.Set(float64(terms))
		m.searchIndexDocs.Set(float64(docs))
	}
	if m.vertexHLCSample != nil {
		m.vertexHLCEntries.Set(float64(m.vertexHLCSample()))
	}
	if m.vertexHLCHighWaterSample != nil {
		m.vertexHLCHighWater.Set(float64(m.vertexHLCHighWaterSample()))
	}
}

// readBuildInfo extracts the module version and vcs.revision from the binary's
// embedded build info. Returns "(devel)"/"unknown" when fields are absent.
func readBuildInfo() (version, commit string) {
	version = "(devel)"
	commit = "unknown"
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if bi.Main.Version != "" {
		version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			commit = s.Value
			if len(commit) > 12 {
				commit = commit[:12]
			}
			break
		}
	}
	return
}
