package graphcache

import (
	"context"
	"errors"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

// tombstoneEntry records a deletion's causal HLC and the wall-clock instant
// at which the tombstone itself expires and can be reaped. The HLC is the
// one stamped by the originating Delete RPC. Local Deletes compute expiration
// as now+TombstoneTTL and carry that same absolute deadline in the sequenced
// Mutation. Subscribe and Snapshot replay preserve the origin's expiration
// instead of renewing the window. Peers may still reap at slightly different
// times because their wall clocks differ. LWW uses the HLC while a floor lives.
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
	return c.edgeTombstoneLockedAt(tail, head, time.Now())
}

func (c *GraphCache[S, T]) edgeTombstoneLockedAt(tail, head S, now time.Time) (hlc.Timestamp, bool) {
	if c.edgeTombstones == nil {
		return hlc.Timestamp{}, false
	}
	t, ok := c.edgeTombstones[EdgeKey[S]{Tail: tail, Head: head}]
	if !ok {
		return hlc.Timestamp{}, false
	}
	if !t.expiration.IsZero() && !now.Before(t.expiration) {
		return hlc.Timestamp{}, false
	}
	return t.ts, true
}

// setVertexTombstoneLocked stamps a tombstone keyed on key. A newer
// incoming HLC supersedes any older tombstone. Equal-HLC replay keeps the
// earlier absolute expiration so repeated Snapshot cannot renew D4.
func (c *GraphCache[S, T]) setVertexTombstoneLocked(key S, ts hlc.Timestamp, expiration time.Time) {
	// The zero HLC sentinel carries no resurrection floor. It is used by
	// backup/legacy restore paths and must never allocate retained metadata or
	// bypass a non-zero causal budget through the checked Delete surface.
	if ts == (hlc.Timestamp{}) {
		return
	}
	if c.vertexTombstones == nil {
		c.vertexTombstones = make(map[S]tombstoneEntry)
	}
	if existing, ok := c.vertexTombstones[key]; ok {
		if ts.Less(existing.ts) {
			return
		}
		expiration = clampTombstoneExpirationOnReplay(existing, ts, expiration)
	}
	c.vertexTombstones[key] = tombstoneEntry{ts: ts, expiration: expiration}
	c.trackVertexTombstoneDeadlineLocked(key, expiration)
	c.ensureVertexCausalUsageLocked(key)
}

// setEdgeTombstoneLocked is the edge sibling of setVertexTombstoneLocked.
func (c *GraphCache[S, T]) setEdgeTombstoneLocked(tail, head S, ts hlc.Timestamp, expiration time.Time) {
	if ts == (hlc.Timestamp{}) {
		return
	}
	if c.edgeTombstones == nil {
		c.edgeTombstones = make(map[EdgeKey[S]]tombstoneEntry)
	}
	k := EdgeKey[S]{Tail: tail, Head: head}
	if existing, ok := c.edgeTombstones[k]; ok {
		if ts.Less(existing.ts) {
			return
		}
		expiration = clampTombstoneExpirationOnReplay(existing, ts, expiration)
	}
	c.edgeTombstones[k] = tombstoneEntry{ts: ts, expiration: expiration}
	c.trackEdgeTombstoneDeadlineLocked(k, expiration)
	c.ensureEdgeCausalUsageLocked(k)
}

func clampTombstoneExpirationOnReplay(
	existing tombstoneEntry, ts hlc.Timestamp, expiration time.Time,
) time.Time {
	if ts == existing.ts && !existing.expiration.IsZero() && existing.expiration.Before(expiration) {
		return existing.expiration
	}
	return expiration
}

// ApplySnapshotVertexTombstoneHLC restores a Delete floor with its original
// D4 deadline. Expired frames still count toward Snapshot framing, but must
// not recreate a floor or extend its retention on the receiver. Like
// Subscribe-applied Deletes, it is exempt from local causal admission limits.
func (c *GraphCache[S, T]) ApplySnapshotVertexTombstoneHLC(key S, ts hlc.Timestamp, expiration time.Time) {
	if !time.Now().Before(expiration) {
		return
	}
	_, _, _, _ = c.deleteVerticesHLC([]S{key}, ts, expiration, false, true, false)
}

// ApplySnapshotEdgeTombstoneHLC is the edge counterpart. Like other remote
// replication apply paths, snapshot replay is exempt from local admission
// limits so replicas cannot silently diverge under different local budgets.
func (c *GraphCache[S, T]) ApplySnapshotEdgeTombstoneHLC(tail, head S, ts hlc.Timestamp, expiration time.Time) {
	if !time.Now().Before(expiration) {
		return
	}
	c.DeleteEdgesHLC([]EdgeKey[S]{{Tail: tail, Head: head}}, ts, expiration)
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
	return c.edgeDeleteWriteAllowedLockedAt(tail, head, ts, time.Now())
}

func (c *GraphCache[S, T]) edgeDeleteWriteAllowedLockedAt(tail, head S, ts hlc.Timestamp, now time.Time) bool {
	if tombstone, ok := c.edgeTombstoneLockedAt(tail, head, now); ok && ts.Less(tombstone) {
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
		c.clearVertexHLCLocked(key)
	}
	c.rebuildIncompleteSearchLocked()
	return existed
}

// DeleteVerticesHLC is the batch sibling of DeleteVertexHLC. Every key
// receives a tombstone, including ones not present in the live cache —
// this is intentional so a Delete-before-Add race is still resolved by
// LWW once the (out-of-order) Add arrives.
func (c *GraphCache[S, T]) DeleteVerticesHLC(keys []S, ts hlc.Timestamp, expiration time.Time) int {
	n, _, _, _ := c.deleteVerticesHLC(keys, ts, expiration, false, false, false)
	return n
}

// DeleteVerticesHLCChecked is the locally-originated sibling of
// DeleteVerticesHLC. A causal-metadata budget overflow leaves graph and
// causal state unchanged.
func (c *GraphCache[S, T]) DeleteVerticesHLCChecked(keys []S, ts hlc.Timestamp, expiration time.Time) (int, error) {
	n, _, _, err := c.deleteVerticesHLC(keys, ts, expiration, true, false, false)
	return n, err
}

// DeleteVerticesHLCOutcomesChecked is the exact local-origin batch result.
// A capacity error returns no outcomes and leaves graph and causal state as-is.
func (c *GraphCache[S, T]) DeleteVerticesHLCOutcomesChecked(keys []S, ts hlc.Timestamp, expiration time.Time) ([]bool, error) {
	_, outcomes, _, err := c.deleteVerticesHLC(keys, ts, expiration, true, false, true)
	return outcomes, err
}

// DeleteVerticesHLCDecisions returns both the public Existed observation and
// the causally accepted request indexes without enforcing the local-origin
// metadata budget. Replication uses this path because an already-committed
// remote mutation must converge even when it takes the receiver over its
// local admission limit.
func (c *GraphCache[S, T]) DeleteVerticesHLCDecisions(keys []S, ts hlc.Timestamp, expiration time.Time) (existed []bool, acceptedIndexes []int) {
	_, existed, acceptedIndexes, _ = c.deleteVerticesHLC(keys, ts, expiration, false, false, true)
	return existed, acceptedIndexes
}

// DeleteVerticesHLCDecisionsChecked returns both the public Existed observation
// and the causally accepted request indexes from one graph lock. An accepted
// absent key has Existed=false but still appears in acceptedIndexes; a rejected
// key also has Existed=false but is absent from acceptedIndexes. Duplicate
// request positions remain distinct and ordered.
func (c *GraphCache[S, T]) DeleteVerticesHLCDecisionsChecked(keys []S, ts hlc.Timestamp, expiration time.Time) (existed []bool, acceptedIndexes []int, err error) {
	_, existed, acceptedIndexes, err = c.deleteVerticesHLC(keys, ts, expiration, true, false, true)
	return existed, acceptedIndexes, err
}

func (c *GraphCache[S, T]) deleteVerticesHLC(keys []S, ts hlc.Timestamp, expiration time.Time, strict, deferSearchRecovery, withOutcomes bool) (int, []bool, []int, error) {
	if len(keys) == 0 {
		if withOutcomes {
			return 0, []bool{}, []int{}, nil
		}
		return 0, nil, nil, nil
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
	var acceptedIndexes []int
	if withOutcomes {
		acceptedIndexes = make([]int, 0, len(keys))
	}
	for i, k := range keys {
		if c.vertexDeleteWriteAllowedLocked(k, ts) {
			accepted = append(accepted, k)
			if withOutcomes {
				acceptedIndexes = append(acceptedIndexes, i)
			}
		}
	}
	if strict && ts != (hlc.Timestamp{}) {
		if err := c.checkVertexCausalCapacityLocked(accepted); err != nil {
			return 0, nil, nil, err
		}
	}
	var n int
	var outcomes []bool
	if withOutcomes {
		outcomes = make([]bool, len(keys))
		removed, acceptedOutcomes := c.vertices.DeleteManyWithOutcomes(accepted)
		n = len(removed)
		for i, exists := range acceptedOutcomes {
			outcomes[acceptedIndexes[i]] = exists
		}
	} else {
		n = len(c.vertices.DeleteMany(accepted))
	}
	for _, k := range accepted {
		c.setVertexTombstoneLocked(k, ts, expiration)
		if barrier, ok := c.vertexCausalBarriers[k]; ok && !ts.Less(barrier) {
			c.clearVertexCausalBarrierLocked(k)
		}
		if live, ok := c.vertexHLC[k]; ok && !ts.Less(live) {
			c.clearVertexHLCLocked(k)
		}
	}
	if !deferSearchRecovery {
		c.rebuildIncompleteSearchLocked()
	}
	return n, outcomes, acceptedIndexes, nil
}

// DeleteEdgeHLC removes the (tail, head) edge and stamps a tombstone.
// See DeleteVertexHLC for the late-replay rationale.
func (c *GraphCache[S, T]) DeleteEdgeHLC(tail, head S, ts hlc.Timestamp, expiration time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.edgeDeleteWriteAllowedLocked(tail, head, ts) {
		return false
	}
	existed := c.edges.bucket(tail, head) != nil
	c.setEdgeTombstoneLocked(tail, head, ts, expiration)
	if !c.edges.resetDeleteHLC(tail, head, ts) {
		c.deleteEdgeLocked(tail, head)
	}
	key := EdgeKey[S]{Tail: tail, Head: head}
	if barrier, ok := c.edgeCausalBarriers[key]; ok && !ts.Less(barrier) {
		c.clearEdgeCausalBarrierLocked(tail, head)
	}
	return existed
}

// DeleteEdgesHLC is the batch sibling of DeleteEdgeHLC.
func (c *GraphCache[S, T]) DeleteEdgesHLC(keys []EdgeKey[S], ts hlc.Timestamp, expiration time.Time) int {
	n, _, _, _ := c.deleteEdgesHLC(keys, ts, expiration, false, false)
	return n
}

// DeleteEdgesHLCChecked is the locally-originated sibling of DeleteEdgesHLC.
// It reserves every newly-retained edge identity before deleting any edge.
func (c *GraphCache[S, T]) DeleteEdgesHLCChecked(keys []EdgeKey[S], ts hlc.Timestamp, expiration time.Time) (int, error) {
	n, _, _, err := c.deleteEdgesHLC(keys, ts, expiration, true, false)
	return n, err
}

// DeleteEdgesHLCOutcomesChecked returns one request-index-aligned observation
// without changing the legacy HLC/tombstone behavior or aggregate count.
func (c *GraphCache[S, T]) DeleteEdgesHLCOutcomesChecked(keys []EdgeKey[S], ts hlc.Timestamp, expiration time.Time) ([]bool, error) {
	_, outcomes, _, err := c.deleteEdgesHLC(keys, ts, expiration, true, true)
	return outcomes, err
}

// DeleteEdgesHLCDecisions is the replication-safe, non-strict sibling of
// DeleteEdgesHLCDecisionsChecked.
func (c *GraphCache[S, T]) DeleteEdgesHLCDecisions(keys []EdgeKey[S], ts hlc.Timestamp, expiration time.Time) (existed []bool, acceptedIndexes []int) {
	_, existed, acceptedIndexes, _ = c.deleteEdgesHLC(keys, ts, expiration, false, true)
	return existed, acceptedIndexes
}

// DeleteEdgesHLCDecisionsChecked is the edge sibling of
// DeleteVerticesHLCDecisionsChecked. Accepted indexes preserve absent and
// duplicate request positions, independently of Existed.
func (c *GraphCache[S, T]) DeleteEdgesHLCDecisionsChecked(keys []EdgeKey[S], ts hlc.Timestamp, expiration time.Time) (existed []bool, acceptedIndexes []int, err error) {
	_, existed, acceptedIndexes, err = c.deleteEdgesHLC(keys, ts, expiration, true, true)
	return existed, acceptedIndexes, err
}

func (c *GraphCache[S, T]) deleteEdgesHLC(keys []EdgeKey[S], ts hlc.Timestamp, expiration time.Time, strict, withOutcomes bool) (int, []bool, []int, error) {
	if len(keys) == 0 {
		if withOutcomes {
			return 0, []bool{}, []int{}, nil
		}
		return 0, nil, nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	accepted := make([]EdgeKey[S], 0, len(keys))
	var acceptedIndexes []int
	if withOutcomes {
		acceptedIndexes = make([]int, 0, len(keys))
	}
	for i, k := range keys {
		if c.edgeDeleteWriteAllowedLocked(k.Tail, k.Head, ts) {
			accepted = append(accepted, k)
			if withOutcomes {
				acceptedIndexes = append(acceptedIndexes, i)
			}
		}
	}
	if strict && ts != (hlc.Timestamp{}) {
		if err := c.checkEdgeCausalCapacityLocked(accepted); err != nil {
			return 0, nil, nil, err
		}
	}
	n := 0
	var outcomes []bool
	if withOutcomes {
		outcomes = make([]bool, len(keys))
	}
	for i, k := range accepted {
		existed := c.edges.bucket(k.Tail, k.Head) != nil
		c.setEdgeTombstoneLocked(k.Tail, k.Head, ts, expiration)
		if !c.edges.resetDeleteHLC(k.Tail, k.Head, ts) {
			c.deleteEdgeLocked(k.Tail, k.Head)
		}
		if existed {
			n++
		}
		if withOutcomes {
			outcomes[acceptedIndexes[i]] = existed
		}
		if barrier, ok := c.edgeCausalBarriers[k]; ok && !ts.Less(barrier) {
			c.clearEdgeCausalBarrierLocked(k.Tail, k.Head)
		}
	}
	return n, outcomes, acceptedIndexes, nil
}

// DeleteByPrefixHLC is the tombstone-aware sibling of DeleteByPrefix:
// every vertex whose projected key matches prefix receives a tombstone
// stamped at ts/expiration. Returns the number of vertices actually
// deleted (matching DeleteByPrefix). limit==0 means unlimited.
func (c *GraphCache[S, T]) DeleteByPrefixHLC(ctx context.Context, prefix string, limit uint32, ts hlc.Timestamp, expiration time.Time) (int, error) {
	keys, err := c.deleteByPrefixHLC(ctx, prefix, limit, ts, expiration, false, nil)
	return len(keys), err
}

// DeleteByPrefixHLCChecked is the locally-originated sibling of
// DeleteByPrefixHLC. Its full victim set is admitted atomically.
func (c *GraphCache[S, T]) DeleteByPrefixHLCChecked(ctx context.Context, prefix string, limit uint32, ts hlc.Timestamp, expiration time.Time) (int, error) {
	keys, err := c.DeleteByPrefixHLCCheckedKeys(ctx, prefix, limit, ts, expiration)
	return len(keys), err
}

// DeleteByPrefixHLCCheckedKeys returns the exact victim keys committed by the
// local prefix mutation. Replication origins use this set to
// log an exact DeleteVertices operation; replaying the broad prefix would let
// a peer delete identities that the origin's limit or causal budget did not
// commit.
func (c *GraphCache[S, T]) DeleteByPrefixHLCCheckedKeys(ctx context.Context, prefix string, limit uint32, ts hlc.Timestamp, expiration time.Time) ([]S, error) {
	return c.deleteByPrefixHLC(ctx, prefix, limit, ts, expiration, true, nil)
}

// DeleteByPrefixHLCCheckedKeysWithPreflight checks causal capacity, then
// preflights the exact causally accepted victims before deleting them under
// the same cache lock. preflight must not call back into GraphCache.
func (c *GraphCache[S, T]) DeleteByPrefixHLCCheckedKeysWithPreflight(ctx context.Context, prefix string, limit uint32, ts hlc.Timestamp, expiration time.Time, preflight func([]S) error) ([]S, error) {
	if preflight == nil {
		return nil, errors.New("HLC prefix delete requires a preflight callback")
	}
	return c.deleteByPrefixHLC(ctx, prefix, limit, ts, expiration, true, preflight)
}

func (c *GraphCache[S, T]) deleteByPrefixHLC(ctx context.Context, prefix string, limit uint32, ts hlc.Timestamp, expiration time.Time, strict bool, preflight func([]S) error) ([]S, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.prefixIndex == nil {
		if preflight != nil {
			return nil, ctx.Err()
		}
		return nil, nil
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
		return nil, err
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
	if strict && ts != (hlc.Timestamp{}) {
		if err := c.checkVertexCausalCapacityLocked(accepted); err != nil {
			return nil, err
		}
	}
	if preflight != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(accepted) > 0 {
			if err := preflight(accepted); err != nil {
				return nil, err
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	c.vertices.DeleteMany(accepted)
	for _, k := range accepted {
		c.setVertexTombstoneLocked(k, ts, expiration)
		if barrier, ok := c.vertexCausalBarriers[k]; ok && !ts.Less(barrier) {
			c.clearVertexCausalBarrierLocked(k)
		}
		if live, ok := c.vertexHLC[k]; ok && !ts.Less(live) {
			c.clearVertexHLCLocked(k)
		}
	}
	c.rebuildIncompleteSearchLocked()
	return accepted, nil
}

// sweepExpiredTombstonesLocked drops tombstones whose expiration has
// passed. Invoked from flush() so reaping rides on the existing GC tick
// rather than introducing a separate timer. Caller must hold c.mu.Lock.
func (c *GraphCache[S, T]) sweepExpiredTombstonesLocked(now time.Time) {
	for k, t := range c.vertexTombstones {
		if !t.expiration.IsZero() && !now.Before(t.expiration) {
			c.clearVertexTombstoneLocked(k)
		}
	}
	for k, t := range c.edgeTombstones {
		if !t.expiration.IsZero() && !now.Before(t.expiration) {
			c.clearEdgeTombstoneLocked(k.Tail, k.Head)
		}
	}
	c.edgeTombstoneDeadlines.shrink()
}
