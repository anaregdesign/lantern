package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/anaregdesign/lantern/mcp/internal/identity"
	"github.com/anaregdesign/lantern/mcp/internal/ttl"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type claimInput struct {
	Resource string `json:"resource" jsonschema:"Dotted resource key to lease (e.g. repo.lantern.core.graphcache). Use the same key everyone tracks with, or the claim protects nothing."`
	Note     string `json:"note,omitempty" jsonschema:"Optional one-liner shown to whoever hits the conflict (what you're doing, expected duration)."`
	Force    bool   `json:"force,omitempty" jsonschema:"Steal the lease even if another live holder exists. Use only after coordinating (or when the holder is provably gone); the previous holder is reported in the output."`
}

type claimOutput struct {
	Granted  bool   `json:"granted"`
	Resource string `json:"resource"`
	AgentID  string `json:"agent_id"`
	// Renewed is true when the caller already held the lease (re-claim =
	// renewal, the intended heartbeat for long work).
	Renewed bool `json:"renewed,omitempty"`
	// Stolen is true when force displaced a live foreign holder.
	Stolen bool `json:"stolen,omitempty"`
	// Holder/HolderNote/ExpiresAt describe the CURRENT lease after the
	// call: on grant they echo the caller, on conflict the blocking agent.
	Holder     string `json:"holder,omitempty"`
	HolderNote string `json:"holder_note,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
}

const claimDescription = "Take an ADVISORY lease on a resource so sibling agents don't collide with you. A live claim by another agent returns {granted:false, holder, expires_at} — a structured result, NOT an error; coordinate or pass force:true to steal after coordinating. Re-claim the same resource to renew (leases live ~10 minutes, the turn bucket, and expire on their own — a crashed agent's claim simply evaporates). Release when done. Advisory means exactly that: nothing on the server ENFORCES the lease (the check-then-write window is documented and accepted — agents are slow writers, contention is rare); honour claims you see."

func registerClaim(srv *mcp.Server, lc lanternClient, r *ttl.Resolver) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "claim",
		Description: claimDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in claimInput) (*mcp.CallToolResult, claimOutput, error) {
		if in.Resource == "" {
			return nil, claimOutput{}, fmt.Errorf("claim: resource must not be empty")
		}
		id := identity.Resolve()
		key := claimKey(in.Resource)

		// Advisory read-check-write. A live foreign holder blocks unless
		// force; an expired claim is already gone (GetVertex → NotFound).
		var prev claimRecord
		havePrev := false
		var prevExpires time.Time
		if v, err := lc.GetVertex(ctx, key); err == nil {
			if decodeRecord(v, &prev) {
				havePrev = true
				prevExpires = client.VertexExpiration(v)
			}
		} else if !errors.Is(err, client.ErrNotFound) {
			return nil, claimOutput{}, mapSDKError("claim", err)
		}

		if havePrev && prev.Holder != id && !in.Force {
			out := claimOutput{
				Granted:    false,
				Resource:   in.Resource,
				AgentID:    id,
				Holder:     prev.Holder,
				HolderNote: prev.Note,
			}
			if !prevExpires.IsZero() {
				out.ExpiresAt = prevExpires.UTC().Format(time.RFC3339)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(
					"Resource %q is claimed by %s (expires≈%s)%s. Coordinate with the holder, wait for expiry, or re-call with force:true.",
					in.Resource, out.Holder, out.ExpiresAt, noteSuffix(prev.Note))}},
			}, out, nil
		}

		now := time.Now().UTC()
		payload, err := encodeRecord(claimRecord{Holder: id, Note: in.Note, At: now.Format(time.RFC3339)})
		if err != nil {
			return nil, claimOutput{}, fmt.Errorf("claim: %w", err)
		}
		d, _ := r.ResolveCapped(ttl.Turn)
		if err := lc.PutVertex(ctx, key, payload, d); err != nil {
			return nil, claimOutput{}, mapSDKError("claim", err)
		}
		// The activity edge makes the lease visible in whats_happening's
		// neighborhood, not just under the claims. prefix.
		_ = lc.AddEdge(ctx, agentKey(id), in.Resource, 1, d)

		out := claimOutput{
			Granted:   true,
			Resource:  in.Resource,
			AgentID:   id,
			Renewed:   havePrev && prev.Holder == id,
			Stolen:    havePrev && prev.Holder != id,
			Holder:    id,
			ExpiresAt: now.Add(d).Format(time.RFC3339),
		}
		verb := "Claimed"
		switch {
		case out.Renewed:
			verb = "Renewed claim on"
		case out.Stolen:
			verb = fmt.Sprintf("STOLE claim (from %s) on", prev.Holder)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(
				"%s %q as %s (expires≈%s; re-claim to renew, release when done).",
				verb, in.Resource, id, out.ExpiresAt)}},
		}, out, nil
	})
}

func noteSuffix(note string) string {
	if note == "" {
		return ""
	}
	return fmt.Sprintf(" — holder says: %q", note)
}
