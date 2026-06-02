package service

import (
	"context"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
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
	if st, _ := status.FromError(err); st.Code() != codes.NotFound {
		t.Errorf("status code = %v, want NotFound", st.Code())
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
	if st, _ := status.FromError(err); st.Code() != codes.NotFound {
		t.Errorf("code = %v, want NotFound", st.Code())
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

func TestLanternService_Illuminate_Unspecified(t *testing.T) {
	s := newTestService(t)
	seedTriangle(t, s)

	resp, err := s.Illuminate(context.Background(), &pb.IlluminateRequest{
		Seed:         "a",
		Step:         3,
		K:            10,
		Optimization: pb.Optimization_OPTIMIZATION_UNSPECIFIED,
	})
	if err != nil {
		t.Fatalf("Illuminate: %v", err)
	}
	if len(resp.Graph.Vertices) != 3 {
		t.Errorf("vertices = %d, want 3", len(resp.Graph.Vertices))
	}
}

func TestLanternService_Illuminate_AllOptimizations(t *testing.T) {
	cases := []pb.Optimization{
		pb.Optimization_OPTIMIZATION_MINIMUM_SPANNING_TREE,
		pb.Optimization_OPTIMIZATION_MAXIMUM_SPANNING_TREE,
		pb.Optimization_OPTIMIZATION_SHORTEST_PATH_TREE,
		pb.Optimization_OPTIMIZATION_SHORTEST_PATH_TREE_INVERSE,
	}
	for _, opt := range cases {
		t.Run(opt.String(), func(t *testing.T) {
			s := newTestService(t)
			seedTriangle(t, s)
			resp, err := s.Illuminate(context.Background(), &pb.IlluminateRequest{
				Seed:         "a",
				Step:         3,
				K:            10,
				Optimization: opt,
			})
			if err != nil {
				t.Fatalf("Illuminate(%v): %v", opt, err)
			}
			if len(resp.Graph.Vertices) == 0 {
				t.Errorf("Illuminate(%v) returned empty vertices", opt)
			}
		})
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
	} else if st, _ := status.FromError(err); st.Code() != codes.Canceled {
		t.Errorf("code = %v, want Canceled", st.Code())
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
