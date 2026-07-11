package integration_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestLantern_Illuminate_ShortestPathTreeRejectsNegativeCycles(t *testing.T) {
	for _, objective := range []pb.Objective{
		pb.Objective_OBJECTIVE_MINIMIZE,
		pb.Objective_OBJECTIVE_MAXIMIZE,
	} {
		t.Run(objective.String(), func(t *testing.T) {
			c, _ := newRawConnectClient(t, false)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			t.Cleanup(cancel)
			exp := timestamppb.New(time.Now().Add(time.Hour))
			if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: []*pb.Vertex{
				{Key: "a", Value: &pb.Vertex_String_{String_: "a"}, Expiration: exp},
				{Key: "b", Value: &pb.Vertex_String_{String_: "b"}, Expiration: exp},
			}})); err != nil {
				t.Fatalf("PutVertices: %v", err)
			}
			if _, err := c.PutEdges(ctx, connect.NewRequest(&pb.PutEdgesRequest{Edges: []*pb.Edge{
				{Tail: "a", Head: "b", Weight: -1, Expiration: exp},
				{Tail: "b", Head: "a", Weight: -1, Expiration: exp},
			}})); err != nil {
				t.Fatalf("PutEdges: %v", err)
			}

			_, err := c.Illuminate(ctx, connect.NewRequest(&pb.IlluminateRequest{
				Seed: "a",
				Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{
					Step: 3, FanOut: 8,
					Reduction: pb.Reduction_REDUCTION_SHORTEST_PATH_TREE,
					Objective: objective,
				}},
			}))
			if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
				t.Fatalf("Illuminate code = %v, want FailedPrecondition (err=%v)", got, err)
			}

			// A follow-up RPC proves the failed reduction released the graph read lock.
			if _, err := c.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "a"})); err != nil {
				t.Fatalf("GetVertex after failed Illuminate: %v", err)
			}
		})
	}
}
