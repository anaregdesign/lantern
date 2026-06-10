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

// recall_related can walk the graph in either edge direction. The default
// (out) preserves the historical behaviour — a forward BFS over out-edges
// via Illuminate. in/both add a reverse-adjacency pass so seeding a pure
// sink (a node that only has inbound edges) returns its predecessors
// instead of just the seed. See #542.

type recallRelatedDirection string

const (
	directionOut  recallRelatedDirection = "out"
	directionIn   recallRelatedDirection = "in"
	directionBoth recallRelatedDirection = "both"
)

// reverse-scan bounds for the in/both directions. The SDK evaluates a
// head-prefix edge scan as a post-filter over a full tail walk, so the
// reverse pass can be as expensive as a whole-store scan; cap the work and
// report truncation rather than walking unbounded.
const (
	recallRelatedReversePageLimit = 1000
	recallRelatedReverseScanMax   = 10000
)

type recallRelatedInput struct {
	Seed      string                 `json:"seed"                jsonschema:"Starting fact key for the walk. The seed itself is returned at depth 0."`
	Step      uint32                 `json:"step,omitempty"      jsonschema:"BFS depth (default 2). Larger values explore further at quadratic cost. Server enforces a hard cap. Applies to the out-direction walk only."`
	K         uint32                 `json:"k,omitempty"         jsonschema:"Per-hop fan-out: keep the top-k strongest outgoing edges at each step (default 8). Server enforces a hard cap. Applies to the out-direction walk only."`
	Direction recallRelatedDirection `json:"direction,omitempty" jsonschema:"Which edge direction to follow from the seed: out (default - forward BFS over out-edges, the historical behaviour), in (reverse - return the seed's direct predecessors, so seeding a pure sink is no longer empty), or both (union of the two). step/k/algorithm/objective/weighting shape the out walk; the in pass is a single bounded reverse-adjacency hop."`
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
	Direction string                  `json:"direction"`
	Algorithm string                  `json:"algorithm"`
	Objective string                  `json:"objective"`
	Weighting string                  `json:"weighting"`
	Count     int                     `json:"count"`
	Truncated bool                    `json:"truncated,omitempty"`
	Neighbors []recallRelatedNeighbor `json:"neighbors"`
}

const recallRelatedDescription = "Walk Lantern's graph from a seed key, returning the related facts with their cumulative edge weights. Call this PROACTIVELY to pull in surrounding context before answering — start from the most relevant known key. Use step + k to bound exploration. Use algorithm + objective + weighting to control how the discovered subgraph is reduced and weighted (see #410). Use direction to choose which way edges are followed: out (default, forward), in (reverse — the seed's predecessors, so seeding a pure sink is no longer empty), or both. IMPORTANT: recall does NOT refresh TTL for any vertex or edge visited; weak relations will still decay on schedule."

func registerRecallRelated(srv *mcp.Server, lc lanternClient) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "recall_related",
		Description: recallRelatedDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in recallRelatedInput) (*mcp.CallToolResult, recallRelatedOutput, error) {
		if in.Seed == "" {
			return nil, recallRelatedOutput{}, fmt.Errorf("recall_related: seed must not be empty")
		}
		direction := in.Direction
		if direction == "" {
			direction = directionOut
		}
		if err := validateDirection(direction); err != nil {
			return nil, recallRelatedOutput{}, fmt.Errorf("recall_related: %w", err)
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

		acc := newNeighborAccumulator()
		var truncated bool

		// out / both: forward BFS over out-edges via Illuminate (the
		// historical behaviour). step/k/algorithm/objective/weighting
		// shape this walk.
		if direction == directionOut || direction == directionBoth {
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
			acc.addGraph(g)
		}

		// in / both: reverse adjacency — fold in the seed's direct
		// predecessors so seeding a pure sink is no longer empty.
		if direction == directionIn || direction == directionBoth {
			preds, t, err := scanPredecessors(ctx, lc, in.Seed)
			if err != nil {
				return nil, recallRelatedOutput{}, mapSDKError("recall_related", err)
			}
			truncated = t
			for _, e := range preds {
				acc.addPredecessor(e.GetTail(), e.GetWeight())
			}
		}

		neighbors := acc.finalize(in.Seed)
		out := recallRelatedOutput{
			Seed:      in.Seed,
			Direction: string(direction),
			Algorithm: string(algorithm),
			Objective: string(objective),
			Weighting: string(weighting),
			Count:     len(neighbors),
			Truncated: truncated,
			Neighbors: neighbors,
		}
		text := fmt.Sprintf("Recalled %d related facts for seed %q (direction=%s, algorithm=%s, objective=%s, weighting=%s). Recall did NOT refresh TTL.", out.Count, in.Seed, out.Direction, out.Algorithm, out.Objective, out.Weighting)
		if truncated {
			text += fmt.Sprintf(" Reverse scan truncated at %d edges; some predecessors may be missing.", recallRelatedReverseScanMax)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
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

// validateDirection rejects an unrecognised direction. The jsonschema enum
// tag is advisory prose only — the framework does not enforce it — so the
// handler hard-rejects unknown values here (see #542 / the #551 finding on
// jsonschema-go enums).
func validateDirection(d recallRelatedDirection) error {
	switch d {
	case directionOut, directionIn, directionBoth:
		return nil
	}
	return fmt.Errorf("unknown direction %q (want one of: out, in, both)", string(d))
}

// scanPredecessors returns the edges that point directly at the seed
// (tail -> seed) for the in/both directions. The SDK evaluates a
// head-prefix edge scan as a prefix match (and as a post-filter over a
// full tail walk), so each page is filtered to head == seed exactly and
// the total work is bounded by recallRelatedReverseScanMax. When the
// budget is exhausted before the store is fully walked, truncated is true.
func scanPredecessors(ctx context.Context, lc lanternClient, seed string) (preds []*client.Edge, truncated bool, err error) {
	var cursor []byte
	scanned := 0
	for {
		edges, next, err := lc.ScanEdges(ctx,
			client.WithEdgeScanHeadPrefix(seed),
			client.WithEdgeScanLimit(recallRelatedReversePageLimit),
			client.WithEdgeScanCursor(cursor),
		)
		if err != nil {
			return nil, false, err
		}
		for _, e := range edges {
			scanned++
			if e.GetHead() == seed {
				preds = append(preds, e)
			}
		}
		if len(next) == 0 {
			break
		}
		if scanned >= recallRelatedReverseScanMax {
			truncated = true
			break
		}
		cursor = next
	}
	return preds, truncated, nil
}

// neighborAccumulator merges weighted neighbours discovered from one or
// both edge directions into a single ranked list. Out-direction vertices
// (from Illuminate) carry their payloads; reverse predecessors contribute
// key + edge weight only — the reverse scan yields edges, not vertices, so
// call recall_fact for a predecessor's payload.
type neighborAccumulator struct {
	keys    map[string]struct{}
	weights map[string]float32
	values  map[string]any
}

func newNeighborAccumulator() *neighborAccumulator {
	return &neighborAccumulator{
		keys:    map[string]struct{}{},
		weights: map[string]float32{},
		values:  map[string]any{},
	}
}

// addGraph folds an Illuminate subgraph in, mirroring the historical
// out-direction semantics: every vertex becomes a neighbour and each edge
// adds its weight to the head's cumulative score. Edge endpoints that are
// not vertices are weighted but not emitted, matching the original
// flatten behaviour.
func (a *neighborAccumulator) addGraph(g *client.Graph) {
	if g == nil {
		return
	}
	for k, v := range g.Vertices {
		a.keys[k] = struct{}{}
		if _, ok := a.weights[k]; !ok {
			a.weights[k] = 0
		}
		a.values[k] = value.FromVertex(v)
	}
	for _, heads := range g.Edges {
		for to, w := range heads {
			a.weights[to] += w
		}
	}
}

// addPredecessor folds one reverse edge (tail -> seed) in: the tail
// becomes a neighbour weighted by the edge it points along.
func (a *neighborAccumulator) addPredecessor(tail string, w float32) {
	a.keys[tail] = struct{}{}
	a.weights[tail] += w
}

// finalize produces the ranked neighbour list. The seed is always present
// (weight 0 if nothing else contributed) and always sorts first; the rest
// are ordered by descending weight then ascending key for determinism.
func (a *neighborAccumulator) finalize(seed string) []recallRelatedNeighbor {
	a.keys[seed] = struct{}{}
	if _, ok := a.weights[seed]; !ok {
		a.weights[seed] = 0
	}
	out := make([]recallRelatedNeighbor, 0, len(a.keys))
	for k := range a.keys {
		out = append(out, recallRelatedNeighbor{
			Key:    k,
			Weight: a.weights[k],
			Value:  a.values[k],
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
