package mcp

import (
	"strings"
	"testing"
	"time"
)

func TestRememberFact_PrimitiveValue(t *testing.T) {
	h := newTestHarness(t)
	res := h.call(t, "remember_fact", map[string]any{
		"key":   "user.tone",
		"value": "warm",
		"ttl":   "conversation",
	})
	if res.IsError {
		t.Fatalf("IsError = true, text=%q", contentText(res))
	}
	if h.fake.putVertexCalls != 1 {
		t.Fatalf("PutVertex calls = %d, want 1", h.fake.putVertexCalls)
	}
	if h.fake.lastPutKey != "user.tone" {
		t.Fatalf("lastPutKey = %q", h.fake.lastPutKey)
	}
	if h.fake.lastPutValue != "warm" {
		t.Fatalf("lastPutValue = %v (%T)", h.fake.lastPutValue, h.fake.lastPutValue)
	}
	if h.fake.lastPutTTL != time.Hour {
		t.Fatalf("lastPutTTL = %v, want conversation=1h", h.fake.lastPutTTL)
	}
	var out rememberFactOutput
	structuredAs(t, res, &out)
	if out.Key != "user.tone" || out.Bucket != "conversation" {
		t.Fatalf("output mismatch: %+v", out)
	}
}

func TestRememberFact_CompositeValueJSONEncoded(t *testing.T) {
	h := newTestHarness(t)
	body := map[string]any{"name": "lantern", "score": float64(0.9)}
	h.call(t, "remember_fact", map[string]any{
		"key":   "config.app",
		"value": body,
		"ttl":   "day",
	})
	s, ok := h.fake.lastPutValue.(string)
	if !ok {
		t.Fatalf("composite value not encoded to string; got %T", h.fake.lastPutValue)
	}
	if len(s) == 0 || s[0] != '{' {
		t.Fatalf("expected JSON object payload, got %q", s)
	}
}

func TestRememberFact_RejectsEmptyKey(t *testing.T) {
	h := newTestHarness(t)
	res := h.callExpectError(t, "remember_fact", map[string]any{
		"key":   "",
		"value": "x",
		"ttl":   "turn",
	})
	if h.fake.putVertexCalls != 0 {
		t.Fatalf("PutVertex should not have been called; calls=%d", h.fake.putVertexCalls)
	}
	_ = res
}

func TestRememberFact_RejectsUnknownBucket(t *testing.T) {
	h := newTestHarness(t)
	res := h.callExpectError(t, "remember_fact", map[string]any{
		"key":   "k",
		"value": "v",
		"ttl":   "forever", // not a real bucket
	})
	if h.fake.putVertexCalls != 0 {
		t.Fatalf("PutVertex should not have been called; calls=%d", h.fake.putVertexCalls)
	}
	_ = res
}

// TestRememberFactDescription_IsProactive guards the proactive framing
// that nudges the agent to capture facts without being asked (#528).
func TestRememberFactDescription_IsProactive(t *testing.T) {
	if !strings.Contains(strings.ToUpper(rememberFactDescription), "PROACTIVELY") {
		t.Errorf("rememberFactDescription should tell the agent to act PROACTIVELY: %q", rememberFactDescription)
	}
	if !strings.Contains(rememberFactDescription, "does NOT refresh") {
		t.Errorf("rememberFactDescription should keep the recall-does-not-refresh invariant: %q", rememberFactDescription)
	}
}

// TestRememberFact_TTLCappedToMaxTTL proves that with LANTERN_MCP_MAX_TTL
// set, an over-cap bucket (durable=180d) is clamped to the cap before the
// PutVertex write and the result reports capped=true (#537). This is the
// honesty contract: a bucket the server would reject is silently shortened
// to a value the server accepts, with the shortening surfaced.
func TestRememberFact_TTLCappedToMaxTTL(t *testing.T) {
	h := newTestHarnessWith(t, mustCappedResolver(t, "24h"))
	res := h.call(t, "remember_fact", map[string]any{
		"key":   "user.identity.role",
		"value": "architect",
		"ttl":   "durable", // nominal 180d, far beyond the 24h cap
	})
	if res.IsError {
		t.Fatalf("IsError = true, text=%q", contentText(res))
	}
	if h.fake.lastPutTTL != 24*time.Hour {
		t.Fatalf("lastPutTTL = %v, want clamped 24h", h.fake.lastPutTTL)
	}
	var out rememberFactOutput
	structuredAs(t, res, &out)
	if !out.Capped {
		t.Fatalf("output Capped = false, want true; out=%+v", out)
	}
	if out.Bucket != "durable" {
		t.Fatalf("output Bucket = %q, want durable (label preserved)", out.Bucket)
	}
	if !strings.Contains(contentText(res), "clamped") {
		t.Fatalf("result text should mention the clamp; got %q", contentText(res))
	}
}

// TestRememberFact_UnderCapNotClamped confirms a bucket at/below the cap is
// written verbatim with capped=false even when a cap is configured.
func TestRememberFact_UnderCapNotClamped(t *testing.T) {
	h := newTestHarnessWith(t, mustCappedResolver(t, "24h"))
	res := h.call(t, "remember_fact", map[string]any{
		"key":   "session.cursor",
		"value": "x",
		"ttl":   "conversation", // 1h, well under the 24h cap
	})
	if res.IsError {
		t.Fatalf("IsError = true, text=%q", contentText(res))
	}
	if h.fake.lastPutTTL != time.Hour {
		t.Fatalf("lastPutTTL = %v, want 1h (unclamped)", h.fake.lastPutTTL)
	}
	var out rememberFactOutput
	structuredAs(t, res, &out)
	if out.Capped {
		t.Fatalf("output Capped = true, want false for under-cap bucket; out=%+v", out)
	}
}
