package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/anaregdesign/lantern/mcp/internal/identity"
	"github.com/anaregdesign/lantern/mcp/internal/ttl"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// announceInput carries the presence heartbeat payload. The agent id is
// deliberately NOT an input: it is resolved server-side from
// LANTERN_MCP_AGENT_ID (or the stable per-process fallback) so a tool
// call cannot impersonate another agent.
type announceInput struct {
	Task   string `json:"task" jsonschema:"One line describing what you are working on right now (e.g. 'refactoring auth middleware in repo.api'). Re-announce whenever the task changes AND periodically as a heartbeat - presence expires on its own if you go silent."`
	Detail string `json:"detail,omitempty" jsonschema:"Optional second line of detail (branch, ticket, ETA). Keep it short; this is a status board, not a journal."`
}

type announceOutput struct {
	AgentID   string `json:"agent_id"`
	Task      string `json:"task"`
	ExpiresAt string `json:"expires_at"`
	// Renewed is true when a live presence vertex already existed for this
	// agent (heartbeat refresh), false on first announce after an absence.
	Renewed bool `json:"renewed"`
}

const announceDescription = "Announce your presence to the shared working context: \"I am here, working on T\". Call this at session start, whenever your task changes, and periodically as a heartbeat — presence lives ~2 minutes (the transient TTL bucket) and re-announcing refreshes it, so an agent that goes silent simply disappears from list_agents. The output tells you your agent_id; other agents see your task line via list_agents and whats_happening. Do NOT use this for durable knowledge — the working context is expendable by design."

func registerAnnounce(srv *mcp.Server, lc lanternClient, r *ttl.Resolver) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "announce",
		Description: announceDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in announceInput) (*mcp.CallToolResult, announceOutput, error) {
		if in.Task == "" {
			return nil, announceOutput{}, fmt.Errorf("announce: task must not be empty")
		}
		id := identity.Resolve()
		key := agentKey(id)

		// Preserve the original since when this is a heartbeat refresh of
		// the same task line, so "since" answers "how long has this agent
		// been on this?" rather than "when did it last heartbeat?".
		now := time.Now().UTC()
		rec := presenceRecord{Task: in.Task, Detail: in.Detail, Since: now.Format(time.RFC3339)}
		renewed := false
		if prev, err := lc.GetVertex(ctx, key); err == nil {
			renewed = true
			var old presenceRecord
			if decodeRecord(prev, &old) && old.Task == in.Task && old.Since != "" {
				rec.Since = old.Since
			}
		}

		payload, err := encodeRecord(rec)
		if err != nil {
			return nil, announceOutput{}, fmt.Errorf("announce: %w", err)
		}
		d, _ := r.ResolveCapped(ttl.Transient)
		if err := lc.PutVertex(ctx, key, payload, d); err != nil {
			return nil, announceOutput{}, mapSDKError("announce", err)
		}

		out := announceOutput{
			AgentID:   id,
			Task:      in.Task,
			ExpiresAt: now.Add(d).Format(time.RFC3339),
			Renewed:   renewed,
		}
		verb := "Announced"
		if renewed {
			verb = "Refreshed"
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(
				"%s presence as %s: %q (expires≈%s; re-announce to stay visible).",
				verb, id, in.Task, out.ExpiresAt)}},
		}, out, nil
	})
}
