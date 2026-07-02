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

type listClaimsInput struct {
	Prefix string `json:"prefix,omitempty" jsonschema:"Optional resource-key prefix to scope the listing (e.g. 'repo.lantern.' lists leases under that subtree). Empty lists every live claim."`
}

type liveClaim struct {
	Resource  string `json:"resource"`
	Holder    string `json:"holder,omitempty"`
	Note      string `json:"note,omitempty"`
	At        string `json:"at,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

type listClaimsOutput struct {
	Claims []liveClaim `json:"claims"`
	Count  int         `json:"count"`
}

const listClaimsDescription = "List live advisory leases, optionally scoped to a resource-key prefix. Expired claims are already gone (TTL), so everything returned is current. Call it before starting work in a namespace to see what siblings have staked out, or to find your own leases to release."

func registerListClaims(srv *mcp.Server, lc lanternClient) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_claims",
		Description: listClaimsDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listClaimsInput) (*mcp.CallToolResult, listClaimsOutput, error) {
		vs, _, err := lc.ScanVertices(ctx, claimKeyPrefix+in.Prefix, client.WithScanLimit(500))
		if err != nil {
			return nil, listClaimsOutput{}, mapSDKError("list_claims", err)
		}
		out := listClaimsOutput{Claims: make([]liveClaim, 0, len(vs))}
		for _, v := range vs {
			c := liveClaim{Resource: strings.TrimPrefix(v.GetKey(), claimKeyPrefix)}
			var rec claimRecord
			if decodeRecord(v, &rec) {
				c.Holder, c.Note, c.At = rec.Holder, rec.Note, rec.At
			}
			if exp := client.VertexExpiration(v); !exp.IsZero() {
				c.ExpiresAt = exp.UTC().Format(time.RFC3339)
			}
			out.Claims = append(out.Claims, c)
		}
		sort.Slice(out.Claims, func(i, j int) bool { return out.Claims[i].Resource < out.Claims[j].Resource })
		out.Count = len(out.Claims)

		if out.Count == 0 {
			scope := "anywhere"
			if in.Prefix != "" {
				scope = fmt.Sprintf("under %q", in.Prefix)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("No live claims %s.", scope)}},
			}, out, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d live claim(s):\n", out.Count)
		for _, c := range out.Claims {
			fmt.Fprintf(&b, "- %s ← %s (expires≈%s)%s\n", c.Resource, c.Holder, c.ExpiresAt, noteSuffix(c.Note))
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: strings.TrimRight(b.String(), "\n")}},
		}, out, nil
	})
}
