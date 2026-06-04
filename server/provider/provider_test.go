package provider

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/cache/graph"
	v1 "github.com/anaregdesign/lantern/pb/graph/v1"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// TestWireCacheGCHooks_EmitsTickSummary asserts that a single
// "graph cache: gc tick" info log record is emitted per cache tick
// after WireCacheGCHooks installs the multiplexed hooks (#223).
func TestWireCacheGCHooks_EmitsTickSummary(t *testing.T) {
	cache := graph.NewGraphCache[string, *v1.Vertex](time.Second)
	reg := prometheus.NewRegistry()
	dm := domainmetrics.New(reg, domainmetrics.Options{})

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if got := WireCacheGCHooks(cache, dm, logger); got != (CacheGCHooksWired{}) {
		t.Fatalf("WireCacheGCHooks return = %+v, want empty marker", got)
	}

	// Drive the Watch loop on a short interval; expect at least one tick
	// to fire while we hold the goroutine open.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		cache.Watch(ctx, 10*time.Millisecond)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	recs := decodeRecords(t, &buf)
	if len(recs) == 0 {
		t.Fatalf("no log records emitted; want >=1 'graph cache: gc tick'")
	}
	rec := recs[0]
	if rec["msg"] != "graph cache: gc tick" {
		t.Errorf("msg = %v, want 'graph cache: gc tick'", rec["msg"])
	}
	for _, k := range []string{
		"vertices_expired", "edges_expired", "dangling_edges_removed",
		"vertices_remaining", "edges_remaining", "duration_ms",
	} {
		if _, ok := rec[k]; !ok {
			t.Errorf("missing field %q in record: %v", k, rec)
		}
	}
}
