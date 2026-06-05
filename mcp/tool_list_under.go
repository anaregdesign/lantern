package mcp

import (
	"context"
	"fmt"

	"github.com/anaregdesign/lantern/mcp/internal/value"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// listUnderDefaultLimit is the small default page size we apply when the
// caller omits limit. Agents that explicitly want more should pass limit
// directly; we deliberately do not expose the cursor in v1.
const listUnderDefaultLimit uint32 = 50

// listUnderMaxLimit caps the per-call response to keep tool outputs from
// blowing past the LLM context window. The server-side ScanVertices
// further clamps this against its own configured maximum.
const listUnderMaxLimit uint32 = 500

type listUnderInput struct {
	Prefix string `json:"prefix" jsonschema:"Key prefix to enumerate (e.g. user.preferences. — note the trailing dot is part of the prefix). Empty string would scan the entire keyspace and is rejected; pick a meaningful namespace."`
	Limit  uint32 `json:"limit,omitempty" jsonschema:"Maximum number of facts to return in this call (default 50, capped at 500). v1 does not expose a pagination cursor; if the response is full and you need more, narrow the prefix."`
}

type listUnderEntry struct {
	Key       string `json:"key"`
	Value     any    `json:"value,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

type listUnderOutput struct {
	Prefix     string           `json:"prefix"`
	Count      int              `json:"count"`
	HasMore    bool             `json:"has_more"`
	Entries    []listUnderEntry `json:"entries"`
	Suggestion string           `json:"suggestion,omitempty"`
}

const listUnderDescription = "Enumerate facts whose key starts with the given prefix, in ascending key order. Defaults to 50 entries, max 500. If has_more=true the result is truncated; narrow the prefix or follow the suggestion. Does NOT refresh TTL for the listed facts."

func registerListUnder(srv *mcp.Server, lc lanternClient) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_under",
		Description: listUnderDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listUnderInput) (*mcp.CallToolResult, listUnderOutput, error) {
		if in.Prefix == "" {
			return nil, listUnderOutput{}, fmt.Errorf("list_under: prefix must not be empty")
		}
		limit := in.Limit
		if limit == 0 {
			limit = listUnderDefaultLimit
		}
		if limit > listUnderMaxLimit {
			limit = listUnderMaxLimit
		}
		// Ask for one more than the caller's limit so we can detect
		// truncation without exposing the cursor. The server may still
		// return fewer than we asked for; if so, has_more = false.
		probeLimit := limit + 1
		verts, _, err := lc.ScanVertices(ctx, in.Prefix, client.WithScanLimit(probeLimit))
		if err != nil {
			return nil, listUnderOutput{}, mapSDKError("list_under", err)
		}
		hasMore := false
		if uint32(len(verts)) > limit {
			hasMore = true
			verts = verts[:limit]
		}
		entries := make([]listUnderEntry, 0, len(verts))
		for _, v := range verts {
			if v == nil {
				continue
			}
			e := listUnderEntry{Key: v.GetKey(), Value: value.FromVertex(v)}
			if exp := client.VertexExpiration(v); !exp.IsZero() {
				e.ExpiresAt = exp.UTC().Format("2006-01-02T15:04:05Z07:00")
			}
			entries = append(entries, e)
		}
		out := listUnderOutput{
			Prefix:  in.Prefix,
			Count:   len(entries),
			HasMore: hasMore,
			Entries: entries,
		}
		if hasMore {
			out.Suggestion = fmt.Sprintf("More than %d facts share this prefix. Narrow the prefix (e.g. %q + a sub-namespace) or raise limit (max %d).", limit, in.Prefix, listUnderMaxLimit)
		}
		text := fmt.Sprintf("Listed %d facts under %q (has_more=%t).", out.Count, in.Prefix, out.HasMore)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	})
}
