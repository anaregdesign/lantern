package graphcache

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

// vertexByKey indexes a vertex snapshot by key for order-independent
// comparison (Range / rangeBuckets iteration order is unspecified).
func vertexByKey(vs []SnapshotVertex[string, string]) map[string]SnapshotVertex[string, string] {
	m := make(map[string]SnapshotVertex[string, string], len(vs))
	for _, v := range vs {
		m[v.Key] = v
	}
	return m
}

// edgeByEndpoints indexes an edge snapshot by (tail, head).
func edgeByEndpoints(es []SnapshotEdge[string]) map[EdgeKey[string]]SnapshotEdge[string] {
	m := make(map[EdgeKey[string]]SnapshotEdge[string], len(es))
	for _, e := range es {
		m[EdgeKey[string]{Tail: e.Tail, Head: e.Head}] = e
	}
	return m
}

func TestGraphCache_SnapshotGraph(t *testing.T) {
	// SnapshotGraph must return exactly what SnapshotVertices and
	// SnapshotEdges return — it is the same materialise logic taken under a
	// single lock instead of two.
	t.Run("MatchesSingleMethods", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		future := time.Now().Add(time.Hour)
		c.PutVerticesWithExpiration([]VertexItem[string, string]{
			{Key: "a", Value: "A", Expiration: future},
			{Key: "b", Value: "B", Expiration: future},
			{Key: "c", Value: "C", Expiration: future},
		})
		c.PutEdgesWithExpiration([]EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 1.5, Expiration: future},
			{Tail: "b", Head: "c", Weight: 2, Expiration: future},
		})

		got := c.SnapshotGraph()
		wantV := c.SnapshotVertices()
		wantE := c.SnapshotEdges()

		if !reflect.DeepEqual(vertexByKey(got.Vertices), vertexByKey(wantV)) {
			t.Errorf("SnapshotGraph().Vertices = %v, want (SnapshotVertices) %v", got.Vertices, wantV)
		}
		if !reflect.DeepEqual(edgeByEndpoints(got.Edges), edgeByEndpoints(wantE)) {
			t.Errorf("SnapshotGraph().Edges = %v, want (SnapshotEdges) %v", got.Edges, wantE)
		}
		if len(got.Vertices) != 3 {
			t.Errorf("got %d vertices, want 3", len(got.Vertices))
		}
		if len(got.Edges) != 2 {
			t.Errorf("got %d edges, want 2", len(got.Edges))
		}
	})

	t.Run("SkipsExpiredVerticesAndEdges", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		now := time.Now()
		future := now.Add(time.Hour)
		past := now.Add(-time.Hour)
		c.PutVerticesWithExpiration([]VertexItem[string, string]{
			{Key: "a", Value: "A", Expiration: future},
			{Key: "b", Value: "B", Expiration: future},
			{Key: "gone", Value: "X", Expiration: past},
		})
		c.PutEdgesWithExpiration([]EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 1, Expiration: future},
			{Tail: "b", Head: "gone", Weight: 1, Expiration: past},
		})

		got := c.SnapshotGraph()

		vs := vertexByKey(got.Vertices)
		if _, ok := vs["gone"]; ok {
			t.Errorf("expired vertex 'gone' present in snapshot: %v", got.Vertices)
		}
		if _, ok := vs["a"]; !ok {
			t.Errorf("live vertex 'a' missing from snapshot: %v", got.Vertices)
		}
		if len(got.Vertices) != 2 {
			t.Errorf("got %d vertices, want 2 (a,b)", len(got.Vertices))
		}

		es := edgeByEndpoints(got.Edges)
		if _, ok := es[EdgeKey[string]{Tail: "b", Head: "gone"}]; ok {
			t.Errorf("expired edge b->gone present in snapshot: %v", got.Edges)
		}
		if _, ok := es[EdgeKey[string]{Tail: "a", Head: "b"}]; !ok {
			t.Errorf("live edge a->b missing from snapshot: %v", got.Edges)
		}
		if len(got.Edges) != 1 {
			t.Errorf("got %d edges, want 1 (a->b)", len(got.Edges))
		}
	})

	t.Run("EmptyCache", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		got := c.SnapshotGraph()
		if len(got.Vertices) != 0 {
			t.Errorf("got %d vertices, want 0", len(got.Vertices))
		}
		if len(got.Edges) != 0 {
			t.Errorf("got %d edges, want 0", len(got.Edges))
		}
	})

	// Exercise SnapshotGraph's single-lock pass against concurrent writers.
	// Run under -race (CI default) this catches a missing or wrong lock.
	t.Run("ConcurrentWritesNoRace", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		future := time.Now().Add(time.Hour)
		var wg sync.WaitGroup

		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				key := string(rune('a' + w))
				for i := 0; i < 200; i++ {
					c.PutVerticesWithExpiration([]VertexItem[string, string]{
						{Key: key, Value: "v", Expiration: future},
					})
					c.PutEdgesWithExpiration([]EdgeItem[string]{
						{Tail: key, Head: "hub", Weight: 1, Expiration: future},
					})
				}
			}(w)
		}
		for s := 0; s < 2; s++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 200; i++ {
					_ = c.SnapshotGraph()
				}
			}()
		}
		wg.Wait()

		if got := c.SnapshotGraph(); len(got.Vertices) == 0 {
			t.Error("expected some vertices after concurrent writes")
		}
	})
}

// TestGraphCache_Snapshot_ReferentialClosure pins the snapshot half of the
// referential-closure contract (#750): a LIVE edge whose endpoint vertex has
// been deleted (or has expired but not yet been flushed) must not appear in
// SnapshotGraph or SnapshotEdges, even before the dangling-edge GC sweep, so a
// restored backup is always referentially closed.
func TestGraphCache_Snapshot_ReferentialClosure(t *testing.T) {
	live := time.Now().Add(time.Hour)
	build := func() *GraphCache[string, string] {
		c := NewGraphCache[string, string](time.Hour)
		c.PutVerticesWithExpiration([]VertexItem[string, string]{
			{Key: "a", Value: "A", Expiration: live},
			{Key: "b", Value: "B", Expiration: live},
		})
		c.PutEdgesWithExpiration([]EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 2, Expiration: live},
		})
		return c
	}
	danglingKey := EdgeKey[string]{Tail: "a", Head: "b"}

	t.Run("SnapshotGraphHidesDanglingEdge", func(t *testing.T) {
		c := build()
		if !c.DeleteVertex("b") {
			t.Fatal("DeleteVertex(b) reported false")
		}
		got := c.SnapshotGraph()
		if _, ok := vertexByKey(got.Vertices)["b"]; ok {
			t.Errorf("deleted vertex b present in SnapshotGraph: %v", got.Vertices)
		}
		if _, ok := edgeByEndpoints(got.Edges)[danglingKey]; ok {
			t.Errorf("dangling edge a->b present in SnapshotGraph: %v", got.Edges)
		}
		if c.edges.count() != 1 {
			t.Fatalf("edge bucket count = %d, want 1 (edge still physical before sweep)", c.edges.count())
		}
	})

	t.Run("SnapshotEdgesHidesDanglingEdge", func(t *testing.T) {
		c := build()
		if !c.DeleteVertex("b") {
			t.Fatal("DeleteVertex(b) reported false")
		}
		for _, e := range c.SnapshotEdges() {
			if e.Tail == "a" && e.Head == "b" {
				t.Fatalf("SnapshotEdges streamed dangling edge a->b: %+v", e)
			}
		}
	})

	t.Run("ExpiredNotFlushedEndpointHidden", func(t *testing.T) {
		c := build()
		// Make b expired-but-not-flushed without touching the edge bucket.
		c.mu.Lock()
		c.putVertexLocked("b", "B", time.Now().Add(-time.Minute))
		c.mu.Unlock()
		got := c.SnapshotGraph()
		if _, ok := edgeByEndpoints(got.Edges)[danglingKey]; ok {
			t.Errorf("snapshot kept edge to expired-not-flushed endpoint: %v", got.Edges)
		}
	})
}
