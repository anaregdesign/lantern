package mcp

import (
	"context"
	"errors"
	"fmt"

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

type searchFactsInput struct {
	Query  string `json:"query" jsonschema:"Words or a phrase to search for. Matched against BOTH each fact's key AND its value through a relevance-ranked full-text index, so you can search by topic words you remember even when you have forgotten the exact key. Matches come back most-relevant-first. Must not be empty."`
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
	Query   string             `json:"query"`
	Count   int                `json:"count"`
	Matches []searchFactsMatch `json:"matches"`
}

const searchFactsDescription = "Find facts by relevance-ranked full-text search over both keys AND values. Use this when you remember the TOPIC of a stored fact but not its exact key — it is the approximate counterpart to recall_fact (exact key) and complements list_under (prefix scan). Matches come back most-relevant-first as compact {key, snippet, expires_at} previews, not full values; call recall_fact with a returned key to read the whole value. Pass a prefix to scope the search to a namespace. Requires the server's search index (LANTERN_SEARCH_ENABLED); if it is off the call returns a clear error. Does NOT refresh TTL for matched facts."

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

		hits, err := lc.SearchVertices(ctx, in.Query,
			client.WithSearchLimit(limit),
			client.WithSearchPrefix(in.Prefix))
		if err != nil {
			if errors.Is(err, client.ErrFailedPrecondition) {
				return nil, searchFactsOutput{}, fmt.Errorf("search_facts: the server's search index is disabled; set LANTERN_SEARCH_ENABLED=true on the Lantern server to enable search_facts: %w", err)
			}
			return nil, searchFactsOutput{}, mapSDKError("search_facts", err)
		}

		matches := make([]searchFactsMatch, 0, len(hits))
		if len(hits) > 0 {
			keys := make([]string, len(hits))
			for i, h := range hits {
				keys[i] = h.Key
			}
			// SearchHit carries only {key, score}; hydrate each match's
			// snippet and expiry with one batch read, then re-emit in the
			// ranked order the index returned.
			found, _, gerr := lc.GetVertices(ctx, keys)
			if gerr != nil {
				return nil, searchFactsOutput{}, mapSDKError("search_facts", gerr)
			}
			byKey := make(map[string]*client.Vertex, len(found))
			for _, v := range found {
				if v != nil {
					byKey[v.GetKey()] = v
				}
			}
			for _, h := range hits {
				m := searchFactsMatch{Key: h.Key}
				if v := byKey[h.Key]; v != nil {
					m.Snippet = value.Snippet(v)
					if exp := client.VertexExpiration(v); !exp.IsZero() {
						m.ExpiresAt = exp.UTC().Format("2006-01-02T15:04:05Z07:00")
					}
				}
				matches = append(matches, m)
			}
		}

		out := searchFactsOutput{
			Query:   in.Query,
			Count:   len(matches),
			Matches: matches,
		}
		text := fmt.Sprintf("Found %d fact(s) matching %q.", out.Count, in.Query)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	})
}
