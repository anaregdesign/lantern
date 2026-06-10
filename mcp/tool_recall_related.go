package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/anaregdesign/lantern/mcp/internal/value"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// recall_related accepts three string-enum inputs that map 1:1 to the
// Illuminate proto axes introduced in #410: algorithm × objective ×
// weighting. The friendly names match the CLI/REPL grammar so an LLM
// (and a human operator) see one consistent vocabulary across all
// surfaces.

type recallRelatedAlgorithm string

const (
	algorithmNone recallRelatedAlgorithm = "none"
	algorithmMST  recallRelatedAlgorithm = "mst"
	algorithmSPT  recallRelatedAlgorithm = "spt"
)

type recallRelatedObjective string

const (
	objectiveMin recallRelatedObjective = "min"
	objectiveMax recallRelatedObjective = "max"
)

type recallRelatedWeighting string

const (
	weightingRaw   recallRelatedWeighting = "raw"
	weightingTFIDF recallRelatedWeighting = "tfidf"
)

type recallRelatedInput struct {
	Seed      string                 `json:"seed"                jsonschema:"Starting fact key for the walk. The seed itself is returned at depth 0."`
	Step      uint32                 `json:"step,omitempty"      jsonschema:"BFS depth (default 2). Larger values explore further at quadratic cost. Server enforces a hard cap."`
	K         uint32                 `json:"k,omitempty"         jsonschema:"Per-hop fan-out: keep the top-k strongest outgoing edges at each step (default 8). Server enforces a hard cap."`
	Algorithm recallRelatedAlgorithm `json:"algorithm,omitempty" jsonschema:"Post-traversal subgraph reduction: one of: none (default - raw BFS subgraph), mst (minimum/maximum spanning tree depending on objective), spt (shortest-path tree from seed)."`
	Objective recallRelatedObjective `json:"objective,omitempty" jsonschema:"Direction of the algorithm reduction: min (default - cost-weighted, smallest tree wins) or max (relevance-weighted, largest tree wins). Ignored when algorithm=none."`
	Weighting recallRelatedWeighting `json:"weighting,omitempty" jsonschema:"Edge-weight transform applied BEFORE the walk: raw (default - edge weights as stored) or tfidf (re-score via TF-IDF over per-vertex out-edge distribution)."`
}

type recallRelatedNeighbor struct {
	Key    string  `json:"key"`
	Weight float32 `json:"weight"`
	Value  any     `json:"value,omitempty"`
}

type recallRelatedOutput struct {
	Seed      string                  `json:"seed"`
	Algorithm string                  `json:"algorithm"`
	Objective string                  `json:"objective"`
	Weighting string                  `json:"weighting"`
	Count     int                     `json:"count"`
	Neighbors []recallRelatedNeighbor `json:"neighbors"`
}

const recallRelatedDescription = "Walk Lantern's graph from a seed key, returning the related facts with their cumulative edge weights. Call this PROACTIVELY to pull in surrounding context before answering — start from the most relevant known key. Use step + k to bound exploration. Use algorithm + objective + weighting to control how the discovered subgraph is reduced and weighted (see #410). IMPORTANT: recall does NOT refresh TTL for any vertex or edge visited; weak relations will still decay on schedule."

func registerRecallRelated(srv *mcp.Server, lc lanternClient) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "recall_related",
		Description: recallRelatedDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in recallRelatedInput) (*mcp.CallToolResult, recallRelatedOutput, error) {
		if in.Seed == "" {
			return nil, recallRelatedOutput{}, fmt.Errorf("recall_related: seed must not be empty")
		}
		algorithm := in.Algorithm
		if algorithm == "" {
			algorithm = algorithmNone
		}
		objective := in.Objective
		if objective == "" {
			objective = objectiveMin
		}
		weighting := in.Weighting
		if weighting == "" {
			weighting = weightingRaw
		}
		algo, err := mapAlgorithm(algorithm)
		if err != nil {
			return nil, recallRelatedOutput{}, fmt.Errorf("recall_related: %w", err)
		}
		obj, err := mapObjective(objective)
		if err != nil {
			return nil, recallRelatedOutput{}, fmt.Errorf("recall_related: %w", err)
		}
		w, err := mapWeighting(weighting)
		if err != nil {
			return nil, recallRelatedOutput{}, fmt.Errorf("recall_related: %w", err)
		}
		opts := []client.IlluminateOption{
			client.WithAlgorithm(algo),
			client.WithObjective(obj),
			client.WithWeighting(w),
		}
		if in.Step > 0 {
			opts = append(opts, client.WithStep(in.Step))
		}
		if in.K > 0 {
			opts = append(opts, client.WithK(in.K))
		}
		g, err := lc.Illuminate(ctx, in.Seed, opts...)
		if err != nil {
			return nil, recallRelatedOutput{}, mapSDKError("recall_related", err)
		}
		neighbors := flattenNeighbors(in.Seed, g)
		out := recallRelatedOutput{
			Seed:      in.Seed,
			Algorithm: string(algorithm),
			Objective: string(objective),
			Weighting: string(weighting),
			Count:     len(neighbors),
			Neighbors: neighbors,
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("Recalled %d related facts for seed %q (algorithm=%s, objective=%s, weighting=%s). Recall did NOT refresh TTL.", out.Count, in.Seed, out.Algorithm, out.Objective, out.Weighting),
			}},
		}, out, nil
	})
}

// mapAlgorithm / mapObjective / mapWeighting translate the friendly
// MCP-input string enums into the SDK enums. Unknown values return an
// InvalidArgument-style error so the LLM gets actionable feedback.

func mapAlgorithm(a recallRelatedAlgorithm) (client.Algorithm, error) {
	switch a {
	case algorithmNone:
		return client.AlgorithmUnspecified, nil
	case algorithmMST:
		return client.AlgorithmMinimumSpanningTree, nil
	case algorithmSPT:
		return client.AlgorithmShortestPathTree, nil
	}
	return client.AlgorithmUnspecified, fmt.Errorf("unknown algorithm %q (want one of: none, mst, spt)", string(a))
}

func mapObjective(o recallRelatedObjective) (client.Objective, error) {
	switch o {
	case objectiveMin:
		return client.ObjectiveMinimize, nil
	case objectiveMax:
		return client.ObjectiveMaximize, nil
	}
	return client.ObjectiveUnspecified, fmt.Errorf("unknown objective %q (want one of: min, max)", string(o))
}

func mapWeighting(w recallRelatedWeighting) (client.Weighting, error) {
	switch w {
	case weightingRaw:
		return client.WeightingRaw, nil
	case weightingTFIDF:
		return client.WeightingTFIDF, nil
	}
	return client.WeightingUnspecified, fmt.Errorf("unknown weighting %q (want one of: raw, tfidf)", string(w))
}

// flattenNeighbors collapses the SDK Graph into a flat list of
// {key, weight, value} suitable for an LLM. The cumulative edge weight
// for each neighbour is the sum over all incoming edges from any vertex
// in the subgraph; this approximates a relevance score without leaking
// the full adjacency matrix into the tool result.
//
// The seed vertex is included with weight 0 so the caller can locate its
// payload in the response without a separate recall_fact round-trip.
// Results are sorted by descending weight, then ascending key for
// determinism.
func flattenNeighbors(seed string, g *client.Graph) []recallRelatedNeighbor {
	if g == nil {
		return nil
	}
	weights := make(map[string]float32, len(g.Vertices))
	for k := range g.Vertices {
		weights[k] = 0
	}
	for _, heads := range g.Edges {
		for to, w := range heads {
			weights[to] += w
		}
	}
	out := make([]recallRelatedNeighbor, 0, len(g.Vertices))
	for key, v := range g.Vertices {
		out = append(out, recallRelatedNeighbor{
			Key:    key,
			Weight: weights[key],
			Value:  value.FromVertex(v),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key == seed {
			return true
		}
		if out[j].Key == seed {
			return false
		}
		if out[i].Weight != out[j].Weight {
			return out[i].Weight > out[j].Weight
		}
		return strings.Compare(out[i].Key, out[j].Key) < 0
	})
	return out
}
