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
//
// Implementation: maintain a size-k min-heap (root = smallest of the current
// top-k). Each map entry is either pushed (heap not yet full) or, if it
// exceeds the root, replaces the root via heap.Fix. Time complexity is
// O(N log k) versus O(N + k log N) for the prior "build a full heap, pop k
// times" approach, and peak memory is O(k) instead of O(N).
func (m SortableMap[S, T]) Top(k int) SortableMap[S, T] {
	if k <= 0 {
		return NewSortableMap[S, T]()
	}
	if len(m) <= k {
		return m
	}

	h := make(topKHeap[S, T], 0, k)
	for key, val := range m {
		if len(h) < k {
			heap.Push(&h, topKEntry[S, T]{value: key, priority: val})
			continue
		}
		// h[0] is the smallest priority currently in the top-k; replace it
		// only when the candidate is strictly larger. Strict inequality
		// keeps the operation cheap when many entries tie at the boundary.
		if val > h[0].priority {
			h[0] = topKEntry[S, T]{value: key, priority: val}
			heap.Fix(&h, 0)
		}
	}

	filtered := make(SortableMap[S, T], len(h))
	for _, e := range h {
		filtered[e.value] = e.priority
	}
	return filtered
}

// topKEntry / topKHeap is an internal, value-type min-heap used only by Top.
// It is deliberately independent of PriorityQueue so this hot path avoids
// per-item pointer allocations and the unused Index field.
type topKEntry[S comparable, T Number] struct {
	value    S
	priority T
}

type topKHeap[S comparable, T Number] []topKEntry[S, T]

func (h topKHeap[S, T]) Len() int           { return len(h) }
func (h topKHeap[S, T]) Less(i, j int) bool { return h[i].priority < h[j].priority }
func (h topKHeap[S, T]) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *topKHeap[S, T]) Push(x any) {
	*h = append(*h, x.(topKEntry[S, T]))
}

func (h *topKHeap[S, T]) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
