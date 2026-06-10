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

func TestRememberRelation_DefaultWeight(t *testing.T) {
	h := newTestHarness(t)
	res := h.call(t, "remember_relation", map[string]any{
		"from": "a", "to": "b", "ttl": "day",
	})
	if res.IsError {
		t.Fatalf("IsError = true")
	}
	var out rememberRelationOutput
	structuredAs(t, res, &out)
	if out.Increment != 1 {
		t.Fatalf("default increment = %v, want 1", out.Increment)
	}
	// With no edge to read back (default fake), the accumulated weight falls
	// back to this write's increment.
	if out.AccumulatedWeight != 1 {
		t.Fatalf("accumulated weight fallback = %v, want 1", out.AccumulatedWeight)
	}
	if h.fake.lastEdgeTail != "a" || h.fake.lastEdgeHead != "b" {
		t.Fatalf("edge args = (%s, %s)", h.fake.lastEdgeTail, h.fake.lastEdgeHead)
	}
	if h.fake.lastEdgeTTL != 24*time.Hour {
		t.Fatalf("TTL = %v, want 24h", h.fake.lastEdgeTTL)
	}
	// The handler must read the edge back by the exact endpoints it wrote.
	if h.fake.lastGetEdgeTail != "a" || h.fake.lastGetEdgeHead != "b" {
		t.Fatalf("GetEdge args = (%s, %s), want (a, b)", h.fake.lastGetEdgeTail, h.fake.lastGetEdgeHead)
	}
}

func TestRememberRelation_AdditiveSemanticDocumented(t *testing.T) {
	// We can't assert weight-summing without a real backend, but we can
	// verify the handler is called multiple times and the description
	// surfaces the additive contract — which is what the LLM relies on.
	h := newTestHarness(t)
	for i := 0; i < 3; i++ {
		h.call(t, "remember_relation", map[string]any{
			"from": "x", "to": "y", "ttl": "turn", "weight": 0.5,
		})
	}
	if h.fake.addEdgeCalls != 3 {
		t.Fatalf("AddEdge calls = %d, want 3", h.fake.addEdgeCalls)
	}
	if h.fake.lastEdgeWeight != 0.5 {
		t.Fatalf("last weight = %v", h.fake.lastEdgeWeight)
	}
}

func TestRememberRelation_RejectsEmptyEndpoints(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "remember_relation", map[string]any{
		"from": "", "to": "b", "ttl": "turn",
	})
	h.callExpectError(t, "remember_relation", map[string]any{
		"from": "a", "to": "", "ttl": "turn",
	})
}

// TestRememberRelationDescription_IsProactive guards the proactive framing
// for capturing associations, plus the additive-write contract (#528).
func TestRememberRelationDescription_IsProactive(t *testing.T) {
	if !strings.Contains(strings.ToUpper(rememberRelationDescription), "PROACTIVELY") {
		t.Errorf("rememberRelationDescription should tell the agent to act PROACTIVELY: %q", rememberRelationDescription)
	}
	if !strings.Contains(rememberRelationDescription, "ADDITIVE") {
		t.Errorf("rememberRelationDescription should keep the ADDITIVE-write contract: %q", rememberRelationDescription)
	}
}

// TestRememberRelation_TTLCappedToMaxTTL mirrors the fact-side clamp (#537):
// with LANTERN_MCP_MAX_TTL=24h, a durable relation is written with the
// clamped TTL and reports capped=true instead of letting the server reject
// the over-cap Expiration.
func TestRememberRelation_TTLCappedToMaxTTL(t *testing.T) {
	h := newTestHarnessWith(t, mustCappedResolver(t, "24h"))
	res := h.call(t, "remember_relation", map[string]any{
		"from": "user.identity.role", "to": "project.lantern", "ttl": "durable",
	})
	if res.IsError {
		t.Fatalf("IsError = true, text=%q", contentText(res))
	}
	if h.fake.lastEdgeTTL != 24*time.Hour {
		t.Fatalf("lastEdgeTTL = %v, want clamped 24h", h.fake.lastEdgeTTL)
	}
	var out rememberRelationOutput
	structuredAs(t, res, &out)
	if !out.Capped {
		t.Fatalf("output Capped = false, want true; out=%+v", out)
	}
	if !strings.Contains(contentText(res), "clamped") {
		t.Fatalf("result text should mention the clamp; got %q", contentText(res))
	}
}

// TestRememberRelation_ReturnsAccumulatedWeight is the core #547 guard: after N
// additive +1 writes the tool must report accumulated_weight = N (read back via
// GetEdge), while increment stays at the per-write amount.
func TestRememberRelation_ReturnsAccumulatedWeight(t *testing.T) {
	h := newTestHarness(t)
	// Model additive accumulation: each remember_relation(+1) triggers exactly
	// one GetEdge read-back, so the running total observed grows by 1 per call.
	var total float32
	h.fake.getEdgeFn = func(_ context.Context, tail, head string) (*client.Edge, error) {
		total++
		return &pb.Edge{Tail: tail, Head: head, Weight: total}, nil
	}
	var lastText string
	for i := 1; i <= 3; i++ {
		res := h.call(t, "remember_relation", map[string]any{
			"from": "a", "to": "b", "ttl": "day",
		})
		if res.IsError {
			t.Fatalf("write %d: IsError, text=%q", i, contentText(res))
		}
		var out rememberRelationOutput
		structuredAs(t, res, &out)
		if out.Increment != 1 {
			t.Fatalf("write %d: increment = %v, want 1", i, out.Increment)
		}
		if out.AccumulatedWeight != float32(i) {
			t.Fatalf("write %d: accumulated_weight = %v, want %d", i, out.AccumulatedWeight, i)
		}
		lastText = contentText(res)
	}
	// The read-back must target the exact endpoints just written.
	if h.fake.lastGetEdgeTail != "a" || h.fake.lastGetEdgeHead != "b" {
		t.Fatalf("GetEdge args = (%s, %s), want (a, b)", h.fake.lastGetEdgeTail, h.fake.lastGetEdgeHead)
	}
	// The accumulation read-back must surface in the human-readable text too.
	if !strings.Contains(lastText, "now 3.00 total") {
		t.Fatalf("text should report the accumulated total; got %q", lastText)
	}
}

// TestRememberRelation_ReadBackUsesEdgeExpiry verifies the reported expires_at
// reflects the stored edge's own expiration when the read-back succeeds.
func TestRememberRelation_ReadBackUsesEdgeExpiry(t *testing.T) {
	h := newTestHarness(t)
	exp, err := time.Parse(time.RFC3339, "2031-02-03T04:05:06Z")
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	h.fake.getEdgeFn = func(_ context.Context, tail, head string) (*client.Edge, error) {
		return &pb.Edge{Tail: tail, Head: head, Weight: 5, Expiration: timestamppb.New(exp)}, nil
	}
	res := h.call(t, "remember_relation", map[string]any{
		"from": "a", "to": "b", "ttl": "day",
	})
	if res.IsError {
		t.Fatalf("IsError = true, text=%q", contentText(res))
	}
	var out rememberRelationOutput
	structuredAs(t, res, &out)
	if out.AccumulatedWeight != 5 {
		t.Fatalf("accumulated_weight = %v, want 5", out.AccumulatedWeight)
	}
	if out.ExpiresAt != "2031-02-03T04:05:06Z" {
		t.Fatalf("expires_at = %q, want the edge's own expiry", out.ExpiresAt)
	}
}

// TestRememberRelation_ReadBackFailureDegrades guards that a failed read-back
// after a successful additive write does NOT fail the tool: the write already
// landed, so the tool reports the increment as a best-effort accumulated value
// and notes the read-back was unavailable.
func TestRememberRelation_ReadBackFailureDegrades(t *testing.T) {
	h := newTestHarness(t)
	h.fake.getEdgeFn = func(_ context.Context, _, _ string) (*client.Edge, error) {
		return nil, client.ErrNotFound
	}
	res := h.call(t, "remember_relation", map[string]any{
		"from": "a", "to": "b", "ttl": "day", "weight": 2,
	})
	if res.IsError {
		t.Fatalf("a failed read-back must not fail the write: text=%q", contentText(res))
	}
	var out rememberRelationOutput
	structuredAs(t, res, &out)
	if out.Increment != 2 {
		t.Fatalf("increment = %v, want 2", out.Increment)
	}
	if out.AccumulatedWeight != 2 {
		t.Fatalf("accumulated_weight fallback = %v, want 2 (the increment)", out.AccumulatedWeight)
	}
	if !strings.Contains(contentText(res), "Could not read back") {
		t.Fatalf("text should note the read-back was unavailable; got %q", contentText(res))
	}
}
