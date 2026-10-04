package integration_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/service"
)

// This gate tests generic Core range constraints through the real Connect path.
// The trusted interceptor supplies a range; OIDC/Role interpretation belongs to
// the later Server delivery and is deliberately outside this Core PR.
func TestQueryScope_SharedStatisticsAndInducedPathsRealConnect(t *testing.T) {
	cache := newProductionSearchCache(time.Hour, true, true, productionSearchAnalysisLimits())
	for key, text := range map[string]string{"visible:a": "needle launch", "visible:b": "needle ordinary longer document", "hidden:best": "needle needle needle needle launch"} {
		if err := cache.PutVertex(key, &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: text}}); err != nil {
			t.Fatal(err)
		}
	}
	view, err := graphcache.NewQueryView([]graphcache.KeyRange{{Lower: "visible:", Upper: "visible;"}}, []graphcache.KeyRange{{Lower: "visible:", Upper: "visible;"}})
	if err != nil {
		t.Fatal(err)
	}
	interceptor := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			return next(graphcache.WithQueryView(ctx, view), req)
		}
	})
	svc := service.NewLanternService(cache).WithSearchLimits(service.SearchLimits{Enabled: true, PositionsEnabled: true})
	server := newConnectTestServer(t, svc, nil, interceptor)
	client := graphv1connect.NewLanternServiceClient(h2cClient(), server.url)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request := &pb.SearchVerticesRequest{Query: "needle", Limit: 1, Projection: pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX}
	first, err := client.SearchVertices(ctx, connect.NewRequest(request))
	if err != nil || len(first.Msg.Hits) != 1 || first.Msg.Hits[0].Key == "hidden:best" {
		t.Fatal("range was applied after top-k", first, err)
	}
	unrestricted, _, err := cache.SearchVerticesMatchContext(ctx, "needle", 10, "", search.MatchOptions{}, false, search.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range unrestricted {
		if hit.ID == first.Msg.Hits[0].Key && hit.Score != first.Msg.Hits[0].Score {
			t.Fatal("range rebuilt corpus statistics", hit, first.Msg.Hits[0])
		}
	}
	if err := cache.PutVertex("hidden:ordinary", &pb.Vertex{Key: "hidden:ordinary", Value: &pb.Vertex_String_{String_: "unrelated private corpus document"}}); err != nil {
		t.Fatal(err)
	}
	second, err := client.SearchVertices(ctx, connect.NewRequest(request))
	if err != nil || len(second.Msg.Hits) != 1 || second.Msg.Hits[0].Score == first.Msg.Hits[0].Score {
		t.Fatal("private same-corpus data did not share statistics", second, err)
	}
	hidden, err := client.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "needle", Prefix: "hidden:", Limit: 1}))
	if err != nil || len(hidden.Msg.Hits) != 0 {
		t.Fatal("requested hidden scope escaped generic range", hidden, err)
	}
	cache.AddEdgesWithExpiration([]graphcache.EdgeItem[string]{{Tail: "visible:seed", Head: "visible:end", Weight: 3}, {Tail: "visible:seed", Head: "hidden:bridge", Weight: 100}, {Tail: "hidden:bridge", Head: "visible:unreachable", Weight: 100}})
	for _, weighting := range []pb.Weighting{pb.Weighting_WEIGHTING_RAW, pb.Weighting_WEIGHTING_TFIDF, pb.Weighting_WEIGHTING_BM25} {
		response, err := client.Illuminate(ctx, connect.NewRequest(&pb.IlluminateRequest{Seed: "visible:seed", Weighting: weighting, Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 2, FanOut: 10}}}))
		if err != nil {
			t.Fatal(err)
		}
		for _, vertex := range response.Msg.GetGraph().Vertices {
			if vertex.Key == "hidden:bridge" || vertex.Key == "visible:unreachable" {
				t.Fatal("traversal crossed an excluded path", response.Msg)
			}
		}
		if len(response.Msg.GetGraph().Edges) != 1 || response.Msg.GetGraph().Edges[0].Head != "visible:end" {
			t.Fatal("visible path lost", response.Msg)
		}
	}
}
