package graphcache

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

// identityExtract is the projection used by the production string-keyed
// instantiation (S = string). The tests pin both halves of the contract.
func identityExtract(s string) string { return s }

func TestGraphCache_PrefixDisabledByDefault(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.PutVertex("foo", "v")
	// Without EnablePrefixIndex, all prefix methods are inert.
	if got := c.CountByPrefix("f"); got != 0 {
		t.Fatalf("CountByPrefix without enable: got %d want 0", got)
	}
	called := false
	ok := c.ScanByPrefix(context.Background(), "f", func(string, string, string) bool {
		called = true
		return true
	})
	if ok || called {
		t.Fatalf("ScanByPrefix without enable: ok=%v called=%v (want false, false)", ok, called)
	}
	if got := c.DeleteByPrefix(context.Background(), "f", 0); got != 0 {
		t.Fatalf("DeleteByPrefix without enable: got %d want 0", got)
	}
}

func TestGraphCache_EnablePrefixIndex_AfterPutPanics(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.PutVertex("k", "v")
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when enabling index on non-empty cache")
		}
	}()
	c.EnablePrefixIndex(identityExtract)
}

func TestGraphCache_EnablePrefixIndex_NilExtractPanics(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when extract is nil")
		}
	}()
	c.EnablePrefixIndex(nil)
}

func TestGraphCache_EnablePrefixIndex_Idempotent(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	c.EnablePrefixIndex(identityExtract) // must not panic
}

func TestGraphCache_ScanByPrefix_Basic(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	for _, k := range []string{"user:1", "user:2", "user:3", "session:a", "session:b"} {
		c.PutVertex(k, "v-"+k)
	}
	var got []string
	completed := c.ScanByPrefix(context.Background(), "user:", func(projected, key, value string) bool {
		if projected != key {
			t.Fatalf("projected (%q) != key (%q) for identity extractor", projected, key)
		}
		if value != "v-"+key {
			t.Fatalf("unexpected value for %q: %q", key, value)
		}
		got = append(got, key)
		return true
	})
	if !completed {
		t.Fatal("ScanByPrefix did not complete")
	}
	want := []string{"user:1", "user:2", "user:3"}
	sort.Strings(got)
	if !equalSlices(got, want) {
		t.Fatalf("ScanByPrefix: got %v want %v", got, want)
	}
}

func TestGraphCache_ScanByPrefix_EarlyExitAndCancel(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	for i := 0; i < 10; i++ {
		c.PutVertex(fmt.Sprintf("k%02d", i), "v")
	}
	// Early exit via fn.
	visits := 0
	completed := c.ScanByPrefix(context.Background(), "k", func(string, string, string) bool {
		visits++
		return visits < 3
	})
	if completed {
		t.Fatal("expected completed=false when fn requests early exit")
	}
	if visits != 3 {
		t.Fatalf("visits: got %d want 3", visits)
	}
	// Cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	visits = 0
	completed = c.ScanByPrefix(ctx, "k", func(string, string, string) bool {
		visits++
		return true
	})
	if completed {
		t.Fatal("expected completed=false on cancelled context")
	}
	if visits != 0 {
		t.Fatalf("visits on cancelled ctx: got %d want 0", visits)
	}
}

func TestGraphCache_CountByPrefix(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	for _, k := range []string{"a", "ab", "abc", "abd", "b"} {
		c.PutVertex(k, "v")
	}
	cases := map[string]int{"": 5, "a": 4, "ab": 3, "abc": 1, "x": 0}
	for prefix, want := range cases {
		if got := c.CountByPrefix(prefix); got != want {
			t.Errorf("CountByPrefix(%q): got %d want %d", prefix, got, want)
		}
	}
}

func TestGraphCache_DeleteByPrefix(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	for _, k := range []string{"tmp:1", "tmp:2", "tmp:3", "keep:1"} {
		c.PutVertex(k, "v")
	}
	deleted := c.DeleteByPrefix(context.Background(), "tmp:", 0)
	if deleted != 3 {
		t.Fatalf("DeleteByPrefix: got %d want 3", deleted)
	}
	if c.CountByPrefix("tmp:") != 0 {
		t.Fatal("CountByPrefix after delete: expected 0")
	}
	if _, ok := c.GetVertex("keep:1"); !ok {
		t.Fatal("keep:1 should still be present")
	}
	// Verify the underlying vertex cache is also emptied of those keys.
	for _, k := range []string{"tmp:1", "tmp:2", "tmp:3"} {
		if _, ok := c.GetVertex(k); ok {
			t.Errorf("vertex %q still present after DeleteByPrefix", k)
		}
	}
}

func TestGraphCache_DeleteByPrefix_LimitRespected(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	for i := 0; i < 5; i++ {
		c.PutVertex(fmt.Sprintf("p:%d", i), "v")
	}
	if got := c.DeleteByPrefix(context.Background(), "p:", 2); got != 2 {
		t.Fatalf("DeleteByPrefix limit=2: got %d want 2", got)
	}
	if got := c.CountByPrefix("p:"); got != 3 {
		t.Fatalf("remaining count: got %d want 3", got)
	}
}

func TestGraphCache_PrefixIndex_DroppedOnTTLExpiry(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	c.PutVertexWithTTL("expire:soon", "v", 10*time.Millisecond)
	c.PutVertexWithTTL("keep", "v", time.Minute)

	// Wait past expiry, then trigger flush by invoking the inner cache.
	time.Sleep(30 * time.Millisecond)
	c.flush() // GraphCache.flush sweeps edges; vertices flush via inner cache.
	// The inner vertex cache flush is what fires OnEvict. Trigger it:
	c.vertices.Flush()

	if got := c.CountByPrefix("expire:"); got != 0 {
		t.Fatalf("prefix count after expiry: got %d want 0", got)
	}
	if got := c.CountByPrefix("keep"); got != 1 {
		t.Fatalf("non-expired prefix count: got %d want 1", got)
	}
}

func TestGraphCache_PrefixIndex_DroppedOnDeleteVertex(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	c.PutVertex("ns:a", "v")
	c.PutVertex("ns:b", "v")
	if !c.DeleteVertex("ns:a") {
		t.Fatal("DeleteVertex returned false")
	}
	if c.CountByPrefix("ns:") != 1 {
		t.Fatalf("count after DeleteVertex: got %d want 1", c.CountByPrefix("ns:"))
	}
}

func TestGraphCache_PrefixIndex_NoDoubleInsertOnRefresh(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	c.PutVertex("k", "v1")
	c.PutVertex("k", "v2") // refresh
	if c.CountByPrefix("") != 1 {
		t.Fatalf("count after refresh: got %d want 1", c.CountByPrefix(""))
	}
}

func TestGraphCache_PrefixIndex_PicksUpEdgeAutoCreate(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	// AddEdge auto-creates both endpoint vertices via ensureVertexLocked.
	// The prefix index must observe both.
	c.AddEdge("a:tail", "a:head", 1)
	if got := c.CountByPrefix("a:"); got != 2 {
		t.Fatalf("CountByPrefix after AddEdge: got %d want 2", got)
	}
}

// ACID gate: concurrent puts + concurrent prefix scans must always observe
// a consistent snapshot \u2014 no key from outside the prefix, no torn read.
func TestGraphCache_PrefixIndex_ConcurrentScanIsConsistent(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)

	const writers = 4
	const perWriter = 200
	var wg sync.WaitGroup
	wg.Add(writers)
	for w := 0; w < writers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				c.PutVertex(fmt.Sprintf("ns:%d:%d", w, i), "v")
			}
		}(w)
	}

	// Run scans concurrently with writes; assert every scanned key has
	// the requested prefix and is currently live in the vertex cache.
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		for i := 0; i < 50; i++ {
			c.ScanByPrefix(context.Background(), "ns:", func(_, key, _ string) bool {
				if len(key) < 3 || key[:3] != "ns:" {
					t.Errorf("scan yielded key without prefix: %q", key)
					return false
				}
				if _, ok := c.GetVertex(key); !ok {
					// Allowed: the key may have been deleted between
					// scan emission and this re-read. But our test
					// has no deletes, so this would be a bug.
					t.Errorf("scan yielded key not in cache: %q", key)
					return false
				}
				return true
			})
		}
	}()

	wg.Wait()
	<-scanDone

	if got, want := c.CountByPrefix("ns:"), writers*perWriter; got != want {
		t.Fatalf("final count: got %d want %d", got, want)
	}
}

// BenchmarkPrefixScan measures the cost of ScanByPrefix and CountByPrefix
// at increasing cache sizes. It is the headline number for the prefix
// index Phase 1 work: every later optimization PR must keep these
// numbers from regressing.
//
// Scales: 10k / 100k / 1M live keys. The 1M case takes ~5 s to populate
// on a modern laptop and is skipped under -short.
func BenchmarkPrefixScan(b *testing.B) {
	scales := []int{10_000, 100_000}
	if !testing.Short() {
		scales = append(scales, 1_000_000)
	}
	for _, n := range scales {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			c := NewGraphCache[string, string](time.Hour)
			c.EnablePrefixIndex(identityExtract)
			// Keys are namespaced "tenant:%04d:key:%010d" so a typical
			// prefix yields ~1/1000 of the population \u2014 representative
			// of multi-tenant production patterns.
			for i := 0; i < n; i++ {
				key := fmt.Sprintf("tenant:%04d:key:%010d", i%1000, i)
				c.PutVertex(key, "v")
			}
			ctx := context.Background()
			prefix := "tenant:0042:"

			b.Run("Count", func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = c.CountByPrefix(prefix)
				}
			})
			b.Run("Scan", func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					c.ScanByPrefix(ctx, prefix, func(string, string, string) bool {
						return true
					})
				}
			})
			b.Run("ScanAll", func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					c.ScanByPrefix(ctx, "", func(string, string, string) bool {
						return true
					})
				}
			})
		})
	}
}
