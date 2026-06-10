package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestRememberRelations_AddsMultipleInOneCall(t *testing.T) {
	h := newTestHarness(t)
	res := h.call(t, "remember_relations", map[string]any{
		"edges": []map[string]any{
			{"from": "a", "to": "b", "ttl": "day"},
			{"from": "b", "to": "c", "ttl": "week", "weight": 2.5},
		},
	})
	if res.IsError {
		t.Fatalf("IsError = true; content=%s", contentText(res))
	}
	var out rememberRelationsOutput
	structuredAs(t, res, &out)
	if out.Stored != 2 || out.Failed != 0 || out.Total != 2 {
		t.Fatalf("counts = stored=%d failed=%d total=%d, want 2/0/2", out.Stored, out.Failed, out.Total)
	}
	if out.Results[0].Increment != 1 {
		t.Errorf("results[0].increment = %v, want default 1", out.Results[0].Increment)
	}
	if out.Results[1].Increment != 2.5 {
		t.Errorf("results[1].increment = %v, want 2.5", out.Results[1].Increment)
	}
	// One batch RPC must carry both edges with the right endpoints/weights.
	if got := len(h.fake.lastAddEdges); got != 2 {
		t.Fatalf("AddEdges inputs = %d, want 2", got)
	}
	if h.fake.lastAddEdges[0].Tail != "a" || h.fake.lastAddEdges[0].Head != "b" {
		t.Errorf("inputs[0] endpoints = (%s,%s)", h.fake.lastAddEdges[0].Tail, h.fake.lastAddEdges[0].Head)
	}
	if h.fake.lastAddEdges[0].Weight != 1 {
		t.Errorf("inputs[0].Weight = %v, want default 1", h.fake.lastAddEdges[0].Weight)
	}
	if !h.fake.lastAddEdges[1].Expiration.After(time.Now()) {
		t.Errorf("inputs[1].Expiration not in the future: %v", h.fake.lastAddEdges[1].Expiration)
	}
}

// TestRememberRelations_NoPerEdgeReadBack guards the #547 decision: the batch
// path must NOT read each edge back (one GetEdge per edge would defeat
// batching), so accumulated_weight is intentionally absent.
func TestRememberRelations_NoPerEdgeReadBack(t *testing.T) {
	h := newTestHarness(t)
	h.call(t, "remember_relations", map[string]any{
		"edges": []map[string]any{
			{"from": "a", "to": "b", "ttl": "day"},
		},
	})
	if h.fake.lastGetEdgeTail != "" || h.fake.lastGetEdgeHead != "" {
		t.Fatalf("batch path must not call GetEdge; saw (%s,%s)", h.fake.lastGetEdgeTail, h.fake.lastGetEdgeHead)
	}
}

func TestRememberRelations_RejectsEmptyEdges(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "remember_relations", map[string]any{
		"edges": []map[string]any{},
	})
	if h.fake.lastAddEdges != nil {
		t.Fatalf("AddEdges must not be called for an empty batch")
	}
}

func TestRememberRelations_RejectsInvalidEdgeWithoutWriting(t *testing.T) {
	h := newTestHarness(t)
	res := h.callExpectError(t, "remember_relations", map[string]any{
		"edges": []map[string]any{
			{"from": "a", "to": "b", "ttl": "day"},
			{"from": "", "to": "c", "ttl": "day"},
		},
	})
	if !strings.Contains(contentText(res), "nothing written") {
		t.Errorf("error should say nothing written: %q", contentText(res))
	}
	// All-or-nothing matters most for additive writes: a rejected batch must
	// leave zero edges so a corrected resubmit cannot double-count.
	if h.fake.lastAddEdges != nil {
		t.Fatalf("AddEdges must not be called when an edge is invalid")
	}
}

func TestRememberRelations_RejectsBadTTL(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "remember_relations", map[string]any{
		"edges": []map[string]any{
			{"from": "a", "to": "b", "ttl": "eternity"},
		},
	})
	if h.fake.lastAddEdges != nil {
		t.Fatalf("AddEdges must not be called when a ttl is invalid")
	}
}

func TestRememberRelations_PartialFailureWarnsAboutDoubleCount(t *testing.T) {
	h := newTestHarness(t)
	h.fake.addEdgesFn = func(_ context.Context, _ []client.EdgeInput) error {
		return &client.BatchError{Written: 2, Err: errors.New("boom")}
	}
	res := h.call(t, "remember_relations", map[string]any{
		"edges": []map[string]any{
			{"from": "a", "to": "b", "ttl": "day"},
			{"from": "b", "to": "c", "ttl": "day"},
			{"from": "c", "to": "d", "ttl": "day"},
		},
	})
	if res.IsError {
		t.Fatalf("partial failure should not be a hard error; content=%s", contentText(res))
	}
	var out rememberRelationsOutput
	structuredAs(t, res, &out)
	if out.Stored != 2 || out.Failed != 1 || out.Total != 3 {
		t.Fatalf("counts = stored=%d failed=%d total=%d, want 2/1/3", out.Stored, out.Failed, out.Total)
	}
	if out.Results[2].Status != "failed" || !strings.Contains(out.Results[2].Error, "boom") {
		t.Errorf("results[2] should be failed with the cause: %+v", out.Results[2])
	}
	text := contentText(res)
	if !strings.Contains(text, "double-count") {
		t.Errorf("partial-failure text must warn about additive double-count: %q", text)
	}
}

func TestRememberRelations_TotalFailureIsError(t *testing.T) {
	h := newTestHarness(t)
	h.fake.addEdgesFn = func(_ context.Context, _ []client.EdgeInput) error {
		return &client.BatchError{Written: 0, Err: client.ErrResourceExhausted}
	}
	res := h.callExpectError(t, "remember_relations", map[string]any{
		"edges": []map[string]any{
			{"from": "a", "to": "b", "ttl": "day"},
		},
	})
	if !strings.Contains(contentText(res), "rate limited") {
		t.Errorf("resource-exhausted should map to the rate-limit hint: %q", contentText(res))
	}
}

func TestRememberRelations_TTLCappedPerEdge(t *testing.T) {
	h := newTestHarnessWith(t, mustCappedResolver(t, "24h"))
	res := h.call(t, "remember_relations", map[string]any{
		"edges": []map[string]any{
			{"from": "a", "to": "b", "ttl": "durable"},
		},
	})
	if res.IsError {
		t.Fatalf("IsError = true; content=%s", contentText(res))
	}
	var out rememberRelationsOutput
	structuredAs(t, res, &out)
	if !out.Results[0].Capped {
		t.Errorf("durable edge under a 24h cap should report capped=true: %+v", out.Results[0])
	}
}

// TestRememberRelationsDescription_FramesBatch guards the additive-write and
// no-read-back contracts the LLM relies on.
func TestRememberRelationsDescription_FramesBatch(t *testing.T) {
	if !strings.Contains(rememberRelationsDescription, "ADDITIVE") {
		t.Errorf("description must keep the additive-write contract: %q", rememberRelationsDescription)
	}
	if !strings.Contains(rememberRelationsDescription, "double-count") {
		t.Errorf("description must warn about resending committed edges: %q", rememberRelationsDescription)
	}
	if !strings.Contains(rememberRelationsDescription, "accumulated_weight") {
		t.Errorf("description must point to the singular tools for accumulated_weight: %q", rememberRelationsDescription)
	}
}
