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

func TestSearchRequiresExactModeSpecificNonemptyResults(t *testing.T) {
	hits := func(keys ...string) []*pb.SearchHit {
		var out []*pb.SearchHit
		for _, key := range keys {
			out = append(out, &pb.SearchHit{Key: key, Vertex: &pb.Vertex{Key: key}})
		}
		return out
	}
	a, b := "bench:ranking:a", "bench:ranking:b"
	for _, mode := range []string{"off", "oidc"} {
		endpoint := &queryEndpoint{mode: mode, client: queryResultClient{hits: hits(expectedSearchKeys(mode)...)}}
		if err := queryOnce(t.Context(), endpoint, "search"); err != nil {
			t.Fatal("correct mode-specific expected set", mode, err)
		}
		for _, keys := range [][]string{nil, {a}, {a, a}, {a, b, "bench:unexpected"}, {a, b, hiddenHit, hiddenHit}} {
			endpoint.client = queryResultClient{hits: hits(keys...)}
			if queryOnce(t.Context(), endpoint, "search") == nil {
				t.Fatal("empty/missing/duplicate/unexpected results accepted", mode, keys)
			}
		}
	}
	if len(corpus().Vertices) != 262 || len(corpus().Edges) != 10245 || corpusDigest() != "30014457373ca8142f0c13cc0606078dc39718f6c9ff842ba08912cf89c4a582" {
		t.Fatal("existing corpus changed")
	}
}
