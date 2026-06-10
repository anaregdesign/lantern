package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/anaregdesign/lantern/mcp/internal/ttl"
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

// reinforce-on-recall defaults (#549). Reinforcement is opt-in (default
// off) so recall stays read-only unless asked. When enabled, each
// traversed edge gets an additive weight contribution via AddEdge. Because
// the store keeps every contribution as an independent (weight, expiration)
// pulse and an edge's life is the latest contribution's expiration, a
// reinforcement NEVER shortens an edge's existing horizon — it adds a
// pulse that itself decays on reinforce_ttl. Frequent use stacks pulses
// (sustained strength); sporadic use lets them fade. This is the
// edge-side analog of touch and the one deliberate exception to "recall
// does NOT refresh TTL".
const (
	defaultReinforceWeight = float32(1)
	defaultReinforceBucket = ttl.Conversation
)

// reinforceEdge identifies one traversed edge (tail -> head) so the
// reinforce pass bumps exactly the edges that fed the returned neighbours,
// and no others. See #549.
type reinforceEdge struct {
	tail string
	head string
}

type recallRelatedInput struct {
	Seed            string                 `json:"seed"                jsonschema:"Starting fact key for the walk. The seed itself is returned at depth 0."`
	Step            uint32                 `json:"step,omitempty"      jsonschema:"BFS depth (default 2). Larger values explore further at quadratic cost. Server enforces a hard cap. Applies to the out-direction walk only."`
	K               uint32                 `json:"k,omitempty"         jsonschema:"Per-hop fan-out: keep the top-k strongest outgoing edges at each step (default 8). Server enforces a hard cap. Applies to the out-direction walk only."`
	Direction       recallRelatedDirection `json:"direction,omitempty" jsonschema:"Which edge direction to follow from the seed: out (default - forward BFS over out-edges, the historical behaviour), in (reverse - return the seed's direct predecessors, so seeding a pure sink is no longer empty), or both (union of the two). step/k/algorithm/objective/weighting shape the out walk; the in pass is a single bounded reverse-adjacency hop."`
	Algorithm       recallRelatedAlgorithm `json:"algorithm,omitempty" jsonschema:"Post-traversal subgraph reduction: one of: none (default - raw BFS subgraph), mst (minimum/maximum spanning tree depending on objective), spt (shortest-path tree from seed)."`
	Objective       recallRelatedObjective `json:"objective,omitempty" jsonschema:"Direction of the algorithm reduction: min (default - cost-weighted, smallest tree wins) or max (relevance-weighted, largest tree wins). Ignored when algorithm=none."`
	Weighting       recallRelatedWeighting `json:"weighting,omitempty" jsonschema:"Edge-weight transform applied BEFORE the walk: raw (default - edge weights as stored) or tfidf (re-score via TF-IDF over per-vertex out-edge distribution)."`
	Reinforce       bool                   `json:"reinforce,omitempty"        jsonschema:"Opt in (default false) to strengthening the edges this walk actually traverses. Each traversed edge gains an additive weight pulse via the same additive model as remember_relation; recall stays read-only when false. This is the Hebbian use-strengthens-memory loop — the edge-side analog of touch and the one deliberate exception to 'recall does NOT refresh TTL'."`
	ReinforceWeight float32                `json:"reinforce_weight,omitempty" jsonschema:"Weight added to each traversed edge when reinforce=true (default 1.0). Additive: the pulse stacks on the edge's existing weight. Ignored when reinforce=false."`
	ReinforceTTL    string                 `json:"reinforce_ttl,omitempty"    jsonschema:"Decay horizon of the reinforcement pulse when reinforce=true (default conversation). The store keeps each contribution independently, so a short horizon NEVER shortens the edge's existing life — it adds a pulse that itself decays on this horizon; frequent use stacks pulses. One of: seconds, transient, turn, conversation, task, workday, day, week, sprint, month, quarter, durable. Ignored when reinforce=false."`
}

type recallRelatedNeighbor struct {
	Key    string  `json:"key"`
	Weight float32 `json:"weight"`
	Value  any     `json:"value,omitempty"`
}

type recallRelatedOutput struct {
	Seed      string `json:"seed"`
	Direction string `json:"direction"`
	Algorithm string `json:"algorithm"`
	Objective string `json:"objective"`
	Weighting string `json:"weighting"`
	Count     int    `json:"count"`
	Truncated bool   `json:"truncated,omitempty"`
	// Reinforced is the number of traversed edges that received a weight
	// pulse this call. Zero (and omitted) unless reinforce=true. It can be
	// lower than the number of edges walked if some best-effort bump writes
	// failed — the recall result itself is unaffected.
	Reinforced int `json:"reinforced,omitempty"`
	// ReinforceCapped is true when the reinforcement pulse's TTL was clamped
	// down to LANTERN_MCP_MAX_TTL before writing.
	ReinforceCapped bool                    `json:"reinforce_capped,omitempty"`
	Neighbors       []recallRelatedNeighbor `json:"neighbors"`
}

const recallRelatedDescription = "Walk Lantern's graph from a seed key, returning the related facts with their cumulative edge weights. Call this PROACTIVELY to pull in surrounding context before answering — start from the most relevant known key. Use step + k to bound exploration. Use algorithm + objective + weighting to control how the discovered subgraph is reduced and weighted (see #410). Use direction to choose which way edges are followed: out (default, forward), in (reverse — the seed's predecessors, so seeding a pure sink is no longer empty), or both. By default recall does NOT refresh TTL for any vertex or edge visited; weak relations will still decay on schedule. Set reinforce=true to OPT IN to strengthening the edges you actually traverse — each gains an additive, decaying weight pulse (the Hebbian use-strengthens-memory loop and the edge-side analog of touch); this is the one deliberate exception to the no-refresh rule, and because contributions stack independently it never shortens an edge's existing life."

func registerRecallRelated(srv *mcp.Server, lc lanternClient, r *ttl.Resolver) {
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
		// Resolve the reinforcement horizon up front so a bad bucket fails
		// fast — before any read work — consistent with the enum validations
		// above. Only consulted when reinforce is requested.
		reinforceBucket := defaultReinforceBucket
		if in.Reinforce && in.ReinforceTTL != "" {
			b, err := ttl.ParseBucket(in.ReinforceTTL)
			if err != nil {
				return nil, recallRelatedOutput{}, fmt.Errorf("recall_related: %w", err)
			}
			reinforceBucket = b
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
				acc.addPredecessor(e.GetTail(), e.GetHead(), e.GetWeight())
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

		// Reinforce-on-recall (#549): opt-in, default off. Replay each
		// traversed edge through AddEdge so it gains an additive weight pulse
		// on the reinforce_ttl horizon. The store keeps contributions
		// independently, so this strengthens (and at most extends) the edge
		// without ever shortening its existing life. It is a best-effort side
		// effect: the recall already succeeded and its neighbours are valid,
		// so a failed bump must not fail the tool — count the bumps that
		// landed and note any shortfall. No per-edge read-back on this hot
		// path (see the #540/#547 findings); call recall_relation to observe
		// an edge's accumulated weight.
		var (
			reinforceBump      float32
			reinforceAttempted int
		)
		if in.Reinforce {
			reinforceBump = in.ReinforceWeight
			if reinforceBump <= 0 {
				reinforceBump = defaultReinforceWeight
			}
			d, capped := r.ResolveCapped(reinforceBucket)
			visited := acc.visitedEdges()
			reinforceAttempted = len(visited)
			reinforced := 0
			for _, e := range visited {
				if err := lc.AddEdge(ctx, e.tail, e.head, reinforceBump, d); err != nil {
					continue // best-effort: the recall already succeeded
				}
				reinforced++
			}
			out.Reinforced = reinforced
			out.ReinforceCapped = capped
		}

		text := fmt.Sprintf("Recalled %d related facts for seed %q (direction=%s, algorithm=%s, objective=%s, weighting=%s).", out.Count, in.Seed, out.Direction, out.Algorithm, out.Objective, out.Weighting)
		if in.Reinforce {
			text += fmt.Sprintf(" Reinforced %d of %d traversed edge(s) with a +%.2f weight pulse (bucket=%s); this never shortens existing edge life.", out.Reinforced, reinforceAttempted, reinforceBump, reinforceBucket.String())
			if out.Reinforced < reinforceAttempted {
				text += " Some reinforcement writes failed; the recall result is unaffected."
			}
			if out.ReinforceCapped {
				text += " Reinforcement TTL was clamped to the server cap."
			}
		} else {
			text += " Recall did NOT refresh TTL."
		}
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
	// edges records the traversed edges (deduplicated, first-seen order) so
	// an opt-in reinforce pass can bump exactly the edges that contributed
	// to the returned neighbours. See #549.
	edges []reinforceEdge
	seen  map[reinforceEdge]struct{}
}

func newNeighborAccumulator() *neighborAccumulator {
	return &neighborAccumulator{
		keys:    map[string]struct{}{},
		weights: map[string]float32{},
		values:  map[string]any{},
		seen:    map[reinforceEdge]struct{}{},
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
	for from, heads := range g.Edges {
		for to, w := range heads {
			a.weights[to] += w
			a.recordEdge(from, to)
		}
	}
}

// addPredecessor folds one reverse edge (tail -> head, where head is the
// seed) in: the tail becomes a neighbour weighted by the edge it points
// along, and the edge is recorded so an opt-in reinforce pass can bump it.
func (a *neighborAccumulator) addPredecessor(tail, head string, w float32) {
	a.keys[tail] = struct{}{}
	a.weights[tail] += w
	a.recordEdge(tail, head)
}

// recordEdge remembers a traversed edge, deduplicated, so a later reinforce
// pass bumps each contributing edge exactly once. Dedup matters for
// direction=both, where the same edge can surface from both the forward
// Illuminate subgraph and the reverse predecessor scan.
func (a *neighborAccumulator) recordEdge(tail, head string) {
	e := reinforceEdge{tail: tail, head: head}
	if _, ok := a.seen[e]; ok {
		return
	}
	a.seen[e] = struct{}{}
	a.edges = append(a.edges, e)
}

// visitedEdges returns the traversed edges in first-seen order.
func (a *neighborAccumulator) visitedEdges() []reinforceEdge {
	return a.edges
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
