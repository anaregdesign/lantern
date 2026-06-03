package graph

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
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
//	go test -bench=BenchmarkGraphCacheMemory -benchmem -benchtime=1x ./core/cache/graph
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
				_ = c.Neighbor(keys[i%s.vertices], 1, 10, false)
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
				_ = c.Neighbor(keys[i%s.vertices], 3, 10, false)
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

// BenchmarkWeight_AddOnly exercises the amortized-compaction path: every add
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

// BenchmarkAddEdge_Existing measures the existing-edge fast path in
// edgeCache.addWithExpiration. After warming up a single edge, every
// iteration must hit the fast path: one dict RLock to resolve both ids,
// one edgeCache RLock to find the *weight, then the leaf weight.mu
// append. No dict writes, no edgeCache writes.
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
