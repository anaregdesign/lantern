package mcp

import (
	"context"
	"strings"
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
)

// vstr builds a string-valued vertex for search tests.
func vstr(key, val string) *client.Vertex {
	return &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: val}}
}

// singlePage makes a scanVerticesFn that returns all of verts in one page
// (empty next cursor), regardless of prefix or options.
func singlePage(verts ...*client.Vertex) func(context.Context, string, ...client.ScanOption) ([]*client.Vertex, []byte, error) {
	return func(_ context.Context, _ string, _ ...client.ScanOption) ([]*client.Vertex, []byte, error) {
		return verts, nil, nil
	}
}

func TestSearchFacts_MatchesValueAcrossUnrelatedKeyPrefixes(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanVerticesFn = singlePage(
		vstr("project.lantern.milestone", "ship build 2026 in June"),
		vstr("user.identity.role", "principal engineer"),
		vstr("session.scratch", "unrelated note"),
	)
	res := h.call(t, "search_facts", map[string]any{"query": "build 2026"})
	if res.IsError {
		t.Fatalf("IsError = true: %s", contentText(res))
	}
	var out searchFactsOutput
	structuredAs(t, res, &out)
	if out.Count != 1 {
		t.Fatalf("Count = %d, want 1 (matches=%+v)", out.Count, out.Matches)
	}
	if out.Matches[0].Key != "project.lantern.milestone" {
		t.Fatalf("matched wrong key: %q", out.Matches[0].Key)
	}
}

func TestSearchFacts_MatchesKey(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanVerticesFn = singlePage(
		vstr("user.preferences.tone", "concise"),
		vstr("user.identity.role", "engineer"),
	)
	// "preferences" appears only in a key, not in any value.
	res := h.call(t, "search_facts", map[string]any{"query": "preferences"})
	var out searchFactsOutput
	structuredAs(t, res, &out)
	if out.Count != 1 || out.Matches[0].Key != "user.preferences.tone" {
		t.Fatalf("key match failed: %+v", out.Matches)
	}
}

func TestSearchFacts_IsCaseInsensitive(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanVerticesFn = singlePage(vstr("k", "The Build 2026 Plan"))
	res := h.call(t, "search_facts", map[string]any{"query": "build 2026"})
	var out searchFactsOutput
	structuredAs(t, res, &out)
	if out.Count != 1 {
		t.Fatalf("case-insensitive match failed: %+v", out)
	}
}

func TestSearchFacts_ResultsAreCompactSnippets(t *testing.T) {
	h := newTestHarness(t)
	long := "match " + strings.Repeat("z", 400)
	h.fake.scanVerticesFn = singlePage(vstr("k", long))
	res := h.call(t, "search_facts", map[string]any{"query": "match"})
	var out searchFactsOutput
	structuredAs(t, res, &out)
	if out.Count != 1 {
		t.Fatalf("Count = %d, want 1", out.Count)
	}
	snip := out.Matches[0].Snippet
	if !strings.HasSuffix(snip, "…") {
		t.Fatalf("long value should be returned as a truncated snippet: %q", snip)
	}
	if n := len([]rune(snip)); n > 121 {
		t.Fatalf("snippet length = %d runes, want <= 121 (no full-value dump)", n)
	}
}

func TestSearchFacts_NoMatchesIsEmptyNotError(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanVerticesFn = singlePage(vstr("k", "nothing relevant here"))
	res := h.call(t, "search_facts", map[string]any{"query": "absent"})
	if res.IsError {
		t.Fatalf("no-match should not be an error: %s", contentText(res))
	}
	var out searchFactsOutput
	structuredAs(t, res, &out)
	if out.Count != 0 || len(out.Matches) != 0 {
		t.Fatalf("expected zero matches, got %+v", out)
	}
}

func TestSearchFacts_RejectsEmptyQuery(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "search_facts", map[string]any{"query": ""})
}

func TestSearchFacts_ForwardsPrefixToScan(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanVerticesFn = singlePage(vstr("project.lantern.x", "build 2026"))
	h.call(t, "search_facts", map[string]any{"query": "build", "prefix": "project.lantern."})
	if h.fake.lastScanPrefix != "project.lantern." {
		t.Fatalf("prefix not forwarded to ScanVertices: %q", h.fake.lastScanPrefix)
	}
}

func TestSearchFacts_TruncatesAtLimit(t *testing.T) {
	h := newTestHarness(t)
	verts := make([]*client.Vertex, 0, 5)
	for i := 0; i < 5; i++ {
		verts = append(verts, vstr("k", "build 2026"))
	}
	h.fake.scanVerticesFn = singlePage(verts...)
	res := h.call(t, "search_facts", map[string]any{"query": "build", "limit": 2})
	var out searchFactsOutput
	structuredAs(t, res, &out)
	if out.Count != 2 {
		t.Fatalf("Count = %d, want 2 (capped at limit)", out.Count)
	}
	if !out.Truncated {
		t.Fatalf("Truncated = false; want true when limit is hit")
	}
	if out.Suggestion == "" {
		t.Fatalf("Suggestion should be set when truncated")
	}
}

func TestSearchFacts_FollowsCursorPagination(t *testing.T) {
	h := newTestHarness(t)
	calls := 0
	h.fake.scanVerticesFn = func(_ context.Context, _ string, _ ...client.ScanOption) ([]*client.Vertex, []byte, error) {
		calls++
		if calls == 1 {
			return []*client.Vertex{vstr("a", "no")}, []byte("cursor-1"), nil
		}
		return []*client.Vertex{vstr("b", "build 2026")}, nil, nil
	}
	res := h.call(t, "search_facts", map[string]any{"query": "build"})
	var out searchFactsOutput
	structuredAs(t, res, &out)
	if calls != 2 {
		t.Fatalf("expected 2 scan pages, got %d", calls)
	}
	if out.Count != 1 || out.Matches[0].Key != "b" {
		t.Fatalf("match on second page not found: %+v", out)
	}
	if out.Scanned != 2 {
		t.Fatalf("Scanned = %d, want 2 across both pages", out.Scanned)
	}
}

func TestSearchFacts_StopsAtScanBudget(t *testing.T) {
	h := newTestHarness(t)
	// One page larger than the scan budget, none matching, empty cursor.
	verts := make([]*client.Vertex, 0, searchFactsMaxScan+1)
	for i := 0; i < searchFactsMaxScan+1; i++ {
		verts = append(verts, vstr("k", "no match here"))
	}
	h.fake.scanVerticesFn = singlePage(verts...)
	res := h.call(t, "search_facts", map[string]any{"query": "needle"})
	var out searchFactsOutput
	structuredAs(t, res, &out)
	if !out.Truncated {
		t.Fatalf("Truncated = false; want true when the scan budget is hit")
	}
	if out.Scanned != searchFactsMaxScan {
		t.Fatalf("Scanned = %d, want %d (budget ceiling)", out.Scanned, searchFactsMaxScan)
	}
	if out.Suggestion == "" {
		t.Fatalf("Suggestion should advise narrowing when the budget is hit")
	}
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
