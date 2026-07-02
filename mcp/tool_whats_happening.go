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
	Key string `json:"key" jsonschema:"Seed to look around: a resource key (repo.lantern.core.graphcache), an agent (agents.<id>), or a note (notes.<id>). A key nobody has touched returns a well-formed empty context, not an error."`
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
	Agents    []happeningAgent    `json:"agents"`
	Resources []happeningResource `json:"resources"`
	Notes     []happeningNote     `json:"notes"`
	Claims    []liveClaim         `json:"claims"`
	Empty     bool                `json:"empty"`
}

const whatsHappeningDescription = "THE situational-awareness read: the active neighborhood of a resource, agent, or note RIGHT NOW — which agents are on it, what else they are co-working (activity spreads through the decaying graph), live notes concerning it, and live claims on the nearby resources. Everything is recency-weighted and self-cleaning: an agent that went quiet, a stale note, an expired claim simply are not there. Call it before touching a resource (who else is on this?), when picking up a task (what is the current state of play?), or on a sibling agent's id to see their working set. A never-touched key returns an empty context — that itself is the answer: nobody is there."

// happeningSeeder abstracts the seed-neighborhood source so the graph
// walk can be swapped (BFS Illuminate today; the #845 local-community
// arm is the designed upgrade) without touching the classification.
type happeningSeeder func(ctx context.Context, lc lanternClient, seed string) (*client.Graph, error)

// bfsSeeder is the default neighborhood source: a 2-hop, fan-out-16 BFS.
func bfsSeeder(ctx context.Context, lc lanternClient, seed string) (*client.Graph, error) {
	return lc.Illuminate(ctx, seed, client.WithBFS(client.BFSOpts{Step: 2, FanOut: 16}))
}

func registerWhatsHappening(srv *mcp.Server, lc lanternClient) {
	registerWhatsHappeningWithSeeder(srv, lc, bfsSeeder)
}

func registerWhatsHappeningWithSeeder(srv *mcp.Server, lc lanternClient, seeder happeningSeeder) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "whats_happening",
		Description: whatsHappeningDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in whatsHappeningInput) (*mcp.CallToolResult, whatsHappeningOutput, error) {
		if in.Key == "" {
			return nil, whatsHappeningOutput{}, fmt.Errorf("whats_happening: key must not be empty")
		}
		out := whatsHappeningOutput{
			Seed:      in.Key,
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
		g, err := seeder(ctx, lc, in.Key)
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
