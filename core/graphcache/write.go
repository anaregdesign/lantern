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
// invariant ever changes. Caller must hold c.mu.
func (c *GraphCache[S, T]) addEdgeContribLocked(tail, head S, w float32, expiration time.Time, contribID ContribID) bool {
	c.ensureVertexLocked(tail, expiration)
	c.ensureVertexLocked(head, expiration)
	created, tailID, headID, applied := c.edges.addWithExpirationContrib(tail, head, w, expiration, contribID)
	if applied || created {
		c.onEdgeAddedLocked(created, tailID, headID, head)
	}
	return applied
}

// putEdgeLocked atomically replaces one edge under the caller's aggregate
// write lock. It intentionally does not call onEdgeDeletedLocked for the
// temporary delete: the edge is immediately re-added with the same head key, so
// the head-side prefix index is unchanged and its insert path is idempotent.
func (c *GraphCache[S, T]) putEdgeLocked(tail, head S, w float32, expiration time.Time) {
	c.ensureVertexLocked(tail, expiration)
	c.ensureVertexLocked(head, expiration)
	c.edges.delete(tail, head)
	created, tailID, headID := c.edges.addWithExpiration(tail, head, w, expiration)
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
