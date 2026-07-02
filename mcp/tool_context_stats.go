package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type contextStatsInput struct {
	// No parameters: the stats are a fleet-wide glance.
}

type contextStatsOutput struct {
	Agents    int `json:"agents"`
	Claims    int `json:"claims"`
	Notes     int `json:"notes"`
	Resources int `json:"resources"`
}

const contextStatsDescription = "Fleet activity overview in four numbers: live agents, live claims, live notes, and tracked resources. Everything counted is currently alive (TTL already pruned the rest), so a shrinking count means the fleet is going quiet, not that data was lost. Cheap — safe to call at session start to decide whether the working context is worth reading in detail."

func registerContextStats(srv *mcp.Server, lc lanternClient) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "context_stats",
		Description: contextStatsDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ contextStatsInput) (*mcp.CallToolResult, contextStatsOutput, error) {
		var out contextStatsOutput
		total := 0
		for _, probe := range []struct {
			prefix string
			dst    *int
		}{
			{agentKeyPrefix, &out.Agents},
			{claimKeyPrefix, &out.Claims},
			{noteKeyPrefix, &out.Notes},
			{"", &total},
		} {
			n, err := lc.CountVerticesByPrefix(ctx, probe.prefix)
			if err != nil {
				return nil, contextStatsOutput{}, mapSDKError("context_stats", err)
			}
			*probe.dst = int(n)
		}
		// Resources = everything that is not one of the reserved prefixes.
		// Approximate by subtraction; foreign keys sharing the server (e.g.
		// legacy memory-profile data) count as resources, which is honest:
		// they ARE part of the shared keyspace.
		out.Resources = max(total-out.Agents-out.Claims-out.Notes, 0)

		var b strings.Builder
		fmt.Fprintf(&b, "Working context: %d agent(s), %d claim(s), %d note(s), %d tracked resource(s).",
			out.Agents, out.Claims, out.Notes, out.Resources)
		if out.Agents == 0 && out.Claims == 0 && out.Notes == 0 {
			b.WriteString(" The fleet is quiet — announce yourself to start the board.")
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: b.String()}},
		}, out, nil
	})
}
