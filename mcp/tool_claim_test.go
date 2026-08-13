package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/mcp/internal/identity"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// claimVertex builds a stored claims.<resource> vertex carrying rec.
func claimVertex(t *testing.T, key string, rec claimRecord) *client.Vertex {
	t.Helper()
	payload, err := encodeRecord(rec)
	if err != nil {
		t.Fatalf("encodeRecord: %v", err)
	}
	return &pb.Vertex{
		Key:        key,
		Value:      &pb.Vertex_String_{String_: payload},
		Expiration: timestamppb.New(time.Now().Add(10 * time.Minute)),
	}
}

func TestClaim(t *testing.T) {
	self := identity.Resolve()

	t.Run("grant on unclaimed resource", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.getVertexFn = func(_ context.Context, _ string) (*client.Vertex, error) {
			return nil, client.ErrNotFound
		}
		res := h.call(t, "claim", map[string]any{"resource": "repo.lantern.core", "note": "refactor"})
		var out claimOutput
		structuredAs(t, res, &out)
		if !out.Granted || out.Renewed || out.Stolen || out.Holder != self {
			t.Fatalf("grant: %+v", out)
		}
		if h.fake.lastPutKey != claimKey("repo.lantern.core") {
			t.Fatalf("claim written to %q", h.fake.lastPutKey)
		}
	})

	t.Run("conflict with live foreign holder is a structured result", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.getVertexFn = func(_ context.Context, key string) (*client.Vertex, error) {
			return claimVertex(t, key, claimRecord{Holder: "other-agent", Note: "migrating"}), nil
		}
		res := h.call(t, "claim", map[string]any{"resource": "repo.lantern.core"})
		var out claimOutput
		structuredAs(t, res, &out)
		if out.Granted {
			t.Fatal("conflict must not grant")
		}
		if out.Holder != "other-agent" || out.HolderNote != "migrating" || out.ExpiresAt == "" {
			t.Fatalf("conflict must report holder/note/expiry: %+v", out)
		}
		if h.fake.putVertexCalls != 0 {
			t.Fatal("conflict must not write")
		}
	})

	t.Run("force steals a live foreign lease", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.getVertexFn = func(_ context.Context, key string) (*client.Vertex, error) {
			return claimVertex(t, key, claimRecord{Holder: "other-agent"}), nil
		}
		res := h.call(t, "claim", map[string]any{"resource": "repo.lantern.core", "force": true})
		var out claimOutput
		structuredAs(t, res, &out)
		if !out.Granted || !out.Stolen || out.Holder != self {
			t.Fatalf("steal: %+v", out)
		}
	})

	t.Run("re-claim by the holder renews", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.getVertexFn = func(_ context.Context, key string) (*client.Vertex, error) {
			return claimVertex(t, key, claimRecord{Holder: self}), nil
		}
		res := h.call(t, "claim", map[string]any{"resource": "repo.lantern.core"})
		var out claimOutput
		structuredAs(t, res, &out)
		if !out.Granted || !out.Renewed || out.Stolen {
			t.Fatalf("renew: %+v", out)
		}
	})

	t.Run("empty resource rejected", func(t *testing.T) {
		h := newContextHarness(t)
		h.callExpectError(t, "claim", map[string]any{"resource": ""})
	})

	t.Run("expired Put never grants or links a claim", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.getVertexFn = func(_ context.Context, _ string) (*client.Vertex, error) {
			return nil, client.ErrNotFound
		}
		h.fake.putVertexOutcome = client.PutOutcomeExpired
		res := h.callExpectError(t, "claim", map[string]any{"resource": "repo.lantern.clock"})
		if h.fake.addEdgeCalls != 0 {
			t.Fatal("claim activity edge was added after EXPIRED Put")
		}
		if got := contentText(res); got == "" {
			t.Fatal("expired claim returned an empty error")
		}
	})
}
