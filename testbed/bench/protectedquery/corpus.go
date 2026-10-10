package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
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
	searchLimit  = 20
	alphaPrefix  = "bench:community:alpha:"
)

type logicalCorpus struct {
	Vertices []*pb.Vertex `json:"vertices"`
	Edges    []*pb.Edge   `json:"edges"`
}

type fixtureSearchExpectation struct {
	keys       []string
	candidates map[string]bool
}

// Derive from the ASCII fixture's independent word/gram inventory, not live
// hits or the production index. Precompute before any timed driver action.
var fixtureSearchExpectations = deriveFixtureSearchExpectations()

func fixtureSearchTerms(text string) map[string]bool {
	for _, r := range text {
		if r > 127 {
			panic("query fixture requires its pinned ASCII search contract")
		}
	}
	terms := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		terms["word:"+word] = true
		if len(word) > 2 { // ScriptAwareTokenizer omits redundant exact-width grams.
			for i := 0; i+2 <= len(word); i++ {
				terms["gram:"+word[i:i+2]] = true
			}
		}
	}
	return terms
}

func deriveFixtureSearchExpectations() map[string]fixtureSearchExpectation {
	query := fixtureSearchTerms("shared")
	matching := map[string]bool{}
	for _, vertex := range corpus().Vertices {
		value, ok := vertex.Value.(*pb.Vertex_String_)
		if !ok {
			panic("query fixture search requires pinned string fields")
		}
		for _, text := range []string{vertex.Key, value.String_} {
			for term := range fixtureSearchTerms(text) {
				if query[term] {
					matching[vertex.Key] = true
				}
			}
		}
	}
	result := map[string]fixtureSearchExpectation{}
	for _, mode := range []string{"off", "oidc"} {
		keys := append(requiredRankingKeys(mode), unreachable)
		anchors := map[string]bool{}
		for _, key := range keys {
			if !matching[key] {
				panic("required query fixture anchor does not match")
			}
			anchors[key] = true
		}
		eligible, alpha := map[string]bool{}, []string{}
		for key := range matching {
			if mode == "oidc" && strings.HasPrefix(key, "bench:private:") {
				continue
			}
			eligible[key] = true
			if strings.HasPrefix(key, alphaPrefix) {
				alpha = append(alpha, key)
			} else if !anchors[key] {
				panic("fixture candidate inventory changed")
			}
		}
		// The full-word ranking anchors and unique key-field 're' in unreachable
		// outrank alpha's shared 'ha' evidence for every existing writer value.
		// Alpha documents have identical query TF/field lengths, hence equal BM25
		// scores even as global statistics change; the required key comparator
		// chooses their lexical prefix. A bounded independent score check covers
		// all initial/mixed value combinations; no cross-RPC score is frozen here.
		sort.Strings(alpha)
		limit := min(searchLimit, len(eligible))
		keys = append(keys, alpha[:limit-len(keys)]...)
		result[mode] = fixtureSearchExpectation{keys: keys, candidates: eligible}
	}
	return result
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

type seedAcknowledgements struct {
	VertexCount            int `json:"vertex_count"`
	EdgeCount              int `json:"edge_count"`
	VerticesAppliedAndLive int `json:"vertices_applied_and_live"`
	EdgesAppliedAndLive    int `json:"edges_applied_and_live"`
	VertexPutRPCs          int `json:"vertex_put_rpcs"`
	EdgePutRPCs            int `json:"edge_put_rpcs"`
}

func seedCorpus(ctx context.Context, endpoint *queryEndpoint) (seedAcknowledgements, error) {
	c := corpus()
	ack := seedAcknowledgements{VertexCount: len(c.Vertices), EdgeCount: len(c.Edges)}
	for start := 0; start < len(c.Vertices); start += 128 {
		end := min(start+128, len(c.Vertices))
		ack.VertexPutRPCs++
		response, err := endpoint.client.PutVertices(ctx, authenticated(endpoint.writer, &pb.PutVerticesRequest{Vertices: c.Vertices[start:end]}))
		if err != nil {
			return ack, err
		}
		if len(response.Msg.Outcomes) != end-start {
			return ack, errors.New("seed vertex outcome count mismatch")
		}
		for _, outcome := range response.Msg.Outcomes {
			if outcome != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
				return ack, errors.New("seed vertex not live")
			}
			ack.VerticesAppliedAndLive++
		}
	}
	for start := 0; start < len(c.Edges); start += 128 {
		end := min(start+128, len(c.Edges))
		ack.EdgePutRPCs++
		response, err := endpoint.client.PutEdges(ctx, authenticated(endpoint.writer, &pb.PutEdgesRequest{Edges: c.Edges[start:end]}))
		if err != nil {
			return ack, err
		}
		if len(response.Msg.Outcomes) != end-start {
			return ack, errors.New("seed edge outcome count mismatch")
		}
		for _, outcome := range response.Msg.Outcomes {
			if outcome != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
				return ack, errors.New("seed edge not live")
			}
			ack.EdgesAppliedAndLive++
		}
	}
	return ack, nil
}

// Retain bounded keys from the actual failing response, without values or credentials.
type searchResultFailure struct {
	Reason                string   `json:"reason"`
	Query                 string   `json:"query"`
	Limit                 int      `json:"limit"`
	Prefix                string   `json:"prefix"`
	Mode                  string   `json:"mode"`
	HitCount              int      `json:"hit_count"`
	ExpectedKeys          []string `json:"expected_keys"`
	ObservedKeys          []string `json:"observed_keys"`
	ObservedKeysTruncated bool     `json:"observed_keys_truncated"`
}

func (f *searchResultFailure) Error() string {
	return fmt.Sprintf("%s: hits=%d expected=%q observed=%q", f.Reason, f.HitCount, f.ExpectedKeys, f.ObservedKeys)
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
		response, err := endpoint.client.SearchVertices(ctx, authenticated(endpoint.reader, &pb.SearchVerticesRequest{Query: "shared", Limit: searchLimit, Projection: pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX}))
		if err != nil {
			return err
		}
		expected := expectedSearchKeys(endpoint.mode)
		fail := func(reason string) error {
			keys := make([]string, 0, min(len(response.Msg.Hits), 20))
			for _, hit := range response.Msg.Hits[:min(len(response.Msg.Hits), 20)] {
				keys = append(keys, hit.GetKey())
			}
			return &searchResultFailure{Reason: reason, Query: "shared", Limit: 20, Mode: endpoint.mode, HitCount: len(response.Msg.Hits), ExpectedKeys: expected, ObservedKeys: keys, ObservedKeysTruncated: len(response.Msg.Hits) > len(keys)}
		}
		seen := make(map[string]bool, len(expected))
		var previous *pb.SearchHit
		var alphaScore float64
		minimumAnchorScore := math.Inf(1)
		alphaSeen := false
		for _, hit := range response.Msg.Hits {
			if hit == nil || !strings.HasPrefix(hit.Key, "bench:") || hit.Vertex == nil || hit.Vertex.Key != hit.Key {
				return fail("search lost logical key or full-vertex result")
			}
			if seen[hit.Key] {
				return fail("search duplicated logical result")
			}
			seen[hit.Key] = true
			if endpoint.mode == "oidc" && strings.HasPrefix(hit.Key, "bench:private:") {
				return fail("search disclosed denied hit")
			}
			if !fixtureSearchExpectations[endpoint.mode].candidates[hit.Key] {
				return fail("search returned a nonmatching fixture candidate")
			}
			if math.IsNaN(hit.Score) || math.IsInf(hit.Score, 0) || hit.Score <= 0 {
				return fail("search returned an invalid matching score")
			}
			if previous != nil && (hit.Score > previous.Score || hit.Score == previous.Score && hit.Key < previous.Key) {
				return fail("search violated descending score or ascending tie order")
			}
			previous = hit
			if strings.HasPrefix(hit.Key, alphaPrefix) {
				if alphaSeen && hit.Score != alphaScore {
					return fail("search alpha candidates lost identical scoring evidence")
				}
				alphaScore, alphaSeen = hit.Score, true
			} else {
				minimumAnchorScore = min(minimumAnchorScore, hit.Score)
			}
		}
		if alphaSeen && minimumAnchorScore <= alphaScore {
			return fail("search required anchors did not outrank the derived alpha tie")
		}
		if len(seen) != len(expected) {
			return fail("search violated derived matching cardinality and limit")
		}
		for _, key := range expected {
			if !seen[key] {
				return fail("search mode-specific expected result missing")
			}
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
				return errors.New("role denial was not enforced")
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

func expectedSearchKeys(mode string) []string {
	return fixtureSearchExpectations[mode].keys
}

func requiredRankingKeys(mode string) []string {
	keys := []string{"bench:ranking:a", "bench:ranking:b"}
	if mode == "off" {
		keys = append(keys, hiddenHit)
	}
	return keys
}
