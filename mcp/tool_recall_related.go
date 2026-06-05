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

// recallRelatedObjective is the string-enum the LLM passes for the
// server-side optimization strategy. We accept the friendly names
// {none, mst, max-st, spt, inverse-spt} and translate to the SDK enum.
type recallRelatedObjective string

const (
	objectiveNone       recallRelatedObjective = "none"
	objectiveMST        recallRelatedObjective = "mst"
	objectiveMaxST      recallRelatedObjective = "max-st"
	objectiveSPT        recallRelatedObjective = "spt"
	objectiveInverseSPT recallRelatedObjective = "inverse-spt"
)

type recallRelatedInput struct {
	Seed      string                 `json:"seed"                jsonschema:"Starting fact key for the walk. The seed itself is returned at depth 0."`
	Step      uint32                 `json:"step,omitempty"      jsonschema:"BFS depth (default 2). Larger values explore further at quadratic cost. Server enforces a hard cap."`
	K         uint32                 `json:"k,omitempty"         jsonschema:"Per-hop fan-out: keep the top-k strongest outgoing edges at each step (default 8). Server enforces a hard cap."`
	Objective recallRelatedObjective `json:"objective,omitempty" jsonschema:"Optional post-processing strategy on the illuminated subgraph. one of: none (default - return raw BFS), mst (minimum spanning tree), max-st (maximum spanning tree), spt (shortest-path tree from seed), inverse-spt (shortest-path tree TO seed)."`
}

type recallRelatedNeighbor struct {
	Key    string  `json:"key"`
	Weight float32 `json:"weight"`
	Value  any     `json:"value,omitempty"`
}

type recallRelatedOutput struct {
	Seed      string                  `json:"seed"`
	Objective string                  `json:"objective"`
	Count     int                     `json:"count"`
	Neighbors []recallRelatedNeighbor `json:"neighbors"`
}

const recallRelatedDescription = "Walk Lantern's graph from a seed key, returning the related facts with their cumulative edge weights. Use step + k to bound exploration; use objective to apply a graph-theoretic reduction (mst / max-st / spt / inverse-spt). IMPORTANT: recall does NOT refresh TTL for any vertex or edge visited; weak relations will still decay on schedule."

func registerRecallRelated(srv *mcp.Server, lc lanternClient) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "recall_related",
		Description: recallRelatedDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in recallRelatedInput) (*mcp.CallToolResult, recallRelatedOutput, error) {
		if in.Seed == "" {
			return nil, recallRelatedOutput{}, fmt.Errorf("recall_related: seed must not be empty")
		}
		objective := in.Objective
		if objective == "" {
			objective = objectiveNone
		}
		opt, err := mapObjective(objective)
		if err != nil {
			return nil, recallRelatedOutput{}, fmt.Errorf("recall_related: %w", err)
		}
		opts := []client.IlluminateOption{client.WithOptimization(opt)}
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
			Objective: string(objective),
			Count:     len(neighbors),
			Neighbors: neighbors,
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("Recalled %d related facts for seed %q (objective=%s). Recall did NOT refresh TTL.", out.Count, in.Seed, out.Objective),
			}},
		}, out, nil
	})
}

// mapObjective converts the friendly enum string into the SDK's
// Optimization constant. Unknown values return an InvalidArgument-style
// error.
func mapObjective(o recallRelatedObjective) (client.Optimization, error) {
	switch o {
	case objectiveNone:
		return client.OptimizationUnspecified, nil
	case objectiveMST:
		return client.OptimizationMinimumSpanningTree, nil
	case objectiveMaxST:
		return client.OptimizationMaximumSpanningTree, nil
	case objectiveSPT:
		return client.OptimizationShortestPathTree, nil
	case objectiveInverseSPT:
		return client.OptimizationShortestPathTreeInverse, nil
	}
	return client.OptimizationUnspecified, fmt.Errorf("unknown objective %q (want one of: none, mst, max-st, spt, inverse-spt)", string(o))
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
