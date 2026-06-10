package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/anaregdesign/lantern/mcp/internal/value"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// searchFactsDefaultLimit is the number of matches returned when the caller
// omits limit. It is small on purpose: search exists to relocate a fact whose
// exact key the agent has forgotten, not to bulk-export a namespace.
const searchFactsDefaultLimit uint32 = 20

// searchFactsMaxLimit caps the per-call match count so a broad query cannot
// flood the model context.
const searchFactsMaxLimit uint32 = 100

// searchFactsScanBatch is the per-page size forwarded to ScanVertices while
// paginating. Larger pages mean fewer round trips; this is independent of how
// many matches we ultimately return.
const searchFactsScanBatch uint32 = 500

// searchFactsMaxScan bounds how many vertices a single search may pull from
// the server before giving up. v1 search is an unindexed scan-and-filter, so
// without this ceiling a miss on a large keyspace would walk every vertex.
// When the ceiling is hit before enough matches accumulate, the result is
// marked truncated and the caller is told to narrow the prefix.
const searchFactsMaxScan = 10_000

type searchFactsInput struct {
	Query  string `json:"query" jsonschema:"Case-insensitive substring to find. It is matched against BOTH each fact's key AND its value, so you can search by topic words you remember even when you have forgotten the exact key. Must not be empty."`
	Prefix string `json:"prefix,omitempty" jsonschema:"Optional key prefix to restrict the search to one namespace (e.g. user. or project.lantern.). Empty (the default) searches the entire keyspace. Supplying a prefix when you know the rough namespace makes the search both faster and more precise."`
	Limit  uint32 `json:"limit,omitempty" jsonschema:"Maximum number of matching facts to return (default 20, capped at 100). Results are compact previews, not full values."`
}

// searchFactsMatch is one hit. It mirrors the {key, snippet, expires_at}
// shape that list_under returns under projection=snippet so callers can treat
// the two surfaces interchangeably. To read a match's full value, call
// recall_fact with its key.
type searchFactsMatch struct {
	Key       string `json:"key"`
	Snippet   string `json:"snippet,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

type searchFactsOutput struct {
	Query      string             `json:"query"`
	Count      int                `json:"count"`
	Matches    []searchFactsMatch `json:"matches"`
	Scanned    int                `json:"scanned"`
	Truncated  bool               `json:"truncated"`
	Suggestion string             `json:"suggestion,omitempty"`
}

const searchFactsDescription = "Find facts by a case-insensitive substring matched against both keys AND values. Use this when you remember the TOPIC of a stored fact but not its exact key — it is the approximate counterpart to recall_fact (exact key) and complements list_under (prefix scan). Returns compact {key, snippet, expires_at} previews, not full values; call recall_fact with a returned key to read the whole value. Pass a prefix to scope the search to a namespace and keep it fast. If truncated=true the scan hit its budget before finishing — narrow with a prefix. Does NOT refresh TTL for matched facts."

func registerSearchFacts(srv *mcp.Server, lc lanternClient) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search_facts",
		Description: searchFactsDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchFactsInput) (*mcp.CallToolResult, searchFactsOutput, error) {
		if in.Query == "" {
			return nil, searchFactsOutput{}, fmt.Errorf("search_facts: query must not be empty")
		}
		limit := in.Limit
		if limit == 0 {
			limit = searchFactsDefaultLimit
		}
		if limit > searchFactsMaxLimit {
			limit = searchFactsMaxLimit
		}
		needle := strings.ToLower(in.Query)

		matches := make([]searchFactsMatch, 0, limit)
		scanned := 0
		truncated := false
		var cursor []byte

	scan:
		for {
			verts, next, err := lc.ScanVertices(ctx, in.Prefix,
				client.WithScanLimit(searchFactsScanBatch),
				client.WithScanCursor(cursor))
			if err != nil {
				return nil, searchFactsOutput{}, mapSDKError("search_facts", err)
			}
			for _, v := range verts {
				if v == nil {
					continue
				}
				scanned++
				if factMatchesQuery(v, needle) {
					m := searchFactsMatch{Key: v.GetKey(), Snippet: value.Snippet(v)}
					if exp := client.VertexExpiration(v); !exp.IsZero() {
						m.ExpiresAt = exp.UTC().Format("2006-01-02T15:04:05Z07:00")
					}
					matches = append(matches, m)
					if uint32(len(matches)) >= limit {
						truncated = true
						break scan
					}
				}
				if scanned >= searchFactsMaxScan {
					truncated = true
					break scan
				}
			}
			if len(next) == 0 {
				break
			}
			cursor = next
		}

		out := searchFactsOutput{
			Query:     in.Query,
			Count:     len(matches),
			Matches:   matches,
			Scanned:   scanned,
			Truncated: truncated,
		}
		if truncated {
			if uint32(len(matches)) >= limit {
				out.Suggestion = fmt.Sprintf("Returned the first %d matches; more may exist. Raise limit (max %d) or pass a prefix to narrow the search.", limit, searchFactsMaxLimit)
			} else {
				out.Suggestion = fmt.Sprintf("Stopped after scanning %d facts without filling the result. Pass a prefix to scope the search to a namespace.", scanned)
			}
		}
		text := fmt.Sprintf("Found %d fact(s) matching %q (scanned=%d, truncated=%t).", out.Count, in.Query, out.Scanned, out.Truncated)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	})
}

// factMatchesQuery reports whether the lower-cased needle occurs in the
// vertex key or in its full single-line value text. value.Text (not Snippet)
// is used so a match in a long value past the preview cap is not missed.
func factMatchesQuery(v *client.Vertex, lowerNeedle string) bool {
	if strings.Contains(strings.ToLower(v.GetKey()), lowerNeedle) {
		return true
	}
	return strings.Contains(strings.ToLower(value.Text(v)), lowerNeedle)
}
