package graphcache

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

// tombstoneEntry records a deletion's causal HLC and the wall-clock instant
// at which the tombstone itself expires and can be reaped. The HLC is the
// one stamped by the originating Delete RPC; the expiration is computed
// locally at apply time as now+TombstoneTTL and is therefore best-effort:
// peers reaping at slightly different times do not affect convergence
// because LWW is decided on the stored HLC, not on local expiration.
//
// Tombstones are intentionally kept outside the vertex / edge maps so
// reads never accidentally surface a deleted key as "present". The
// post-#183 invariant is: a key is visible iff it is in the live cache
// AND there is no live tombstone whose HLC is >= the live entry's HLC.
// Because Delete*HLC atomically removes the live entry under the same
// write lock that stamps the tombstone, point reads do not need to
// consult the tombstone store — only the replication apply path does.
type tombstoneEntry struct {
	ts         hlc.Timestamp
	expiration time.Time
}

// vertexTombstoneLocked returns the live tombstone HLC for key if one
// exists and has not yet expired. Caller must hold c.mu (read lock is
// sufficient; write lock if the caller also intends to mutate).
func (c *GraphCache[S, T]) vertexTombstoneLocked(key S) (hlc.Timestamp, bool) {
	if c.vertexTombstones == nil {
		return hlc.Timestamp{}, false
	}
	t, ok := c.vertexTombstones[key]
	if !ok {
		return hlc.Timestamp{}, false
	}
	if !t.expiration.IsZero() && !time.Now().Before(t.expiration) {
		return hlc.Timestamp{}, false
	}
	return t.ts, true
}

// edgeTombstoneLocked is the edge sibling of vertexTombstoneLocked. The
// tombstone is keyed on the full (tail, head) pair so deleting one
// direction of a parallel pair never accidentally hides the other.
func (c *GraphCache[S, T]) edgeTombstoneLocked(tail, head S) (hlc.Timestamp, bool) {
	if c.edgeTombstones == nil {
		return hlc.Timestamp{}, false
	}
	t, ok := c.edgeTombstones[EdgeKey[S]{Tail: tail, Head: head}]
	if !ok {
		return hlc.Timestamp{}, false
	}
	if !t.expiration.IsZero() && !time.Now().Before(t.expiration) {
		return hlc.Timestamp{}, false
	}
	return t.ts, true
}

// setVertexTombstoneLocked stamps a tombstone keyed on key. A newer
// (>=) incoming HLC supersedes any existing tombstone; an older HLC is
// ignored so a late replay never shortens the effective resurrection
// window.
func (c *GraphCache[S, T]) setVertexTombstoneLocked(key S, ts hlc.Timestamp, expiration time.Time) {
	if c.vertexTombstones == nil {
		c.vertexTombstones = make(map[S]tombstoneEntry)
	}
	if existing, ok := c.vertexTombstones[key]; ok && ts.Less(existing.ts) {
		return
	}
	c.vertexTombstones[key] = tombstoneEntry{ts: ts, expiration: expiration}
}

// setEdgeTombstoneLocked is the edge sibling of setVertexTombstoneLocked.
func (c *GraphCache[S, T]) setEdgeTombstoneLocked(tail, head S, ts hlc.Timestamp, expiration time.Time) {
	if c.edgeTombstones == nil {
		c.edgeTombstones = make(map[EdgeKey[S]]tombstoneEntry)
	}
	k := EdgeKey[S]{Tail: tail, Head: head}
	if existing, ok := c.edgeTombstones[k]; ok && ts.Less(existing.ts) {
		return
	}
	c.edgeTombstones[k] = tombstoneEntry{ts: ts, expiration: expiration}
}

func (c *GraphCache[S, T]) vertexDeleteWriteAllowedLocked(key S, ts hlc.Timestamp) bool {
	if tombstone, ok := c.vertexTombstoneLocked(key); ok && ts.Less(tombstone) {
		return false
	}
	if barrier, ok := c.vertexCausalBarriers[key]; ok && ts.Less(barrier) {
		return false
	}
	if live, ok := c.vertexHLC[key]; ok && ts.Less(live) {
		return false
	}
	return true
}

func (c *GraphCache[S, T]) edgeDeleteWriteAllowedLocked(tail, head S, ts hlc.Timestamp) bool {
	if tombstone, ok := c.edgeTombstoneLocked(tail, head); ok && ts.Less(tombstone) {
		return false
	}
	if barrier, ok := c.edgeCausalBarriers[EdgeKey[S]{Tail: tail, Head: head}]; ok && ts.Less(barrier) {
		return false
	}
	if live, ok := c.edges.lastPutHLC(tail, head); ok && ts.Less(live) {
		return false
	}
	return true
}

// DeleteVertexHLC removes the vertex and stamps a tombstone so a late
// Put*/Add* with an HLC strictly older than ts is rejected for the
// duration of expiration. Returns whether the vertex was present at
// call time. Edges of the vertex are NOT tombstoned by this call — they
// are still GC'd via the dangling sweep when their endpoints disappear,
// but a late AddEdge that re-creates an endpoint can still reappear.
// Callers that need full subgraph fencing should pair this with
// DeleteEdgesHLC for the relevant (tail, head) pairs.
func (c *GraphCache[S, T]) DeleteVertexHLC(key S, ts hlc.Timestamp, expiration time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.searchIndex != nil {
		c.searchCommitMu.Lock()
		defer c.searchCommitMu.Unlock()
	}
	if !c.vertexDeleteWriteAllowedLocked(key, ts) {
		return false
	}
	existed := c.vertices.Delete(key)
	c.setVertexTombstoneLocked(key, ts, expiration)
	if barrier, ok := c.vertexCausalBarriers[key]; ok && !ts.Less(barrier) {
		c.clearVertexCausalBarrierLocked(key)
	}
	if live, ok := c.vertexHLC[key]; ok && !ts.Less(live) {
		delete(c.vertexHLC, key)
	}
	c.rebuildIncompleteSearchLocked()
	return existed
}

// DeleteVerticesHLC is the batch sibling of DeleteVertexHLC. Every key
// receives a tombstone, including ones not present in the live cache —
// this is intentional so a Delete-before-Add race is still resolved by
// LWW once the (out-of-order) Add arrives.
func (c *GraphCache[S, T]) DeleteVerticesHLC(keys []S, ts hlc.Timestamp, expiration time.Time) int {
	if len(keys) == 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.searchIndex != nil {
		c.searchCommitMu.Lock()
		defer c.searchCommitMu.Unlock()
	}
	// DeleteMany batches the vertex-index maintenance into one pass (#738);
	// it returns only the keys that were present, which is exactly the count
	// we report. Tombstones still go on EVERY key (including absent ones) so
	// a Delete-before-Add race is resolved by LWW once the Add arrives.
	accepted := make([]S, 0, len(keys))
	for _, k := range keys {
		if c.vertexDeleteWriteAllowedLocked(k, ts) {
			accepted = append(accepted, k)
		}
	}
	n := len(c.vertices.DeleteMany(accepted))
	for _, k := range accepted {
		c.setVertexTombstoneLocked(k, ts, expiration)
		if barrier, ok := c.vertexCausalBarriers[k]; ok && !ts.Less(barrier) {
			c.clearVertexCausalBarrierLocked(k)
		}
		if live, ok := c.vertexHLC[k]; ok && !ts.Less(live) {
			delete(c.vertexHLC, k)
		}
	}
	c.rebuildIncompleteSearchLocked()
	return n
}

// DeleteEdgeHLC removes the (tail, head) edge and stamps a tombstone.
// See DeleteVertexHLC for the late-replay rationale.
func (c *GraphCache[S, T]) DeleteEdgeHLC(tail, head S, ts hlc.Timestamp, expiration time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.edgeDeleteWriteAllowedLocked(tail, head, ts) {
		return false
	}
	deleted := c.deleteEdgeLocked(tail, head)
	c.setEdgeTombstoneLocked(tail, head, ts, expiration)
	key := EdgeKey[S]{Tail: tail, Head: head}
	if barrier, ok := c.edgeCausalBarriers[key]; ok && !ts.Less(barrier) {
		c.clearEdgeCausalBarrierLocked(tail, head)
	}
	return deleted
}

// DeleteEdgesHLC is the batch sibling of DeleteEdgeHLC.
func (c *GraphCache[S, T]) DeleteEdgesHLC(keys []EdgeKey[S], ts hlc.Timestamp, expiration time.Time) int {
	if len(keys) == 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, k := range keys {
		if !c.edgeDeleteWriteAllowedLocked(k.Tail, k.Head, ts) {
			continue
		}
		if c.deleteEdgeLocked(k.Tail, k.Head) {
			n++
		}
		c.setEdgeTombstoneLocked(k.Tail, k.Head, ts, expiration)
		if barrier, ok := c.edgeCausalBarriers[k]; ok && !ts.Less(barrier) {
			c.clearEdgeCausalBarrierLocked(k.Tail, k.Head)
		}
	}
	return n
}

// DeleteByPrefixHLC is the tombstone-aware sibling of DeleteByPrefix:
// every vertex whose projected key matches prefix receives a tombstone
// stamped at ts/expiration. Returns the number of vertices actually
// deleted (matching DeleteByPrefix). limit==0 means unlimited.
func (c *GraphCache[S, T]) DeleteByPrefixHLC(ctx context.Context, prefix string, limit uint32, ts hlc.Timestamp, expiration time.Time) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.prefixIndex == nil {
		return 0, nil
	}
	var victims []S
	c.prefixIndex.walkPrefix(prefix, func(projected string) bool {
		if err := ctx.Err(); err != nil {
			return false
		}
		if limit > 0 && uint32(len(victims)) >= limit {
			return false
		}
		key, ok := c.resolveProjected(projected)
		if !ok {
			return true
		}
		victims = append(victims, key)
		return true
	})
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if c.searchIndex != nil && len(victims) > 0 {
		c.searchCommitMu.Lock()
		defer c.searchCommitMu.Unlock()
	}
	// victims are all live (resolveProjected confirms via the vertex cache),
	// so DeleteMany removes them all; batch it into one index-maintenance pass
	// (#738) and tombstone each victim.
	accepted := victims[:0]
	for _, k := range victims {
		if c.vertexDeleteWriteAllowedLocked(k, ts) {
			accepted = append(accepted, k)
		}
	}
	n := len(c.vertices.DeleteMany(accepted))
	for _, k := range accepted {
		c.setVertexTombstoneLocked(k, ts, expiration)
		if barrier, ok := c.vertexCausalBarriers[k]; ok && !ts.Less(barrier) {
			c.clearVertexCausalBarrierLocked(k)
		}
		if live, ok := c.vertexHLC[k]; ok && !ts.Less(live) {
			delete(c.vertexHLC, k)
		}
	}
	c.rebuildIncompleteSearchLocked()
	return n, nil
}

// sweepExpiredTombstonesLocked drops tombstones whose expiration has
// passed. Invoked from flush() so reaping rides on the existing GC tick
// rather than introducing a separate timer. Caller must hold c.mu.Lock.
func (c *GraphCache[S, T]) sweepExpiredTombstonesLocked(now time.Time) {
	for k, t := range c.vertexTombstones {
		if !t.expiration.IsZero() && !now.Before(t.expiration) {
			delete(c.vertexTombstones, k)
		}
	}
	for k, t := range c.edgeTombstones {
		if !t.expiration.IsZero() && !now.Before(t.expiration) {
			delete(c.edgeTombstones, k)
		}
	}
}
