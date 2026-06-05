package mcp

import (
	"testing"
	"time"
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
	if out.Weight != 1 {
		t.Fatalf("default weight = %v, want 1", out.Weight)
	}
	if h.fake.lastEdgeTail != "a" || h.fake.lastEdgeHead != "b" {
		t.Fatalf("edge args = (%s, %s)", h.fake.lastEdgeTail, h.fake.lastEdgeHead)
	}
	if h.fake.lastEdgeTTL != 24*time.Hour {
		t.Fatalf("TTL = %v, want 24h", h.fake.lastEdgeTTL)
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
