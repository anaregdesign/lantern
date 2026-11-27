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
	return m.selectK(k, false, nil)
}

// TopStable returns the k entries with the largest values. Equal-priority
// entries are resolved with less: the key for which less(a, b) is true wins
// over b. Passing nil preserves Top's existing unspecified tie behavior.
//
// Use TopStable when membership is externally observable. Lantern's
// traversals, for example, use ascending vertex keys so tied scores produce
// the same subgraph regardless of map iteration order.
func (m SortableMap[S, T]) TopStable(k int, less func(a, b S) bool) SortableMap[S, T] {
	return m.selectK(k, false, less)
}

// Bottom returns the k entries with the smallest values. It mirrors Top —
// same passthrough (len <= k returns the receiver) and same k <= 0 empty
// result — but keeps the lower tail of the value distribution. Illuminate
// uses it for per-hop pruning when the Objective is MINIMIZE so the cheapest
// edges a cost-minimiser cares about survive instead of the costliest (#560).
func (m SortableMap[S, T]) Bottom(k int) SortableMap[S, T] {
	return m.selectK(k, true, nil)
}

// BottomStable returns the k entries with the smallest values. Equal-priority
// entries are resolved with less: the key for which less(a, b) is true wins
// over b. Passing nil preserves Bottom's existing unspecified tie behavior.
func (m SortableMap[S, T]) BottomStable(k int, less func(a, b S) bool) SortableMap[S, T] {
	return m.selectK(k, true, less)
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
func (m SortableMap[S, T]) selectK(k int, smallest bool, less func(a, b S) bool) SortableMap[S, T] {
	if k <= 0 {
		return NewSortableMap[S, T]()
	}
	if len(m) <= k {
		return m
	}

	selection := NewSelection[S, T](k, smallest, less)
	selection.h.entries = make([]kEntry[S, T], 0, k)
	for key, val := range m {
		selection.Add(key, val)
	}
	filtered := make(SortableMap[S, T], selection.h.Len())
	selection.Visit(func(key S, val T) { filtered[key] = val })
	return filtered
}

// Selection keeps the best distinct candidates from a stream without
// materializing the input. The caller supplies each candidate exactly once.
type Selection[S comparable, T Number] struct {
	limit int
	h     boundedKHeap[S, T]
}

// NewSelection selects the smallest priorities if smallest is true, or the
// largest otherwise. For tied priorities less names the preferred candidate.
func NewSelection[S comparable, T Number](limit int, smallest bool, less func(a, b S) bool) Selection[S, T] {
	return Selection[S, T]{limit: limit, h: boundedKHeap[S, T]{smallest: smallest, less: less}}
}

func (s *Selection[S, T]) Add(candidate S, priority T) {
	if s.limit <= 0 {
		return
	}
	if s.h.Len() < s.limit {
		heap.Push(&s.h, kEntry[S, T]{value: candidate, priority: priority})
		return
	}
	if s.h.replaces(candidate, priority, s.h.entries[0]) {
		s.h.entries[0] = kEntry[S, T]{value: candidate, priority: priority}
		heap.Fix(&s.h, 0)
	}
}

// Visit visits the retained entries in heap order. Do not change the selection
// during iteration.
func (s *Selection[S, T]) Visit(emit func(S, T)) {
	for _, entry := range s.h.entries {
		emit(entry.value, entry.priority)
	}
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
	less     func(a, b S) bool
}

func (h boundedKHeap[S, T]) Len() int { return len(h.entries) }

func (h boundedKHeap[S, T]) Less(i, j int) bool {
	// Order the root toward the first entry to evict: the smallest priority
	// for Top (min-heap), the largest priority for Bottom (max-heap).
	if h.entries[i].priority == h.entries[j].priority && h.less != nil {
		// The heap root is the entry first to evict. A stable comparator names
		// the preferred entry, so reverse it here and put the larger/worse key
		// at the root for both heap orientations.
		return h.less(h.entries[j].value, h.entries[i].value)
	}
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
func (h boundedKHeap[S, T]) replaces(candidateKey S, candidate T, root kEntry[S, T]) bool {
	if h.smallest {
		if candidate != root.priority {
			return candidate < root.priority
		}
		return h.less != nil && h.less(candidateKey, root.value)
	}
	if candidate != root.priority {
		return candidate > root.priority
	}
	return h.less != nil && h.less(candidateKey, root.value)
}
