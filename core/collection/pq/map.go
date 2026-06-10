package pq

import (
	"container/heap"
)

type SortableMap[S comparable, T Number] map[S]T

func NewSortableMap[S comparable, T Number]() SortableMap[S, T] {
	return make(map[S]T)
}

// Top returns the k entries with the largest values. When the map already
// holds k or fewer entries the receiver is returned as-is (no copy). For
// k <= 0 an empty map is returned.
func (m SortableMap[S, T]) Top(k int) SortableMap[S, T] {
	return m.selectK(k, false)
}

// Bottom returns the k entries with the smallest values. It mirrors Top —
// same passthrough (len <= k returns the receiver) and same k <= 0 empty
// result — but keeps the lower tail of the value distribution. Illuminate
// uses it for per-hop pruning when the Objective is MINIMIZE so the cheapest
// edges a cost-minimiser cares about survive instead of the costliest (#560).
func (m SortableMap[S, T]) Bottom(k int) SortableMap[S, T] {
	return m.selectK(k, true)
}

// selectK returns the k entries at one extreme of the value distribution: the
// largest k when smallest is false (Top), the smallest k when smallest is true
// (Bottom).
//
// Implementation: maintain a size-k bounded heap whose root is the first entry
// to be evicted — the smallest of the current top-k (a min-heap) when selecting
// the largest k, or the largest of the current bottom-k (a max-heap) when
// selecting the smallest k. Each map entry is either pushed (heap not yet full)
// or, when it belongs deeper in the retained set than the root, replaces the
// root via heap.Fix. Time complexity is O(N log k) versus O(N + k log N) for a
// "build a full heap, pop k times" approach, and peak memory is O(k) not O(N).
func (m SortableMap[S, T]) selectK(k int, smallest bool) SortableMap[S, T] {
	if k <= 0 {
		return NewSortableMap[S, T]()
	}
	if len(m) <= k {
		return m
	}

	h := boundedKHeap[S, T]{entries: make([]kEntry[S, T], 0, k), smallest: smallest}
	for key, val := range m {
		if len(h.entries) < k {
			heap.Push(&h, kEntry[S, T]{value: key, priority: val})
			continue
		}
		// h.entries[0] is the root: the boundary entry first to be evicted.
		// Replace it only when the candidate belongs deeper in the retained
		// set. Strict inequality keeps the operation cheap when many entries
		// tie at the boundary.
		if h.replaces(val, h.entries[0].priority) {
			h.entries[0] = kEntry[S, T]{value: key, priority: val}
			heap.Fix(&h, 0)
		}
	}

	filtered := make(SortableMap[S, T], len(h.entries))
	for _, e := range h.entries {
		filtered[e.value] = e.priority
	}
	return filtered
}

// kEntry / boundedKHeap is an internal, value-type bounded heap used only by
// selectK (Top / Bottom). It is deliberately independent of PriorityQueue so
// this hot path avoids per-item pointer allocations and the unused Index field.
//
// The smallest flag flips the heap orientation so a single implementation
// serves both extremes:
//   - smallest == false: a min-heap whose root is the smallest of the current
//     top-k, so larger candidates evict it → the k LARGEST survive (Top).
//   - smallest == true: a max-heap whose root is the largest of the current
//     bottom-k, so smaller candidates evict it → the k SMALLEST survive (Bottom).
type kEntry[S comparable, T Number] struct {
	value    S
	priority T
}

type boundedKHeap[S comparable, T Number] struct {
	entries  []kEntry[S, T]
	smallest bool
}

func (h boundedKHeap[S, T]) Len() int { return len(h.entries) }

func (h boundedKHeap[S, T]) Less(i, j int) bool {
	// Order the root toward the first entry to evict: the smallest priority
	// for Top (min-heap), the largest priority for Bottom (max-heap).
	if h.smallest {
		return h.entries[i].priority > h.entries[j].priority
	}
	return h.entries[i].priority < h.entries[j].priority
}

func (h boundedKHeap[S, T]) Swap(i, j int) {
	h.entries[i], h.entries[j] = h.entries[j], h.entries[i]
}

func (h *boundedKHeap[S, T]) Push(x any) {
	h.entries = append(h.entries, x.(kEntry[S, T]))
}

func (h *boundedKHeap[S, T]) Pop() any {
	old := h.entries
	n := len(old)
	x := old[n-1]
	h.entries = old[:n-1]
	return x
}

// replaces reports whether a candidate priority should evict the current heap
// root. For Top the candidate must be strictly larger than the smallest
// retained entry; for Bottom it must be strictly smaller than the largest
// retained. Strict inequality keeps boundary ties cheap (no churn when many
// entries tie at the boundary).
func (h boundedKHeap[S, T]) replaces(candidate, root T) bool {
	if h.smallest {
		return candidate < root
	}
	return candidate > root
}
