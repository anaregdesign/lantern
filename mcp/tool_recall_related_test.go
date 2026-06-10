package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

func TestRecallRelated_RejectsUnknownAlgorithm(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "recall_related", map[string]any{
		"seed":      "x",
		"algorithm": "not-a-real-algorithm",
	})
}

func TestRecallRelated_PropagatesIlluminateOptions(t *testing.T) {
	h := newTestHarness(t)
	h.fake.illuminateFn = func(_ context.Context, _ string, opts ...client.IlluminateOption) (*client.Graph, error) {
		// Five options expected: algorithm + objective + weighting + step + k.
		if len(opts) != 5 {
			t.Errorf("Illuminate option count = %d, want 5", len(opts))
		}
		return &client.Graph{Vertices: map[string]*client.Vertex{"x": {Key: "x"}}}, nil
	}
	h.call(t, "recall_related", map[string]any{
		"seed":      "x",
		"step":      3,
		"k":         5,
		"algorithm": "mst",
		"objective": "max",
		"weighting": "tfidf",
	})
}

func TestRecallRelated_RejectsEmptySeed(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "recall_related", map[string]any{"seed": ""})
}

// TestRecallRelatedDescription_IsProactive guards the recall-before-
// answering framing while keeping the no-refresh invariant (#528).
func TestRecallRelatedDescription_IsProactive(t *testing.T) {
	if !strings.Contains(strings.ToUpper(recallRelatedDescription), "PROACTIVELY") {
		t.Errorf("recallRelatedDescription should tell the agent to recall PROACTIVELY: %q", recallRelatedDescription)
	}
	if !strings.Contains(recallRelatedDescription, "does NOT refresh") {
		t.Errorf("recallRelatedDescription should keep the recall-does-not-refresh invariant: %q", recallRelatedDescription)
	}
}

// TestRecallRelated_DefaultDirectionIsOut confirms the historical
// behaviour is untouched: no direction means a forward Illuminate walk and
// the reverse edge scan is never consulted (#542).
func TestRecallRelated_DefaultDirectionIsOut(t *testing.T) {
	h := newTestHarness(t)
	h.fake.illuminateFn = func(_ context.Context, _ string, _ ...client.IlluminateOption) (*client.Graph, error) {
		return &client.Graph{Vertices: map[string]*client.Vertex{"seed": {Key: "seed"}}}, nil
	}
	res := h.call(t, "recall_related", map[string]any{"seed": "seed"})
	var out recallRelatedOutput
	structuredAs(t, res, &out)
	if out.Direction != "out" {
		t.Fatalf("Direction = %q, want out", out.Direction)
	}
	if h.fake.scanEdgesCalls != 0 {
		t.Fatalf("ScanEdges called %d times for direction=out; want 0", h.fake.scanEdgesCalls)
	}
}

// TestRecallRelated_DirectionInReturnsPredecessors seeds a pure sink (a
// node with only inbound edges) and asserts the reverse pass returns its
// predecessors instead of just the seed. Illuminate must NOT run for a
// pure in walk (#542).
func TestRecallRelated_DirectionInReturnsPredecessors(t *testing.T) {
	h := newTestHarness(t)
	illuminateCalled := false
	h.fake.illuminateFn = func(_ context.Context, _ string, _ ...client.IlluminateOption) (*client.Graph, error) {
		illuminateCalled = true
		return &client.Graph{}, nil
	}
	h.fake.scanEdgesFn = func(_ context.Context, _ ...client.EdgeScanOption) ([]*client.Edge, []byte, error) {
		return []*client.Edge{
			{Tail: "x", Head: "sink", Weight: 0.4},
			{Tail: "y", Head: "sink", Weight: 0.6},
		}, nil, nil
	}
	res := h.call(t, "recall_related", map[string]any{"seed": "sink", "direction": "in"})
	if res.IsError {
		t.Fatalf("IsError = true")
	}
	if illuminateCalled {
		t.Fatalf("Illuminate must not run for direction=in")
	}
	if h.fake.scanEdgesCalls == 0 {
		t.Fatalf("ScanEdges must run for direction=in")
	}
	var out recallRelatedOutput
	structuredAs(t, res, &out)
	if out.Direction != "in" {
		t.Fatalf("Direction = %q, want in", out.Direction)
	}
	if out.Count != 3 {
		t.Fatalf("Count = %d, want 3 (sink + 2 predecessors)", out.Count)
	}
	if out.Neighbors[0].Key != "sink" {
		t.Fatalf("seed must sort first; got %+v", out.Neighbors[0])
	}
	// Highest-weight predecessor first.
	if out.Neighbors[1].Key != "y" || out.Neighbors[1].Weight != 0.6 {
		t.Fatalf("Neighbors[1] = %+v, want y@0.6", out.Neighbors[1])
	}
	if out.Neighbors[2].Key != "x" || out.Neighbors[2].Weight != 0.4 {
		t.Fatalf("Neighbors[2] = %+v, want x@0.4", out.Neighbors[2])
	}
}

// TestRecallRelated_DirectionInFiltersHeadOvermatch documents that the SDK
// head-prefix scan is a prefix match, so the handler keeps only edges
// whose head equals the seed exactly (#542).
func TestRecallRelated_DirectionInFiltersHeadOvermatch(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanEdgesFn = func(_ context.Context, _ ...client.EdgeScanOption) ([]*client.Edge, []byte, error) {
		return []*client.Edge{
			{Tail: "a", Head: "seed", Weight: 0.5},     // exact head — keep
			{Tail: "b", Head: "seedling", Weight: 0.9}, // prefix over-match — drop
		}, nil, nil
	}
	res := h.call(t, "recall_related", map[string]any{"seed": "seed", "direction": "in"})
	var out recallRelatedOutput
	structuredAs(t, res, &out)
	if out.Count != 2 {
		t.Fatalf("Count = %d, want 2 (seed + a only)", out.Count)
	}
	for _, n := range out.Neighbors {
		if n.Key == "b" {
			t.Fatalf("over-matched predecessor b (head=seedling) must be excluded")
		}
	}
}

// TestRecallRelated_DirectionBothUnionsOutAndIn confirms both passes are
// merged: a node reachable forward AND pointing back at the seed gets both
// weight contributions summed and keeps its out-graph payload (#542).
func TestRecallRelated_DirectionBothUnionsOutAndIn(t *testing.T) {
	h := newTestHarness(t)
	h.fake.illuminateFn = func(_ context.Context, _ string, _ ...client.IlluminateOption) (*client.Graph, error) {
		return &client.Graph{
			Vertices: map[string]*client.Vertex{
				"seed": {Key: "seed", Value: &pb.Vertex_String_{String_: "S"}},
				"a":    {Key: "a", Value: &pb.Vertex_String_{String_: "A"}},
			},
			Edges: map[string]map[string]float32{
				"seed": {"a": 0.5},
			},
		}, nil
	}
	h.fake.scanEdgesFn = func(_ context.Context, _ ...client.EdgeScanOption) ([]*client.Edge, []byte, error) {
		return []*client.Edge{
			{Tail: "a", Head: "seed", Weight: 0.2}, // a is also a predecessor
			{Tail: "p", Head: "seed", Weight: 0.3}, // p only inbound
		}, nil, nil
	}
	res := h.call(t, "recall_related", map[string]any{"seed": "seed", "direction": "both"})
	var out recallRelatedOutput
	structuredAs(t, res, &out)
	if out.Direction != "both" {
		t.Fatalf("Direction = %q, want both", out.Direction)
	}
	if out.Count != 3 {
		t.Fatalf("Count = %d, want 3 (seed, a, p)", out.Count)
	}
	if out.Neighbors[0].Key != "seed" {
		t.Fatalf("seed must sort first; got %+v", out.Neighbors[0])
	}
	// a = out 0.5 + reverse 0.2 = 0.7, and keeps its out-graph payload.
	if out.Neighbors[1].Key != "a" || out.Neighbors[1].Weight != 0.7 {
		t.Fatalf("Neighbors[1] = %+v, want a@0.7", out.Neighbors[1])
	}
	if out.Neighbors[1].Value != "A" {
		t.Fatalf("a should keep its out-graph value; got %+v", out.Neighbors[1].Value)
	}
	// p = reverse 0.3, no payload (reverse scan yields edges, not vertices).
	if out.Neighbors[2].Key != "p" || out.Neighbors[2].Weight != 0.3 {
		t.Fatalf("Neighbors[2] = %+v, want p@0.3", out.Neighbors[2])
	}
	if out.Neighbors[2].Value != nil {
		t.Fatalf("predecessor p should have no payload; got %+v", out.Neighbors[2].Value)
	}
}

func TestRecallRelated_RejectsUnknownDirection(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "recall_related", map[string]any{
		"seed":      "x",
		"direction": "sideways",
	})
}

// TestRecallRelated_ReverseScanTruncates verifies the reverse pass is
// bounded: when the scan budget is exhausted before the cursor drains, the
// result is flagged truncated rather than walking unbounded (#542).
func TestRecallRelated_ReverseScanTruncates(t *testing.T) {
	h := newTestHarness(t)
	page := make([]*client.Edge, recallRelatedReverseScanMax)
	for i := range page {
		page[i] = &client.Edge{Tail: "other", Head: "notseed"}
	}
	h.fake.scanEdgesFn = func(_ context.Context, _ ...client.EdgeScanOption) ([]*client.Edge, []byte, error) {
		// Always report more pages remain; the handler must stop on budget.
		return page, []byte("more"), nil
	}
	res := h.call(t, "recall_related", map[string]any{"seed": "seed", "direction": "in"})
	var out recallRelatedOutput
	structuredAs(t, res, &out)
	if !out.Truncated {
		t.Fatalf("Truncated = false; want true once the scan budget is hit")
	}
}

// captureReinforce wires an addEdgeFn that records every reinforcement write
// into a tail->head keyed map and returns the supplied per-call error.
func captureReinforce(f *fakeLantern, err error) map[string]float32 {
	got := make(map[string]float32)
	f.addEdgeFn = func(_ context.Context, tail, head string, weight float32, _ time.Duration) error {
		got[tail+"->"+head] = weight
		return err
	}
	return got
}

// TestRecallRelated_DefaultDoesNotReinforce confirms recall stays read-only
// unless reinforce is set: no AddEdge write happens and the reinforced count
// is zero (#549).
func TestRecallRelated_DefaultDoesNotReinforce(t *testing.T) {
	h := newTestHarness(t)
	h.fake.illuminateFn = func(_ context.Context, _ string, _ ...client.IlluminateOption) (*client.Graph, error) {
		return &client.Graph{
			Vertices: map[string]*client.Vertex{"seed": {Key: "seed"}, "a": {Key: "a"}},
			Edges:    map[string]map[string]float32{"seed": {"a": 0.7}},
		}, nil
	}
	res := h.call(t, "recall_related", map[string]any{"seed": "seed"})
	if res.IsError {
		t.Fatalf("IsError = true")
	}
	if h.fake.addEdgeCalls != 0 {
		t.Fatalf("AddEdge called %d times without reinforce; want 0", h.fake.addEdgeCalls)
	}
	var out recallRelatedOutput
	structuredAs(t, res, &out)
	if out.Reinforced != 0 {
		t.Fatalf("Reinforced = %d, want 0", out.Reinforced)
	}
	if !strings.Contains(contentText(res), "did NOT refresh") {
		t.Fatalf("read-only recall should restate the no-refresh invariant: %q", contentText(res))
	}
}

// TestRecallRelated_ReinforceBumpsTraversedEdgesOut asserts every edge the
// forward walk traverses is reinforced exactly once with the default pulse,
// and an edge that is NOT part of the traversed subgraph is left alone (#549).
func TestRecallRelated_ReinforceBumpsTraversedEdgesOut(t *testing.T) {
	h := newTestHarness(t)
	h.fake.illuminateFn = func(_ context.Context, _ string, _ ...client.IlluminateOption) (*client.Graph, error) {
		return &client.Graph{
			Vertices: map[string]*client.Vertex{
				"seed": {Key: "seed"}, "a": {Key: "a"}, "b": {Key: "b"},
			},
			Edges: map[string]map[string]float32{
				"seed": {"a": 0.7, "b": 0.1},
				"a":    {"b": 0.2},
			},
		}, nil
	}
	got := captureReinforce(h.fake, nil)
	res := h.call(t, "recall_related", map[string]any{"seed": "seed", "reinforce": true})
	if res.IsError {
		t.Fatalf("IsError = true")
	}
	want := map[string]float32{"seed->a": 1, "seed->b": 1, "a->b": 1}
	if len(got) != len(want) {
		t.Fatalf("reinforced edges = %v, want %v", got, want)
	}
	for k, w := range want {
		if got[k] != w {
			t.Fatalf("edge %q reinforced with %v, want %v (all=%v)", k, got[k], w, got)
		}
	}
	if _, bumped := got["seed->z"]; bumped {
		t.Fatalf("untraversed edge seed->z must not be reinforced; got=%v", got)
	}
	var out recallRelatedOutput
	structuredAs(t, res, &out)
	if out.Reinforced != 3 {
		t.Fatalf("Reinforced = %d, want 3", out.Reinforced)
	}
}

// TestRecallRelated_ReinforceCustomWeight asserts reinforce_weight overrides
// the default pulse magnitude on every traversed edge (#549).
func TestRecallRelated_ReinforceCustomWeight(t *testing.T) {
	h := newTestHarness(t)
	h.fake.illuminateFn = func(_ context.Context, _ string, _ ...client.IlluminateOption) (*client.Graph, error) {
		return &client.Graph{
			Vertices: map[string]*client.Vertex{"seed": {Key: "seed"}, "a": {Key: "a"}},
			Edges:    map[string]map[string]float32{"seed": {"a": 0.7}},
		}, nil
	}
	got := captureReinforce(h.fake, nil)
	res := h.call(t, "recall_related", map[string]any{
		"seed": "seed", "reinforce": true, "reinforce_weight": 2.5,
	})
	if res.IsError {
		t.Fatalf("IsError = true")
	}
	if got["seed->a"] != 2.5 {
		t.Fatalf("edge seed->a reinforced with %v, want 2.5 (all=%v)", got["seed->a"], got)
	}
}

// TestRecallRelated_ReinforceInDirectionBumpsPredecessors confirms reinforce
// also strengthens the reverse-walk edges (predecessor -> seed) when
// direction=in (#549).
func TestRecallRelated_ReinforceInDirectionBumpsPredecessors(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanEdgesFn = func(_ context.Context, _ ...client.EdgeScanOption) ([]*client.Edge, []byte, error) {
		return []*client.Edge{
			{Tail: "x", Head: "seed", Weight: 0.4},
			{Tail: "y", Head: "seed", Weight: 0.6},
		}, nil, nil
	}
	got := captureReinforce(h.fake, nil)
	res := h.call(t, "recall_related", map[string]any{
		"seed": "seed", "direction": "in", "reinforce": true,
	})
	if res.IsError {
		t.Fatalf("IsError = true")
	}
	want := map[string]float32{"x->seed": 1, "y->seed": 1}
	if len(got) != len(want) {
		t.Fatalf("reinforced edges = %v, want %v", got, want)
	}
	for k, w := range want {
		if got[k] != w {
			t.Fatalf("edge %q reinforced with %v, want %v (all=%v)", k, got[k], w, got)
		}
	}
	var out recallRelatedOutput
	structuredAs(t, res, &out)
	if out.Reinforced != 2 {
		t.Fatalf("Reinforced = %d, want 2", out.Reinforced)
	}
}

// TestRecallRelated_ReinforceIsBestEffort asserts a failing reinforcement
// write never fails the recall: the neighbours still return and Reinforced
// reflects that nothing landed (#549).
func TestRecallRelated_ReinforceIsBestEffort(t *testing.T) {
	h := newTestHarness(t)
	h.fake.illuminateFn = func(_ context.Context, _ string, _ ...client.IlluminateOption) (*client.Graph, error) {
		return &client.Graph{
			Vertices: map[string]*client.Vertex{
				"seed": {Key: "seed"}, "a": {Key: "a"}, "b": {Key: "b"},
			},
			Edges: map[string]map[string]float32{"seed": {"a": 0.7, "b": 0.1}},
		}, nil
	}
	captureReinforce(h.fake, errors.New("edge store unavailable"))
	res := h.call(t, "recall_related", map[string]any{"seed": "seed", "reinforce": true})
	if res.IsError {
		t.Fatalf("IsError = true; a failed reinforcement must not fail the recall")
	}
	var out recallRelatedOutput
	structuredAs(t, res, &out)
	if out.Count != 3 {
		t.Fatalf("Count = %d, want 3 (seed, a, b)", out.Count)
	}
	if out.Reinforced != 0 {
		t.Fatalf("Reinforced = %d, want 0 (no write landed)", out.Reinforced)
	}
}

// TestRecallRelated_RejectsUnknownReinforceTTL asserts a bad reinforce_ttl
// bucket fails fast with a tool error and never writes (#549).
func TestRecallRelated_RejectsUnknownReinforceTTL(t *testing.T) {
	h := newTestHarness(t)
	h.fake.illuminateFn = func(_ context.Context, _ string, _ ...client.IlluminateOption) (*client.Graph, error) {
		return &client.Graph{Vertices: map[string]*client.Vertex{"seed": {Key: "seed"}}}, nil
	}
	h.callExpectError(t, "recall_related", map[string]any{
		"seed": "seed", "reinforce": true, "reinforce_ttl": "not-a-bucket",
	})
	if h.fake.addEdgeCalls != 0 {
		t.Fatalf("AddEdge called %d times after a bad bucket; want 0", h.fake.addEdgeCalls)
	}
}

// TestRecallRelated_ReinforceTTLControlsHorizon asserts reinforce_ttl selects
// the pulse's decay horizon: a short bucket yields a strictly shorter AddEdge
// TTL than the conversation default, and neither alters the long-lived base
// edge (the store keeps each contribution independently) (#549).
func TestRecallRelated_ReinforceTTLControlsHorizon(t *testing.T) {
	graph := func(_ context.Context, _ string, _ ...client.IlluminateOption) (*client.Graph, error) {
		return &client.Graph{
			Vertices: map[string]*client.Vertex{"seed": {Key: "seed"}, "a": {Key: "a"}},
			Edges:    map[string]map[string]float32{"seed": {"a": 0.7}},
		}, nil
	}

	hDefault := newTestHarness(t)
	hDefault.fake.illuminateFn = graph
	hDefault.call(t, "recall_related", map[string]any{"seed": "seed", "reinforce": true})
	defaultTTL := hDefault.fake.lastEdgeTTL

	hShort := newTestHarness(t)
	hShort.fake.illuminateFn = graph
	hShort.call(t, "recall_related", map[string]any{
		"seed": "seed", "reinforce": true, "reinforce_ttl": "seconds",
	})
	shortTTL := hShort.fake.lastEdgeTTL

	if !(shortTTL < defaultTTL) {
		t.Fatalf("reinforce_ttl=seconds TTL %v should be shorter than default %v", shortTTL, defaultTTL)
	}
}
