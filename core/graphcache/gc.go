package graphcache

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
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

	c.sweepExpiredTombstonesLocked(time.Now())
	c.sweepStaleVertexHLCLocked()

	keep := func(tailID, headID vertexID) bool {
		tail, ok := c.edges.resolveID(tailID)
		if !ok {
			return false
		}
		head, ok := c.edges.resolveID(headID)
		if !ok {
			return false
		}
		return c.vertices.Has(tail) && c.vertices.Has(head)
	}
	onDelete := c.headIndexOnFlushDeleteLocked()

	if c.gcEdgeBudget <= 0 {
		// Full sweep (default): unchanged O(E) behavior and the safety net
		// that guarantees every expired/dangling edge is reclaimed each tick.
		c.gcSweepPlan = nil
		c.gcSweepPos = 0
		return c.edges.flushFunc(keep, onDelete)
	}
	return c.flushIncrementalLocked(keep, onDelete)
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
	onDelete func(tailID, headID vertexID),
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
func (c *GraphCache[S, T]) sweepStaleVertexHLCLocked() {
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
	for key := range c.vertexHLC {
		if !c.vertices.Has(key) {
			delete(c.vertexHLC, key)
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

func (c *GraphCache[S, T]) Watch(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			start := time.Now()
			vExpired := c.vertices.Flush()
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
