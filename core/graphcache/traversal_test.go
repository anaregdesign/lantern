package graphcache

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graph"
)

func TestGraphCache_Neighbor(t *testing.T) {
	type args[S comparable] struct {
		seed           S
		step           int
		k              int
		tfidf          bool
		selectSmallest bool
	}
	type testCase[S comparable, T any] struct {
		name string
		c    GraphCache[S, T]
		args args[S]
		want *graph.Graph[S, T]
	}
	tests := []testCase[string, string]{
		// TODO: Add test cases.
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.Neighbor(tt.args.seed, tt.args.step, tt.args.k, tt.args.tfidf, tt.args.selectSmallest, nil); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Neighbor() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGraphCache_Neighbor_ObjectiveDirection pins the #560 fix at the cache
// layer. When a vertex's out-degree exceeds k, the selectSmallest flag chooses
// WHICH k edges survive the per-hop prune: false keeps the k largest-weight
// heads (Top — the maximise/strongest direction), true keeps the k smallest
// (Bottom — the minimise/cheapest direction). Before the fix the prune always
// kept the largest k regardless of objective, starving a cost minimiser of the
// cheap edges it cares about. When k does not bind, the flag is a no-op.
func TestGraphCache_Neighbor_ObjectiveDirection(t *testing.T) {
	const seed = "seed"
	mk := func() *GraphCache[string, string] {
		c := NewGraphCache[string, string](time.Minute)
		// Five heads with distinct weights 1..5 so k=2 binds and the two
		// directions select disjoint survivors.
		c.AddEdge(seed, "h1", 1)
		c.AddEdge(seed, "h2", 2)
		c.AddEdge(seed, "h3", 3)
		c.AddEdge(seed, "h4", 4)
		c.AddEdge(seed, "h5", 5)
		return c
	}

	headSet := func(g *graph.Graph[string, string]) map[string]bool {
		got := map[string]bool{}
		if g != nil {
			for head := range g.Edges[seed] {
				got[head] = true
			}
		}
		return got
	}

	tests := []struct {
		name           string
		k              int
		selectSmallest bool
		want           map[string]bool
	}{
		{
			name:           "selectSmallest=false keeps the k largest",
			k:              2,
			selectSmallest: false,
			want:           map[string]bool{"h4": true, "h5": true},
		},
		{
			name:           "selectSmallest=true keeps the k smallest",
			k:              2,
			selectSmallest: true,
			want:           map[string]bool{"h1": true, "h2": true},
		},
		{
			name:           "k does not bind: direction is a no-op",
			k:              5,
			selectSmallest: true,
			want: map[string]bool{
				"h1": true, "h2": true, "h3": true, "h4": true, "h5": true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := headSet(mk().Neighbor(seed, 1, tt.k, false, tt.selectSmallest, nil))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Neighbor(k=%d, selectSmallest=%v) heads = %v, want %v",
					tt.k, tt.selectSmallest, got, tt.want)
			}
		})
	}
}

// TestGraphCache_NeighborKeep pins the #601 frontier predicate: keep func(S) bool
// is applied at frontier materialisation, BEFORE scoring and the per-hop top-k
// prune, and the seed is always exempt. The prefix-style closures here stand in
// for the strings.HasPrefix closure the server builds in #602 — core itself stays
// generic (it only ever sees a predicate over S).
func TestGraphCache_NeighborKeep(t *testing.T) {
	// KeepNil_Unchanged: a nil predicate is identical to an accept-all predicate
	// (the pre-#601 behaviour).
	t.Run("KeepNil_Unchanged", func(t *testing.T) {
		mk := func() *GraphCache[string, int] {
			c := NewGraphCache[string, int](time.Minute)
			c.PutVertex("a", 0)
			c.PutVertex("b", 0)
			c.PutVertex("c", 0)
			c.AddEdge("a", "b", 2)
			c.AddEdge("a", "c", 1)
			return c
		}
		gNil := mk().Neighbor("a", 2, 10, false, false, nil)
		gAll := mk().Neighbor("a", 2, 10, false, false, func(string) bool { return true })
		if !reflect.DeepEqual(gNil, gAll) {
			t.Errorf("nil predicate graph = %v, want identical to accept-all %v", gNil, gAll)
		}
	})

	// KeepExcludesNonMatching: a predicate that rejects some heads drops exactly
	// those heads (and their vertices) from the result.
	t.Run("KeepExcludesNonMatching", func(t *testing.T) {
		c := NewGraphCache[string, int](time.Minute)
		for _, k := range []string{"a", "keep1", "keep2", "drop1"} {
			c.PutVertex(k, 0)
		}
		c.AddEdge("a", "keep1", 1)
		c.AddEdge("a", "keep2", 1)
		c.AddEdge("a", "drop1", 1)
		g := c.Neighbor("a", 1, 10, false, false, func(s string) bool {
			return strings.HasPrefix(s, "keep")
		})
		if _, ok := g.Edges["a"]["drop1"]; ok {
			t.Errorf("non-matching head drop1 survived: %v", g.Edges["a"])
		}
		for _, want := range []string{"keep1", "keep2"} {
			if _, ok := g.Edges["a"][want]; !ok {
				t.Errorf("matching head %q missing: %v", want, g.Edges["a"])
			}
		}
		if _, ok := g.Vertices["drop1"]; ok {
			t.Errorf("non-matching vertex drop1 present in g.Vertices")
		}
	})

	// SeedRetainedWhenItFails: the seed is the anchor exemption — present even
	// when keep(seed) is false.
	t.Run("SeedRetainedWhenItFails", func(t *testing.T) {
		c := NewGraphCache[string, int](time.Minute)
		c.PutVertex("seed", 0)
		c.PutVertex("keep1", 0)
		c.AddEdge("seed", "keep1", 1)
		keep := func(s string) bool { return strings.HasPrefix(s, "keep") }
		if keep("seed") {
			t.Fatal("test setup: seed must NOT match the predicate")
		}
		g := c.Neighbor("seed", 1, 10, false, false, keep)
		if _, ok := g.Vertices["seed"]; !ok {
			t.Errorf("seed dropped despite anchor exemption: %v", g.Vertices)
		}
		if _, ok := g.Edges["seed"]["keep1"]; !ok {
			t.Errorf("matching head keep1 missing from seed edges: %v", g.Edges["seed"])
		}
	})

	// KeepBeforeTopK is the load-bearing ordering proof: the k strongest heads
	// overall are all non-matching, so if the filter ran AFTER top-k the result
	// would be empty. Filtering BEFORE top-k yields the k strongest *matching*
	// heads instead.
	t.Run("KeepBeforeTopK", func(t *testing.T) {
		c := NewGraphCache[string, int](time.Minute)
		for _, k := range []string{"a", "x1", "x2", "y1", "y2", "y3"} {
			c.PutVertex(k, 0)
		}
		// Non-matching heads carry the largest weights; matching heads are weaker.
		c.AddEdge("a", "x1", 100)
		c.AddEdge("a", "x2", 90)
		c.AddEdge("a", "y1", 10)
		c.AddEdge("a", "y2", 9)
		c.AddEdge("a", "y3", 8)
		// k=2, Top (selectSmallest=false): without the before-top-k ordering this
		// would select x1,x2 then filter to empty.
		g := c.Neighbor("a", 1, 2, false, false, func(s string) bool {
			return strings.HasPrefix(s, "y")
		})
		got := map[string]bool{}
		for head := range g.Edges["a"] {
			got[head] = true
		}
		want := map[string]bool{"y1": true, "y2": true}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("KeepBeforeTopK heads = %v, want %v (filter must run before top-k)", got, want)
		}
	})

	// NonMatchingBridgeBlocks: induced-subgraph semantics — a matching vertex
	// reachable only through a rejected bridge is never reached.
	t.Run("NonMatchingBridgeBlocks", func(t *testing.T) {
		c := NewGraphCache[string, int](time.Minute)
		for _, k := range []string{"m_seed", "bridge", "m_target", "m_direct"} {
			c.PutVertex(k, 0)
		}
		c.AddEdge("m_seed", "bridge", 5)   // non-matching bridge
		c.AddEdge("bridge", "m_target", 5) // matching, but only reachable via bridge
		c.AddEdge("m_seed", "m_direct", 1) // matching, directly reachable
		g := c.Neighbor("m_seed", 2, 10, false, false, func(s string) bool {
			return strings.HasPrefix(s, "m")
		})
		if _, ok := g.Vertices["bridge"]; ok {
			t.Errorf("rejected bridge present in g.Vertices")
		}
		if _, ok := g.Vertices["m_target"]; ok {
			t.Errorf("m_target reached through a rejected bridge (induced-subgraph violated)")
		}
		if _, ok := g.Vertices["m_direct"]; !ok {
			t.Errorf("directly-reachable matching vertex m_direct missing")
		}
		if _, ok := g.Edges["bridge"]; ok {
			t.Errorf("rejected bridge was expanded: %v", g.Edges["bridge"])
		}
	})
}

// TestGraphCache_Neighbor_HidesDeadEndpoints pins the traversal half of the
// referential-closure contract (#750): neither Neighbor nor
// NeighborWithExpirationsContext may surface an edge to (or a vertex that is) a
// deleted/expired endpoint, and a dead intermediary must not be a path to
// vertices beyond it.
func TestGraphCache_Neighbor_HidesDeadEndpoints(t *testing.T) {
	live := time.Now().Add(time.Hour)
	build := func() *GraphCache[string, string] {
		c := NewGraphCache[string, string](time.Hour)
		c.PutVertexWithExpiration("a", "a", live)
		c.PutVertexWithExpiration("b", "b", live)
		c.PutVertexWithExpiration("c", "c", live)
		c.AddEdgeWithExpiration("a", "b", 1, live)
		c.AddEdgeWithExpiration("b", "c", 1, live)
		return c
	}

	t.Run("DeletedHeadDropsEdgeAndVertex", func(t *testing.T) {
		c := build()
		if !c.DeleteVertex("b") {
			t.Fatal("DeleteVertex(b) reported false")
		}
		g := c.Neighbor("a", 3, 8, false, false, nil)
		if _, ok := g.Vertices["b"]; ok {
			t.Errorf("Neighbor surfaced deleted vertex b: %v", g.Vertices)
		}
		if heads, ok := g.Edges["a"]; ok {
			if _, ok := heads["b"]; ok {
				t.Errorf("Neighbor surfaced dangling edge a->b: %v", g.Edges)
			}
		}
		if _, ok := g.Edges["b"]; ok {
			t.Errorf("Neighbor surfaced edges from deleted tail b: %v", g.Edges["b"])
		}
		if _, ok := g.Vertices["c"]; ok {
			t.Errorf("Neighbor reached c through dead intermediary b: %v", g.Vertices)
		}
		if _, ok := g.Vertices["a"]; !ok {
			t.Errorf("Neighbor dropped the live seed a: %v", g.Vertices)
		}
	})

	t.Run("ExpirationsMapExcludesDeadEndpoints", func(t *testing.T) {
		c := build()
		if !c.DeleteVertex("b") {
			t.Fatal("DeleteVertex(b) reported false")
		}
		g, exps, err := c.NeighborWithExpirationsContext(context.Background(), "a", 3, 8, false, false, nil)
		if err != nil {
			t.Fatalf("NeighborWithExpirationsContext: %v", err)
		}
		if heads, ok := exps["a"]; ok {
			if _, ok := heads["b"]; ok {
				t.Errorf("expirations map surfaced dangling edge a->b: %v", exps)
			}
		}
		if heads, ok := g.Edges["a"]; ok {
			if _, ok := heads["b"]; ok {
				t.Errorf("graph surfaced dangling edge a->b: %v", g.Edges)
			}
		}
	})
}
