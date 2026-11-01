package provider

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	v1 "github.com/anaregdesign/lantern/pb/graph/v1"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
)

func NewGraphCache(c CacheConfig, sc SearchConfig) *graphcache.GraphCache[string, *v1.Vertex] {
	gc := graphcache.NewGraphCacheWithStaging[string, *v1.Vertex](c.TTL)
	ConfigureGraphCache(gc, c, sc)
	return gc
}

// ConfigureGraphCache applies the production cache policy to a fresh cache.
// Receipt WAL and archive staging use the same policy before replay, while
// NewGraphCache uses it for the ordinary graph-only provider. Indexes must be
// enabled before the first mutation reaches the cache.
func ConfigureGraphCache(gc *graphcache.GraphCache[string, *v1.Vertex], c CacheConfig, sc SearchConfig) {
	if c.namespaceFormat != "" {
		gc.RetainDanglingEdgeHistory()
	}
	gc.SetGCEdgeBudget(c.GCEdgeBudget)
	gc.SetCausalMetadataLimits(graphcache.CausalMetadataLimits{
		MaxVertexEntries: c.MaxVertexCausalEntries,
		MaxEdgeEntries:   c.MaxEdgeCausalEntries,
	})
	// Identity projection: vertex keys are themselves the indexed string.
	// Enabling the prefix index up-front (before any insert) is required
	// by the cache contract — EnablePrefixIndex panics on a non-empty
	// cache. Doing it here in the constructor guarantees that invariant.
	gc.EnablePrefixIndex(func(s string) string { return s })
	// Content search (#624) is opt-out. EnableSearchIndex carries the same
	// "before any insert" contract as EnablePrefixIndex, so it must also be
	// called here; gating on sc.Enabled keeps the put hot path free of the
	// inverted-index cost when an operator turns the feature off. Positional
	// postings are a second opt-out (LANTERN_SEARCH_POSITIONS, #908): dropping
	// them shrinks the index at the cost of phrase/proximity fidelity.
	if sc.Enabled {
		var opts []graphcache.SearchIndexOption
		if !sc.Positions {
			opts = append(opts, graphcache.WithoutSearchPositions())
		}
		opts = append(opts, graphcache.WithSearchAnalysisLimits(sc.AnalysisLimits))
		projection := vertexSearchDocument
		if c.namespaceFormat != "" {
			projection = namespacedVertexSearchDocument
		}
		gc.EnableSearchIndex(projection, strings.Compare, opts...)
	}
}

// CacheGCHooksWired is a marker returned by WireCacheGCHooks so wire can
// force ordering: the wiring must happen after both DomainMetrics and the
// logger are constructed, but the result is not consumed by any downstream
// provider directly. newApp accepts it as an unused parameter.
type CacheGCHooksWired struct{}

// WireCacheGCHooks installs a multiplexed pair of GC hooks on the cache:
// one branch updates DomainMetrics (lantern_cache_evicted_total +
// lantern_cache_gc_duration_seconds), the other emits one info-level
// "graph cache: gc tick" slog line per tick summarising the work done
// (#223). The hooks are fired sequentially by GraphCache.Watch on a
// single goroutine, so the per-tick accumulator inside the closure is
// safe without locking.
func WireCacheGCHooks(
	cache *graphcache.GraphCache[string, *v1.Vertex],
	m *domainmetrics.DomainMetrics,
	logger *slog.Logger,
) CacheGCHooksWired {
	// Per-tick accumulator. GraphCache.Watch calls onExpire 0–3 times
	// (one per non-empty kind) then onGC once — atomically per tick,
	// on a single goroutine — so a plain struct is sufficient.
	var tick struct {
		vertices int
		edges    int
		dangling int
	}
	onExpire := func(kind string, n int) {
		m.OnExpire(kind, n)
		switch kind {
		case "vertex":
			tick.vertices += n
		case "edge":
			tick.edges += n
		case "dangling_edge":
			tick.dangling += n
		}
	}
	onGC := func(d time.Duration) {
		m.OnGCDuration(d)
		stats := cache.LastGCSweepStats()
		m.SetGCEdgeBacklog(stats.BacklogTails)
		logger.LogAttrs(context.Background(), slog.LevelInfo, "graph cache: gc tick",
			slog.Int("vertices_expired", tick.vertices),
			slog.Int("edges_expired", tick.edges),
			slog.Int("dangling_edges_removed", tick.dangling),
			slog.Int("vertices_remaining", cache.VertexCount()),
			slog.Int("edges_remaining", cache.EdgeCount()),
			slog.Int64("duration_ms", d.Milliseconds()),
			slog.Int64("duration_ns", d.Nanoseconds()),
			slog.Int("tails_scanned", stats.ScannedTails),
			slog.Int("edges_scanned", stats.ScannedEdges),
			slog.Int("contributions_expired", stats.ExpiredContributions),
			slog.Int("contributions_compacted_in_live_edges", stats.CompactedContributionsInLiveBuckets),
			slog.Int("tail_backlog", stats.BacklogTails),
		)
		tick.vertices, tick.edges, tick.dangling = 0, 0, 0
	}
	cache.SetGCHooks(onExpire, onGC)
	return CacheGCHooksWired{}
}
