package graph

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestGraph_PutVertex(t *testing.T) {
	g := NewGraph[string, int]()
	g.PutVertex("a", 1)
	g.PutVertex("a", 2) // overwrite
	if got, want := g.Vertices["a"], 2; got != want {
		t.Errorf("Vertices[\"a\"] = %d, want %d", got, want)
	}
}

func TestGraph_PutEdge_AutoCreatesVertices(t *testing.T) {
	g := NewGraph[string, int]()
	g.PutEdge("a", "b", 0.5)
	if _, ok := g.Vertices["a"]; !ok {
		t.Errorf("PutEdge should create tail vertex \"a\"")
	}
	if _, ok := g.Vertices["b"]; !ok {
		t.Errorf("PutEdge should create head vertex \"b\"")
	}
	if w := g.Edges["a"]["b"]; w != 0.5 {
		t.Errorf("Edges[a][b] = %v, want 0.5", w)
	}
}

func TestGraph_ConnectedGraph(t *testing.T) {
	g := NewGraph[string, int]()
	g.PutVertex("a", 1)
	g.PutVertex("b", 2)
	g.PutVertex("c", 3)
	g.PutVertex("isolated", 99)
	g.PutEdge("a", "b", 1)
	g.PutEdge("b", "c", 1)

	c := g.ConnectedGraph("a")

	if _, ok := c.Vertices["isolated"]; ok {
		t.Errorf("ConnectedGraph should not include disconnected vertex")
	}
	for _, want := range []string{"a", "b", "c"} {
		if _, ok := c.Vertices[want]; !ok {
			t.Errorf("ConnectedGraph missing %q", want)
		}
	}
}

// TestGraph_ConnectedGraph_DeepChain exercises a path that previously required
// many "rounds" in the old algorithm and now drains through a single FIFO BFS.
// All chain vertices plus a cycle edge must be reached; an unreachable side
// component must be excluded.
func TestGraph_ConnectedGraph_DeepChain(t *testing.T) {
	g := NewGraph[string, int]()
	chain := []string{"v0", "v1", "v2", "v3", "v4", "v5", "v6"}
	for i, v := range chain {
		g.PutVertex(v, i)
	}
	for i := 0; i < len(chain)-1; i++ {
		g.PutEdge(chain[i], chain[i+1], 1)
	}
	// back-edge forming a cycle
	g.PutEdge("v6", "v3", 1)
	// unreachable component
	g.PutEdge("x", "y", 1)

	c := g.ConnectedGraph("v0")

	for _, want := range chain {
		if _, ok := c.Vertices[want]; !ok {
			t.Errorf("missing reachable vertex %q", want)
		}
	}
	for _, dont := range []string{"x", "y"} {
		if _, ok := c.Vertices[dont]; ok {
			t.Errorf("unreachable vertex %q must not be included", dont)
		}
	}
	// cycle edge must be preserved
	if _, ok := c.Edges["v6"]["v3"]; !ok {
		t.Errorf("cycle edge v6->v3 must be preserved")
	}
}

// TestGraph_ConnectedGraph_SelfLoop pins behavior for self-loops: the loop
// edge is preserved and traversal terminates.
func TestGraph_ConnectedGraph_SelfLoop(t *testing.T) {
	g := NewGraph[string, int]()
	g.PutVertex("a", 1)
	g.PutEdge("a", "a", 1)
	g.PutEdge("a", "b", 1)

	c := g.ConnectedGraph("a")
	if _, ok := c.Edges["a"]["a"]; !ok {
		t.Errorf("self-loop a->a must be preserved")
	}
	if _, ok := c.Vertices["b"]; !ok {
		t.Errorf("neighbor b must be reached")
	}
}

func TestGraph_MinimumSpanningTree(t *testing.T) {
	// Triangle: a-b=1, b-c=2, a-c=10. MST picks (a-b) + (b-c) = total 3.
	g := NewGraph[string, int]()
	g.PutEdge("a", "b", 1)
	g.PutEdge("b", "a", 1)
	g.PutEdge("b", "c", 2)
	g.PutEdge("c", "b", 2)
	g.PutEdge("a", "c", 10)
	g.PutEdge("c", "a", 10)

	mst := g.MinimumSpanningTree("a")

	total := float32(0)
	for _, heads := range mst.Edges {
		for _, w := range heads {
			total += w
		}
	}
	if total != 3 {
		t.Errorf("MST total weight = %v, want 3 (edges=%v)", total, mst.Edges)
	}
	if len(mst.Vertices) != 3 {
		t.Errorf("MST vertices = %d, want 3", len(mst.Vertices))
	}
}

// TestGraph_MinimumSpanningTree_Larger exercises Prim on a 5-vertex graph
// with a clear optimum, to guard against the incremental-push refactor.
//
//	a -1- b
//	|  \  |
//	5   2 3
//	|    \|
//	d -1- c
//	      |
//	      4
//	      e
//
// MST edges: a-b(1), a-c(2), c-d (... wait, encode explicitly)
func TestGraph_MinimumSpanningTree_Larger(t *testing.T) {
	g := NewGraph[string, int]()
	add := func(u, v string, w float32) {
		g.PutEdge(u, v, w)
		g.PutEdge(v, u, w)
	}
	add("a", "b", 1)
	add("a", "c", 2)
	add("a", "d", 5)
	add("b", "c", 3)
	add("c", "d", 1)
	add("c", "e", 4)
	add("d", "e", 6)

	mst := g.MinimumSpanningTree("a")

	// Count undirected edges (each appears twice in mst.Edges if both
	// directions present, but Prim only adds one direction).
	total := float32(0)
	edges := 0
	for _, heads := range mst.Edges {
		for _, w := range heads {
			total += w
			edges++
		}
	}
	// Optimal MST: a-b(1) + a-c(2) + c-d(1) + c-e(4) = 8
	if total != 8 {
		t.Errorf("MST total = %v, want 8 (edges=%v)", total, mst.Edges)
	}
	if len(mst.Vertices) != 5 {
		t.Errorf("MST vertices = %d, want 5", len(mst.Vertices))
	}
	if edges != 4 {
		t.Errorf("MST directed-edge count = %d, want 4", edges)
	}
}

func TestGraph_MaximumSpanningTree(t *testing.T) {
	// Same triangle; max spanning tree picks (a-c)=10 + (b-c)=2 = 12 (avoids a-b=1).
	g := NewGraph[string, int]()
	g.PutEdge("a", "b", 1)
	g.PutEdge("b", "a", 1)
	g.PutEdge("b", "c", 2)
	g.PutEdge("c", "b", 2)
	g.PutEdge("a", "c", 10)
	g.PutEdge("c", "a", 10)

	mst := g.MaximumSpanningTree("a")

	total := float32(0)
	for _, heads := range mst.Edges {
		for _, w := range heads {
			total += w
		}
	}
	if total != 12 {
		t.Errorf("Max spanning tree total = %v, want 12 (edges=%v)", total, mst.Edges)
	}
}

func TestGraph_ShortestPathTree(t *testing.T) {
	// a -1- b -1- c, a -10- c. Dijkstra with cost = weight should still visit
	// c via b because (1+1)=2 < 10.
	g := NewGraph[string, int]()
	g.PutEdge("a", "b", 1)
	g.PutEdge("b", "c", 1)
	g.PutEdge("a", "c", 10)

	spt := g.ShortestPathTree("a", func(w float32) float32 { return w })

	if _, ok := spt.Edges["a"]["b"]; !ok {
		t.Errorf("SPT should contain a->b edge, got %v", spt.Edges)
	}
	if _, ok := spt.Edges["b"]["c"]; !ok {
		t.Errorf("SPT should contain b->c edge, got %v", spt.Edges)
	}
	if _, ok := spt.Edges["a"]["c"]; ok {
		t.Errorf("SPT should NOT contain expensive a->c edge, got %v", spt.Edges)
	}
}

func TestGraph_ShortestPathTree_IndirectCheaper(t *testing.T) {
	// 4-vertex graph where the indirect path strictly beats the direct edge,
	// and the indirect path is discovered AFTER the direct edge has already
	// been pushed onto the priority queue. Exercises Dijkstra's relaxation
	// step: dist[b] must be updated from 10 (a->b) to 3 (a->c->d->b), and
	// the reconstructed predecessor of b must be d, not a.
	g := NewGraph[string, int]()
	g.PutEdge("a", "b", 10)
	g.PutEdge("a", "c", 1)
	g.PutEdge("c", "d", 1)
	g.PutEdge("d", "b", 1)

	spt := g.ShortestPathTree("a", func(w float32) float32 { return w })

	if len(spt.Vertices) != 4 {
		t.Errorf("SPT vertices = %d, want 4 (vertices=%v)", len(spt.Vertices), spt.Vertices)
	}
	if _, ok := spt.Edges["a"]["b"]; ok {
		t.Errorf("SPT should NOT contain direct a->b edge (relaxation failed), got %v", spt.Edges)
	}
	if _, ok := spt.Edges["d"]["b"]; !ok {
		t.Errorf("SPT should contain d->b edge as b's shortest predecessor, got %v", spt.Edges)
	}
	if _, ok := spt.Edges["a"]["c"]; !ok {
		t.Errorf("SPT should contain a->c edge, got %v", spt.Edges)
	}
	if _, ok := spt.Edges["c"]["d"]; !ok {
		t.Errorf("SPT should contain c->d edge, got %v", spt.Edges)
	}
	// Resulting tree has exactly 3 directed edges for 4 vertices.
	totalEdges := 0
	for _, m := range spt.Edges {
		totalEdges += len(m)
	}
	if totalEdges != 3 {
		t.Errorf("SPT total edges = %d, want 3 (edges=%v)", totalEdges, spt.Edges)
	}
}

func TestGraph_ShortestPathTreeContext_RejectsInvalidCosts(t *testing.T) {
	base := func() *Graph[string, int] {
		g := NewGraph[string, int]()
		g.PutEdge("a", "b", 1)
		g.PutEdge("b", "a", 1) // A negative cycle must fail rather than requeue forever.
		return g
	}

	for _, tc := range []struct {
		name string
		cost func(float32) float32
	}{
		{"negative cycle", func(float32) float32 { return -1 }},
		{"NaN", func(float32) float32 { return float32(math.NaN()) }},
		{"positive infinity", func(float32) float32 { return float32(math.Inf(1)) }},
		{"distance overflow", func(float32) float32 { return math.MaxFloat32 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := base().ShortestPathTreeContext(context.Background(), "a", tc.cost)
			if !errors.Is(err, ErrInvalidShortestPathCost) {
				t.Fatalf("errors.Is(err, ErrInvalidShortestPathCost) = false; err = %v", err)
			}
			var invalid *InvalidShortestPathCostError
			if !errors.As(err, &invalid) {
				t.Fatalf("errors.As(err, *InvalidShortestPathCostError) = false; err = %v", err)
			}
		})
	}
}

func TestGraph_ContextCancelled(t *testing.T) {
	g := NewGraph[string, int]()
	g.PutEdge("a", "b", 1.0)
	g.PutEdge("b", "c", 1.0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name string
		run  func() error
	}{
		{"ConnectedGraphContext", func() error {
			_, err := g.ConnectedGraphContext(ctx, "a")
			return err
		}},
		{"MinimumSpanningTreeContext", func() error {
			_, err := g.MinimumSpanningTreeContext(ctx, "a")
			return err
		}},
		{"ShortestPathTreeContext", func() error {
			_, err := g.ShortestPathTreeContext(ctx, "a", func(w float32) float32 { return w })
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("want context.Canceled, got %v", err)
			}
		})
	}
}
