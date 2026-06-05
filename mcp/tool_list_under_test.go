package mcp

import (
	"context"
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
