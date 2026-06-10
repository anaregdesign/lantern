package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/anaregdesign/lantern/mcp/internal/ttl"
	"github.com/anaregdesign/lantern/mcp/internal/value"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// touchInput is the JSON-schema-bearing input for touch. It mirrors the
// remember_fact TTL bucket enum so the keep-alive horizon is chosen the
// same way a fresh write would choose one — but there is deliberately no
// value field: touch never changes what is stored.
type touchInput struct {
	Key string `json:"key" jsonschema:"Key of an existing fact to keep alive. Lookup is exact (not prefix); use list_under or search_facts to find the key first if unsure."`
	TTL string `json:"ttl" jsonschema:"Required new decay horizon (string), applied as now + bucket. Use the SAME 12-bucket ladder as remember_fact; when in doubt pick the SHORTER bucket. Buckets (monotonic): seconds (~30s) | transient (~2m) | turn (~10m) | conversation (~1h) | task (~4h) | workday (~12h) | day (~24h) | week (~7d) | sprint (~14d) | month (~30d) | quarter (~90d) | durable (~180d). There is no 'forever'."`
}

// touchOutput is the structured result. Found mirrors presence so the LLM
// can branch without parsing the human-readable Text: a missing key yields
// {found:false} (a structured result, NOT a tool error), and the TTL
// fields are populated only when a vertex was actually kept alive.
type touchOutput struct {
	Found     bool   `json:"found"`
	Key       string `json:"key"`
	Bucket    string `json:"bucket,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	// Capped is true when the bucket's nominal horizon was clamped down to
	// LANTERN_MCP_MAX_TTL before writing, so the caller knows the new
	// expiry is shorter than the bucket label implies.
	Capped bool `json:"capped,omitempty"`
}

const touchDescription = "Extend a fact's TTL WITHOUT rewriting its value — the cheap keep-alive. Recall does NOT refresh TTL, so when you reference a fact you want to keep, touch it instead of re-supplying its whole value via remember_fact. Touch reads the current value and re-stores it unchanged with a fresh expiry of now + the given bucket. A missing key returns {found=false} (a structured result, NOT a tool error) — touch never creates a fact, so use remember_fact for that. This is the vertex-side keep-alive; the edge-side counterpart is remember_relation, which strengthens an association rather than just keeping it alive."

func registerTouch(srv *mcp.Server, lc lanternClient, r *ttl.Resolver) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "touch",
		Description: touchDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in touchInput) (*mcp.CallToolResult, touchOutput, error) {
		if in.Key == "" {
			return nil, touchOutput{}, fmt.Errorf("touch: key must not be empty")
		}
		bucket, err := ttl.ParseBucket(in.TTL)
		if err != nil {
			return nil, touchOutput{}, fmt.Errorf("touch: %w", err)
		}

		v, err := lc.GetVertex(ctx, in.Key)
		if err != nil {
			if errors.Is(err, client.ErrNotFound) {
				out := touchOutput{Found: false, Key: in.Key}
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{
						Text: fmt.Sprintf("No fact stored for %q; nothing to touch. Use remember_fact to create it.", in.Key),
					}},
				}, out, nil
			}
			return nil, touchOutput{}, mapSDKError("touch", err)
		}

		// Re-store the existing value verbatim with a fresh expiry. value.Native
		// preserves the stored kind exactly, so the value the server holds is
		// unchanged — only the TTL advances.
		d, capped := r.ResolveCapped(bucket)
		if err := lc.PutVertex(ctx, in.Key, value.Native(v), d); err != nil {
			return nil, touchOutput{}, mapSDKError("touch", err)
		}

		out := touchOutput{
			Found:     true,
			Key:       in.Key,
			Bucket:    bucket.String(),
			ExpiresAt: time.Now().Add(d).UTC().Format(time.RFC3339),
			Capped:    capped,
		}
		text := fmt.Sprintf("Touched %q (bucket=%s, expires≈%s); value unchanged.", out.Key, out.Bucket, out.ExpiresAt)
		if capped {
			text += fmt.Sprintf(" Note: TTL clamped to the server cap (%s); touch again before it expires to keep the fact alive.", d)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	})
}
