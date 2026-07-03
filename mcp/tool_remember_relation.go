package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/anaregdesign/lantern/mcp/internal/ttl"
	client "github.com/anaregdesign/lantern/sdks/go"
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
	// AccumulatedWeight is the edge's resulting weight after the write, read
	// back from the store. Because writes are additive, repeating a relation
	// makes this grow — it is the signal that distinguishes a strong
	// association from a weak one. Falls back to Increment when the post-write
	// read-back is unavailable.
	AccumulatedWeight float32 `json:"accumulated_weight"`
	ExpiresAt         string  `json:"expires_at"`
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
		if _, err := lc.AddEdge(ctx, in.From, in.To, weight, d); err != nil {
			return nil, rememberRelationOutput{}, mapSDKError("remember_relation", err)
		}
		// Read the edge back so the agent sees the ACCUMULATED weight, not just
		// the increment it wrote — that read-back is the whole point of the
		// additive model. It is best-effort: the write already succeeded, so a
		// transient read failure must not fail the tool; fall back to reporting
		// this write's increment and the computed expiry.
		accumulated := weight
		expiresAt := time.Now().Add(d).UTC().Format(time.RFC3339)
		readBack := false
		if e, gerr := lc.GetEdge(ctx, in.From, in.To); gerr == nil && e != nil {
			accumulated = e.GetWeight()
			readBack = true
			if exp := client.EdgeExpiration(e); !exp.IsZero() {
				expiresAt = exp.UTC().Format(time.RFC3339)
			}
		}
		out := rememberRelationOutput{
			From:              in.From,
			To:                in.To,
			Bucket:            bucket.String(),
			Increment:         weight,
			AccumulatedWeight: accumulated,
			ExpiresAt:         expiresAt,
			Capped:            capped,
		}
		var text string
		if readBack {
			text = fmt.Sprintf("Remembered relation %q -> %q (+%.2f, now %.2f total; bucket=%s, expires≈%s). Writes are additive — repeat to strengthen.", in.From, in.To, weight, accumulated, out.Bucket, out.ExpiresAt)
		} else {
			text = fmt.Sprintf("Remembered relation %q -> %q (+%.2f; bucket=%s, expires≈%s). Writes are additive — repeat to strengthen. (Could not read back the accumulated weight.)", in.From, in.To, weight, out.Bucket, out.ExpiresAt)
		}
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
