package metrics

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/concurrent/pubsub"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

func TestDomainMetrics_ExposesLanternFamilies(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{Version: "v0.7.0", Commit: "deadbeef", SampleInterval: time.Hour})
	m.BindSampler(func() (int, int) { return 12, 34 })

	// Drive one tick directly so gauges have values without spinning up Run.
	m.tick()
	m.OnExpire("vertex", 2)
	m.OnExpire("edge", 5)
	m.OnExpire("dangling_edge", 1)
	m.OnGCDuration(7 * time.Millisecond)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := make(map[string]bool, len(mfs))
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{
		"lantern_vertices",
		"lantern_edges",
		"lantern_ttl_expirations_total",
		"lantern_gc_duration_seconds",
		"lantern_build_info",
	} {
		if !names[want] {
			t.Errorf("metric family %q not registered", want)
		}
	}

	if got := testutil.ToFloat64(m.vertices); got != 12 {
		t.Errorf("lantern_vertices = %v, want 12", got)
	}
	if got := testutil.ToFloat64(m.edges); got != 34 {
		t.Errorf("lantern_edges = %v, want 34", got)
	}
	if got := testutil.ToFloat64(m.expirations.WithLabelValues("vertex")); got != 2 {
		t.Errorf("expirations[vertex] = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.expirations.WithLabelValues("edge")); got != 5 {
		t.Errorf("expirations[edge] = %v, want 5", got)
	}
	if got := testutil.ToFloat64(m.expirations.WithLabelValues("dangling_edge")); got != 1 {
		t.Errorf("expirations[dangling_edge] = %v, want 1", got)
	}

	// build_info should be exactly 1 with the labels we passed.
	if got := testutil.ToFloat64(m.buildInfo.WithLabelValues("v0.7.0", "deadbeef", runtime.Version())); got != 1 {
		t.Errorf("lantern_build_info gauge = %v, want 1", got)
	}
}

func TestDomainMetrics_Run_NoSampler_StopsOnContextCancel(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{SampleInterval: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

// TestDomainMetrics_ReplicationFamilies asserts the #187 replication and
// anti-entropy collectors register with the expected names and that the
// adapter methods used by the pump (Metrics) and anti-entropy driver
// (AntiEntropyMetrics) interfaces emit on the right counters/gauges.
func TestDomainMetrics_ReplicationFamilies(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{Version: "v", Commit: "c", SampleInterval: time.Hour})

	// Drive each emission path once.
	m.OnReplicationApplied("aabbccdd")
	m.OnReplicationDropped("peer-a:7000", "ctx_cancel")
	m.OnPumpDropSelfEcho("peer-b:7000") // → dropped{peer,self_echo}
	m.OnAntiEntropyCycle()
	m.OnAntiEntropyBehind("peer-a:7000", "aabbccdd", 42)   // gauge=42, gaps=1
	m.OnAntiEntropyCaughtUp("peer-a:7000", "aabbccdd", 42) // gauge→0

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{
		"lantern_replication_applied_total",
		"lantern_replication_dropped_total",
		"lantern_replication_lag_seq",
		"lantern_anti_entropy_cycles_total",
		"lantern_anti_entropy_gaps_found_total",
	} {
		if !names[want] {
			t.Errorf("metric family %q not registered", want)
		}
	}

	if got := testutil.ToFloat64(m.replicationApplied.WithLabelValues("aabbccdd")); got != 1 {
		t.Errorf("replication_applied_total{origin=aabbccdd} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.replicationDropped.WithLabelValues("peer-a:7000", "ctx_cancel")); got != 1 {
		t.Errorf("replication_dropped_total{peer-a,ctx_cancel} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.replicationDropped.WithLabelValues("peer-b:7000", "self_echo")); got != 1 {
		t.Errorf("replication_dropped_total{peer-b,self_echo} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.antiEntropyCycles); got != 1 {
		t.Errorf("anti_entropy_cycles_total = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.antiEntropyGapsFound.WithLabelValues("peer-a:7000", "aabbccdd")); got != 1 {
		t.Errorf("anti_entropy_gaps_found_total = %v, want 1", got)
	}
	// CaughtUp reset the lag back to 0 after the Behind tick set it to 42.
	if got := testutil.ToFloat64(m.replicationLag.WithLabelValues("peer-a:7000", "aabbccdd")); got != 0 {
		t.Errorf("replication_lag_seq after CaughtUp = %v, want 0", got)
	}
}

// TestDomainMetrics_HotPathFamilies asserts the #220 hot-path collectors
// register with the expected family names, that the label set is
// pre-warmed (so dashboards render the full variant set from process
// start), and that the OnIlluminate / OnScan / OnBatch adapters emit on
// the right histograms.
func TestDomainMetrics_HotPathFamilies(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{SampleInterval: time.Hour})

	m.OnIlluminate("mst", 17, 42, 5*time.Millisecond, 2*time.Millisecond)
	m.OnIlluminate("unspecified", 1, 0, 100*time.Microsecond, 0) // optimize=0 → no observation on optimize phase
	m.OnScan("ScanVertices", 64, 750*time.Microsecond)
	m.OnScan("ScanEdges", 128, time.Millisecond)
	m.OnScan("DeleteVerticesByPrefix", 32, 200*time.Microsecond)
	m.OnBatch("PutVertices", 8)
	m.OnBatch("AddEdges", 4)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{
		"lantern_illuminate_visited_vertices",
		"lantern_illuminate_visited_edges",
		"lantern_illuminate_duration_seconds",
		"lantern_scan_results",
		"lantern_scan_duration_seconds",
		"lantern_batch_size",
	} {
		if !names[want] {
			t.Errorf("metric family %q not registered", want)
		}
	}

	// Pre-warming: every optimization label should have a histogram row
	// for the traversal phase even though only "mst" and "unspecified"
	// have been observed.
	for _, opt := range optimizationLabels {
		if got := testutil.CollectAndCount(m.illuminateDuration.WithLabelValues(opt, "traversal").(prometheus.Histogram)); got == 0 {
			t.Errorf("illuminate_duration{optimization=%q,phase=traversal} row not pre-warmed", opt)
		}
	}

	// Observed values land on the right rows.
	if got := histSampleCount(t, m.illuminateVisitedVertices.WithLabelValues("mst")); got != 1 {
		t.Errorf("illuminate_visited_vertices{mst} sample count = %v, want 1", got)
	}
	if got := histSampleCount(t, m.illuminateDuration.WithLabelValues("mst", "optimize")); got != 1 {
		t.Errorf("illuminate_duration{mst,optimize} sample count = %v, want 1", got)
	}
	// optimize=0 must NOT record on the optimize phase (it would skew
	// p99 toward zero on RPCs that didn't run an optimizer).
	if got := histSampleCount(t, m.illuminateDuration.WithLabelValues("unspecified", "optimize")); got != 0 {
		t.Errorf("illuminate_duration{unspecified,optimize} sample count = %v, want 0 (optimize=0 must not observe)", got)
	}

	for _, op := range scanOps {
		if got := histSampleCount(t, m.scanResults.WithLabelValues(op)); got != 1 {
			t.Errorf("scan_results{%s} sample count = %v, want 1", op, got)
		}
	}

	if got := histSampleCount(t, m.batchSize.WithLabelValues("PutVertices")); got != 1 {
		t.Errorf("batch_size{PutVertices} sample count = %v, want 1", got)
	}
	if got := histSampleCount(t, m.batchSize.WithLabelValues("AddEdges")); got != 1 {
		t.Errorf("batch_size{AddEdges} sample count = %v, want 1", got)
	}
}

// histSampleCount extracts the observation count from a histogram via
// the dto round-trip. testutil.CollectAndCount counts series, not
// samples — which is the opposite of what these assertions need.
func histSampleCount(t *testing.T, o prometheus.Observer) uint64 {
	t.Helper()
	h, ok := o.(prometheus.Histogram)
	if !ok {
		t.Fatalf("observer is not a Histogram: %T", o)
	}
	var m dto.Metric
	if err := h.Write(&m); err != nil {
		t.Fatalf("histogram write: %v", err)
	}
	if m.Histogram == nil {
		return 0
	}
	return m.Histogram.GetSampleCount()
}

// TestDomainMetrics_HotPathSanitizesUnknownOptimization confirms a stale
// or typo'd optimization label folds onto "unspecified" rather than
// inflating the cardinality of the histogram.
func TestDomainMetrics_HotPathSanitizesUnknownOptimization(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{SampleInterval: time.Hour})

	m.OnIlluminate("bogus", 1, 1, time.Microsecond, 0)
	if got := histSampleCount(t, m.illuminateVisitedVertices.WithLabelValues("unspecified")); got != 1 {
		t.Errorf("unknown optimization should fall back to unspecified; sample count = %v, want 1", got)
	}
	if got := histSampleCount(t, m.illuminateVisitedVertices.WithLabelValues("bogus")); got != 0 {
		t.Errorf("unknown optimization should NOT route observations to its own row; sample count = %v, want 0", got)
	}
}

// TestDomainMetrics_ReplicationObservabilityFamilies asserts the #221
// collectors register, the per-MutationOp counter is pre-warmed, the
// pump adapter flips the per-peer gauge and emits the snapshot
// histograms, and the periodic tick populates mutation-log + origin
// gauges from the bound samplers.
func TestDomainMetrics_ReplicationObservabilityFamilies(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{SampleInterval: time.Hour})

	// Adapter paths.
	m.OnPumpConnect("peer-a:7000")
	m.OnPumpConnect("peer-b:7000")
	m.OnPumpSnapshotReplayed("peer-a:7000", 100, 250, 5*time.Millisecond)
	m.OnPumpDisconnect("peer-b:7000", "ctx_cancel")
	for _, op := range []string{"PutVertex", "PutVertices", "AddEdge", "DeleteEdges"} {
		m.OnReplicationApply(op)
	}
	m.OnReplicationApply("bogus-op") // → "unknown"

	// Periodic tick should pick up bound samplers.
	m.BindMutationLogSampler(func() (int, int, uint64) { return 750, 1000, 42 })
	m.BindOriginStatesSampler(func() int { return 3 })
	m.tick()

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{
		"lantern_peer_connected",
		"lantern_replication_apply_total",
		"lantern_snapshot_replayed_total",
		"lantern_snapshot_vertices",
		"lantern_snapshot_edges",
		"lantern_snapshot_duration_seconds",
		"lantern_mutation_log_fill_ratio",
		"lantern_mutation_log_evicted_total",
		"lantern_origin_states_count",
	} {
		if !names[want] {
			t.Errorf("metric family %q not registered", want)
		}
	}

	// peer-a connected → 1, peer-b disconnected → 0.
	if got := testutil.ToFloat64(m.peerConnected.WithLabelValues("peer-a:7000")); got != 1 {
		t.Errorf("peer_connected{peer-a} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.peerConnected.WithLabelValues("peer-b:7000")); got != 0 {
		t.Errorf("peer_connected{peer-b} = %v, want 0", got)
	}
	// Disconnect with reason fans out to replication_dropped_total.
	if got := testutil.ToFloat64(m.replicationDropped.WithLabelValues("peer-b:7000", "ctx_cancel")); got != 1 {
		t.Errorf("replication_dropped_total{peer-b,ctx_cancel} = %v, want 1", got)
	}

	// Snapshot counter + histograms.
	if got := testutil.ToFloat64(m.snapshotReplayedTotal.WithLabelValues("peer-a:7000")); got != 1 {
		t.Errorf("snapshot_replayed_total{peer-a} = %v, want 1", got)
	}
	if got := histSampleCount(t, m.snapshotVertices.WithLabelValues("peer-a:7000")); got != 1 {
		t.Errorf("snapshot_vertices{peer-a} sample count = %v, want 1", got)
	}
	if got := histSampleCount(t, m.snapshotEdges.WithLabelValues("peer-a:7000")); got != 1 {
		t.Errorf("snapshot_edges{peer-a} sample count = %v, want 1", got)
	}
	if got := histSampleCount(t, m.snapshotDuration.WithLabelValues("peer-a:7000")); got != 1 {
		t.Errorf("snapshot_duration_seconds{peer-a} sample count = %v, want 1", got)
	}

	// Per-MutationOp counter: pre-warmed for all 11 variants and the
	// observed ones incremented exactly once.
	for _, op := range replicationApplyOps {
		if testutil.ToFloat64(m.replicationApplyTotal.WithLabelValues(op)) < 0 {
			t.Errorf("replication_apply_total{op=%q} not pre-warmed", op)
		}
	}
	for _, want := range []struct {
		op  string
		exp float64
	}{
		{"PutVertex", 1},
		{"PutVertices", 1},
		{"AddEdge", 1},
		{"DeleteEdges", 1},
		{"unknown", 1},
	} {
		if got := testutil.ToFloat64(m.replicationApplyTotal.WithLabelValues(want.op)); got != want.exp {
			t.Errorf("replication_apply_total{op=%q} = %v, want %v", want.op, got, want.exp)
		}
	}

	// Sampler-driven gauges populated by tick().
	if got := testutil.ToFloat64(m.mutationLogFillRatio); got != 0.75 {
		t.Errorf("mutation_log_fill_ratio = %v, want 0.75", got)
	}
	if got := testutil.ToFloat64(m.mutationLogEvicted); got != 42 {
		t.Errorf("mutation_log_evicted_total = %v, want 42", got)
	}
	if got := testutil.ToFloat64(m.originStatesCount); got != 3 {
		t.Errorf("origin_states_count = %v, want 3", got)
	}
}

// TestDomainMetrics_MutationLogEvictedMonotonic confirms the
// Counter.Add path delta-applies cumulative evicted samples and
// recovers gracefully from a sampler reset (e.g. log rebuild).
func TestDomainMetrics_MutationLogEvictedMonotonic(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{SampleInterval: time.Hour})

	var evicted uint64
	m.BindMutationLogSampler(func() (int, int, uint64) { return 1, 10, evicted })

	evicted = 5
	m.tick()
	if got := testutil.ToFloat64(m.mutationLogEvicted); got != 5 {
		t.Errorf("after first tick (5): counter = %v, want 5", got)
	}

	evicted = 12 // +7 since last sample
	m.tick()
	if got := testutil.ToFloat64(m.mutationLogEvicted); got != 12 {
		t.Errorf("after second tick (12): counter = %v, want 12", got)
	}

	evicted = 3 // sampler reset; treat as a fresh +3
	m.tick()
	if got := testutil.ToFloat64(m.mutationLogEvicted); got != 15 {
		t.Errorf("after reset+3: counter = %v, want 15 (12+3)", got)
	}
}

// TestDomainMetrics_RejectionFamilies asserts the #222 rejection
// collectors register, the validation reason label is pre-warmed for
// every canonical reason (plus the "unknown" fallback row), and the
// three adapters increment exactly the right counters.
func TestDomainMetrics_RejectionFamilies(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{SampleInterval: time.Hour})

	for _, r := range validationRejectReasons {
		m.OnValidationRejected(r)
	}
	m.OnValidationRejected("bogus-reason") // → "unknown"
	m.OnRateLimitRejected()
	m.OnRateLimitRejected()
	m.OnTombstoneClampRejected()

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{
		"lantern_validation_rejected_total",
		"lantern_rate_limit_rejected_total",
		"lantern_tombstone_clamp_rejected_total",
	} {
		if !names[want] {
			t.Errorf("metric family %q not registered", want)
		}
	}

	// Pre-warming: every canonical reason has a row visible from the
	// start (so dashboards never have to wait for the first reject).
	for _, r := range validationRejectReasons {
		if got := testutil.ToFloat64(m.validationRejected.WithLabelValues(r)); got != 1 {
			t.Errorf("validation_rejected_total{reason=%q} = %v, want 1", r, got)
		}
	}
	if got := testutil.ToFloat64(m.validationRejected.WithLabelValues("unknown")); got != 1 {
		t.Errorf("validation_rejected_total{reason=unknown} = %v, want 1 (bogus reason should fold)", got)
	}
	if got := testutil.ToFloat64(m.rateLimitRejected); got != 2 {
		t.Errorf("rate_limit_rejected_total = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.tombstoneClampRejected); got != 1 {
		t.Errorf("tombstone_clamp_rejected_total = %v, want 1", got)
	}
}

func TestDomainMetrics_PubsubFamiliesAndPreWarm(t *testing.T) {
	// Acceptance for #240: the three pubsub collectors must register and
	// every DropPolicies label row must scrape as 0 from process start
	// (pre-warmed in New). Methods must increment / observe correctly.
	reg := prometheus.NewRegistry()
	m := New(reg, Options{Version: "v0.7.0", Commit: "deadbeef", SampleInterval: time.Hour})

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := make(map[string]bool, len(mfs))
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{
		"lantern_subscription_queue_depth",
		"lantern_subscription_dropped_total",
		"lantern_subscription_dispatch_duration_seconds",
	} {
		if !names[want] {
			t.Errorf("metric family %q not registered", want)
		}
	}

	// Every DropPolicy label row must be pre-warmed at 0.
	for _, p := range pubsub.DropPolicies {
		if got := testutil.ToFloat64(m.pubsubDropped.WithLabelValues(p)); got != 0 {
			t.Errorf("pubsub_dropped_total{policy=%q} pre-warm = %v, want 0", p, got)
		}
	}

	// Methods bump the right collectors.
	m.OnPubsubQueueDepth(7)
	m.OnPubsubDrop(pubsub.DropPolicyNewest)
	m.OnPubsubDrop(pubsub.DropPolicyNewest)
	m.OnPubsubDrop(pubsub.DropPolicyOldest)
	m.OnPubsubDispatchDuration(3 * time.Millisecond)

	if got := testutil.ToFloat64(m.pubsubDropped.WithLabelValues(pubsub.DropPolicyNewest)); got != 2 {
		t.Errorf("pubsub_dropped_total{policy=drop_newest} = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.pubsubDropped.WithLabelValues(pubsub.DropPolicyOldest)); got != 1 {
		t.Errorf("pubsub_dropped_total{policy=drop_oldest} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.pubsubDropped.WithLabelValues(pubsub.DropPolicyNewestAfterOldest)); got != 0 {
		t.Errorf("pubsub_dropped_total{policy=drop_newest_after_oldest} = %v, want 0 (not called)", got)
	}

	// Histograms expose count via the dto.Metric; assert sample_count.
	if got := collectHistogramCount(t, m.pubsubQueueDepth); got != 1 {
		t.Errorf("pubsub_queue_depth count = %d, want 1", got)
	}
	if got := collectHistogramCount(t, m.pubsubDispatchDuration); got != 1 {
		t.Errorf("pubsub_dispatch_duration count = %d, want 1", got)
	}
}

func collectHistogramCount(t *testing.T, h prometheus.Histogram) uint64 {
	t.Helper()
	var dtoM dto.Metric
	if err := h.Write(&dtoM); err != nil {
		t.Fatalf("histogram write: %v", err)
	}
	return dtoM.GetHistogram().GetSampleCount()
}
