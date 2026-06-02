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
