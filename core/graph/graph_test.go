package graph

import (
	"sort"
	"strconv"
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

func TestGraph_Render(t *testing.T) {
	g := NewGraph[string, int]()
	g.PutVertex("a", 1)
	g.PutVertex("b", 2)
	g.PutEdge("a", "b", 0.5)

	keyToID := func(k string) int { return int(k[0]) }
	valToStr := func(v int) string { return strconv.Itoa(v) }

	view := g.Render(keyToID, valToStr)
	if len(view.Vertices) != 2 {
		t.Errorf("Render vertices = %d, want 2", len(view.Vertices))
	}
	if len(view.Edges) != 1 {
		t.Errorf("Render edges = %d, want 1", len(view.Edges))
	}

	labels := make([]string, 0, len(view.Vertices))
	for _, v := range view.Vertices {
		labels = append(labels, v.Label)
	}
	sort.Strings(labels)
	if labels[0] != "1" || labels[1] != "2" {
		t.Errorf("Render labels = %v, want [1 2]", labels)
	}

	e := view.Edges[0]
	if e.From != int('a') || e.To != int('b') || e.Value != 0.5 {
		t.Errorf("Render edge = %+v, want From=%d To=%d Value=0.5", e, int('a'), int('b'))
	}
}
