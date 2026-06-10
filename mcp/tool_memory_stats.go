package mcp

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/anaregdesign/lantern/mcp/internal/ttl"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// memoryStatsScanBatch is the per-page size forwarded to ScanVertices /
// ScanEdges while building the histogram and edge count.
const memoryStatsScanBatch uint32 = 500

// memoryStatsMaxVertexScan bounds how many vertices a single call examines
// for the TTL histogram. The histogram needs each vertex's expiration, so
// unlike the radix totals it cannot avoid a scan; this ceiling keeps a
// survey of a huge keyspace from transferring every vertex. When it is hit
// the histogram is a sample (sampled < vertices) and truncated is set.
const memoryStatsMaxVertexScan = 10_000

// memoryStatsMaxEdgeScan bounds the edge count scan the same way. There is
// no server-side radix count for edges, so the edge total is whatever this
// bounded scan observes; when the ceiling is hit the edge count is a lower
// bound and truncated is set.
const memoryStatsMaxEdgeScan = 10_000

type memoryStatsInput struct {
	Prefix string `json:"prefix,omitempty" jsonschema:"Optional key prefix to scope the stats to one namespace (e.g. project.lantern. or user.). Empty (the default) reports over the entire keyspace and adds a per-scope breakdown. When set, vertex/edge counts and the TTL histogram cover only keys under the prefix (edges are matched on their tail key)."`
}

// scopeCount is one recognized top-level namespace and how many facts live
// under it. Only emitted for a whole-keyspace call (empty prefix), since a
// prefixed call already names its scope.
type scopeCount struct {
	Scope string `json:"scope"`
	Count uint64 `json:"count"`
}

// ttlBucketCount is one entry of the remaining-life histogram: how many of
// the sampled vertices will expire within the named bucket's horizon. The
// special labels "expired" (past expiration but not yet garbage-collected)
// and "unbounded" (remaining life longer than the longest bucket, or no
// expiration set) bracket the canonical ladder.
type ttlBucketCount struct {
	Bucket string `json:"bucket"`
	Count  int    `json:"count"`
}

type memoryStatsOutput struct {
	Prefix string `json:"prefix"`
	// Vertices is the radix count of live vertices under the prefix. It is
	// fast and authoritative-ish but may transiently overshoot ScanVertices
	// under heavy expiration churn (see CountVerticesByPrefix), so it is the
	// total to quote, while the histogram below is a bounded sample.
	Vertices uint64 `json:"vertices"`
	// Edges is the number of edges observed by the bounded edge scan (tail
	// under the prefix). A lower bound when truncated is set.
	Edges int `json:"edges"`
	// Scopes is the per-recognized-scope vertex breakdown, present only for a
	// whole-keyspace call (empty prefix).
	Scopes []scopeCount `json:"scopes,omitempty"`
	// TTLBuckets is the remaining-life histogram over the sampled vertices,
	// in ascending-horizon order. It sums to Sampled, not to Vertices.
	TTLBuckets []ttlBucketCount `json:"ttl_buckets"`
	// Sampled is how many vertices were examined for the histogram. Equals
	// Vertices unless the scan budget was hit.
	Sampled int `json:"sampled"`
	// Truncated reports that a scan budget (vertex or edge) was hit, so the
	// histogram is a sample and/or the edge count is a lower bound.
	Truncated  bool   `json:"truncated"`
	Suggestion string `json:"suggestion,omitempty"`
}

const memoryStatsDescription = "Report how much is currently remembered and how soon it will decay — counts only, never values. Call this PROACTIVELY to gauge memory health: total facts (vertices) and relations (edges), a per-scope breakdown (session./task./project./user.), and a remaining-life histogram bucketing facts by how soon they expire (the seconds/transient/turn buckets are what is about to be forgotten). Pass a prefix to scope every number to one namespace (e.g. \"how many facts about project.lantern\"). Counts come from a cheap radix lookup; the histogram is a bounded sample, so a huge keyspace reports sampled<vertices and truncated=true. Reads nothing back and refreshes no TTL — purely observational."

// bucketHorizon pairs a bucket label with its resolved nominal duration,
// precomputed once per call in ascending order so classifyRemaining can do a
// single linear walk.
type bucketHorizon struct {
	label string
	dur   time.Duration
}

// classifyRemaining maps a vertex's remaining lifetime to the tightest TTL
// bucket label that still covers it ("expires within <bucket>"). A
// non-positive remaining is "expired" (past expiration but not yet GC'd); a
// remaining longer than the longest horizon is "unbounded". Vertices with no
// expiration at all are classified as unbounded by the caller before reaching
// here, so this only ever sees a real remaining duration.
func classifyRemaining(remaining time.Duration, horizons []bucketHorizon) string {
	if remaining <= 0 {
		return "expired"
	}
	for _, h := range horizons {
		if remaining <= h.dur {
			return h.label
		}
	}
	return "unbounded"
}

func registerMemoryStats(srv *mcp.Server, lc lanternClient, r *ttl.Resolver) {
	// Precompute the ascending bucket ladder once at registration; the
	// resolver is immutable after load so the horizons are stable.
	horizons := make([]bucketHorizon, 0, len(ttl.AllBuckets()))
	for _, b := range ttl.AllBuckets() {
		horizons = append(horizons, bucketHorizon{label: b.String(), dur: r.Resolve(b)})
	}
	// histogramOrder ranks bucket labels for stable, meaningful output:
	// the canonical ladder ascending, then unbounded, then expired.
	histogramOrder := make(map[string]int, len(horizons)+2)
	for i, h := range horizons {
		histogramOrder[h.label] = i
	}
	histogramOrder["unbounded"] = len(horizons)
	histogramOrder["expired"] = len(horizons) + 1

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "memory_stats",
		Description: memoryStatsDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in memoryStatsInput) (*mcp.CallToolResult, memoryStatsOutput, error) {
		now := time.Now()

		// Authoritative-ish total via a single radix count.
		total, err := lc.CountVerticesByPrefix(ctx, in.Prefix)
		if err != nil {
			return nil, memoryStatsOutput{}, mapSDKError("memory_stats", err)
		}

		// Per-scope breakdown only makes sense for a whole-keyspace call; a
		// prefixed call has already chosen its scope. Each is one cheap radix
		// lookup, so we issue them only for the recognized heads.
		var scopes []scopeCount
		if in.Prefix == "" {
			scopes = make([]scopeCount, 0, len(recognizedScopes))
			for scope := range recognizedScopes {
				n, serr := lc.CountVerticesByPrefix(ctx, scope+".")
				if serr != nil {
					return nil, memoryStatsOutput{}, mapSDKError("memory_stats", serr)
				}
				scopes = append(scopes, scopeCount{Scope: scope, Count: n})
			}
			sort.Slice(scopes, func(i, j int) bool {
				if scopes[i].Count != scopes[j].Count {
					return scopes[i].Count > scopes[j].Count
				}
				return scopes[i].Scope < scopes[j].Scope
			})
		}

		// Remaining-life histogram: a bounded vertex scan reading only key
		// presence and expiration, never the stored value.
		histogram := make(map[string]int)
		sampled := 0
		vertexBudgetHit := false
		var cursor []byte
	vscan:
		for {
			verts, next, serr := lc.ScanVertices(ctx, in.Prefix,
				client.WithScanLimit(memoryStatsScanBatch),
				client.WithScanCursor(cursor))
			if serr != nil {
				return nil, memoryStatsOutput{}, mapSDKError("memory_stats", serr)
			}
			for _, v := range verts {
				if v == nil {
					continue
				}
				sampled++
				// A vertex with no expiration lives until explicitly forgotten:
				// classify it as unbounded rather than letting a zero remaining
				// masquerade as "expired".
				if exp := client.VertexExpiration(v); exp.IsZero() {
					histogram["unbounded"]++
				} else {
					histogram[classifyRemaining(exp.Sub(now), horizons)]++
				}
				if sampled >= memoryStatsMaxVertexScan {
					vertexBudgetHit = true
					break vscan
				}
			}
			if len(next) == 0 {
				break
			}
			cursor = next
		}

		// Edge count: a bounded edge scan, narrowed to the prefix on the tail
		// dimension when one is given. No radix count exists for edges.
		edgeCount := 0
		edgeBudgetHit := false
		var edgeCursor []byte
		edgeOpts := func(cur []byte) []client.EdgeScanOption {
			opts := []client.EdgeScanOption{
				client.WithEdgeScanLimit(memoryStatsScanBatch),
				client.WithEdgeScanCursor(cur),
			}
			if in.Prefix != "" {
				opts = append(opts, client.WithEdgeScanTailPrefix(in.Prefix))
			}
			return opts
		}
	escan:
		for {
			edges, next, serr := lc.ScanEdges(ctx, edgeOpts(edgeCursor)...)
			if serr != nil {
				return nil, memoryStatsOutput{}, mapSDKError("memory_stats", serr)
			}
			for _, e := range edges {
				if e == nil {
					continue
				}
				edgeCount++
				if edgeCount >= memoryStatsMaxEdgeScan {
					edgeBudgetHit = true
					break escan
				}
			}
			if len(next) == 0 {
				break
			}
			edgeCursor = next
		}

		buckets := make([]ttlBucketCount, 0, len(histogram))
		for label, count := range histogram {
			buckets = append(buckets, ttlBucketCount{Bucket: label, Count: count})
		}
		sort.Slice(buckets, func(i, j int) bool {
			return histogramOrder[buckets[i].Bucket] < histogramOrder[buckets[j].Bucket]
		})

		out := memoryStatsOutput{
			Prefix:     in.Prefix,
			Vertices:   total,
			Edges:      edgeCount,
			Scopes:     scopes,
			TTLBuckets: buckets,
			Sampled:    sampled,
			Truncated:  vertexBudgetHit || edgeBudgetHit,
		}
		switch {
		case vertexBudgetHit:
			out.Suggestion = fmt.Sprintf("Histogram sampled the first %d facts; counts are exact but the decay profile is a sample. Pass a longer prefix to scope it to a sub-namespace.", sampled)
		case edgeBudgetHit:
			out.Suggestion = fmt.Sprintf("Stopped after counting %d relations; the edge total is a lower bound. Pass a prefix to scope the count.", edgeCount)
		}

		scopeText := "the whole keyspace"
		if in.Prefix != "" {
			scopeText = fmt.Sprintf("%q", in.Prefix)
		}
		text := fmt.Sprintf("Memory under %s holds %d fact(s) and %d relation(s); histogram over %d sampled fact(s), truncated=%t.",
			scopeText, out.Vertices, out.Edges, out.Sampled, out.Truncated)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	})
}
