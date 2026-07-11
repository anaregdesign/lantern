package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type whatsHappeningInput struct {
	Key       string                   `json:"key" jsonschema:"Seed to look around: a resource key (repo.lantern.core.graphcache), an agent (agents.<id>), or a note (notes.<id>). A key nobody has touched returns a well-formed empty context, not an error."`
	Weighting string                   `json:"weighting,omitempty" jsonschema:"Optional shared edge weighting: raw (default) | tfidf | bm25."`
	BFS       *whatsHappeningBFS       `json:"bfs,omitempty" jsonschema:"Select bounded BFS. Omit every family arm for the safe BFS default (step 2, fan_out 16). Mutually exclusive with ppr and community."`
	PPR       *whatsHappeningPPR       `json:"ppr,omitempty" jsonschema:"Select Personalized PageRank relevance. Mutually exclusive with bfs and community."`
	Community *whatsHappeningCommunity `json:"community,omitempty" jsonschema:"Select local community extraction. Mutually exclusive with bfs and ppr."`
}

type whatsHappeningBFS struct {
	Step      uint32 `json:"step,omitempty" jsonschema:"Traversal depth; default 2 when omitted."`
	FanOut    uint32 `json:"fan_out,omitempty" jsonschema:"Per-hop top-k bound; default 16 when omitted."`
	Reduction string `json:"reduction,omitempty" jsonschema:"Optional tree view: none (default) | mst | spt."`
	Objective string `json:"objective,omitempty" jsonschema:"Weight direction: maximize (default) | minimize."`
}

type whatsHappeningPPR struct {
	TopN        uint32  `json:"top_n,omitempty" jsonschema:"Maximum relevance-star vertices; default 16 when omitted."`
	RestartProb float32 `json:"restart_prob,omitempty" jsonschema:"Teleport probability in (0,1); 0 uses the server default."`
	Epsilon     float32 `json:"epsilon,omitempty" jsonschema:"Positive forward-push threshold; 0 uses the server default."`
}

type whatsHappeningCommunity struct {
	MaxSize     uint32  `json:"max_size,omitempty" jsonschema:"Upper bound on community size; default 32 when omitted."`
	RestartProb float32 `json:"restart_prob,omitempty" jsonschema:"Teleport probability in (0,1); 0 uses the server default."`
	Epsilon     float32 `json:"epsilon,omitempty" jsonschema:"Positive forward-push threshold; 0 uses the server default."`
	Reduction   string  `json:"reduction,omitempty" jsonschema:"Optional tree view: none (default) | mst | spt."`
	Objective   string  `json:"objective,omitempty" jsonschema:"Tree weight direction: maximize (default) | minimize."`
}

type happeningAgent struct {
	AgentID string  `json:"agent_id"`
	Task    string  `json:"task,omitempty"`
	Weight  float32 `json:"weight,omitempty"`
}

type happeningResource struct {
	Key    string  `json:"key"`
	Weight float32 `json:"weight,omitempty"`
}

type happeningNote struct {
	NoteID   string `json:"note_id"`
	Text     string `json:"text,omitempty"`
	Author   string `json:"author,omitempty"`
	Severity string `json:"severity,omitempty"`
}

type whatsHappeningOutput struct {
	Seed      string              `json:"seed"`
	Family    string              `json:"family"`
	Agents    []happeningAgent    `json:"agents"`
	Resources []happeningResource `json:"resources"`
	Notes     []happeningNote     `json:"notes"`
	Claims    []liveClaim         `json:"claims"`
	Empty     bool                `json:"empty"`
}

const whatsHappeningDescription = "THE situational-awareness read: the active neighborhood of a resource, agent, or note RIGHT NOW — which agents are on it, what else they are co-working, live notes, and live claims. Omit traversal arms for the safe bounded BFS default, or select exactly one typed bfs, ppr, or community arm; family-specific knobs cannot leak into another family. Everything is recency-weighted and self-cleaning. A never-touched key returns an empty context — that itself is the answer: nobody is there."

func registerWhatsHappening(srv *mcp.Server, lc lanternClient) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "whats_happening",
		Description: whatsHappeningDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in whatsHappeningInput) (*mcp.CallToolResult, whatsHappeningOutput, error) {
		if in.Key == "" {
			return nil, whatsHappeningOutput{}, fmt.Errorf("whats_happening: key must not be empty")
		}
		family, illuminateOpts, err := whatsHappeningTraversal(in)
		if err != nil {
			return nil, whatsHappeningOutput{}, fmt.Errorf("whats_happening: %w", err)
		}
		out := whatsHappeningOutput{
			Seed:      in.Key,
			Family:    family,
			Agents:    []happeningAgent{},
			Resources: []happeningResource{},
			Notes:     []happeningNote{},
			Claims:    []liveClaim{},
		}

		// Forward neighborhood: out-edges spread from agents (agent→resource,
		// agent→note) and notes (note→resource), so seeding an agent or note
		// walks naturally. Weight per vertex = strongest incoming edge in the
		// walked subgraph — the recency/repetition heat track built up.
		weights := map[string]float32{}
		g, err := lc.Illuminate(ctx, in.Key, illuminateOpts...)
		if err != nil {
			return nil, whatsHappeningOutput{}, mapSDKError("whats_happening", err)
		}
		for _, heads := range g.Edges {
			for head, w := range heads {
				if w > weights[head] {
					weights[head] = w
				}
			}
		}
		members := map[string]bool{}
		for k := range g.Vertices {
			if k != in.Key {
				members[k] = true
			}
		}

		// Reverse edges: who points AT the seed (agents tracking a resource,
		// notes linked to it). The head-prefix scan is an index probe; the
		// exact-match filter below trims sibling keys sharing the prefix.
		revEdges, _, err := lc.ScanEdges(ctx, client.WithEdgeScanHeadPrefix(in.Key), client.WithEdgeScanLimit(200))
		if err != nil {
			return nil, whatsHappeningOutput{}, mapSDKError("whats_happening", err)
		}
		for _, e := range revEdges {
			if e.GetHead() != in.Key {
				continue
			}
			members[e.GetTail()] = true
			if w := e.GetWeight(); w > weights[e.GetTail()] {
				weights[e.GetTail()] = w
			}
		}

		// Classify members by key prefix, and collect the resource set the
		// claim lookup below probes (the seed itself counts when it is a
		// resource — its lease matters most of all).
		var agentKeys, noteKeys, resourceKeys []string
		if contextKind(in.Key) == "resource" {
			resourceKeys = append(resourceKeys, in.Key)
		}
		for k := range members {
			switch contextKind(k) {
			case "agent":
				agentKeys = append(agentKeys, k)
			case "note":
				noteKeys = append(noteKeys, k)
			case "claim":
				// claims.* vertices are reachable only if someone tracked one
				// directly; the structured lease lookup below is the real
				// source, so skip here.
			default:
				resourceKeys = append(resourceKeys, k)
			}
		}
		sort.Strings(agentKeys)
		sort.Strings(noteKeys)
		sort.Strings(resourceKeys)

		// Decode agents (live presence records) and notes in one batch each.
		if len(agentKeys) > 0 {
			found, _, err := lc.GetVertices(ctx, agentKeys)
			if err != nil {
				return nil, whatsHappeningOutput{}, mapSDKError("whats_happening", err)
			}
			live := map[string]*client.Vertex{}
			for _, v := range found {
				live[v.GetKey()] = v
			}
			for _, k := range agentKeys {
				a := happeningAgent{AgentID: strings.TrimPrefix(k, agentKeyPrefix), Weight: weights[k]}
				if v, ok := live[k]; ok {
					var rec presenceRecord
					if decodeRecord(v, &rec) {
						a.Task = rec.Task
					}
				}
				out.Agents = append(out.Agents, a)
			}
		}
		if len(noteKeys) > 0 {
			found, _, err := lc.GetVertices(ctx, noteKeys)
			if err != nil {
				return nil, whatsHappeningOutput{}, mapSDKError("whats_happening", err)
			}
			for _, v := range found {
				n := happeningNote{NoteID: strings.TrimPrefix(v.GetKey(), noteKeyPrefix)}
				var rec noteRecord
				if decodeRecord(v, &rec) {
					n.Text, n.Author, n.Severity = rec.Text, rec.Author, rec.Severity
				}
				out.Notes = append(out.Notes, n)
			}
			sort.Slice(out.Notes, func(i, j int) bool { return out.Notes[i].NoteID < out.Notes[j].NoteID })
		}
		for _, k := range resourceKeys {
			if k == in.Key {
				continue
			}
			out.Resources = append(out.Resources, happeningResource{Key: k, Weight: weights[k]})
		}
		sort.Slice(out.Resources, func(i, j int) bool {
			if out.Resources[i].Weight != out.Resources[j].Weight {
				return out.Resources[i].Weight > out.Resources[j].Weight
			}
			return out.Resources[i].Key < out.Resources[j].Key
		})

		// Live leases on the seed + nearby resources (capped — the context
		// is a glance, not an audit).
		const claimProbeCap = 20
		probe := resourceKeys
		if len(probe) > claimProbeCap {
			probe = probe[:claimProbeCap]
		}
		if len(probe) > 0 {
			keys := make([]string, len(probe))
			for i, res := range probe {
				keys[i] = claimKey(res)
			}
			found, _, err := lc.GetVertices(ctx, keys)
			if err != nil {
				return nil, whatsHappeningOutput{}, mapSDKError("whats_happening", err)
			}
			for _, v := range found {
				c := liveClaim{Resource: strings.TrimPrefix(v.GetKey(), claimKeyPrefix)}
				var rec claimRecord
				if decodeRecord(v, &rec) {
					c.Holder, c.Note, c.At = rec.Holder, rec.Note, rec.At
				}
				if exp := client.VertexExpiration(v); !exp.IsZero() {
					c.ExpiresAt = exp.UTC().Format(time.RFC3339)
				}
				out.Claims = append(out.Claims, c)
			}
			sort.Slice(out.Claims, func(i, j int) bool { return out.Claims[i].Resource < out.Claims[j].Resource })
		}

		out.Empty = len(out.Agents) == 0 && len(out.Resources) == 0 && len(out.Notes) == 0 && len(out.Claims) == 0
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: renderHappening(out)}},
		}, out, nil
	})
}

func whatsHappeningTraversal(in whatsHappeningInput) (string, []client.IlluminateOption, error) {
	selected := 0
	for _, present := range []bool{in.BFS != nil, in.PPR != nil, in.Community != nil} {
		if present {
			selected++
		}
	}
	if selected > 1 {
		return "", nil, fmt.Errorf("select at most one traversal family: bfs, ppr, or community")
	}

	weighting, err := parseHappeningWeighting(in.Weighting)
	if err != nil {
		return "", nil, err
	}
	opts := []client.IlluminateOption{client.WithWeighting(weighting)}

	switch {
	case in.PPR != nil:
		if err := validateHappeningRankParams(in.PPR.RestartProb, in.PPR.Epsilon); err != nil {
			return "", nil, err
		}
		topN := in.PPR.TopN
		if topN == 0 {
			topN = 16
		}
		return "ppr", append(opts, client.WithPPR(client.PPROpts{
			TopN: topN, RestartProb: in.PPR.RestartProb, Epsilon: in.PPR.Epsilon,
		})), nil
	case in.Community != nil:
		if err := validateHappeningRankParams(in.Community.RestartProb, in.Community.Epsilon); err != nil {
			return "", nil, err
		}
		reduction, objective, err := parseHappeningTree(in.Community.Reduction, in.Community.Objective)
		if err != nil {
			return "", nil, err
		}
		maxSize := in.Community.MaxSize
		if maxSize == 0 {
			maxSize = 32
		}
		return "community", append(opts, client.WithLocalCommunity(client.LocalCommunityOpts{
			MaxSize: maxSize, RestartProb: in.Community.RestartProb, Epsilon: in.Community.Epsilon,
			Reduction: reduction, Objective: objective,
		})), nil
	default:
		bfs := in.BFS
		if bfs == nil {
			bfs = &whatsHappeningBFS{}
		}
		reduction, objective, err := parseHappeningTree(bfs.Reduction, bfs.Objective)
		if err != nil {
			return "", nil, err
		}
		step, fanOut := bfs.Step, bfs.FanOut
		if step == 0 {
			step = 2
		}
		if fanOut == 0 {
			fanOut = 16
		}
		return "bfs", append(opts, client.WithBFS(client.BFSOpts{
			Step: step, FanOut: fanOut, Reduction: reduction, Objective: objective,
		})), nil
	}
}

func parseHappeningWeighting(raw string) (client.Weighting, error) {
	switch strings.ToLower(raw) {
	case "", "raw":
		return client.WeightingRaw, nil
	case "tfidf":
		return client.WeightingTFIDF, nil
	case "bm25":
		return client.WeightingBM25, nil
	default:
		return client.WeightingUnspecified, fmt.Errorf("weighting must be raw, tfidf, or bm25")
	}
}

func parseHappeningTree(reductionRaw, objectiveRaw string) (client.Reduction, client.Objective, error) {
	var reduction client.Reduction
	switch strings.ToLower(reductionRaw) {
	case "", "none":
		reduction = client.ReductionNone
	case "mst":
		reduction = client.ReductionMinimumSpanningTree
	case "spt":
		reduction = client.ReductionShortestPathTree
	default:
		return client.ReductionNone, client.ObjectiveUnspecified, fmt.Errorf("reduction must be none, mst, or spt")
	}

	var objective client.Objective
	switch strings.ToLower(objectiveRaw) {
	case "", "maximize":
		objective = client.ObjectiveMaximize
	case "minimize":
		objective = client.ObjectiveMinimize
	default:
		return client.ReductionNone, client.ObjectiveUnspecified, fmt.Errorf("objective must be maximize or minimize")
	}
	return reduction, objective, nil
}

func validateHappeningRankParams(restartProb, epsilon float32) error {
	if restartProb < 0 || restartProb >= 1 {
		return fmt.Errorf("restart_prob must be 0 (server default) or in (0,1)")
	}
	if epsilon < 0 {
		return fmt.Errorf("epsilon must be 0 (server default) or positive")
	}
	return nil
}

func renderHappening(out whatsHappeningOutput) string {
	if out.Empty {
		return fmt.Sprintf("Nothing is happening around %q right now — no active agents, notes, or claims. The coast is clear.", out.Seed)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Around %q right now:\n", out.Seed)
	if len(out.Agents) > 0 {
		b.WriteString("Agents:\n")
		for _, a := range out.Agents {
			fmt.Fprintf(&b, "- %s", a.AgentID)
			if a.Task != "" {
				fmt.Fprintf(&b, ": %s", a.Task)
			}
			b.WriteString("\n")
		}
	}
	if len(out.Claims) > 0 {
		b.WriteString("Claims:\n")
		for _, c := range out.Claims {
			fmt.Fprintf(&b, "- %s ← %s (expires≈%s)\n", c.Resource, c.Holder, c.ExpiresAt)
		}
	}
	if len(out.Notes) > 0 {
		b.WriteString("Notes:\n")
		for _, n := range out.Notes {
			fmt.Fprintf(&b, "- [%s] %s (by %s)\n", n.Severity, n.Text, n.Author)
		}
	}
	if len(out.Resources) > 0 {
		b.WriteString("Co-active resources:\n")
		for _, r := range out.Resources {
			fmt.Fprintf(&b, "- %s\n", r.Key)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
