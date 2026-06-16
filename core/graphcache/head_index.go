package graphcache

// headIndex is the per-tail head-side prefix index that backs the head
// dimension of ScanEdgesByPrefix. For one tail vertex it carries:
//
//   - radix:  the projected head strings of every outgoing edge, so a
//     head_prefix query becomes a Patricia-trie probe of the
//     matching subtree (instead of a full fan-out post-filter).
//   - byProj: projected string -> set of head vertexIDs, so an emitted
//     projected key can be resolved back to the underlying
//     edge buckets (one bucket per headID in the set) even when
//     the user-supplied extractor collapses several distinct S
//     keys onto the same projection.
//
// A headIndex is created and mutated only while the surrounding
// GraphCache holds c.mu.Lock — the radix's own mutex is therefore
// redundant for our use, but the radix package keeps it for symmetry
// with the vertex-side prefixIndex and the cost is negligible.
//
// When the byProj map becomes empty (tail has no outgoing edges left)
// the caller drops the *headIndex entry from headByTail entirely, so an
// untouched cache pays zero memory for tails that have never had a
// prefix index enabled at all.
type headIndex struct {
	radix  *radix
	byProj map[string]map[vertexID]struct{}
}

func newHeadIndex() *headIndex {
	return &headIndex{
		radix:  newRadix(),
		byProj: make(map[string]map[vertexID]struct{}),
	}
}

// insert records that headID maps to projected. It is idempotent on
// repeated insertions of the same (projected, headID) pair.
func (h *headIndex) insert(headID vertexID, projected string) {
	set, ok := h.byProj[projected]
	if !ok {
		set = make(map[vertexID]struct{})
		h.byProj[projected] = set
		h.radix.insert(projected)
	}
	set[headID] = struct{}{}
}

// delete removes the (projected, headID) mapping. It reports whether
// the headIndex is now fully empty so the caller can drop the entry
// from its outer map.
func (h *headIndex) delete(headID vertexID, projected string) (empty bool) {
	set, ok := h.byProj[projected]
	if !ok {
		return len(h.byProj) == 0
	}
	if _, ok := set[headID]; !ok {
		return len(h.byProj) == 0
	}
	delete(set, headID)
	if len(set) == 0 {
		delete(h.byProj, projected)
		h.radix.delete(projected)
	}
	return len(h.byProj) == 0
}

// walkPrefix invokes fn with every (projected, headID) pair whose
// projected starts with prefix, in projected-ascending order. The
// order of headIDs WITHIN the same projected bucket is unspecified
// (Go map iteration). fn may return false to stop the walk early.
//
// Locking: walkPrefix takes the underlying radix's RLock for the whole
// traversal, so fn must not call insert or delete on this headIndex.
func (h *headIndex) walkPrefix(prefix string, fn func(projected string, headID vertexID) bool) {
	stopped := false
	h.radix.walkPrefix(prefix, func(projected string) bool {
		if stopped {
			return false
		}
		for headID := range h.byProj[projected] {
			if !fn(projected, headID) {
				stopped = true
				return false
			}
		}
		return true
	})
}

// --- GraphCache wiring -----------------------------------------------------
//
// The following helpers are mounted on GraphCache and called only by the
// edge write paths (single + batch + flush). They preserve a single
// invariant: when c.headByTail != nil, every edge present in c.edges has
// a matching (projected, headID) entry in c.headByTail[tailID], and vice
// versa. Callers MUST hold c.mu in write mode (Lock, not RLock).

// onEdgeAddedLocked maintains the per-tail head index after a successful
// edges.addWithExpiration. When created is false the underlying bucket
// already existed and the index already reflects it; the call is a no-op.
func (c *GraphCache[S, T]) onEdgeAddedLocked(created bool, tailID, headID vertexID, head S) {
	if !created || c.headByTail == nil {
		return
	}
	hi := c.headByTail[tailID]
	if hi == nil {
		hi = newHeadIndex()
		c.headByTail[tailID] = hi
	}
	hi.insert(headID, c.prefixExtract(head))
}

// onEdgeDeletedLocked maintains the per-tail head index after a successful
// edges.delete (or before a flushFunc deleteLocked). The head argument is
// the original S key the caller already has in hand, so we never have to
// resolve it through the dict — which is important because dict refs are
// released by deleteLocked.
func (c *GraphCache[S, T]) onEdgeDeletedLocked(tailID, headID vertexID, head S) {
	if c.headByTail == nil {
		return
	}
	hi, ok := c.headByTail[tailID]
	if !ok {
		return
	}
	if empty := hi.delete(headID, c.prefixExtract(head)); empty {
		delete(c.headByTail, tailID)
	}
}

// headIndexOnFlushDeleteLocked returns the onDelete callback handed to
// edgeCache.flushFunc. It captures head S BEFORE deleteLocked runs
// (flushFunc invokes the callback first), so resolveID is guaranteed to
// succeed. Returns nil when the head index is disabled, in which case
// flushFunc skips the call entirely.
func (c *GraphCache[S, T]) headIndexOnFlushDeleteLocked() func(tailID, headID vertexID) {
	if c.headByTail == nil {
		return nil
	}
	return func(tailID, headID vertexID) {
		head, ok := c.edges.resolveID(headID)
		if !ok {
			return
		}
		c.onEdgeDeletedLocked(tailID, headID, head)
	}
}

// disableHeadIndexForTesting drops the per-tail head index so
// ScanEdgesByPrefix falls back to the v1 materialise-and-sort path. It
// exists so regression tests and before/after benchmarks can exercise
// the fallback under identical data. Not part of the public API: lower
// case on purpose, and never invoked by production code.
func (c *GraphCache[S, T]) disableHeadIndexForTesting() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.headByTail = nil
}
