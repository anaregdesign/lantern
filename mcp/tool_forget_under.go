package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// forgetUnderMaxRounds bounds how many DeleteVerticesByPrefix calls a single
// forget_under issues. The server removes only up to its per-call limit
// (LANTERN_DELETE_BY_PREFIX_DEFAULT_LIMIT, default 10000) each round, so to
// drain a namespace larger than one call we loop until a round deletes
// nothing — the pattern the SDK documents on DeleteVerticesByPrefix. Real
// deletes strictly make progress (each round removes the keys it reports), so
// the only way to keep returning a positive count is a namespace under active
// concurrent writes; this ceiling stops that pathological case from spinning
// forever and instead reports truncated=true so the caller can re-run.
const forgetUnderMaxRounds = 1000

type forgetUnderInput struct {
	Prefix string `json:"prefix" jsonschema:"Key prefix whose entire namespace to delete: every fact whose key starts with this exact string is removed. Matching is literal prefix, not glob — pass session.verify. WITH the trailing dot to scope to that namespace and avoid also catching session.verifyN. Must not be empty and must not be \"*\"; forget_under deliberately refuses a blank or wildcard prefix because either would wipe the whole store."`
	DryRun bool   `json:"dry_run,omitempty" jsonschema:"When true, report how many facts WOULD be deleted (a fast index estimate) without deleting anything. Use this first to confirm the blast radius, then re-run with dry_run=false. Defaults to false, which deletes."`
}

type forgetUnderOutput struct {
	Prefix string `json:"prefix"`
	DryRun bool   `json:"dry_run"`
	// Count is the would-delete estimate when dry_run is true, otherwise the
	// number of facts actually deleted across all drain rounds.
	Count uint64 `json:"count"`
	// Truncated is set only on a real delete that hit forgetUnderMaxRounds
	// before the namespace drained — re-run forget_under to continue.
	Truncated bool `json:"truncated,omitempty"`
}

const forgetUnderDescription = "Bulk-delete an entire key namespace in one call: every fact whose key starts with prefix is removed. This is the prefix-scoped counterpart to forget (single exact key) — use it to tear down a working namespace such as session.verify. instead of forgetting keys one by one. Refuses an empty or \"*\" prefix so you cannot accidentally wipe the whole store. Pass dry_run=true first to see how many facts would be deleted (a fast index estimate that may slightly overshoot under TTL churn), then re-run with dry_run=false to delete. A real delete drains the namespace in rounds until it is empty; if truncated=true it stopped early (a very large or concurrently-written namespace) — re-run to finish. Edges incident to deleted keys are NOT cascade-deleted; they decay on their own TTL. Like forget, reach for this only when facts are wrong or the user asks to clear a namespace — ordinary staleness is handled by TTL decay, so you rarely need it."

func registerForgetUnder(srv *mcp.Server, lc lanternClient) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "forget_under",
		Description: forgetUnderDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in forgetUnderInput) (*mcp.CallToolResult, forgetUnderOutput, error) {
		// Guard the destructive blast radius before touching the server.
		// An empty (or whitespace-only) prefix would match every key, and
		// a lone "*" is almost certainly a caller reaching for glob syntax
		// that this tool does not support — either would wipe the store.
		if strings.TrimSpace(in.Prefix) == "" {
			return nil, forgetUnderOutput{}, fmt.Errorf("forget_under: prefix must not be empty — refusing to delete the entire store")
		}
		if in.Prefix == "*" {
			return nil, forgetUnderOutput{}, fmt.Errorf("forget_under: prefix %q is not a wildcard and matching is literal-prefix only; refusing to delete the entire store — pass a concrete namespace prefix such as session.verify.", in.Prefix)
		}

		if in.DryRun {
			n, err := lc.CountVerticesByPrefix(ctx, in.Prefix)
			if err != nil {
				return nil, forgetUnderOutput{}, mapSDKError("forget_under", err)
			}
			out := forgetUnderOutput{Prefix: in.Prefix, DryRun: true, Count: n}
			text := fmt.Sprintf("Dry run: %d fact(s) under %q would be deleted (estimate). Re-run with dry_run=false to delete them.", n, in.Prefix)
			if n == 0 {
				text = fmt.Sprintf("Dry run: nothing stored under %q.", in.Prefix)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: text}},
			}, out, nil
		}

		var deleted uint64
		truncated := true
		for round := 0; round < forgetUnderMaxRounds; round++ {
			n, err := lc.DeleteVerticesByPrefix(ctx, in.Prefix)
			if err != nil {
				return nil, forgetUnderOutput{}, mapSDKError("forget_under", err)
			}
			deleted += n
			if n == 0 {
				truncated = false
				break
			}
		}

		out := forgetUnderOutput{Prefix: in.Prefix, DryRun: false, Count: deleted, Truncated: truncated}
		text := fmt.Sprintf("Deleted %d fact(s) under %q.", deleted, in.Prefix)
		if deleted == 0 {
			text = fmt.Sprintf("Nothing to delete under %q.", in.Prefix)
		}
		if truncated {
			text += fmt.Sprintf(" Stopped after %d rounds with deletions still in progress; re-run forget_under to continue.", forgetUnderMaxRounds)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	})
}
