package pq

import (
	"container/heap"
	"testing"
)

func TestPriorityQueue_PushPopOrder(t *testing.T) {
	q := make(PriorityQueue[string, int], 0)
	heap.Init(&q)

	heap.Push(&q, &Item[string, int]{Value: "low", Priority: 1})
	heap.Push(&q, &Item[string, int]{Value: "high", Priority: 10})
	heap.Push(&q, &Item[string, int]{Value: "mid", Priority: 5})

	if got, want := q.Len(), 3; got != want {
		t.Fatalf("Len() = %d, want %d", got, want)
	}

	want := []string{"high", "mid", "low"}
	for i, w := range want {
		it := heap.Pop(&q).(*Item[string, int])
		if it.Value != w {
			t.Errorf("Pop[%d] = %q, want %q", i, it.Value, w)
		}
		if it.Index != -1 {
			t.Errorf("Pop[%d].Index = %d, want -1 (sentinel)", i, it.Index)
		}
	}
	if q.Len() != 0 {
		t.Errorf("Len() after draining = %d, want 0", q.Len())
	}
}

func TestPriorityQueue_Update(t *testing.T) {
	q := make(PriorityQueue[string, int], 0)
	heap.Init(&q)

	a := &Item[string, int]{Value: "a", Priority: 1}
	b := &Item[string, int]{Value: "b", Priority: 2}
	c := &Item[string, int]{Value: "c", Priority: 3}
	heap.Push(&q, a)
	heap.Push(&q, b)
	heap.Push(&q, c)

	// Promote "a" to highest priority.
	q.update(a, "a*", 100)

	got := heap.Pop(&q).(*Item[string, int])
	if got.Value != "a*" || got.Priority != 100 {
		t.Errorf("after update, Pop() = (%q, %d), want (\"a*\", 100)", got.Value, got.Priority)
	}
}

func TestPriorityQueue_SwapMaintainsIndex(t *testing.T) {
	q := PriorityQueue[string, int]{
		{Value: "x", Priority: 1, Index: 0},
		{Value: "y", Priority: 2, Index: 1},
	}
	q.Swap(0, 1)
	if q[0].Value != "y" || q[0].Index != 0 {
		t.Errorf("q[0] = (%q, idx=%d), want (\"y\", 0)", q[0].Value, q[0].Index)
	}
	if q[1].Value != "x" || q[1].Index != 1 {
		t.Errorf("q[1] = (%q, idx=%d), want (\"x\", 1)", q[1].Value, q[1].Index)
	}
}

func TestSortableMap_Top_KSmallerThanLen(t *testing.T) {
	m := SortableMap[string, float64]{
		"a": 1, "b": 2, "c": 3, "d": 4, "e": 5,
	}
	got := m.Top(2)
	if len(got) != 2 {
		t.Fatalf("len(Top(2)) = %d, want 2", len(got))
	}
	if _, ok := got["e"]; !ok {
		t.Errorf("Top(2) missing \"e\" (priority 5): %v", got)
	}
	if _, ok := got["d"]; !ok {
		t.Errorf("Top(2) missing \"d\" (priority 4): %v", got)
	}
}

func TestSortableMap_Top_Empty(t *testing.T) {
	m := NewSortableMap[string, int]()
	got := m.Top(5)
	if len(got) != 0 {
		t.Errorf("Top on empty map = %v, want empty", got)
	}
}
