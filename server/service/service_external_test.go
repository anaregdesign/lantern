package service_test

import (
	"context"
	"net"
	"testing"
	"time"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// newBufconnClient spins LanternService up on bufconn and returns a raw
// LanternServiceClient. This complements the in-process tests in
// service_test.go by also exercising the actual gRPC wire format.
func newBufconnClient(t *testing.T) (pb.LanternServiceClient, context.Context, func()) {
	t.Helper()

	lis := bufconn.Listen(1 << 16)
	srv := grpc.NewServer()
	svc := service.NewLanternService(cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute))
	pb.RegisterLanternServiceServer(srv, svc)
	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("grpc Serve returned: %v", err)
		}
	}()

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.NewClient(
		"passthrough://bufconn",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	cleanup := func() {
		cancel()
		_ = conn.Close()
		srv.Stop()
		_ = lis.Close()
	}
	return pb.NewLanternServiceClient(conn), ctx, cleanup
}

func bufconnExp(d time.Duration) *timestamppb.Timestamp {
	return timestamppb.New(time.Now().Add(d))
}

func TestBufconn_PutGetVertex_NilValueRoundTrip(t *testing.T) {
	c, ctx, cleanup := newBufconnClient(t)
	defer cleanup()

	v := &pb.Vertex{Key: "n", Value: &pb.Vertex_Nil{Nil: true}, Expiration: bufconnExp(time.Minute)}
	if _, err := c.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{v}}); err != nil {
		t.Fatalf("PutVertex: %v", err)
	}

	got, err := c.GetVertex(ctx, &pb.GetVertexRequest{Key: "n"})
	if err != nil {
		t.Fatalf("GetVertex: %v", err)
	}
	if _, ok := got.GetVertex().GetValue().(*pb.Vertex_Nil); !ok {
		t.Errorf("value = %T, want *Vertex_Nil", got.GetVertex().GetValue())
	}
}

func TestBufconn_GetVertex_NotFound(t *testing.T) {
	c, ctx, cleanup := newBufconnClient(t)
	defer cleanup()

	_, err := c.GetVertex(ctx, &pb.GetVertexRequest{Key: "missing"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v, want NotFound", status.Code(err))
	}
}

func TestBufconn_AddEdge_IsAdditive(t *testing.T) {
	c, ctx, cleanup := newBufconnClient(t)
	defer cleanup()

	edge := &pb.Edge{Tail: "a", Head: "b", Weight: 1.5, Expiration: bufconnExp(time.Minute)}
	for i := 0; i < 2; i++ {
		if _, err := c.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{edge}}); err != nil {
			t.Fatalf("AddEdge %d: %v", i, err)
		}
	}

	got, err := c.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"})
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if w := got.GetEdge().GetWeight(); w != 3.0 {
		t.Errorf("weight after two adds = %v, want 3.0", w)
	}
}

func TestBufconn_PutEdge_IsIdempotent(t *testing.T) {
	c, ctx, cleanup := newBufconnClient(t)
	defer cleanup()

	first := &pb.Edge{Tail: "a", Head: "b", Weight: 1.5, Expiration: bufconnExp(time.Minute)}
	second := &pb.Edge{Tail: "a", Head: "b", Weight: 9.0, Expiration: bufconnExp(time.Minute)}
	if _, err := c.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{first}}); err != nil {
		t.Fatalf("PutEdge 1: %v", err)
	}
	if _, err := c.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{second}}); err != nil {
		t.Fatalf("PutEdge 2: %v", err)
	}

	got, err := c.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"})
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if w := got.GetEdge().GetWeight(); w != 9.0 {
		t.Errorf("weight = %v, want 9.0 (last write wins)", w)
	}
}

func TestBufconn_Illuminate_AllOptimizations(t *testing.T) {
	c, ctx, cleanup := newBufconnClient(t)
	defer cleanup()

	edges := []*pb.Edge{
		{Tail: "a", Head: "b", Weight: 1, Expiration: bufconnExp(time.Minute)},
		{Tail: "b", Head: "c", Weight: 1, Expiration: bufconnExp(time.Minute)},
		{Tail: "a", Head: "c", Weight: 3, Expiration: bufconnExp(time.Minute)},
	}
	if _, err := c.PutEdges(ctx, &pb.PutEdgesRequest{Edges: edges}); err != nil {
		t.Fatalf("PutEdge seed: %v", err)
	}

	opts := []pb.Optimization{
		pb.Optimization_OPTIMIZATION_UNSPECIFIED,
		pb.Optimization_OPTIMIZATION_MINIMUM_SPANNING_TREE,
		pb.Optimization_OPTIMIZATION_MAXIMUM_SPANNING_TREE,
		pb.Optimization_OPTIMIZATION_SHORTEST_PATH_TREE,
		pb.Optimization_OPTIMIZATION_SHORTEST_PATH_TREE_INVERSE,
	}
	for _, opt := range opts {
		t.Run(opt.String(), func(t *testing.T) {
			resp, err := c.Illuminate(ctx, &pb.IlluminateRequest{
				Seed: "a", Step: 3, K: 10, Optimization: opt,
			})
			if err != nil {
				t.Fatalf("Illuminate(%s): %v", opt, err)
			}
			got := map[string]bool{}
			for _, v := range resp.GetGraph().GetVertices() {
				got[v.GetKey()] = true
			}
			for _, want := range []string{"a", "b", "c"} {
				if !got[want] {
					t.Errorf("Illuminate(%s) missing vertex %q (got %v)", opt, want, got)
				}
			}
		})
	}
}

func TestBufconn_DeleteVertices_HappyPath(t *testing.T) {
	c, ctx, cleanup := newBufconnClient(t)
	defer cleanup()

	vs := []*pb.Vertex{
		{Key: "a", Value: &pb.Vertex_Int64{Int64: 1}, Expiration: bufconnExp(time.Minute)},
		{Key: "b", Value: &pb.Vertex_Int64{Int64: 2}, Expiration: bufconnExp(time.Minute)},
	}
	if _, err := c.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: vs}); err != nil {
		t.Fatalf("PutVertex: %v", err)
	}
	resp, err := c.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("DeleteVertices: %v", err)
	}
	if resp.GetDeleted() != 2 {
		t.Errorf("Deleted = %d, want 2", resp.GetDeleted())
	}
	for _, k := range []string{"a", "b"} {
		if _, err := c.GetVertex(ctx, &pb.GetVertexRequest{Key: k}); status.Code(err) != codes.NotFound {
			t.Errorf("GetVertex %q after delete: code = %v, want NotFound", k, status.Code(err))
		}
	}
}

func TestBufconn_DeleteEdges_HappyPath(t *testing.T) {
	c, ctx, cleanup := newBufconnClient(t)
	defer cleanup()

	edges := []*pb.Edge{
		{Tail: "a", Head: "b", Weight: 1, Expiration: bufconnExp(time.Minute)},
		{Tail: "b", Head: "c", Weight: 1, Expiration: bufconnExp(time.Minute)},
	}
	if _, err := c.AddEdges(ctx, &pb.AddEdgesRequest{Edges: edges}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	resp, err := c.DeleteEdges(ctx, &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{
		{Tail: "a", Head: "b"},
		{Tail: "b", Head: "c"},
	}})
	if err != nil {
		t.Fatalf("DeleteEdges: %v", err)
	}
	if resp.GetDeleted() != 2 {
		t.Errorf("Deleted = %d, want 2", resp.GetDeleted())
	}
	for _, e := range edges {
		if _, err := c.GetEdge(ctx, &pb.GetEdgeRequest{Tail: e.GetTail(), Head: e.GetHead()}); status.Code(err) != codes.NotFound {
			t.Errorf("GetEdge after delete: code = %v, want NotFound", status.Code(err))
		}
	}
}

func TestBufconn_GetVertices_PartialMiss(t *testing.T) {
	c, ctx, cleanup := newBufconnClient(t)
	defer cleanup()

	vs := []*pb.Vertex{
		{Key: "a", Value: &pb.Vertex_Int64{Int64: 1}, Expiration: bufconnExp(time.Minute)},
		{Key: "b", Value: &pb.Vertex_Nil{Nil: true}, Expiration: bufconnExp(time.Minute)},
	}
	if _, err := c.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: vs}); err != nil {
		t.Fatalf("PutVertex: %v", err)
	}

	resp, err := c.GetVertices(ctx, &pb.GetVerticesRequest{Keys: []string{"a", "b", "missing"}})
	if err != nil {
		t.Fatalf("GetVertices: %v", err)
	}
	if got, want := len(resp.GetVertices()), 2; got != want {
		t.Fatalf("len(Vertices) = %d, want %d", got, want)
	}
	gotKeys := map[string]bool{}
	for _, v := range resp.GetVertices() {
		gotKeys[v.GetKey()] = true
	}
	if !gotKeys["a"] || !gotKeys["b"] {
		t.Errorf("Vertices keys = %v, want a+b", gotKeys)
	}
	if len(resp.GetMissing()) != 1 || resp.GetMissing()[0] != "missing" {
		t.Errorf("Missing = %v, want [missing]", resp.GetMissing())
	}
}

func TestBufconn_GetEdges_PartialMiss(t *testing.T) {
	c, ctx, cleanup := newBufconnClient(t)
	defer cleanup()

	edges := []*pb.Edge{
		{Tail: "a", Head: "b", Weight: 1.5, Expiration: bufconnExp(time.Minute)},
		{Tail: "b", Head: "c", Weight: 2, Expiration: bufconnExp(time.Minute)},
	}
	if _, err := c.AddEdges(ctx, &pb.AddEdgesRequest{Edges: edges}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	resp, err := c.GetEdges(ctx, &pb.GetEdgesRequest{Edges: []*pb.EdgeKey{
		{Tail: "a", Head: "b"},
		{Tail: "b", Head: "c"},
		{Tail: "x", Head: "y"},
	}})
	if err != nil {
		t.Fatalf("GetEdges: %v", err)
	}
	if got, want := len(resp.GetEdges()), 2; got != want {
		t.Fatalf("len(Edges) = %d, want %d", got, want)
	}
	if len(resp.GetMissing()) != 1 ||
		resp.GetMissing()[0].GetTail() != "x" || resp.GetMissing()[0].GetHead() != "y" {
		t.Errorf("Missing = %v, want [{x y}]", resp.GetMissing())
	}
}

// TestBufconn_PutVertex_NoExpiration_NeverExpires is the end-to-end
// regression for #250. Before the fix, omitting Expiration on PutVertex
// caused (*timestamppb.Timestamp)(nil).AsTime() == Unix(0,0) to flow into
// the volatile cache entry. volatile.IsExpired returned Before(now)==true
// for that timestamp, so GetVertex returned NotFound for every write —
// exactly the silent data-loss observed in the read_heavy bench scenario.
func TestBufconn_PutVertex_NoExpiration_NeverExpires(t *testing.T) {
	c, ctx, cleanup := newBufconnClient(t)
	defer cleanup()

	v := &pb.Vertex{Key: "no-exp", Value: &pb.Vertex_Nil{Nil: true}} // no Expiration set
	if _, err := c.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{v}}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}

	got, err := c.GetVertex(ctx, &pb.GetVertexRequest{Key: "no-exp"})
	if err != nil {
		t.Fatalf("GetVertex(no-exp) returned %v, want vertex; this is the #250 regression", err)
	}
	if got.GetVertex().GetKey() != "no-exp" {
		t.Errorf("key = %q, want %q", got.GetVertex().GetKey(), "no-exp")
	}
}

// TestBufconn_AddEdge_NoExpiration_NeverExpires mirrors the vertex
// regression for edges. The edgeCache flushLocked path also called
// v.expiration.After(now), so an edge added without Expiration was
// immediately swept on the next read.
func TestBufconn_AddEdge_NoExpiration_NeverExpires(t *testing.T) {
	c, ctx, cleanup := newBufconnClient(t)
	defer cleanup()

	e := &pb.Edge{Tail: "a", Head: "b", Weight: 1.5} // no Expiration set
	if _, err := c.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{e}}); err != nil {
		t.Fatalf("AddEdges: %v", err)
	}

	got, err := c.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"})
	if err != nil {
		t.Fatalf("GetEdge(a->b) returned %v, want edge; this is the #250 regression", err)
	}
	if got.GetEdge().GetWeight() != 1.5 {
		t.Errorf("weight = %v, want 1.5", got.GetEdge().GetWeight())
	}
}
