package pq

import (
	"container/heap"
)

/*
 * This code is from https://golang.org/pkg/container/heap/
 */

// Number is any ordered numeric type usable as a priority. Defined locally so
// core/collection/pq does not pull in golang.org/x/exp/constraints.
type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr |
		~float32 | ~float64
}

type Item[S any, T Number] struct {
	Value    S // The Value of the item; arbitrary.
	Priority T // The Priority of the item in the queue.
	// The Index is needed by update and is maintained by the heap.Interface methods.
	Index int // The Index of the item in the heap.
}

// A PriorityQueue implements heap.Interface and holds Items.
type PriorityQueue[S any, T Number] []*Item[S, T]

func (pq PriorityQueue[S, T]) Len() int { return len(pq) }

func (pq PriorityQueue[S, T]) Less(i, j int) bool {
	// We want Pop to give us the highest, not lowest, Priority so we use greater than here.
	return pq[i].Priority > pq[j].Priority
}

func (pq PriorityQueue[S, T]) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].Index = i
	pq[j].Index = j
}

func (pq *PriorityQueue[S, T]) Push(x any) {
	n := len(*pq)
	item := x.(*Item[S, T])
	item.Index = n
	*pq = append(*pq, item)
}

func (pq *PriorityQueue[S, T]) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil  // avoid memory leak
	item.Index = -1 // for safety
	*pq = old[0 : n-1]
	return item
}

// update modifies the Priority and Value of an Item in the queue.
func (pq *PriorityQueue[S, T]) update(item *Item[S, T], value S, priority T) {
	item.Value = value
	item.Priority = priority
	heap.Fix(pq, item.Index)
}
