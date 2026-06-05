package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/anaregdesign/lantern/mcp/internal/value"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type recallFactInput struct {
	Key string `json:"key" jsonschema:"Key previously written by remember_fact. Lookup is exact (not prefix); use list_under to enumerate a namespace."`
}

// recallFactOutput is the structured result. Found mirrors presence so the
// LLM can branch without parsing the human-readable Text content. A nil
// vertex (proto Vertex_Nil tombstone) is treated as found=true with
// value=null — distinct from missing.
type recallFactOutput struct {
	Found     bool   `json:"found"`
	Key       string `json:"key"`
	Value     any    `json:"value,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

const recallFactDescription = "Look up a single fact by exact key. Returns {found=false} for missing keys (this is a structured result, NOT a tool error). IMPORTANT: recalling a fact does NOT refresh its TTL — the fact will still decay on schedule. To extend a fact's lifetime, call remember_fact again."

func registerRecallFact(srv *mcp.Server, lc lanternClient) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "recall_fact",
		Description: recallFactDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in recallFactInput) (*mcp.CallToolResult, recallFactOutput, error) {
		if in.Key == "" {
			return nil, recallFactOutput{}, fmt.Errorf("recall_fact: key must not be empty")
		}
		v, err := lc.GetVertex(ctx, in.Key)
		if err != nil {
			if errors.Is(err, client.ErrNotFound) {
				out := recallFactOutput{Found: false, Key: in.Key}
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{
						Text: fmt.Sprintf("No fact stored for %q.", in.Key),
					}},
				}, out, nil
			}
			return nil, recallFactOutput{}, mapSDKError("recall_fact", err)
		}
		out := recallFactOutput{
			Found: true,
			Key:   in.Key,
			Value: value.FromVertex(v),
		}
		if exp := client.VertexExpiration(v); !exp.IsZero() {
			out.ExpiresAt = exp.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("Recalled %q (expires=%s). Recall did NOT refresh TTL.", in.Key, out.ExpiresAt),
			}},
		}, out, nil
	})
}
