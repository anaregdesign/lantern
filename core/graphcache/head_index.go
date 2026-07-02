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
	h.walkPrefixBound(prefix, "", fn)
}

// walkPrefixBound is walkPrefix restricted to projected keys strictly
// greater than after (#836) — the resume seek for a partially-consumed tail
// in ScanEdgesByPrefixPage. An empty after is exactly walkPrefix.
func (h *headIndex) walkPrefixBound(prefix, after string, fn func(projected string, headID vertexID) bool) {
	stopped := false
	visit := func(projected string) bool {
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
	}
	if after == "" {
		h.radix.walkPrefix(prefix, visit)
		return
	}
	h.radix.walkPrefixBound(prefix, after, false, visit)
}
