package mcp

import (
	"context"
	"testing"
)

func TestContextStats(t *testing.T) {
	t.Run("counts per reserved prefix and derives resources by subtraction", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.countByPrefixFn = func(_ context.Context, prefix string) (uint64, error) {
			switch prefix {
			case agentKeyPrefix:
				return 3, nil
			case claimKeyPrefix:
				return 2, nil
			case noteKeyPrefix:
				return 1, nil
			case "":
				return 10, nil
			}
			t.Fatalf("unexpected count prefix %q", prefix)
			return 0, nil
		}
		res := h.call(t, "context_stats", map[string]any{})
		var out contextStatsOutput
		structuredAs(t, res, &out)
		if out.Agents != 3 || out.Claims != 2 || out.Notes != 1 || out.Resources != 4 {
			t.Fatalf("stats: %+v", out)
		}
	})

	t.Run("resources never go negative", func(t *testing.T) {
		h := newContextHarness(t)
		h.fake.countByPrefixFn = func(_ context.Context, prefix string) (uint64, error) {
			if prefix == "" {
				return 1, nil
			}
			return 5, nil
		}
		res := h.call(t, "context_stats", map[string]any{})
		var out contextStatsOutput
		structuredAs(t, res, &out)
		if out.Resources != 0 {
			t.Fatalf("resources = %d, want clamped 0", out.Resources)
		}
	})
}
