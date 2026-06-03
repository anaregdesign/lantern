package graph

import "context"

// ScanByPrefix iterates every live vertex whose projected key starts with
// prefix, in lexicographic order, invoking fn for each one. fn may return
// false to stop the walk early; iteration also stops when ctx is
// cancelled. The boolean return is fn's last verdict (true means the
// caller exhausted the result set without asking to stop).
//
// Consistency: the whole walk holds c.mu.RLock, so the snapshot is
// point-in-time consistent with concurrent point reads. Callers MUST NOT
// invoke any GraphCache write method from inside fn \u2014 doing so would
// deadlock (RLock held by the same goroutine cannot be upgraded). Long-
// running fn bodies starve writers; callers that need to do non-trivial
// per-vertex work should accumulate keys into a slice and process them
// after ScanByPrefix returns.
//
// If the prefix index has not been enabled (see EnablePrefixIndex),
// ScanByPrefix returns false immediately and invokes fn zero times. The
// boolean is true if the prefix index is enabled and the walk completed
// without fn requesting a stop.
//
// Vertices whose TTL has already expired but which have not yet been
// flushed are skipped \u2014 ScanByPrefix returns the same set of vertices a
// matching sequence of GetVertex calls would have returned.\n//
// fn receives both the projected string key (the prefix-index view) and
// the original S key (the cache view). For S = string they coincide; for
// other S the projection is intentionally lossy and the original key is
// the one suitable for downstream GraphCache calls.
func (c *GraphCache[S, T]) ScanByPrefix(ctx context.Context, prefix string, fn func(projected string, key S, value T) bool) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.prefixIndex == nil {
		return false
	}
	completed := true
	// We cannot re-resolve "projected -> S" from the radix alone for the
	// general S case, so we look up each key through the dictionary's
	// inverse map. For S = string the projection is identity and the
	// lookup is a no-op overhead. The walk callback must not call back
	// into a writer; radix.walkPrefix documents the same constraint, and
	// we are holding c.mu.RLock which would deadlock a writer anyway.
	c.prefixIndex.walkPrefix(prefix, func(projected string) bool {
		if err := ctx.Err(); err != nil {
			completed = false
			return false
		}
		key, ok := c.resolveProjected(projected)
		if !ok {
			// Index says the key exists but the dict does not \u2014 this
			// can only happen during a developer bug (radix and dict
			// drifted). Skip rather than crash.
			return true
		}
		value, ok := c.vertices.Get(key)
		if !ok {
			// TTL expired between radix insert and this point but the
			// async Flush has not run yet; consistent with point-read
			// semantics, treat as absent.
			return true
		}
		if !fn(projected, key, value) {
			completed = false
			return false
		}
		return true
	})
	return completed
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
