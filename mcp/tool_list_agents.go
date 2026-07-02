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

type listAgentsInput struct {
	// No parameters: the fleet is the scope. Expired presences are already
	// gone (TTL), so the scan is self-cleaning.
}

type activeAgent struct {
	AgentID   string `json:"agent_id"`
	Task      string `json:"task,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Since     string `json:"since,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

type listAgentsOutput struct {
	Agents []activeAgent `json:"agents"`
	Count  int           `json:"count"`
}

const listAgentsDescription = "List every agent currently active in the shared working context — who is here and what each one says it is working on. Presence expires on its own (~2 minutes without an announce), so this list is always live: no tombstones, no stale entries. Call it before picking up work to avoid colliding with a sibling, or when whats_happening shows an agent id you don't recognise."

func registerListAgents(srv *mcp.Server, lc lanternClient) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_agents",
		Description: listAgentsDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ listAgentsInput) (*mcp.CallToolResult, listAgentsOutput, error) {
		vs, _, err := lc.ScanVertices(ctx, agentKeyPrefix, client.WithScanLimit(500))
		if err != nil {
			return nil, listAgentsOutput{}, mapSDKError("list_agents", err)
		}
		out := listAgentsOutput{Agents: make([]activeAgent, 0, len(vs))}
		for _, v := range vs {
			a := activeAgent{AgentID: strings.TrimPrefix(v.GetKey(), agentKeyPrefix)}
			var rec presenceRecord
			if decodeRecord(v, &rec) {
				a.Task, a.Detail, a.Since = rec.Task, rec.Detail, rec.Since
			}
			if exp := client.VertexExpiration(v); !exp.IsZero() {
				a.ExpiresAt = exp.UTC().Format(time.RFC3339)
			}
			out.Agents = append(out.Agents, a)
		}
		sort.Slice(out.Agents, func(i, j int) bool { return out.Agents[i].AgentID < out.Agents[j].AgentID })
		out.Count = len(out.Agents)

		if out.Count == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "No agents are currently announced. You may be the first — call announce to show up here."}},
			}, out, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d agent(s) active:\n", out.Count)
		for _, a := range out.Agents {
			fmt.Fprintf(&b, "- %s: %s", a.AgentID, a.Task)
			if a.Detail != "" {
				fmt.Fprintf(&b, " (%s)", a.Detail)
			}
			b.WriteString("\n")
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: strings.TrimRight(b.String(), "\n")}},
		}, out, nil
	})
}
