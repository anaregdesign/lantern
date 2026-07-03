package graphcache

import (
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

// addEdgeLocked applies additive edge semantics after auto-creating both
// endpoint vertices. Caller must hold c.mu.
func (c *GraphCache[S, T]) addEdgeLocked(tail, head S, w float32, expiration time.Time) {
	c.ensureVertexLocked(tail, expiration)
	c.ensureVertexLocked(head, expiration)
	created, tailID, headID := c.edges.addWithExpiration(tail, head, w, expiration)
	c.onEdgeAddedLocked(created, tailID, headID, head)
}

// addEdgeContribLocked applies additive edge semantics with optional
// contribution dedup. The defensive created-without-applied branch preserves
// side-index correctness if edgeCache's current "new bucket always applies"
// invariant ever changes. It returns the dedup result plus the post-apply live
// weight sum (#897; on a dedup no-op, the current live sum). `now` supplies the
// liveness clock. Caller must hold c.mu.
func (c *GraphCache[S, T]) addEdgeContribLocked(tail, head S, w float32, expiration time.Time, contribID ContribID, now time.Time) (applied bool, effective float32) {
	c.ensureVertexLocked(tail, expiration)
	c.ensureVertexLocked(head, expiration)
	created, tailID, headID, applied, effective := c.edges.addWithExpirationContribAt(tail, head, w, expiration, contribID, now)
	if applied || created {
		c.onEdgeAddedLocked(created, tailID, headID, head)
	}
	return applied, effective
}

// tryAddExistingEdgeContrib is the lock-free hot path for additive writes to
// an edge that ALREADY exists between two LIVE endpoints. On success it
// appends to the edge weight without taking c.mu, returning ok=true and the
// dedup result; on any miss it returns ok=false so the caller falls back to
// the locked slow path (addEdgeContribLocked).
//
// It returns ok=false — deferring to the slow path — in every case that the
// slow path handles differently:
//   - dict is nil (test caches without interning),
//   - either endpoint is absent or interned-but-not-both (pinBoth miss),
//   - either endpoint is NOT live (expired-but-not-yet-flushed): the slow
//     path must revive it via ensureVertexLocked, and only the slow path may
//     mint the (tail, head) bucket,
//   - the (tail, head) bucket does not yet exist (new edge needs interning,
//     bucket creation, and onEdgeAddedLocked side-index maintenance).
//
// Restricting the fast path to existing edges between live endpoints is what
// makes skipping ensureVertexLocked and onEdgeAddedLocked correct:
// ensureVertexLocked is a no-op once an endpoint is live, and
// onEdgeAddedLocked is a no-op when the bucket already exists (created=false).
// pinBoth holds a refcount on each endpoint for the duration of the append so
// a concurrent DeleteEdge + vertex flush cannot free and recycle an id under
// us — see dictionary.pinBoth and edgeCache.addExistingContribByIDAt.
//
// `now` supplies the liveness clock and effective returns the post-apply live
// weight sum (#897).
func (c *GraphCache[S, T]) tryAddExistingEdgeContrib(tail, head S, w float32, expiration time.Time, contribID ContribID, now time.Time) (applied bool, effective float32, ok bool) {
	if c.dict == nil {
		return false, 0, false
	}
	tailID, headID, release, pinned := c.dict.pinBoth(tail, head)
	if !pinned {
		return false, 0, false
	}
	defer release()
	if !c.vertices.Has(tail) || !c.vertices.Has(head) {
		return false, 0, false
	}
	return c.edges.addExistingContribByIDAt(tailID, headID, w, expiration, contribID, now)
}

// putEdgeLocked atomically replaces one edge under the caller's aggregate
// write lock. It replaces an existing edge's weight in place (edgeCache.
// putWithExpiration → weight.replace) rather than delete+add, so a lock-free
// point reader (GetWeight / GetEdgeDetail, which dropped GraphCache.mu in #740)
// never observes a transient missing edge. It intentionally does not call
// onEdgeDeletedLocked: the head key is unchanged, so the head-side prefix index
// is untouched and its insert path is idempotent.
func (c *GraphCache[S, T]) putEdgeLocked(tail, head S, w float32, expiration time.Time) {
	c.ensureVertexLocked(tail, expiration)
	c.ensureVertexLocked(head, expiration)
	created, tailID, headID := c.edges.putWithExpiration(tail, head, w, expiration)
	c.onEdgeAddedLocked(created, tailID, headID, head)
}

// putEdgeHLCLocked applies replicated LWW Put semantics after endpoint
// creation. Endpoint vertices are created even when the edge write loses inside
// edgeCache's HLC register, matching PutEdgeWithExpirationHLC's public
// contract. Caller must hold c.mu.
func (c *GraphCache[S, T]) putEdgeHLCLocked(tail, head S, w float32, expiration time.Time, ts hlc.Timestamp) bool {
	c.ensureVertexLocked(tail, expiration)
	c.ensureVertexLocked(head, expiration)
	created, tailID, headID, applied := c.edges.putWithExpirationHLC(tail, head, w, expiration, ts)
	if created {
		c.onEdgeAddedLocked(created, tailID, headID, head)
	}
	return applied
}

// deleteEdgeLocked removes one edge and updates all edge-owned secondary
// indexes when the storage layer reports that a bucket was present. Caller
// must hold c.mu.
func (c *GraphCache[S, T]) deleteEdgeLocked(tail, head S) bool {
	deleted, tailID, headID := c.edges.delete(tail, head)
	if deleted {
		c.onEdgeDeletedLocked(tailID, headID, head)
	}
	return deleted
}
