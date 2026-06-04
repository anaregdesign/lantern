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
	rateLimitRejected      prometheus.Counter
	tombstoneClampRejected prometheus.Counter

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

	sampleInterval time.Duration
	sample         Sampler
	mlogSample     MutationLogSampler
	originSample   OriginStatesSampler
	lastEvicted    uint64 // last observed cumulative eviction count
}

// Hot-path label values. Exposed so the service layer can reference the
// canonical string set without importing prometheus.
//
// optimizationLabels covers every pb.Optimization variant. Service code
// translates the enum into one of these strings and passes it to
// OnIlluminate; unknown variants are normalised to "unspecified" so a new
// optimization added in proto without a metrics update cannot break label
// pre-warming on existing dashboards.
var (
	optimizationLabels = []string{"unspecified", "mst", "max_mst", "spt", "spt_inverse"}
	illuminatePhases   = []string{"traversal", "optimize"}
	scanOps            = []string{
		"ScanVertices",
		"ScanEdges",
		"DeleteVerticesByPrefix",
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
	}
	// validationRejectReasons is the bounded reason set bumped on
	// lantern_validation_rejected_total. Sources:
	//   - ValidationInterceptor (server/provider/extra.go): empty_key,
	//     key_too_long, empty_batch, batch_too_large, nil_item,
	//     bad_weight, step_too_large, k_too_large
	//   - service.LanternService.validateExpiration: bad_ttl
	//   - service prefix-scan cursor decode: bad_cursor
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
			Help:    "Vertices in the subgraph returned by Illuminate, partitioned by post-traversal optimization.",
			Buckets: prometheus.ExponentialBuckets(1, 4, 10), // 1 .. ~262K
		}, []string{"optimization"}),
		illuminateVisitedEdges: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_illuminate_visited_edges",
			Help:    "Edges in the subgraph returned by Illuminate, partitioned by post-traversal optimization.",
			Buckets: prometheus.ExponentialBuckets(1, 4, 10),
		}, []string{"optimization"}),
		illuminateDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_illuminate_duration_seconds",
			Help:    "Wall-clock duration of Illuminate, partitioned by optimization and phase (traversal | optimize).",
			Buckets: prometheus.ExponentialBuckets(0.0001, 4, 8), // 0.1ms .. ~1.6s
		}, []string{"optimization", "phase"}),
		scanResults: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_scan_results",
			Help:    "Number of results returned by a prefix scan RPC, partitioned by op (ScanVertices | ScanEdges | DeleteVerticesByPrefix).",
			Buckets: prometheus.ExponentialBuckets(1, 4, 10),
		}, []string{"op"}),
		scanDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_scan_duration_seconds",
			Help:    "Wall-clock duration of a prefix scan RPC, partitioned by op.",
			Buckets: prometheus.ExponentialBuckets(0.0001, 4, 8),
		}, []string{"op"}),
		batchSize: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lantern_batch_size",
			Help:    "Item count of a single plural-RPC batch (PutVertices, PutEdges, AddEdges, GetVertices, GetEdges, DeleteVertices, DeleteEdges). Singular RPC forwarders are not double-counted.",
			Buckets: prometheus.ExponentialBuckets(1, 4, 10),
		}, []string{"op"}),
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
			Help: "Total requests rejected by server-side input validation, partitioned by reason (empty_key, key_too_long, empty_batch, batch_too_large, nil_item, bad_weight, step_too_large, k_too_large, bad_ttl, bad_cursor). Counted before the handler runs (ValidationInterceptor) or during validateExpiration / cursor decode in the service layer.",
		}, []string{"reason"}),
		rateLimitRejected: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lantern_rate_limit_rejected_total",
			Help: "Total RPCs rejected by the process-wide token-bucket rate limiter (codes.ResourceExhausted). Registered at 0 even when LANTERN_RATE_LIMIT_RPS=0 so dashboards can compare deployments uniformly.",
		}),
		tombstoneClampRejected: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lantern_tombstone_clamp_rejected_total",
			Help: "Total ApplyMutation Put/Add operations dropped on the tombstone-aware HLC path because the incoming mutation lost the LWW comparison against an existing tombstone or a strictly-newer live HLC. A sustained rate indicates causally-late replication frames or clock skew larger than LANTERN_TOMBSTONE_TTL.",
		}),
		sampleInterval: opts.SampleInterval,
	}

	reg.MustRegister(m.vertices, m.edges, m.expirations, m.gcDuration, m.buildInfo,
		m.mutationLogEntries, m.mutationLogCapacity, m.subscribeActive, m.subscribeDropped,
		m.replicationApplied, m.replicationDropped, m.replicationLag,
		m.antiEntropyCycles, m.antiEntropyGapsFound,
		m.illuminateVisitedVertices, m.illuminateVisitedEdges, m.illuminateDuration,
		m.scanResults, m.scanDuration, m.batchSize,
		m.peerConnected, m.replicationApplyTotal, m.snapshotReplayedTotal,
		m.snapshotVertices, m.snapshotEdges, m.snapshotDuration,
		m.mutationLogFillRatio, m.mutationLogEvicted, m.originStatesCount,
		m.validationRejected, m.rateLimitRejected, m.tombstoneClampRejected)

	// Pre-create label rows so empty counters scrape as 0.
	for _, r := range []string{"gapped", "send_failed"} {
		m.subscribeDropped.WithLabelValues(r)
	}

	// Pre-create label rows so empty counters are still scraped as 0.
	for _, k := range []string{"vertex", "edge", "dangling_edge"} {
		m.expirations.WithLabelValues(k)
	}

	// Pre-create hot-path histogram label rows so /metrics renders the
	// full variant set on a fresh process. Histograms emit count/sum/
	// bucket families lazily per label; observing a no-op is the
	// idiomatic way to materialise the row.
	for _, opt := range optimizationLabels {
		m.illuminateVisitedVertices.WithLabelValues(opt)
		m.illuminateVisitedEdges.WithLabelValues(opt)
		for _, ph := range illuminatePhases {
			m.illuminateDuration.WithLabelValues(opt, ph)
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
// when no optimization was requested.
func (m *DomainMetrics) OnIlluminate(optimization string, visitedVertices, visitedEdges int, traversal, optimize time.Duration) {
	opt := sanitizeLabel(optimization, optimizationLabels, "unspecified")
	m.illuminateVisitedVertices.WithLabelValues(opt).Observe(float64(visitedVertices))
	m.illuminateVisitedEdges.WithLabelValues(opt).Observe(float64(visitedEdges))
	m.illuminateDuration.WithLabelValues(opt, "traversal").Observe(traversal.Seconds())
	if optimize > 0 {
		m.illuminateDuration.WithLabelValues(opt, "optimize").Observe(optimize.Seconds())
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

// Run drives the gauge sampler on the configured cadence until ctx is done.
// Safe to launch as a goroutine. A nil sampler is treated as a no-op so
// tests can construct the collectors without wiring a cache.
func (m *DomainMetrics) Run(ctx context.Context) {
	if m.sample == nil && m.mlogSample == nil && m.originSample == nil {
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
