package mutationreceipt

import (
	"encoding/binary"
	"math"
	"math/rand"
	"testing"
)

func TestDeadlineIndexRemovesTouchedRowsWithoutRebuild(t *testing.T) {
	h := newDeadlineIndex()
	for i, deadline := range []int64{40, 10, 30, 20, 10} {
		h.insert(deadlineEntry{id: ID{byte(i + 1)}, deadlineMS: deadline})
	}
	if h.Len() != 5 || h.oldestMillis() != 10 {
		t.Fatalf("initial index = %v, oldest %d", h.entries, h.oldestMillis())
	}
	if !h.remove(ID{4}) || h.remove(ID{4}) {
		t.Fatal("remove must find exactly one indexed row")
	}
	if !h.remove(ID{3}) || h.Len() != 3 {
		t.Fatalf("middle removals lost index positions: %v", h.entries)
	}
	for id, want := range map[ID]int64{{5}: 10, {2}: 10, {1}: 40} {
		index, ok := h.positions[id]
		if !ok || h.entries[index].id != id || h.entries[index].deadlineMS != want {
			t.Fatalf("position for %x = %d, %v", id, index, ok)
		}
	}
	for _, want := range []ID{{2}, {5}, {1}} {
		if got := h.popOldest().id; got != want {
			t.Fatalf("pop = %x, want %x", got, want)
		}
	}
	if h.Len() != 0 || len(h.positions) != 0 || h.oldestMillis() != 0 {
		t.Fatalf("index retained removed rows: %+v", h)
	}
}

func TestDeadlineIndexArbitraryRemovalOrder(t *testing.T) {
	const entries = 1000
	h := newDeadlineIndex()
	active := make(map[ID]int64, entries)
	for i := 0; i < entries; i++ {
		var id ID
		binary.BigEndian.PutUint32(id[:4], uint32(i+1))
		deadline := int64((i*7919)%1009 + 1)
		h.insert(deadlineEntry{id: id, deadlineMS: deadline})
		active[id] = deadline
	}
	for _, i := range rand.New(rand.NewSource(42)).Perm(entries) {
		var id ID
		binary.BigEndian.PutUint32(id[:4], uint32(i+1))
		if !h.remove(id) {
			t.Fatalf("removed ID %d disappeared from position index", i+1)
		}
		delete(active, id)
		if h.Len() != len(active) || len(h.positions) != len(active) {
			t.Fatalf("after removal %d: rows=%d positions=%d active=%d",
				i, h.Len(), len(h.positions), len(active))
		}
		oldest := int64(math.MaxInt64)
		for key, deadline := range active {
			position, ok := h.positions[key]
			if !ok || h.entries[position].id != key {
				t.Fatalf("ID %x has invalid heap position %d", key, position)
			}
			if deadline < oldest {
				oldest = deadline
			}
		}
		if len(active) == 0 {
			oldest = 0
		}
		if got := h.oldestMillis(); got != oldest {
			t.Fatalf("after removal %d: oldest=%d, want %d", i, got, oldest)
		}
	}
}
