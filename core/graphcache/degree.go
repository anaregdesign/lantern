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
// Counts are read under a single point-in-time snapshot (one clock reading for
// the whole walk); like GetServerStatus counts they are best-effort — an edge
// whose endpoint expired but has not yet been swept is already hidden, but the
// snapshot may otherwise race an in-flight writer.
//
// Selection uses a size-k bounded min-heap (pq.SortableMap.Top, the #127
// pattern), so peak memory over the ranking metric is O(k) rather than
// O(candidates). k <= 0 returns nil. Candidates with a zero degree remain
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

	type degreeAcc struct {
		count  uint64
		weight float64
	}
	accum := make(map[S]*degreeAcc)
	now := time.Now()

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
				accum[key] = &degreeAcc{}
			}
		}
		return true
	})
	if len(accum) == 0 {
		c.mu.RUnlock()
		return nil
	}

	// 2. Accumulate live degree per candidate. For OUT-only we walk just the
	// candidates' out-edges (O(sum of candidate out-degrees)); IN and BOTH
	// need a single full pass over the edge table because the store keeps no
	// reverse (head->tails) adjacency to enumerate a head's in-edges.
	countOut := dir == DegreeOut || dir == DegreeBoth
	countIn := dir == DegreeIn || dir == DegreeBoth
	if dir == DegreeOut {
		for tail, a := range accum {
			heads, ok := c.edges.headsOf(tail)
			if !ok {
				continue
			}
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
		}
	} else {
		c.edges.rangeBuckets(func(tail, head S, w *weight) bool {
			aTail, tailIn := accum[tail]
			aHead, headIn := accum[head]
			if !tailIn && !headIn {
				return true
			}
			if !c.vertices.Has(tail) || !c.vertices.Has(head) {
				return true
			}
			sum, _, nonZero := w.snapshotAt(now)
			if !nonZero {
				return true
			}
			if countOut && tailIn {
				aTail.count++
				aTail.weight += float64(sum)
			}
			if countIn && headIn {
				aHead.count++
				aHead.weight += float64(sum)
			}
			return true
		})
	}
	c.mu.RUnlock()

	// 3. Size-k selection over the ranking metric, then order the survivors
	// descending. Both steps are pure post-processing over plain numbers — no
	// cache lock and no *weight pointers survive the critical section.
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
