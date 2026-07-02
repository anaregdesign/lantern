package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/anaregdesign/lantern/mcp/internal/identity"
	"github.com/anaregdesign/lantern/mcp/internal/ttl"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type trackInput struct {
	Resources []string `json:"resources" jsonschema:"Dotted resource keys you just touched (file, module, ticket, dataset...) - e.g. ['repo.lantern.core.graphcache','ticket.LANT-42']. One call per work step with everything it touched is ideal; repetition across calls is the SIGNAL (it strengthens the association), so do not deduplicate across turns."`
}

type trackOutput struct {
	AgentID   string   `json:"agent_id"`
	Resources []string `json:"resources"`
	Tracked   int      `json:"tracked"`
}

const trackDescription = "Record \"I touched R\" into the shared activity graph. Each call adds a decaying agent->resource edge: repetition strengthens the connection, silence lets it fade (~1 hour horizon), so the graph always reflects RECENT attention, not history. Call it as you work — after editing a file, reading a ticket, querying a dataset — with the dotted resource keys involved. This is what makes whats_happening useful for everyone else; resources appear in the context automatically (no registration step). Do NOT use it for durable provenance — the decay is the feature."

func registerTrack(srv *mcp.Server, lc lanternClient, r *ttl.Resolver) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "track",
		Description: trackDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in trackInput) (*mcp.CallToolResult, trackOutput, error) {
		if len(in.Resources) == 0 {
			return nil, trackOutput{}, fmt.Errorf("track: resources must not be empty")
		}
		for _, res := range in.Resources {
			if strings.TrimSpace(res) == "" {
				return nil, trackOutput{}, fmt.Errorf("track: resources must not contain empty keys")
			}
		}
		id := identity.Resolve()
		d, _ := r.ResolveCapped(ttl.Conversation)
		edges := make([]client.EdgeInput, 0, len(in.Resources))
		for _, res := range in.Resources {
			edges = append(edges, client.EdgeInput{
				Tail:       agentKey(id),
				Head:       res,
				Weight:     1,
				Expiration: expirationIn(d),
			})
		}
		if err := lc.AddEdges(ctx, edges); err != nil {
			return nil, trackOutput{}, mapSDKError("track", err)
		}
		out := trackOutput{AgentID: id, Resources: in.Resources, Tracked: len(edges)}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(
				"Tracked %d resource(s) for %s: %s. Repetition strengthens; silence decays (~%s).",
				out.Tracked, id, strings.Join(in.Resources, ", "), d)}},
		}, out, nil
	})
}
