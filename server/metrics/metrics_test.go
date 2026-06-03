package metrics

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
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
