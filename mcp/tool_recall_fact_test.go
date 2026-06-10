package mcp

import (
	"context"
	"strings"
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestRecallFact_Found(t *testing.T) {
	h := newTestHarness(t)
	h.fake.getVertexFn = func(_ context.Context, _ string) (*client.Vertex, error) {
		return &pb.Vertex{Key: "user.tone", Value: &pb.Vertex_String_{String_: "warm"}}, nil
	}
	res := h.call(t, "recall_fact", map[string]any{"key": "user.tone"})
	if res.IsError {
		t.Fatalf("IsError = true; content=%+v", res.Content)
	}
	var out recallFactOutput
	structuredAs(t, res, &out)
	if !out.Found {
		t.Fatalf("Found = false; want true")
	}
	if out.Key != "user.tone" || out.Value != "warm" {
		t.Fatalf("unexpected output: %+v", out)
	}
}

func TestRecallFact_NotFoundIsStructured(t *testing.T) {
	h := newTestHarness(t)
	h.fake.getVertexFn = func(_ context.Context, _ string) (*client.Vertex, error) {
		return nil, client.ErrNotFound
	}
	res := h.call(t, "recall_fact", map[string]any{"key": "nope"})
	if res.IsError {
		t.Fatalf("not_found should be a structured result, not a tool error: %+v", res.Content)
	}
	var out recallFactOutput
	structuredAs(t, res, &out)
	if out.Found {
		t.Fatalf("Found = true; want false")
	}
	if out.Key != "nope" {
		t.Fatalf("Key = %q", out.Key)
	}
}

func TestRecallFact_RejectsEmptyKey(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "recall_fact", map[string]any{"key": ""})
}

// TestRecallFactDescription_IsProactive guards the recall-before-answering
// framing while keeping the no-refresh invariant (#528).
func TestRecallFactDescription_IsProactive(t *testing.T) {
	if !strings.Contains(strings.ToUpper(recallFactDescription), "PROACTIVELY") {
		t.Errorf("recallFactDescription should tell the agent to recall PROACTIVELY: %q", recallFactDescription)
	}
	if !strings.Contains(recallFactDescription, "does NOT refresh") {
		t.Errorf("recallFactDescription should keep the recall-does-not-refresh invariant: %q", recallFactDescription)
	}
}
