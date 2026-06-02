package pq

import (
	"container/heap"
)

type SortableMap[S comparable, T Number] map[S]T

func NewSortableMap[S comparable, T Number]() SortableMap[S, T] {
	return make(map[S]T)
}

func (m SortableMap[S, T]) Top(k int) SortableMap[S, T] {
	if len(m) <= k {
		return m
	}

	pq := make(PriorityQueue[S, T], len(m))

	i := 0
	for k, v := range m {
		pq[i] = &Item[S, T]{
			Value:    k,
			Priority: v,
			Index:    i,
		}
		i++
	}
	heap.Init(&pq)

	filtered := NewSortableMap[S, T]()
	for i := 0; i < k; i++ {
		item := heap.Pop(&pq).(*Item[S, T])
		filtered[item.Value] = item.Priority
	}
	return filtered
}
