package integration_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/service"
)

func TestLantern_Illuminate_PPRWorkBudgetFailsWithoutRetainingReadLock(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request func() *pb.IlluminateRequest
	}{
		{
			name: "PPR",
			request: func() *pb.IlluminateRequest {
				return &pb.IlluminateRequest{Seed: "seed", Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{Epsilon: 1e-9}}}
			},
		},
		{
			name: "local community",
			request: func() *pb.IlluminateRequest {
				return &pb.IlluminateRequest{Seed: "seed", Params: &pb.IlluminateRequest_Community{Community: &pb.LocalCommunityParams{Epsilon: 1e-9}}}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
			svc := service.NewLanternService(cache).
				WithTraversalTimeout(time.Second).
				WithTraversalLimits(service.TraversalLimits{
					WorkBudget: graphcache.PPRWorkBudget{MaxPushes: 1, MaxTouchedEdges: 100},
					MaxResults: 8,
				})
			srv := newConnectTestServer(t, svc, nil)
			c := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			t.Cleanup(cancel)
			if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: []*pb.Vertex{
				{Key: "seed", Value: &pb.Vertex_String_{String_: "seed"}},
				{Key: "a", Value: &pb.Vertex_String_{String_: "a"}},
			}})); err != nil {
				t.Fatalf("PutVertices: %v", err)
			}
			if _, err := c.PutEdges(ctx, connect.NewRequest(&pb.PutEdgesRequest{Edges: []*pb.Edge{
				{Tail: "seed", Head: "a", Weight: 1},
				{Tail: "a", Head: "seed", Weight: 1},
			}})); err != nil {
				t.Fatalf("PutEdges: %v", err)
			}

			_, err := c.Illuminate(ctx, connect.NewRequest(tc.request()))
			if got := connect.CodeOf(err); got != connect.CodeResourceExhausted {
				t.Fatalf("Illuminate code = %v, want ResourceExhausted (err=%v)", got, err)
			}

			// This needs the cache write lock. Its prompt completion proves the
			// exhausted forward-push released GraphCache.mu.RLock.
			if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: []*pb.Vertex{
				{Key: "after-budget", Value: &pb.Vertex_String_{String_: "ok"}},
			}})); err != nil {
				t.Fatalf("PutVertices after exhausted Illuminate: %v", err)
			}
		})
	}
}
