package graphcache

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graph"
)

func TestQueryView(t *testing.T) {
	ranges := []KeyRange{{"data:a:", "data:a:private:"}, {"data:a:private;", "data:a;"}, {"data:日本:", "data:日本;"}}
	view, err := NewQueryView(ranges, ranges)
	if err != nil {
		t.Fatal(err)
	}
	ranges[0].Lower = "mutated"
	if !view.Vertex("data:a:1") || view.Vertex("data:a:private:1") || view.Vertex("sys:1") || !view.Edge("data:a:1", "data:日本:1") || view.Edge("data:a:1", "data:a:private:1") {
		t.Fatal("invalid detached view")
	}
	for _, invalid := range [][]KeyRange{{{"z", "a"}}, {{"a", ""}, {"z", ""}}, {{"b", "c"}, {"a", "b"}}, {{"a", "d"}, {"c", "e"}}} {
		if _, err := NewQueryView(invalid, nil); err == nil {
			t.Fatalf("accepted invalid ranges %v", invalid)
		}
	}
}

func TestQueryViewTraversalSharesWeightsButExcludesPaths(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	c.EnablePrefixIndex(identityExtract)
	c.AddEdge("visible:seed", "visible:a", 3)
	c.AddEdge("visible:seed", "visible:blocked:a", 1000)
	c.AddEdge("visible:seed", "hidden:bridge", 1000)
	c.AddEdge("hidden:bridge", "visible:unreachable", 1000)
	c.AddEdge("visible:a", "visible:seed", 1)
	vertices := []KeyRange{{"visible:", "visible;"}}
	edges := []KeyRange{{"visible:", "visible:blocked:"}, {"visible:blocked;", "visible;"}}
	view, _ := NewQueryView(vertices, edges)
	ctx := WithQueryView(context.Background(), view)
	check := func(weighting EdgeWeighting) float32 {
		t.Helper()
		got, _, err := c.NeighborWithExpirationsContext(ctx, "visible:seed", 3, 1, weighting, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		for key := range got.Vertices {
			if key != "visible:seed" && key != "visible:a" {
				t.Fatal("hidden path traversed", key)
			}
		}
		// Filtering the same complete graph's candidates preserves the exact
		// shared base weight, including DF and hidden outgoing DocLen.
		want, _, err := c.NeighborWithExpirationsContext(context.Background(), "visible:seed", 1, 1, weighting, false, func(head string) bool { return head == "visible:a" })
		if err != nil || got.Edges["visible:seed"]["visible:a"] != want.Edges["visible:seed"]["visible:a"] {
			t.Fatal("range constraint changed base ranking statistics", err, got, want)
		}
		assertPaths := func(result *graph.Graph[string, string]) {
			for key := range result.Vertices {
				if !view.Vertex(key) || key == "visible:unreachable" || key == "visible:blocked:a" {
					t.Fatal("hidden node/bridge contributed to traversal", key)
				}
			}
			for tail, heads := range result.Edges {
				for head := range heads {
					if !view.Edge(tail, head) {
						t.Fatal("hidden edge disclosed", tail, head)
					}
				}
			}
		}
		ppr, err := c.PersonalizedPageRankWithWorkBudgetContext(ctx, "visible:seed", 10, .2, 1e-4, weighting, nil, PPRWorkBudget{MaxPushes: 10000, MaxTouchedEdges: 100000})
		if err != nil {
			t.Fatal(err)
		}
		assertPaths(ppr)
		community, _, err := c.LocalCommunityWithWorkBudgetContext(ctx, "visible:seed", 10, .2, 1e-4, weighting, nil, PPRWorkBudget{MaxPushes: 10000, MaxTouchedEdges: 100000})
		if err != nil {
			t.Fatal(err)
		}
		assertPaths(community)
		degree := c.TopVerticesByDegreeContext(ctx, "visible:seed", 1, DegreeOut, true)
		if len(degree) != 1 || degree[0].Degree != 1 || degree[0].WeightedDegree != 3 {
			t.Fatal("shared ranking statistics leaked into actual degree", degree)
		}
		denied, _, err := c.NeighborWithExpirationsContext(ctx, "hidden:bridge", 1, 1, weighting, false, nil)
		if err != nil || len(denied.Vertices) != 0 {
			t.Fatal("seed bypassed view", denied, err)
		}
		return got.Edges["visible:seed"]["visible:a"]
	}
	before := map[EdgeWeighting]float32{}
	for _, weighting := range []EdgeWeighting{WeightingRaw, WeightingTFIDF, WeightingBM25} {
		before[weighting] = check(weighting)
	}
	count := c.CountByPrefixContext(ctx, "visible:")
	for i := range 80 {
		c.AddEdge(fmt.Sprintf("hidden:%d", i), "visible:a", 100)
	}
	for _, weighting := range []EdgeWeighting{WeightingRaw, WeightingTFIDF, WeightingBM25} {
		after := check(weighting)
		if weighting != WeightingRaw && before[weighting] == after {
			t.Fatal("private application edges did not participate in shared corpus", weighting)
		}
	}
	if c.CountByPrefixContext(ctx, "visible:") != count {
		t.Fatal("hidden-only vertices entered actual counts")
	}
}

func TestQueryViewTraversalBoundsRejectedAdjacencyWork(t *testing.T) {
	c := NewGraphCache[string, int](time.Hour)
	c.EnablePrefixIndex(identityExtract)
	c.AddEdge("visible:seed", "hidden:a", 1)
	c.AddEdge("visible:seed", "hidden:b", 1)
	ranges := []KeyRange{{"visible:", "visible;"}}
	view, _ := NewQueryView(ranges, ranges)
	ctx := WithQueryView(context.Background(), view)
	budget := PPRWorkBudget{MaxPushes: 100, MaxTouchedEdges: 1}
	for _, community := range []bool{false, true} {
		var err error
		if community {
			_, _, err = c.LocalCommunityWithWorkBudgetContext(ctx, "visible:seed", 10, .2, 1e-4, WeightingBM25, nil, budget)
		} else {
			_, err = c.PersonalizedPageRankWithWorkBudgetContext(ctx, "visible:seed", 10, .2, 1e-4, WeightingBM25, nil, budget)
		}
		var exhausted *PPRWorkBudgetExceededError
		if !errors.As(err, &exhausted) || exhausted.TouchedEdges != 2 {
			t.Fatal("denied adjacency escaped the physical work limit", community, err)
		}
	}
}

func TestQueryViewSharedStatisticsAfterMaintenanceAndRestore(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	c.EnablePrefixIndex(identityExtract)
	exp := time.Now().Add(time.Hour)
	check := func(c *GraphCache[string, string]) {
		t.Helper()
		c.mu.RLock()
		defer c.mu.RUnlock()
		tails, count := c.edges.corpusStats()
		df := make(map[vertexID]int)
		edges := 0
		for _, heads := range c.edges.tf {
			for head := range heads {
				edges++
				df[head]++
			}
		}
		if tails != len(c.edges.tf) || count != edges || !reflect.DeepEqual(df, c.edges.df) {
			t.Fatalf("shared structural statistics drift: tails=%d edges=%d DF=%v want=%v", tails, count, c.edges.df, df)
		}
	}
	c.PutEdgeWithExpiration("visible:a", "visible:b", 3, exp)
	c.PutEdgeWithExpiration("hidden:c", "visible:b", 10, exp)
	check(c)
	c.PutEdgeWithExpiration("visible:a", "visible:b", 5, exp)
	check(c)
	c.PutEdgeWithExpiration("hidden:d", "visible:b", 1, exp)
	c.DeleteEdge("hidden:c", "visible:b")
	check(c)
	// Ranking statistics retain the pre-existing structural bucket semantics;
	// TTL/zero/dangling maintenance must update the shared aggregates exactly.
	c.AddEdgeWithExpiration("hidden:ttl", "visible:b", 1, time.Now().Add(-time.Hour))
	c.flush()
	check(c)
	ranges := []KeyRange{{"visible:", "visible;"}}
	view, _ := NewQueryView(ranges, ranges)
	ctx := WithQueryView(context.Background(), view)
	restored := NewGraphCache[string, string](time.Hour)
	restored.EnablePrefixIndex(identityExtract)
	replayReplicationSnapshot(restored, c.SnapshotReplication())
	check(restored)
	for _, weighting := range []EdgeWeighting{WeightingTFIDF, WeightingBM25} {
		before, _, err := c.NeighborWithExpirationsContext(ctx, "visible:a", 1, 10, weighting, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		after, _, err := restored.NeighborWithExpirationsContext(ctx, "visible:a", 1, 10, weighting, false, nil)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("restore changed shared ranking statistics", weighting, before, after, err)
		}
	}
}

func TestQueryViewScanCountDelete(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	for _, key := range []string{"data:a:1", "data:a:2", "data:a:private:1", "data:a:private:2", "data:a:z", "data:b:1", "data:日本:1", "sys:1"} {
		c.PutVertex(key, key)
	}
	ranges := []KeyRange{{"data:a:", "data:a:private:"}, {"data:a:private;", "data:a;"}, {"data:日本:", "data:日本;"}}
	view, _ := NewQueryView(ranges, ranges)
	ctx := WithQueryView(context.Background(), view)
	if got := c.CountByPrefixContext(ctx, "data:"); got != 4 {
		t.Fatalf("visible count %d", got)
	}
	if got := c.CountByPrefixContext(ctx, "data:a:private:"); got != 0 {
		t.Fatalf("denied count %d", got)
	}
	for _, desc := range []bool{false, true} {
		var got []string
		after := ""
		for {
			more, ok := c.ScanByPrefixPage(ctx, "data:", after, 1, desc, func(_, key, _ string) bool { got = append(got, key); after = key; return true })
			if !ok {
				t.Fatal("incomplete visible scan")
			}
			if !more {
				break
			}
		}
		want := []string{"data:a:1", "data:a:2", "data:a:z", "data:日本:1"}
		if desc {
			want = []string{"data:日本:1", "data:a:z", "data:a:2", "data:a:1"}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("descending=%v got=%v want=%v", desc, got, want)
		}
	}
	keys, err := c.DeleteByPrefixKeysWithPreflight(ctx, "data:", 3, func(keys []string) error {
		if !reflect.DeepEqual(keys, []string{"data:a:1", "data:a:2", "data:a:z"}) {
			t.Fatalf("unexpected victims %v", keys)
		}
		return nil
	})
	if err != nil || len(keys) != 3 {
		t.Fatalf("delete %v %v", keys, err)
	}
	for _, key := range []string{"data:a:private:1", "data:a:private:2", "data:b:1", "sys:1"} {
		if _, ok := c.GetVertex(key); !ok {
			t.Fatalf("deleted hidden key %q", key)
		}
	}
}

func TestQueryViewEdgeScanDelete(t *testing.T) {
	for _, indexed := range []bool{true, false} {
		c := NewGraphCache[string, string](time.Minute)
		c.EnablePrefixIndex(identityExtract)
		c.AddEdgesWithExpiration([]EdgeItem[string]{{Tail: "data:a:1", Head: "data:a:private:1", Weight: 100}, {Tail: "data:a:1", Head: "data:a:2", Weight: 2}, {Tail: "data:b:1", Head: "data:a:2", Weight: 3}, {Tail: "data:a:2", Head: "data:a:3", Weight: 4}})
		if !indexed {
			c.headByTail = nil
		}
		ranges := []KeyRange{{"data:a:", "data:a:private:"}, {"data:a:private;", "data:a;"}}
		view, _ := NewQueryView(ranges, ranges)
		ctx := WithQueryView(context.Background(), view)
		var got []EdgeKey[string]
		more, ok := c.ScanEdgesByPrefixPage(ctx, "data:", "data:", "", "", 1, func(_ string, tail string, _ string, head string, _ float32, _ time.Time) bool {
			got = append(got, EdgeKey[string]{tail, head})
			return true
		})
		if !more || !ok || !reflect.DeepEqual(got, []EdgeKey[string]{{"data:a:1", "data:a:2"}}) {
			t.Fatalf("indexed=%v visible page %v %v %v", indexed, got, more, ok)
		}
		if n := c.DeleteEdgesByPrefix(ctx, "data:", "data:", 10); n != 2 {
			t.Fatalf("authorized edge delete %d", n)
		}
		if _, _, ok := c.GetEdgeDetail("data:a:1", "data:a:private:1"); !ok {
			t.Fatal("deleted hidden edge")
		}
	}
}

func TestQueryViewRetainedDanglingSourcesNeverCreatePaths(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	c.RetainDanglingEdgeHistory()
	c.EnablePrefixIndex(identityExtract)
	c.PutVertex("visible:seed", "seed")
	c.PutVertex("visible:a", "allowed")
	c.PutVertex("visible:unreachable", "behind missing bridge")
	for _, item := range []EdgeItem[string]{
		{Tail: "visible:seed", Head: "visible:a", Weight: 1, NoEndpointCreation: true},
		{Tail: "visible:seed", Head: "visible:missing", Weight: 1000, NoEndpointCreation: true},
		{Tail: "visible:missing", Head: "visible:unreachable", Weight: 1000, NoEndpointCreation: true},
	} {
		if _, err := c.PutEdgesWithExpirationOutcomesChecked([]EdgeItem[string]{item}); err != nil {
			t.Fatal(err)
		}
	}
	c.flush()
	if len(c.SnapshotReplication().Graph.Edges) != 3 {
		t.Fatal("private pending history lost")
	}
	view, err := NewQueryView([]KeyRange{{"visible:", "visible;"}}, []KeyRange{{"visible:", "visible;"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{context.Background(), WithQueryView(context.Background(), view)} {
		for _, weighting := range []EdgeWeighting{WeightingRaw, WeightingTFIDF, WeightingBM25} {
			bfs, _, err := c.NeighborWithExpirationsContext(ctx, "visible:seed", 3, 1, weighting, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(bfs.Vertices) != 2 || len(bfs.Edges["visible:seed"]) != 1 {
				t.Fatal("dangling source displaced live top-k", bfs)
			}
			ppr, err := c.PersonalizedPageRankContext(ctx, "visible:seed", 10, .2, 1e-4, weighting, nil)
			if err != nil {
				t.Fatal(err)
			}
			community, _, err := c.LocalCommunityContext(ctx, "visible:seed", 10, .2, 1e-4, weighting, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, result := range []*graph.Graph[string, string]{bfs, ppr, community} {
				for key := range result.Vertices {
					if key != "visible:seed" && key != "visible:a" {
						t.Fatal("traversal passed through absent endpoint", key)
					}
				}
				for tail, row := range result.Edges {
					for head := range row {
						if !c.vertices.Has(tail) || !c.vertices.Has(head) {
							t.Fatal("dangling public Edge", tail, head)
						}
					}
				}
			}
		}
	}
}
