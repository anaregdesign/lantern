package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
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
	endpoint := &queryEndpoint{mode: "oidc", client: queryResultClient{hits: []*pb.SearchHit{{Key: "bench:ranking:a", Score: 2, Vertex: &pb.Vertex{Key: "bench:ranking:a"}}, {Key: "bench:ranking:b", Score: 2, Vertex: &pb.Vertex{Key: "bench:ranking:b"}}, {Key: hiddenHit, Score: 2, Vertex: &pb.Vertex{Key: hiddenHit}}}}}
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

func fixtureResultHits(keys ...string) []*pb.SearchHit {
	var out []*pb.SearchHit
	for _, key := range keys {
		score := 2.0
		if strings.HasPrefix(key, alphaPrefix) {
			score = 1
		}
		out = append(out, &pb.SearchHit{Key: key, Score: score, Vertex: &pb.Vertex{Key: key}})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func TestSearchRequiresExactModeSpecificNonemptyResults(t *testing.T) {
	hits := func(keys ...string) []*pb.SearchHit {
		return fixtureResultHits(keys...)
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

func TestSearchExpectationDerivesAsciiCandidatesAndLimit(t *testing.T) {
	terms := fixtureSearchTerms("shared")
	for _, term := range []string{"word:shared", "gram:sh", "gram:ha", "gram:ar", "gram:re", "gram:ed"} {
		if !terms[term] {
			t.Fatal("query term inventory incomplete", term)
		}
	}
	if len(terms) != 6 || fixtureSearchTerms("s h")["gram:sh"] || fixtureSearchTerms("ha")["gram:ha"] {
		t.Fatal("fixture analyzer crossed boundaries or added exact-width document grams")
	}
	for _, mode := range []string{"off", "oidc"} {
		e := fixtureSearchExpectations[mode]
		if len(e.candidates) != map[string]int{"off": 36, "oidc": 35}[mode] || len(e.keys) != min(searchLimit, len(e.candidates)) || !e.candidates[unreachable] || e.candidates[hiddenBridge] || e.candidates[hiddenHit] != (mode == "off") {
			t.Fatal("matching/auth/limit derived from the wrong corpus", mode, e)
		}
		alphaCount := 0
		for key := range e.candidates {
			if strings.HasPrefix(key, alphaPrefix) {
				alphaCount++
			}
		}
		if alphaCount != 32 {
			t.Fatal("complete seeded alpha tie group missing")
		}
	}
}

// A fixture-only independent scalar BM25 calculation: no production index,
// scorer, live hit list, authorization implementation or timed query is used.
func fixtureFieldCounts(text string) map[string]int {
	counts := map[string]int{}
	for _, word := range strings.FieldsFunc(text, func(r rune) bool { return (r < 'a' || r > 'z') && (r < '0' || r > '9') }) {
		counts["word:"+word]++
		if len(word) > 2 {
			for i := 0; i+2 <= len(word); i++ {
				counts["gram:"+word[i:i+2]]++
			}
		}
	}
	return counts
}

func fixtureIndependentScores(a, private string) map[string]float64 {
	documents := map[string][2]map[string]int{}
	for _, v := range corpus().Vertices {
		text := v.Value.(*pb.Vertex_String_).String_
		if v.Key == "bench:ranking:a" {
			text = a
		}
		if v.Key == hiddenHit {
			text = private
		}
		documents[v.Key] = [2]map[string]int{fixtureFieldCounts(v.Key), fixtureFieldCounts(text)}
	}
	scores := map[string]float64{}
	for term := range fixtureSearchTerms("shared") {
		class := "word:"
		classWeight := 1.0
		if strings.HasPrefix(term, "gram:") {
			class, classWeight = "gram:", .2
		}
		for field := 0; field < 2; field++ {
			fieldWeight := 1.0
			if field == 0 {
				fieldWeight = 1.75
			}
			df, n, total := 0, 0, 0
			lengths := map[string]int{}
			for key, fields := range documents {
				for t, count := range fields[field] {
					if strings.HasPrefix(t, class) {
						lengths[key] += count
					}
				}
				if lengths[key] > 0 {
					n++
					total += lengths[key]
				}
				if fields[field][term] > 0 {
					df++
				}
			}
			if df == 0 {
				continue
			}
			avg := float64(total) / float64(n)
			idf := math.Log(1 + (float64(n-df)+.5)/(float64(df)+.5))
			for key, fields := range documents {
				tf := float64(fields[field][term])
				if tf == 0 {
					continue
				}
				denominator := tf + 1.2*(1-.75+.75*float64(lengths[key])/avg)
				scores[key] += fieldWeight * classWeight * idf * tf * (1.2 + 1) / denominator
			}
		}
	}
	return scores
}

func TestSearchFixtureIndependentScoreBoundsAcrossExistingWriterStates(t *testing.T) {
	short, longer := "shared searchable ordinary document", "shared ordinary longer updated searchable document"
	for _, a := range []string{short, longer} {
		for _, private := range []string{"shared shared shared shared searchable document", short, longer} {
			scores := fixtureIndependentScores(a, private)
			if len(scores) != 36 {
				t.Fatal("independent matching inventory changed", len(scores))
			}
			alphaScore, count := scores[alphaPrefix+"00"], 0
			for key, score := range scores {
				if strings.HasPrefix(key, alphaPrefix) {
					count++
					if score != alphaScore {
						t.Fatal("alpha TF/field lengths do not tie", key)
					}
				} else if !(score > alphaScore) {
					t.Fatal("required anchor fell outside tie boundary", key, score, alphaScore)
				}
			}
			if count != 32 {
				t.Fatal("incomplete seeded tie group", count)
			}
		}
	}
}

func TestSearchRejectsFullSizeWrongCandidatesMissingAnchorsLeakAndTieOrder(t *testing.T) {
	for _, mode := range []string{"off", "oidc"} {
		for _, mutation := range []func([]*pb.SearchHit) []*pb.SearchHit{
			func(h []*pb.SearchHit) []*pb.SearchHit { return h[:len(h)-1] },
			func(h []*pb.SearchHit) []*pb.SearchHit { h[len(h)-1] = h[0]; return h },
			func(h []*pb.SearchHit) []*pb.SearchHit {
				h[len(h)-1] = fixtureResultHits("bench:community:alpha:31")[0]
				return h
			},
			func(h []*pb.SearchHit) []*pb.SearchHit { h[len(h)-1] = fixtureResultHits(hiddenBridge)[0]; return h },
			func(h []*pb.SearchHit) []*pb.SearchHit { h[0].Score = math.NaN(); return h },
			func(h []*pb.SearchHit) []*pb.SearchHit { h[0].Score = math.Inf(1); return h },
			func(h []*pb.SearchHit) []*pb.SearchHit { h[len(h)-1].Score = 0; return h },
			func(h []*pb.SearchHit) []*pb.SearchHit { h[0], h[len(h)-1] = h[len(h)-1], h[0]; return h },
			func(h []*pb.SearchHit) []*pb.SearchHit { h[len(h)-1], h[len(h)-2] = h[len(h)-2], h[len(h)-1]; return h },
			func(h []*pb.SearchHit) []*pb.SearchHit {
				for _, hit := range h {
					if strings.HasPrefix(hit.Key, alphaPrefix) {
						hit.Score = 1.01
						break
					}
				}
				return h
			},
			func(h []*pb.SearchHit) []*pb.SearchHit {
				for _, hit := range h {
					hit.Score = 1
				}
				sort.Slice(h, func(i, j int) bool { return h[i].Key < h[j].Key })
				return h
			},
			func(h []*pb.SearchHit) []*pb.SearchHit {
				keys := []string{}
				for _, hit := range h {
					if hit.Key != "bench:ranking:a" {
						keys = append(keys, hit.Key)
					}
				}
				return fixtureResultHits(append(keys, "bench:community:alpha:31")...)
			},
		} {
			hits := mutation(fixtureResultHits(expectedSearchKeys(mode)...))
			if queryOnce(t.Context(), &queryEndpoint{mode: mode, client: queryResultClient{hits: hits}}, "search") == nil {
				t.Fatal("bad full-size selection/cardinality/score/tie passed", mode)
			}
		}
	}
	hits := fixtureResultHits(expectedSearchKeys("oidc")...)
	hits[len(hits)-1] = fixtureResultHits(hiddenHit)[0]
	if queryOnce(t.Context(), &queryEndpoint{mode: "oidc", client: queryResultClient{hits: hits}}, "search") == nil {
		t.Fatal("same-size private leak passed")
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
		if len(failure.ObservedKeys) != min(len(hits), 20) || failure.ObservedKeysTruncated != (len(hits) > 20) || len(failure.ExpectedKeys) != 20 {
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
