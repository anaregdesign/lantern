package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/cache/graph"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func newTestService(t *testing.T) *LanternService {
	t.Helper()
	return NewLanternService(graph.NewGraphCache[string, *pb.Vertex](time.Minute))
}

func futureTs(d time.Duration) *timestamppb.Timestamp {
	return timestamppb.New(time.Now().Add(d))
}

func TestLanternService_PutAndGetVertex(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	v := &pb.Vertex{
		Key:        "k1",
		Value:      &pb.Vertex_String_{String_: "hello"},
		Expiration: futureTs(time.Minute),
	}
	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{v}}); err != nil {
		t.Fatalf("PutVertex: %v", err)
	}

	resp, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: "k1"})
	if err != nil {
		t.Fatalf("GetVertex: %v", err)
	}
	if got := resp.Vertex.GetString_(); got != "hello" {
		t.Errorf("GetVertex value = %q, want \"hello\"", got)
	}
}

func TestLanternService_GetVertex_NotFound(t *testing.T) {
	s := newTestService(t)
	_, err := s.GetVertex(context.Background(), &pb.GetVertexRequest{Key: "missing"})
	if err == nil {
		t.Fatal("expected error for missing vertex, got nil")
	}
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Errorf("connect code = %v, want CodeNotFound", code)
	}
}

func TestLanternService_DeleteVertex(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	v := &pb.Vertex{Key: "x", Value: &pb.Vertex_Int64{Int64: 1}, Expiration: futureTs(time.Minute)}
	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{v}}); err != nil {
		t.Fatalf("PutVertex: %v", err)
	}
	dResp, err := s.DeleteVertex(ctx, &pb.DeleteVertexRequest{Key: "x"})
	if err != nil {
		t.Fatalf("DeleteVertex: %v", err)
	}
	if !dResp.GetExisted() {
		t.Errorf("DeleteVertex.Existed = false, want true (vertex was present)")
	}
	if _, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: "x"}); err == nil {
		t.Error("expected NotFound after delete, got nil error")
	}
	// Second delete of the same key must report existed=false.
	dResp2, err := s.DeleteVertex(ctx, &pb.DeleteVertexRequest{Key: "x"})
	if err != nil {
		t.Fatalf("DeleteVertex(repeat): %v", err)
	}
	if dResp2.GetExisted() {
		t.Errorf("DeleteVertex.Existed = true on second call, want false")
	}
}

func TestLanternService_AddAndGetEdge(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	edge := &pb.Edge{Tail: "a", Head: "b", Weight: 1.25, Expiration: futureTs(time.Minute)}
	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{edge}}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	resp, err := s.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"})
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if resp.Edge.Weight != 1.25 {
		t.Errorf("Weight = %v, want 1.25", resp.Edge.Weight)
	}
}

func TestLanternService_GetEdge_NotFound(t *testing.T) {
	s := newTestService(t)
	_, err := s.GetEdge(context.Background(), &pb.GetEdgeRequest{Tail: "no", Head: "such"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Errorf("code = %v, want CodeNotFound", code)
	}
}

func TestLanternService_AddEdge_Additive(t *testing.T) {
	// AddEdge is additive: two adds of weight 2 each should accumulate to 4.
	s := newTestService(t)
	ctx := context.Background()
	e := &pb.Edge{Tail: "a", Head: "b", Weight: 2, Expiration: futureTs(time.Minute)}
	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{e}}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{e}}); err != nil {
		t.Fatalf("AddEdge2: %v", err)
	}
	resp, err := s.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"})
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if resp.Edge.Weight != 4 {
		t.Errorf("Weight = %v, want 4 (additive)", resp.Edge.Weight)
	}
}

func TestLanternService_PutEdge_Replaces(t *testing.T) {
	// PutEdge deletes-then-adds: repeated calls with the same weight stay at that weight.
	s := newTestService(t)
	ctx := context.Background()
	e := &pb.Edge{Tail: "a", Head: "b", Weight: 7, Expiration: futureTs(time.Minute)}
	if _, err := s.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{e}}); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
	if _, err := s.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{e}}); err != nil {
		t.Fatalf("PutEdge2: %v", err)
	}
	resp, err := s.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"})
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if resp.Edge.Weight != 7 {
		t.Errorf("Weight = %v, want 7 (replaced, not accumulated)", resp.Edge.Weight)
	}
}

func TestLanternService_DeleteEdge(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	e := &pb.Edge{Tail: "a", Head: "b", Weight: 1, Expiration: futureTs(time.Minute)}
	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{e}}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	dResp, err := s.DeleteEdge(ctx, &pb.DeleteEdgeRequest{Tail: "a", Head: "b"})
	if err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}
	if !dResp.GetExisted() {
		t.Errorf("DeleteEdge.Existed = false, want true (edge was present)")
	}
	if _, err := s.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"}); err == nil {
		t.Error("expected NotFound after delete")
	}
	// Second delete reports existed=false.
	dResp2, err := s.DeleteEdge(ctx, &pb.DeleteEdgeRequest{Tail: "a", Head: "b"})
	if err != nil {
		t.Fatalf("DeleteEdge(repeat): %v", err)
	}
	if dResp2.GetExisted() {
		t.Errorf("DeleteEdge.Existed = true on second call, want false")
	}
}

func seedTriangle(t *testing.T, s *LanternService) {
	t.Helper()
	ctx := context.Background()
	exp := futureTs(time.Minute)
	verts := []*pb.Vertex{
		{Key: "a", Value: &pb.Vertex_String_{String_: "A"}, Expiration: exp},
		{Key: "b", Value: &pb.Vertex_String_{String_: "B"}, Expiration: exp},
		{Key: "c", Value: &pb.Vertex_String_{String_: "C"}, Expiration: exp},
	}
	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: verts}); err != nil {
		t.Fatalf("PutVertex: %v", err)
	}
	edges := []*pb.Edge{
		{Tail: "a", Head: "b", Weight: 1, Expiration: exp},
		{Tail: "b", Head: "a", Weight: 1, Expiration: exp},
		{Tail: "b", Head: "c", Weight: 2, Expiration: exp},
		{Tail: "c", Head: "b", Weight: 2, Expiration: exp},
		{Tail: "a", Head: "c", Weight: 10, Expiration: exp},
		{Tail: "c", Head: "a", Weight: 10, Expiration: exp},
	}
	if _, err := s.PutEdges(ctx, &pb.PutEdgesRequest{Edges: edges}); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
}

func TestLanternService_Illuminate_NoAlgorithm(t *testing.T) {
	s := newTestService(t)
	seedTriangle(t, s)

	resp, err := s.Illuminate(context.Background(), &pb.IlluminateRequest{
		Seed:      "a",
		Step:      3,
		K:         10,
		Algorithm: pb.Algorithm_ALGORITHM_UNSPECIFIED,
	})
	if err != nil {
		t.Fatalf("Illuminate: %v", err)
	}
	if len(resp.Graph.Vertices) != 3 {
		t.Errorf("vertices = %d, want 3", len(resp.Graph.Vertices))
	}
}

// TestLanternService_Illuminate_AllAxisCombos exercises the orthogonal
// axes introduced in #410: every algorithm × objective × weighting tuple
// must run to completion and return at least one vertex against the
// triangle seed. The (UNSPECIFIED algorithm, UNSPECIFIED objective)
// combos are covered separately by the _NoAlgorithm test above.
func TestLanternService_Illuminate_AllAxisCombos(t *testing.T) {
	algorithms := []pb.Algorithm{
		pb.Algorithm_ALGORITHM_MINIMUM_SPANNING_TREE,
		pb.Algorithm_ALGORITHM_SHORTEST_PATH_TREE,
	}
	objectives := []pb.Objective{
		pb.Objective_OBJECTIVE_MINIMIZE,
		pb.Objective_OBJECTIVE_MAXIMIZE,
	}
	weightings := []pb.Weighting{
		pb.Weighting_WEIGHTING_RAW,
		pb.Weighting_WEIGHTING_TFIDF,
	}
	for _, algo := range algorithms {
		for _, obj := range objectives {
			for _, w := range weightings {
				name := algo.String() + "/" + obj.String() + "/" + w.String()
				t.Run(name, func(t *testing.T) {
					s := newTestService(t)
					seedTriangle(t, s)
					resp, err := s.Illuminate(context.Background(), &pb.IlluminateRequest{
						Seed:      "a",
						Step:      3,
						K:         10,
						Algorithm: algo,
						Objective: obj,
						Weighting: w,
					})
					if err != nil {
						t.Fatalf("Illuminate(%s): %v", name, err)
					}
					if len(resp.Graph.Vertices) == 0 {
						t.Errorf("Illuminate(%s) returned empty vertices", name)
					}
				})
			}
		}
	}
}

func TestLanternService_GetEdge_ReturnsExpiration(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	exp := futureTs(2 * time.Minute)
	e := &pb.Edge{Tail: "a", Head: "b", Weight: 1, Expiration: exp}
	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{e}}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	resp, err := s.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"})
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if resp.Edge.Expiration == nil {
		t.Fatal("Expiration is nil; expected propagated TTL")
	}
	got := resp.Edge.Expiration.AsTime()
	want := exp.AsTime()
	if delta := got.Sub(want); delta > time.Second || delta < -time.Second {
		t.Errorf("Expiration = %v, want ~%v (delta=%v)", got, want, delta)
	}
}

func TestLanternService_WriteResponses_ReportCounts(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	exp := futureTs(time.Minute)

	vresp, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "a", Value: &pb.Vertex_Int64{Int64: 1}, Expiration: exp},
		{Key: "b", Value: &pb.Vertex_Int64{Int64: 2}, Expiration: exp},
	}})
	if err != nil {
		t.Fatalf("PutVertex: %v", err)
	}
	if vresp.Written != 2 {
		t.Errorf("PutVertex Written = %d, want 2", vresp.Written)
	}

	aresp, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{
		{Tail: "a", Head: "b", Weight: 1, Expiration: exp},
	}})
	if err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if aresp.Written != 1 {
		t.Errorf("AddEdge Written = %d, want 1", aresp.Written)
	}

	presp, err := s.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{
		{Tail: "a", Head: "b", Weight: 3, Expiration: exp},
	}})
	if err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
	if presp.Written != 1 {
		t.Errorf("PutEdge Written = %d, want 1", presp.Written)
	}
}

func TestLanternService_RespectsContextCancel(t *testing.T) {
	s := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: "x"}); err == nil {
		t.Fatal("expected ctx cancel error, got nil")
	} else if code := connect.CodeOf(err); code != connect.CodeCanceled {
		t.Errorf("code = %v, want CodeCanceled", code)
	}
}

// TestLanternService_SingularWriteFacades verifies that PutVertex / AddEdge /
// PutEdge behave like single-element calls to their plural counterparts.
func TestLanternService_SingularWriteFacades(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	exp := futureTs(time.Minute)

	// PutVertex writes a single vertex visible via GetVertex.
	if _, err := s.PutVertex(ctx, &pb.PutVertexRequest{
		Vertex: &pb.Vertex{Key: "k", Value: &pb.Vertex_String_{String_: "v"}, Expiration: exp},
	}); err != nil {
		t.Fatalf("PutVertex: %v", err)
	}
	if gv, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: "k"}); err != nil {
		t.Fatalf("GetVertex: %v", err)
	} else if got := gv.GetVertex().GetString_(); got != "v" {
		t.Errorf("vertex value = %q, want %q", got, "v")
	}

	// AddEdge accumulates weight just like AddEdges.
	for range 2 {
		if _, err := s.AddEdge(ctx, &pb.AddEdgeRequest{
			Edge: &pb.Edge{Tail: "a", Head: "b", Weight: 1.5, Expiration: exp},
		}); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
	}
	if ge, err := s.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"}); err != nil {
		t.Fatalf("GetEdge: %v", err)
	} else if ge.GetEdge().GetWeight() != 3 {
		t.Errorf("weight = %v, want 3 (additive)", ge.GetEdge().GetWeight())
	}

	// PutEdge replaces weight (idempotent) instead of accumulating.
	if _, err := s.PutEdge(ctx, &pb.PutEdgeRequest{
		Edge: &pb.Edge{Tail: "a", Head: "b", Weight: 9, Expiration: exp},
	}); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
	if _, err := s.PutEdge(ctx, &pb.PutEdgeRequest{
		Edge: &pb.Edge{Tail: "a", Head: "b", Weight: 9, Expiration: exp},
	}); err != nil {
		t.Fatalf("PutEdge(repeat): %v", err)
	}
	if ge, err := s.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"}); err != nil {
		t.Fatalf("GetEdge: %v", err)
	} else if ge.GetEdge().GetWeight() != 9 {
		t.Errorf("weight = %v, want 9 (replaced)", ge.GetEdge().GetWeight())
	}
}

// When WithTombstoneTTL is set, RPCs that accept a per-entry Expiration
// must reject values that exceed the clamp with codes.InvalidArgument and
// an error message that names LANTERN_TOMBSTONE_TTL (so operators can
// trace it back to the env knob).
func TestExpirationClamp_RejectsBeyondTTL(t *testing.T) {
	const ttl = time.Hour
	s := NewLanternService(graph.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithTombstoneTTL(ttl)
	ctx := context.Background()

	tooFar := timestamppb.New(time.Now().Add(2 * ttl))

	cases := []struct {
		name string
		call func() error
	}{
		{"PutVertices", func() error {
			_, err := s.PutVertices(ctx, &pb.PutVerticesRequest{
				Vertices: []*pb.Vertex{{Key: "v", Value: &pb.Vertex_Nil{Nil: true}, Expiration: tooFar}},
			})
			return err
		}},
		{"AddEdges", func() error {
			_, err := s.AddEdges(ctx, &pb.AddEdgesRequest{
				Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 1, Expiration: tooFar}},
			})
			return err
		}},
		{"PutEdges", func() error {
			_, err := s.PutEdges(ctx, &pb.PutEdgesRequest{
				Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 1, Expiration: tooFar}},
			})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatalf("want error, got nil")
			}
			if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
				t.Fatalf("code: got %v, want CodeInvalidArgument", code)
			}
			if !strings.Contains(err.Error(), "LANTERN_TOMBSTONE_TTL") {
				t.Errorf("message should reference LANTERN_TOMBSTONE_TTL; got %q", err.Error())
			}
		})
	}
}

// Within the clamp, the same RPCs succeed.
func TestExpirationClamp_AcceptsWithinTTL(t *testing.T) {
	const ttl = time.Hour
	s := NewLanternService(graph.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithTombstoneTTL(ttl)
	ctx := context.Background()

	ok := timestamppb.New(time.Now().Add(ttl / 2))

	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{{Key: "v", Value: &pb.Vertex_Nil{Nil: true}, Expiration: ok}},
	}); err != nil {
		t.Fatalf("PutVertices within TTL: %v", err)
	}
	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{
		Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 1, Expiration: ok}},
	}); err != nil {
		t.Fatalf("AddEdges within TTL: %v", err)
	}
}

// Zero expiration (= no expiration) is always accepted regardless of TTL.
func TestExpirationClamp_ZeroAlwaysAllowed(t *testing.T) {
	s := NewLanternService(graph.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithTombstoneTTL(time.Minute)
	ctx := context.Background()
	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{{Key: "v", Value: &pb.Vertex_Nil{Nil: true}}},
	}); err != nil {
		t.Fatalf("PutVertices with zero expiration: %v", err)
	}
}

func TestLanternService_FakeBackend_PutGetDelete(t *testing.T) {
	fb := newFakeBackend()
	svc := NewLanternService(fb)
	ctx := context.Background()

	v := &pb.Vertex{Key: "a", Value: &pb.Vertex_String_{String_: "alpha"}}
	if _, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{v}}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
	if fb.putVerticesCalls != 1 {
		t.Errorf("putVerticesCalls = %d, want 1", fb.putVerticesCalls)
	}

	resp, err := svc.GetVertex(ctx, &pb.GetVertexRequest{Key: "a"})
	if err != nil {
		t.Fatalf("GetVertex: %v", err)
	}
	if got := resp.Vertex.GetString_(); got != "alpha" {
		t.Errorf("value = %q, want \"alpha\"", got)
	}

	if _, err := svc.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: []string{"a"}}); err != nil {
		t.Fatalf("DeleteVertices: %v", err)
	}
	if fb.deleteVertices != 1 {
		t.Errorf("deleteVertices = %d, want 1", fb.deleteVertices)
	}
}

func TestLanternService_FakeBackend_Illuminate_PropagatesError(t *testing.T) {
	fb := newFakeBackend()
	fb.neighborErr = errors.New("simulated cache failure")
	svc := NewLanternService(fb)

	_, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{Seed: "a", Step: 1, K: 1})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// A bare non-context error from the backend passes through to the
	// service handler unwrapped; connect.CodeOf reports CodeUnknown.
	if code := connect.CodeOf(err); code != connect.CodeUnknown {
		t.Errorf("connect code = %v, want CodeUnknown", code)
	}
}

// TestLanternService_FakeBackend_Illuminate_ObjectiveSteersPruning pins the
// #560 contract at the service boundary: the Illuminate handler must translate
// the request Objective into the per-hop pruning direction it hands the
// backend. MINIMIZE asks for the cheapest edges (selectSmallest=true); MAXIMIZE
// and the UNSPECIFIED default both ask for the strongest (selectSmallest=false),
// preserving the historical strongest-neighbour behaviour.
func TestLanternService_FakeBackend_Illuminate_ObjectiveSteersPruning(t *testing.T) {
	tests := []struct {
		name            string
		objective       pb.Objective
		wantSelectSmall bool
	}{
		{
			name:            "MINIMIZE prunes to smallest",
			objective:       pb.Objective_OBJECTIVE_MINIMIZE,
			wantSelectSmall: true,
		},
		{
			name:            "MAXIMIZE prunes to largest",
			objective:       pb.Objective_OBJECTIVE_MAXIMIZE,
			wantSelectSmall: false,
		},
		{
			name:            "UNSPECIFIED defaults to largest (#560)",
			objective:       pb.Objective_OBJECTIVE_UNSPECIFIED,
			wantSelectSmall: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fb := newFakeBackend()
			svc := NewLanternService(fb)
			if _, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{
				Seed:      "a",
				Step:      1,
				K:         3,
				Objective: tt.objective,
			}); err != nil {
				t.Fatalf("Illuminate: %v", err)
			}
			if fb.neighborCalls != 1 {
				t.Fatalf("neighborCalls = %d, want 1", fb.neighborCalls)
			}
			if fb.lastNeighborSelectSmall != tt.wantSelectSmall {
				t.Errorf("lastNeighborSelectSmall = %v, want %v",
					fb.lastNeighborSelectSmall, tt.wantSelectSmall)
			}
		})
	}
}

// seedStar builds a directed star: seed "s" fans out to h1..h5 with distinct
// weights 1..5 (and nothing else), so a per-hop prune at k < 5 must choose
// which heads survive. Used by the #560 min-vs-max-differ end-to-end test.
func seedStar(t *testing.T, s *LanternService) {
	t.Helper()
	ctx := context.Background()
	exp := futureTs(time.Minute)
	verts := []*pb.Vertex{
		{Key: "s", Value: &pb.Vertex_String_{String_: "S"}, Expiration: exp},
		{Key: "h1", Value: &pb.Vertex_String_{String_: "H1"}, Expiration: exp},
		{Key: "h2", Value: &pb.Vertex_String_{String_: "H2"}, Expiration: exp},
		{Key: "h3", Value: &pb.Vertex_String_{String_: "H3"}, Expiration: exp},
		{Key: "h4", Value: &pb.Vertex_String_{String_: "H4"}, Expiration: exp},
		{Key: "h5", Value: &pb.Vertex_String_{String_: "H5"}, Expiration: exp},
	}
	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: verts}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
	edges := []*pb.Edge{
		{Tail: "s", Head: "h1", Weight: 1, Expiration: exp},
		{Tail: "s", Head: "h2", Weight: 2, Expiration: exp},
		{Tail: "s", Head: "h3", Weight: 3, Expiration: exp},
		{Tail: "s", Head: "h4", Weight: 4, Expiration: exp},
		{Tail: "s", Head: "h5", Weight: 5, Expiration: exp},
	}
	if _, err := s.PutEdges(ctx, &pb.PutEdgesRequest{Edges: edges}); err != nil {
		t.Fatalf("PutEdges: %v", err)
	}
}

// TestLanternService_Illuminate_ObjectiveSelectsPrunedNeighbors is the #560
// end-to-end regression through the real GraphCache: against a star where k
// binds (out-degree 5, k=2), the surviving per-hop neighbours must differ by
// Objective. MAXIMIZE (and the UNSPECIFIED default) keep the two STRONGEST
// heads; MINIMIZE keeps the two weakest. Before #560 the prune always kept the
// strongest two regardless of Objective, so the MINIMIZE row would have
// (wrongly) returned {h4, h5}.
func TestLanternService_Illuminate_ObjectiveSelectsPrunedNeighbors(t *testing.T) {
	tests := []struct {
		name      string
		objective pb.Objective
		wantHeads []string
	}{
		{
			name:      "MAXIMIZE keeps the strongest k",
			objective: pb.Objective_OBJECTIVE_MAXIMIZE,
			wantHeads: []string{"h4", "h5"},
		},
		{
			name:      "UNSPECIFIED defaults to strongest k (#560)",
			objective: pb.Objective_OBJECTIVE_UNSPECIFIED,
			wantHeads: []string{"h4", "h5"},
		},
		{
			name:      "MINIMIZE keeps the weakest k",
			objective: pb.Objective_OBJECTIVE_MINIMIZE,
			wantHeads: []string{"h1", "h2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService(t)
			seedStar(t, s)
			resp, err := s.Illuminate(context.Background(), &pb.IlluminateRequest{
				Seed:      "s",
				Step:      1,
				K:         2,
				Objective: tt.objective,
			})
			if err != nil {
				t.Fatalf("Illuminate: %v", err)
			}
			got := map[string]struct{}{}
			for _, v := range resp.Graph.Vertices {
				got[v.GetKey()] = struct{}{}
			}
			want := append([]string{"s"}, tt.wantHeads...)
			if len(got) != len(want) {
				t.Fatalf("vertex count = %d (%v), want %d (%v)", len(got), got, len(want), want)
			}
			for _, k := range want {
				if _, ok := got[k]; !ok {
					t.Errorf("missing vertex %q; got %v, want %v", k, got, want)
				}
			}
		})
	}
}

func TestLanternService_FakeBackend_PutAndDeleteEdge(t *testing.T) {
	fb := newFakeBackend()
	svc := NewLanternService(fb)
	ctx := context.Background()

	if _, err := svc.PutEdge(ctx, &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "t", Head: "h", Weight: 2.5}}); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
	if fb.putEdgesCalls != 1 {
		t.Errorf("putEdgesCalls = %d, want 1", fb.putEdgesCalls)
	}

	resp, err := svc.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "t", Head: "h"})
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if resp.Edge.Weight != 2.5 {
		t.Errorf("weight = %v, want 2.5", resp.Edge.Weight)
	}

	del, err := svc.DeleteEdge(ctx, &pb.DeleteEdgeRequest{Tail: "t", Head: "h"})
	if err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}
	if !del.Existed {
		t.Error("Existed = false, want true")
	}
}

// TestLanternService_MutationLog_BurstAppendsMonotone exercises the wire-in
// from issue #179: a burst of N plural-write RPCs must produce N entries on
// the in-memory mutation log with strictly monotonic Seq starting at 1, and
// the onAppend metric callback must fire exactly once per append.
func TestLanternService_MutationLog_BurstAppendsMonotone(t *testing.T) {
	const N = 100

	log := mutationlog.New(mutationlog.Options{Capacity: 2 * N, SubscriberBuffer: 2 * N})
	t.Cleanup(func() { _ = log.Close() })

	clock := hlc.New(hlc.NodeID{0x11, 0x22, 0x33, 0x44}, hlc.Options{})

	var appendCount int
	s := NewLanternService(graph.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithReplication(log, clock, func() { appendCount++ })

	ch, cancel, err := log.Subscribe(0)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = cancel() })

	for i := 0; i < N; i++ {
		_, err := s.PutVertices(context.Background(), &pb.PutVerticesRequest{
			Vertices: []*pb.Vertex{{Key: keyFor(i)}},
		})
		if err != nil {
			t.Fatalf("PutVertices[%d] error: %v", i, err)
		}
	}

	if appendCount != N {
		t.Fatalf("onAppend count = %d, want %d", appendCount, N)
	}
	first, ok1 := log.FirstSeq()
	last, ok2 := log.LastSeq()
	if !ok1 || !ok2 {
		t.Fatalf("log empty after %d appends (first ok=%v, last ok=%v)", N, ok1, ok2)
	}
	if got := last - first + 1; int(got) != N {
		t.Fatalf("log length = %d, want %d (first=%d last=%d)", got, N, first, last)
	}

	prev := uint64(0)
	for i := 0; i < N; i++ {
		select {
		case e := <-ch:
			if e.Seq != prev+1 {
				t.Fatalf("entry[%d] seq = %d, want %d", i, e.Seq, prev+1)
			}
			prev = e.Seq
			mu, ok := e.Op.(*pb.Mutation)
			if !ok {
				t.Fatalf("entry[%d] op type = %T, want *pb.Mutation", i, e.Op)
			}
			if mu.GetOp().GetPutVertices() == nil {
				t.Fatalf("entry[%d] missing PutVertices oneof", i)
			}
			if len(mu.GetOrigin()) != len(hlc.NodeID{}) {
				t.Fatalf("entry[%d] origin len = %d, want %d",
					i, len(mu.GetOrigin()), len(hlc.NodeID{}))
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for entry %d", i)
		}
	}
}

// TestLanternService_MutationLog_NotWired_NoOp guards the test path where
// the service is built without WithReplication: write RPCs must still
// succeed and not panic on the nil log/clock.
func TestLanternService_MutationLog_NotWired_NoOp(t *testing.T) {
	s := NewLanternService(graph.NewGraphCache[string, *pb.Vertex](time.Minute))
	if _, err := s.PutVertices(context.Background(), &pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{{Key: "k"}},
	}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
}

func keyFor(i int) string {
	return "k-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

// fakeHotPathMetrics captures one observation per RPC so tests can assert
// the service-layer instrumentation hooks fire on the right callbacks.
type fakeHotPathMetrics struct {
	illuminate []illuminateObs
	scan       []scanObs
	batch      []batchObs
	getVertex  []hitMissObs
	getEdge    []hitMissObs
}

type illuminateObs struct {
	algorithm, objective, weighting string
	visitedVertices, visitedEdges   int
	traversal, optimize             time.Duration
}

type scanObs struct {
	op       string
	results  int
	duration time.Duration
}

type batchObs struct {
	op   string
	size int
}

type hitMissObs struct {
	hits   int
	misses int
}

func (f *fakeHotPathMetrics) OnIlluminate(algorithm, objective, weighting string, vV, vE int, traversal, optimize time.Duration) {
	f.illuminate = append(f.illuminate, illuminateObs{algorithm, objective, weighting, vV, vE, traversal, optimize})
}
func (f *fakeHotPathMetrics) OnScan(op string, results int, d time.Duration) {
	f.scan = append(f.scan, scanObs{op, results, d})
}
func (f *fakeHotPathMetrics) OnBatch(op string, size int) {
	f.batch = append(f.batch, batchObs{op, size})
}
func (f *fakeHotPathMetrics) OnGetVertices(hits, misses int) {
	f.getVertex = append(f.getVertex, hitMissObs{hits, misses})
}
func (f *fakeHotPathMetrics) OnGetEdges(hits, misses int) {
	f.getEdge = append(f.getEdge, hitMissObs{hits, misses})
}

func TestLanternService_HotPathMetrics_EmitsOnceForBatchAndIlluminate(t *testing.T) {
	fm := &fakeHotPathMetrics{}
	s := NewLanternService(graph.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithHotPathMetrics(fm)

	ctx := context.Background()

	// PutVertices → one batch observation.
	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "a", Value: &pb.Vertex_Int64{Int64: 1}, Expiration: futureTs(time.Minute)},
		{Key: "b", Value: &pb.Vertex_Int64{Int64: 2}, Expiration: futureTs(time.Minute)},
	}}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
	// AddEdges → one batch observation.
	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{
		{Tail: "a", Head: "b", Weight: 1, Expiration: futureTs(time.Minute)},
	}}); err != nil {
		t.Fatalf("AddEdges: %v", err)
	}

	if len(fm.batch) != 2 {
		t.Fatalf("batch observations = %d, want 2 (PutVertices, AddEdges)", len(fm.batch))
	}
	if fm.batch[0].op != "PutVertices" || fm.batch[0].size != 2 {
		t.Errorf("batch[0] = %+v, want {PutVertices, 2}", fm.batch[0])
	}
	if fm.batch[1].op != "AddEdges" || fm.batch[1].size != 1 {
		t.Errorf("batch[1] = %+v, want {AddEdges, 1}", fm.batch[1])
	}

	// Illuminate → exactly one illuminate observation with the right labels.
	// Objective is pinned to MINIMIZE so the asserted "minimize" label is
	// explicit and independent of the UNSPECIFIED default (which is MAXIMIZE
	// since #560).
	if _, err := s.Illuminate(ctx, &pb.IlluminateRequest{
		Seed:      "a",
		Step:      2,
		K:         10,
		Algorithm: pb.Algorithm_ALGORITHM_MINIMUM_SPANNING_TREE,
		Objective: pb.Objective_OBJECTIVE_MINIMIZE,
	}); err != nil {
		t.Fatalf("Illuminate: %v", err)
	}
	if len(fm.illuminate) != 1 {
		t.Fatalf("illuminate observations = %d, want 1", len(fm.illuminate))
	}
	if got := fm.illuminate[0]; got.algorithm != "mst" || got.objective != "minimize" || got.weighting != "raw" || got.visitedVertices < 1 {
		t.Errorf("illuminate[0] = %+v, want algorithm=mst objective=minimize weighting=raw visitedVertices≥1", got)
	}

	// Singular forwarders must NOT double-instrument: GetVertex forwards
	// to GetVertices; one batch observation is expected, not two.
	prev := len(fm.batch)
	if _, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: "a"}); err != nil {
		t.Fatalf("GetVertex: %v", err)
	}
	if added := len(fm.batch) - prev; added != 1 {
		t.Errorf("singular GetVertex emitted %d batch observations, want 1 (must not double-count via plural)", added)
	}
}

func TestLanternService_HotPathMetrics_EmitsOnScan(t *testing.T) {
	fm := &fakeHotPathMetrics{}
	fb := newFakeBackend()
	fb.vertices["p:1"] = &pb.Vertex{Key: "p:1", Value: &pb.Vertex_Int64{Int64: 1}}
	fb.vertices["p:2"] = &pb.Vertex{Key: "p:2", Value: &pb.Vertex_Int64{Int64: 2}}
	s := NewLanternService(fb).WithHotPathMetrics(fm)
	ctx := context.Background()
	if _, err := s.ScanVertices(ctx, &pb.ScanVerticesRequest{Prefix: "p:", Limit: 100}); err != nil {
		t.Fatalf("ScanVertices: %v", err)
	}
	if _, err := s.ScanEdges(ctx, &pb.ScanEdgesRequest{Limit: 100}); err != nil {
		t.Fatalf("ScanEdges: %v", err)
	}
	if _, err := s.DeleteVerticesByPrefix(ctx, &pb.DeleteVerticesByPrefixRequest{Prefix: "p:", Limit: 100}); err != nil {
		t.Fatalf("DeleteVerticesByPrefix: %v", err)
	}
	if len(fm.scan) != 3 {
		t.Fatalf("scan observations = %d, want 3", len(fm.scan))
	}
	wantOps := []string{"ScanVertices", "ScanEdges", "DeleteVerticesByPrefix"}
	for i, want := range wantOps {
		if fm.scan[i].op != want {
			t.Errorf("scan[%d].op = %q, want %q", i, fm.scan[i].op, want)
		}
	}
	if fm.scan[0].results != 2 {
		t.Errorf("ScanVertices results = %d, want 2", fm.scan[0].results)
	}
	if fm.scan[2].results != 2 {
		t.Errorf("DeleteVerticesByPrefix results = %d, want 2", fm.scan[2].results)
	}
}

// TestLanternService_HotPathMetrics_EmitsGetVertexHitMiss asserts the #539
// hit/miss split fires once per GetVertices with the right counts, that the
// singular GetVertex forwards through the plural (so it counts exactly
// once), and that a present-but-nil vertex value still scores as a hit.
func TestLanternService_HotPathMetrics_EmitsGetVertexHitMiss(t *testing.T) {
	fm := &fakeHotPathMetrics{}
	s := NewLanternService(graph.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithHotPathMetrics(fm)
	ctx := context.Background()

	// Two live vertices: one with a concrete value, one explicitly nil.
	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "a", Value: &pb.Vertex_Int64{Int64: 1}, Expiration: futureTs(time.Minute)},
		{Key: "n", Value: &pb.Vertex_Nil{Nil: true}, Expiration: futureTs(time.Minute)},
	}}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}

	// Mixed batch: 2 hits (a, n incl. present-but-nil) + 1 miss (gone).
	if _, err := s.GetVertices(ctx, &pb.GetVerticesRequest{Keys: []string{"a", "n", "gone"}}); err != nil {
		t.Fatalf("GetVertices: %v", err)
	}
	if len(fm.getVertex) != 1 {
		t.Fatalf("getVertex observations = %d, want 1", len(fm.getVertex))
	}
	if got := fm.getVertex[0]; got.hits != 2 || got.misses != 1 {
		t.Errorf("getVertex[0] = %+v, want {hits:2 misses:1}", got)
	}

	// Singular GetVertex must forward through the plural: exactly one more
	// observation, counted as a single hit.
	if _, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: "a"}); err != nil {
		t.Fatalf("GetVertex: %v", err)
	}
	if len(fm.getVertex) != 2 {
		t.Fatalf("getVertex observations after singular = %d, want 2", len(fm.getVertex))
	}
	if got := fm.getVertex[1]; got.hits != 1 || got.misses != 0 {
		t.Errorf("getVertex[1] = %+v, want {hits:1 misses:0}", got)
	}

	// A pure miss scores only on the miss side.
	if _, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: "absent"}); err == nil {
		t.Fatal("GetVertex(absent): expected NotFound error, got nil")
	}
	if got := fm.getVertex[2]; got.hits != 0 || got.misses != 1 {
		t.Errorf("getVertex[2] = %+v, want {hits:0 misses:1}", got)
	}
}

// TestLanternService_HotPathMetrics_EmitsGetEdgeHitMiss asserts the #539
// edge-side hit/miss split fires once per GetEdges and that the singular
// GetEdge forwards through the plural.
func TestLanternService_HotPathMetrics_EmitsGetEdgeHitMiss(t *testing.T) {
	fm := &fakeHotPathMetrics{}
	s := NewLanternService(graph.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithHotPathMetrics(fm)
	ctx := context.Background()

	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{
		{Tail: "a", Head: "b", Weight: 1, Expiration: futureTs(time.Minute)},
	}}); err != nil {
		t.Fatalf("AddEdges: %v", err)
	}

	// 1 hit (a->b) + 1 miss (a->z).
	if _, err := s.GetEdges(ctx, &pb.GetEdgesRequest{Edges: []*pb.EdgeKey{
		{Tail: "a", Head: "b"},
		{Tail: "a", Head: "z"},
	}}); err != nil {
		t.Fatalf("GetEdges: %v", err)
	}
	if len(fm.getEdge) != 1 {
		t.Fatalf("getEdge observations = %d, want 1", len(fm.getEdge))
	}
	if got := fm.getEdge[0]; got.hits != 1 || got.misses != 1 {
		t.Errorf("getEdge[0] = %+v, want {hits:1 misses:1}", got)
	}

	// Singular GetEdge forwards through the plural: one more observation.
	if _, err := s.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"}); err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if len(fm.getEdge) != 2 {
		t.Fatalf("getEdge observations after singular = %d, want 2", len(fm.getEdge))
	}
	if got := fm.getEdge[1]; got.hits != 1 || got.misses != 0 {
		t.Errorf("getEdge[1] = %+v, want {hits:1 misses:0}", got)
	}
}

// TestExpirationClamp_FiresValidationRejectHook covers the #222
// bad_ttl hook fire from validateExpiration. The hook MUST run before
// the InvalidArgument status is returned and MUST NOT fire when the
// expiration is within the clamp.
func TestExpirationClamp_FiresValidationRejectHook(t *testing.T) {
	const ttl = time.Hour
	var got []string
	s := NewLanternService(graph.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithTombstoneTTL(ttl).
		WithValidationRejectHook(func(reason string) { got = append(got, reason) })

	bad := timestamppb.New(time.Now().Add(2 * ttl))
	if _, err := s.PutVertices(context.Background(), &pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{{Key: "v", Value: &pb.Vertex_Nil{Nil: true}, Expiration: bad}},
	}); err == nil {
		t.Fatal("PutVertices over TTL: expected error")
	}
	if len(got) != 1 || got[0] != "bad_ttl" {
		t.Fatalf("reject hook calls = %v, want [bad_ttl]", got)
	}

	got = nil
	ok := timestamppb.New(time.Now().Add(ttl / 2))
	if _, err := s.PutVertices(context.Background(), &pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{{Key: "v", Value: &pb.Vertex_Nil{Nil: true}, Expiration: ok}},
	}); err != nil {
		t.Fatalf("PutVertices within TTL: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("reject hook fired on success path: %v", got)
	}
}
