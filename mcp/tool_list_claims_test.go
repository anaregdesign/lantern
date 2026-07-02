package mcp

import (
	"context"
	"testing"

	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestListClaims(t *testing.T) {
	t.Run("prefix scopes the scan under claims.", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.scanVerticesFn = func(_ context.Context, prefix string, _ ...client.ScanOption) ([]*client.Vertex, []byte, error) {
			if prefix != claimKeyPrefix+"repo.lantern." {
				t.Fatalf("scan prefix = %q", prefix)
			}
			return []*client.Vertex{
				claimVertex(t, "claims.repo.lantern.core", claimRecord{Holder: "a1", Note: "wip"}),
			}, nil, nil
		}
		res := h.call(t, "list_claims", map[string]any{"prefix": "repo.lantern."})
		var out listClaimsOutput
		structuredAs(t, res, &out)
		if out.Count != 1 || out.Claims[0].Resource != "repo.lantern.core" || out.Claims[0].Holder != "a1" {
			t.Fatalf("claims: %+v", out)
		}
		if out.Claims[0].ExpiresAt == "" {
			t.Fatal("expiry missing")
		}
	})

	t.Run("no live claims", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.scanVerticesFn = func(_ context.Context, _ string, _ ...client.ScanOption) ([]*client.Vertex, []byte, error) {
			return nil, nil, nil
		}
		res := h.call(t, "list_claims", map[string]any{})
		var out listClaimsOutput
		structuredAs(t, res, &out)
		if out.Count != 0 {
			t.Fatalf("want empty: %+v", out)
		}
	})
}
