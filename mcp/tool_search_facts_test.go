package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
)

// vstr builds a string-valued vertex for search tests.
func vstr(key, val string) *client.Vertex {
	return &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: val}}
}

// singlePage makes a scanVerticesFn that returns all of verts in one page
// (empty next cursor), regardless of prefix or options. It is shared by the
// scan-backed tool tests in this package.
func singlePage(verts ...*client.Vertex) func(context.Context, string, ...client.ScanOption) ([]*client.Vertex, []byte, error) {
	return func(_ context.Context, _ string, _ ...client.ScanOption) ([]*client.Vertex, []byte, error) {
		return verts, nil, nil
	}
}

// hit builds a ranked search hit. search_facts only consumes the key; score is
// carried for ordering realism.
func hit(key string, score float64) client.SearchHit {
	return client.SearchHit{Key: key, Score: score}
}

// hydrateFrom returns a getVerticesFn that resolves keys against the supplied
// vertices (by key), mimicking a batch GetVertices read. Unknown keys are
// reported as missing.
func hydrateFrom(verts ...*client.Vertex) func(context.Context, []string) ([]*client.Vertex, []string, error) {
	byKey := make(map[string]*client.Vertex, len(verts))
	for _, v := range verts {
		byKey[v.GetKey()] = v
	}
	return func(_ context.Context, keys []string) ([]*client.Vertex, []string, error) {
		var found []*client.Vertex
		var missing []string
		for _, k := range keys {
			if v, ok := byKey[k]; ok {
				found = append(found, v)
			} else {
				missing = append(missing, k)
			}
		}
		return found, missing, nil
	}
}

func TestSearchFacts_ForwardsQueryAndOptions(t *testing.T) {
	h := newTestHarness(t)
	h.fake.searchVerticesFn = func(_ context.Context, _ string, opts ...client.SearchOption) ([]client.SearchHit, error) {
		return []client.SearchHit{hit("project.lantern.milestone", 0.9)}, nil
	}
	h.fake.getVerticesFn = hydrateFrom(vstr("project.lantern.milestone", "ship build 2026 in June"))

	res := h.call(t, "search_facts", map[string]any{"query": "build 2026", "prefix": "project.lantern."})
	if res.IsError {
		t.Fatalf("IsError = true: %s", contentText(res))
	}
	if h.fake.lastSearchQuery != "build 2026" {
		t.Fatalf("query forwarded = %q, want %q", h.fake.lastSearchQuery, "build 2026")
	}
	// The handler always forwards both WithSearchLimit and WithSearchPrefix;
	// their values are covered by the SDK forwarder tests, which can inspect
	// the request the options build.
	if len(h.fake.lastSearchOptions) != 2 {
		t.Fatalf("forwarded %d search options, want 2 (limit + prefix)", len(h.fake.lastSearchOptions))
	}
	var out searchFactsOutput
	structuredAs(t, res, &out)
	if out.Count != 1 || out.Matches[0].Key != "project.lantern.milestone" {
		t.Fatalf("unexpected matches: %+v", out.Matches)
	}
	if out.Matches[0].Snippet == "" {
		t.Fatalf("snippet not hydrated: %+v", out.Matches[0])
	}
}

func TestSearchFacts_PreservesRankOrder(t *testing.T) {
	h := newTestHarness(t)
	// Index ranks b above a.
	h.fake.searchVerticesFn = func(context.Context, string, ...client.SearchOption) ([]client.SearchHit, error) {
		return []client.SearchHit{hit("b", 0.9), hit("a", 0.4)}, nil
	}
	// GetVertices answers in a different (key-sorted) order to prove the
	// handler re-keys by rank, not by the batch-read order.
	h.fake.getVerticesFn = func(_ context.Context, keys []string) ([]*client.Vertex, []string, error) {
		return []*client.Vertex{vstr("a", "alpha"), vstr("b", "bravo")}, nil, nil
	}

	res := h.call(t, "search_facts", map[string]any{"query": "x"})
	var out searchFactsOutput
	structuredAs(t, res, &out)
	if out.Count != 2 {
		t.Fatalf("Count = %d, want 2", out.Count)
	}
	if out.Matches[0].Key != "b" || out.Matches[1].Key != "a" {
		t.Fatalf("rank order not preserved: %+v", out.Matches)
	}
	// Hydration is requested with the keys in ranked order.
	if got := h.fake.lastGetVerticesKeys; len(got) != 2 || got[0] != "b" || got[1] != "a" {
		t.Fatalf("GetVertices keys = %v, want [b a]", got)
	}
}

func TestSearchFacts_HydratesSnippetAndExpiry(t *testing.T) {
	h := newTestHarness(t)
	exp := time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC)
	long := "match " + strings.Repeat("z", 400)
	h.fake.searchVerticesFn = func(context.Context, string, ...client.SearchOption) ([]client.SearchHit, error) {
		return []client.SearchHit{hit("k", 1)}, nil
	}
	h.fake.getVerticesFn = hydrateFrom(vexp("k", long, exp))

	res := h.call(t, "search_facts", map[string]any{"query": "match"})
	var out searchFactsOutput
	structuredAs(t, res, &out)
	if out.Count != 1 {
		t.Fatalf("Count = %d, want 1", out.Count)
	}
	snip := out.Matches[0].Snippet
	if !strings.HasSuffix(snip, "…") {
		t.Fatalf("long value should be a truncated snippet: %q", snip)
	}
	if n := len([]rune(snip)); n > 121 {
		t.Fatalf("snippet length = %d runes, want <= 121 (no full-value dump)", n)
	}
	if out.Matches[0].ExpiresAt != "2031-02-03T04:05:06Z" {
		t.Fatalf("expires_at = %q, want the vertex's own expiry", out.Matches[0].ExpiresAt)
	}
}

func TestSearchFacts_MissingHydrationStillEmitsKey(t *testing.T) {
	h := newTestHarness(t)
	// A hit whose vertex raced away (expired/deleted) between search and get.
	h.fake.searchVerticesFn = func(context.Context, string, ...client.SearchOption) ([]client.SearchHit, error) {
		return []client.SearchHit{hit("ghost", 0.7)}, nil
	}
	h.fake.getVerticesFn = hydrateFrom() // resolves nothing

	res := h.call(t, "search_facts", map[string]any{"query": "x"})
	if res.IsError {
		t.Fatalf("IsError = true: %s", contentText(res))
	}
	var out searchFactsOutput
	structuredAs(t, res, &out)
	if out.Count != 1 || out.Matches[0].Key != "ghost" {
		t.Fatalf("missing hydration should still surface the key: %+v", out.Matches)
	}
	if out.Matches[0].Snippet != "" || out.Matches[0].ExpiresAt != "" {
		t.Fatalf("unhydrated match should carry only the key: %+v", out.Matches[0])
	}
}

func TestSearchFacts_NoMatchesIsEmptyNotError(t *testing.T) {
	h := newTestHarness(t)
	h.fake.searchVerticesFn = func(context.Context, string, ...client.SearchOption) ([]client.SearchHit, error) {
		return nil, nil
	}
	res := h.call(t, "search_facts", map[string]any{"query": "absent"})
	if res.IsError {
		t.Fatalf("no-match should not be an error: %s", contentText(res))
	}
	var out searchFactsOutput
	structuredAs(t, res, &out)
	if out.Count != 0 || len(out.Matches) != 0 {
		t.Fatalf("expected zero matches, got %+v", out)
	}
	// No hits means no hydration round trip.
	if h.fake.lastGetVerticesKeys != nil {
		t.Fatalf("GetVertices should not be called when there are no hits: %v", h.fake.lastGetVerticesKeys)
	}
}

func TestSearchFacts_RejectsEmptyQuery(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "search_facts", map[string]any{"query": ""})
	// A rejected query never reaches the server.
	if h.fake.lastSearchQuery != "" {
		t.Fatalf("empty query should not be forwarded, got %q", h.fake.lastSearchQuery)
	}
}

func TestSearchFacts_DisabledIndexSurfacesEnableHint(t *testing.T) {
	h := newTestHarness(t)
	h.fake.searchVerticesFn = func(context.Context, string, ...client.SearchOption) ([]client.SearchHit, error) {
		return nil, fmt.Errorf("search is off: %w", client.ErrFailedPrecondition)
	}
	res := h.callExpectError(t, "search_facts", map[string]any{"query": "x"})
	if !strings.Contains(contentText(res), "LANTERN_SEARCH_ENABLED") {
		t.Fatalf("disabled-index error should name the enable flag: %s", contentText(res))
	}
}

func TestSearchFacts_SearchErrorMapsThroughSDKError(t *testing.T) {
	h := newTestHarness(t)
	h.fake.searchVerticesFn = func(context.Context, string, ...client.SearchOption) ([]client.SearchHit, error) {
		return nil, client.ErrResourceExhausted
	}
	res := h.callExpectError(t, "search_facts", map[string]any{"query": "x"})
	if !strings.Contains(contentText(res), "rate limited") {
		t.Fatalf("resource-exhausted should map to a backoff hint: %s", contentText(res))
	}
}

func TestSearchFacts_HydrationErrorSurfaces(t *testing.T) {
	h := newTestHarness(t)
	h.fake.searchVerticesFn = func(context.Context, string, ...client.SearchOption) ([]client.SearchHit, error) {
		return []client.SearchHit{hit("k", 1)}, nil
	}
	h.fake.getVerticesFn = func(context.Context, []string) ([]*client.Vertex, []string, error) {
		return nil, nil, fmt.Errorf("boom")
	}
	h.callExpectError(t, "search_facts", map[string]any{"query": "x"})
}

// TestSearchFactsDescription_IsApproximateRecall guards the framing that
// distinguishes search_facts (approximate) from recall_fact (exact) and
// keeps the no-refresh invariant.
func TestSearchFactsDescription_IsApproximateRecall(t *testing.T) {
	if !strings.Contains(searchFactsDescription, "recall_fact") {
		t.Errorf("description should relate search_facts to recall_fact: %q", searchFactsDescription)
	}
	if !strings.Contains(searchFactsDescription, "NOT refresh") {
		t.Errorf("description should keep the does-not-refresh invariant: %q", searchFactsDescription)
	}
}
