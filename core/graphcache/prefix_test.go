package graphcache

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
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

// TestGraphCache_DeleteByPrefix_ClearsSearchIndex verifies the batched
// DeleteByPrefix (#738) drops deleted vertices from the search index as well as
// the prefix index, leaving keys outside the prefix searchable. Content words
// are chosen to share no bigrams (the index uses an NGram{N:2} analyzer) so a
// match is unambiguous.
func TestGraphCache_DeleteByPrefix_ClearsSearchIndex(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	c.EnableSearchIndex(textExtract)
	c.PutVertex("doc:1", "alpha")
	c.PutVertex("doc:2", "bravo")
	c.PutVertex("keep:1", "zulu")

	if got := c.DeleteByPrefix(context.Background(), "doc:", 0); got != 2 {
		t.Fatalf("DeleteByPrefix(doc:) = %d, want 2", got)
	}

	// Deleted docs are gone from the search index...
	if got := c.SearchVertices("alpha", 10, ""); got != nil {
		t.Fatalf(`SearchVertices("alpha") after DeleteByPrefix = %v, want nil`, keys(got))
	}
	if got := c.SearchVertices("bravo", 10, ""); got != nil {
		t.Fatalf(`SearchVertices("bravo") after DeleteByPrefix = %v, want nil`, keys(got))
	}
	// ...but the surviving doc is still indexed.
	if got := keys(c.SearchVertices("zulu", 10, "")); !equalKeys(got, []string{"keep:1"}) {
		t.Fatalf(`SearchVertices("zulu") after DeleteByPrefix = %v, want [keep:1]`, got)
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

// TestGraphCache_PutVertices_PrefixStableAcrossExpiredOverwrite confirms the
// #739 physical-presence upsert keeps prefix membership correct (no leak, no
// double count) when a batch overwrites a vertex whose slot expired but was not
// yet flushed: the once-per-slot prefix insert is now keyed on physical
// presence, so the overwrite neither re-inserts nor drops the radix entry.
func TestGraphCache_PutVertices_PrefixStableAcrossExpiredOverwrite(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	// Seed an expired-but-not-flushed slot through the unconditional store path.
	c.mu.Lock()
	c.putVertexLocked("user:1", "v1", time.Now().Add(-time.Minute))
	c.mu.Unlock()
	// The slot is physically present in the radix but logically dead, so the
	// liveness-filtered count hides it (#752).
	if got := c.CountByPrefix("user:"); got != 0 {
		t.Fatalf("CountByPrefix after seeding an expired slot = %d, want 0", got)
	}
	c.PutVerticesWithExpiration([]VertexItem[string, string]{
		{Key: "user:1", Value: "v2", Expiration: time.Now().Add(time.Minute)},
	})
	// The live overwrite makes the single key visible again — exactly once, with
	// no stale double-count from the expired posting.
	if got := c.CountByPrefix("user:"); got != 1 {
		t.Fatalf("CountByPrefix after overwrite = %d, want 1", got)
	}
}

// TestGraphCache_CountByPrefix_EqualsLiveScan pins the #752 count/scan
// agreement invariant: for every prefix, CountByPrefix returns exactly the
// number of keys ScanByPrefix yields, after a mix of overwrite, delete,
// prefix-delete, and expired-but-not-flushed mutations. Both surfaces must
// observe the live logical set, not stale radix postings.
func TestGraphCache_CountByPrefix_EqualsLiveScan(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	c.EnablePrefixIndex(identityExtract)
	live := time.Now().Add(time.Hour)

	for _, k := range []string{"u:1", "u:2", "u:3", "u:4", "other:1"} {
		c.PutVertexWithExpiration(k, "v", live)
	}
	c.PutVertexWithExpiration("u:2", "v2", live)     // overwrite stays live
	c.DeleteVertex("u:3")                            // delete
	c.DeleteByPrefix(context.Background(), "u:4", 0) // prefix delete removes u:4
	// u:5 is expired-but-not-flushed: a stale radix posting that neither
	// surface may observe.
	c.mu.Lock()
	c.putVertexLocked("u:5", "v", time.Now().Add(-time.Minute))
	c.mu.Unlock()

	for _, prefix := range []string{"", "u:", "u:1", "other:", "zzz"} {
		var scanned []string
		c.ScanByPrefix(context.Background(), prefix, func(_, key string, _ string) bool {
			scanned = append(scanned, key)
			return true
		})
		if got := c.CountByPrefix(prefix); got != len(scanned) {
			t.Errorf("CountByPrefix(%q) = %d, ScanByPrefix len = %d (%v)", prefix, got, len(scanned), scanned)
		}
	}
	// Concrete check: the live u: set is exactly {u:1, u:2}.
	if got := c.CountByPrefix("u:"); got != 2 {
		t.Errorf("CountByPrefix(u:) = %d, want 2 (u:1, u:2)", got)
	}
}

// TestScanByPrefixPage property-checks the paged scan (#836) against the
// unpaged walk: for random keyspaces and every page size, stitching pages
// together via (after, limit) must reproduce the unpaged sequence exactly —
// no duplicates, no gaps — with `more` true on every page except the last.
func TestScanByPrefixPage(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	c.EnablePrefixIndex(func(s string) string { return s })
	exp := time.Now().Add(time.Hour)
	rng := rand.New(rand.NewSource(836))
	for i := 0; i < 200; i++ {
		c.PutVertexWithExpiration(fmt.Sprintf("ns%d:key%04d", rng.Intn(3), rng.Intn(500)), "v", exp)
	}
	// A sprinkle of expired-but-unflushed entries that every page must skip.
	for i := 0; i < 20; i++ {
		c.PutVertexWithExpiration(fmt.Sprintf("ns1:dead%03d", i), "v", time.Now().Add(-time.Minute))
	}

	for _, prefix := range []string{"", "ns1:", "ns1:key02", "missing:"} {
		var want []string
		if !c.ScanByPrefix(context.Background(), prefix, func(_ string, key string, _ string) bool {
			want = append(want, key)
			return true
		}) {
			t.Fatalf("unpaged scan reported early stop")
		}

		for _, limit := range []int{1, 3, 7, len(want) + 5} {
			var got []string
			after := ""
			for page := 0; ; page++ {
				var pageKeys []string
				more, ok := c.ScanByPrefixPage(context.Background(), prefix, after, limit, false, func(_ string, key string, _ string) bool {
					pageKeys = append(pageKeys, key)
					return true
				})
				if !ok {
					t.Fatalf("prefix=%q limit=%d page=%d not ok", prefix, limit, page)
				}
				if len(pageKeys) > limit {
					t.Fatalf("prefix=%q limit=%d page=%d overflowed: %d rows", prefix, limit, page, len(pageKeys))
				}
				got = append(got, pageKeys...)
				if !more {
					break
				}
				if len(pageKeys) == 0 {
					t.Fatalf("prefix=%q limit=%d: more=true with empty page", prefix, limit)
				}
				after = pageKeys[len(pageKeys)-1]
				if page > len(want)+2 {
					t.Fatalf("prefix=%q limit=%d: pagination did not terminate", prefix, limit)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("prefix=%q limit=%d:\n got  %v\n want %v", prefix, limit, got, want)
			}
		}
	}
}

// TestScanByPrefixPageDesc exercises descending-order paged scans (#898):
// every page walks the prefix range from the high end, cursors resume
// strictly below the previous page's smallest key, and the concatenated
// result equals the ascending set reversed — independent of page size.
func TestScanByPrefixPageDesc(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	c.EnablePrefixIndex(func(s string) string { return s })
	exp := time.Now().Add(time.Hour)
	rng := rand.New(rand.NewSource(898))
	for i := 0; i < 200; i++ {
		c.PutVertexWithExpiration(fmt.Sprintf("ns%d:key%04d", rng.Intn(3), rng.Intn(500)), "v", exp)
	}
	// Expired-but-unflushed entries every page must skip, same as ascending.
	for i := 0; i < 20; i++ {
		c.PutVertexWithExpiration(fmt.Sprintf("ns1:dead%03d", i), "v", time.Now().Add(-time.Minute))
	}

	for _, prefix := range []string{"", "ns1:", "ns1:key02", "missing:"} {
		// Ground truth: ascending set, then reversed.
		var asc []string
		if !c.ScanByPrefix(context.Background(), prefix, func(_ string, key string, _ string) bool {
			asc = append(asc, key)
			return true
		}) {
			t.Fatalf("unpaged scan reported early stop")
		}
		want := make([]string, 0, len(asc))
		for i := len(asc) - 1; i >= 0; i-- {
			want = append(want, asc[i])
		}
		if len(want) == 0 {
			want = nil
		}

		for _, limit := range []int{1, 3, 7, len(want) + 5} {
			var got []string
			after := ""
			for page := 0; ; page++ {
				var pageKeys []string
				more, ok := c.ScanByPrefixPage(context.Background(), prefix, after, limit, true, func(_ string, key string, _ string) bool {
					pageKeys = append(pageKeys, key)
					return true
				})
				if !ok {
					t.Fatalf("prefix=%q limit=%d page=%d not ok", prefix, limit, page)
				}
				if len(pageKeys) > limit {
					t.Fatalf("prefix=%q limit=%d page=%d overflowed: %d rows", prefix, limit, page, len(pageKeys))
				}
				// Each page must itself be descending.
				if !sort.SliceIsSorted(pageKeys, func(i, j int) bool { return pageKeys[i] > pageKeys[j] }) {
					t.Fatalf("prefix=%q limit=%d page=%d not descending: %v", prefix, limit, page, pageKeys)
				}
				got = append(got, pageKeys...)
				if !more {
					break
				}
				if len(pageKeys) == 0 {
					t.Fatalf("prefix=%q limit=%d: more=true with empty page", prefix, limit)
				}
				after = pageKeys[len(pageKeys)-1]
				if page > len(want)+2 {
					t.Fatalf("prefix=%q limit=%d: pagination did not terminate", prefix, limit)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("prefix=%q limit=%d descending:\n got  %v\n want %v", prefix, limit, got, want)
			}
		}
	}
}
