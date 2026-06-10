package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/anaregdesign/lantern/mcp/internal/ttl"
	"github.com/anaregdesign/lantern/mcp/internal/value"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// rememberFactInput is the JSON-schema-bearing input for remember_fact.
// The ttl field is the required 12-bucket enum; struct tags drive the
// schema the SDK advertises to the LLM.
type rememberFactInput struct {
	Key   string `json:"key"   jsonschema:"Hierarchical key identifying the fact (e.g. user.preferences.tone). Re-using the same key overwrites the previous value and resets its TTL."`
	Value any    `json:"value" jsonschema:"The fact body. Strings, numbers, booleans, objects and arrays are all supported. Composite values are JSON-encoded server-side and round-trip via recall_fact."`
	TTL   string `json:"ttl"   jsonschema:"Required decay horizon (string). Recalling does NOT refresh TTL; when in doubt pick the SHORTER bucket. Buckets (monotonic): seconds (~30s ephemeral cursor) | transient (~2m last tool result) | turn (~10m working hypothesis) | conversation (~1h session preference) | task (~4h today's focus) | workday (~12h shift state) | day (~24h rolling-day fact) | week (~7d sprint-week scope) | sprint (~14d sprint goal) | month (~30d OKR) | quarter (~90d project phase) | durable (~180d semi-permanent). There is no 'forever' — rewrite to refresh."`
}

type rememberFactOutput struct {
	Key       string `json:"key"`
	Bucket    string `json:"bucket"`
	ExpiresAt string `json:"expires_at"`
	// Capped is true when the bucket's nominal horizon was clamped down to
	// LANTERN_MCP_MAX_TTL before writing, so the caller knows the stored
	// expiry is shorter than the bucket label implies.
	Capped bool `json:"capped,omitempty"`
}

const rememberFactDescription = "Store a fact in Lantern with a required TTL bucket. Call this PROACTIVELY whenever the user states a durable preference, decision, identity, or project fact — you do not need to be asked. The value can be any JSON-encodable shape (string, number, bool, object, array). Use a dotted namespaced key (user.* / project.* / session.*). Writing the same key again overwrites the previous value and resets the TTL — that is the canonical way to refresh a fact since recall does NOT refresh."

func registerRememberFact(srv *mcp.Server, lc lanternClient, r *ttl.Resolver) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "remember_fact",
		Description: rememberFactDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in rememberFactInput) (*mcp.CallToolResult, rememberFactOutput, error) {
		if in.Key == "" {
			return nil, rememberFactOutput{}, fmt.Errorf("remember_fact: key must not be empty")
		}
		bucket, err := ttl.ParseBucket(in.TTL)
		if err != nil {
			return nil, rememberFactOutput{}, fmt.Errorf("remember_fact: %w", err)
		}
		sdkValue, err := value.ToSDK(in.Value)
		if err != nil {
			return nil, rememberFactOutput{}, fmt.Errorf("remember_fact: %w", err)
		}
		d, capped := r.ResolveCapped(bucket)
		if err := lc.PutVertex(ctx, in.Key, sdkValue, d); err != nil {
			return nil, rememberFactOutput{}, mapSDKError("remember_fact", err)
		}
		out := rememberFactOutput{
			Key:       in.Key,
			Bucket:    bucket.String(),
			ExpiresAt: time.Now().Add(d).UTC().Format(time.RFC3339),
			Capped:    capped,
		}
		text := fmt.Sprintf("Stored %q (bucket=%s, expires≈%s).", out.Key, out.Bucket, out.ExpiresAt)
		if capped {
			text += fmt.Sprintf(" Note: TTL clamped to the server cap (%s); re-remember before it expires to keep the fact alive.", d)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: text,
			}},
		}, out, nil
	})
}
