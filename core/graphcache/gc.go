package graphcache

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

// flush performs the fused GC sweep over the edge map in a single walk.
// It removes zero-weight edges (returned as `zero`) and edges whose endpoints
// reference vertices that are no longer live (returned as `dangling`).
//
// Previously the GC tick walked the edge map twice and additionally cloned
// the whole tf map via snapshotTF before the dangling sweep; folding the two
// checks into one walk eliminates that O(E) snapshot allocation and roughly
// halves the c.mu.Lock hold time on large graphs.
func (c *GraphCache[S, T]) flush() (zero, dangling int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	c.sweepExpiredTombstonesLocked(now)
	c.sweepStaleVertexHLCLocked(now)

	// Per-flush endpoint-liveness memo (#839). The sweep consults keep once
	// per EDGE, which used to cost two dict resolves plus two vertex-cache
	// probes each — four lock round-trips per edge inside the most global
	// critical section the server has, with the tail re-paying them for
	// every head in its bucket and popular heads re-paying across tails.
	// Memoizing per distinct endpoint collapses that to one resolve+probe
	// per endpoint per tick.
	//
	// Scope: exactly this flush call. A budgeted sweep (#744) processes one
	// batch per flush call, so the memo never outlives a tick — a vertex
	// deleted between ticks is re-evaluated by the next tick's fresh memo,
	// and an ID recycled between ticks resolves against current state.
	liveByID := make(map[vertexID]bool)
	liveID := func(id vertexID) bool {
		if live, ok := liveByID[id]; ok {
			return live
		}
		key, ok := c.edges.resolveID(id)
		live := ok && c.vertices.Has(key)
		liveByID[id] = live
		return live
	}
	keep := func(tailID, headID vertexID) bool {
		return liveID(tailID) && liveID(headID)
	}
	indexDelete := c.headIndexOnFlushDeleteLocked()
	onDelete := func(tailID, headID vertexID, w *weight) {
		if tail, tailOK := c.edges.resolveID(tailID); tailOK {
			if head, headOK := c.edges.resolveID(headID); headOK {
				// Any physical bucket removal must retain its non-zero Put
				// floor—not only TTL-zero removal. A zero-weight live Put,
				// cancelling contributions, or dangling endpoint sweep can also
				// delete a bucket whose LWW metadata is still authoritative.
				if ts := w.lastPutTimestamp(); ts != (hlc.Timestamp{}) {
					c.recordEdgeCausalBarrierLocked(tail, head, ts)
				}
			}
		}
		if indexDelete != nil {
			indexDelete(tailID, headID)
		}
	}

	if c.gcEdgeBudget <= 0 {
		// Full sweep (default): unchanged O(E) behavior and the safety net
		// that guarantees every expired/dangling edge is reclaimed each tick.
		c.gcSweepPlan = nil
		c.gcSweepPos = 0
		return c.edges.flushFunc(keep, onDelete)
	}
	return c.flushIncrementalLocked(keep, onDelete)
}

// migrateExpiredVertexHLCToBarriersLocked moves a live Put watermark into the
// retained store as soon as its value is no longer visible. This is the key
// resurrection invariant: TTL expiry may remove the payload, never its last
// replicated Put floor. Caller must hold c.mu.Lock.
func (c *GraphCache[S, T]) migrateExpiredVertexHLCToBarriersLocked(now time.Time) {
	for key, ts := range c.vertexHLC {
		if !c.vertices.HasAt(key, now) {
			c.applyVertexCausalBarrierLocked(key, ts)
		}
	}
}

// migrateExpiredEdgeHLCToBarriersLocked is the edge counterpart used by
// replication snapshot before it filters expired-but-unflushed buckets. GC
// performs the same transfer from its delete callback.
func (c *GraphCache[S, T]) migrateExpiredEdgeHLCToBarriersLocked(now time.Time) {
	type hidden struct {
		tail S
		head S
		ts   hlc.Timestamp
	}
	var hiddenBuckets []hidden
	c.edges.rangeBuckets(func(tail, head S, w *weight) bool {
		contribs, ts, nonEmpty := w.snapshotEntry(now)
		visible := nonEmpty && c.vertices.HasAt(tail, now) && c.vertices.HasAt(head, now)
		if visible {
			var sum float32
			for _, contribution := range contribs {
				sum += contribution.Weight
			}
			visible = sum != 0
		}
		if !visible {
			// A retained barrier can coexist with an Add-only bucket, whose
			// weight.lastHLC is zero. Once all Add rows are hidden at the
			// snapshot instant, remove that physical bucket too; otherwise a
			// later wall-clock rollback could reveal state that the snapshot
			// receiver never got. Preserve the greatest available Put floor.
			if barrier, ok := c.edgeCausalBarriers[EdgeKey[S]{Tail: tail, Head: head}]; ok && ts.Less(barrier) {
				ts = barrier
			}
			hiddenBuckets = append(hiddenBuckets, hidden{tail: tail, head: head, ts: ts})
		}
		return true
	})
	for _, bucket := range hiddenBuckets {
		if bucket.ts == (hlc.Timestamp{}) {
			c.deleteEdgeLocked(bucket.tail, bucket.head)
			continue
		}
		c.applyEdgeCausalBarrierLocked(bucket.tail, bucket.head, bucket.ts)
	}
}

// flushIncrementalLocked sweeps at most c.gcEdgeBudget tail buckets per call,
// advancing c.gcSweepPos across ticks (#744). When the cursor reaches the end
// of the current plan (or no plan exists) it snapshots the live tail set and
// starts a fresh cycle, so every tail present at cycle start is visited exactly
// once before the plan is rebuilt; tails created mid-cycle are picked up by the
// next cycle. This bounds the per-tick c.mu.Lock pause to O(budget × fan-out)
// while preserving eventual cleanup: any expired/dangling edge is reclaimed
// within at most two cycles. Caller must hold c.mu.Lock.
func (c *GraphCache[S, T]) flushIncrementalLocked(
	keep func(tailID, headID vertexID) bool,
	onDelete func(tailID, headID vertexID, w *weight),
) (zero, dangling int) {
	if c.gcSweepPos >= len(c.gcSweepPlan) {
		c.gcSweepPlan = c.edges.tailIDs()
		c.gcSweepPos = 0
	}
	end := c.gcSweepPos + c.gcEdgeBudget
	if end > len(c.gcSweepPlan) {
		end = len(c.gcSweepPlan)
	}
	batch := c.gcSweepPlan[c.gcSweepPos:end]
	c.gcSweepPos = end
	zero, dangling = c.edges.flushTails(batch, keep, onDelete)
	if c.gcSweepPos >= len(c.gcSweepPlan) {
		// Cycle consumed: drop the plan so its backing array can be GC'd and
		// the next tick rebuilds the cursor against the current tail set.
		c.gcSweepPlan = nil
		c.gcSweepPos = 0
	}
	return zero, dangling
}

// vertexHLCShrinkFloor and vertexHLCShrinkDivisor govern when
// sweepStaleVertexHLCLocked reallocates vertexHLC to release its bucket array.
// Go never shrinks a map after delete, so a born-expired churn that peaks at
// hundreds of thousands of entries (#719) and is then drained back to the live
// set (#700) would otherwise leave the full-size backing store pinned on the
// heap — the retention behind the ttl_churn release-bench leak gate (#727).
// The sweep rebuilds the map only when the pre-sweep size cleared the floor and
// the survivors are at most 1/divisor of it: the floor keeps negligible maps
// off the copy path, and the ratio keeps a healthy steady-state workload —
// whose live set tracks the vertex cache, so survivors ≈ pre-sweep size — from
// ever rebuilding.
const (
	vertexHLCShrinkFloor   = 1024
	vertexHLCShrinkDivisor = 4
)

// sweepStaleVertexHLCLocked removes vertexHLC entries whose corresponding
// vertex is no longer live. It is called from flush() which already holds
// c.mu.Lock(), so it is safe to mutate c.vertexHLC without an additional lock.
// This bounds the map to the live replicated-key set rather than the all-time
// set, fixing the leak described in issue #700. After a large drain it also
// reallocates the map so Go can reclaim the oversized bucket array (#727).
func (c *GraphCache[S, T]) sweepStaleVertexHLCLocked(now time.Time) {
	if c.vertexHLC == nil {
		return
	}
	// Record the pre-drain peak first: len(vertexHLC) here is the per-cycle
	// high-water (writes only grow the map between GC ticks; this sweep is the
	// only drain). This is the value that sizes the retained bucket array, so
	// it must be captured before the delete loop below empties the map.
	before := len(c.vertexHLC)
	if before > c.vertexHLCHighWater {
		c.vertexHLCHighWater = before
	}
	for key, ts := range c.vertexHLC {
		if !c.vertices.HasAt(key, now) {
			c.recordVertexCausalBarrierLocked(key, ts)
			c.clearVertexHLCLocked(key)
		}
	}
	// Reclaim the oversized bucket array after a large drain. Go never shrinks a
	// map after delete, so without this the map keeps the heap it sized for its
	// high-water entry count even though len() has collapsed (#727). When the
	// pre-sweep size cleared the floor and the survivors are at most 1/divisor of
	// it, copy them into a right-sized map and drop the old one for the GC.
	if after := len(c.vertexHLC); before >= vertexHLCShrinkFloor && after <= before/vertexHLCShrinkDivisor {
		rebuilt := make(map[S]hlc.Timestamp, after)
		for key, ts := range c.vertexHLC {
			rebuilt[key] = ts
		}
		c.vertexHLC = rebuilt
	}
}

// SetGCHooks installs optional observability callbacks invoked from Watch
// after every GC tick. Either argument may be nil. Hooks must not call back
// into the cache (re-entrant locking would deadlock). Safe for concurrent use.
func (c *GraphCache[S, T]) SetGCHooks(onExpire func(kind string, n int), onGCDuration func(d time.Duration)) {
	c.hookMu.Lock()
	defer c.hookMu.Unlock()
	c.onExpire = onExpire
	c.onGCDuration = onGCDuration
}

// SetGCEdgeBudget bounds how many tail buckets the GC edge sweep walks per
// Watch tick (#744). A positive n caps each tick at n tails, spreading a large
// O(E) sweep across multiple ticks to bound the c.mu.Lock pause; the sweep
// carries a cursor across ticks so every tail is still reclaimed within bounded
// time. n <= 0 (the default) restores the full single-tick sweep and clears any
// in-flight cursor. Safe for concurrent use; takes effect on the next tick.
func (c *GraphCache[S, T]) SetGCEdgeBudget(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n < 0 {
		n = 0
	}
	c.gcEdgeBudget = n
	if n == 0 {
		c.gcSweepPlan = nil
		c.gcSweepPos = 0
	}
}

// GCSweepBacklog reports how many tail buckets remain in the current
// incremental sweep cycle (#744) — those not yet visited by flush(). It is 0
// when the incremental sweep is disabled or a cycle has just completed, and
// lets operators see when bounded cleanup is falling behind the write rate.
func (c *GraphCache[S, T]) GCSweepBacklog() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.gcSweepPlan) - c.gcSweepPos
}

func (c *GraphCache[S, T]) snapshotHooks() (func(string, int), func(time.Duration)) {
	c.hookMu.RLock()
	defer c.hookMu.RUnlock()
	return c.onExpire, c.onGCDuration
}

// flushVertices serializes physical vertex expiry with replication snapshots.
// The inner cache has its own lock, but SnapshotReplication must atomically
// classify each HLC-tracked identity as either a live frame or a causal
// barrier. Letting Cache.Flush bypass c.mu could remove a value between that
// classification and materialization, causing the snapshot to carry neither.
func (c *GraphCache[S, T]) flushVertices() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.vertices.Flush()
}

func (c *GraphCache[S, T]) Watch(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			start := time.Now()
			vExpired := c.flushVertices()
			if vExpired > 0 && c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
				_ = c.RebuildSearchIndex()
			}
			eExpired, dRemoved := c.flush()
			d := time.Since(start)
			onExpire, onGC := c.snapshotHooks()
			if onExpire != nil {
				if vExpired > 0 {
					onExpire("vertex", vExpired)
				}
				if eExpired > 0 {
					onExpire("edge", eExpired)
				}
				if dRemoved > 0 {
					onExpire("dangling_edge", dRemoved)
				}
			}
			if onGC != nil {
				onGC(d)
			}

		case <-ctx.Done():
			return
		}
	}
}
