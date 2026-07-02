package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/anaregdesign/lantern/mcp/internal/identity"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type releaseInput struct {
	Resource string `json:"resource" jsonschema:"Resource key whose lease you hold and want to drop."`
}

type releaseOutput struct {
	Released bool   `json:"released"`
	Resource string `json:"resource"`
	AgentID  string `json:"agent_id"`
	// Holder is set when the release was refused because another agent
	// holds the lease (structured result, not an error).
	Holder string `json:"holder,omitempty"`
}

const releaseDescription = "Drop your advisory lease on a resource as soon as you finish with it — don't make siblings wait out the TTL. Releasing a resource you don't hold returns {released:false, holder} (a structured result, NOT an error) and leaves the foreign lease intact; an already-expired or never-claimed resource returns {released:true} idempotently."

func registerRelease(srv *mcp.Server, lc lanternClient) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "release",
		Description: releaseDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in releaseInput) (*mcp.CallToolResult, releaseOutput, error) {
		if in.Resource == "" {
			return nil, releaseOutput{}, fmt.Errorf("release: resource must not be empty")
		}
		id := identity.Resolve()
		key := claimKey(in.Resource)

		v, err := lc.GetVertex(ctx, key)
		if err != nil {
			if errors.Is(err, client.ErrNotFound) {
				// Nothing to release — idempotent success (the lease may
				// simply have expired, which is the same end state).
				out := releaseOutput{Released: true, Resource: in.Resource, AgentID: id}
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(
						"No live claim on %q (already expired or never claimed) — nothing to do.", in.Resource)}},
				}, out, nil
			}
			return nil, releaseOutput{}, mapSDKError("release", err)
		}

		var rec claimRecord
		if decodeRecord(v, &rec) && rec.Holder != id {
			out := releaseOutput{Released: false, Resource: in.Resource, AgentID: id, Holder: rec.Holder}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(
					"Claim on %q belongs to %s, not you — left intact. Use claim with force:true if you truly need to displace it.",
					in.Resource, rec.Holder)}},
			}, out, nil
		}

		if _, err := lc.DeleteVertex(ctx, key); err != nil {
			return nil, releaseOutput{}, mapSDKError("release", err)
		}
		out := releaseOutput{Released: true, Resource: in.Resource, AgentID: id}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Released claim on %q.", in.Resource)}},
		}, out, nil
	})
}
