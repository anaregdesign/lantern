package graph

import (
	"context"
	"testing"
	"time"
)

// Asserts that NeighborWithExpirationsContext returns expiration data
// aligned to the same (tail, head) pairs present in the returned graph,
// so handlers do not need a second per-edge cache lookup.
func TestGraphCache_NeighborWithExpirationsContext_ReturnsAlignedMap(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	c.PutVertex("a", 1)
	c.PutVertex("b", 2)
	c.PutVertex("c", 3)

	expAB := time.Now().Add(30 * time.Second).Truncate(time.Microsecond)
	expAC := time.Now().Add(45 * time.Second).Truncate(time.Microsecond)
	c.AddEdgeWithExpiration("a", "b", 1.0, expAB)
	c.AddEdgeWithExpiration("a", "c", 2.0, expAC)

	g, exps, err := c.NeighborWithExpirationsContext(context.Background(), "a", 2, 10, false)
	if err != nil {
		t.Fatalf("NeighborWithExpirationsContext: %v", err)
	}
	if exps == nil {
		t.Fatal("expirations map is nil")
	}

	for tail, heads := range g.Edges {
		row, ok := exps[tail]
		if !ok {
			t.Errorf("missing expirations row for %q", tail)
			continue
		}
		for head := range heads {
			got, ok := row[head]
			if !ok {
				t.Errorf("missing expiration for edge %q->%q", tail, head)
				continue
			}
			if got.IsZero() {
				t.Errorf("expiration for edge %q->%q is zero", tail, head)
			}
		}
	}

	if got, want := exps["a"]["b"].Unix(), expAB.Unix(); got != want {
		t.Errorf("exp a->b unix = %d, want %d", got, want)
	}
	if got, want := exps["a"]["c"].Unix(), expAC.Unix(); got != want {
		t.Errorf("exp a->c unix = %d, want %d", got, want)
	}
}

// Smoke test: the plain Neighbor / NeighborContext paths still work.
func TestGraphCache_NeighborContext_StillWorksAfterRefactor(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	c.PutVertex("a", 1)
	c.PutVertex("b", 2)
	c.AddEdge("a", "b", 1.0)

	g, err := c.NeighborContext(context.Background(), "a", 2, 10, false)
	if err != nil {
		t.Fatalf("NeighborContext: %v", err)
	}
	if len(g.Edges["a"]) != 1 {
		t.Errorf("len(g.Edges[a]) = %d, want 1", len(g.Edges["a"]))
	}
}
