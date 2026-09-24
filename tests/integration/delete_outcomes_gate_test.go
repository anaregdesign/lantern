package integration_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestExactDeleteOutcomes_RealConnectWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	node := newPumpNode(t, hlc.NodeID{0xD2, 0x01})

	if _, err := node.raw.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "delete/a"}, {Key: "delete/b"},
	}})); err != nil {
		t.Fatal(err)
	}
	vertices, err := node.raw.DeleteVertices(ctx, connect.NewRequest(&pb.DeleteVerticesRequest{
		Keys: []string{"delete/a", "delete/missing", "delete/a", "delete/b"},
	}))
	if err != nil || vertices.Msg.GetDeleted() != 2 || !slices.Equal(vertices.Msg.GetExisted(), []bool{true, false, false, true}) {
		t.Fatalf("wire DeleteVertices = (%+v, %v)", vertices, err)
	}
	if empty, err := node.raw.DeleteVertices(ctx, connect.NewRequest(&pb.DeleteVerticesRequest{})); err != nil || empty.Msg.GetDeleted() != 0 || len(empty.Msg.GetExisted()) != 0 {
		t.Fatalf("empty wire DeleteVertices = (%+v, %v)", empty, err)
	}
	if _, err := node.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "delete/singular"}})); err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{true, false} {
		got, err := node.raw.DeleteVertex(ctx, connect.NewRequest(&pb.DeleteVertexRequest{Key: "delete/singular"}))
		if err != nil || got.Msg.GetExisted() != want {
			t.Fatalf("wire DeleteVertex call %d = (%+v, %v), want %v", i, got, err, want)
		}
	}

	if _, err := node.raw.PutEdges(ctx, connect.NewRequest(&pb.PutEdgesRequest{Edges: []*pb.Edge{
		{Tail: "edge/a", Head: "edge/b", Weight: 1},
		{Tail: "edge/a", Head: "edge/c", Weight: 1},
	}})); err != nil {
		t.Fatal(err)
	}
	edges, err := node.raw.DeleteEdges(ctx, connect.NewRequest(&pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{
		{Tail: "edge/a", Head: "edge/b"}, {Tail: "missing", Head: "edge"},
		{Tail: "edge/a", Head: "edge/b"}, {Tail: "edge/a", Head: "edge/c"},
	}}))
	if err != nil || edges.Msg.GetDeleted() != 2 || !slices.Equal(edges.Msg.GetExisted(), []bool{true, false, false, true}) {
		t.Fatalf("wire DeleteEdges = (%+v, %v)", edges, err)
	}
	if _, err := node.raw.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "edge/singular", Head: "edge/target", Weight: 1}})); err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{true, false} {
		got, err := node.raw.DeleteEdge(ctx, connect.NewRequest(&pb.DeleteEdgeRequest{Tail: "edge/singular", Head: "edge/target"}))
		if err != nil || got.Msg.GetExisted() != want {
			t.Fatalf("wire DeleteEdge call %d = (%+v, %v), want %v", i, got, err, want)
		}
	}
}

func TestExactDeleteOutcomes_CapacityFailureHasNoPartialWireResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	node := newPumpNode(t, hlc.NodeID{0xD2, 0x02})
	node.cache.SetCausalMetadataLimits(graphcache.CausalMetadataLimits{MaxVertexEntries: 1})
	if _, err := node.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "retained"}})); err != nil {
		t.Fatal(err)
	}
	resp, err := node.raw.DeleteVertices(ctx, connect.NewRequest(&pb.DeleteVerticesRequest{Keys: []string{"retained", "new"}}))
	if connect.CodeOf(err) != connect.CodeResourceExhausted || resp != nil {
		t.Fatalf("capacity-limited wire DeleteVertices = (%+v, %v), want nil/ResourceExhausted", resp, err)
	}
	if _, err := node.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "retained"})); err != nil {
		t.Fatalf("rejected batch removed a vertex: %v", err)
	}
}
