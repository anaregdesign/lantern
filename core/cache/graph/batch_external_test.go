package graph_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/cache/graph"
)

// TestAddEdgesWithExpiration_AtomicNeighborSnapshot verifies that a
// concurrent reader using Neighbor (which holds the cache RLock for the
// duration of a single call) observes either the pre-batch state (no
// edges) or the post-batch state (all batched edges), never a partial
// fan-in.
func TestAddEdgesWithExpiration_AtomicNeighborSnapshot(t *testing.T) {
	const fanOut = 128
	c := graph.NewGraphCache[string, int](time.Minute)

	exp := time.Now().Add(time.Minute)
	c.PutVertexWithExpiration("s", 0, exp)

	addBatch := make([]graph.EdgeItem[string], fanOut)
	delBatch := make([]graph.EdgeKey[string], fanOut)
	for i := 0; i < fanOut; i++ {
		head := "h" + itoa(i)
		addBatch[i] = graph.EdgeItem[string]{Tail: "s", Head: head, Weight: 1, Expiration: exp}
		delBatch[i] = graph.EdgeKey[string]{Tail: "s", Head: head}
	}

	var (
		stop      atomic.Bool
		readerWG  sync.WaitGroup
		violation atomic.Int64
		reads     atomic.Int64
	)

	readerWG.Add(4)
	for r := 0; r < 4; r++ {
		go func() {
			defer readerWG.Done()
			for !stop.Load() {
				g := c.Neighbor("s", 1, fanOut+1, false)
				reads.Add(1)
				if g == nil {
					continue
				}
				count := 0
				for _, heads := range g.Edges {
					count += len(heads)
				}
				if count != 0 && count != fanOut {
					violation.Add(1)
				}
			}
		}()
	}

	for i := 0; i < 100; i++ {
		c.DeleteEdges(delBatch)
		c.AddEdgesWithExpiration(addBatch)
	}

	stop.Store(true)
	readerWG.Wait()

	if reads.Load() == 0 {
		t.Fatal("reader never executed; test is meaningless")
	}
	if v := violation.Load(); v != 0 {
		t.Fatalf("reader observed %d intermediate snapshots out of %d reads; batch is not atomic under Neighbor", v, reads.Load())
	}
}

// TestDeleteEdges_AtomicNeighborSnapshot is the symmetric test: every
// Neighbor call sees either all batched edges or none.
func TestDeleteEdges_AtomicNeighborSnapshot(t *testing.T) {
	const fanOut = 128
	c := graph.NewGraphCache[string, int](time.Minute)

	exp := time.Now().Add(time.Minute)
	c.PutVertexWithExpiration("s", 0, exp)

	addBatch := make([]graph.EdgeItem[string], fanOut)
	delBatch := make([]graph.EdgeKey[string], fanOut)
	for i := 0; i < fanOut; i++ {
		head := "h" + itoa(i)
		addBatch[i] = graph.EdgeItem[string]{Tail: "s", Head: head, Weight: 1, Expiration: exp}
		delBatch[i] = graph.EdgeKey[string]{Tail: "s", Head: head}
	}

	var (
		stop      atomic.Bool
		readerWG  sync.WaitGroup
		violation atomic.Int64
		reads     atomic.Int64
	)

	readerWG.Add(4)
	for r := 0; r < 4; r++ {
		go func() {
			defer readerWG.Done()
			for !stop.Load() {
				g := c.Neighbor("s", 1, fanOut+1, false)
				reads.Add(1)
				if g == nil {
					continue
				}
				count := 0
				for _, heads := range g.Edges {
					count += len(heads)
				}
				if count != 0 && count != fanOut {
					violation.Add(1)
				}
			}
		}()
	}

	for i := 0; i < 100; i++ {
		c.AddEdgesWithExpiration(addBatch)
		c.DeleteEdges(delBatch)
	}

	stop.Store(true)
	readerWG.Wait()

	if reads.Load() == 0 {
		t.Fatal("reader never executed; test is meaningless")
	}
	if v := violation.Load(); v != 0 {
		t.Fatalf("reader observed %d intermediate snapshots out of %d reads; batch delete is not atomic under Neighbor", v, reads.Load())
	}
}

// TestPutEdgesWithExpiration_NoTransientNotFound verifies the per-edge
// invariant of PutEdges: callers replacing edges in a batch never expose a
// transient NotFound where a concurrent GetEdge sees the edge missing
// between the per-item delete and re-add.
func TestPutEdgesWithExpiration_NoTransientNotFound(t *testing.T) {
	const batchSize = 128
	c := graph.NewGraphCache[string, int](time.Minute)

	items := make([]graph.EdgeItem[string], batchSize)
	keys := make([]graph.EdgeKey[string], batchSize)
	for i := 0; i < batchSize; i++ {
		tail, head := "t"+itoa(i), "h"+itoa(i)
		items[i] = graph.EdgeItem[string]{Tail: tail, Head: head, Weight: 1, Expiration: time.Now().Add(time.Minute)}
		keys[i] = graph.EdgeKey[string]{Tail: tail, Head: head}
	}
	c.AddEdgesWithExpiration(items)

	var (
		stop     atomic.Bool
		readerWG sync.WaitGroup
		missing  atomic.Int64
	)

	readerWG.Add(4)
	for r := 0; r < 4; r++ {
		go func() {
			defer readerWG.Done()
			for !stop.Load() {
				for _, k := range keys {
					if _, _, ok := c.GetEdgeDetail(k.Tail, k.Head); !ok {
						missing.Add(1)
					}
				}
			}
		}()
	}

	for i := 0; i < 100; i++ {
		c.PutEdgesWithExpiration(items)
	}

	stop.Store(true)
	readerWG.Wait()

	if m := missing.Load(); m != 0 {
		t.Fatalf("reader observed %d transient NotFound during PutEdges; batch replace is not atomic", m)
	}
}

// TestBatchAPIs_ReturnCounts spot-checks the bookkeeping returned by
// DeleteVertices / DeleteEdges (count of entries actually removed).
func TestBatchAPIs_ReturnCounts(t *testing.T) {
	c := graph.NewGraphCache[string, int](time.Minute)
	exp := time.Now().Add(time.Minute)

	c.PutVerticesWithExpiration([]graph.VertexItem[string, int]{
		{Key: "a", Value: 1, Expiration: exp},
		{Key: "b", Value: 2, Expiration: exp},
		{Key: "c", Value: 3, Expiration: exp},
	})
	if n := c.DeleteVertices([]string{"a", "b", "missing"}); n != 2 {
		t.Fatalf("DeleteVertices: got %d, want 2", n)
	}

	c.AddEdgesWithExpiration([]graph.EdgeItem[string]{
		{Tail: "x", Head: "y", Weight: 1, Expiration: exp},
		{Tail: "y", Head: "z", Weight: 1, Expiration: exp},
	})
	got := c.DeleteEdges([]graph.EdgeKey[string]{
		{Tail: "x", Head: "y"},
		{Tail: "nope", Head: "nope"},
	})
	if got != 1 {
		t.Fatalf("DeleteEdges: got %d, want 1", got)
	}
}

// itoa avoids pulling in strconv just to label test keys.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	const digits = "0123456789"
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = digits[i%10]
		i /= 10
	}
	return string(buf[pos:])
}
