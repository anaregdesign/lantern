package mcp

import (
	"context"
	"strings"
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestListUnder_DefaultLimitAndTruncation(t *testing.T) {
	h := newTestHarness(t)
	// Return 51 results — the handler asks for limit+1 to detect truncation.
	h.fake.scanVerticesFn = func(_ context.Context, _ string, _ ...client.ScanOption) ([]*client.Vertex, []byte, error) {
		out := make([]*client.Vertex, 0, 51)
		for i := 0; i < 51; i++ {
			out = append(out, &pb.Vertex{Key: "user.preferences.k", Value: &pb.Vertex_String_{String_: "v"}})
		}
		return out, nil, nil
	}
	res := h.call(t, "list_under", map[string]any{"prefix": "user.preferences."})
	if res.IsError {
		t.Fatalf("IsError = true")
	}
	var out listUnderOutput
	structuredAs(t, res, &out)
	if out.Count != int(listUnderDefaultLimit) {
		t.Fatalf("Count = %d, want %d", out.Count, listUnderDefaultLimit)
	}
	if !out.HasMore {
		t.Fatalf("HasMore = false; want true")
	}
	if out.Suggestion == "" {
		t.Fatalf("Suggestion should be set when truncated")
	}
}

func TestListUnder_RespectsExplicitLimit(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanVerticesFn = func(_ context.Context, _ string, opts ...client.ScanOption) ([]*client.Vertex, []byte, error) {
		// Return exactly the limit, no truncation.
		out := make([]*client.Vertex, 0, 10)
		for i := 0; i < 10; i++ {
			out = append(out, &pb.Vertex{Key: "k", Value: &pb.Vertex_String_{String_: "v"}})
		}
		return out, nil, nil
	}
	res := h.call(t, "list_under", map[string]any{"prefix": "k.", "limit": 10})
	var out listUnderOutput
	structuredAs(t, res, &out)
	if out.HasMore {
		t.Fatalf("HasMore should be false when exactly limit rows returned")
	}
	if out.Count != 10 {
		t.Fatalf("Count = %d, want 10", out.Count)
	}
}

func TestListUnder_RejectsEmptyPrefix(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "list_under", map[string]any{"prefix": ""})
}

// fakeListVertices returns a fixed set of string vertices for projection
// tests: one short value and one long value so snippet truncation is
// observable.
func fakeListVertices() []*client.Vertex {
	return []*client.Vertex{
		{Key: "topic.a", Value: &pb.Vertex_String_{String_: "short"}},
		{Key: "topic.b", Value: &pb.Vertex_String_{String_: strings.Repeat("y", 300)}},
	}
}

func TestListUnder_DefaultProjectionIsFull(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanVerticesFn = func(_ context.Context, _ string, _ ...client.ScanOption) ([]*client.Vertex, []byte, error) {
		return fakeListVertices(), nil, nil
	}
	res := h.call(t, "list_under", map[string]any{"prefix": "topic."})
	var out listUnderOutput
	structuredAs(t, res, &out)
	if out.Projection != "full" {
		t.Fatalf("Projection = %q, want full (back-compat default)", out.Projection)
	}
	if len(out.Entries) != 2 {
		t.Fatalf("Count = %d, want 2", len(out.Entries))
	}
	if out.Entries[0].Value != "short" {
		t.Fatalf("full projection should return Value; got %+v", out.Entries[0])
	}
	if out.Entries[0].Snippet != "" {
		t.Fatalf("full projection should not set Snippet; got %q", out.Entries[0].Snippet)
	}
}

func TestListUnder_KeysProjectionOmitsValues(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanVerticesFn = func(_ context.Context, _ string, _ ...client.ScanOption) ([]*client.Vertex, []byte, error) {
		return fakeListVertices(), nil, nil
	}
	res := h.call(t, "list_under", map[string]any{"prefix": "topic.", "projection": "keys"})
	var out listUnderOutput
	structuredAs(t, res, &out)
	if out.Projection != "keys" {
		t.Fatalf("Projection = %q, want keys", out.Projection)
	}
	for _, e := range out.Entries {
		if e.Key == "" {
			t.Fatalf("keys projection must still return Key: %+v", e)
		}
		if e.Value != nil || e.Snippet != "" {
			t.Fatalf("keys projection must omit Value and Snippet: %+v", e)
		}
	}
}

func TestListUnder_SnippetProjectionTruncates(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanVerticesFn = func(_ context.Context, _ string, _ ...client.ScanOption) ([]*client.Vertex, []byte, error) {
		return fakeListVertices(), nil, nil
	}
	res := h.call(t, "list_under", map[string]any{"prefix": "topic.", "projection": "snippet"})
	var out listUnderOutput
	structuredAs(t, res, &out)
	if out.Projection != "snippet" {
		t.Fatalf("Projection = %q, want snippet", out.Projection)
	}
	if out.Entries[0].Value != nil {
		t.Fatalf("snippet projection must omit Value: %+v", out.Entries[0])
	}
	if out.Entries[0].Snippet != "short" {
		t.Fatalf("short value should pass through: %q", out.Entries[0].Snippet)
	}
	long := out.Entries[1].Snippet
	if !strings.HasSuffix(long, "…") {
		t.Fatalf("long value should be truncated with ellipsis: %q", long)
	}
	if n := len([]rune(long)); n > 121 {
		t.Fatalf("snippet length = %d runes, want <= 121", n)
	}
}

func TestListUnder_RejectsUnknownProjection(t *testing.T) {
	h := newTestHarness(t)
	res := h.callExpectError(t, "list_under", map[string]any{"prefix": "topic.", "projection": "bogus"})
	if !strings.Contains(contentText(res), "projection") {
		t.Fatalf("error should mention projection: %q", contentText(res))
	}
}

// TestListUnderDescription_IsProactive guards the survey-before-answering
// framing while keeping the no-refresh invariant (#528).
func TestListUnderDescription_IsProactive(t *testing.T) {
	if !strings.Contains(strings.ToUpper(listUnderDescription), "PROACTIVELY") {
		t.Errorf("listUnderDescription should tell the agent to survey PROACTIVELY: %q", listUnderDescription)
	}
	if !strings.Contains(listUnderDescription, "NOT refresh") {
		t.Errorf("listUnderDescription should keep the does-not-refresh invariant: %q", listUnderDescription)
	}
}
