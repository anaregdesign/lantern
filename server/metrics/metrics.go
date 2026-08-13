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
	"github.com/anaregdesign/lantern/core/search"
	"github.com/anaregdesign/lantern/server/service"
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
type SearchIndexSampler func() search.IndexMemoryStats

// VertexHLCSampler reports the current number of entries in the per-key LWW
// watermark map (vertexHLC). A nil sampler leaves lantern_vertex_hlc_entries
// unsampled.
type VertexHLCSampler func() int

// CausalBarrierSampler reports retained accepted-expired Put LWW floors by
// identity kind. A nil sampler leaves both gauges at zero.
type CausalBarrierSampler func() (vertices int, edges int)

// CausalMetadataSample is the cache-independent shape sampled for the HA
// causal-identity budget. Oldest*RetentionDeadline is zero when no retained
// Delete tombstone exists for that kind.
type CausalMetadataSample struct {
	VertexLimit                   int
	EdgeLimit                     int
	VertexEntries                 int
	EdgeEntries                   int
	VertexEstimatedBytes          uint64
	EdgeEstimatedBytes            uint64
	VertexEntriesHighWater        int
	EdgeEntriesHighWater          int
	VertexEstimatedBytesHighWater uint64
	EdgeEstimatedBytesHighWater   uint64
	VertexRejected                uint64
	EdgeRejected                  uint64
	VertexOverLimit               bool
	EdgeOverLimit                 bool
	OldestVertexRetentionDeadline time.Time
	OldestEdgeRetentionDeadline   time.Time
}

// CausalMetadataSampler reports one lock-consistent causal-metadata snapshot.
// A nil sampler leaves the pre-warmed per-kind series at zero.
type CausalMetadataSampler func() CausalMetadataSample

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
	searchConfigMatch    *prometheus.GaugeVec
	searchConfigMismatch *prometheus.CounterVec

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
	illuminateCalls           *prometheus.CounterVec
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
	searchResults       *prometheus.HistogramVec
	searchDuration      *prometheus.HistogramVec
	searchPhaseDuration *prometheus.HistogramVec
	searchCalls         *prometheus.CounterVec
	searchWork          *prometheus.HistogramVec
	searchRejections    *prometheus.CounterVec

	// Search-index size/health gauges (#703/#1064). Sampled off
	// InvertedIndex.Stats() on the same cadence as lantern_vertices / edges.
	// Size gauges stay 0 and state=disabled when no sampler is bound.
	searchIndexTerms            prometheus.Gauge
	searchIndexDocs             prometheus.Gauge
	searchIndexPhysicalDocs     prometheus.Gauge
	searchIndexExpiredDocs      prometheus.Gauge
	searchIndexExpirationQueue  prometheus.Gauge
	searchIndexExpirationPurged prometheus.Gauge
	searchIndexPurgeDuration    prometheus.Gauge
	searchIndexRetainedTerms    prometheus.Gauge
	searchIndexRetainedOrdinals prometheus.Gauge
	searchIndexPostings         prometheus.Gauge
	searchIndexPositions        prometheus.Gauge
	searchIndexLiveBytes        prometheus.Gauge
	searchIndexRetainedBytes    prometheus.Gauge
	searchIndexRetainedRatio    prometheus.Gauge
	searchIndexRebuilds         prometheus.Gauge
	searchIndexRebuildDuration  prometheus.Gauge
	searchIndexHealthy          prometheus.Gauge
	searchIndexState            *prometheus.GaugeVec

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
	vertexHLCHighWater                    prometheus.Gauge
	vertexCausalBarriers                  prometheus.Gauge
	edgeCausalBarriers                    prometheus.Gauge
	causalMetadataEntries                 *prometheus.GaugeVec
	causalMetadataEstimatedBytes          *prometheus.GaugeVec
	causalMetadataEntriesHighWater        *prometheus.GaugeVec
	causalMetadataEstimatedBytesHighWater *prometheus.GaugeVec
	causalMetadataRejected                *prometheus.CounterVec
	causalMetadataOldestRetentionDeadline *prometheus.GaugeVec
	causalMetadataLimit                   *prometheus.GaugeVec
	causalMetadataOverLimit               *prometheus.GaugeVec
	// Unlabelled vertex aliases keep the release-sweep metric gate scalar-only
	// while the canonical operator surface remains the bounded {kind} family.
	vertexCausalMetadataEntries          prometheus.Gauge
	vertexCausalMetadataEntriesHighWater prometheus.Gauge
	vertexCausalMetadataEstimatedBytes   prometheus.Gauge
	vertexCausalMetadataOverLimit        prometheus.Gauge

	sampleInterval           time.Duration
	sample                   Sampler
	mlogSample               MutationLogSampler
	originSample             OriginStatesSampler
	searchIndexSample        SearchIndexSampler
	vertexHLCSample          VertexHLCSampler
	vertexHLCHighWaterSample VertexHLCSampler
	causalBarrierSample      CausalBarrierSampler
	causalMetadataSample     CausalMetadataSampler
	lastEvicted              uint64 // last observed cumulative eviction count
	lastVertexCausalRejected uint64
	lastEdgeCausalRejected   uint64
}

// Hot-path label values. Exposed so the service layer can reference the
// canonical string set without importing prometheus.
//
// Illuminate metrics carry four orthogonal labels (#410, #846, #961, #963):
//   - algorithm ∈ {bfs, ppr, community} — the traversal FAMILY (#801, #845).
//   - reduction ∈ {none, mst, spt} — the post-traversal tree reduction,
//     applied to the bfs and community families (a community walk's own
//     reduction is now broken out here rather than collapsed into the
//     family label — #963); "none" for the ppr family, which returns a
//     ranked star with no subgraph to reduce.
//   - objective ∈ {minimize, maximize} — direction for a mst/spt reduction
//     and the bfs per-hop pruning; constant/harmless when no reduction
//     applies (reduction "none", or the ppr family, a fixed maximiser)
//     but still recorded for label-symmetric scraping
//   - weighting ∈ {raw, tfidf, bm25}   — edge-weight transform before the walk
//
// Service code resolves enum UNSPECIFIED values to their canonical
// defaults BEFORE calling OnIlluminate so the label space stays bounded
// at 3 × 3 × 2 × 3 = 54 combinations. Unknown enum values (a future axis
// added in proto without a metrics update) fall through to "unknown"
// so a new variant cannot break label pre-warming on existing dashboards.
var (
	algorithmLabels        = []string{"bfs", "ppr", "community"}
	reductionLabels        = []string{"none", "mst", "spt"}
	objectiveLabels        = []string{"minimize", "maximize"}
	weightingLabels        = []string{"raw", "tfidf", "bm25"}
	illuminatePhases       = []string{"traversal", "optimize"}
	illuminateResultPhases = []string{"validation", "traversal", "reduction", "response", "complete"}
	illuminateCodes        = []string{"ok", "canceled", "deadline_exceeded", "invalid_argument", "failed_precondition", "resource_exhausted", "internal", "unknown"}
	scanOps                = []string{
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
	searchOutcomes = []string{
		"ok",
		"canceled",
		"deadline_exceeded",
		"invalid_argument",
		"failed_precondition",
		"resource_exhausted",
		"internal",
	}
	searchReasons = []string{
		"none",
		"no_hits",
		"canceled",
		"deadline",
		"invalid_options",
		"invalid_projection",
		"cursor_invalid",
		"cursor_stale",
		"disabled",
		"positions_disabled",
		"index_incomplete",
		"query_bytes",
		"admission",
		string(search.WorkQueryTerms),
		string(search.WorkDictionaryVisits),
		string(search.WorkPostingVisits),
		string(search.WorkPositionVisits),
		string(search.WorkExpirationVisits),
		"internal",
	}
	searchWorkKinds = []string{
		string(search.WorkQueryBytes),
		string(search.WorkQueryTokens),
		string(search.WorkQueryClauses),
		string(search.WorkQueryTerms),
		string(search.WorkDictionaryVisits),
		string(search.WorkExpansionRetained),
		string(search.WorkPostingVisits),
		string(search.WorkPositionVisits),
		string(search.WorkExpirationVisits),
		string(search.WorkCandidateVisits),
		string(search.WorkCandidateSkips),
	}
	searchModes         = []string{"server", "any", "all", "min_should", "unknown"}
	searchPhases        = []string{"analysis", "expansion", "selection"}
	searchIndexStates   = []string{"disabled", "healthy", "incomplete"}
	searchTerminalPairs = [][2]string{
		{"ok", "none"},
		{"ok", "no_hits"},
		{"canceled", "canceled"},
		{"deadline_exceeded", "deadline"},
		{"invalid_argument", "invalid_options"},
		{"failed_precondition", "disabled"},
		{"failed_precondition", "positions_disabled"},
		{"failed_precondition", "index_incomplete"},
		{"resource_exhausted", "query_bytes"},
		{"resource_exhausted", "admission"},
		{"resource_exhausted", string(search.WorkQueryTerms)},
		{"resource_exhausted", string(search.WorkDictionaryVisits)},
		{"resource_exhausted", string(search.WorkPostingVisits)},
		{"resource_exhausted", string(search.WorkPositionVisits)},
		{"resource_exhausted", string(search.WorkExpirationVisits)},
		{"internal", "internal"},
	}
	// replicationApplyOps covers every pb.MutationOp oneof variant. The
	// service layer translates the proto-internal case selector into one
	// of these strings; unknown variants fall through to "unknown" so a
	// new MutationOp added in proto without a metrics update still
	// scrapes without exploding the registry.
	replicationApplyOps = []string{
		"PutVertex",
		"PutVertices",
		"ReplicatedPutVertices",
		"DeleteVertex",
		"DeleteVertices",
		"DeleteVerticesByPrefix",
		"AddEdge",
		"AddEdges",
		"PutEdge",
		"PutEdges",
		"ReplicatedPutEdges",
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

// New registers the Lantern domain collectors on the supplied registerer and
// emits `lantern_build_info` immediately. It returns the DomainMetrics handle
// callers bind to cache/log/search samplers.
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
		searchConfigMatch: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lantern_search_config_match",
			Help: "1 when the named peer reports the exact local search capability fingerprint; 0 when missing or mismatched.",
		}, []string{"peer"}),
		searchConfigMismatch: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lantern_search_config_mismatch_total",
			Help: "Total pump or anti-entropy observations of a missing or mismatched peer search capability fingerprint.",
		}, []string{"peer"}),
		illuminateVisitedVertices: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_illuminate_visited_vertices",
			Help:    "Vertices in the subgraph returned by Illuminate, partitioned by traversal family (algorithm) + post-traversal reduction + objective + edge weighting (#410, #963).",
			Buckets: prometheus.ExponentialBuckets(1, 4, 10), // 1 .. ~262K
		}, []string{"algorithm", "reduction", "objective", "weighting"}),
		illuminateVisitedEdges: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_illuminate_visited_edges",
			Help:    "Edges in the subgraph returned by Illuminate, partitioned by traversal family (algorithm) + post-traversal reduction + objective + edge weighting (#410, #963).",
			Buckets: prometheus.ExponentialBuckets(1, 4, 10),
		}, []string{"algorithm", "reduction", "objective", "weighting"}),
		illuminateDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_illuminate_duration_seconds",
			Help:    "Wall-clock duration of Illuminate, partitioned by traversal family (algorithm) + reduction + objective + edge weighting and phase (traversal | optimize). #410, #963.",
			Buckets: prometheus.ExponentialBuckets(0.0001, 4, 8), // 0.1ms .. ~1.6s
		}, []string{"algorithm", "reduction", "objective", "weighting", "phase"}),
		illuminateCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lantern_illuminate_calls_total",
			Help: "Illuminate calls partitioned by traversal family, reduction, objective, weighting, terminal phase, and Connect code. Includes failures and timeouts (#999).",
		}, []string{"algorithm", "reduction", "objective", "weighting", "phase", "code"}),
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
		searchResults: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_search_results",
			Help:    "Number of hits returned by every terminal SearchVertices attempt, partitioned by bounded request mode and outcome.",
			Buckets: append([]float64{0}, prometheus.ExponentialBuckets(1, 4, 10)...),
		}, []string{"mode", "outcome"}),
		searchDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_search_duration_seconds",
			Help:    "End-to-end handler duration of every SearchVertices attempt, partitioned by bounded request mode and outcome.",
			Buckets: prometheus.ExponentialBuckets(0.0001, 4, 8), // 0.1ms .. ~1.6s
		}, []string{"mode", "outcome"}),
		searchPhaseDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_search_phase_duration_seconds",
			Help:    "Executor duration partitioned by bounded phase (analysis, expansion, selection) and request mode.",
			Buckets: prometheus.ExponentialBuckets(0.00001, 4, 10),
		}, []string{"phase", "mode"}),
		searchCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lantern_search_calls_total",
			Help: "Total SearchVertices attempts partitioned only by bounded option dimensions, terminal outcome, and reason. Query text, prefixes, and matched keys are never labels.",
		}, []string{"mode", "phrase", "fuzziness", "prefix_terms", "prefix_present", "outcome", "reason"}),
		searchWork: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_search_work",
			Help:    "Deterministic work charged by SearchVertices, partitioned by bounded work kind and request mode.",
			Buckets: prometheus.ExponentialBuckets(1, 4, 12),
		}, []string{"kind", "mode"}),
		searchRejections: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lantern_search_rejections_total",
			Help: "SearchVertices attempts rejected before a successful response, partitioned by a bounded reason.",
		}, []string{"reason"}),
		searchIndexTerms: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_search_index_terms",
			Help: "Current number of distinct terms in the search inverted index (#703). Sampled on the same cadence as lantern_vertices. Always 0 when LANTERN_SEARCH_ENABLED=false.",
		}),
		searchIndexDocs: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_search_index_docs",
			Help: "Current number of live documents (indexed vertices) in the search inverted index (#703/#1057). Sampled on the same cadence as lantern_vertices. Always 0 when LANTERN_SEARCH_ENABLED=false.",
		}),
		searchIndexPhysicalDocs:     prometheus.NewGauge(prometheus.GaugeOpts{Name: "lantern_search_index_physical_documents", Help: "Physical search-index documents before query-time TTL purge."}),
		searchIndexExpiredDocs:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "lantern_search_index_expired_documents", Help: "Expired physical search-index documents awaiting bounded query purge or background GC."}),
		searchIndexExpirationQueue:  prometheus.NewGauge(prometheus.GaugeOpts{Name: "lantern_search_index_expiration_queue_entries", Help: "Entries in the bounded search-index expiration min-heap."}),
		searchIndexExpirationPurged: prometheus.NewGauge(prometheus.GaugeOpts{Name: "lantern_search_index_expiration_purged", Help: "Cumulative documents removed by query-time expiration purge."}),
		searchIndexPurgeDuration:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "lantern_search_index_last_expiration_purge_duration_seconds", Help: "Wall duration of the latest query-time expiration purge."}),
		searchIndexRetainedTerms:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "lantern_search_index_retained_term_slots", Help: "Retained term-ID slots, including reusable tombstoned slots."}),
		searchIndexRetainedOrdinals: prometheus.NewGauge(prometheus.GaugeOpts{Name: "lantern_search_index_retained_ordinals", Help: "Retained document ordinal high-water after compaction."}),
		searchIndexPostings:         prometheus.NewGauge(prometheus.GaugeOpts{Name: "lantern_search_index_postings", Help: "Live distinct term-document posting entries."}),
		searchIndexPositions:        prometheus.NewGauge(prometheus.GaugeOpts{Name: "lantern_search_index_position_entries", Help: "Live positional entries retained by the search index."}),
		searchIndexLiveBytes:        prometheus.NewGauge(prometheus.GaugeOpts{Name: "lantern_search_index_estimated_live_bytes", Help: "Stable logical estimate of live search-index bytes."}),
		searchIndexRetainedBytes:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "lantern_search_index_estimated_retained_bytes", Help: "Stable logical estimate including retained high-water slots."}),
		searchIndexRetainedRatio:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "lantern_search_index_retained_ratio", Help: "Estimated retained bytes divided by max(estimated live bytes, 1); sustained growth indicates unreclaimed high-water storage."}),
		searchIndexRebuilds:         prometheus.NewGauge(prometheus.GaugeOpts{Name: "lantern_search_index_rebuild_count", Help: "Completed search-index compactions and bounded rebuilds in this process."}),
		searchIndexRebuildDuration:  prometheus.NewGauge(prometheus.GaugeOpts{Name: "lantern_search_index_last_rebuild_duration_seconds", Help: "Wall duration of the latest search-index compaction or rebuild."}),
		searchIndexHealthy:          prometheus.NewGauge(prometheus.GaugeOpts{Name: "lantern_search_index_healthy", Help: "1 when search can be served from a complete index; 0 otherwise."}),
		searchIndexState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lantern_search_index_state",
			Help: "One-hot current search-index state (disabled, healthy, incomplete).",
		}, []string{"state"}),
		vertexHLCEntries: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_vertex_hlc_entries",
			Help: "Current number of entries in the per-key LWW watermark map used by the replication apply path (#705). Tracks the live replicated-key set; a value growing monotonically across GC ticks signals the vertexHLC leak (issue #700). Always 0 on a single-node deployment.",
		}),
		vertexHLCHighWater: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_vertex_hlc_entries_high_water",
			Help: "Per-GC-cycle peak number of entries in the per-key LWW watermark map (vertexHLC), recorded at the start of each sweep before stale entries are drained (#727). Unlike lantern_vertex_hlc_entries (the post-sweep len, which reads low right after a drain), this is monotonic non-decreasing and records the all-time churn peak. Born-expired LWW churn (#700/#719) used to retain the map's bucket array at this size (Go never shrinks a map after delete); the sweep now reallocates the map after a large drain, so a high value here is a historical churn signal, not pinned heap. Always 0 on a single-node deployment.",
		}),
		vertexCausalBarriers: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_vertex_causal_barrier_entries",
			Help: "Retained vertex LWW floors from accepted-expired Put outcomes. These entries have no TTL and remain until superseded by an equal/newer Put; sustained growth is an intentional memory-retention signal.",
		}),
		edgeCausalBarriers: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_edge_causal_barrier_entries",
			Help: "Retained edge LWW floors from accepted-expired Put outcomes. These entries have no TTL and remain until superseded by an equal/newer Put; sustained growth is an intentional memory-retention signal.",
		}),
		causalMetadataEntries: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lantern_causal_metadata_entries",
			Help: "Current retained HA causal identities, partitioned by kind (vertex, edge). One live HLC floor, Put barrier, or Delete tombstone consumes one entry.",
		}, []string{"kind"}),
		causalMetadataEstimatedBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lantern_causal_metadata_estimated_bytes",
			Help: "Stable logical estimate of retained HA causal records, budget-ledger, and deadline-index bytes, partitioned by kind. This is allocator-independent and is not a process-heap measurement.",
		}, []string{"kind"}),
		causalMetadataEntriesHighWater: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lantern_causal_metadata_entries_high_water",
			Help: "All-time high-water count of retained HA causal identities, partitioned by kind.",
		}, []string{"kind"}),
		causalMetadataEstimatedBytesHighWater: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lantern_causal_metadata_estimated_bytes_high_water",
			Help: "All-time high-water stable byte estimate for retained HA causal metadata, partitioned by kind.",
		}, []string{"kind"}),
		causalMetadataRejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lantern_causal_metadata_rejected_total",
			Help: "Locally-originated mutation batches atomically rejected by the causal-metadata budget, partitioned by kind.",
		}, []string{"kind"}),
		causalMetadataOldestRetentionDeadline: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lantern_causal_metadata_oldest_retention_deadline_seconds",
			Help: "Unix timestamp of the oldest retained Delete-tombstone deadline, partitioned by kind; 0 when no tombstone is retained.",
		}, []string{"kind"}),
		causalMetadataLimit: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lantern_causal_metadata_limit",
			Help: "Configured local-origin causal-identity admission ceiling, partitioned by kind; 0 means unlimited. Replication apply is exempt.",
		}, []string{"kind"}),
		causalMetadataOverLimit: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lantern_causal_metadata_over_limit",
			Help: "1 when converged replicated causal identities exceed the configured local-origin limit, partitioned by kind; otherwise 0.",
		}, []string{"kind"}),
		vertexCausalMetadataEntries: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_vertex_causal_metadata_entries",
			Help: "Unlabelled release-gate alias of lantern_causal_metadata_entries{kind=\"vertex\"}.",
		}),
		vertexCausalMetadataEntriesHighWater: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_vertex_causal_metadata_entries_high_water",
			Help: "Unlabelled release-gate alias of lantern_causal_metadata_entries_high_water{kind=\"vertex\"}.",
		}),
		vertexCausalMetadataEstimatedBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_vertex_causal_metadata_estimated_bytes",
			Help: "Unlabelled release-gate alias of lantern_causal_metadata_estimated_bytes{kind=\"vertex\"}.",
		}),
		vertexCausalMetadataOverLimit: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lantern_vertex_causal_metadata_over_limit",
			Help: "Unlabelled release-gate alias of lantern_causal_metadata_over_limit{kind=\"vertex\"}.",
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
			Help: "Configured conservative live-plus-Put-barrier soft cap per kind (vertex, edge) from LANTERN_MAX_VERTICES / LANTERN_MAX_EDGES (#848). 0 = unlimited. For an approximate fill ratio, sum the matching live and causal-barrier gauges before dividing by this limit; alert above 0.8.",
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
		m.searchConfigMatch, m.searchConfigMismatch,
		m.illuminateVisitedVertices, m.illuminateVisitedEdges, m.illuminateDuration, m.illuminateCalls,
		m.scanResults, m.scanDuration, m.batchSize,
		m.getVertexHits, m.getVertexMisses, m.getEdgeHits, m.getEdgeMisses,
		m.edgeContribDeduped,
		m.searchResults, m.searchDuration, m.searchPhaseDuration, m.searchCalls, m.searchWork, m.searchRejections, m.searchIndexTerms, m.searchIndexDocs,
		m.searchIndexPhysicalDocs, m.searchIndexExpiredDocs, m.searchIndexExpirationQueue, m.searchIndexExpirationPurged, m.searchIndexPurgeDuration,
		m.searchIndexRetainedTerms, m.searchIndexRetainedOrdinals, m.searchIndexPostings, m.searchIndexPositions,
		m.searchIndexLiveBytes, m.searchIndexRetainedBytes, m.searchIndexRetainedRatio, m.searchIndexRebuilds, m.searchIndexRebuildDuration, m.searchIndexHealthy, m.searchIndexState,
		m.vertexHLCEntries, m.vertexHLCHighWater, m.vertexCausalBarriers, m.edgeCausalBarriers,
		m.causalMetadataEntries, m.causalMetadataEstimatedBytes,
		m.causalMetadataEntriesHighWater, m.causalMetadataEstimatedBytesHighWater,
		m.causalMetadataRejected, m.causalMetadataOldestRetentionDeadline,
		m.causalMetadataLimit, m.causalMetadataOverLimit,
		m.vertexCausalMetadataEntries, m.vertexCausalMetadataEntriesHighWater,
		m.vertexCausalMetadataEstimatedBytes, m.vertexCausalMetadataOverLimit,
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
	// idiomatic way to materialise the row. Per #410/#801/#963 the
	// Illuminate label space is 3 × 3 × 2 × 3 = 54 combinations
	// (algorithm × reduction × objective × weighting), well below
	// Prometheus cardinality concerns.
	for _, algo := range algorithmLabels {
		for _, red := range reductionLabels {
			for _, obj := range objectiveLabels {
				for _, w := range weightingLabels {
					m.illuminateVisitedVertices.WithLabelValues(algo, red, obj, w)
					m.illuminateVisitedEdges.WithLabelValues(algo, red, obj, w)
					for _, ph := range illuminatePhases {
						m.illuminateDuration.WithLabelValues(algo, red, obj, w, ph)
					}
					for _, ph := range illuminateResultPhases {
						for _, code := range illuminateCodes {
							m.illuminateCalls.WithLabelValues(algo, red, obj, w, ph, code)
						}
					}
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
	for _, mode := range searchModes {
		for _, outcome := range searchOutcomes {
			m.searchResults.WithLabelValues(mode, outcome)
			m.searchDuration.WithLabelValues(mode, outcome)
		}
		for _, pair := range searchTerminalPairs {
			m.searchCalls.WithLabelValues(mode, "no", "0", "no", "no", pair[0], pair[1])
		}
		for _, kind := range searchWorkKinds {
			m.searchWork.WithLabelValues(kind, mode)
		}
		for _, phase := range searchPhases {
			m.searchPhaseDuration.WithLabelValues(phase, mode)
		}
	}
	for _, reason := range searchReasons {
		if reason != "none" && reason != "no_hits" {
			m.searchRejections.WithLabelValues(reason)
		}
	}
	for _, state := range searchIndexStates {
		m.searchIndexState.WithLabelValues(state)
	}
	// Until a sampler is bound and ticked the index is disabled, not an
	// unknown all-zero state. A real sampler replaces this one-hot row on the
	// first tick with healthy or incomplete as appropriate.
	m.searchIndexState.WithLabelValues("disabled").Set(1)
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
	for _, kind := range []string{"vertex", "edge"} {
		m.causalMetadataEntries.WithLabelValues(kind).Set(0)
		m.causalMetadataEstimatedBytes.WithLabelValues(kind).Set(0)
		m.causalMetadataEntriesHighWater.WithLabelValues(kind).Set(0)
		m.causalMetadataEstimatedBytesHighWater.WithLabelValues(kind).Set(0)
		m.causalMetadataRejected.WithLabelValues(kind)
		m.causalMetadataOldestRetentionDeadline.WithLabelValues(kind).Set(0)
		m.causalMetadataLimit.WithLabelValues(kind).Set(0)
		m.causalMetadataOverLimit.WithLabelValues(kind).Set(0)
	}

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

// OnSearchConfig publishes the latest compatibility verdict and counts every
// mismatch observation. Fingerprints themselves stay in status/logs rather
// than metric labels to avoid unbounded cardinality.
func (m *DomainMetrics) OnSearchConfig(peer string, matched bool) {
	value := 0.0
	if matched {
		value = 1
	}
	m.searchConfigMatch.WithLabelValues(peer).Set(value)
	if !matched {
		m.searchConfigMismatch.WithLabelValues(peer).Inc()
	}
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
// when no reduction was requested. Per #410/#963 the labels are the four
// orthogonal axes (family algorithm, reduction, objective, weighting);
// service code is expected to have resolved UNSPECIFIED values to their
// canonical defaults already.
func (m *DomainMetrics) OnIlluminate(algorithm, reduction, objective, weighting string, visitedVertices, visitedEdges int, traversal, optimize time.Duration) {
	a := sanitizeLabel(algorithm, algorithmLabels, "unknown")
	r := sanitizeLabel(reduction, reductionLabels, "unknown")
	o := sanitizeLabel(objective, objectiveLabels, "unknown")
	w := sanitizeLabel(weighting, weightingLabels, "unknown")
	m.illuminateVisitedVertices.WithLabelValues(a, r, o, w).Observe(float64(visitedVertices))
	m.illuminateVisitedEdges.WithLabelValues(a, r, o, w).Observe(float64(visitedEdges))
	m.illuminateDuration.WithLabelValues(a, r, o, w, "traversal").Observe(traversal.Seconds())
	if optimize > 0 {
		m.illuminateDuration.WithLabelValues(a, r, o, w, "optimize").Observe(optimize.Seconds())
	}
}

// OnIlluminateResult records the terminal outcome of every Illuminate call,
// including failures that return before the success histograms are observed.
func (m *DomainMetrics) OnIlluminateResult(algorithm, reduction, objective, weighting, phase, code string) {
	a := sanitizeLabel(algorithm, algorithmLabels, "unknown")
	r := sanitizeLabel(reduction, reductionLabels, "unknown")
	o := sanitizeLabel(objective, objectiveLabels, "unknown")
	w := sanitizeLabel(weighting, weightingLabels, "unknown")
	p := sanitizeLabel(phase, illuminateResultPhases, "response")
	c := sanitizeLabel(code, illuminateCodes, "unknown")
	m.illuminateCalls.WithLabelValues(a, r, o, w, p, c).Inc()
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

// OnSearchExecution records exactly one bounded terminal observation for a
// SearchVertices attempt. Every string is sanitized against a finite set and
// the remaining dimensions are booleans or a fixed fuzziness bucket, so raw
// query text, prefixes, keys, and values cannot become labels.
func (m *DomainMetrics) OnSearchExecution(observation service.SearchObservation) {
	mode := sanitizeLabel(observation.Mode, searchModes, "unknown")
	outcome := sanitizeLabel(observation.Outcome, searchOutcomes, "internal")
	reason := sanitizeLabel(observation.Reason, searchReasons, "internal")
	phrase := boolLabel(observation.Phrase)
	prefixTerms := boolLabel(observation.PrefixTerms)
	prefixPresent := boolLabel(observation.PrefixPresent)
	fuzziness := fuzzinessLabel(observation.Fuzziness)
	m.searchCalls.WithLabelValues(mode, phrase, fuzziness, prefixTerms, prefixPresent, outcome, reason).Inc()
	m.searchResults.WithLabelValues(mode, outcome).Observe(float64(max(0, observation.Results)))
	m.searchDuration.WithLabelValues(mode, outcome).Observe(observation.TotalDuration.Seconds())
	if outcome == "invalid_argument" || outcome == "failed_precondition" || outcome == "resource_exhausted" || outcome == "internal" {
		m.searchRejections.WithLabelValues(reason).Inc()
	}
	for _, phase := range []struct {
		name     string
		duration time.Duration
	}{
		{"analysis", observation.Stats.AnalysisDuration},
		{"expansion", observation.Stats.ExpansionDuration},
		{"selection", observation.Stats.SelectionDuration},
	} {
		if phase.duration > 0 {
			m.searchPhaseDuration.WithLabelValues(phase.name, mode).Observe(phase.duration.Seconds())
		}
	}
	for _, item := range []struct {
		kind  string
		value int64
	}{
		{string(search.WorkQueryBytes), observation.Stats.QueryBytes},
		{string(search.WorkQueryTokens), observation.Stats.QueryTokens},
		{string(search.WorkQueryClauses), observation.Stats.QueryClauses},
		{string(search.WorkQueryTerms), observation.Stats.QueryTerms},
		{string(search.WorkDictionaryVisits), observation.Stats.DictionaryVisits},
		{string(search.WorkExpansionRetained), observation.Stats.ExpansionRetained},
		{string(search.WorkPostingVisits), observation.Stats.PostingVisits},
		{string(search.WorkPositionVisits), observation.Stats.PositionVisits},
		{string(search.WorkExpirationVisits), observation.Stats.ExpirationVisits},
		{string(search.WorkCandidateVisits), observation.Stats.CandidateVisits},
		{string(search.WorkCandidateSkips), observation.Stats.CandidateSkips},
	} {
		m.searchWork.WithLabelValues(item.kind, mode).Observe(float64(item.value))
	}
}

func boolLabel(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func fuzzinessLabel(value uint32) string {
	switch value {
	case 0:
		return "0"
	case 1:
		return "1"
	case 2:
		return "2"
	default:
		return "other"
	}
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

// BindCausalBarrierSampler installs retained accepted-expired Put cardinality
// sampling. Must be called before Run; safe to call exactly once during wiring.
func (m *DomainMetrics) BindCausalBarrierSampler(s CausalBarrierSampler) {
	m.causalBarrierSample = s
}

// BindCausalMetadataSampler installs the complete causal-identity capacity
// sampler. Must be called before Run; safe to call exactly once during wiring.
func (m *DomainMetrics) BindCausalMetadataSampler(s CausalMetadataSampler) {
	m.causalMetadataSample = s
}

// Run drives the gauge sampler on the configured cadence until ctx is done.
// Safe to launch as a goroutine. A nil sampler is treated as a no-op so
// tests can construct the collectors without wiring a cache.
func (m *DomainMetrics) Run(ctx context.Context) {
	if m.sample == nil && m.mlogSample == nil && m.originSample == nil &&
		m.searchIndexSample == nil && m.vertexHLCSample == nil &&
		m.vertexHLCHighWaterSample == nil && m.causalBarrierSample == nil &&
		m.causalMetadataSample == nil {
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
		stats := m.searchIndexSample()
		m.searchIndexTerms.Set(float64(stats.LiveTerms))
		m.searchIndexDocs.Set(float64(stats.Documents))
		m.searchIndexPhysicalDocs.Set(float64(stats.PhysicalDocuments))
		m.searchIndexExpiredDocs.Set(float64(stats.ExpiredDocuments))
		m.searchIndexExpirationQueue.Set(float64(stats.ExpirationQueueEntries))
		m.searchIndexExpirationPurged.Set(float64(stats.ExpirationPurged))
		m.searchIndexPurgeDuration.Set(stats.LastExpirationPurge.Seconds())
		m.searchIndexRetainedTerms.Set(float64(stats.RetainedTermSlots))
		m.searchIndexRetainedOrdinals.Set(float64(stats.RetainedOrdinals))
		m.searchIndexPostings.Set(float64(stats.Postings))
		m.searchIndexPositions.Set(float64(stats.PositionEntries))
		m.searchIndexLiveBytes.Set(float64(stats.EstimatedLiveBytes))
		m.searchIndexRetainedBytes.Set(float64(stats.EstimatedRetainedBytes))
		denominator := max(int64(1), stats.EstimatedLiveBytes)
		m.searchIndexRetainedRatio.Set(float64(stats.EstimatedRetainedBytes) / float64(denominator))
		m.searchIndexRebuilds.Set(float64(stats.RebuildCount))
		m.searchIndexRebuildDuration.Set(stats.LastRebuildDuration.Seconds())
		state := "disabled"
		if stats.Health == search.IndexHealthy {
			m.searchIndexHealthy.Set(1)
			state = "healthy"
		} else {
			m.searchIndexHealthy.Set(0)
			if stats.Health == search.IndexIncomplete {
				state = "incomplete"
			}
		}
		for _, candidate := range searchIndexStates {
			value := 0.0
			if candidate == state {
				value = 1
			}
			m.searchIndexState.WithLabelValues(candidate).Set(value)
		}
	}
	if m.vertexHLCSample != nil {
		m.vertexHLCEntries.Set(float64(m.vertexHLCSample()))
	}
	if m.vertexHLCHighWaterSample != nil {
		m.vertexHLCHighWater.Set(float64(m.vertexHLCHighWaterSample()))
	}
	if m.causalBarrierSample != nil {
		vertices, edges := m.causalBarrierSample()
		m.vertexCausalBarriers.Set(float64(vertices))
		m.edgeCausalBarriers.Set(float64(edges))
	}
	if m.causalMetadataSample != nil {
		m.sampleCausalMetadata(m.causalMetadataSample())
	}
}

func (m *DomainMetrics) sampleCausalMetadata(sample CausalMetadataSample) {
	type kindSample struct {
		kind                    string
		limit                   int
		entries                 int
		estimatedBytes          uint64
		entriesHighWater        int
		estimatedBytesHighWater uint64
		rejected                uint64
		overLimit               bool
		oldestDeadline          time.Time
		lastRejected            *uint64
	}
	for _, current := range []kindSample{
		{
			kind: "vertex", limit: sample.VertexLimit, entries: sample.VertexEntries,
			estimatedBytes: sample.VertexEstimatedBytes, entriesHighWater: sample.VertexEntriesHighWater,
			estimatedBytesHighWater: sample.VertexEstimatedBytesHighWater, rejected: sample.VertexRejected,
			overLimit: sample.VertexOverLimit, oldestDeadline: sample.OldestVertexRetentionDeadline,
			lastRejected: &m.lastVertexCausalRejected,
		},
		{
			kind: "edge", limit: sample.EdgeLimit, entries: sample.EdgeEntries,
			estimatedBytes: sample.EdgeEstimatedBytes, entriesHighWater: sample.EdgeEntriesHighWater,
			estimatedBytesHighWater: sample.EdgeEstimatedBytesHighWater, rejected: sample.EdgeRejected,
			overLimit: sample.EdgeOverLimit, oldestDeadline: sample.OldestEdgeRetentionDeadline,
			lastRejected: &m.lastEdgeCausalRejected,
		},
	} {
		m.causalMetadataLimit.WithLabelValues(current.kind).Set(nonNegativeFloat(current.limit))
		m.causalMetadataEntries.WithLabelValues(current.kind).Set(nonNegativeFloat(current.entries))
		m.causalMetadataEstimatedBytes.WithLabelValues(current.kind).Set(float64(current.estimatedBytes))
		m.causalMetadataEntriesHighWater.WithLabelValues(current.kind).Set(nonNegativeFloat(current.entriesHighWater))
		m.causalMetadataEstimatedBytesHighWater.WithLabelValues(current.kind).Set(float64(current.estimatedBytesHighWater))
		m.causalMetadataOverLimit.WithLabelValues(current.kind).Set(boolFloat(current.overLimit))
		deadline := 0.0
		if !current.oldestDeadline.IsZero() {
			deadline = float64(current.oldestDeadline.Unix()) +
				float64(current.oldestDeadline.Nanosecond())/float64(time.Second)
		}
		m.causalMetadataOldestRetentionDeadline.WithLabelValues(current.kind).Set(deadline)

		// The cache exposes an all-time cumulative count while Prometheus Counter
		// has no Set. Delta-apply it and treat a lower sample as a sampler reset:
		// adding the new value preserves monotonicity without unsigned underflow.
		delta := current.rejected - *current.lastRejected
		if current.rejected < *current.lastRejected {
			delta = current.rejected
		}
		if delta > 0 {
			m.causalMetadataRejected.WithLabelValues(current.kind).Add(float64(delta))
		}
		*current.lastRejected = current.rejected
	}

	m.vertexCausalMetadataEntries.Set(nonNegativeFloat(sample.VertexEntries))
	m.vertexCausalMetadataEntriesHighWater.Set(nonNegativeFloat(sample.VertexEntriesHighWater))
	m.vertexCausalMetadataEstimatedBytes.Set(float64(sample.VertexEstimatedBytes))
	m.vertexCausalMetadataOverLimit.Set(boolFloat(sample.VertexOverLimit))
}

func nonNegativeFloat(value int) float64 {
	if value <= 0 {
		return 0
	}
	return float64(value)
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
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
