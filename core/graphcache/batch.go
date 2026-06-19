package graphcache

import (
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

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
		c.putLocalVertexLocked(it.Key, it.Value, it.Expiration)
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

// PutVerticesWithExpirationHLC is the LWW-aware, single-lock batch sibling of
// PutVerticesWithExpiration used by the LOCAL write path when replication is
// enabled. Every item is stamped with the SAME ts — the HLC the originating
// mutation is logged under — so the origin's own writes participate in
// last-writer-wins on equal footing with the values its peers apply from its
// mutation log.
//
// This closes a convergence hole: when the local path used the non-HLC
// PutVerticesWithExpiration it stored a value WITHOUT recording a vertexHLC
// watermark, so a concurrently-written OLDER value replayed from a peer would
// clobber the origin's newer value on the origin (the incoming write saw no
// watermark to lose to) while every other replica kept the newer value —
// permanent divergence for the same key. Stamping the watermark here makes
// PutVertex an LWW-Register on every replica, exactly as docs/replication.md
// specifies ("Higher HLC wins; same HLC ⇒ higher origin ID wins").
//
// Per item the usual guards apply: a write whose ts is strictly older than the
// key's tombstone, or strictly older than the key's stored vertexHLC, is
// skipped. Born-expired items follow the same dead-on-arrival handling as
// PutVerticesWithExpiration (the entry is removed, not stored) so the #698
// high-water optimisation is preserved; the watermark is still recorded so a
// later strictly-older write cannot resurrect the key.
func (c *GraphCache[S, T]) PutVerticesWithExpirationHLC(items []VertexItem[S, T], ts hlc.Timestamp) {
	if len(items) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, it := range items {
		if tombTs, ok := c.vertexTombstoneLocked(it.Key); ok && ts.Less(tombTs) {
			continue
		}
		if existing, ok := c.vertexHLC[it.Key]; ok && ts.Less(existing) {
			continue
		}
		c.putLocalVertexLocked(it.Key, it.Value, it.Expiration)
		if c.vertexHLC == nil {
			c.vertexHLC = make(map[S]hlc.Timestamp)
		}
		c.vertexHLC[it.Key] = ts
		if c.vertexTombstones != nil {
			delete(c.vertexTombstones, it.Key)
		}
	}
}

// PutEdgesWithExpirationHLC is the LWW-aware, single-lock batch sibling of
// PutEdgesWithExpiration used by the LOCAL write path when replication is
// enabled. Like PutVerticesWithExpirationHLC it stamps every edge with the
// originating mutation's ts so PutEdge resolves as an LWW-Register on
// (tail, head) across replicas rather than diverging when two origins write
// the same edge concurrently. Endpoint vertices are auto-created regardless of
// the per-edge LWW outcome (matching PutEdgeWithExpirationHLC) so traversal
// always sees the endpoints.
func (c *GraphCache[S, T]) PutEdgesWithExpirationHLC(items []EdgeItem[S], ts hlc.Timestamp) {
	if len(items) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, it := range items {
		if tombTs, ok := c.edgeTombstoneLocked(it.Tail, it.Head); ok && ts.Less(tombTs) {
			continue
		}
		c.ensureVertexLocked(it.Tail, it.Expiration)
		c.ensureVertexLocked(it.Head, it.Expiration)
		created, tailID, headID, applied := c.edges.putWithExpirationHLC(it.Tail, it.Head, it.Weight, it.Expiration, ts)
		if created {
			c.onEdgeAddedLocked(created, tailID, headID, it.Head)
		}
		if applied && c.edgeTombstones != nil {
			delete(c.edgeTombstones, EdgeKey[S]{Tail: it.Tail, Head: it.Head})
		}
	}
}

// AddEdgesWithExpirationContribHLC is the tombstone-aware, single-lock batch
// sibling of AddEdgesWithExpirationContrib used by the LOCAL write path when
// replication is enabled. Additive merge already converges via ContribID set
// semantics regardless of delivery order, so the ts is NOT compared against a
// per-edge write watermark; it is consulted ONLY against the edge tombstone so
// a contribution whose ts is strictly older than a delete is dropped on the
// origin exactly as it is on every peer. Items whose ts loses to the tombstone
// are skipped and counted as deduped=false (they applied nothing); items with
// a matching live ContribID are deduped as in the non-HLC variant. Returns the
// number of items that added no weight (tombstone-dropped or ContribID-deduped).
func (c *GraphCache[S, T]) AddEdgesWithExpirationContribHLC(items []EdgeItem[S], ts hlc.Timestamp) (deduped int) {
	if len(items) == 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, it := range items {
		if tombTs, ok := c.edgeTombstoneLocked(it.Tail, it.Head); ok && ts.Less(tombTs) {
			deduped++
			continue
		}
		c.ensureVertexLocked(it.Tail, it.Expiration)
		c.ensureVertexLocked(it.Head, it.Expiration)
		created, tailID, headID, applied := c.edges.addWithExpirationContrib(it.Tail, it.Head, it.Weight, it.Expiration, it.ContribID)
		if applied {
			c.onEdgeAddedLocked(created, tailID, headID, it.Head)
			if c.edgeTombstones != nil {
				delete(c.edgeTombstones, EdgeKey[S]{Tail: it.Tail, Head: it.Head})
			}
			continue
		}
		deduped++
		if created {
			c.onEdgeAddedLocked(created, tailID, headID, it.Head)
		}
	}
	return deduped
}
