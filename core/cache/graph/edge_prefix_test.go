package graph

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"
)

// scanEdgesCollect drains ScanEdgesByPrefix into a sorted []string of
// "tail->head" lines, ignoring weight/expiration. Sorting makes the
// assertions invariant to per-tail map iteration order (the cache
// guarantees per-tail head order and global tail order, but consumers
// rarely care about the exact head subordering when only checking
// membership).
func scanEdgesCollect(t *testing.T, c *GraphCache[string, string], tailPrefix, headPrefix string) []string {
	t.Helper()
	var got []string
	ok := c.ScanEdgesByPrefix(context.Background(), tailPrefix, headPrefix,
		func(tProj string, tail string, hProj string, head string, w float32, _ time.Time) bool {
			if tProj != tail {
				t.Errorf("tailProj %q != tail %q (identity extractor)", tProj, tail)
			}
			if hProj != head {
				t.Errorf("headProj %q != head %q (identity extractor)", hProj, head)
			}
			got = append(got, fmt.Sprintf("%s->%s=%g", tail, head, w))
			return true
		})
	if !ok {
		t.Fatalf("ScanEdgesByPrefix did not complete (prefix=%q/%q)", tailPrefix, headPrefix)
	}
	return got
}

func TestGraphCache_ScanEdgesByPrefix_Disabled(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.PutEdgeWithExpiration("a", "b", 1.0, time.Now().Add(time.Minute))
	called := false
	ok := c.ScanEdgesByPrefix(context.Background(), "", "",
		func(string, string, string, string, float32, time.Time) bool {
			called = true
			return true
		})
	if ok || called {
		t.Fatalf("disabled cache: ok=%v called=%v (want false,false)", ok, called)
	}
}

func TestGraphCache_ScanEdgesByPrefix_TailHeadIntersection(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	exp := time.Now().Add(time.Minute)
	edges := []struct{ t, h string }{
		{"user:1", "post:10"},
		{"user:1", "post:11"},
		{"user:1", "session:a"},
		{"user:2", "post:20"},
		{"user:2", "session:b"},
		{"admin:1", "post:99"},
	}
	for _, e := range edges {
		c.PutEdgeWithExpiration(e.t, e.h, 1.0, exp)
	}

	// tail-only filter
	got := scanEdgesCollect(t, c, "user:", "")
	want := []string{
		"user:1->post:10=1", "user:1->post:11=1", "user:1->session:a=1",
		"user:2->post:20=1", "user:2->session:b=1",
	}
	sort.Strings(got)
	sort.Strings(want)
	if !equalSlices(got, want) {
		t.Fatalf("tail-only: got %v want %v", got, want)
	}

	// head-only filter (no tail constraint)
	got = scanEdgesCollect(t, c, "", "post:")
	want = []string{
		"admin:1->post:99=1",
		"user:1->post:10=1", "user:1->post:11=1",
		"user:2->post:20=1",
	}
	sort.Strings(got)
	sort.Strings(want)
	if !equalSlices(got, want) {
		t.Fatalf("head-only: got %v want %v", got, want)
	}

	// intersection
	got = scanEdgesCollect(t, c, "user:", "post:")
	want = []string{
		"user:1->post:10=1", "user:1->post:11=1",
		"user:2->post:20=1",
	}
	sort.Strings(got)
	sort.Strings(want)
	if !equalSlices(got, want) {
		t.Fatalf("intersection: got %v want %v", got, want)
	}

	// both empty -> all edges
	got = scanEdgesCollect(t, c, "", "")
	if len(got) != len(edges) {
		t.Fatalf("both empty: got %d edges, want %d", len(got), len(edges))
	}
}

func TestGraphCache_ScanEdgesByPrefix_TailOrdering(t *testing.T) {
	// Per-tail head ordering must be ascending so that page-boundary
	// cursors can resume deterministically.
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	exp := time.Now().Add(time.Minute)
	for _, h := range []string{"d", "b", "a", "c"} {
		c.PutEdgeWithExpiration("t1", h, 1.0, exp)
	}
	var heads []string
	ok := c.ScanEdgesByPrefix(context.Background(), "t1", "",
		func(_ string, _ string, _ string, head string, _ float32, _ time.Time) bool {
			heads = append(heads, head)
			return true
		})
	if !ok {
		t.Fatal("did not complete")
	}
	want := []string{"a", "b", "c", "d"}
	if !equalSlices(heads, want) {
		t.Fatalf("head order: got %v want %v", heads, want)
	}
}

func TestGraphCache_ScanEdgesByPrefix_EarlyExitAndCancel(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	exp := time.Now().Add(time.Minute)
	for i := 0; i < 5; i++ {
		c.PutEdgeWithExpiration(fmt.Sprintf("t%d", i), "h", 1.0, exp)
	}
	visits := 0
	ok := c.ScanEdgesByPrefix(context.Background(), "t", "",
		func(string, string, string, string, float32, time.Time) bool {
			visits++
			return visits < 3
		})
	if ok {
		t.Fatal("expected ok=false on early exit")
	}
	if visits != 3 {
		t.Fatalf("visits = %d want 3", visits)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	ok = c.ScanEdgesByPrefix(ctx, "t", "",
		func(string, string, string, string, float32, time.Time) bool {
			called = true
			return true
		})
	if ok || called {
		t.Fatalf("cancelled: ok=%v called=%v (want false,false)", ok, called)
	}
}

func TestGraphCache_ScanEdgesByPrefix_ExpiredEdgeSkipped(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	// Long-lived endpoints so vertex liveness survives; short-lived edge.
	expVtx := time.Now().Add(time.Minute)
	c.PutVertexWithExpiration("t", "v", expVtx)
	c.PutVertexWithExpiration("h", "v", expVtx)
	c.PutEdgeWithExpiration("t", "h", 1.0, time.Now().Add(-time.Second))

	var got []string
	ok := c.ScanEdgesByPrefix(context.Background(), "", "",
		func(_ string, _ string, _ string, head string, _ float32, _ time.Time) bool {
			got = append(got, head)
			return true
		})
	if !ok {
		t.Fatal("did not complete")
	}
	if len(got) != 0 {
		t.Fatalf("expired edge surfaced: %v", got)
	}
}
