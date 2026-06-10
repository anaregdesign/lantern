package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/mcp/internal/ttl"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// rememberRelationEdge is one entry of a remember_relations batch. It mirrors
// the singular remember_relation input (from→to + per-item TTL + optional
// weight).
type rememberRelationEdge struct {
	From   string  `json:"from"             jsonschema:"Tail vertex key. The relation is directed from→to; reverse traversal is not implicit."`
	To     string  `json:"to"               jsonschema:"Head vertex key."`
	TTL    string  `json:"ttl"              jsonschema:"Required decay horizon (string) for THIS edge. The relation disappears when its TTL expires. Recalling does NOT refresh it. Valid values: seconds, transient, turn, conversation, task, workday, day, week, sprint, month, quarter, durable."`
	Weight float32 `json:"weight,omitempty" jsonschema:"Edge weight increment for THIS edge (default 1.0). Writes are ADDITIVE: the increment is added to any existing weight."`
}

// rememberRelationsInput is the batch input: a list of directed relations to
// add (or reinforce) in one call.
type rememberRelationsInput struct {
	Edges []rememberRelationEdge `json:"edges" jsonschema:"The relations to add. Each edge has from, to, ttl, and an optional weight (default 1). Provide at least one; prefer this over many remember_relation calls when wiring up multiple associations at once."`
}

// rememberRelationsResult is the per-item outcome reported back to the caller.
// Unlike the singular remember_relation, the batch path does NOT read back
// each edge's accumulated weight (that would cost one extra round-trip per
// edge and defeat batching), so only the increment this write contributed is
// reported.
type rememberRelationsResult struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Status string `json:"status"` // "stored" | "failed"
	Bucket string `json:"bucket"`
	// Increment is the weight this write contributed (set for stored items).
	Increment float32 `json:"increment,omitempty"`
	// ExpiresAt is set only for stored items.
	ExpiresAt string `json:"expires_at,omitempty"`
	// Capped is true when this edge's bucket horizon was clamped down to
	// LANTERN_MCP_MAX_TTL before writing.
	Capped bool `json:"capped,omitempty"`
	// Error carries the failure cause on the first non-committed item.
	Error string `json:"error,omitempty"`
}

type rememberRelationsOutput struct {
	Stored  int                       `json:"stored"`
	Failed  int                       `json:"failed"`
	Total   int                       `json:"total"`
	Results []rememberRelationsResult `json:"results"`
}

const rememberRelationsDescription = "Add (or reinforce) SEVERAL directed relations in one call — the batch counterpart to remember_relation. Pass edges[] where each entry has from, to, ttl, and an optional weight (default 1). Prefer this over many remember_relation calls when wiring up multiple associations at once — it cuts round-trips. IMPORTANT: like remember_relation, writes are ADDITIVE, not idempotent. If an invalid edge is found the whole call is rejected and nothing is written, so a corrected batch is safe to resubmit. But on a PARTIAL server-side failure you MUST resubmit ONLY the edges that did not commit (the result reports how many committed and per-item status) — resending an already-committed edge double-counts its weight. For efficiency the batch path does NOT read back accumulated weights; use the singular remember_relation or recall_relation when you need a specific edge's resulting accumulated_weight."

func registerRememberRelations(srv *mcp.Server, lc lanternClient, r *ttl.Resolver) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "remember_relations",
		Description: rememberRelationsDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in rememberRelationsInput) (*mcp.CallToolResult, rememberRelationsOutput, error) {
		if len(in.Edges) == 0 {
			return nil, rememberRelationsOutput{}, fmt.Errorf("remember_relations: edges must not be empty")
		}

		// Validate EVERY edge up front and reject the whole call on any
		// failure without writing. For ADDITIVE writes this all-or-nothing
		// validation matters: it guarantees a rejected batch left zero edges
		// behind, so the caller can fix and resubmit the whole batch without
		// double-counting the valid edges.
		inputs := make([]client.EdgeInput, len(in.Edges))
		results := make([]rememberRelationsResult, len(in.Edges))
		var verrs []string
		for i, e := range in.Edges {
			if strings.TrimSpace(e.From) == "" || strings.TrimSpace(e.To) == "" {
				verrs = append(verrs, fmt.Sprintf("edges[%d]: both from and to must be non-empty", i))
				continue
			}
			bucket, err := ttl.ParseBucket(e.TTL)
			if err != nil {
				verrs = append(verrs, fmt.Sprintf("edges[%d] (%s->%s): %v", i, e.From, e.To, err))
				continue
			}
			weight := e.Weight
			if weight == 0 {
				weight = 1
			}
			d, capped := r.ResolveCapped(bucket)
			exp := time.Now().Add(d)
			inputs[i] = client.EdgeInput{Tail: e.From, Head: e.To, Weight: weight, Expiration: exp}
			results[i] = rememberRelationsResult{
				From:      e.From,
				To:        e.To,
				Bucket:    bucket.String(),
				Increment: weight,
				ExpiresAt: exp.UTC().Format(time.RFC3339),
				Capped:    capped,
			}
		}
		if len(verrs) > 0 {
			return nil, rememberRelationsOutput{}, fmt.Errorf(
				"remember_relations: %d invalid edge(s), nothing written: %s",
				len(verrs), strings.Join(verrs, "; "))
		}

		written, cause := batchWritten(len(inputs), lc.AddEdges(ctx, inputs))
		if written == 0 {
			// Total failure: nothing committed. Surface as a hard error so the
			// caller retries the whole batch — safe because no edge landed.
			return nil, rememberRelationsOutput{}, mapSDKError("remember_relations", cause)
		}

		for i := range results {
			if i < written {
				results[i].Status = "stored"
				continue
			}
			results[i].Status = "failed"
			results[i].Increment = 0
			results[i].ExpiresAt = ""
			results[i].Capped = false
			if i == written && cause != nil {
				results[i].Error = cause.Error()
			}
		}
		out := rememberRelationsOutput{
			Stored:  written,
			Failed:  len(inputs) - written,
			Total:   len(inputs),
			Results: results,
		}

		var text string
		if out.Failed == 0 {
			text = fmt.Sprintf("Added %d relation(s). Writes are additive — repeat to strengthen.", out.Stored)
		} else {
			text = fmt.Sprintf(
				"Added %d of %d relation(s); %d failed starting at edges[%d] (%q -> %q). Cause: %v. Writes are ADDITIVE: resubmit ONLY the %d relation(s) that were not stored — do NOT resend the %d already-committed edges or their weight will double-count.",
				out.Stored, out.Total, out.Failed, written, results[written].From, results[written].To, cause, out.Failed, out.Stored)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	})
}
