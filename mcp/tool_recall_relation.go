package mcp

import (
	"context"
	"errors"
	"fmt"

	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type recallRelationInput struct {
	From string `json:"from" jsonschema:"Tail vertex key. The lookup is directed: this checks the edge from→to only, never the reverse. Use recall_related to walk a neighbourhood instead of probing one edge."`
	To   string `json:"to"   jsonschema:"Head vertex key."`
}

// recallRelationOutput is the structured result. Found mirrors presence so
// the LLM can branch without parsing the human-readable Text. Weight is the
// current accumulated edge weight (additive writes have already been summed
// server-side); it is omitted when the edge is missing. ExpiresAt is omitted
// when the edge carries no expiration.
type recallRelationOutput struct {
	Found     bool    `json:"found"`
	From      string  `json:"from"`
	To        string  `json:"to"`
	Weight    float32 `json:"weight,omitempty"`
	ExpiresAt string  `json:"expires_at,omitempty"`
}

const recallRelationDescription = "Read a single directed relation by its exact (from, to) endpoints, returning its current accumulated weight and decay horizon. Call this PROACTIVELY when you need to know whether — and how strongly — two specific facts are connected (e.g. before relying on an association you wrote earlier). Direction matters: this checks from→to only, not the reverse. Returns {found=false} for a missing or fully-decayed edge (this is a structured result, NOT a tool error). IMPORTANT: recalling a relation does NOT refresh its TTL or weight — to strengthen it, call remember_relation again (weights are additive). To explore a whole neighbourhood instead of one edge, use recall_related."

func registerRecallRelation(srv *mcp.Server, lc lanternClient) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "recall_relation",
		Description: recallRelationDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in recallRelationInput) (*mcp.CallToolResult, recallRelationOutput, error) {
		if in.From == "" || in.To == "" {
			return nil, recallRelationOutput{}, fmt.Errorf("recall_relation: both from and to must be non-empty")
		}
		e, err := lc.GetEdge(ctx, in.From, in.To)
		if err != nil {
			if errors.Is(err, client.ErrNotFound) {
				return notFoundRelation(in.From, in.To), recallRelationOutput{Found: false, From: in.From, To: in.To}, nil
			}
			return nil, recallRelationOutput{}, mapSDKError("recall_relation", err)
		}
		// Defensive: a nil edge with no error is treated as missing so the
		// tool never reports found=true with no weight.
		if e == nil {
			return notFoundRelation(in.From, in.To), recallRelationOutput{Found: false, From: in.From, To: in.To}, nil
		}
		out := recallRelationOutput{
			Found:  true,
			From:   in.From,
			To:     in.To,
			Weight: e.GetWeight(),
		}
		if exp := client.EdgeExpiration(e); !exp.IsZero() {
			out.ExpiresAt = exp.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		text := fmt.Sprintf("Relation %q -> %q has weight %.2f", in.From, in.To, out.Weight)
		if out.ExpiresAt != "" {
			text += fmt.Sprintf(", expires=%s", out.ExpiresAt)
		} else {
			text += " (no expiration)"
		}
		text += ". Recall did NOT refresh TTL or weight."
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	})
}

func notFoundRelation(from, to string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("No relation stored from %q to %q.", from, to),
		}},
	}
}
