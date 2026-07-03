package graphcache

import (
	"sort"
	"time"

	"github.com/anaregdesign/lantern/core/collection/pq"
)

// DegreeDirection selects which incident edges count toward a vertex's degree
// in TopVerticesByDegree.
type DegreeDirection uint8

const (
	// DegreeOut counts edges leaving the vertex (the vertex as tail).
	DegreeOut DegreeDirection = iota
	// DegreeIn counts edges entering the vertex (the vertex as head).
	DegreeIn
	// DegreeBoth counts edges in either direction. A reciprocal pair
	// contributes to both endpoints independently, and a self-loop counts
	// twice for its vertex (once as out, once as in).
	DegreeBoth
)

// DegreeEntry is one ranked vertex in a TopVerticesByDegree result: the vertex
// key, its live edge count in the chosen direction (Degree), and the summed
// live edge weight in that direction (WeightedDegree).
type DegreeEntry[S comparable] struct {
	Key            S
	Degree         uint64
	WeightedDegree float64
}

// TopVerticesByDegree ranks the live vertices whose projected key starts with
// prefix by their degree in the requested direction and returns the top k in
// descending order of the ranking metric (WeightedDegree when weighted is
// true, else Degree). It answers the cold-start "which vertices under this
// namespace are most connected?" question without shipping the subgraph to the
// client (#900).
//
// Live-visibility (#750): only live vertices are candidates, and an edge counts
// only when BOTH endpoints are live and the edge has not fully decayed to zero.
//
// Consistency (#920): the OUT-only walk visits just the candidates' out-edges
// and runs entirely under the read lock, so it observes a point-in-time
// snapshot. IN/BOTH has no reverse (head->tails) index and must consider every
// edge bucket; to avoid holding the aggregate read lock for that O(E) pass — and
// stalling every writer for its duration — it collects the candidate-incident
// bucket references under the lock, releases it, and reads the weights
// afterwards. The IN/BOTH result is therefore a best-effort snapshot: an edge
// added or deleted while the weights are being read may be partially reflected.
// Like GetServerStatus counts these are advisory, not transactional.
//
// Selection uses a size-k bounded min-heap (pq.SortableMap.Top, the #127
// pattern), so peak memory over the ranking metric is O(k) rather than
// O(candidates). Accumulation working memory is O(candidates) — one value-typed
// accumulator per candidate, no per-candidate heap allocation — plus, for
// IN/BOTH, O(candidate-incident edges) transient bucket references held across
// the unlocked read. k <= 0 returns nil. Candidates with a zero degree remain
// eligible, so they surface only when fewer than k vertices under the prefix
// carry any live incident edge. Ties at the k-th boundary are broken
// arbitrarily; the returned slice is otherwise sorted by the ranking metric
// descending (callers that need a total order should apply a key tie-break).
//
// The prefix index must be enabled (EnablePrefixIndex); otherwise the method
// returns nil.
func (c *GraphCache[S, T]) TopVerticesByDegree(prefix string, k int, dir DegreeDirection, weighted bool) []DegreeEntry[S] {
	if k <= 0 {
		return nil
	}

	// A value-typed accumulator carries both metrics (the DegreeEntry result
	// always reports Degree and WeightedDegree regardless of the ranking axis),
	// while avoiding the per-candidate pointer allocation of a map[S]*degreeAcc.
	type degreeAcc struct {
		count  uint64
		weight float64
	}
	accum := make(map[S]degreeAcc)
	now := time.Now()

	countOut := dir == DegreeOut || dir == DegreeBoth
	countIn := dir == DegreeIn || dir == DegreeBoth

	// A candidate-incident edge bucket captured under the read lock but read
	// (snapshotAt) after it is released. The *weight is individually
	// mutex-guarded, so reading it outside c.mu is safe.
	type bucketRef struct {
		tail, head S
		w          *weight
	}
	var buckets []bucketRef

	c.mu.RLock()
	if c.prefixIndex == nil {
		c.mu.RUnlock()
		return nil
	}

	// 1. Enumerate the live vertices under prefix as ranking candidates.
	// resolveProjected confirms liveness against the vertex cache, so a stale
	// radix posting for an expired-but-unflushed vertex is not a candidate.
	c.prefixIndex.walkPrefix(prefix, func(projected string) bool {
		if key, ok := c.resolveProjected(projected); ok {
			if _, seen := accum[key]; !seen {
				accum[key] = degreeAcc{}
			}
		}
		return true
	})
	if len(accum) == 0 {
		c.mu.RUnlock()
		return nil
	}

	// 2. Accumulate live degree per candidate.
	if dir == DegreeOut {
		// OUT-only walks just the candidates' out-edges (O(sum of candidate
		// out-degrees)) — cheap and already scoped — so it stays under the read
		// lock and snapshots the weights in place.
		for tail := range accum {
			heads, ok := c.edges.headsOf(tail)
			if !ok {
				continue
			}
			a := accum[tail]
			for headID, w := range heads {
				head, ok := c.edges.resolveID(headID)
				if !ok || !c.vertices.Has(head) {
					continue
				}
				sum, _, nonZero := w.snapshotAt(now)
				if !nonZero {
					continue
				}
				a.count++
				a.weight += float64(sum)
			}
			accum[tail] = a
		}
		c.mu.RUnlock()
	} else {
		// IN/BOTH must scan every bucket (no reverse index). Capture only the
		// candidate-incident bucket references here — a cheap membership test,
		// no weight snapshot — then release the lock so the O(E) weight reads do
		// not stall writers waiting on c.mu.
		c.edges.rangeBuckets(func(tail, head S, w *weight) bool {
			_, tailIn := accum[tail]
			_, headIn := accum[head]
			if tailIn || headIn {
				buckets = append(buckets, bucketRef{tail: tail, head: head, w: w})
			}
			return true
		})
		c.mu.RUnlock()

		// Unlocked accumulation over the captured references. Endpoint liveness
		// is re-checked against the independently-locked vertex cache; each
		// weight snapshot takes only its own mutex. A self-loop under DegreeBoth
		// contributes twice (out and in) to one candidate, so each branch
		// re-reads the accumulator before mutating it.
		for _, b := range buckets {
			if !c.vertices.Has(b.tail) || !c.vertices.Has(b.head) {
				continue
			}
			sum, _, nonZero := b.w.snapshotAt(now)
			if !nonZero {
				continue
			}
			if countOut {
				if a, ok := accum[b.tail]; ok {
					a.count++
					a.weight += float64(sum)
					accum[b.tail] = a
				}
			}
			if countIn {
				if a, ok := accum[b.head]; ok {
					a.count++
					a.weight += float64(sum)
					accum[b.head] = a
				}
			}
		}
	}

	// 3. Size-k selection over the ranking metric, then order the survivors
	// descending. Both steps are pure post-processing over plain numbers — no
	// cache lock and no *weight pointers survive into the sort.
	scores := pq.NewSortableMap[S, float64]()
	for key, a := range accum {
		if weighted {
			scores[key] = a.weight
		} else {
			scores[key] = float64(a.count)
		}
	}
	top := scores.Top(k)

	entries := make([]DegreeEntry[S], 0, len(top))
	for key := range top {
		a := accum[key]
		entries = append(entries, DegreeEntry[S]{Key: key, Degree: a.count, WeightedDegree: a.weight})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if weighted {
			return entries[i].WeightedDegree > entries[j].WeightedDegree
		}
		return entries[i].Degree > entries[j].Degree
	})
	return entries
}
