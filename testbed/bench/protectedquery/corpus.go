package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/testbed/bench/topology"
)

const (
	hiddenBridge = "bench:private:bridge"
	unreachable  = "bench:walk:unreachable"
	hiddenHit    = "bench:private:best"
)

type logicalCorpus struct {
	Vertices []*pb.Vertex `json:"vertices"`
	Edges    []*pb.Edge   `json:"edges"`
}

func corpus() logicalCorpus {
	broad := topology.NewBroadIlluminateFixture(time.Time{})
	result := logicalCorpus{}
	for _, vertex := range broad.Vertices {
		result.Vertices = append(result.Vertices, &pb.Vertex{Key: vertex.Key, Value: &pb.Vertex_String_{String_: vertex.Value.(string)}})
	}
	for _, edge := range broad.Edges {
		result.Edges = append(result.Edges, &pb.Edge{Tail: edge.Tail, Head: edge.Head, Weight: edge.Weight})
	}
	for _, item := range []struct{ key, text string }{
		{"bench:ranking:a", "shared searchable ordinary document"},
		{"bench:ranking:b", "shared ordinary longer corpus document"},
		{hiddenHit, "shared shared shared shared searchable document"},
		{hiddenBridge, "private bridge"}, {unreachable, "visible reachable only through hidden bridge"},
	} {
		result.Vertices = append(result.Vertices, &pb.Vertex{Key: item.key, Value: &pb.Vertex_String_{String_: item.text}})
	}
	for _, seed := range []string{topology.WalkSeed, topology.CommunitySeed} {
		result.Edges = append(result.Edges, &pb.Edge{Tail: seed, Head: hiddenBridge, Weight: 100})
	}
	result.Edges = append(result.Edges, &pb.Edge{Tail: hiddenBridge, Head: unreachable, Weight: 100})
	return result
}

func corpusDigest() string {
	raw, _ := json.Marshal(corpus())
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func seedCorpus(ctx context.Context, endpoint *queryEndpoint) error {
	c := corpus()
	for start := 0; start < len(c.Vertices); start += 128 {
		end := min(start+128, len(c.Vertices))
		response, err := endpoint.client.PutVertices(ctx, authenticated(endpoint.writer, &pb.PutVerticesRequest{Vertices: c.Vertices[start:end]}))
		if err != nil {
			return err
		}
		if len(response.Msg.Outcomes) != end-start {
			return errors.New("seed vertex outcome count mismatch")
		}
		for _, outcome := range response.Msg.Outcomes {
			if outcome != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
				return errors.New("seed vertex not live")
			}
		}
	}
	for start := 0; start < len(c.Edges); start += 128 {
		end := min(start+128, len(c.Edges))
		response, err := endpoint.client.PutEdges(ctx, authenticated(endpoint.writer, &pb.PutEdgesRequest{Edges: c.Edges[start:end]}))
		if err != nil {
			return err
		}
		if len(response.Msg.Outcomes) != end-start {
			return errors.New("seed edge outcome count mismatch")
		}
		for _, outcome := range response.Msg.Outcomes {
			if outcome != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
				return errors.New("seed edge not live")
			}
		}
	}
	return nil
}

func traversal(family, seed string) *pb.IlluminateRequest {
	request := &pb.IlluminateRequest{Seed: seed, Weighting: pb.Weighting_WEIGHTING_BM25}
	switch family {
	case "bfs":
		request.Params = &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 2, FanOut: 32}}
	case "ppr":
		request.Params = &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{TopN: 32, RestartProb: .2, Epsilon: .0001}}
	case "community":
		request.Params = &pb.IlluminateRequest_Community{Community: &pb.LocalCommunityParams{MaxSize: 32, RestartProb: .2, Epsilon: .0001}}
	}
	return request
}

func queryOnce(ctx context.Context, endpoint *queryEndpoint, family string) error {
	if family == "search" {
		response, err := endpoint.client.SearchVertices(ctx, authenticated(endpoint.reader, &pb.SearchVerticesRequest{Query: "shared", Limit: 20, Projection: pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX}))
		if err != nil {
			return err
		}
		visible := 0
		for _, hit := range response.Msg.Hits {
			if !strings.HasPrefix(hit.Key, "bench:") || hit.Vertex == nil || hit.Vertex.Key != hit.Key {
				return errors.New("search lost logical key or full-vertex result")
			}
			if strings.HasPrefix(hit.Key, "bench:ranking:") {
				visible++
			}
			if endpoint.mode == "oidc" && strings.HasPrefix(hit.Key, "bench:private:") {
				return errors.New("search disclosed denied hit")
			}
		}
		if visible < 2 || endpoint.mode == "off" && len(response.Msg.Hits) < 3 {
			return errors.New("search lost corpus results")
		}
		return nil
	}
	seed := topology.WalkSeed
	if family == "community" {
		seed = topology.CommunitySeed
	}
	response, err := endpoint.client.Illuminate(ctx, authenticated(endpoint.reader, traversal(family, seed)))
	if err != nil {
		return err
	}
	graph := response.Msg.GetGraph()
	if graph == nil || len(graph.Vertices) < 2 || len(graph.Edges) == 0 {
		return fmt.Errorf("%s returned no traversed graph", family)
	}
	for _, vertex := range graph.Vertices {
		if !strings.HasPrefix(vertex.Key, "bench:") {
			return errors.New("traversal escaped logical corpus")
		}
		if endpoint.mode == "oidc" && (strings.HasPrefix(vertex.Key, "bench:private:") || vertex.Key == unreachable) {
			return errors.New("traversal crossed denied bridge")
		}
	}
	for _, edge := range graph.Edges {
		if !strings.HasPrefix(edge.Tail, "bench:") || !strings.HasPrefix(edge.Head, "bench:") {
			return errors.New("traversal edge escaped logical corpus")
		}
		if endpoint.mode == "oidc" && (strings.HasPrefix(edge.Tail, "bench:private:") || strings.HasPrefix(edge.Head, "bench:private:") || edge.Head == unreachable || edge.Tail == unreachable) {
			return errors.New("traversal disclosed denied edge")
		}
	}
	return nil
}

func verifyCorpus(ctx context.Context, endpoint *queryEndpoint, invalid string) error {
	count, err := endpoint.client.CountVerticesByPrefix(ctx, authenticated(endpoint.reader, &pb.CountVerticesByPrefixRequest{Prefix: "bench:ranking:"}))
	if err != nil || count.Msg.Count != 2 {
		return errors.New("actual visible count mismatch")
	}
	for _, family := range []string{"search", "bfs", "ppr", "community"} {
		if err := queryOnce(ctx, endpoint, family); err != nil {
			return fmt.Errorf("%s preflight: %w", family, err)
		}
	}
	if endpoint.mode == "oidc" {
		if _, err := endpoint.client.GetVertex(ctx, authenticated(invalid, &pb.GetVertexRequest{Key: "bench:ranking:a"})); connect.CodeOf(err) != connect.CodeUnauthenticated {
			return errors.New("invalid JWT was not rejected")
		}
		if _, err := endpoint.client.GetVertex(ctx, authenticated("", &pb.GetVertexRequest{Key: "bench:ranking:a"})); connect.CodeOf(err) != connect.CodeUnauthenticated {
			return errors.New("missing JWT was not rejected")
		}
		for _, key := range []string{hiddenHit, "outside:absent"} {
			if _, err := endpoint.client.GetVertex(ctx, authenticated(endpoint.reader, &pb.GetVertexRequest{Key: key})); connect.CodeOf(err) != connect.CodePermissionDenied {
				return errors.New("Role denial was not enforced")
			}
		}
		if _, err := endpoint.client.Illuminate(ctx, authenticated(endpoint.reader, traversal("bfs", hiddenBridge))); connect.CodeOf(err) != connect.CodePermissionDenied {
			return errors.New("denied seed was not rejected")
		}
	}
	// Prove the high-weight bridge exists in the same corpus using the
	// independently authorized writer, rather than relying on empty results.
	response, err := endpoint.client.Illuminate(ctx, authenticated(endpoint.writer, traversal("bfs", topology.WalkSeed)))
	if err != nil {
		return err
	}
	found := false
	for _, vertex := range response.Msg.GetGraph().Vertices {
		found = found || vertex.Key == unreachable
	}
	if !found {
		return errors.New("full-corpus control did not cross planted hidden bridge")
	}
	return nil
}
