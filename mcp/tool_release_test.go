package mcp

import (
	"context"
	"testing"

	"github.com/anaregdesign/lantern/mcp/internal/identity"
	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestRelease(t *testing.T) {
	self := identity.Resolve()

	t.Run("holder releases own lease", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.getVertexFn = func(_ context.Context, key string) (*client.Vertex, error) {
			return claimVertex(t, key, claimRecord{Holder: self}), nil
		}
		h.fake.deleteVertexFn = func(_ context.Context, _ string) (bool, error) { return true, nil }
		res := h.call(t, "release", map[string]any{"resource": "repo.lantern.core"})
		var out releaseOutput
		structuredAs(t, res, &out)
		if !out.Released {
			t.Fatalf("release: %+v", out)
		}
		if h.fake.lastDeleteKey != claimKey("repo.lantern.core") {
			t.Fatalf("deleted %q", h.fake.lastDeleteKey)
		}
	})

	t.Run("non-holder release refused, lease intact", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.getVertexFn = func(_ context.Context, key string) (*client.Vertex, error) {
			return claimVertex(t, key, claimRecord{Holder: "other-agent"}), nil
		}
		res := h.call(t, "release", map[string]any{"resource": "repo.lantern.core"})
		var out releaseOutput
		structuredAs(t, res, &out)
		if out.Released || out.Holder != "other-agent" {
			t.Fatalf("non-holder release: %+v", out)
		}
		if h.fake.lastDeleteKey != "" {
			t.Fatal("foreign lease must not be deleted")
		}
	})

	t.Run("expired or never-claimed is idempotent success", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.getVertexFn = func(_ context.Context, _ string) (*client.Vertex, error) {
			return nil, client.ErrNotFound
		}
		res := h.call(t, "release", map[string]any{"resource": "repo.lantern.core"})
		var out releaseOutput
		structuredAs(t, res, &out)
		if !out.Released {
			t.Fatalf("idempotent release: %+v", out)
		}
	})
}
