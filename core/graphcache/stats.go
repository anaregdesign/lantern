package graphcache

import "github.com/anaregdesign/lantern/core/search"

// VertexCount returns the live vertex count under an RLock. Intended for
// Prometheus gauges that sample the cache periodically.
func (c *GraphCache[S, T]) VertexCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.vertices.Count()
}

// EdgeCount returns the live (tail, head) edge count under an RLock. Like
// VertexCount it is intended for periodic gauge sampling, not hot paths.
func (c *GraphCache[S, T]) EdgeCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.edges.count()
}

// VertexHLCCount returns the number of entries in the per-key LWW watermark
// map (vertexHLC). Under a healthy workload this equals the number of distinct
// vertex keys ever touched by the replication apply path and should track the
// live vertex count after a full GC sweep. A value that grows without bound
// across GC ticks is a leak signal (see issue #700). Returns 0 when the map
// has never been initialised (no replicated writes have arrived yet).
func (c *GraphCache[S, T]) VertexHLCCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.vertexHLC)
}

// VertexHLCHighWater returns the largest number of vertexHLC entries ever
// observed at the start of a GC sweep — the per-cycle peak before stale
// watermarks are drained. Unlike VertexHLCCount (which reports the current,
// post-sweep len and therefore reads low right after a drain), this value is
// monotonic non-decreasing and records the all-time churn peak. It used to also
// proxy the map's retained bucket-array heap (Go never shrinks a map after
// delete), but sweepStaleVertexHLCLocked now reallocates the map after a large
// drain, so a high VertexHLCHighWater no longer implies pinned heap — it is a
// historical confirm-the-phenomenon signal for the ttl_churn leak gate
// (#700/#719/#727). Returns 0 when no sweep has run yet. Safe for concurrent
// use; intended for Prometheus gauge sampling.
func (c *GraphCache[S, T]) VertexHLCHighWater() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.vertexHLCHighWater
}

// CausalBarrierCounts returns the retained accepted-expired Put LWW floors for
// vertices and edges. Unlike VertexHLCCount these entries intentionally do not
// follow the live set and are not reaped by TTL GC; exposing both cardinalities
// lets operators account for their unbounded-by-time memory contract.
func (c *GraphCache[S, T]) CausalBarrierCounts() (vertices, edges int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.vertexCausalBarriers), len(c.edgeCausalBarriers)
}

// CapacityFootprint returns the identities retained by the live graph or by
// accepted-expired/expired-live causal barriers under one lock sample. The
// counts are deliberately conservative: an identity represented in both live
// additive edge state and a retained Put barrier is counted twice, matching
// the soft-cap admission policy's preference for bounded heap over precision.
func (c *GraphCache[S, T]) CapacityFootprint() (vertices, edges int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.vertices.Count() + len(c.vertexCausalBarriers), c.edges.count() + len(c.edgeCausalBarriers)
}

// SearchIndexStats returns the current number of distinct terms and indexed
// documents in the optional search index. Both values are 0 when the search
// index is disabled. Safe for concurrent use; intended for Prometheus gauge
// sampling.
func (c *GraphCache[S, T]) SearchIndexStats() (terms, docs int) {
	stats := c.SearchIndexMemoryStats()
	return stats.LiveTerms, stats.Documents
}

// SearchIndexMemoryStats returns the complete index capacity/health snapshot.
func (c *GraphCache[S, T]) SearchIndexMemoryStats() search.IndexMemoryStats {
	c.mu.RLock()
	idx := c.searchIndex
	c.mu.RUnlock()
	if idx == nil {
		return search.IndexMemoryStats{}
	}
	return idx.MemoryStats()
}
