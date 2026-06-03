package graph

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

// TestDictRefcount_VertexPutIsIdempotent verifies that re-inserting the same
// vertex key does not inflate the dictionary refcount. After N PutWithExpiration
// calls for the same key the dict must hold exactly one reference.
func TestDictRefcount_VertexPutIsIdempotent(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	for i := 0; i < 50; i++ {
		c.AddVertexWithExpiration("k", i, time.Now().Add(time.Minute))
	}
	if got := c.dict.len(); got != 1 {
		t.Fatalf("dict.len after 50 puts of same key = %d, want 1", got)
	}
	if got := c.vertices.Count(); got != 1 {
		t.Fatalf("vertices.Count = %d, want 1", got)
	}
}

// TestDictRefcount_PutEdgeIdempotent verifies that PutEdge on the same
// (tail, head) does not inflate dict refcounts. After N PutEdge calls the
// dict still holds exactly the references owned by the vertex slots (1 per
// endpoint) plus the single edge (1 per endpoint) = 2 net refs per endpoint.
func TestDictRefcount_PutEdgeIdempotent(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	for i := 0; i < 25; i++ {
		c.PutEdgeWithExpiration("a", "b", float32(i+1), time.Now().Add(time.Minute))
	}
	// Two distinct vertex slots ("a", "b"); each interned once for the vertex
	// cache entry and once for the single edge endpoint = 2 ids in the dict.
	if got := c.dict.len(); got != 2 {
		t.Fatalf("dict.len after 25 PutEdge same (a,b) = %d, want 2", got)
	}
	// Each vertex id holds refcount 2 (vertex slot + edge endpoint).
	id, ok := c.dict.lookup("a")
	if !ok {
		t.Fatalf("dict.lookup(a) missing")
	}
	if got := c.dict.refcount[id]; got != 2 {
		t.Fatalf("refcount(a) = %d, want 2", got)
	}
}

// TestDictRefcount_AddEdgeAdditiveBumps verifies that AddEdge on the same
// (tail, head) keeps the edge-slot refcount at exactly 1 per endpoint
// (additive contributions on the same edge are not new edges).
func TestDictRefcount_AddEdgeAdditiveBumps(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	for i := 0; i < 10; i++ {
		c.AddEdgeWithExpiration("x", "y", 1, time.Now().Add(time.Minute))
	}
	if got := c.dict.len(); got != 2 {
		t.Fatalf("dict.len after 10 AddEdge same (x,y) = %d, want 2", got)
	}
	id, _ := c.dict.lookup("x")
	if got := c.dict.refcount[id]; got != 2 {
		t.Fatalf("refcount(x) = %d, want 2 (vertex slot + 1 edge endpoint)", got)
	}
}

// TestDictRefcount_DeleteReleases verifies vertex Delete drops the vertex
// slot's reference (through SetOnEvict) and edge Delete drops both endpoint
// references.
func TestDictRefcount_DeleteReleases(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	c.AddVertexWithExpiration("v", 1, time.Now().Add(time.Minute))
	if got := c.dict.len(); got != 1 {
		t.Fatalf("dict.len after add = %d, want 1", got)
	}
	c.DeleteVertex("v")
	if got := c.dict.len(); got != 0 {
		t.Fatalf("dict.len after DeleteVertex = %d, want 0", got)
	}

	c.AddEdgeWithExpiration("a", "b", 1, time.Now().Add(time.Minute))
	if got := c.dict.len(); got != 2 {
		t.Fatalf("dict.len after AddEdge = %d, want 2", got)
	}
	c.DeleteEdge("a", "b")
	// Endpoints still live as vertex slots (refcount 1 each).
	if got := c.dict.len(); got != 2 {
		t.Fatalf("dict.len after DeleteEdge = %d, want 2", got)
	}
	c.DeleteVertex("a")
	c.DeleteVertex("b")
	if got := c.dict.len(); got != 0 {
		t.Fatalf("dict.len after deleting both vertices = %d, want 0", got)
	}
}

// TestDictRefcount_InsertDeleteInvariant fuzzes random Add/Delete sequences
// and asserts dict.len() always tracks the distinct live keys.
func TestDictRefcount_InsertDeleteInvariant(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	r := rand.New(rand.NewSource(42))
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}

	for step := 0; step < 500; step++ {
		k1 := keys[r.Intn(len(keys))]
		k2 := keys[r.Intn(len(keys))]
		switch r.Intn(5) {
		case 0:
			c.AddVertexWithExpiration(k1, step, time.Now().Add(time.Minute))
		case 1:
			c.AddEdgeWithExpiration(k1, k2, 1, time.Now().Add(time.Minute))
		case 2:
			c.PutEdgeWithExpiration(k1, k2, 1, time.Now().Add(time.Minute))
		case 3:
			c.DeleteEdge(k1, k2)
		case 4:
			c.DeleteVertex(k1)
		}

		// Invariant: every key with refcount > 0 must be one of:
		//   - present in the vertex cache (vertex-slot ref), or
		//   - referenced as an endpoint of a live edge.
		// And every distinct live key (vertex OR endpoint) must own an id.
		live := make(map[string]struct{})
		for _, k := range keys {
			if c.vertices.Has(k) {
				live[k] = struct{}{}
			}
		}
		for tail, heads := range c.edges.snapshotTF() {
			live[tail] = struct{}{}
			for head := range heads {
				live[head] = struct{}{}
			}
		}
		if got := c.dict.len(); got != len(live) {
			t.Fatalf("step %d: dict.len=%d, distinct live keys=%d (keys=%v)",
				step, got, len(live), live)
		}
	}
}

// TestDictRefcount_FullExpiryFreesAll verifies that after every entry expires
// and the GC sweep runs, the dict is empty and every id ever allocated has
// been returned to the freelist.
func TestDictRefcount_FullExpiryFreesAll(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	const N = 20
	short := 30 * time.Millisecond
	deadline := time.Now().Add(short)

	for i := 0; i < N; i++ {
		k := fmt.Sprintf("v%d", i)
		c.AddVertexWithExpiration(k, i, deadline)
	}
	for i := 0; i < N; i++ {
		for j := 0; j < 3; j++ {
			tail := fmt.Sprintf("v%d", i)
			head := fmt.Sprintf("v%d", (i+j+1)%N)
			c.AddEdgeWithExpiration(tail, head, 1, deadline)
		}
	}

	maxID := c.dict.len()
	if maxID == 0 {
		t.Fatalf("setup error: dict empty before expiry")
	}

	time.Sleep(short + 80*time.Millisecond)

	// Reproduce the Watch tick: expire vertex entries, sweep dangling edges,
	// flush expired edges.
	c.vertices.Flush()
	c.flush()
	c.edges.flush()

	if got := c.dict.len(); got != 0 {
		t.Fatalf("dict.len after full expiry = %d, want 0", got)
	}
	if got := len(c.dict.free); got != maxID {
		t.Fatalf("dict.free count after full expiry = %d, want %d (every id should be reusable)",
			got, maxID)
	}
}
