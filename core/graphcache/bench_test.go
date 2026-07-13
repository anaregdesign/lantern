package graphcache

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

// makeKey returns a string of length n. It mimics a normalized URL by
// prefixing with a fixed scheme and padding the rest with the deterministic
// index so each key is unique and the byte content cannot be deduplicated by
// chance.
func makeKey(prefix string, idx int, length int) string {
	tail := fmt.Sprintf("/%010d", idx)
	if len(prefix)+len(tail) >= length {
		return (prefix + tail)[:length]
	}
	var b strings.Builder
	b.Grow(length)
	b.WriteString(prefix)
	b.WriteString(tail)
	for b.Len() < length {
		b.WriteByte('a' + byte(idx%26))
	}
	return b.String()[:length]
}

// benchScale lets a single benchmark function cover several workload sizes
// while staying small enough to run in CI when -short is passed.
type benchScale struct {
	name     string
	vertices int
	degree   int
	keyLen   int
}

func smallScales(short bool) []benchScale {
	if short {
		return []benchScale{
			{name: "v1k_d8_k80", vertices: 1_000, degree: 8, keyLen: 80},
		}
	}
	return []benchScale{
		{name: "v1k_d8_k80", vertices: 1_000, degree: 8, keyLen: 80},
		{name: "v10k_d16_k80", vertices: 10_000, degree: 16, keyLen: 80},
		{name: "v100k_d32_k80", vertices: 100_000, degree: 32, keyLen: 80},
	}
}

// populate fills the cache with `s.vertices` vertices and `s.vertices*s.degree`
// edges. Heads are picked deterministically (round-robin offset) so the graph
// is dense enough to exercise the inner edge map without becoming pathological.
func populate(b *testing.B, c *GraphCache[string, string], s benchScale) []string {
	b.Helper()
	keys := make([]string, s.vertices)
	for i := 0; i < s.vertices; i++ {
		keys[i] = makeKey("https://example.com", i, s.keyLen)
	}
	expiration := time.Now().Add(time.Hour)
	vs := make([]VertexItem[string, string], s.vertices)
	for i, k := range keys {
		vs[i] = VertexItem[string, string]{Key: k, Value: "", Expiration: expiration}
	}
	c.PutVerticesWithExpiration(vs)

	es := make([]EdgeItem[string], 0, s.vertices*s.degree)
	for i := 0; i < s.vertices; i++ {
		for j := 1; j <= s.degree; j++ {
			head := keys[(i+j)%s.vertices]
			es = append(es, EdgeItem[string]{
				Tail:       keys[i],
				Head:       head,
				Weight:     1,
				Expiration: expiration,
			})
		}
	}
	c.AddEdgesWithExpiration(es)
	return keys
}

// reportHeap captures the heap-in-use delta caused by populating the cache.
// It is the single most important number this file produces: it is what
// every subsequent optimization PR must move downward.
func reportHeap(b *testing.B, before, after runtime.MemStats, scale benchScale) {
	b.Helper()
	heapDelta := after.HeapInuse - before.HeapInuse
	allocDelta := after.TotalAlloc - before.TotalAlloc
	edges := scale.vertices * scale.degree
	b.ReportMetric(float64(heapDelta), "heap_inuse_B")
	if edges > 0 {
		b.ReportMetric(float64(heapDelta)/float64(edges), "heap_inuse_B/edge")
	}
	if scale.vertices > 0 {
		b.ReportMetric(float64(heapDelta)/float64(scale.vertices), "heap_inuse_B/vertex")
	}
	b.ReportMetric(float64(allocDelta), "alloc_B")
}

// BenchmarkGraphCacheMemory reports heap-in-use after populating a cache at
// several scales. Run with:
//
//	go test -bench=BenchmarkGraphCacheMemory -benchmem -benchtime=1x ./core/graphcache
//
// The b.N loop iteration count is forced to 1 by -benchtime=1x so the heap
// snapshot reflects exactly one populated cache. Without -benchtime=1x the
// reported numbers still scale linearly with b.N — divide accordingly.
func BenchmarkGraphCacheMemory(b *testing.B) {
	for _, s := range smallScales(testing.Short()) {
		s := s
		b.Run(s.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				var before, after runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&before)
				c := NewGraphCache[string, string](time.Hour)
				populate(b, c, s)
				runtime.GC()
				runtime.ReadMemStats(&after)
				reportHeap(b, before, after, s)
				runtime.KeepAlive(c)
			}
		})
	}
}

// BenchmarkGraphCacheAddEdges measures throughput of the additive edge
// path, which is the hot ingest path. Allocations per op are the headline
// metric here.
func BenchmarkGraphCacheAddEdges(b *testing.B) {
	for _, s := range smallScales(testing.Short()) {
		s := s
		b.Run(s.name, func(b *testing.B) {
			c := NewGraphCache[string, string](time.Hour)
			keys := make([]string, s.vertices)
			for i := range keys {
				keys[i] = makeKey("https://example.com", i, s.keyLen)
			}
			expiration := time.Now().Add(time.Hour)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tail := keys[i%s.vertices]
				head := keys[(i+1)%s.vertices]
				c.AddEdgeWithExpiration(tail, head, 1, expiration)
			}
		})
	}
}

// BenchmarkGraphCacheGetWeight measures the lookup hot path.
func BenchmarkGraphCacheGetWeight(b *testing.B) {
	for _, s := range smallScales(testing.Short()) {
		s := s
		b.Run(s.name, func(b *testing.B) {
			c := NewGraphCache[string, string](time.Hour)
			keys := populate(b, c, s)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tail := keys[i%s.vertices]
				head := keys[(i+1)%s.vertices]
				_, _ = c.GetWeight(tail, head)
			}
		})
	}
}

// BenchmarkGetVertex_Contended measures the GetVertex point read while a
// background writer churns unrelated keys. Before #740 the read serialized
// behind the writer on GraphCache.mu; afterwards it only contends on the inner
// vertex-cache lock. Run before/after to capture the contention win.
func BenchmarkGetVertex_Contended(b *testing.B) {
	for _, s := range smallScales(testing.Short()) {
		s := s
		b.Run(s.name, func(b *testing.B) {
			c := NewGraphCache[string, string](time.Hour)
			keys := populate(b, c, s)
			exp := time.Now().Add(time.Hour)
			var stop atomic.Bool
			done := make(chan struct{})
			go func() {
				defer close(done)
				i := 0
				for !stop.Load() {
					// Write churn on a disjoint key namespace so reads always hit.
					wk := makeKey("writer", i%s.vertices, s.keyLen)
					c.PutVertexWithExpiration(wk, "", exp)
					c.DeleteVertex(wk)
					i++
				}
			}()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = c.GetVertex(keys[i%s.vertices])
			}
			b.StopTimer()
			stop.Store(true)
			<-done
		})
	}
}

// BenchmarkGetEdgeDetail_Contended measures the GetEdgeDetail point read on a
// hot existing edge while a background writer adds unrelated edges. It captures
// the #740 win of dropping GraphCache.mu from edge point reads (pinBoth closes
// the dictionary ABA hazard instead).
func BenchmarkGetEdgeDetail_Contended(b *testing.B) {
	for _, s := range smallScales(testing.Short()) {
		s := s
		b.Run(s.name, func(b *testing.B) {
			c := NewGraphCache[string, string](time.Hour)
			keys := populate(b, c, s)
			exp := time.Now().Add(time.Hour)
			var stop atomic.Bool
			done := make(chan struct{})
			go func() {
				defer close(done)
				i := 0
				for !stop.Load() {
					tail := keys[i%s.vertices]
					head := keys[(i+2)%s.vertices]
					c.AddEdgeWithExpiration(tail, head, 1, exp)
					i++
				}
			}()
			hotTail, hotHead := keys[0], keys[1%s.vertices]
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _, _ = c.GetEdgeDetail(hotTail, hotHead)
			}
			b.StopTimer()
			stop.Store(true)
			<-done
		})
	}
}

// BenchmarkNeighbor_Small exercises the sequential path in neighborContext:
// step=1 keeps the frontier at exactly 1 (the seed), well below
// neighborParallelThreshold, so goroutine fan-out is skipped entirely.
// This is the case the threshold gate is meant to protect.
func BenchmarkNeighbor_Small(b *testing.B) {
	for _, s := range smallScales(testing.Short()) {
		s := s
		b.Run(s.name, func(b *testing.B) {
			c := NewGraphCache[string, string](time.Hour)
			keys := populate(b, c, s)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = c.Neighbor(keys[i%s.vertices], 1, 10, WeightingRaw, false, nil)
			}
		})
	}
}

// BenchmarkNeighbor_Large exercises the worker-pool path: step=3 lets the
// frontier grow past the threshold so fan-out kicks in. Should show no
// regression vs the prior per-tail-goroutine implementation.
func BenchmarkNeighbor_Large(b *testing.B) {
	for _, s := range smallScales(testing.Short()) {
		s := s
		b.Run(s.name, func(b *testing.B) {
			c := NewGraphCache[string, string](time.Hour)
			keys := populate(b, c, s)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = c.Neighbor(keys[i%s.vertices], 3, 10, WeightingRaw, false, nil)
			}
		})
	}
}

// BenchmarkGCFlush exercises the fused GC sweep (zero-weight + dangling) in a
// single walk over the edge map. Populates the graph, deletes a quarter of
// the vertices to create dangling edges, then runs c.flush(). Should show
// no O(E) snapshot allocation per tick.
func BenchmarkGCFlush(b *testing.B) {
	for _, s := range smallScales(testing.Short()) {
		s := s
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				c := NewGraphCache[string, string](time.Hour)
				keys := populate(b, c, s)
				for j := 0; j < s.vertices; j += 4 {
					c.DeleteVertex(keys[j])
				}
				b.StartTimer()
				_, _ = c.flush()
			}
		})
	}
}

// BenchmarkGCFlushIncremental measures the worst per-tick pause of the bounded
// edge sweep (#744): it times a single flush() that builds a fresh sweep plan
// (the tailIDs snapshot) and processes one budget's worth of tails. Contrast
// its ns/op with BenchmarkGCFlush — the unbounded O(E) sweep — to see the pause
// the budget trades away for more ticks. The budget scales with the graph
// (1/16 of the tail count) so the bound tracks graph size.
func BenchmarkGCFlushIncremental(b *testing.B) {
	for _, s := range smallScales(testing.Short()) {
		s := s
		budget := s.vertices / 16
		if budget < 1 {
			budget = 1
		}
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				c := NewGraphCache[string, string](time.Hour)
				keys := populate(b, c, s)
				for j := 0; j < s.vertices; j += 4 {
					c.DeleteVertex(keys[j])
				}
				c.SetGCEdgeBudget(budget)
				b.StartTimer()
				_, _ = c.flush() // first bounded tick: plan build + one batch
			}
		})
	}
}

// targets a single weight with already-expired entries, forcing flushLocked
// to fire at the trigger boundary. Cost must stay roughly O(1) per op rather
// than blowing up as the slice grows.
func BenchmarkWeight_AddOnly(b *testing.B) {
	w := newWeight()
	past := time.Now().Add(-time.Hour)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.addWithExpiration(1, past)
	}
}

// BenchmarkAddEdge_Existing measures the existing-edge fast path taken by
// AddEdgeWithExpiration (#743). After warming up a single edge, every
// iteration hits the lock-free path: a brief dict write-lock to pin both
// endpoint ids (refcount++), two vertex liveness checks, one edgeCache
// RLock to find the *weight, then the leaf weight.mu append. The giant
// GraphCache.mu is never taken, so concurrent writers to the same hot edge
// no longer serialize on it (see BenchmarkAddEdge_ExistingParallel).
func BenchmarkAddEdge_Existing(b *testing.B) {
	c := NewGraphCache[string, int](time.Minute)
	exp := time.Now().Add(time.Hour)
	c.AddEdgeWithExpiration("hot-tail", "hot-head", 1, exp)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.AddEdgeWithExpiration("hot-tail", "hot-head", 1, exp)
	}
}

// BenchmarkAddEdge_ExistingParallel is the headline #743 measurement: many
// goroutines additively writing the SAME already-present edge. Pre-#743 every
// writer serialized on GraphCache.mu.Lock; the fast path drops that to a tiny
// dict critical section plus the per-edge weight lock.
func BenchmarkAddEdge_ExistingParallel(b *testing.B) {
	c := NewGraphCache[string, int](time.Minute)
	exp := time.Now().Add(time.Hour)
	c.AddEdgeWithExpiration("hot-tail", "hot-head", 1, exp)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.AddEdgeWithExpiration("hot-tail", "hot-head", 1, exp)
		}
	})
}

// BenchmarkAddEdges_ExistingBatch measures the batch fast path over a fan of
// already-present edges. Every item resolves through tryAddExistingEdgeContrib
// so the whole batch avoids GraphCache.mu entirely.
func BenchmarkAddEdges_ExistingBatch(b *testing.B) {
	const fan = 64
	c := NewGraphCache[string, int](time.Minute)
	exp := time.Now().Add(time.Hour)
	batch := make([]EdgeItem[string], fan)
	for i := 0; i < fan; i++ {
		batch[i] = EdgeItem[string]{Tail: "s", Head: fmt.Sprintf("h%d", i), Weight: 1, Expiration: exp}
	}
	c.AddEdgesWithExpiration(batch) // warm: create all buckets
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.AddEdgesWithExpiration(batch)
	}
}

// BenchmarkAddEdge_ExistingWithReaders is the workload #743 actually targets:
// read throughput on a hot edge while a background goroutine continuously
// writes it. Pre-#743 each write took GraphCache.mu.Lock and stalled every
// reader's GraphCache.mu.RLock; the existing-edge fast path takes no
// aggregate lock, so the GetWeight readers run concurrently with the writer.
//
// The background writer reuses a single ContribID so each post-first write is
// a dedup no-op at the weight layer: it still pays the full fast-path locking
// (pin both endpoints, two liveness checks, the edgeCache read lock, the
// per-edge weight lock) but never grows the weight slice, isolating lock
// contention from O(N) weight-flush cost.
func BenchmarkAddEdge_ExistingWithReaders(b *testing.B) {
	c := NewGraphCache[string, int](time.Minute)
	exp := time.Now().Add(time.Hour)
	var id ContribID
	id[0] = 0x9E
	c.AddEdgeWithExpirationContrib("hot-tail", "hot-head", 1, exp, id)

	stop := make(chan struct{})
	var writers sync.WaitGroup
	writers.Add(1)
	go func() {
		defer writers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				c.AddEdgeWithExpirationContrib("hot-tail", "hot-head", 1, exp, id)
			}
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.GetWeight("hot-tail", "hot-head")
		}
	})
	b.StopTimer()
	close(stop)
	writers.Wait()
}

// benchSearchCorpus is a small pool of realistic content strings the put
// benchmark cycles through so the analyzer (normalize → bigram → filter) does
// representative work on every indexed write rather than re-tokenizing one
// cached string.
var benchSearchCorpus = []string{
	"lantern graph database memory",
	"connect http2 grpc gateway",
	"vertex edge ttl decay model",
	"inverted index bm25 ranking",
	"namespace prefix scan traversal",
}

// BenchmarkPutVertex_Search contrasts the vertex put hot path with the search
// index disabled ("Plain") versus enabled ("Indexed"). The Plain arm proves
// the feature stays pay-for-what-you-use — a single nil check per put — while
// the Indexed arm captures the analyze-and-index cost a search-enabled server
// pays. Run with -benchmem; the delta between the two arms is the headline.
func BenchmarkPutVertex_Search(b *testing.B) {
	exp := time.Now().Add(time.Hour)

	b.Run("Plain", func(b *testing.B) {
		c := NewGraphCache[string, string](time.Hour)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.PutVertexWithExpiration(makeKey("k", i, 24), benchSearchCorpus[i%len(benchSearchCorpus)], exp)
		}
	})

	b.Run("Indexed", func(b *testing.B) {
		c := NewGraphCache[string, string](time.Hour)
		c.EnableSearchIndex(func(v string) search.Document { return search.Text(v) }, compareStringID)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.PutVertexWithExpiration(makeKey("k", i, 24), benchSearchCorpus[i%len(benchSearchCorpus)], exp)
		}
	})
}

// BenchmarkScanEdgesByPrefix measures the edge-prefix scan after #742 moved the
// visitor out of the read lock: matching rows are snapshotted under c.mu.RLock
// and the visitor replays after release. The headline cost surfaced here is the
// snapshot allocation (one row per matched edge, visible under -benchmem); the
// win it buys — a slow visitor no longer holding the lock against writers — is
// asserted for correctness by TestGraphCache_ScanEdgesByPrefix_CallbackReentrant
// rather than timed here.
func BenchmarkScanEdgesByPrefix(b *testing.B) {
	for _, s := range smallScales(testing.Short()) {
		s := s
		b.Run(s.name, func(b *testing.B) {
			c := NewGraphCache[string, string](time.Hour)
			c.EnablePrefixIndex(func(k string) string { return k })
			populate(b, c, s)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var n int
				c.ScanEdgesByPrefix(context.Background(), "https://example.com", "",
					func(_ string, _ string, _ string, _ string, _ float32, _ time.Time) bool {
						n++
						return true
					})
				if n == 0 {
					b.Fatal("scan matched no edges")
				}
			}
		})
	}
}

// BenchmarkSearchVertices measures the read-side search path after the #741
// lock-splitting refactor (query analysis + BM25 ranking + liveness/prefix
// filtering run without GraphCache.mu). It covers a narrow query (few
// matches), a broad query (many matches), a prefix-scoped query, and a broad
// query under concurrent writer pressure — the contended case the refactor
// targets, where searches previously serialized against writers on
// GraphCache.mu. Run with -benchmem.
func BenchmarkSearchVertices(b *testing.B) {
	const n = 5000
	newSeeded := func() *GraphCache[string, string] {
		c := NewGraphCache[string, string](time.Hour)
		c.EnableSearchIndex(func(v string) search.Document { return search.Text(v) }, compareStringID)
		c.EnablePrefixIndex(func(s string) string { return s })
		exp := time.Now().Add(time.Hour)
		for i := 0; i < n; i++ {
			ns := "user:"
			if i%2 == 1 {
				ns = "session:"
			}
			// Every doc carries the broad term "payload"; only a few carry the
			// rare term "unicorn" so the narrow query ranks a small candidate set.
			val := benchSearchCorpus[i%len(benchSearchCorpus)] + " payload"
			if i%1000 == 0 {
				val += " unicorn"
			}
			c.PutVertexWithExpiration(fmt.Sprintf("%s%05d", ns, i), val, exp)
		}
		return c
	}

	b.Run("Narrow", func(b *testing.B) {
		c := newSeeded()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = c.SearchVertices("unicorn", 50, "")
		}
	})

	b.Run("Broad", func(b *testing.B) {
		c := newSeeded()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = c.SearchVertices("payload", 50, "")
		}
	})

	b.Run("PrefixScoped", func(b *testing.B) {
		c := newSeeded()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = c.SearchVertices("payload", 50, "user:")
		}
	})

	b.Run("BroadConcurrentWriters", func(b *testing.B) {
		c := newSeeded()
		var stop atomic.Bool
		var wg sync.WaitGroup
		exp := time.Now().Add(time.Hour)
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func(seed int) {
				defer wg.Done()
				i := seed
				for !stop.Load() {
					c.PutVertexWithExpiration(fmt.Sprintf("user:%05d", i%n), "churn payload", exp)
					i += 7
				}
			}(w)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = c.SearchVertices("payload", 50, "")
		}
		b.StopTimer()
		stop.Store(true)
		wg.Wait()
	})
}

func BenchmarkDeleteVertices_Indexed(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		n := n
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			exp := time.Now().Add(time.Hour)
			keys := make([]string, n)
			for i := range keys {
				keys[i] = makeKey("https://example.com", i, 32)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for iter := 0; iter < b.N; iter++ {
				b.StopTimer()
				c := NewGraphCache[string, string](time.Hour)
				c.EnablePrefixIndex(func(k string) string { return k })
				c.EnableSearchIndex(func(v string) search.Document { return search.Text(v) }, compareStringID)
				vs := make([]VertexItem[string, string], n)
				for i, k := range keys {
					vs[i] = VertexItem[string, string]{Key: k, Value: benchSearchCorpus[i%len(benchSearchCorpus)], Expiration: exp}
				}
				c.PutVerticesWithExpiration(vs)
				b.StartTimer()

				if got := c.DeleteVertices(keys); got != n {
					b.Fatalf("DeleteVertices = %d, want %d", got, n)
				}
			}
		})
	}
}

// BenchmarkDeleteByPrefix_Indexed measures the batched DeleteByPrefix path
// (#738): it walks the prefix radix to collect victims, then removes them in a
// single DeleteMany that fires one batched index-maintenance pass. Setup is
// excluded from the timer.
func BenchmarkDeleteByPrefix_Indexed(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		n := n
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			exp := time.Now().Add(time.Hour)
			keys := make([]string, n)
			for i := range keys {
				keys[i] = makeKey("doomed:", i, 32)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for iter := 0; iter < b.N; iter++ {
				b.StopTimer()
				c := NewGraphCache[string, string](time.Hour)
				c.EnablePrefixIndex(func(k string) string { return k })
				c.EnableSearchIndex(func(v string) search.Document { return search.Text(v) }, compareStringID)
				vs := make([]VertexItem[string, string], n)
				for i, k := range keys {
					vs[i] = VertexItem[string, string]{Key: k, Value: benchSearchCorpus[i%len(benchSearchCorpus)], Expiration: exp}
				}
				c.PutVerticesWithExpiration(vs)
				b.StartTimer()

				if got := c.DeleteByPrefix(context.Background(), "doomed:", 0); got != n {
					b.Fatalf("DeleteByPrefix = %d, want %d", got, n)
				}
			}
		})
	}
}

// BenchmarkPutVerticesIndexed measures the batch vertex-write path with the
// search index disabled vs enabled, and under concurrency. Since #739 the
// per-document analysis (tokenization) runs BEFORE the aggregate write lock is
// taken, so the Indexed arms hold the lock only for the cheap store + postings
// mutation. The Parallel arm is the headline: multiple writers batch into the
// same cache, where shrinking the locked critical section lifts throughput.
// Run with -benchmem.
func BenchmarkPutVerticesIndexed(b *testing.B) {
	const batch = 256
	exp := time.Now().Add(time.Hour)

	makeBatch := func(prefix string) []VertexItem[string, string] {
		items := make([]VertexItem[string, string], batch)
		for i := range items {
			items[i] = VertexItem[string, string]{
				Key:        makeKey(prefix, i, 32),
				Value:      benchSearchCorpus[i%len(benchSearchCorpus)],
				Expiration: exp,
			}
		}
		return items
	}

	b.Run("SearchDisabled", func(b *testing.B) {
		c := NewGraphCache[string, string](time.Hour)
		items := makeBatch("k")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.PutVerticesWithExpiration(items)
		}
	})

	b.Run("SearchEnabled", func(b *testing.B) {
		c := NewGraphCache[string, string](time.Hour)
		c.EnableSearchIndex(func(v string) search.Document { return search.Text(v) }, compareStringID)
		items := makeBatch("k")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.PutVerticesWithExpiration(items)
		}
	})

	b.Run("SearchEnabledParallel", func(b *testing.B) {
		c := NewGraphCache[string, string](time.Hour)
		c.EnableSearchIndex(func(v string) search.Document { return search.Text(v) }, compareStringID)
		var worker atomic.Int64
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			// Each goroutine writes into its own key namespace so the run
			// exercises concurrent inserts rather than pure last-write churn.
			items := makeBatch(fmt.Sprintf("w%d-", worker.Add(1)))
			for pb.Next() {
				c.PutVerticesWithExpiration(items)
			}
		})
	})
}

// BenchmarkPutVerticesIndexedBatchPreparation covers #1051's production batch
// shapes. In addition to Go's ns/op and allocation metrics it reports the time
// actually spent holding GraphCache.mu, input throughput, and search-index
// write-lock acquisitions so off-lock preparation is visible independently of
// total analysis cost. Run with -benchmem; use -benchtime=1x for the 65k arms.
func BenchmarkPutVerticesIndexedBatchPreparation(b *testing.B) {
	type scenario struct {
		name       string
		n          int
		value      string
		duplicates int
		skipped    bool
	}
	short := "alpha beta gamma delta"
	long := strings.Repeat("alpha beta gamma delta epsilon zeta eta theta ", 64)
	scenarios := []scenario{
		{name: "UniqueShort/1k", n: 1_000, value: short},
		{name: "UniqueShort/10k", n: 10_000, value: short},
		{name: "UniqueShort/65k", n: 65_000, value: short},
		{name: "UniqueLong/1k", n: 1_000, value: long},
		{name: "DuplicateHeavy/65k", n: 65_000, value: short, duplicates: 128},
		{name: "SkippedHeavy/65k", n: 65_000, value: short, skipped: true},
	}
	for _, positions := range []bool{true, false} {
		positionName := "PositionsOn"
		if !positions {
			positionName = "PositionsOff"
		}
		for _, scenario := range scenarios {
			scenario := scenario
			b.Run(positionName+"/"+scenario.name, func(b *testing.B) {
				exp := time.Now().Add(time.Hour)
				items := make([]VertexItem[string, string], scenario.n)
				for i := range items {
					keyIndex := i
					if scenario.duplicates > 0 {
						keyIndex %= scenario.duplicates
					}
					items[i] = VertexItem[string, string]{
						Key:        makeKey("batch://", keyIndex, 32),
						Value:      scenario.value,
						Expiration: exp,
					}
				}
				c := NewGraphCache[string, string](time.Hour)
				var opts []SearchIndexOption
				if !positions {
					opts = append(opts, WithoutSearchPositions())
				}
				c.EnableSearchIndex(func(value string) search.Document { return search.Text(value) }, compareStringID, opts...)
				if scenario.skipped {
					if err := c.PutVerticesWithExpiration(items); err != nil {
						b.Fatal(err)
					}
				}

				var graphLock time.Duration
				observe := func(d time.Duration) { graphLock += d }
				beforeLocks := c.SearchIndexMemoryStats().WriteLockAcquisitions
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if scenario.skipped {
						if _, _, err := c.putVerticesIfAbsentChecked(items, hlc.Timestamp{}, false, observe); err != nil {
							b.Fatal(err)
						}
						continue
					}
					if err := c.putVerticesWithExpirationChecked(items, observe); err != nil {
						b.Fatal(err)
					}
				}
				elapsed := b.Elapsed()
				afterLocks := c.SearchIndexMemoryStats().WriteLockAcquisitions
				if b.N > 0 {
					b.ReportMetric(float64(graphLock.Nanoseconds())/float64(b.N), "graph_lock_ns/op")
					b.ReportMetric(float64(afterLocks-beforeLocks)/float64(b.N), "index_write_locks/op")
					b.ReportMetric(float64(scenario.n*b.N)/elapsed.Seconds(), "items/s")
				}
			})
		}
	}
}

// BenchmarkApplyPutVerticesHLC contrasts the two ways the replication apply
// path can replay a 1k-vertex MutationOp_PutVertices with the search index
// enabled (#840): the pre-#840 loop of singular PutVertexWithExpirationHLC
// calls (N lock cycles, tokenization under GraphCache.mu) against one
// PutVerticesWithExpirationHLC batch (one lock cycle, analysis off-lock).
func BenchmarkApplyPutVerticesHLC(b *testing.B) {
	const n = 1000
	value := "the quick brown fox jumps over the lazy dog and keeps running through the forest"
	exp := time.Now().Add(time.Hour)
	items := make([]VertexItem[string, string], n)
	for i := range items {
		items[i] = VertexItem[string, string]{Key: makeKey("bench://apply", i, 40), Value: value, Expiration: exp}
	}
	newCache := func() *GraphCache[string, string] {
		c := NewGraphCache[string, string](time.Hour)
		c.EnableSearchIndex(func(v string) search.Document { return search.Text(v) }, compareStringID)
		return c
	}
	ts := hlc.Timestamp{WallNs: 1}

	b.Run("SingularLoop", func(b *testing.B) {
		c := newCache()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for j := range items {
				c.PutVertexWithExpirationHLC(items[j].Key, items[j].Value, items[j].Expiration, ts)
			}
		}
	})
	b.Run("Batch", func(b *testing.B) {
		c := newCache()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.PutVerticesWithExpirationHLC(items, ts)
		}
	})

	// The contended pair measures what the receiving replica's local READS
	// experience while catch-up replay hammers the apply path — the singular
	// loop tokenizes all N documents under GraphCache.mu, the batch analyzes
	// off-lock and holds the write lock only for the map/index writes.
	for _, mode := range []string{"SingularLoop", "Batch"} {
		mode := mode
		b.Run("GetVertexUnder/"+mode, func(b *testing.B) {
			c := newCache()
			c.PutVerticesWithExpirationHLC(items, ts)
			var stop atomic.Bool
			done := make(chan struct{})
			go func() {
				defer close(done)
				for !stop.Load() {
					if mode == "Batch" {
						c.PutVerticesWithExpirationHLC(items, ts)
					} else {
						for j := range items {
							c.PutVertexWithExpirationHLC(items[j].Key, items[j].Value, items[j].Expiration, ts)
						}
					}
				}
			}()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = c.GetVertex(items[i%n].Key)
			}
			b.StopTimer()
			stop.Store(true)
			<-done
		})
	}
}

// BenchmarkLocalCommunity pits the #845 sweep-cut extraction against plain
// PPR top-N on a planted-partition graph (20 communities of 50, dense
// intra-community edges + sparse cross links). The sweep's target cost is
// O(touched·log touched + edges(touched)) on top of the shared push.
func BenchmarkLocalCommunity(b *testing.B) {
	const (
		communities = 20
		size        = 50
	)
	c := NewGraphCache[string, string](time.Hour)
	exp := time.Now().Add(time.Hour)
	key := func(ci, vi int) string { return fmt.Sprintf("c%02d/v%02d", ci, vi) }
	for ci := 0; ci < communities; ci++ {
		for vi := 0; vi < size; vi++ {
			c.PutVertexWithExpiration(key(ci, vi), "v", exp)
		}
	}
	for ci := 0; ci < communities; ci++ {
		for vi := 0; vi < size; vi++ {
			// Dense ring+chords inside the community.
			for d := 1; d <= 5; d++ {
				c.PutEdgeWithExpiration(key(ci, vi), key(ci, (vi+d)%size), 1.0, exp)
			}
			// One weak cross-community link per vertex.
			c.PutEdgeWithExpiration(key(ci, vi), key((ci+1)%communities, vi), 0.02, exp)
		}
	}
	b.Run("LocalCommunity", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			g, _, err := c.LocalCommunityContext(context.Background(), key(3, 7), 0, 0.15, 1e-5, WeightingRaw, nil)
			if err != nil || len(g.Vertices) == 0 {
				b.Fatalf("g=%v err=%v", len(g.Vertices), err)
			}
		}
	})
	b.Run("PPRTopN", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			g, err := c.PersonalizedPageRankContext(context.Background(), key(3, 7), size, 0.15, 1e-5, WeightingRaw, nil)
			if err != nil || len(g.Vertices) == 0 {
				b.Fatalf("g=%v err=%v", len(g.Vertices), err)
			}
		}
	})
}
