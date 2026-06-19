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

	return c.edges.flushFunc(func(tailID, headID vertexID) bool {
		tail, ok := c.edges.resolveID(tailID)
		if !ok {
			return false
		}
		head, ok := c.edges.resolveID(headID)
		if !ok {
			return false
		}
		return c.vertices.Has(tail) && c.vertices.Has(head)
	}, c.headIndexOnFlushDeleteLocked())
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
