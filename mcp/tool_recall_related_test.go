package mcp

import (
	"context"
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestRecallRelated_FlatNeighborsRanked(t *testing.T) {
	h := newTestHarness(t)
	h.fake.illuminateFn = func(_ context.Context, seed string, _ ...client.IlluminateOption) (*client.Graph, error) {
		return &client.Graph{
			Vertices: map[string]*client.Vertex{
				"seed": {Key: "seed", Value: &pb.Vertex_String_{String_: "S"}},
				"a":    {Key: "a", Value: &pb.Vertex_String_{String_: "A"}},
				"b":    {Key: "b", Value: &pb.Vertex_String_{String_: "B"}},
			},
			Edges: map[string]map[string]float32{
				"seed": {"a": 0.7, "b": 0.1},
				"a":    {"b": 0.2},
			},
		}, nil
	}
	res := h.call(t, "recall_related", map[string]any{"seed": "seed"})
	if res.IsError {
		t.Fatalf("IsError = true")
	}
	var out recallRelatedOutput
	structuredAs(t, res, &out)
	if out.Seed != "seed" {
		t.Fatalf("Seed = %q", out.Seed)
	}
	if out.Count != 3 {
		t.Fatalf("Count = %d, want 3", out.Count)
	}
	// Seed always first; remaining sorted by descending weight.
	if out.Neighbors[0].Key != "seed" {
		t.Fatalf("seed must be first; got %+v", out.Neighbors[0])
	}
	if out.Neighbors[1].Key != "a" {
		t.Fatalf("highest-weight neighbour should be \"a\"; got %+v", out.Neighbors[1])
	}
	// "a" has incoming weight 0.7 from seed.
	if out.Neighbors[1].Weight != 0.7 {
		t.Fatalf("a weight = %v, want 0.7", out.Neighbors[1].Weight)
	}
	// "b" cumulative incoming = 0.1 (from seed) + 0.2 (from a) = 0.3.
	if out.Neighbors[2].Weight != 0.3 {
		t.Fatalf("b weight = %v, want 0.3", out.Neighbors[2].Weight)
	}
}

func TestRecallRelated_RejectsUnknownObjective(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "recall_related", map[string]any{
		"seed":      "x",
		"objective": "not-a-real-objective",
	})
}

func TestRecallRelated_PropagatesIlluminateOptions(t *testing.T) {
	h := newTestHarness(t)
	h.fake.illuminateFn = func(_ context.Context, _ string, opts ...client.IlluminateOption) (*client.Graph, error) {
		// Three options expected: optimization + step + k.
		if len(opts) != 3 {
			t.Errorf("Illuminate option count = %d, want 3", len(opts))
		}
		return &client.Graph{Vertices: map[string]*client.Vertex{"x": {Key: "x"}}}, nil
	}
	h.call(t, "recall_related", map[string]any{
		"seed":      "x",
		"step":      3,
		"k":         5,
		"objective": "mst",
	})
}

func TestRecallRelated_RejectsEmptySeed(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "recall_related", map[string]any{"seed": ""})
}
