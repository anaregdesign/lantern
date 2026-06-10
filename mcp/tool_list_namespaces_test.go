package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	client "github.com/anaregdesign/lantern/sdks/go"
)

// keysOf extracts the segment->facet map from a result for terse assertions.
func facetsBySegment(out listNamespacesOutput) map[string]namespaceFacet {
	m := make(map[string]namespaceFacet, len(out.Namespaces))
	for _, f := range out.Namespaces {
		m[f.Segment] = f
	}
	return m
}

func TestListNamespaces_AggregatesChildSegments(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanVerticesFn = singlePage(
		vstr("topic.lantern.a", "x"),
		vstr("topic.lantern.b", "y"),
		vstr("topic.build-2026.x", "z"),
	)
	res := h.call(t, "list_namespaces", map[string]any{"prefix": "topic."})
	if res.IsError {
		t.Fatalf("IsError = true: %s", contentText(res))
	}
	var out listNamespacesOutput
	structuredAs(t, res, &out)
	if out.Count != 2 {
		t.Fatalf("Count = %d, want 2 (namespaces=%+v)", out.Count, out.Namespaces)
	}
	// Biggest bucket first: lantern (2) before build-2026 (1).
	if out.Namespaces[0].Segment != "lantern" {
		t.Fatalf("expected lantern first (count desc), got %+v", out.Namespaces)
	}
	by := facetsBySegment(out)
	if by["lantern"].Count != 2 || !by["lantern"].HasChildren {
		t.Fatalf("lantern facet wrong: %+v", by["lantern"])
	}
	if by["build-2026"].Count != 1 || !by["build-2026"].HasChildren {
		t.Fatalf("build-2026 facet wrong: %+v", by["build-2026"])
	}
}

func TestListNamespaces_DepthControlsAggregation(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanVerticesFn = singlePage(
		vstr("topic.lantern.a", "x"),
		vstr("topic.lantern.b", "y"),
		vstr("topic.build-2026.x", "z"),
	)
	res := h.call(t, "list_namespaces", map[string]any{"prefix": "topic.", "depth": 2})
	var out listNamespacesOutput
	structuredAs(t, res, &out)
	if out.Depth != 2 {
		t.Fatalf("Depth echoed = %d, want 2", out.Depth)
	}
	// Two segments deep distinguishes the leaf keys.
	if out.Count != 3 {
		t.Fatalf("Count = %d, want 3 (namespaces=%+v)", out.Count, out.Namespaces)
	}
	by := facetsBySegment(out)
	for _, seg := range []string{"lantern.a", "lantern.b", "build-2026.x"} {
		f, ok := by[seg]
		if !ok {
			t.Fatalf("missing facet %q: %+v", seg, out.Namespaces)
		}
		if f.HasChildren {
			t.Fatalf("facet %q should have no children at depth 2: %+v", seg, f)
		}
	}
}

// TestListNamespaces_ReturnsNoValues guards that facet discovery never leaks
// stored values into the result — only segment names and counts.
func TestListNamespaces_ReturnsNoValues(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanVerticesFn = singlePage(
		vstr("topic.secret.k", "TOPSECRETVALUE"),
	)
	res := h.call(t, "list_namespaces", map[string]any{"prefix": "topic."})
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if strings.Contains(string(raw), "TOPSECRETVALUE") {
		t.Fatalf("result leaked a stored value: %s", raw)
	}
}

// TestListNamespaces_EmptyPrefixEnumeratesTopLevel verifies the key
// difference from list_under: an empty prefix is allowed and returns the
// top-level namespaces of the whole keyspace.
func TestListNamespaces_EmptyPrefixEnumeratesTopLevel(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanVerticesFn = singlePage(
		vstr("user.identity.role", "engineer"),
		vstr("project.lantern.stack", "go"),
		vstr("session.current-task", "issue-550"),
	)
	res := h.call(t, "list_namespaces", map[string]any{})
	if res.IsError {
		t.Fatalf("empty prefix should be allowed: %s", contentText(res))
	}
	var out listNamespacesOutput
	structuredAs(t, res, &out)
	if out.Depth != 1 {
		t.Fatalf("Depth defaulted = %d, want 1", out.Depth)
	}
	if out.Count != 3 {
		t.Fatalf("Count = %d, want 3 top-level namespaces: %+v", out.Count, out.Namespaces)
	}
	by := facetsBySegment(out)
	for _, seg := range []string{"user", "project", "session"} {
		if _, ok := by[seg]; !ok {
			t.Fatalf("missing top-level namespace %q: %+v", seg, out.Namespaces)
		}
	}
}

// TestListNamespaces_HasChildrenFlag checks the drill-down signal: a key with
// deeper segments sets has_children; a flat key does not.
func TestListNamespaces_HasChildrenFlag(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanVerticesFn = singlePage(
		vstr("a.b.c", "deep"),
		vstr("x", "flat"),
	)
	res := h.call(t, "list_namespaces", map[string]any{})
	var out listNamespacesOutput
	structuredAs(t, res, &out)
	by := facetsBySegment(out)
	if !by["a"].HasChildren {
		t.Fatalf("facet a should have children: %+v", by["a"])
	}
	if by["x"].HasChildren {
		t.Fatalf("facet x should not have children: %+v", by["x"])
	}
}

func TestListNamespaces_ForwardsPrefixToScan(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanVerticesFn = singlePage(vstr("user.a.b", "x"))
	_ = h.call(t, "list_namespaces", map[string]any{"prefix": "user."})
	if h.fake.lastScanPrefix != "user." {
		t.Fatalf("prefix forwarded to scan = %q, want %q", h.fake.lastScanPrefix, "user.")
	}
}

func TestListNamespaces_FollowsCursorPagination(t *testing.T) {
	h := newTestHarness(t)
	calls := 0
	h.fake.scanVerticesFn = func(_ context.Context, _ string, _ ...client.ScanOption) ([]*client.Vertex, []byte, error) {
		calls++
		if calls == 1 {
			return []*client.Vertex{vstr("topic.lantern.a", "x")}, []byte("cursor-1"), nil
		}
		return []*client.Vertex{vstr("topic.lantern.b", "y")}, nil, nil
	}
	res := h.call(t, "list_namespaces", map[string]any{"prefix": "topic."})
	var out listNamespacesOutput
	structuredAs(t, res, &out)
	if calls != 2 {
		t.Fatalf("expected 2 scan pages, got %d", calls)
	}
	if out.Scanned != 2 {
		t.Fatalf("Scanned = %d, want 2 across both pages", out.Scanned)
	}
	by := facetsBySegment(out)
	if by["lantern"].Count != 2 {
		t.Fatalf("counts should aggregate across pages: %+v", out.Namespaces)
	}
}

func TestListNamespaces_DepthDefaultedAndCapped(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanVerticesFn = singlePage(vstr("a.b", "x"))

	// depth omitted -> default 1.
	var def listNamespacesOutput
	structuredAs(t, h.call(t, "list_namespaces", map[string]any{}), &def)
	if def.Depth != listNamespacesDefaultDepth {
		t.Fatalf("default depth = %d, want %d", def.Depth, listNamespacesDefaultDepth)
	}

	// depth above the ceiling -> clamped to max.
	var capped listNamespacesOutput
	structuredAs(t, h.call(t, "list_namespaces", map[string]any{"depth": listNamespacesMaxDepth + 5}), &capped)
	if capped.Depth != listNamespacesMaxDepth {
		t.Fatalf("capped depth = %d, want %d", capped.Depth, listNamespacesMaxDepth)
	}
}

func TestListNamespaces_StopsAtScanBudget(t *testing.T) {
	h := newTestHarness(t)
	verts := make([]*client.Vertex, 0, listNamespacesMaxScan+1)
	for i := 0; i < listNamespacesMaxScan+1; i++ {
		verts = append(verts, vstr("topic.lantern.k", "v"))
	}
	h.fake.scanVerticesFn = singlePage(verts...)
	res := h.call(t, "list_namespaces", map[string]any{"prefix": "topic."})
	var out listNamespacesOutput
	structuredAs(t, res, &out)
	if !out.Truncated {
		t.Fatalf("Truncated = false; want true when the scan budget is hit")
	}
	if out.Scanned != listNamespacesMaxScan {
		t.Fatalf("Scanned = %d, want %d (budget ceiling)", out.Scanned, listNamespacesMaxScan)
	}
	if out.Suggestion == "" {
		t.Fatalf("Suggestion should advise narrowing when the budget is hit")
	}
}

// TestListNamespacesDescription_IsProactiveDiscovery guards the framing that
// markets list_namespaces as a proactive discovery surface and keeps the
// no-refresh invariant.
func TestListNamespacesDescription_IsProactiveDiscovery(t *testing.T) {
	if !strings.Contains(listNamespacesDescription, "PROACTIVELY") {
		t.Errorf("description should encourage proactive discovery: %q", listNamespacesDescription)
	}
	if !strings.Contains(listNamespacesDescription, "NOT refresh") {
		t.Errorf("description should keep the does-not-refresh invariant: %q", listNamespacesDescription)
	}
}
