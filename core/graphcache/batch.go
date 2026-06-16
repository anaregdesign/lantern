package graphcache

import "time"

// VertexItem is a single (key, value, expiration) tuple supplied to
// PutVerticesWithExpiration.
type VertexItem[S comparable, T any] struct {
	Key        S
	Value      T
	Expiration time.Time
}

// EdgeItem is a single (tail, head, weight, expiration) tuple supplied to
// AddEdgesWithExpiration / PutEdgesWithExpiration.
type EdgeItem[S comparable] struct {
	Tail       S
	Head       S
	Weight     float32
	Expiration time.Time
	// ContribID is an optional dedup key for additive AddEdges* writes.
	// A non-zero id makes the contribution idempotent: re-applying an item
	// with the same id leaves the stored weight unchanged (see
	// AddEdgesWithExpirationContrib). The zero value disables dedup and keeps
	// the legacy additive semantics. PutEdgesWithExpiration ignores this
	// field — Put is already idempotent.
	ContribID ContribID
}

// EdgeKey identifies a directed edge for batch deletion via DeleteEdges.
type EdgeKey[S comparable] struct {
	Tail S
	Head S
}

// PutVerticesWithExpiration writes every supplied vertex under a single
// write lock. Concurrent readers observe either the pre-batch or the
// post-batch state — never an intermediate snapshot where some keys are
// present and others are not.
func (c *GraphCache[S, T]) PutVerticesWithExpiration(items []VertexItem[S, T]) {
	if len(items) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, it := range items {
		c.putVertexLocked(it.Key, it.Value, it.Expiration)
	}
}

// AddEdgesWithExpiration additively writes every supplied edge under a
// single write lock, auto-creating endpoint vertices on demand (matching
// the per-edge AddEdgeWithExpiration invariant). Concurrent readers see
// either the pre-batch or the post-batch state.
//
// Each item's ContribID is honored: a non-zero id deduplicates the
// contribution (see AddEdgesWithExpirationContrib). Leaving ContribID at its
// zero value — the default — keeps the legacy non-idempotent additive path.
func (c *GraphCache[S, T]) AddEdgesWithExpiration(items []EdgeItem[S]) {
	c.AddEdgesWithExpirationContrib(items)
}

// AddEdgesWithExpirationContrib is the dedup-aware batch sibling of
// AddEdgesWithExpiration. It applies the whole batch under a single write
// lock and returns the number of items that were deduplicated — i.e. whose
// non-zero ContribID matched a live contribution already stored on the
// (tail, head) edge, so no weight was added. Items with a zero ContribID
// always apply (legacy additive semantics) and never count as deduped.
//
// The dedup guarantee mirrors the per-edge AddEdgeWithExpirationContrib used
// by the replication apply path (#182): replaying a batch with the same
// ContribIDs leaves the stored weights unchanged. This is what lets a
// client-supplied idempotency key make AddEdge(s) safe to retry (#588).
func (c *GraphCache[S, T]) AddEdgesWithExpirationContrib(items []EdgeItem[S]) (deduped int) {
	if len(items) == 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, it := range items {
		c.ensureVertexLocked(it.Tail, it.Expiration)
		c.ensureVertexLocked(it.Head, it.Expiration)
		created, tailID, headID, applied := c.edges.addWithExpirationContrib(it.Tail, it.Head, it.Weight, it.Expiration, it.ContribID)
		if applied {
			c.onEdgeAddedLocked(created, tailID, headID, it.Head)
			continue
		}
		deduped++
		if created {
			// Defensive: a dedup-skip cannot also create a fresh bucket
			// (see AddEdgeWithExpirationContrib), but keep the side indexes
			// consistent if that invariant ever changes.
			c.onEdgeAddedLocked(created, tailID, headID, it.Head)
		}
	}
	return deduped
}

// PutEdgesWithExpiration replaces every supplied edge atomically under a
// single write lock. Each (tail, head) pair is deleted and re-added so the
// resulting weight is exactly the supplied weight — matching
// PutEdgeWithExpiration semantics for each item in the batch.
func (c *GraphCache[S, T]) PutEdgesWithExpiration(items []EdgeItem[S]) {
	if len(items) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, it := range items {
		c.ensureVertexLocked(it.Tail, it.Expiration)
		c.ensureVertexLocked(it.Head, it.Expiration)
		c.edges.delete(it.Tail, it.Head)
		created, tailID, headID := c.edges.addWithExpiration(it.Tail, it.Head, it.Weight, it.Expiration)
		c.onEdgeAddedLocked(created, tailID, headID, it.Head)
	}
}

// DeleteVertices removes every supplied vertex under a single write lock
// and returns the count of keys that were actually present (and therefore
// deleted). Concurrent readers observe either the pre-batch or the
// post-batch state.
func (c *GraphCache[S, T]) DeleteVertices(keys []S) int {
	if len(keys) == 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var n int
	for _, k := range keys {
		if c.vertices.Delete(k) {
			n++
		}
	}
	return n
}

// DeleteEdges removes every supplied edge under a single write lock and
// returns the count of edges that were actually present. Concurrent
// readers observe either the pre-batch or the post-batch state.
func (c *GraphCache[S, T]) DeleteEdges(keys []EdgeKey[S]) int {
	if len(keys) == 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var n int
	for _, k := range keys {
		deleted, tailID, headID := c.edges.delete(k.Tail, k.Head)
		if deleted {
			c.onEdgeDeletedLocked(tailID, headID, k.Head)
			n++
		}
	}
	return n
}
