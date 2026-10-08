package main

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

type queryResultClient struct {
	graphv1connect.LanternServiceClient
	graph *pb.Graph
	hits  []*pb.SearchHit
}

func (c queryResultClient) Illuminate(context.Context, *connect.Request[pb.IlluminateRequest]) (*connect.Response[pb.IlluminateResponse], error) {
	return connect.NewResponse(&pb.IlluminateResponse{Graph: c.graph}), nil
}
func (c queryResultClient) SearchVertices(context.Context, *connect.Request[pb.SearchVerticesRequest]) (*connect.Response[pb.SearchVerticesResponse], error) {
	return connect.NewResponse(&pb.SearchVerticesResponse{Hits: c.hits}), nil
}

func TestQueryResultValidationRejectsHiddenBridgesAndEmptySuccess(t *testing.T) {
	for _, family := range []string{"bfs", "ppr", "community"} {
		for _, forbidden := range []string{hiddenBridge, unreachable} {
			endpoint := &queryEndpoint{mode: "oidc", client: queryResultClient{graph: &pb.Graph{Vertices: []*pb.Vertex{{Key: "bench:walk:root"}, {Key: forbidden}}, Edges: []*pb.Edge{{Tail: "bench:walk:root", Head: forbidden}}}}}
			if queryOnce(t.Context(), endpoint, family) == nil {
				t.Fatal("denied bridge/resource accepted", family, forbidden)
			}
		}
		if queryOnce(t.Context(), &queryEndpoint{mode: "oidc", client: queryResultClient{}}, family) == nil {
			t.Fatal("empty success accepted", family)
		}
	}
	endpoint := &queryEndpoint{mode: "oidc", client: queryResultClient{hits: []*pb.SearchHit{{Key: "bench:ranking:a", Vertex: &pb.Vertex{Key: "bench:ranking:a"}}, {Key: "bench:ranking:b", Vertex: &pb.Vertex{Key: "bench:ranking:b"}}, {Key: hiddenHit, Vertex: &pb.Vertex{Key: hiddenHit}}}}}
	if queryOnce(t.Context(), endpoint, "search") == nil {
		t.Fatal("denied search candidate accepted")
	}
}

func TestCorpusRetainsBroadTopologyAndStableLogicalKeys(t *testing.T) {
	first, second := corpusDigest(), corpusDigest()
	if first != second || len(first) != 64 {
		t.Fatal("OFF/ON logical corpus cannot be pinned")
	}
	data := corpus()
	degree := 0
	for _, edge := range data.Edges {
		if edge.Tail == "bench:walk:root" {
			degree++
		}
	}
	if degree != 65 || len(data.Edges) < 10000 {
		t.Fatal("broad topology silently shrank", degree, len(data.Edges))
	}
}
