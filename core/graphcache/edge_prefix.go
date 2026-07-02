package graphcache

import (
	"context"
	"sort"
	"strings"
	"time"
)

// ScanEdgesByPrefix iterates every live edge whose tail projected key
// starts with tailPrefix AND whose head projected key starts with
// headPrefix, in ascending (tail, head) order, invoking fn for each one.
// Either prefix may be empty to disable the corresponding filter (both
// empty scans every edge).
//
// fn may return false to stop the walk early; iteration also stops when
// ctx is cancelled. The boolean return is fn's last verdict — true means
// the caller exhausted the matching set without asking to stop.
//
// Implementation: the walk reuses the vertex-side prefix index for the
// tail dimension. Every edge endpoint is auto-created as a vertex on
// insert (see AddEdgeWithExpiration / PutEdgeWithExpiration), so the
// radix already covers all live tails. For each matching tail the head
// dimension is served by a per-tail head radix (Issue #167) when
// available — see scanTailHeadsFast — or falls back to the v1
// materialise-and-sort path (scanTailHeadsFallback) when the head index
// is disabled. The two paths emit identical (tailProj, headProj) sets
// for any given graph state.
//
// Locking contract mirrors ScanByPrefix (#742): the matching edge rows are
// collected into a point-in-time snapshot under c.mu.RLock, the lock is
// released, and only THEN is fn invoked per row. Because fn runs after the
// lock is dropped, a slow visitor no longer starves writers for the whole
// scan, and fn MAY call back into GraphCache (including write methods)
// without risking sync.RWMutex reentrancy. The tradeoff is that fn observes
// a consistent snapshot taken at collection time, not necessarily the latest
// state after the lock was released, and an unbounded scan buffers one row
// per matching edge — callers that need to bound memory should scope the
// scan with prefixes or page at the service layer.
//
// Returns false when the prefix index is not enabled, when ctx is cancelled,
// or when fn requested an early stop; true on a clean exhaustion of the
// matching set.
func (c *GraphCache[S, T]) ScanEdgesByPrefix(
	ctx context.Context,
	tailPrefix, headPrefix string,
	fn func(tailProjected string, tail S, headProjected string, head S, weight float32, expiration time.Time) bool,
) bool {
	_, ok := c.ScanEdgesByPrefixPage(ctx, tailPrefix, headPrefix, "", "", 0, fn)
	return ok
}

// ScanEdgesByPrefixPage is the paged form of ScanEdgesByPrefix (#836): it
// resumes strictly after the (afterTail, afterHead) pair — seeking the tail
// radix to afterTail (inclusive, because that tail's remaining heads may
// still qualify) and, WITHIN the resume tail only, seeking heads strictly
// past afterHead — and buffers at most limit rows plus one sentinel
// (limit <= 0 means unbounded, i.e. exactly ScanEdgesByPrefix). A resumed
// page therefore costs O(depth + page) instead of O(every matching edge),
// and per-call buffering is bounded by the page size instead of the whole
// matching set.
//
// more reports whether at least one further matching edge exists past the
// returned page; ok mirrors ScanEdgesByPrefix (false on disabled index, ctx
// cancellation, or fn early-stop). Locking/consistency semantics are
// unchanged: rows are collected under one c.mu.RLock, fn runs off-lock.
func (c *GraphCache[S, T]) ScanEdgesByPrefixPage(
	ctx context.Context,
	tailPrefix, headPrefix, afterTail, afterHead string,
	limit int,
	fn func(tailProjected string, tail S, headProjected string, head S, weight float32, expiration time.Time) bool,
) (more, ok bool) {
	snapshot, enabled, collected := c.collectEdgeScanRows(ctx, tailPrefix, headPrefix, afterTail, afterHead, limit)
	if !enabled {
		return false, false
	}
	// Collection was interrupted by ctx cancellation; report the walk as
	// incomplete even if some rows were buffered.
	if !collected {
		return false, false
	}
	if limit > 0 && len(snapshot) > limit {
		more = true
		snapshot = snapshot[:limit]
	}
	for i := range snapshot {
		if err := ctx.Err(); err != nil {
			return more, false
		}
		r := &snapshot[i]
		if !fn(r.tailProj, r.tail, r.headProj, r.head, r.weight, r.expiration) {
			return more, false
		}
	}
	return more, true
}

// edgeScanRow is one matched (tail, head) edge captured during the locked
// collection phase of ScanEdgesByPrefix so the visitor can run after the read
// lock is released (#742).
type edgeScanRow[S comparable] struct {
	tailProj   string
	tail       S
	headProj   string
	head       S
	weight     float32
	expiration time.Time
}

// collectEdgeScanRows gathers matching edges into a snapshot under a single
// c.mu.RLock, reusing the same fast/fallback head walk as the live scan.
// enabled is false when the prefix index is disabled (the caller maps this
// to a false return); collected is false when ctx was cancelled mid-walk.
// The visitor is NOT called here — collection appends rows (stopping one
// row past limit when limit > 0, so the caller can answer `more`), and
// fn early-stop is honoured later during replay, matching ScanByPrefix.
//
// The (afterTail, afterHead) resume point (#836) seeks the tail walk to
// afterTail INCLUSIVE — the cursor tail's remaining heads may still qualify
// — and applies afterHead as a strictly-greater head bound on that one
// resume tail only; every later tail walks its full matching head set.
func (c *GraphCache[S, T]) collectEdgeScanRows(
	ctx context.Context,
	tailPrefix, headPrefix, afterTail, afterHead string,
	limit int,
) (snapshot []edgeScanRow[S], enabled, collected bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.prefixIndex == nil {
		return nil, false, false
	}
	collected = true
	full := func() bool { return limit > 0 && len(snapshot) > limit }
	collect := func(tailProj string, tail S, headProj string, head S, weight float32, expiration time.Time) bool {
		snapshot = append(snapshot, edgeScanRow[S]{
			tailProj:   tailProj,
			tail:       tail,
			headProj:   headProj,
			head:       head,
			weight:     weight,
			expiration: expiration,
		})
		return !full()
	}
	tailWalk := func(visit func(string) bool) {
		if afterTail == "" && afterHead == "" {
			c.prefixIndex.walkPrefix(tailPrefix, visit)
			return
		}
		c.prefixIndex.walkPrefixBound(tailPrefix, afterTail, true, visit)
	}
	tailWalk(func(tailProj string) bool {
		if err := ctx.Err(); err != nil {
			collected = false
			return false
		}
		if full() {
			return false
		}
		tail, ok := c.resolveProjected(tailProj)
		if !ok {
			return true
		}
		heads, ok := c.edges.headsOf(tail)
		if !ok || len(heads) == 0 {
			return true
		}
		headAfter := ""
		if tailProj == afterTail {
			headAfter = afterHead
		}
		if c.headByTail != nil {
			c.scanTailHeadsFast(tail, tailProj, headPrefix, headAfter, heads, collect)
		} else {
			c.scanTailHeadsFallback(tail, tailProj, headPrefix, headAfter, heads, collect)
		}
		return !full()
	})
	return snapshot, true, collected
}

// scanTailHeadsFast emits matching (tail, head) pairs for one tail using
// the per-tail head radix. It walks only the head_prefix subtree of the
// radix instead of materialising every head bucket — the asymptotic win
// for "1 tail with millions of heads, head_prefix matches a small slice"
// workloads (Issue #167). Returns false to abort the outer walk.
func (c *GraphCache[S, T]) scanTailHeadsFast(
	tail S,
	tailProj string,
	headPrefix, headAfter string,
	heads map[vertexID]*weight,
	fn func(tailProjected string, tail S, headProjected string, head S, weight float32, expiration time.Time) bool,
) bool {
	tailID, ok := c.edges.dict.lookup(tail)
	if !ok {
		return c.scanTailHeadsFallback(tail, tailProj, headPrefix, headAfter, heads, fn)
	}
	hi, ok := c.headByTail[tailID]
	if !ok || hi == nil {
		// No per-tail index yet (or out of sync). Fall back to the safe
		// path so we never silently under-report.
		return c.scanTailHeadsFallback(tail, tailProj, headPrefix, headAfter, heads, fn)
	}
	keepGoing := true
	hi.walkPrefixBound(headPrefix, headAfter, func(headProj string, headID vertexID) bool {
		w, ok := heads[headID]
		if !ok {
			// Index/edge drift: skip rather than crash. This window is
			// bounded by c.mu (we hold RLock) so in practice it should
			// not happen, but defensive coding is cheap here.
			return true
		}
		head, ok := c.edges.resolveID(headID)
		if !ok {
			return true
		}
		sum, latest, nonZero := w.snapshot()
		if !nonZero {
			return true
		}
		// Referential closure (#750): hide an edge whose tail or head vertex
		// is no longer live, even if the dangling-edge GC sweep has not yet
		// reclaimed it.
		if !c.edgeEndpointsLive(tail, head) {
			return true
		}
		if !fn(tailProj, tail, headProj, head, sum, latest) {
			keepGoing = false
			return false
		}
		return true
	})
	return keepGoing
}

// scanTailHeadsFallback is the v1 materialise-and-sort path, preserved
// verbatim so we can fall back to it when the head index is disabled
// (disableHeadIndexForTesting) and so benchmarks can A/B the two
// implementations on identical data.
func (c *GraphCache[S, T]) scanTailHeadsFallback(
	tail S,
	tailProj string,
	headPrefix, headAfter string,
	heads map[vertexID]*weight,
	fn func(tailProjected string, tail S, headProjected string, head S, weight float32, expiration time.Time) bool,
) bool {
	type headEntry struct {
		proj string
		head S
		w    *weight
	}
	entries := make([]headEntry, 0, len(heads))
	for headID, w := range heads {
		head, ok := c.edges.resolveID(headID)
		if !ok {
			continue
		}
		proj := c.prefixExtract(head)
		if headPrefix != "" && !strings.HasPrefix(proj, headPrefix) {
			continue
		}
		// Resume-tail head bound (#836): only heads strictly past the
		// cursor's last emitted head qualify.
		if headAfter != "" && proj <= headAfter {
			continue
		}
		entries = append(entries, headEntry{proj: proj, head: head, w: w})
	}
	if len(entries) == 0 {
		return true
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].proj < entries[j].proj })
	for _, e := range entries {
		sum, latest, nonZero := e.w.snapshot()
		if !nonZero {
			continue
		}
		// Referential closure (#750): hide an edge whose tail or head vertex
		// is no longer live, even if the dangling-edge GC sweep has not yet
		// reclaimed it.
		if !c.edgeEndpointsLive(tail, e.head) {
			continue
		}
		if !fn(tailProj, tail, e.proj, e.head, sum, latest) {
			return false
		}
	}
	return true
}
