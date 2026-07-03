package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/anaregdesign/lantern/mcp/internal/ttl"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type rememberRelationInput struct {
	From   string  `json:"from"             jsonschema:"Tail vertex key. The relation is directed from→to; reverse traversal is not implicit."`
	To     string  `json:"to"               jsonschema:"Head vertex key."`
	TTL    string  `json:"ttl"              jsonschema:"Required decay horizon (string). The relation disappears when this TTL expires. Recalling a relation does NOT refresh its TTL — to reinforce, call remember_relation again (weights are ADDITIVE; see weight below). Valid values: seconds, transient, turn, conversation, task, workday, day, week, sprint, month, quarter, durable."`
	Weight float32 `json:"weight,omitempty" jsonschema:"Edge weight increment (default 1.0). Writes are ADDITIVE: calling remember_relation twice with weight=1 leaves an edge of weight 2. Combine frequent short-TTL writes to make strong relations dominant while weak ones decay."`
}

type rememberRelationOutput struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Bucket string `json:"bucket"`
	// Increment is the weight this single additive write contributed.
	Increment float32 `json:"increment"`
	// AccumulatedWeight is the edge's live weight after this write, reported
	// by the serving node in AddEdgeResponse.effective_weight (#897) — the
	// atomic post-own-write sum, with no follow-up read. Because writes are
	// additive, repeating a relation makes this grow; it is the signal that
	// distinguishes a strong association from a weak one.
	AccumulatedWeight float32 `json:"accumulated_weight"`
	// ExpiresAt is when THIS write's contribution decays (now + resolved TTL).
	// It is this contribution's own horizon, not necessarily the edge's latest
	// expiration across other concurrent contributions.
	ExpiresAt string `json:"expires_at"`
	// Capped is true when the bucket's nominal horizon was clamped down to
	// LANTERN_MCP_MAX_TTL before writing, so the caller knows the stored
	// expiry is shorter than the bucket label implies.
	Capped bool `json:"capped,omitempty"`
}

const rememberRelationDescription = "Add (or reinforce) a directed relation from one fact to another. Call this PROACTIVELY whenever you learn how two things connect — you do not need to be asked. IMPORTANT: writes are ADDITIVE — writing the same relation twice STRENGTHENS it, it does not idempotently overwrite. This is the Hebbian-style memory primitive: frequent short-TTL writes accumulate into strong associations, while weak relations decay, so reinforce associations you keep using. The tool returns the resulting accumulated_weight after each write, so you can watch an association get stronger and tell strong links from weak ones. Use remember_fact to ensure the endpoint keys exist."

func registerRememberRelation(srv *mcp.Server, lc lanternClient, r *ttl.Resolver) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "remember_relation",
		Description: rememberRelationDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in rememberRelationInput) (*mcp.CallToolResult, rememberRelationOutput, error) {
		if in.From == "" || in.To == "" {
			return nil, rememberRelationOutput{}, fmt.Errorf("remember_relation: both from and to must be non-empty")
		}
		bucket, err := ttl.ParseBucket(in.TTL)
		if err != nil {
			return nil, rememberRelationOutput{}, fmt.Errorf("remember_relation: %w", err)
		}
		weight := in.Weight
		if weight == 0 {
			weight = 1
		}
		d, capped := r.ResolveCapped(bucket)
		// AddEdge returns the live accumulated weight after applying this
		// contribution (AddEdgeResponse.effective_weight, #897), so the agent
		// sees the ACCUMULATED total — the whole point of the additive model —
		// without a follow-up GetEdge (which reopened a TOCTOU window and could
		// attribute a concurrent writer's weight to this call).
		accumulated, err := lc.AddEdge(ctx, in.From, in.To, weight, d)
		if err != nil {
			return nil, rememberRelationOutput{}, mapSDKError("remember_relation", err)
		}
		expiresAt := time.Now().Add(d).UTC().Format(time.RFC3339)
		out := rememberRelationOutput{
			From:              in.From,
			To:                in.To,
			Bucket:            bucket.String(),
			Increment:         weight,
			AccumulatedWeight: accumulated,
			ExpiresAt:         expiresAt,
			Capped:            capped,
		}
		text := fmt.Sprintf("Remembered relation %q -> %q (+%.2f, now %.2f total; bucket=%s, expires≈%s). Writes are additive — repeat to strengthen.", in.From, in.To, weight, accumulated, out.Bucket, out.ExpiresAt)
		if capped {
			text += fmt.Sprintf(" Note: TTL clamped to the server cap (%s); re-remember before it expires to keep the relation alive.", d)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: text,
			}},
		}, out, nil
	})
}
