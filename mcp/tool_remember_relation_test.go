package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
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
	// AccumulatedWeight now comes from AddEdge's effective_weight (#897); the
	// default fake echoes the increment, so a single +1 write reports 1.
	if out.AccumulatedWeight != 1 {
		t.Fatalf("accumulated weight = %v, want 1", out.AccumulatedWeight)
	}
	if h.fake.lastEdgeTail != "a" || h.fake.lastEdgeHead != "b" {
		t.Fatalf("edge args = (%s, %s)", h.fake.lastEdgeTail, h.fake.lastEdgeHead)
	}
	if h.fake.lastEdgeTTL != 24*time.Hour {
		t.Fatalf("TTL = %v, want 24h", h.fake.lastEdgeTTL)
	}
	// The obsolete GetEdge read-back (#897) must be gone — no TOCTOU window.
	if h.fake.lastGetEdgeTail != "" || h.fake.lastGetEdgeHead != "" {
		t.Fatalf("GetEdge was called (%s, %s) — the read-back is obsolete", h.fake.lastGetEdgeTail, h.fake.lastGetEdgeHead)
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

// TestRememberRelation_ReturnsAccumulatedWeight is the core #547 guard, now
// sourced from AddEdgeResponse.effective_weight (#897): after N additive +1
// writes the tool must report accumulated_weight = N from AddEdge's return,
// with NO GetEdge read-back (which reopened the TOCTOU window #897 closed).
func TestRememberRelation_ReturnsAccumulatedWeight(t *testing.T) {
	h := newTestHarness(t)
	// Server-side accumulation arrives in AddEdgeResponse.effective_weight.
	var total float32
	h.fake.addEdgeEffectiveFn = func(_, _ string, weight float32) float32 {
		total += weight
		return total
	}
	// If the tool still read back via GetEdge it would see this poisoned value.
	h.fake.getEdgeFn = func(_ context.Context, tail, head string) (*client.Edge, error) {
		return &pb.Edge{Tail: tail, Head: head, Weight: 999}, nil
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
			t.Fatalf("write %d: accumulated_weight = %v, want %d — must come from AddEdge's return, not a GetEdge read-back (999)", i, out.AccumulatedWeight, i)
		}
		lastText = contentText(res)
	}
	if h.fake.lastGetEdgeTail != "" || h.fake.lastGetEdgeHead != "" {
		t.Fatalf("GetEdge was called (args %s,%s) — #897 made the read-back obsolete; the TOCTOU window is back", h.fake.lastGetEdgeTail, h.fake.lastGetEdgeHead)
	}
	if !strings.Contains(lastText, "now 3.00 total") {
		t.Fatalf("text should report the accumulated total; got %q", lastText)
	}
}

// TestRememberRelation_ReportsOwnAtomicTotal is the concurrent-attribution
// guard: AddEdge returns this call's own post-write live sum (3), while a
// concurrent writer has since pushed the stored edge to 5. The tool must
// report 3 — its own atomic total — never the poisoned read-back value.
func TestRememberRelation_ReportsOwnAtomicTotal(t *testing.T) {
	h := newTestHarness(t)
	h.fake.addEdgeEffectiveFn = func(_, _ string, _ float32) float32 { return 3 }
	h.fake.getEdgeFn = func(_ context.Context, tail, head string) (*client.Edge, error) {
		return &pb.Edge{Tail: tail, Head: head, Weight: 5}, nil
	}
	res := h.call(t, "remember_relation", map[string]any{
		"from": "a", "to": "b", "ttl": "day",
	})
	if res.IsError {
		t.Fatalf("IsError = true, text=%q", contentText(res))
	}
	var out rememberRelationOutput
	structuredAs(t, res, &out)
	if out.AccumulatedWeight != 3 {
		t.Fatalf("accumulated_weight = %v, want 3 (own atomic total, not the concurrent writer's 5)", out.AccumulatedWeight)
	}
}

// TestRememberRelation_ExpiresAtIsOwnHorizon pins the contract change from
// #897: without the GetEdge read-back, expires_at is this write's own
// now+TTL horizon (a "day" bucket → ~24h out), not the edge's latest
// cross-contribution expiration. Tolerance-based; no exact clock match.
func TestRememberRelation_ExpiresAtIsOwnHorizon(t *testing.T) {
	h := newTestHarness(t)
	before := time.Now()
	res := h.call(t, "remember_relation", map[string]any{
		"from": "a", "to": "b", "ttl": "day",
	})
	if res.IsError {
		t.Fatalf("IsError = true, text=%q", contentText(res))
	}
	var out rememberRelationOutput
	structuredAs(t, res, &out)
	got, err := time.Parse(time.RFC3339, out.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at %q is not RFC3339: %v", out.ExpiresAt, err)
	}
	want := before.Add(24 * time.Hour)
	if diff := got.Sub(want); diff < -time.Minute || diff > time.Minute {
		t.Fatalf("expires_at = %v, want ≈ now+24h (%v); off by %v", got, want, diff)
	}
}
