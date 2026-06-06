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
}

type illuminateObs struct {
	optimization                  string
	visitedVertices, visitedEdges int
	traversal, optimize           time.Duration
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

func (f *fakeHotPathMetrics) OnIlluminate(opt string, vV, vE int, traversal, optimize time.Duration) {
	f.illuminate = append(f.illuminate, illuminateObs{opt, vV, vE, traversal, optimize})
}
func (f *fakeHotPathMetrics) OnScan(op string, results int, d time.Duration) {
	f.scan = append(f.scan, scanObs{op, results, d})
}
func (f *fakeHotPathMetrics) OnBatch(op string, size int) {
	f.batch = append(f.batch, batchObs{op, size})
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

	// Illuminate → exactly one illuminate observation with the right label.
	if _, err := s.Illuminate(ctx, &pb.IlluminateRequest{
		Seed:         "a",
		Step:         2,
		K:            10,
		Optimization: pb.Optimization_OPTIMIZATION_MINIMUM_SPANNING_TREE,
	}); err != nil {
		t.Fatalf("Illuminate: %v", err)
	}
	if len(fm.illuminate) != 1 {
		t.Fatalf("illuminate observations = %d, want 1", len(fm.illuminate))
	}
	if got := fm.illuminate[0]; got.optimization != "mst" || got.visitedVertices < 1 {
		t.Errorf("illuminate[0] = %+v, want optimization=mst & visitedVertices≥1", got)
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
