package graphcache

import (
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

// AddEdgeWithExpirationContribHLC is the tombstone-aware sibling of
// AddEdgeWithExpirationContrib used by the replication apply path (#183).
// When a live edge tombstone exists for (tail, head) whose HLC is strictly
// newer than ts the contribution is dropped and false is returned, preventing
// a late Add* from resurrecting a freshly-deleted edge. Otherwise behaviour
// matches AddEdgeWithExpirationContrib, including ContribID-based dedup. A
// successful apply clears any existing tombstone for the edge.
func (c *GraphCache[S, T]) AddEdgeWithExpirationContribHLC(tail, head S, w float32, expiration time.Time, contribID ContribID, ts hlc.Timestamp) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.edgeWriteAllowedLocked(tail, head, ts) {
		return false
	}
	applied, _ := c.addEdgeContribLocked(tail, head, w, expiration, contribID, time.Now())
	if applied {
		// Add does not carry a replacement Put watermark in the edge bucket.
		// Keep any accepted-expired Put barrier so a delayed Put older than
		// that floor still cannot reset the newer additive state.
		c.clearEdgeTombstoneLocked(tail, head)
	}
	return applied
}

// PutVertexWithExpirationHLC is the LWW-aware sibling of
// PutVertexWithExpiration used by the replication apply path. When the stored
// HLC for key is strictly newer than ts the call is a no-op and returns
// applied=false. A zero ts always applies and may mark live state as having no
// causal floor, but it is never retained as a barrier after the payload is
// absent. Local writers continue to use
// PutVertexWithExpiration; this helper is intentionally narrow to the
// replicated path so non-replicated workloads pay nothing.
func (c *GraphCache[S, T]) PutVertexWithExpirationHLC(key S, value T, expiration time.Time, ts hlc.Timestamp) bool {
	var prepared search.PreparedDocument
	var prepErr error
	if c.searchIndex != nil {
		prepared, _, prepErr = c.searchIndex.Prepare(c.searchExtract(key, value))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.searchIndex != nil {
		c.searchCommitMu.Lock()
		defer c.searchCommitMu.Unlock()
	}
	if !c.vertexWriteAllowedLocked(key, ts) {
		return false
	}
	applicationTime := c.applicationTime()
	live := cache.IsLiveAt(expiration, applicationTime)
	if c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
		prepErr = search.ErrIndexIncomplete
	}
	// Preparation happens outside the graph lock and deliberately analyzes the
	// value even when a preliminary wall-clock sample would call it expired.
	// The injectable/final application clock may move backwards before commit;
	// retaining the prepared document prevents a live value with an empty search
	// projection. Conversely an error is irrelevant when the final sample is
	// expired because the only index mutation is a deletion marker.
	if !live {
		prepared = search.PreparedDocument{}
		if prepErr != search.ErrIndexIncomplete {
			prepErr = nil
		}
	}
	if c.searchIndex != nil && prepErr == nil {
		prepErr = c.searchIndex.ValidateManyPreparedAt([]search.PreparedItem[S]{{ID: key, Prepared: prepared, Expiration: expiration}}, applicationTime)
	}
	if prepErr != nil && c.searchIndex != nil {
		c.searchIndex.MarkIncomplete()
	} else if c.searchIndex != nil {
		c.searchIndex.IndexManyPreparedValidatedAt([]search.PreparedItem[S]{{ID: key, Prepared: prepared, Expiration: expiration}}, applicationTime)
	}
	stored := c.putLocalVertexLockedAtMode(key, value, expiration, applicationTime, false)
	if stored {
		c.recordVertexHLCLocked(key, ts)
		c.clearVertexCausalBarrierLocked(key)
	} else {
		c.applyVertexCausalBarrierLocked(key, ts)
	}
	c.clearVertexTombstoneLocked(key)
	return true
}

// ApplyVertexCausalBarrierHLC replays an explicit replication Snapshot causal
// barrier. It removes state only when ts wins the same LWW/tombstone checks as
// a Put; a strictly-newer local value remains untouched. A successful apply
// never creates a vertex and retains the floor independently of TTL GC.
func (c *GraphCache[S, T]) ApplyVertexCausalBarrierHLC(key S, ts hlc.Timestamp) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.searchIndex != nil {
		c.searchCommitMu.Lock()
		defer c.searchCommitMu.Unlock()
	}
	if !c.vertexWriteAllowedLocked(key, ts) {
		return false
	}
	c.applyVertexCausalBarrierLocked(key, ts)
	c.clearVertexTombstoneLocked(key)
	return true
}

func (c *GraphCache[S, T]) applyVertexCausalBarrierLocked(key S, ts hlc.Timestamp) {
	c.vertices.Delete(key)
	c.recordVertexCausalBarrierLocked(key, ts)
	if c.vertexHLC != nil {
		delete(c.vertexHLC, key)
	}
}

// PutEdgeWithExpirationHLC is the LWW-aware sibling of PutEdgeWithExpiration
// used by the replication apply path. Returns applied=false when the stored
// edge's last accepted HLC is strictly newer than ts. Endpoint vertices are
// auto-created (matching PutEdgeWithExpiration) regardless of the LWW outcome,
// unless an edge tombstone rejects the write before storage is touched.
func (c *GraphCache[S, T]) PutEdgeWithExpirationHLC(tail, head S, w float32, expiration time.Time, ts hlc.Timestamp) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	live := cache.IsLiveAt(expiration, c.applicationTime())
	if !live {
		if !c.edgePutWriteAllowedLocked(tail, head, ts) {
			return false
		}
		c.applyEdgeCausalBarrierLocked(tail, head, ts)
		c.clearEdgeTombstoneLocked(tail, head)
		return true
	}
	// Keep the normal live hot path on its historical single bucket lookup:
	// edgeWriteAllowedLocked checks tombstone/barrier maps, then the weight's
	// putWithExpirationHLC performs the current-bucket LWW comparison while it
	// applies. Only the delete-like expired branch above needs a separate read.
	if !c.edgeWriteAllowedLocked(tail, head, ts) {
		return false
	}
	applied := c.putEdgeHLCLocked(tail, head, w, expiration, ts)
	if applied {
		c.clearEdgeCausalBarrierLocked(tail, head)
		c.clearEdgeTombstoneLocked(tail, head)
	}
	return applied
}

// ApplyEdgeCausalBarrierHLC is the explicit Snapshot replay seam for an
// accepted-expired PutEdge floor. It never creates endpoint vertices or an
// edge bucket and rejects a floor older than current live/retained state.
func (c *GraphCache[S, T]) ApplyEdgeCausalBarrierHLC(tail, head S, ts hlc.Timestamp) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.edgePutWriteAllowedLocked(tail, head, ts) {
		return false
	}
	c.applyEdgeCausalBarrierLocked(tail, head, ts)
	c.clearEdgeTombstoneLocked(tail, head)
	return true
}

func (c *GraphCache[S, T]) applyEdgeCausalBarrierLocked(tail, head S, ts hlc.Timestamp) {
	c.deleteEdgeLocked(tail, head)
	c.recordEdgeCausalBarrierLocked(tail, head, ts)
}

func (c *GraphCache[S, T]) vertexWriteAllowedLocked(key S, ts hlc.Timestamp) bool {
	if tombTs, ok := c.vertexTombstoneLocked(key); ok && ts.Less(tombTs) {
		return false
	}
	if barrier, ok := c.vertexCausalBarriers[key]; ok && ts.Less(barrier) {
		return false
	}
	if existing, ok := c.vertexHLC[key]; ok && ts.Less(existing) {
		return false
	}
	return true
}

func (c *GraphCache[S, T]) recordVertexHLCLocked(key S, ts hlc.Timestamp) {
	if c.vertexHLC == nil {
		c.vertexHLC = make(map[S]hlc.Timestamp)
	}
	c.vertexHLC[key] = ts
}

// recordVertexCausalBarrierLocked retains the greatest accepted dead-on-arrival
// Put HLC for key. The barrier is intentionally independent of vertexHLC: the
// latter tracks live replicated values and is swept with the live set, while a
// delete-like overwrite must remain authoritative after GC.
func (c *GraphCache[S, T]) recordVertexCausalBarrierLocked(key S, ts hlc.Timestamp) {
	if ts == (hlc.Timestamp{}) {
		return
	}
	if c.vertexCausalBarriers == nil {
		c.vertexCausalBarriers = make(map[S]hlc.Timestamp)
	}
	if existing, ok := c.vertexCausalBarriers[key]; ok && ts.Less(existing) {
		return
	}
	c.vertexCausalBarriers[key] = ts
}

func (c *GraphCache[S, T]) clearVertexCausalBarrierLocked(key S) {
	if c.vertexCausalBarriers != nil {
		delete(c.vertexCausalBarriers, key)
		if len(c.vertexCausalBarriers) == 0 {
			c.vertexCausalBarriers = nil
		}
	}
}

func (c *GraphCache[S, T]) clearVertexTombstoneLocked(key S) {
	if c.vertexTombstones != nil {
		delete(c.vertexTombstones, key)
	}
}

func (c *GraphCache[S, T]) edgeWriteAllowedLocked(tail, head S, ts hlc.Timestamp) bool {
	if tombTs, ok := c.edgeTombstoneLocked(tail, head); ok && ts.Less(tombTs) {
		return false
	}
	if barrier, ok := c.edgeCausalBarriers[EdgeKey[S]{Tail: tail, Head: head}]; ok && ts.Less(barrier) {
		return false
	}
	return true
}

// edgePutWriteAllowedLocked additionally compares the HLC carried by a live
// edge bucket. The ordinary live Put path performs that comparison inside the
// weight lock; the accepted-expired path deletes the bucket instead, so it must
// validate the same floor before removal. Caller must hold c.mu.
func (c *GraphCache[S, T]) edgePutWriteAllowedLocked(tail, head S, ts hlc.Timestamp) bool {
	if !c.edgeWriteAllowedLocked(tail, head, ts) {
		return false
	}
	if existing, ok := c.edges.lastPutHLC(tail, head); ok && ts.Less(existing) {
		return false
	}
	return true
}

func (c *GraphCache[S, T]) recordEdgeCausalBarrierLocked(tail, head S, ts hlc.Timestamp) {
	if ts == (hlc.Timestamp{}) {
		return
	}
	if c.edgeCausalBarriers == nil {
		c.edgeCausalBarriers = make(map[EdgeKey[S]]hlc.Timestamp)
	}
	key := EdgeKey[S]{Tail: tail, Head: head}
	if existing, ok := c.edgeCausalBarriers[key]; ok && ts.Less(existing) {
		return
	}
	c.edgeCausalBarriers[key] = ts
}

func (c *GraphCache[S, T]) clearEdgeCausalBarrierLocked(tail, head S) {
	if c.edgeCausalBarriers != nil {
		delete(c.edgeCausalBarriers, EdgeKey[S]{Tail: tail, Head: head})
		if len(c.edgeCausalBarriers) == 0 {
			c.edgeCausalBarriers = nil
		}
	}
}

func (c *GraphCache[S, T]) clearEdgeTombstoneLocked(tail, head S) {
	if c.edgeTombstones != nil {
		delete(c.edgeTombstones, EdgeKey[S]{Tail: tail, Head: head})
	}
}
