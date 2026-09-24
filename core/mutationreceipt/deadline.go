package mutationreceipt

import (
	"bytes"
	"container/heap"
)

type deadlineEntry struct {
	id         ID
	deadlineMS int64
}

// deadlineIndex has one row per retained receipt. Its position map makes a
// staged batch rollback proportional to only the touched receipts, even when
// unrelated live receipts fill the store. The slice's retained capacity is
// bounded by the configured entry cap; Pop zeroes removed slots for GC.
type deadlineIndex struct {
	entries   []deadlineEntry
	positions map[ID]int
}

func newDeadlineIndex() deadlineIndex {
	return deadlineIndex{positions: make(map[ID]int)}
}

func (h deadlineIndex) Len() int { return len(h.entries) }

func (h deadlineIndex) Less(i, j int) bool {
	if h.entries[i].deadlineMS != h.entries[j].deadlineMS {
		return h.entries[i].deadlineMS < h.entries[j].deadlineMS
	}
	return bytes.Compare(h.entries[i].id[:], h.entries[j].id[:]) < 0
}

func (h deadlineIndex) Swap(i, j int) {
	h.entries[i], h.entries[j] = h.entries[j], h.entries[i]
	h.positions[h.entries[i].id] = i
	h.positions[h.entries[j].id] = j
}

func (h *deadlineIndex) Push(value any) {
	entry := value.(deadlineEntry)
	if _, exists := h.positions[entry.id]; exists {
		panic("mutationreceipt: duplicate deadline ID")
	}
	h.positions[entry.id] = len(h.entries)
	h.entries = append(h.entries, entry)
}

func (h *deadlineIndex) Pop() any {
	last := len(h.entries) - 1
	entry := h.entries[last]
	delete(h.positions, entry.id)
	h.entries[last] = deadlineEntry{}
	h.entries = h.entries[:last]
	return entry
}

func (h *deadlineIndex) insert(entry deadlineEntry) {
	heap.Push(h, entry)
}

func (h *deadlineIndex) remove(id ID) bool {
	index, exists := h.positions[id]
	if !exists {
		return false
	}
	heap.Remove(h, index)
	return true
}

func (h *deadlineIndex) popOldest() deadlineEntry {
	return heap.Pop(h).(deadlineEntry)
}

func (h *deadlineIndex) oldestMillis() int64 {
	if len(h.entries) == 0 {
		return 0
	}
	return h.entries[0].deadlineMS
}
