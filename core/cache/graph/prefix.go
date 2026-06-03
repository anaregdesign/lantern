package graph

import "context"

// ScanByPrefix iterates every live vertex whose projected key starts with
// prefix, in lexicographic order, invoking fn for each one. fn may return
// false to stop the walk early; iteration also stops when ctx is
// cancelled. The boolean return is fn's last verdict (true means the
// caller exhausted the result set without asking to stop).
//
// Consistency: ScanByPrefix takes a point-in-time snapshot of the
// matching (projected, key, value) triples under c.mu.RLock and then
// releases the lock BEFORE invoking fn. As a result fn is free to call
// back into any GraphCache method (including writers) without deadlock,
// at the cost of possibly observing keys whose live state has since
// changed \u2014 the snapshot itself is consistent, but the cache may have
// moved on by the time fn runs. Callers that need to react to the live
// state should re-fetch via GetVertex inside fn.
//
// Releasing the lock before fn runs also avoids the
// sync.RWMutex-reentrancy hazard: a concurrent writer queued on c.mu
// would otherwise block any nested RLock from the visitor's goroutine
// (Go's RWMutex forbids recursive read-locking when a writer is
// waiting), which previously deadlocked callers whose fn touched the
// cache. See #202.
//
// If the prefix index has not been enabled (see EnablePrefixIndex),
// ScanByPrefix returns false immediately and invokes fn zero times. The
// boolean is true if the prefix index is enabled and the walk completed
// without fn requesting a stop.
//
// Vertices whose TTL has already expired but which have not yet been
// flushed are skipped at snapshot time \u2014 ScanByPrefix returns the same
// set of vertices a matching sequence of GetVertex calls under the
// snapshot lock would have returned.
//
// fn receives both the projected string key (the prefix-index view) and
// the original S key (the cache view). For S = string they coincide; for
// other S the projection is intentionally lossy and the original key is
// the one suitable for downstream GraphCache calls.
func (c *GraphCache[S, T]) ScanByPrefix(ctx context.Context, prefix string, fn func(projected string, key S, value T) bool) bool {
	type entry struct {
		projected string
		key       S
		value     T
	}
	var snapshot []entry
	enabled := func() bool {
		c.mu.RLock()
		defer c.mu.RUnlock()
		if c.prefixIndex == nil {
			return false
		}
		// Collect (projected, key, value) under the read lock. We
		// cannot mutate the cache here \u2014 the visitor runs AFTER the
		// lock is released to avoid sync.RWMutex reentrancy deadlocks
		// (see method doc and #202).
		c.prefixIndex.walkPrefix(prefix, func(projected string) bool {
			if err := ctx.Err(); err != nil {
				return false
			}
			key, ok := c.resolveProjected(projected)
			if !ok {
				// Index says the key exists but the dict does not \u2014
				// a developer bug (radix and dict drifted). Skip
				// rather than crash.
				return true
			}
			value, ok := c.vertices.Get(key)
			if !ok {
				// TTL expired between radix insert and snapshot but
				// the async Flush has not run yet; consistent with
				// point-read semantics, treat as absent.
				return true
			}
			snapshot = append(snapshot, entry{projected: projected, key: key, value: value})
			return true
		})
		return true
	}()
	if !enabled {
		return false
	}
	// If snapshot collection was interrupted by ctx cancellation, report
	// the walk as incomplete even when the visitor never ran.
	if err := ctx.Err(); err != nil {
		return false
	}
	for _, e := range snapshot {
		if err := ctx.Err(); err != nil {
			return false
		}
		if !fn(e.projected, e.key, e.value) {
			return false
		}
	}
	return true
}

// CountByPrefix returns the number of indexed keys whose projection
// starts with prefix. The count is taken from the prefix index, so it
// reflects entries that may have expired but not yet been flushed; for
// most workloads the skew is bounded by the Watch tick interval. Returns
// 0 when the index has not been enabled.
func (c *GraphCache[S, T]) CountByPrefix(prefix string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.prefixIndex == nil {
		return 0
	}
	return c.prefixIndex.countPrefix(prefix)
}

// DeleteByPrefix removes every vertex whose projected key starts with
// prefix and returns the number of vertices deleted. limit caps the
// number of deletions in a single call; pass 0 (or a negative value) to
// delete the entire matching set. When the index has not been enabled,
// DeleteByPrefix returns 0 without touching the cache.
//
// Deletion goes through DeleteVertex semantics for each matched key so
// the standard OnEvict / refcount / radix-removal chain runs uniformly.
// Edges incident to a deleted vertex are NOT removed eagerly here \u2014
// they will be reclaimed by the next dangling-edge flush, matching the
// existing DeleteVertex contract.
func (c *GraphCache[S, T]) DeleteByPrefix(ctx context.Context, prefix string, limit int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.prefixIndex == nil {
		return 0
	}
	// Collect first, then delete. We cannot mutate the radix from inside
	// its own walk (the walk holds radix.mu.RLock; delete needs the
	// write lock), and even if we could, the vertex-cache OnEvict
	// callback already calls radix.delete \u2014 which would deadlock.
	var victims []S
	c.prefixIndex.walkPrefix(prefix, func(projected string) bool {
		if err := ctx.Err(); err != nil {
			return false
		}
		if limit > 0 && len(victims) >= limit {
			return false
		}
		key, ok := c.resolveProjected(projected)
		if !ok {
			return true
		}
		victims = append(victims, key)
		return true
	})
	deleted := 0
	for _, k := range victims {
		if c.vertices.Delete(k) {
			deleted++
		}
	}
	return deleted
}

// resolveProjected inverts the prefix extractor. For the common
// identity-projection case (S = string) it returns the projected value
// cast through any; for the general case the radix entry was inserted
// from a key we cannot reconstruct from the projection alone, so we
// scan the dictionary for the matching key. The scan path is a tagged
// fallback \u2014 the fast path covers Lantern's only production
// instantiation today.
func (c *GraphCache[S, T]) resolveProjected(projected string) (S, bool) {
	// Fast path: S is string. We assert through any so the generic
	// instantiation compiles for non-string S without a build tag.
	var zero S
	if _, isString := any(zero).(string); isString {
		anyKey := any(projected)
		key, _ := anyKey.(S)
		// Confirm via the live vertex cache rather than trusting the
		// projection blindly; this also doubles as the TTL liveness
		// check the caller needs.
		if c.vertices.Has(key) {
			return key, true
		}
		return zero, false
	}
	// Slow path: dictionary scan. Only reachable if someone enabled the
	// prefix index on a non-string S; we keep it for completeness rather
	// than panic so the generic remains honest, but it is O(N).
	return c.dict.findByProjection(c.prefixExtract, projected)
}
