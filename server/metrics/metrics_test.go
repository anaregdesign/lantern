package metrics

import (
	"context"
	"runtime"
	"testing"
	"time"

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
