package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestRememberFacts_StoresMultipleInOneCall(t *testing.T) {
	h := newTestHarness(t)
	res := h.call(t, "remember_facts", map[string]any{
		"items": []map[string]any{
			{"key": "user.identity.role", "value": "engineer", "ttl": "durable"},
			{"key": "project.lantern.stack", "value": "go", "ttl": "month"},
			{"key": "session.current-task", "value": "batch tools", "ttl": "task"},
		},
	})
	if res.IsError {
		t.Fatalf("IsError = true; content=%s", contentText(res))
	}
	var out rememberFactsOutput
	structuredAs(t, res, &out)
	if out.Stored != 3 || out.Failed != 0 || out.Total != 3 {
		t.Fatalf("counts = stored=%d failed=%d total=%d, want 3/0/3", out.Stored, out.Failed, out.Total)
	}
	if len(out.Results) != 3 {
		t.Fatalf("results len = %d, want 3", len(out.Results))
	}
	for i, r := range out.Results {
		if r.Status != "stored" {
			t.Errorf("results[%d].status = %q, want stored", i, r.Status)
		}
		if r.Bucket == "" || r.ExpiresAt == "" {
			t.Errorf("results[%d] missing bucket/expires_at: %+v", i, r)
		}
	}
	// One batch RPC must carry all three facts.
	if got := len(h.fake.lastPutVertices); got != 3 {
		t.Fatalf("PutVertices inputs = %d, want 3", got)
	}
	if h.fake.lastPutVertices[0].Key != "user.identity.role" {
		t.Errorf("inputs[0].Key = %q", h.fake.lastPutVertices[0].Key)
	}
	if !h.fake.lastPutVertices[0].Expiration.After(time.Now()) {
		t.Errorf("inputs[0].Expiration not in the future: %v", h.fake.lastPutVertices[0].Expiration)
	}
}

func TestRememberFacts_RejectsEmptyItems(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "remember_facts", map[string]any{
		"items": []map[string]any{},
	})
	if h.fake.lastPutVertices != nil {
		t.Fatalf("PutVertices must not be called for an empty batch")
	}
}

func TestRememberFacts_RejectsInvalidItemWithoutWriting(t *testing.T) {
	h := newTestHarness(t)
	res := h.callExpectError(t, "remember_facts", map[string]any{
		"items": []map[string]any{
			{"key": "ok", "value": "v", "ttl": "day"},
			{"key": "bad", "value": "v", "ttl": "eternity"},
		},
	})
	if !strings.Contains(contentText(res), "nothing written") {
		t.Errorf("error should say nothing written: %q", contentText(res))
	}
	// All-or-nothing validation: a single bad item must prevent any write.
	if h.fake.lastPutVertices != nil {
		t.Fatalf("PutVertices must not be called when an item is invalid")
	}
}

func TestRememberFacts_RejectsEmptyKey(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "remember_facts", map[string]any{
		"items": []map[string]any{
			{"key": "", "value": "v", "ttl": "day"},
		},
	})
	if h.fake.lastPutVertices != nil {
		t.Fatalf("PutVertices must not be called when a key is empty")
	}
}

func TestRememberFacts_PartialFailureReportedPerItem(t *testing.T) {
	h := newTestHarness(t)
	// The SDK reports a mid-batch failure as *BatchError.Written = entries
	// committed before the failing chunk.
	h.fake.putVerticesFn = func(_ context.Context, _ []client.VertexInput) error {
		return &client.BatchError{Written: 2, Err: errors.New("boom")}
	}
	res := h.call(t, "remember_facts", map[string]any{
		"items": []map[string]any{
			{"key": "a", "value": "1", "ttl": "day"},
			{"key": "b", "value": "2", "ttl": "day"},
			{"key": "c", "value": "3", "ttl": "day"},
		},
	})
	// A partial failure is a soft result so the per-item detail is delivered.
	if res.IsError {
		t.Fatalf("partial failure should not be a hard error; content=%s", contentText(res))
	}
	var out rememberFactsOutput
	structuredAs(t, res, &out)
	if out.Stored != 2 || out.Failed != 1 || out.Total != 3 {
		t.Fatalf("counts = stored=%d failed=%d total=%d, want 2/1/3", out.Stored, out.Failed, out.Total)
	}
	if out.Results[0].Status != "stored" || out.Results[1].Status != "stored" {
		t.Errorf("first two should be stored: %+v", out.Results)
	}
	if out.Results[2].Status != "failed" {
		t.Errorf("results[2].status = %q, want failed", out.Results[2].Status)
	}
	if !strings.Contains(out.Results[2].Error, "boom") {
		t.Errorf("results[2].error should carry the cause: %q", out.Results[2].Error)
	}
	if !strings.Contains(contentText(res), "Stored 2 of 3") {
		t.Errorf("text should summarize the partial outcome: %q", contentText(res))
	}
}

func TestRememberFacts_TotalFailureIsError(t *testing.T) {
	h := newTestHarness(t)
	h.fake.putVerticesFn = func(_ context.Context, _ []client.VertexInput) error {
		return &client.BatchError{Written: 0, Err: errors.New("down")}
	}
	res := h.callExpectError(t, "remember_facts", map[string]any{
		"items": []map[string]any{
			{"key": "a", "value": "1", "ttl": "day"},
			{"key": "b", "value": "2", "ttl": "day"},
		},
	})
	if !strings.Contains(contentText(res), "remember_facts") {
		t.Errorf("error should be labeled with the tool: %q", contentText(res))
	}
}

func TestRememberFacts_RateLimitMapped(t *testing.T) {
	h := newTestHarness(t)
	h.fake.putVerticesFn = func(_ context.Context, _ []client.VertexInput) error {
		return &client.BatchError{Written: 0, Err: client.ErrResourceExhausted}
	}
	res := h.callExpectError(t, "remember_facts", map[string]any{
		"items": []map[string]any{
			{"key": "a", "value": "1", "ttl": "day"},
		},
	})
	if !strings.Contains(contentText(res), "rate limited") {
		t.Errorf("resource-exhausted should map to the rate-limit hint: %q", contentText(res))
	}
}

func TestRememberFacts_TTLCappedPerItem(t *testing.T) {
	h := newTestHarnessWith(t, mustCappedResolver(t, "24h"))
	res := h.call(t, "remember_facts", map[string]any{
		"items": []map[string]any{
			{"key": "k", "value": "v", "ttl": "durable"},
		},
	})
	if res.IsError {
		t.Fatalf("IsError = true; content=%s", contentText(res))
	}
	var out rememberFactsOutput
	structuredAs(t, res, &out)
	if !out.Results[0].Capped {
		t.Errorf("durable item under a 24h cap should report capped=true: %+v", out.Results[0])
	}
}

// TestRememberFactsDescription_FramesBatch guards the batch framing the LLM
// relies on to choose this over many singular calls.
func TestRememberFactsDescription_FramesBatch(t *testing.T) {
	if !strings.Contains(rememberFactsDescription, "remember_fact") {
		t.Errorf("description should reference the singular counterpart: %q", rememberFactsDescription)
	}
	if !strings.Contains(rememberFactsDescription, "round-trips") {
		t.Errorf("description should motivate batching via round-trips: %q", rememberFactsDescription)
	}
	if !strings.Contains(rememberFactsDescription, "per-item") {
		t.Errorf("description should promise per-item partial-failure reporting: %q", rememberFactsDescription)
	}
}

// TestRememberFacts_LintsPerItemKey proves the non-blocking key lint (#551)
// runs per batch item: a bad key is still stored but its result row carries
// warnings, while a good key in the same call stays clean.
func TestRememberFacts_LintsPerItemKey(t *testing.T) {
	h := newTestHarness(t)
	res := h.call(t, "remember_facts", map[string]any{
		"items": []map[string]any{
			{"key": "user.preferences.tone", "value": "warm", "ttl": "day"}, // clean
			{"key": "topic.2026-06-10.note", "value": "v", "ttl": "day"},    // bad scope + mid-key date
		},
	})
	if res.IsError {
		t.Fatalf("a lint warning must NOT block the batch; content=%s", contentText(res))
	}
	var out rememberFactsOutput
	structuredAs(t, res, &out)
	if out.Stored != 2 {
		t.Fatalf("both items should still store; stored=%d", out.Stored)
	}
	if len(out.Results[0].Warnings) != 0 {
		t.Errorf("clean key should have no warnings; got %v", out.Results[0].Warnings)
	}
	if len(out.Results[1].Warnings) == 0 {
		t.Errorf("bad key should carry warnings; got none in %+v", out.Results[1])
	}
}
