package mcp

import (
	"testing"

	"github.com/anaregdesign/lantern/mcp/internal/identity"
)

func TestTrack(t *testing.T) {
	self := agentKey(identity.Resolve())

	t.Run("adds one additive edge per resource from the agent vertex", func(t *testing.T) {
		h := newContextHarness(t)
		res := h.call(t, "track", map[string]any{
			"resources": []string{"repo.lantern.core.graphcache", "ticket.LANT-42"},
		})
		var out trackOutput
		structuredAs(t, res, &out)
		if out.Tracked != 2 {
			t.Fatalf("tracked = %d, want 2", out.Tracked)
		}
		if len(h.fake.lastAddEdges) != 2 {
			t.Fatalf("AddEdges received %d edges", len(h.fake.lastAddEdges))
		}
		for i, e := range h.fake.lastAddEdges {
			if e.Tail != self {
				t.Fatalf("edge %d tail = %q, want %q", i, e.Tail, self)
			}
			if e.Weight != 1 {
				t.Fatalf("edge %d weight = %v, want 1 (unit pulses; repetition is the signal)", i, e.Weight)
			}
			if e.Expiration.IsZero() {
				t.Fatalf("edge %d missing expiration", i)
			}
		}
	})

	t.Run("repetition is observed as repeated AddEdges calls, not deduped", func(t *testing.T) {
		h := newContextHarness(t)
		h.call(t, "track", map[string]any{"resources": []string{"repo.x"}})
		first := len(h.fake.lastAddEdges)
		h.call(t, "track", map[string]any{"resources": []string{"repo.x"}})
		if first != 1 || len(h.fake.lastAddEdges) != 1 {
			t.Fatalf("each call must send its own additive edge (got %d then %d)", first, len(h.fake.lastAddEdges))
		}
	})

	t.Run("empty inputs rejected", func(t *testing.T) {
		h := newContextHarness(t)
		h.callExpectError(t, "track", map[string]any{"resources": []string{}})
		h.callExpectError(t, "track", map[string]any{"resources": []string{"ok", " "}})
	})
}
