package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// listNamespacesDefaultDepth aggregates one dot-delimited segment below the
// prefix when the caller omits depth — the "immediate children" view.
const listNamespacesDefaultDepth uint32 = 1

// listNamespacesMaxDepth bounds how many segments deep a single call may
// aggregate. Deeper than this is almost always better expressed as a longer
// prefix.
const listNamespacesMaxDepth uint32 = 10

// listNamespacesScanBatch is the per-page size forwarded to ScanVertices.
const listNamespacesScanBatch uint32 = 500

// listNamespacesMaxScan bounds how many vertices a single call pulls before
// stopping. Facet discovery is an unindexed scan (ScanVertices returns full
// vertices even though we only read keys), so without this ceiling a survey
// of a huge keyspace would transfer every value. When the ceiling is hit the
// reported counts are lower bounds and truncated is set.
const listNamespacesMaxScan = 10_000

// listNamespacesMaxFacets caps how many distinct namespaces a single result
// returns, so a high depth over a diverse keyspace cannot flood the context.
const listNamespacesMaxFacets = 500

type listNamespacesInput struct {
	Prefix string `json:"prefix,omitempty" jsonschema:"Key prefix to enumerate beneath (e.g. user. or project.lantern. — end it at a segment boundary with a trailing dot for clean results, since matching is a byte prefix). Empty (the default) enumerates the TOP-LEVEL namespaces of the entire keyspace — the natural starting point for discovering what exists. Unlike list_under, an empty prefix is allowed here because only segment names and counts are returned, never values."`
	Depth  uint32 `json:"depth,omitempty" jsonschema:"How many dot-delimited segments below the prefix to aggregate into each namespace (default 1 = immediate children, max 10). depth=1 under 'topic.' groups topic.lantern.* and topic.build.* into 'lantern' and 'build'; depth=2 would distinguish their next segment too."`
}

// namespaceFacet is one distinct child namespace. Segment is the path
// relative to the requested prefix (the joined first `depth` segments).
// Count is how many facts fall under it. HasChildren reports whether at
// least one of those facts has further segments below this depth — i.e.
// whether drilling down (a longer prefix or a larger depth) would reveal
// more structure.
type namespaceFacet struct {
	Segment     string `json:"segment"`
	Count       int    `json:"count"`
	HasChildren bool   `json:"has_children"`
}

type listNamespacesOutput struct {
	Prefix     string           `json:"prefix"`
	Depth      uint32           `json:"depth"`
	Count      int              `json:"count"`
	Namespaces []namespaceFacet `json:"namespaces"`
	Scanned    int              `json:"scanned"`
	Truncated  bool             `json:"truncated"`
	Suggestion string           `json:"suggestion,omitempty"`
}

const listNamespacesDescription = "Discover the SHAPE of the key space: return the distinct child namespace segments under a prefix, each with a count of facts beneath it, WITHOUT returning any values. Call this PROACTIVELY to learn which namespaces exist — start with an empty prefix to see top-level user./project./session., then drill into the ones that look relevant — before guessing exact keys for recall_fact or running search_facts. depth controls how many dot-delimited segments deep to aggregate (default 1). has_children on a result marks namespaces you can drill into further. Cheap by design (no value payloads). Does NOT refresh TTL."

func registerListNamespaces(srv *mcp.Server, lc lanternClient) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_namespaces",
		Description: listNamespacesDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listNamespacesInput) (*mcp.CallToolResult, listNamespacesOutput, error) {
		depth := in.Depth
		if depth == 0 {
			depth = listNamespacesDefaultDepth
		}
		if depth > listNamespacesMaxDepth {
			depth = listNamespacesMaxDepth
		}

		type agg struct {
			count       int
			hasChildren bool
		}
		facets := make(map[string]*agg)
		scanned := 0
		scanBudgetHit := false
		var cursor []byte

	scan:
		for {
			verts, next, err := lc.ScanVertices(ctx, in.Prefix,
				client.WithScanLimit(listNamespacesScanBatch),
				client.WithScanCursor(cursor))
			if err != nil {
				return nil, listNamespacesOutput{}, mapSDKError("list_namespaces", err)
			}
			for _, v := range verts {
				if v == nil {
					continue
				}
				scanned++
				// rest is the key tail below the prefix. A key exactly equal
				// to the prefix has no child segment and is skipped.
				if rest := strings.TrimPrefix(v.GetKey(), in.Prefix); rest != "" {
					segs := strings.Split(rest, ".")
					n := int(depth)
					if n > len(segs) {
						n = len(segs)
					}
					facet := strings.Join(segs[:n], ".")
					a := facets[facet]
					if a == nil {
						a = &agg{}
						facets[facet] = a
					}
					a.count++
					if len(segs) > int(depth) {
						a.hasChildren = true
					}
				}
				if scanned >= listNamespacesMaxScan {
					scanBudgetHit = true
					break scan
				}
			}
			if len(next) == 0 {
				break
			}
			cursor = next
		}

		namespaces := make([]namespaceFacet, 0, len(facets))
		for seg, a := range facets {
			namespaces = append(namespaces, namespaceFacet{
				Segment:     seg,
				Count:       a.count,
				HasChildren: a.hasChildren,
			})
		}
		// Biggest buckets first so the most populated namespaces surface even
		// when the facet list is capped; segment order breaks ties for stable
		// output.
		sort.Slice(namespaces, func(i, j int) bool {
			if namespaces[i].Count != namespaces[j].Count {
				return namespaces[i].Count > namespaces[j].Count
			}
			return namespaces[i].Segment < namespaces[j].Segment
		})
		facetCapHit := false
		if len(namespaces) > listNamespacesMaxFacets {
			namespaces = namespaces[:listNamespacesMaxFacets]
			facetCapHit = true
		}

		out := listNamespacesOutput{
			Prefix:     in.Prefix,
			Depth:      depth,
			Count:      len(namespaces),
			Namespaces: namespaces,
			Scanned:    scanned,
			Truncated:  scanBudgetHit || facetCapHit,
		}
		switch {
		case scanBudgetHit:
			out.Suggestion = fmt.Sprintf("Stopped after scanning %d facts; counts are lower bounds. Pass a longer prefix to scope the survey to a sub-namespace.", scanned)
		case facetCapHit:
			out.Suggestion = fmt.Sprintf("More than %d distinct namespaces matched; showing the %d most populated. Pass a longer prefix or a smaller depth to narrow.", listNamespacesMaxFacets, listNamespacesMaxFacets)
		}
		text := fmt.Sprintf("Found %d namespace(s) under %q at depth %d (scanned=%d, truncated=%t).", out.Count, in.Prefix, out.Depth, out.Scanned, out.Truncated)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	})
}
