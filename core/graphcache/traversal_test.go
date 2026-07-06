package graphcache

import (
	"context"
	"fmt"
	"math"
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
		weighting      EdgeWeighting
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
			if got := tt.c.Neighbor(tt.args.seed, tt.args.step, tt.args.k, tt.args.weighting, tt.args.selectSmallest, nil); !reflect.DeepEqual(got, tt.want) {
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
			got := headSet(mk().Neighbor(seed, 1, tt.k, WeightingRaw, tt.selectSmallest, nil))
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
		gNil := mk().Neighbor("a", 2, 10, WeightingRaw, false, nil)
		gAll := mk().Neighbor("a", 2, 10, WeightingRaw, false, func(string) bool { return true })
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
		g := c.Neighbor("a", 1, 10, WeightingRaw, false, func(s string) bool {
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
		g := c.Neighbor("seed", 1, 10, WeightingRaw, false, keep)
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
		g := c.Neighbor("a", 1, 2, WeightingRaw, false, func(s string) bool {
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
		g := c.Neighbor("m_seed", 2, 10, WeightingRaw, false, func(s string) bool {
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
		g := c.Neighbor("a", 3, 8, WeightingRaw, false, nil)
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
		g, exps, err := c.NeighborWithExpirationsContext(context.Background(), "a", 3, 8, WeightingRaw, false, nil)
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

// TestGraphCache_NeighborBM25 pins the #800 BM25 edge weighting. Because the
// per-hop scoring writes the transformed score back as the edge weight, the
// returned subgraph's edge weights ARE the BM25 scores, so the assertions read
// them directly off g.Edges.
func TestGraphCache_NeighborBM25(t *testing.T) {
	// HubDiscountVsRawAndTFIDF: a globally popular head (high df) is discounted
	// relative to a niche head of equal raw weight. RAW keeps them equal; both
	// TFIDF and BM25 rank the niche head above the hub; and BM25 is a DISTINCT
	// transform from the crude TFIDF (different numeric score for the same edge).
	t.Run("HubDiscountVsRawAndTFIDF", func(t *testing.T) {
		mk := func() *GraphCache[string, string] {
			c := NewGraphCache[string, string](time.Hour)
			for _, v := range []string{"s", "popular", "niche"} {
				c.PutVertex(v, v)
			}
			// Seed points at both heads with identical raw weight.
			c.AddEdge("s", "popular", 1)
			c.AddEdge("s", "niche", 1)
			// Ten other tails also point at "popular", inflating df(popular)
			// to 11 while df(niche) stays 1. These tails are never traversed
			// (we walk from s, step 1) but still count toward document
			// frequency, exactly the "popular item" the hub-suppressor targets.
			for i := 0; i < 10; i++ {
				c.AddEdge(fmt.Sprintf("hubtail%d", i), "popular", 1)
			}
			return c
		}

		raw := mk().Neighbor("s", 1, 10, WeightingRaw, false, nil)
		if got := raw.Edges["s"]["popular"]; got != raw.Edges["s"]["niche"] {
			t.Fatalf("RAW should weight equal-weight edges equally: popular=%v niche=%v",
				got, raw.Edges["s"]["niche"])
		}

		tfidf := mk().Neighbor("s", 1, 10, WeightingTFIDF, false, nil)
		if !(tfidf.Edges["s"]["niche"] > tfidf.Edges["s"]["popular"]) {
			t.Fatalf("TFIDF should rank niche above the hub: niche=%v popular=%v",
				tfidf.Edges["s"]["niche"], tfidf.Edges["s"]["popular"])
		}

		bm25 := mk().Neighbor("s", 1, 10, WeightingBM25, false, nil)
		if !(bm25.Edges["s"]["niche"] > bm25.Edges["s"]["popular"]) {
			t.Fatalf("BM25 should rank niche above the hub: niche=%v popular=%v",
				bm25.Edges["s"]["niche"], bm25.Edges["s"]["popular"])
		}
		// BM25 is N-aware and saturating, so its score for the same edge must
		// diverge from the crude TFIDF transform — proving it is not a relabel.
		if bm25.Edges["s"]["niche"] == tfidf.Edges["s"]["niche"] {
			t.Fatalf("BM25 should differ numerically from TFIDF for niche: both=%v",
				bm25.Edges["s"]["niche"])
		}
	})

	// DocumentLengthNormalization pins the BM25 `b` term: two tails point at the
	// same head with identical raw weight, but one tail is verbose (large
	// out-degree). BM25 damps the edge from the longer document; RAW does not.
	t.Run("DocumentLengthNormalization", func(t *testing.T) {
		mk := func() *GraphCache[string, string] {
			c := NewGraphCache[string, string](time.Hour)
			for _, v := range []string{"seed", "short", "long", "H", "x2", "x3", "x4"} {
				c.PutVertex(v, v)
			}
			// Seed reaches both tails so a 2-hop walk visits each as a tail.
			c.AddEdge("seed", "short", 1)
			c.AddEdge("seed", "long", 1)
			// Both tails point at H with the SAME raw weight...
			c.AddEdge("short", "H", 1)
			c.AddEdge("long", "H", 1)
			// ...but "long" is verbose (out-degree 4 vs 1), so BM25 length-
			// normalisation must damp its edge to H below "short"'s.
			c.AddEdge("long", "x2", 1)
			c.AddEdge("long", "x3", 1)
			c.AddEdge("long", "x4", 1)
			return c
		}

		raw := mk().Neighbor("seed", 2, 10, WeightingRaw, false, nil)
		if raw.Edges["short"]["H"] != raw.Edges["long"]["H"] {
			t.Fatalf("RAW should not length-normalise: short->H=%v long->H=%v",
				raw.Edges["short"]["H"], raw.Edges["long"]["H"])
		}

		bm25 := mk().Neighbor("seed", 2, 10, WeightingBM25, false, nil)
		if !(bm25.Edges["long"]["H"] < bm25.Edges["short"]["H"]) {
			t.Fatalf("BM25 should damp the verbose tail's edge: long->H=%v short->H=%v",
				bm25.Edges["long"]["H"], bm25.Edges["short"]["H"])
		}
	})
}

// TestGraphCache_PersonalizedPageRank pins the #801 forward-push (ACL) PPR
// path: parity with an independent power-iteration reference, the seed-relative
// hub-discount property, top-N capping, the keep frontier predicate, context
// cancellation, and the unknown-seed degenerate case.
func TestGraphCache_PersonalizedPageRank(t *testing.T) {
	// powerIterationPPR is an independent reference for the PPR vector over a
	// row-normalized weighted transition matrix: pi = alpha*e_seed +
	// (1-alpha)*P^T*pi, iterated to convergence. It deliberately shares no code
	// with the forward-push under test.
	powerIterationPPR := func(adj map[string]map[string]float64, seed string, alpha float64) map[string]float64 {
		nodes := map[string]struct{}{seed: {}}
		for u, heads := range adj {
			nodes[u] = struct{}{}
			for v := range heads {
				nodes[v] = struct{}{}
			}
		}
		wsum := make(map[string]float64, len(adj))
		for u, heads := range adj {
			for _, w := range heads {
				wsum[u] += w
			}
		}
		pi := map[string]float64{seed: 1}
		for iter := 0; iter < 100000; iter++ {
			next := make(map[string]float64, len(nodes))
			for v := range nodes {
				next[v] = 0
			}
			next[seed] += alpha
			for u := range nodes {
				mass := pi[u]
				if mass == 0 || wsum[u] == 0 {
					continue
				}
				for v, w := range adj[u] {
					next[v] += (1 - alpha) * mass * (w / wsum[u])
				}
			}
			var diff float64
			for v := range nodes {
				d := next[v] - pi[v]
				if d < 0 {
					d = -d
				}
				diff += d
			}
			pi = next
			if diff < 1e-15 {
				break
			}
		}
		return pi
	}

	t.Run("PowerIterationParity", func(t *testing.T) {
		// A small sink-free strongly connected graph so the reference
		// distribution conserves mass and converges cleanly.
		adj := map[string]map[string]float64{
			"seed": {"a": 1, "b": 2},
			"a":    {"b": 1, "seed": 1},
			"b":    {"seed": 1, "a": 3},
		}
		c := NewGraphCache[string, int](time.Minute)
		for u, heads := range adj {
			for v, w := range heads {
				c.AddEdge(u, v, float32(w))
			}
		}

		const alpha = 0.2
		want := powerIterationPPR(adj, "seed", alpha)

		g, err := c.PersonalizedPageRankContext(context.Background(), "seed", 0, alpha, 1e-7, WeightingRaw, nil)
		if err != nil {
			t.Fatalf("PersonalizedPageRankContext: %v", err)
		}
		star := g.Edges["seed"]
		if star == nil {
			t.Fatalf("no relevance star produced: %+v", g.Edges)
		}
		for _, v := range []string{"a", "b"} {
			got := float64(star[v])
			if diff := math.Abs(got - want[v]); diff > 1e-3 {
				t.Errorf("pi[%s]: forward-push=%.6f power-iteration=%.6f (diff %.6f > 1e-3)", v, got, want[v], diff)
			}
		}
		// The seed is never emitted in its own star.
		if _, ok := star["seed"]; ok {
			t.Errorf("seed must be excluded from its own relevance star: %+v", star)
		}
	})

	t.Run("HubDiscountVsGlobalPopularity", func(t *testing.T) {
		// near sits in a tight near<->near2 loop one hop from the seed, so a
		// seed-anchored surfer re-visits it many times. hub is globally
		// popular — 20 seed-irrelevant vertices point at it with heavy weight,
		// giving it by far the largest in-degree — but the seed reaches it only
		// directly, and its mass disperses across 20 sinks that never return.
		// PPR must therefore rank near ABOVE hub even though a global
		// popularity / in-degree view would crown hub.
		c := NewGraphCache[string, int](time.Minute)
		c.AddEdge("seed", "near", 1)
		c.AddEdge("near", "near2", 1)
		c.AddEdge("near2", "near", 1)
		c.AddEdge("seed", "hub", 1)
		for i := 0; i < 20; i++ {
			c.AddEdge("hub", fmt.Sprintf("o%d", i), 1)   // hub fans out (disperses mass)
			c.AddEdge(fmt.Sprintf("p%d", i), "hub", 100) // hub globally popular (heavy in-edges)
		}

		// Sanity: hub really is the globally popular vertex (largest in-degree).
		hubIn := 0
		nearIn := 0
		dump := c.SnapshotEdges()
		for _, e := range dump {
			switch e.Head {
			case "hub":
				hubIn++
			case "near":
				nearIn++
			}
		}
		if !(hubIn > nearIn) {
			t.Fatalf("fixture invalid: hub in-degree %d should exceed near in-degree %d", hubIn, nearIn)
		}

		g := c.PersonalizedPageRank("seed", 0, 0.15, 1e-7, WeightingRaw, nil)
		star := g.Edges["seed"]
		if star == nil {
			t.Fatalf("no relevance star produced")
		}
		if !(star["near"] > star["hub"]) {
			t.Errorf("hub discount failed: near=%.6f should outrank globally popular hub=%.6f", star["near"], star["hub"])
		}
		// The discount is decisive, not marginal.
		if star["hub"] > 0 && star["near"] < 2*star["hub"] {
			t.Errorf("expected near to dominate hub by a wide margin: near=%.6f hub=%.6f", star["near"], star["hub"])
		}
	})

	t.Run("TopNCap", func(t *testing.T) {
		c := NewGraphCache[string, int](time.Minute)
		for i := 0; i < 10; i++ {
			c.AddEdge("seed", fmt.Sprintf("v%d", i), float32(i+1))
		}
		g := c.PersonalizedPageRank("seed", 3, 0.15, 1e-6, WeightingRaw, nil)
		if got := len(g.Edges["seed"]); got != 3 {
			t.Errorf("topN=3 should cap the star to 3 vertices, got %d: %+v", got, g.Edges["seed"])
		}
		// The retained vertices are the heaviest-weight (highest-mass) heads.
		for _, v := range []string{"v9", "v8", "v7"} {
			if _, ok := g.Edges["seed"][v]; !ok {
				t.Errorf("top-3 by mass should retain %s: %+v", v, g.Edges["seed"])
			}
		}
	})

	t.Run("KeepFrontierPredicate", func(t *testing.T) {
		c := NewGraphCache[string, int](time.Minute)
		c.AddEdge("seed", "keep/a", 1)
		c.AddEdge("seed", "drop/b", 1)
		c.AddEdge("keep/a", "keep/c", 1)
		c.AddEdge("keep/a", "drop/d", 1)
		keep := func(s string) bool { return strings.HasPrefix(s, "keep/") }
		g := c.PersonalizedPageRank("seed", 0, 0.15, 1e-6, WeightingRaw, keep)
		for v := range g.Edges["seed"] {
			if !strings.HasPrefix(v, "keep/") {
				t.Errorf("keep predicate breached: %q ranked despite not matching", v)
			}
		}
		if _, ok := g.Edges["seed"]["keep/a"]; !ok {
			t.Errorf("matching vertex keep/a should be ranked: %+v", g.Edges["seed"])
		}
	})

	t.Run("ContextCancelled", func(t *testing.T) {
		c := NewGraphCache[string, int](time.Minute)
		c.AddEdge("seed", "a", 1)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := c.PersonalizedPageRankContext(ctx, "seed", 0, 0.15, 1e-6, WeightingRaw, nil); err == nil {
			t.Errorf("a cancelled context must surface its error")
		}
	})

	t.Run("UnknownSeed", func(t *testing.T) {
		c := NewGraphCache[string, int](time.Minute)
		c.AddEdge("seed", "a", 1)
		g := c.PersonalizedPageRank("ghost", 0, 0.15, 1e-6, WeightingRaw, nil)
		if len(g.Vertices) != 0 || len(g.Edges) != 0 {
			t.Errorf("unknown seed must yield an empty graph, got vertices=%v edges=%v", g.Vertices, g.Edges)
		}
	})

	t.Run("DefaultsOnNonPositiveParams", func(t *testing.T) {
		// alpha out of range and epsilon <= 0 must fall back to the documented
		// defaults rather than diverging or spinning.
		c := NewGraphCache[string, int](time.Minute)
		c.AddEdge("seed", "a", 2)
		c.AddEdge("seed", "b", 1)
		g := c.PersonalizedPageRank("seed", 0, 0 /*alpha*/, 0 /*eps*/, WeightingRaw, nil)
		star := g.Edges["seed"]
		if star == nil || !(star["a"] > star["b"]) {
			t.Errorf("defaulted PPR should still rank the heavier head first: %+v", star)
		}
	})
}

// TestGraphCache_LocalCommunity pins the #845 PageRank-Nibble contract:
// sweep-cut boundary detection over the shared push, deterministic ordering,
// the maxSize cap, keep scoping, liveness, the degenerate fallback, and the
// induced-subgraph output shape (real edges + expirations, not a star).
func TestGraphCache_LocalCommunity(t *testing.T) {
	exp := time.Now().Add(time.Hour)

	// clique wires every ordered pair inside members with weight w.
	buildClique := func(c *GraphCache[string, string], members []string, w float32) {
		for _, u := range members {
			c.PutVertexWithExpiration(u, "v", exp)
		}
		for _, u := range members {
			for _, v := range members {
				if u != v {
					c.PutEdgeWithExpiration(u, v, w, exp)
				}
			}
		}
	}

	t.Run("two cliques with weak bridge: community is exactly the seed clique", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		a := []string{"a1", "a2", "a3", "a4"}
		b := []string{"b1", "b2", "b3", "b4"}
		buildClique(c, a, 1.0)
		buildClique(c, b, 1.0)
		// One weak directed bridge each way so mass can leak but the cut is cheap.
		c.PutEdgeWithExpiration("a1", "b1", 0.05, exp)
		c.PutEdgeWithExpiration("b1", "a1", 0.05, exp)

		g, expirations, err := c.LocalCommunityContext(context.Background(), "a2", 0, 0.15, 1e-6, WeightingRaw, nil)
		if err != nil {
			t.Fatalf("LocalCommunityContext: %v", err)
		}
		got := make(map[string]bool, len(g.Vertices))
		for k := range g.Vertices {
			got[k] = true
		}
		for _, m := range a {
			if !got[m] {
				t.Errorf("clique member %s missing from community: %v", m, got)
			}
		}
		for _, m := range b {
			if got[m] {
				t.Errorf("cross-bridge vertex %s leaked into the community: %v", m, got)
			}
		}
		// Induced subgraph, not a star: intra-clique edges must be present
		// with their REAL stored weights, from tails other than the seed.
		if w, ok := g.Edges["a1"]["a3"]; !ok || w != 1.0 {
			t.Errorf("induced edge a1->a3 = (%v,%v), want (1.0,true)", w, ok)
		}
		if len(expirations["a1"]) == 0 {
			t.Errorf("expirations missing for member tail a1")
		}
		// The weak bridge edge to the excluded clique must NOT be present.
		if _, ok := g.Edges["a1"]["b1"]; ok {
			t.Errorf("edge to excluded vertex b1 leaked into the induced subgraph")
		}
	})

	t.Run("maxSize caps the community", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		a := []string{"a1", "a2", "a3", "a4", "a5", "a6"}
		buildClique(c, a, 1.0)
		g, _, err := c.LocalCommunityContext(context.Background(), "a1", 3, 0.15, 1e-6, WeightingRaw, nil)
		if err != nil {
			t.Fatalf("LocalCommunityContext: %v", err)
		}
		if len(g.Vertices) > 3 {
			t.Errorf("community size %d exceeds maxSize=3: %v", len(g.Vertices), g.Vertices)
		}
		if _, ok := g.Vertices["a1"]; !ok {
			t.Errorf("seed evicted by the cap")
		}
	})

	t.Run("keep predicate scopes the community", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		buildClique(c, []string{"p:1", "p:2", "p:3"}, 1.0)
		c.PutVertexWithExpiration("q:x", "v", exp)
		c.PutEdgeWithExpiration("p:1", "q:x", 5.0, exp) // strong but out-of-scope
		g, _, err := c.LocalCommunityContext(context.Background(), "p:1", 0, 0.15, 1e-6, WeightingRaw,
			func(s string) bool { return strings.HasPrefix(s, "p:") })
		if err != nil {
			t.Fatalf("LocalCommunityContext: %v", err)
		}
		if _, ok := g.Vertices["q:x"]; ok {
			t.Errorf("keep-rejected vertex q:x entered the community")
		}
		if len(g.Vertices) < 3 {
			t.Errorf("scoped community lost members: %v", g.Vertices)
		}
	})

	t.Run("deterministic under ties", func(t *testing.T) {
		// A symmetric star: every satellite has identical mass and degree, so
		// the sweep ordering beyond the seed is decided purely by the
		// (p/deg desc, key asc) tie-breaker. Repeated runs must agree exactly.
		c := NewGraphCache[string, string](time.Hour)
		sat := []string{"s1", "s2", "s3", "s4", "s5"}
		c.PutVertexWithExpiration("hub", "v", exp)
		for _, s := range sat {
			c.PutVertexWithExpiration(s, "v", exp)
			c.PutEdgeWithExpiration("hub", s, 1.0, exp)
			c.PutEdgeWithExpiration(s, "hub", 1.0, exp)
		}
		var first map[string]bool
		for i := 0; i < 10; i++ {
			g, _, err := c.LocalCommunityContext(context.Background(), "hub", 3, 0.15, 1e-6, WeightingRaw, nil)
			if err != nil {
				t.Fatalf("run %d: %v", i, err)
			}
			got := make(map[string]bool, len(g.Vertices))
			for k := range g.Vertices {
				got[k] = true
			}
			if first == nil {
				first = got
				continue
			}
			if !reflect.DeepEqual(first, got) {
				t.Fatalf("membership not deterministic: run 0 %v vs run %d %v", first, i, got)
			}
		}
	})

	t.Run("expired member never selected (#750)", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		buildClique(c, []string{"a1", "a2", "a3"}, 1.0)
		c.PutVertexWithExpiration("dead", "v", time.Now().Add(20*time.Millisecond))
		c.PutEdgeWithExpiration("a1", "dead", 10.0, exp)
		c.PutEdgeWithExpiration("dead", "a1", 10.0, exp)
		time.Sleep(30 * time.Millisecond)
		g, _, err := c.LocalCommunityContext(context.Background(), "a1", 0, 0.15, 1e-6, WeightingRaw, nil)
		if err != nil {
			t.Fatalf("LocalCommunityContext: %v", err)
		}
		if _, ok := g.Vertices["dead"]; ok {
			t.Errorf("expired vertex entered the community")
		}
	})

	t.Run("degenerate touched set falls back to mass ranking", func(t *testing.T) {
		// Seed with a single neighbour: touched < 3, so the sweep is
		// undefined and the fallback must return the seed + its neighbour —
		// the same set top-k-by-mass selects.
		c := NewGraphCache[string, string](time.Hour)
		c.PutVertexWithExpiration("a", "v", exp)
		c.PutVertexWithExpiration("b", "v", exp)
		c.PutEdgeWithExpiration("a", "b", 1.0, exp)
		g, _, err := c.LocalCommunityContext(context.Background(), "a", 0, 0.15, 1e-6, WeightingRaw, nil)
		if err != nil {
			t.Fatalf("LocalCommunityContext: %v", err)
		}
		if _, ok := g.Vertices["a"]; !ok {
			t.Errorf("seed missing")
		}
		if _, ok := g.Vertices["b"]; !ok {
			t.Errorf("fallback dropped the only neighbour: %v", g.Vertices)
		}
	})

	t.Run("unknown seed yields empty graph", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		g, _, err := c.LocalCommunityContext(context.Background(), "ghost", 0, 0, 0, WeightingRaw, nil)
		if err != nil || len(g.Vertices) != 0 {
			t.Fatalf("unknown seed: g=%v err=%v", g.Vertices, err)
		}
	})

	t.Run("ctx cancel propagates", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		buildClique(c, []string{"a1", "a2", "a3"}, 1.0)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := c.LocalCommunityContext(ctx, "a1", 0, 0, 0, WeightingRaw, nil); err == nil {
			t.Fatal("cancelled ctx must surface an error")
		}
	})

	t.Run("output edge weights carry the weighting transform (#966)", func(t *testing.T) {
		// Two cliques joined by a weak bridge (as in the first sub-test) so
		// the seed community is the FULL seed clique A. Every member of A
		// also receives the SAME number of external in-edges, so TF-IDF/BM25
		// down-weight the intra-A edges by an identical factor: membership
		// stays A under all three weightings, but the RETURNED weights
		// differ. Raw = verbatim 1; TF-IDF/BM25 = the re-scored value. Pins
		// that the induced-subgraph output is no longer weighting-neutral —
		// it applies the same scoreEdge transform the BFS family does, so a
		// subsequent Reduction honours weighting.
		A := []string{"s", "a", "b", "c"}
		B := []string{"x", "y", "z", "w"}
		mk := func() *GraphCache[string, string] {
			c := NewGraphCache[string, string](time.Hour)
			buildClique(c, A, 1.0)
			buildClique(c, B, 1.0)
			c.PutEdgeWithExpiration("s", "x", 0.05, exp) // weak bridge each way
			c.PutEdgeWithExpiration("x", "s", 0.05, exp)
			for _, m := range A {
				for i := 0; i < 4; i++ {
					ext := fmt.Sprintf("%s_ext%d", m, i)
					c.PutVertexWithExpiration(ext, "v", exp)
					c.PutEdgeWithExpiration(ext, m, 1.0, exp) // in-edge only: skews docFreq, never joins the community
				}
			}
			return c
		}

		raw, _, err := mk().LocalCommunityContext(context.Background(), "s", 0, 0.15, 1e-6, WeightingRaw, nil)
		if err != nil {
			t.Fatalf("Raw: %v", err)
		}
		wr, ok := raw.Edges["a"]["b"]
		if !ok || wr != 1.0 {
			t.Fatalf("Raw induced edge a->b = (%v,%v), want (1,true) — verbatim stored weight; community=%v", wr, ok, raw.Vertices)
		}

		tfidf, _, err := mk().LocalCommunityContext(context.Background(), "s", 0, 0.15, 1e-6, WeightingTFIDF, nil)
		if err != nil {
			t.Fatalf("TFIDF: %v", err)
		}
		wt, ok := tfidf.Edges["a"]["b"]
		if !ok {
			t.Fatalf("TFIDF induced edge a->b missing (community=%v)", tfidf.Vertices)
		}
		if !(wt > 0 && wt < wr) {
			t.Errorf("TFIDF edge a->b = %v, want 0 < w < raw(%v) — idf must down-weight the popular head", wt, wr)
		}

		bm25, _, err := mk().LocalCommunityContext(context.Background(), "s", 0, 0.15, 1e-6, WeightingBM25, nil)
		if err != nil {
			t.Fatalf("BM25: %v", err)
		}
		wb, ok := bm25.Edges["a"]["b"]
		if !ok {
			t.Fatalf("BM25 induced edge a->b missing (community=%v)", bm25.Vertices)
		}
		if wb == wr {
			t.Errorf("BM25 edge a->b = %v, want != raw(%v) — transform not applied", wb, wr)
		}
	})
}
