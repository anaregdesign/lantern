package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

func TestSearchFailureRetainsActualBoundedKeysWithoutValues(t *testing.T) {
	var many []*pb.SearchHit
	for i := 0; i < 21; i++ {
		key := fmt.Sprintf("bench:extra:%02d", i)
		many = append(many, &pb.SearchHit{Key: key, Vertex: &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: "PRIVATE_VALUE_SENTINEL"}}})
	}
	for _, hits := range [][]*pb.SearchHit{nil, many, {nil}, {{Key: "bench:ranking:a"}}} {
		endpoint := &queryEndpoint{mode: "oidc", reader: "TOKEN_SENTINEL", client: queryResultClient{hits: hits}}
		err := queryOnce(t.Context(), endpoint, "search")
		var failure *searchResultFailure
		if !errors.As(fmt.Errorf("preflight: %w", err), &failure) || failure.HitCount != len(hits) || failure.Mode != "oidc" || failure.Query != "shared" || failure.Limit != 20 || failure.Prefix != "" {
			t.Fatal("actual failed response/request observation missing", err)
		}
		if len(failure.ObservedKeys) != min(len(hits), 20) || failure.ObservedKeysTruncated != (len(hits) > 20) || len(failure.ExpectedKeys) != 2 {
			t.Fatal("bounded observations or expected set missing", failure)
		}
		for i, key := range failure.ObservedKeys {
			if key != hits[i].GetKey() {
				t.Fatal("observations were inferred from the expected set")
			}
		}
		raw, err := json.Marshal(failure)
		if err != nil || strings.Contains(string(raw), "PRIVATE_VALUE_SENTINEL") || strings.Contains(string(raw), "TOKEN_SENTINEL") {
			t.Fatal("diagnostic lost serialization or retained values/credentials", err)
		}
	}
}

type seedResultClient struct {
	graphv1connect.LanternServiceClient
	badVertexCount bool
	expiredEdge    bool
}

func (c seedResultClient) PutVertices(_ context.Context, r *connect.Request[pb.PutVerticesRequest]) (*connect.Response[pb.PutVerticesResponse], error) {
	out := make([]pb.PutOutcome, len(r.Msg.Vertices))
	for i := range out {
		out[i] = pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE
	}
	if c.badVertexCount {
		out = out[:len(out)-1]
	}
	return connect.NewResponse(&pb.PutVerticesResponse{Outcomes: out}), nil
}

func (c seedResultClient) PutEdges(_ context.Context, r *connect.Request[pb.PutEdgesRequest]) (*connect.Response[pb.PutEdgesResponse], error) {
	out := make([]pb.PutOutcome, len(r.Msg.Edges))
	for i := range out {
		out[i] = pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE
	}
	if c.expiredEdge {
		out[0] = pb.PutOutcome_PUT_OUTCOME_EXPIRED
	}
	return connect.NewResponse(&pb.PutEdgesResponse{Outcomes: out}), nil
}

func TestSeedAcknowledgementsRetainVerifiedCountsOnFailure(t *testing.T) {
	for _, c := range []struct {
		client             seedResultClient
		vertices, edges    int
		vertexRPC, edgeRPC int
		passed             bool
	}{
		{seedResultClient{}, 262, 10245, 3, 81, true},
		{seedResultClient{badVertexCount: true}, 0, 0, 1, 0, false},
		{seedResultClient{expiredEdge: true}, 262, 0, 3, 1, false},
	} {
		ack, err := seedCorpus(t.Context(), &queryEndpoint{client: c.client})
		if (err == nil) != c.passed || ack.VertexCount != 262 || ack.EdgeCount != 10245 || ack.VerticesAppliedAndLive != c.vertices || ack.EdgesAppliedAndLive != c.edges || ack.VertexPutRPCs != c.vertexRPC || ack.EdgePutRPCs != c.edgeRPC {
			t.Fatal("seed acknowledgements inferred success or lost partial failure", ack, err)
		}
	}
}
