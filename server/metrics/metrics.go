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

	sampleInterval time.Duration
	sample         Sampler
}

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
		sampleInterval: opts.SampleInterval,
	}

	reg.MustRegister(m.vertices, m.edges, m.expirations, m.gcDuration, m.buildInfo,
		m.mutationLogEntries, m.mutationLogCapacity, m.subscribeActive, m.subscribeDropped)

	// Pre-create label rows so empty counters scrape as 0.
	for _, r := range []string{"gapped", "send_failed"} {
		m.subscribeDropped.WithLabelValues(r)
	}

	// Pre-create label rows so empty counters are still scraped as 0.
	for _, k := range []string{"vertex", "edge", "dangling_edge"} {
		m.expirations.WithLabelValues(k)
	}

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

// BindSampler stores the gauge-population callback. Must be called before
// Run; safe to call exactly once during wiring.
func (m *DomainMetrics) BindSampler(s Sampler) {
	m.sample = s
}

// Run drives the gauge sampler on the configured cadence until ctx is done.
// Safe to launch as a goroutine. A nil sampler is treated as a no-op so
// tests can construct the collectors without wiring a cache.
func (m *DomainMetrics) Run(ctx context.Context) {
	if m.sample == nil {
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
	v, e := m.sample()
	m.vertices.Set(float64(v))
	m.edges.Set(float64(e))
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
