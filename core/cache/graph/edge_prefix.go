package graph

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
// radix already covers all live tails. For each matching tail the heads
// map is materialised and sorted into projected ascending order, then
// post-filtered against headPrefix. Edges with zero weight (fully
// decayed but not yet flushed) are skipped, matching point-read
// semantics.
//
// Locking contract is identical to ScanByPrefix: the walk holds
// c.mu.RLock for its duration. Callers MUST NOT invoke any GraphCache
// write method from inside fn. Long-running fn bodies starve writers;
// callers that need to do non-trivial per-edge work should accumulate
// into a slice and process after ScanEdgesByPrefix returns.
//
// Returns false when the prefix index is not enabled or fn requested an
// early stop; true on a clean exhaustion of the matching set.
func (c *GraphCache[S, T]) ScanEdgesByPrefix(
	ctx context.Context,
	tailPrefix, headPrefix string,
	fn func(tailProjected string, tail S, headProjected string, head S, weight float32, expiration time.Time) bool,
) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.prefixIndex == nil {
		return false
	}
	completed := true
	c.prefixIndex.walkPrefix(tailPrefix, func(tailProj string) bool {
		if err := ctx.Err(); err != nil {
			completed = false
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
		// Materialise + sort per tail. We keep the temporary slice's
		// allocation bound to the fan-out of a single tail; in practice
		// edge fan-outs are small relative to the global vertex count.
		type headEntry struct {
			proj string
			head S
			w    *weight
		}
		entries := make([]headEntry, 0, len(heads))
		for headID, w := range heads {
			head, ok := c.edges.resolveID(headID)
			if !ok {
				// dict drift: skip rather than crash, same as
				// ScanByPrefix.
				continue
			}
			proj := c.prefixExtract(head)
			if headPrefix != "" && !strings.HasPrefix(proj, headPrefix) {
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
			if !fn(tailProj, tail, e.proj, e.head, sum, latest) {
				completed = false
				return false
			}
		}
		return true
	})
	return completed
}
