package client

import (
	"sort"
	"strconv"
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestGraph_Render(t *testing.T) {
	g := NewGraph()
	g.Vertices["a"] = &pb.Vertex{Value: &pb.Vertex_Int32{Int32: 1}}
	g.Vertices["b"] = &pb.Vertex{Value: &pb.Vertex_Int32{Int32: 2}}
	g.Edges["a"] = map[string]float32{"b": 0.5}

	keyToID := func(k string) int { return int(k[0]) }
	valToStr := func(v *Vertex) string {
		n, err := IntValue(v)
		if err != nil {
			t.Fatalf("IntValue: %v", err)
		}
		return strconv.Itoa(n)
	}

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
