package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRecallRelation_Found(t *testing.T) {
	h := newTestHarness(t)
	exp, err := time.Parse(time.RFC3339, "2031-01-02T03:04:05Z")
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	h.fake.getEdgeFn = func(_ context.Context, tail, head string) (*client.Edge, error) {
		return &pb.Edge{Tail: tail, Head: head, Weight: 2.5, Expiration: timestamppb.New(exp)}, nil
	}
	res := h.call(t, "recall_relation", map[string]any{"from": "a", "to": "b"})
	if res.IsError {
		t.Fatalf("IsError = true; content=%+v", res.Content)
	}
	var out recallRelationOutput
	structuredAs(t, res, &out)
	if !out.Found {
		t.Fatalf("Found = false; want true")
	}
	if out.From != "a" || out.To != "b" {
		t.Fatalf("unexpected endpoints: %+v", out)
	}
	if out.Weight != 2.5 {
		t.Fatalf("Weight = %v; want 2.5", out.Weight)
	}
	if out.ExpiresAt != "2031-01-02T03:04:05Z" {
		t.Fatalf("ExpiresAt = %q; want 2031-01-02T03:04:05Z", out.ExpiresAt)
	}
	// The handler must read the edge by the exact (from, to) endpoints.
	if h.fake.lastGetEdgeTail != "a" || h.fake.lastGetEdgeHead != "b" {
		t.Fatalf("GetEdge called with (%q,%q); want (a,b)", h.fake.lastGetEdgeTail, h.fake.lastGetEdgeHead)
	}
}

func TestRecallRelation_FoundWithoutExpiration(t *testing.T) {
	h := newTestHarness(t)
	h.fake.getEdgeFn = func(_ context.Context, tail, head string) (*client.Edge, error) {
		return &pb.Edge{Tail: tail, Head: head, Weight: 1}, nil
	}
	res := h.call(t, "recall_relation", map[string]any{"from": "a", "to": "b"})
	if res.IsError {
		t.Fatalf("IsError = true; content=%+v", res.Content)
	}
	var out recallRelationOutput
	structuredAs(t, res, &out)
	if !out.Found || out.Weight != 1 {
		t.Fatalf("unexpected output: %+v", out)
	}
	if out.ExpiresAt != "" {
		t.Fatalf("ExpiresAt = %q; want empty for a permanent edge", out.ExpiresAt)
	}
}

func TestRecallRelation_NotFoundIsStructured(t *testing.T) {
	h := newTestHarness(t)
	h.fake.getEdgeFn = func(_ context.Context, _, _ string) (*client.Edge, error) {
		return nil, client.ErrNotFound
	}
	res := h.call(t, "recall_relation", map[string]any{"from": "a", "to": "missing"})
	if res.IsError {
		t.Fatalf("not_found should be a structured result, not a tool error: %+v", res.Content)
	}
	var out recallRelationOutput
	structuredAs(t, res, &out)
	if out.Found {
		t.Fatalf("Found = true; want false")
	}
	if out.From != "a" || out.To != "missing" {
		t.Fatalf("unexpected endpoints on miss: %+v", out)
	}
}

// TestRecallRelation_NilEdgeIsNotFound guards the defensive branch: a nil
// edge with no error must not surface as found=true with a zero weight.
func TestRecallRelation_NilEdgeIsNotFound(t *testing.T) {
	h := newTestHarness(t)
	h.fake.getEdgeFn = func(_ context.Context, _, _ string) (*client.Edge, error) {
		return nil, nil
	}
	res := h.call(t, "recall_relation", map[string]any{"from": "a", "to": "b"})
	if res.IsError {
		t.Fatalf("nil edge should be a structured miss, not a tool error: %+v", res.Content)
	}
	var out recallRelationOutput
	structuredAs(t, res, &out)
	if out.Found {
		t.Fatalf("Found = true; want false for a nil edge")
	}
}

func TestRecallRelation_RejectsEmptyEndpoints(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "recall_relation", map[string]any{"from": "", "to": "b"})
	h.callExpectError(t, "recall_relation", map[string]any{"from": "a", "to": ""})
}

func TestRecallRelation_RateLimitMapsToError(t *testing.T) {
	h := newTestHarness(t)
	h.fake.getEdgeFn = func(_ context.Context, _, _ string) (*client.Edge, error) {
		return nil, client.ErrResourceExhausted
	}
	res := h.callExpectError(t, "recall_relation", map[string]any{"from": "a", "to": "b"})
	if !strings.Contains(contentText(res), "rate limited") {
		t.Fatalf("expected rate-limit guidance; got %q", contentText(res))
	}
}

// TestRecallRelationDescription_IsProactive guards the recall-before-relying
// framing and the no-refresh invariant (#528 alignment).
func TestRecallRelationDescription_IsProactive(t *testing.T) {
	if !strings.Contains(strings.ToUpper(recallRelationDescription), "PROACTIVELY") {
		t.Errorf("recallRelationDescription should tell the agent to recall PROACTIVELY: %q", recallRelationDescription)
	}
	if !strings.Contains(recallRelationDescription, "does NOT refresh") {
		t.Errorf("recallRelationDescription should keep the recall-does-not-refresh invariant: %q", recallRelationDescription)
	}
}
