package graphcache

import (
	"time"

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
		c.clearEdgeTombstoneLocked(tail, head)
	}
	return applied
}

// PutVertexWithExpirationHLC is the LWW-aware sibling of
// PutVertexWithExpiration used by the replication apply path. When the stored
// HLC for key is strictly newer than ts the call is a no-op and returns
// applied=false. A zero ts always applies and is recorded as the zero watermark
// (nothing can be strictly older than it). Local writers continue to use
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
	if !c.vertexWriteAllowedLocked(key, ts) {
		return false
	}
	if c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
		prepErr = search.ErrIndexIncomplete
	}
	if c.searchIndex != nil && prepErr == nil {
		prepErr = c.searchIndex.ValidateManyPrepared([]search.PreparedItem[S]{{ID: key, Prepared: prepared, Expiration: expiration}})
	}
	if prepErr != nil && c.searchIndex != nil {
		c.searchIndex.MarkIncomplete()
		c.upsertVertexStorageLocked(key, value, expiration)
	} else if c.searchIndex != nil {
		c.searchIndex.IndexManyPreparedValidated([]search.PreparedItem[S]{{ID: key, Prepared: prepared, Expiration: expiration}})
		c.upsertVertexStorageLocked(key, value, expiration)
	} else {
		c.putVertexLocked(key, value, expiration)
	}
	c.recordVertexHLCLocked(key, ts)
	c.clearVertexTombstoneLocked(key)
	return true
}

// PutEdgeWithExpirationHLC is the LWW-aware sibling of PutEdgeWithExpiration
// used by the replication apply path. Returns applied=false when the stored
// edge's last accepted HLC is strictly newer than ts. Endpoint vertices are
// auto-created (matching PutEdgeWithExpiration) regardless of the LWW outcome,
// unless an edge tombstone rejects the write before storage is touched.
func (c *GraphCache[S, T]) PutEdgeWithExpirationHLC(tail, head S, w float32, expiration time.Time, ts hlc.Timestamp) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.edgeWriteAllowedLocked(tail, head, ts) {
		return false
	}
	applied := c.putEdgeHLCLocked(tail, head, w, expiration, ts)
	if applied {
		c.clearEdgeTombstoneLocked(tail, head)
	}
	return applied
}

func (c *GraphCache[S, T]) vertexWriteAllowedLocked(key S, ts hlc.Timestamp) bool {
	if tombTs, ok := c.vertexTombstoneLocked(key); ok && ts.Less(tombTs) {
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

func (c *GraphCache[S, T]) clearVertexTombstoneLocked(key S) {
	if c.vertexTombstones != nil {
		delete(c.vertexTombstones, key)
	}
}

func (c *GraphCache[S, T]) edgeWriteAllowedLocked(tail, head S, ts hlc.Timestamp) bool {
	if tombTs, ok := c.edgeTombstoneLocked(tail, head); ok && ts.Less(tombTs) {
		return false
	}
	return true
}

func (c *GraphCache[S, T]) clearEdgeTombstoneLocked(tail, head S) {
	if c.edgeTombstones != nil {
		delete(c.edgeTombstones, EdgeKey[S]{Tail: tail, Head: head})
	}
}
