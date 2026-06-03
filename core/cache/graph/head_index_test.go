package graph

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestScanEdgesByPrefix_HeadIndexMatchesFallback asserts that the fast
// (per-tail head radix) path emits exactly the same (tailProj, headProj)
// set as the v1 materialise-and-sort fallback for any given graph state.
//
// We populate a small graph through the normal write surface (so both
// the head index and the heads map agree), then scan it twice — once
// with the index enabled and once after disableHeadIndexForTesting —
// and assert the captured tuples are identical.
func TestScanEdgesByPrefix_HeadIndexMatchesFallback(t *testing.T) {
	build := func() *GraphCache[string, string] {
		c := NewGraphCache[string, string](time.Minute)
		c.EnablePrefixIndex(identityExtract)
		exp := time.Now().Add(time.Minute)
		tails := []string{"user:001", "user:002", "user:003", "post:001"}
		heads := []string{"post:001", "post:002", "post:003", "user:001", "tag:x"}
		for _, t1 := range tails {
			for _, h := range heads {
				if t1 == h {
					continue
				}
				c.AddEdgeWithExpiration(t1, h, 1, exp)
			}
		}
		c.PutEdgeWithExpiration("user:001", "post:002", 3, exp)
		c.DeleteEdge("user:002", "tag:x")
		return c
	}

	cases := []struct {
		name         string
		tailP, headP string
	}{
		{"all", "", ""},
		{"narrow head", "user:", "post:00"},
		{"narrow tail", "user:001", "post:"},
		{"no match", "user:001", "zzz"},
		{"head exact", "", "tag:x"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastC := build()
			fast := collectScan(fastC, tc.tailP, tc.headP)
			slowC := build()
			slowC.disableHeadIndexForTesting()
			slow := collectScan(slowC, tc.tailP, tc.headP)
			if len(fast) != len(slow) {
				t.Fatalf("size mismatch: fast=%d slow=%d\nfast=%v\nslow=%v", len(fast), len(slow), fast, slow)
			}
			for i := range fast {
				if fast[i] != slow[i] {
					t.Fatalf("entry %d mismatch: fast=%q slow=%q", i, fast[i], slow[i])
				}
			}
		})
	}
}

// TestHeadIndex_LifecycleOnPutDelete asserts that after put-then-delete
// the per-tail bucket is removed entirely so we do not leak empty
// headIndex objects.
func TestHeadIndex_LifecycleOnPutDelete(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	exp := time.Now().Add(time.Minute)

	c.PutEdgeWithExpiration("u:1", "p:1", 1, exp)
	c.mu.RLock()
	tailID, ok := c.edges.dict.lookup("u:1")
	c.mu.RUnlock()
	if !ok {
		t.Fatalf("expected tail to be interned")
	}
	if c.headByTail[tailID] == nil {
		t.Fatalf("expected head index for u:1 after put")
	}

	if !c.DeleteEdge("u:1", "p:1") {
		t.Fatalf("delete reported nothing removed")
	}
	if _, present := c.headByTail[tailID]; present {
		t.Fatalf("expected per-tail head index to be dropped after last edge removed")
	}
}

func collectScan(c *GraphCache[string, string], tailP, headP string) []string {
	var out []string
	c.ScanEdgesByPrefix(context.Background(), tailP, headP, func(tp string, _ string, hp string, _ string, _ float32, _ time.Time) bool {
		out = append(out, tp+"|"+hp)
		return true
	})
	return out
}

// BenchmarkScanEdgesByPrefix_StarHead exercises the canonical Issue #167
// shape: a single hub tail with millions of heads, where head_prefix
// matches a small slice. The fast (head-index) path walks only that
// slice in the radix; the fallback materialises and sorts every head.
//
// Run with:
//
//	go test -bench BenchmarkScanEdgesByPrefix_StarHead -run ^$ ./cache/graph/...
//
// On a 1-tail / 1M-head graph with a ~0.1% match the fast path should
// be at least an order of magnitude faster than the fallback.
func BenchmarkScanEdgesByPrefix_StarHead(b *testing.B) {
	const n = 100_000 // CI-friendly; bump to 1_000_000 locally for the headline number
	c := NewGraphCache[string, string](time.Hour)
	c.EnablePrefixIndex(identityExtract)
	exp := time.Now().Add(time.Hour)
	for i := 0; i < n; i++ {
		c.AddEdgeWithExpiration("hub", fmt.Sprintf("post:%07d", i), 1, exp)
	}
	// Match the first 0.1% of heads.
	headPrefix := "post:00000" // matches 100 / 100_000 = 0.1%

	b.Run("HeadIndex", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var hits int
			c.ScanEdgesByPrefix(context.Background(), "", headPrefix, func(_, _, hp, _ string, _ float32, _ time.Time) bool {
				if !strings.HasPrefix(hp, headPrefix) {
					b.Fatalf("unexpected head %q", hp)
				}
				hits++
				return true
			})
			if hits == 0 {
				b.Fatalf("expected matches")
			}
		}
	})

	b.Run("Fallback", func(b *testing.B) {
		c.disableHeadIndexForTesting()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var hits int
			c.ScanEdgesByPrefix(context.Background(), "", headPrefix, func(_, _, hp, _ string, _ float32, _ time.Time) bool {
				if !strings.HasPrefix(hp, headPrefix) {
					b.Fatalf("unexpected head %q", hp)
				}
				hits++
				return true
			})
			if hits == 0 {
				b.Fatalf("expected matches")
			}
		}
	})
}
