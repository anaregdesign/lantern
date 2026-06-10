package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
)

// TestTouch_ExtendsTTLValueUnchanged is the core acceptance case (#543): an
// existing fact is re-stored with a fresh expiry and the SAME value, and the
// result reports found=true with the new bucket/expiry.
func TestTouch_ExtendsTTLValueUnchanged(t *testing.T) {
	h := newTestHarness(t)
	h.fake.getVertexFn = func(_ context.Context, _ string) (*client.Vertex, error) {
		return &pb.Vertex{Key: "session.mode", Value: &pb.Vertex_String_{String_: "record-while-chatting"}}, nil
	}
	res := h.call(t, "touch", map[string]any{"key": "session.mode", "ttl": "day"})
	if res.IsError {
		t.Fatalf("IsError = true, text=%q", contentText(res))
	}
	if h.fake.lastGetKey != "session.mode" {
		t.Fatalf("GetVertex key = %q, want session.mode", h.fake.lastGetKey)
	}
	if h.fake.putVertexCalls != 1 {
		t.Fatalf("PutVertex calls = %d, want 1", h.fake.putVertexCalls)
	}
	if h.fake.lastPutKey != "session.mode" {
		t.Fatalf("lastPutKey = %q, want session.mode", h.fake.lastPutKey)
	}
	if h.fake.lastPutValue != "record-while-chatting" {
		t.Fatalf("lastPutValue = %v (%T), want value preserved verbatim", h.fake.lastPutValue, h.fake.lastPutValue)
	}
	if h.fake.lastPutTTL != 24*time.Hour {
		t.Fatalf("lastPutTTL = %v, want day=24h", h.fake.lastPutTTL)
	}
	var out touchOutput
	structuredAs(t, res, &out)
	if !out.Found {
		t.Fatalf("Found = false, want true; out=%+v", out)
	}
	if out.Key != "session.mode" || out.Bucket != "day" {
		t.Fatalf("output mismatch: %+v", out)
	}
	if out.ExpiresAt == "" {
		t.Fatalf("ExpiresAt should be populated; out=%+v", out)
	}
	if !strings.Contains(contentText(res), "value unchanged") {
		t.Fatalf("result text should state the value is unchanged; got %q", contentText(res))
	}
}

// TestTouch_PreservesNonStringKind proves value.Native keeps the stored kind
// exactly: touching an Int32 fact re-puts an int32 (not a widened int64 or a
// stringified form), so the server's value is byte-for-byte unchanged.
func TestTouch_PreservesNonStringKind(t *testing.T) {
	h := newTestHarness(t)
	h.fake.getVertexFn = func(_ context.Context, _ string) (*client.Vertex, error) {
		return &pb.Vertex{Key: "k", Value: &pb.Vertex_Int32{Int32: -7}}, nil
	}
	res := h.call(t, "touch", map[string]any{"key": "k", "ttl": "week"})
	if res.IsError {
		t.Fatalf("IsError = true, text=%q", contentText(res))
	}
	got, ok := h.fake.lastPutValue.(int32)
	if !ok || got != -7 {
		t.Fatalf("lastPutValue = %v (%T), want int32(-7) preserved", h.fake.lastPutValue, h.fake.lastPutValue)
	}
}

// TestTouch_PreservesNilTombstone confirms touching a present-nil tombstone
// keeps it alive: GetVertex returns the Vertex_Nil variant (found, not
// missing), and touch re-puts a nil value rather than treating it as absent.
func TestTouch_PreservesNilTombstone(t *testing.T) {
	h := newTestHarness(t)
	h.fake.getVertexFn = func(_ context.Context, _ string) (*client.Vertex, error) {
		return &pb.Vertex{Key: "k", Value: &pb.Vertex_Nil{Nil: true}}, nil
	}
	res := h.call(t, "touch", map[string]any{"key": "k", "ttl": "turn"})
	if res.IsError {
		t.Fatalf("IsError = true, text=%q", contentText(res))
	}
	if h.fake.putVertexCalls != 1 {
		t.Fatalf("PutVertex calls = %d, want 1 (tombstone kept alive)", h.fake.putVertexCalls)
	}
	if h.fake.lastPutValue != nil {
		t.Fatalf("lastPutValue = %v (%T), want nil", h.fake.lastPutValue, h.fake.lastPutValue)
	}
	var out touchOutput
	structuredAs(t, res, &out)
	if !out.Found {
		t.Fatalf("Found = false, want true for a present tombstone; out=%+v", out)
	}
}

// TestTouch_MissingKeyIsStructured proves a missing key is a structured
// {found:false} result, NOT a tool error, and that no write is attempted.
func TestTouch_MissingKeyIsStructured(t *testing.T) {
	h := newTestHarness(t)
	h.fake.getVertexFn = func(_ context.Context, _ string) (*client.Vertex, error) {
		return nil, client.ErrNotFound
	}
	res := h.call(t, "touch", map[string]any{"key": "nope", "ttl": "day"})
	if res.IsError {
		t.Fatalf("missing key should be a structured result, not a tool error: %q", contentText(res))
	}
	if h.fake.putVertexCalls != 0 {
		t.Fatalf("PutVertex should NOT be called for a missing key; calls=%d", h.fake.putVertexCalls)
	}
	var out touchOutput
	structuredAs(t, res, &out)
	if out.Found {
		t.Fatalf("Found = true, want false; out=%+v", out)
	}
	if out.Key != "nope" {
		t.Fatalf("Key = %q, want nope", out.Key)
	}
}

func TestTouch_RejectsEmptyKey(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "touch", map[string]any{"key": "", "ttl": "day"})
	if h.fake.putVertexCalls != 0 {
		t.Fatalf("PutVertex should not be called; calls=%d", h.fake.putVertexCalls)
	}
}

func TestTouch_RejectsUnknownBucket(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "touch", map[string]any{"key": "k", "ttl": "forever"})
	if h.fake.putVertexCalls != 0 {
		t.Fatalf("PutVertex should not be called; calls=%d", h.fake.putVertexCalls)
	}
}

// TestTouch_TTLCappedToMaxTTL proves the keep-alive horizon honours the same
// LANTERN_MCP_MAX_TTL clamp as remember_fact (#537): an over-cap bucket is
// shortened before the write and the result reports capped=true.
func TestTouch_TTLCappedToMaxTTL(t *testing.T) {
	h := newTestHarnessWith(t, mustCappedResolver(t, "24h"))
	h.fake.getVertexFn = func(_ context.Context, _ string) (*client.Vertex, error) {
		return &pb.Vertex{Key: "user.identity.role", Value: &pb.Vertex_String_{String_: "architect"}}, nil
	}
	res := h.call(t, "touch", map[string]any{"key": "user.identity.role", "ttl": "durable"})
	if res.IsError {
		t.Fatalf("IsError = true, text=%q", contentText(res))
	}
	if h.fake.lastPutTTL != 24*time.Hour {
		t.Fatalf("lastPutTTL = %v, want clamped 24h", h.fake.lastPutTTL)
	}
	var out touchOutput
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

// TestTouchDescription_FramesKeepAlive guards the description's job: it must
// teach that touch extends TTL without rewriting the value and that recall
// does not refresh TTL (the motivation), and that it never creates a fact.
func TestTouchDescription_FramesKeepAlive(t *testing.T) {
	if !strings.Contains(touchDescription, "does NOT refresh") {
		t.Errorf("touchDescription should cite the recall-does-not-refresh motivation: %q", touchDescription)
	}
	if !strings.Contains(touchDescription, "without rewriting") && !strings.Contains(touchDescription, "keep-alive") {
		t.Errorf("touchDescription should frame touch as the value-preserving keep-alive: %q", touchDescription)
	}
	if !strings.Contains(touchDescription, "found=false") {
		t.Errorf("touchDescription should document the missing-key {found=false} contract: %q", touchDescription)
	}
}
