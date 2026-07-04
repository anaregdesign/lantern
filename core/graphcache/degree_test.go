package graphcache

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// degreeKeys projects a []DegreeEntry into its key sequence so ordering
// assertions read cleanly.
func degreeKeys(entries []DegreeEntry[string]) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Key
	}
	return out
}

// newDegreeGraph builds a small fixed graph whose per-direction degrees are all
// distinct, so top-k ordering is unambiguous. Vertices n:a..n:d are the ranking
// candidates under prefix "n:"; x:1/x:2 sit outside the prefix and exercise
// out-edges leaving the namespace and in-edges entering it.
//
//	OUT (count/weight)          IN (count/weight)         BOTH (count/weight)
//	n:a 3 / 7                   n:a 1 / 1                 n:a 4 / 8
//	n:b 1 / 1                   n:b 1 / 1                 n:b 2 / 2
//	n:c 0 / 0                   n:c 3 / 11                n:c 3 / 11
//	n:d 0 / 0                   n:d 0 / 0                 n:d 0 / 0
func newDegreeGraph(t *testing.T) *GraphCache[string, string] {
	t.Helper()
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	exp := time.Now().Add(time.Minute)
	for _, k := range []string{"n:a", "n:b", "n:c", "n:d", "x:1", "x:2"} {
		c.PutVertexWithExpiration(k, "v", exp)
	}
	edges := []struct {
		tail, head string
		w          float32
	}{
		{"n:a", "n:b", 1},
		{"n:a", "n:c", 2},
		{"n:a", "x:1", 4},
		{"n:b", "n:c", 1},
		{"x:2", "n:c", 8},
		{"x:2", "n:a", 1},
	}
	for _, e := range edges {
		c.PutEdgeWithExpiration(e.tail, e.head, e.w, exp)
	}
	return c
}

func TestGraphCache_TopVerticesByDegree(t *testing.T) {
	t.Run("OutByCount", func(t *testing.T) {
		c := newDegreeGraph(t)
		got := c.TopVerticesByDegree("n:", 2, DegreeOut, false)
		if want := []string{"n:a", "n:b"}; !equalSlices(degreeKeys(got), want) {
			t.Fatalf("keys = %v, want %v", degreeKeys(got), want)
		}
		if got[0].Degree != 3 || got[0].WeightedDegree != 7 {
			t.Errorf("n:a = {%d,%g}, want {3,7}", got[0].Degree, got[0].WeightedDegree)
		}
		if got[1].Degree != 1 || got[1].WeightedDegree != 1 {
			t.Errorf("n:b = {%d,%g}, want {1,1}", got[1].Degree, got[1].WeightedDegree)
		}
	})

	t.Run("OutByWeight", func(t *testing.T) {
		c := newDegreeGraph(t)
		got := c.TopVerticesByDegree("n:", 2, DegreeOut, true)
		if want := []string{"n:a", "n:b"}; !equalSlices(degreeKeys(got), want) {
			t.Fatalf("keys = %v, want %v", degreeKeys(got), want)
		}
		if got[0].WeightedDegree != 7 {
			t.Errorf("n:a weighted = %g, want 7", got[0].WeightedDegree)
		}
	})

	t.Run("InByCount", func(t *testing.T) {
		c := newDegreeGraph(t)
		got := c.TopVerticesByDegree("n:", 1, DegreeIn, false)
		if len(got) != 1 || got[0].Key != "n:c" || got[0].Degree != 3 {
			t.Fatalf("in top-1 = %+v, want n:c degree 3", got)
		}
		if got[0].WeightedDegree != 11 {
			t.Errorf("n:c weighted in = %g, want 11", got[0].WeightedDegree)
		}
	})

	t.Run("InByWeight", func(t *testing.T) {
		c := newDegreeGraph(t)
		got := c.TopVerticesByDegree("n:", 1, DegreeIn, true)
		if len(got) != 1 || got[0].Key != "n:c" || got[0].WeightedDegree != 11 {
			t.Fatalf("in top-1 weighted = %+v, want n:c weight 11", got)
		}
	})

	t.Run("BothByCount", func(t *testing.T) {
		c := newDegreeGraph(t)
		got := c.TopVerticesByDegree("n:", 3, DegreeBoth, false)
		if want := []string{"n:a", "n:c", "n:b"}; !equalSlices(degreeKeys(got), want) {
			t.Fatalf("keys = %v, want %v", degreeKeys(got), want)
		}
		if got[0].Degree != 4 || got[1].Degree != 3 || got[2].Degree != 2 {
			t.Errorf("degrees = %d,%d,%d, want 4,3,2", got[0].Degree, got[1].Degree, got[2].Degree)
		}
	})

	t.Run("BothByWeight", func(t *testing.T) {
		c := newDegreeGraph(t)
		got := c.TopVerticesByDegree("n:", 2, DegreeBoth, true)
		if want := []string{"n:c", "n:a"}; !equalSlices(degreeKeys(got), want) {
			t.Fatalf("keys = %v, want %v", degreeKeys(got), want)
		}
		if got[0].WeightedDegree != 11 || got[1].WeightedDegree != 8 {
			t.Errorf("weights = %g,%g, want 11,8", got[0].WeightedDegree, got[1].WeightedDegree)
		}
	})

	t.Run("KClampZeroReturnsNil", func(t *testing.T) {
		c := newDegreeGraph(t)
		if got := c.TopVerticesByDegree("n:", 0, DegreeOut, false); got != nil {
			t.Errorf("k=0 = %v, want nil", got)
		}
	})

	t.Run("ZeroDegreeCandidatesSurfaceOnlyWhenRoomRemains", func(t *testing.T) {
		c := newDegreeGraph(t)
		got := c.TopVerticesByDegree("n:", 4, DegreeOut, false)
		if len(got) != 4 {
			t.Fatalf("k=4 returned %d entries, want 4", len(got))
		}
		// The two positive-degree vertices rank first; the two isolated ones
		// (degree 0) fill the tail in arbitrary order.
		if !equalSlices(degreeKeys(got)[:2], []string{"n:a", "n:b"}) {
			t.Errorf("head = %v, want [n:a n:b]", degreeKeys(got)[:2])
		}
		tail := append([]string(nil), degreeKeys(got)[2:]...)
		sort.Strings(tail)
		if !equalSlices(tail, []string{"n:c", "n:d"}) {
			t.Errorf("tail = %v, want [n:c n:d]", tail)
		}
		if got[2].Degree != 0 || got[3].Degree != 0 {
			t.Errorf("tail degrees = %d,%d, want 0,0", got[2].Degree, got[3].Degree)
		}
	})

	t.Run("PrefixIsolation", func(t *testing.T) {
		c := newDegreeGraph(t)
		got := c.TopVerticesByDegree("n:", 10, DegreeBoth, false)
		for _, e := range got {
			if e.Key == "x:1" || e.Key == "x:2" {
				t.Errorf("prefix n: leaked out-of-namespace vertex %q", e.Key)
			}
		}
	})

	t.Run("DisabledIndexReturnsNil", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.PutVertex("n:a", "v")
		c.AddEdge("n:a", "n:b", 1)
		if got := c.TopVerticesByDegree("n:", 5, DegreeOut, false); got != nil {
			t.Errorf("disabled prefix index = %v, want nil", got)
		}
	})

	t.Run("EmptyPrefixMatchReturnsNil", func(t *testing.T) {
		c := newDegreeGraph(t)
		if got := c.TopVerticesByDegree("zzz:", 5, DegreeOut, false); got != nil {
			t.Errorf("no-match prefix = %v, want nil", got)
		}
	})
}

// TestGraphCache_TopVerticesByDegree_LiveVisibility pins the #750 rule: an edge
// whose endpoint vertex is no longer live stops contributing to either
// endpoint's degree, and a dead vertex is not itself a candidate.
func TestGraphCache_TopVerticesByDegree_LiveVisibility(t *testing.T) {
	c := newDegreeGraph(t)

	// Baseline: n:a out-degree counts all three of its out-edges.
	if got := c.TopVerticesByDegree("n:", 1, DegreeOut, false); len(got) != 1 || got[0].Degree != 3 {
		t.Fatalf("baseline out top-1 = %+v, want n:a degree 3", got)
	}

	// Delete n:b: it is both a head of n:a and a tail into n:c. n:a's edge to
	// the now-dead head must stop counting (out-degree 3 -> 2), n:c's in-edge
	// from the dead tail must stop counting (in-degree 3 -> 2), and n:b itself
	// must drop out of the candidate set.
	c.DeleteVertex("n:b")

	out := c.TopVerticesByDegree("n:", 1, DegreeOut, false)
	if len(out) != 1 || out[0].Key != "n:a" || out[0].Degree != 2 {
		t.Errorf("after delete, out top-1 = %+v, want n:a degree 2", out)
	}
	in := c.TopVerticesByDegree("n:", 1, DegreeIn, false)
	if len(in) != 1 || in[0].Key != "n:c" || in[0].Degree != 2 {
		t.Errorf("after delete, in top-1 = %+v, want n:c degree 2", in)
	}
	for _, e := range c.TopVerticesByDegree("n:", 10, DegreeBoth, false) {
		if e.Key == "n:b" {
			t.Errorf("deleted vertex n:b is still a ranking candidate")
		}
	}
}

// TestGraphCache_TopVerticesByDegree_ConcurrentWrites guards the two-phase
// IN/BOTH walk (#920): degree accumulation runs outside c.mu, so it must be
// race-free against concurrent edge writes/deletes and always return a
// well-formed bounded result. On the pre-#920 single-lock implementation this
// test passes trivially (writers just stall); after #920 it is the -race guard
// for the unlocked accumulation phase, where the captured *weight pointers are
// read while a concurrent mutator may be adding or deleting the same buckets.
func TestGraphCache_TopVerticesByDegree_ConcurrentWrites(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	exp := time.Now().Add(time.Hour)
	for i := 0; i < 64; i++ {
		c.PutVertexWithExpiration("user:"+strconv.Itoa(i), "v", exp)
	}
	c.PutVertexWithExpiration("hub", "v", exp)

	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // writer: churn edges into the candidates
		defer wg.Done()
		for i := 0; !stop.Load(); i++ {
			head := "user:" + strconv.Itoa(i%64)
			c.AddEdgesWithExpiration([]EdgeItem[string]{{Tail: "hub", Head: head, Weight: 1, Expiration: exp}})
		}
	}()
	go func() { // deleter
		defer wg.Done()
		for i := 0; !stop.Load(); i++ {
			c.DeleteEdge("hub", "user:"+strconv.Itoa(i%64))
		}
	}()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, dir := range []DegreeDirection{DegreeOut, DegreeIn, DegreeBoth} {
			got := c.TopVerticesByDegree("user:", 10, dir, true)
			if len(got) > 10 {
				t.Errorf("dir %v: %d entries, want <= 10", dir, len(got))
			}
			for _, e := range got {
				if !strings.HasPrefix(e.Key, "user:") {
					t.Errorf("dir %v: entry %q escaped the prefix", dir, e.Key)
				}
			}
		}
	}
	stop.Store(true)
	wg.Wait()
}

// BenchmarkTopVerticesByDegree_InNarrowPrefix measures the IN walk (#920) over a
// wide graph whose bulk of edges do NOT touch the ranked prefix. It is the
// allocation-profile evidence for the two-phase refactor: the per-candidate
// value-typed accumulator drops the per-candidate pointer allocation, and only
// the candidate-incident buckets are captured across the unlocked phase (not the
// whole edge table). ns/op stays O(E) — every bucket is still visited under the
// lock to test candidacy — but that walk no longer holds c.mu during the
// weight snapshots. Record allocs/op before/after in the PR body.
func BenchmarkTopVerticesByDegree_InNarrowPrefix(b *testing.B) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	exp := time.Now().Add(time.Hour)
	// 100k edges NOT touching the ranked prefix + a 50-vertex target namespace.
	for i := 0; i < 100_000; i++ {
		c.AddEdgesWithExpiration([]EdgeItem[string]{{Tail: "noise:" + strconv.Itoa(i), Head: "sink", Weight: 1, Expiration: exp}})
	}
	for i := 0; i < 50; i++ {
		k := "user:" + strconv.Itoa(i)
		c.PutVertexWithExpiration(k, "v", exp)
		c.AddEdgesWithExpiration([]EdgeItem[string]{{Tail: "seed", Head: k, Weight: 1, Expiration: exp}})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.TopVerticesByDegree("user:", 10, DegreeIn, true)
	}
}
