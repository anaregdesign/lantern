package provider

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	v1 "github.com/anaregdesign/lantern/pb/graph/v1"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

func TestNewGraphCache_CausalMetadataLimits(t *testing.T) {
	cache := NewGraphCache(CacheConfig{CacheSettings: CacheSettings{
		TTL: time.Minute, MaxVertexCausalEntries: 7, MaxEdgeCausalEntries: 9,
	}}, SearchConfig{})
	stats := cache.CausalMetadataStats()
	if stats.MaxVertexEntries != 7 || stats.MaxEdgeEntries != 9 {
		t.Fatalf("NewGraphCache causal limits = (%d, %d), want (7, 9)", stats.MaxVertexEntries, stats.MaxEdgeEntries)
	}
}

func TestConfigureGraphCacheForStagedRecovery(t *testing.T) {
	cache := graphcache.NewGraphCacheWithStaging[string, *v1.Vertex](time.Minute)
	ConfigureGraphCache(cache, CacheConfig{CacheSettings: CacheSettings{
		TTL: time.Minute, MaxVertexCausalEntries: 7, MaxEdgeCausalEntries: 9,
	}}, SearchConfig{Enabled: true, Positions: true})
	stats := cache.CausalMetadataStats()
	if stats.MaxVertexEntries != 7 || stats.MaxEdgeEntries != 9 {
		t.Fatalf("staged causal limits = (%d, %d), want (7, 9)", stats.MaxVertexEntries, stats.MaxEdgeEntries)
	}
	if err := cache.PutVertexWithExpiration("indexed-key", &v1.Vertex{Key: "indexed-key"}, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := cache.CountByPrefix("indexed"); got != 1 {
		t.Fatalf("staged prefix index count = %d, want 1", got)
	}
	if hits := cache.SearchVertices("indexed", 10, ""); len(hits) != 1 || hits[0].ID != "indexed-key" {
		t.Fatalf("staged search index = %+v, want indexed-key", hits)
	}
}

// TestWireCacheGCHooks_EmitsTickSummary asserts that a single
// "graph cache: gc tick" info log record is emitted per cache tick
// after WireCacheGCHooks installs the multiplexed hooks (#223).
func TestWireCacheGCHooks_EmitsTickSummary(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *v1.Vertex](time.Second)
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
		"vertices_remaining", "edges_remaining", "duration_ms", "duration_ns",
		"tails_scanned", "edges_scanned", "contributions_expired",
		"contributions_compacted_in_live_edges", "tail_backlog",
	} {
		if _, ok := rec[k]; !ok {
			t.Errorf("missing field %q in record: %v", k, rec)
		}
	}
}

func TestNewGraphCache_GCEdgeBudget(t *testing.T) {
	cache := NewGraphCache(CacheConfig{CacheSettings: CacheSettings{TTL: time.Minute, GCEdgeBudget: 1}}, SearchConfig{})
	live := time.Now().Add(time.Minute)
	for _, key := range []string{"a", "b", "c"} {
		cache.PutVertexWithExpiration(key, &v1.Vertex{Key: key}, live)
	}
	cache.AddEdgeWithExpiration("a", "b", 1, live)
	cache.AddEdgeWithExpiration("b", "c", 1, live)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := make(chan graphcache.GCSweepStats, 1)
	cache.SetGCHooks(nil, func(time.Duration) {
		seen <- cache.LastGCSweepStats()
		cancel()
	})
	go cache.Watch(ctx, time.Millisecond)
	select {
	case stats := <-seen:
		if stats.ScannedTails != 1 || stats.BacklogTails != 1 {
			t.Fatalf("budgeted first tick = %+v, want one scanned and one backlog tail", stats)
		}
	case <-time.After(time.Second):
		t.Fatal("no GC tick")
	}
}
