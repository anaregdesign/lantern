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
	From      string  `json:"from"`
	To        string  `json:"to"`
	Bucket    string  `json:"bucket"`
	Weight    float32 `json:"weight"`
	ExpiresAt string  `json:"expires_at"`
}

const rememberRelationDescription = "Add (or reinforce) a directed relation from one fact to another. IMPORTANT: writes are ADDITIVE — writing the same relation twice STRENGTHENS it, it does not idempotently overwrite. This is the Hebbian-style memory primitive: frequent short-TTL writes accumulate into strong associations, while weak relations decay. Use remember_fact to ensure the endpoint keys exist."

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
		d := r.Resolve(bucket)
		if err := lc.AddEdge(ctx, in.From, in.To, weight, d); err != nil {
			return nil, rememberRelationOutput{}, mapSDKError("remember_relation", err)
		}
		out := rememberRelationOutput{
			From:      in.From,
			To:        in.To,
			Bucket:    bucket.String(),
			Weight:    weight,
			ExpiresAt: time.Now().Add(d).UTC().Format(time.RFC3339),
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("Added relation %q -> %q (+%.2f, bucket=%s, expires≈%s). Repeated calls strengthen.", in.From, in.To, weight, out.Bucket, out.ExpiresAt),
			}},
		}, out, nil
	})
}
