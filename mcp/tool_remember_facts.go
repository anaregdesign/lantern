package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/mcp/internal/ttl"
	"github.com/anaregdesign/lantern/mcp/internal/value"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// rememberFactItem is one entry of a remember_facts batch. It mirrors the
// singular remember_fact input (key + value + per-item TTL bucket) so an
// agent can build a batch by listing the same shapes it would write one at a
// time.
type rememberFactItem struct {
	Key   string `json:"key"   jsonschema:"Hierarchical key identifying the fact (e.g. user.preferences.tone). Re-using the same key overwrites the previous value and resets its TTL."`
	Value any    `json:"value" jsonschema:"The fact body. Strings, numbers, booleans, objects and arrays are all supported. Composite values are JSON-encoded server-side and round-trip via recall_fact."`
	TTL   string `json:"ttl"   jsonschema:"Required decay horizon (string) for THIS item; each item carries its own bucket. Recalling does NOT refresh TTL; when in doubt pick the SHORTER bucket. Buckets (monotonic): seconds | transient | turn | conversation | task | workday | day | week | sprint | month | quarter | durable."`
}

// rememberFactsInput is the batch input: a list of facts to persist in one
// call.
type rememberFactsInput struct {
	Items []rememberFactItem `json:"items" jsonschema:"The facts to store. Each item has its own key, value, and ttl bucket. Provide at least one; prefer this over many remember_fact calls when you have several facts to record at once."`
}

// rememberFactsResult is the per-item outcome reported back to the caller.
type rememberFactsResult struct {
	Key    string `json:"key"`
	Status string `json:"status"` // "stored" | "failed"
	Bucket string `json:"bucket"`
	// ExpiresAt is set only for stored items.
	ExpiresAt string `json:"expires_at,omitempty"`
	// Capped is true when this item's bucket horizon was clamped down to
	// LANTERN_MCP_MAX_TTL before writing.
	Capped bool `json:"capped,omitempty"`
	// Warnings carries advisory, non-blocking key-quality hints from lintKey
	// for THIS item's key. Independent of the storage outcome.
	Warnings []string `json:"warnings,omitempty"`
	// Error carries the failure cause on the first non-committed item.
	Error string `json:"error,omitempty"`
}

type rememberFactsOutput struct {
	Stored  int                   `json:"stored"`
	Failed  int                   `json:"failed"`
	Total   int                   `json:"total"`
	Results []rememberFactsResult `json:"results"`
}

const rememberFactsDescription = "Store SEVERAL facts in one call — the batch counterpart to remember_fact. Pass items[] where each entry has its own key, value, and ttl bucket. Prefer this over many remember_fact calls when you have multiple facts to record at once (e.g. capturing a knowledge graph from a conversation) — it cuts round-trips. Each item's TTL is clamped to the server cap independently and reported per item. Writing a key that already exists overwrites it (idempotent), so if the call is rejected for an invalid item nothing is written and you can fix and resubmit the whole batch safely. On a partial server-side failure the result reports per-item status (stored/failed) and how many committed, so you can resubmit only the rest."

func registerRememberFacts(srv *mcp.Server, lc lanternClient, r *ttl.Resolver) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "remember_facts",
		Description: rememberFactsDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in rememberFactsInput) (*mcp.CallToolResult, rememberFactsOutput, error) {
		if len(in.Items) == 0 {
			return nil, rememberFactsOutput{}, fmt.Errorf("remember_facts: items must not be empty")
		}

		// Validate EVERY item up front. Validation failures are deterministic
		// caller bugs (empty key, bad bucket, unencodable value), so we reject
		// the WHOLE call without writing anything. Because nothing is written,
		// the caller can fix the offending items and resubmit the whole batch
		// — overwrite semantics make that safe for facts.
		inputs := make([]client.VertexInput, len(in.Items))
		results := make([]rememberFactsResult, len(in.Items))
		var verrs []string
		for i, it := range in.Items {
			if strings.TrimSpace(it.Key) == "" {
				verrs = append(verrs, fmt.Sprintf("items[%d]: key must not be empty", i))
				continue
			}
			bucket, err := ttl.ParseBucket(it.TTL)
			if err != nil {
				verrs = append(verrs, fmt.Sprintf("items[%d] (%s): %v", i, it.Key, err))
				continue
			}
			sdkValue, err := value.ToSDK(it.Value)
			if err != nil {
				verrs = append(verrs, fmt.Sprintf("items[%d] (%s): %v", i, it.Key, err))
				continue
			}
			d, capped := r.ResolveCapped(bucket)
			exp := time.Now().Add(d)
			inputs[i] = client.VertexInput{Key: it.Key, Value: sdkValue, Expiration: exp}
			results[i] = rememberFactsResult{
				Key:       it.Key,
				Bucket:    bucket.String(),
				ExpiresAt: exp.UTC().Format(time.RFC3339),
				Capped:    capped,
				Warnings:  lintKey(it.Key),
			}
		}
		if len(verrs) > 0 {
			return nil, rememberFactsOutput{}, fmt.Errorf(
				"remember_facts: %d invalid item(s), nothing written: %s",
				len(verrs), strings.Join(verrs, "; "))
		}

		written, cause := batchWritten(len(inputs), lc.PutVertices(ctx, inputs))
		if written == 0 {
			// Total failure: nothing committed. Surface as a hard error so the
			// caller retries the whole batch; there is no partial state worth
			// preserving in structured output.
			return nil, rememberFactsOutput{}, mapSDKError("remember_facts", cause)
		}

		for i := range results {
			if i < written {
				results[i].Status = "stored"
				continue
			}
			results[i].Status = "failed"
			results[i].ExpiresAt = ""
			results[i].Capped = false
			if i == written && cause != nil {
				results[i].Error = cause.Error()
			}
		}
		out := rememberFactsOutput{
			Stored:  written,
			Failed:  len(inputs) - written,
			Total:   len(inputs),
			Results: results,
		}

		var text string
		if out.Failed == 0 {
			text = fmt.Sprintf("Stored %d fact(s).", out.Stored)
		} else {
			text = fmt.Sprintf(
				"Stored %d of %d fact(s); %d failed starting at items[%d] (%q). Cause: %v. Re-submit only the items that were not stored.",
				out.Stored, out.Total, out.Failed, written, results[written].Key, cause)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	})
}
