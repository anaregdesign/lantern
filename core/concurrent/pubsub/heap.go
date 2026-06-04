package pubsub

// expEntry is a (id, deadline-unix-nano) pair stored in expHeap. The heap is
// keyed on deadline only; id is the back-pointer used by salvage to look up
// the live *Message[T] from Subscription.messages. Acked messages leave their
// expEntry in the heap as a tombstone — salvage skips entries whose id is no
// longer in the map. This avoids an O(log N) heap removal on every Ack at
// the cost of peak-heap-size >= peak-in-flight-count, which is acceptable
// because the heap holds only 16 bytes per entry (#232).
type expEntry struct {
	id       uint64
	deadline int64
}

// expHeap is a min-heap of expEntry ordered by deadline. It implements
// container/heap.Interface. Equal deadlines are tiebroken by id (lower id
// first) so the heap order is deterministic and matches publish order when
// the producer is single-threaded — useful for tests and for FullPolicyDropOldest.
type expHeap []expEntry

func (h expHeap) Len() int { return len(h) }
func (h expHeap) Less(i, j int) bool {
	if h[i].deadline != h[j].deadline {
		return h[i].deadline < h[j].deadline
	}
	return h[i].id < h[j].id
}
func (h expHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *expHeap) Push(x any) { *h = append(*h, x.(expEntry)) }

func (h *expHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
