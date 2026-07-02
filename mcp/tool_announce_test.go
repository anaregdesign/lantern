package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/mcp/internal/identity"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// presenceVertex builds a stored agents.<id> vertex carrying rec.
func presenceVertex(t *testing.T, key string, rec presenceRecord) *client.Vertex {
	t.Helper()
	payload, err := encodeRecord(rec)
	if err != nil {
		t.Fatalf("encodeRecord: %v", err)
	}
	return &pb.Vertex{
		Key:        key,
		Value:      &pb.Vertex_String_{String_: payload},
		Expiration: timestamppb.New(time.Now().Add(time.Minute)),
	}
}

func TestAnnounce(t *testing.T) {
	self := agentKey(identity.Resolve())

	t.Run("first announce writes presence under agents.<id>", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.getVertexFn = func(_ context.Context, _ string) (*client.Vertex, error) {
			return nil, client.ErrNotFound
		}
		res := h.call(t, "announce", map[string]any{"task": "reviewing PR 866"})
		var out announceOutput
		structuredAs(t, res, &out)
		if out.AgentID == "" || out.Renewed {
			t.Fatalf("first announce: %+v (want fresh, non-renewed, id set)", out)
		}
		if h.fake.lastPutKey != self {
			t.Fatalf("presence written to %q, want %q", h.fake.lastPutKey, self)
		}
		var rec presenceRecord
		payload, _ := h.fake.lastPutValue.(string)
		if err := decodePayload(payload, &rec); err != nil || rec.Task != "reviewing PR 866" {
			t.Fatalf("stored payload %q did not round-trip: %+v err=%v", payload, rec, err)
		}
	})

	t.Run("re-announce is a heartbeat that preserves since for the same task", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.getVertexFn = func(_ context.Context, key string) (*client.Vertex, error) {
			return presenceVertex(t, key, presenceRecord{Task: "reviewing PR 866", Since: "2026-07-02T00:00:00Z"}), nil
		}
		res := h.call(t, "announce", map[string]any{"task": "reviewing PR 866"})
		var out announceOutput
		structuredAs(t, res, &out)
		if !out.Renewed {
			t.Fatal("heartbeat refresh not flagged as renewed")
		}
		var rec presenceRecord
		payload, _ := h.fake.lastPutValue.(string)
		if err := decodePayload(payload, &rec); err != nil || rec.Since != "2026-07-02T00:00:00Z" {
			t.Fatalf("since not preserved across heartbeat: %+v err=%v", rec, err)
		}
	})

	t.Run("task change resets since", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.getVertexFn = func(_ context.Context, key string) (*client.Vertex, error) {
			return presenceVertex(t, key, presenceRecord{Task: "old task", Since: "2026-07-02T00:00:00Z"}), nil
		}
		h.call(t, "announce", map[string]any{"task": "new task"})
		var rec presenceRecord
		payload, _ := h.fake.lastPutValue.(string)
		if err := decodePayload(payload, &rec); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if rec.Since == "2026-07-02T00:00:00Z" {
			t.Fatal("since must reset when the task line changes")
		}
	})

	t.Run("empty task rejected", func(t *testing.T) {
		h := newContextHarness(t)
		h.callExpectError(t, "announce", map[string]any{"task": ""})
	})

	t.Run("output surfaces the agent id", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.getVertexFn = func(_ context.Context, _ string) (*client.Vertex, error) {
			return nil, client.ErrNotFound
		}
		res := h.call(t, "announce", map[string]any{"task": "t"})
		var out announceOutput
		structuredAs(t, res, &out)
		if !strings.HasSuffix(self, out.AgentID) {
			t.Fatalf("output agent id %q does not match resolved identity key %q", out.AgentID, self)
		}
	})
}
