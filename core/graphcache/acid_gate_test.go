package graphcache

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ACID-style quality gate for the GraphCache.
//
// Lantern is an in-memory key-vertex-store: there is no on-disk log, so the
// classical "D" (Durability) leg of ACID is intentionally out of scope and is
// only documented here. The remaining legs are exercised as discrete sub-tests
// so that a regression in any one property surfaces independently in CI.
//
// Run with -race to also catch any data race that would, in practice, break
// the isolation property even when the higher-level invariants happen to hold.
//
// The whole file lives in the `graph` package so the gate can reach into
// unexported helpers (notably `flush`) without exposing them to the public
// API surface.
func TestACIDQualityGate(t *testing.T) {
	t.Parallel()

	t.Run("Atomicity/PutEdgeNeverLeaksNotFound", testAtomicityPutEdge)
	t.Run("Atomicity/AddEdgeAutoCreatesBothEndpoints", testAtomicityAddEdgeEndpoints)

	t.Run("Consistency/AddEdgeIsAdditive", testConsistencyAddEdgeAdditive)
	t.Run("Consistency/PutEdgeReplacesWeight", testConsistencyPutEdgeReplaces)
	t.Run("Consistency/DeleteVertexCascadesViaFlush", testConsistencyDeleteVertexCascade)
	t.Run("Consistency/ExpiredEdgeIsUnreachable", testConsistencyEdgeTTL)

	t.Run("Isolation/EdgeImpliesEndpointVisibility", testIsolationEdgeImpliesEndpoints)
	t.Run("Isolation/ConcurrentMixedOpsKeepInvariants", testIsolationMixedOps)

	t.Run("Durability/InMemoryByDesign", testDurabilityDocumented)
}

// ---------------------------------------------------------------------------
// A — Atomicity
// ---------------------------------------------------------------------------

// testAtomicityPutEdge is the regression guard for the bug fixed by
// PutEdgeWithExpiration: PutEdge must replace an edge weight without ever
// leaking a transient NotFound to concurrent readers.
func testAtomicityPutEdge(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	exp := time.Now().Add(time.Minute)
	c.AddEdgeWithExpiration("T", "H", 1, exp)

	const writers = 4
	const readers = 16
	const writesPerWriter = 1000

	var missing, reads int64
	stop := make(chan struct{})

	var readerWG sync.WaitGroup
	for r := 0; r < readers; r++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, _, ok := c.GetEdgeDetail("T", "H"); !ok {
					atomic.AddInt64(&missing, 1)
				}
				atomic.AddInt64(&reads, 1)
			}
		}()
	}

	var writerWG sync.WaitGroup
	for w := 0; w < writers; w++ {
		writerWG.Add(1)
		go func(w int) {
			defer writerWG.Done()
			for i := 0; i < writesPerWriter; i++ {
				c.PutEdgeWithExpiration("T", "H", float32(w*1000+i+1), exp)
			}
		}(w)
	}
	writerWG.Wait()
	close(stop)
	readerWG.Wait()

	if missing != 0 {
		t.Fatalf("PutEdge leaked NotFound to readers: %d of %d reads observed a missing edge",
			missing, reads)
	}
}

// testAtomicityAddEdgeEndpoints asserts that the endpoint auto-create
// performed by AddEdge / PutEdge is atomic w.r.t. the edge itself: once an
// edge is visible, both endpoint vertices must also be visible.
func testAtomicityAddEdgeEndpoints(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	exp := time.Now().Add(time.Minute)

	const ops = 2000
	const readers = 8
	stop := make(chan struct{})

	var violations int64
	var readerWG sync.WaitGroup
	for r := 0; r < readers; r++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// Walk a small window of tails; if the edge is visible the
				// endpoints must also be visible.
				for i := 0; i < 16; i++ {
					tail := keyFromInt(i)
					head := tail + "_h"
					if _, ok := c.GetWeight(tail, head); ok {
						if _, ok := c.GetVertex(tail); !ok {
							atomic.AddInt64(&violations, 1)
						}
						if _, ok := c.GetVertex(head); !ok {
							atomic.AddInt64(&violations, 1)
						}
					}
				}
			}
		}()
	}

	for i := 0; i < ops; i++ {
		tail := keyFromInt(i % 16)
		head := tail + "_h"
		c.AddEdgeWithExpiration(tail, head, 1, exp)
	}
	close(stop)
	readerWG.Wait()

	if violations != 0 {
		t.Fatalf("edge visible without endpoint vertex: %d violations", violations)
	}
}

// ---------------------------------------------------------------------------
// C — Consistency
// ---------------------------------------------------------------------------

// testConsistencyAddEdgeAdditive locks in the documented additive semantics of
// AddEdge: two contributions of weight 2 must sum to 4.
func testConsistencyAddEdgeAdditive(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	exp := time.Now().Add(time.Minute)
	c.AddEdgeWithExpiration("a", "b", 2, exp)
	c.AddEdgeWithExpiration("a", "b", 2, exp)

	w, ok := c.GetWeight("a", "b")
	if !ok {
		t.Fatalf("expected edge a->b to exist after AddEdge")
	}
	if w != 4 {
		t.Fatalf("AddEdge should be additive: got weight %v, want 4", w)
	}
}

// testConsistencyPutEdgeReplaces locks in the documented replace semantics of
// PutEdge: PutEdge must overwrite, not accumulate.
func testConsistencyPutEdgeReplaces(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	exp := time.Now().Add(time.Minute)
	c.AddEdgeWithExpiration("a", "b", 5, exp)
	c.PutEdgeWithExpiration("a", "b", 2, exp)

	w, ok := c.GetWeight("a", "b")
	if !ok {
		t.Fatalf("expected edge a->b to exist after PutEdge")
	}
	if w != 2 {
		t.Fatalf("PutEdge should replace weight: got %v, want 2", w)
	}
}

// testConsistencyDeleteVertexCascade asserts the global invariant the
// background `flush()` enforces: after a vertex is deleted there must not be
// any edge referencing it once the next flush has run.
func testConsistencyDeleteVertexCascade(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	exp := time.Now().Add(time.Minute)
	c.AddEdgeWithExpiration("a", "b", 1, exp)
	c.AddEdgeWithExpiration("a", "c", 1, exp)
	c.AddEdgeWithExpiration("x", "a", 1, exp)

	c.DeleteVertex("a")
	// flush is the same orphan-edge sweeper that Watch ticks on; calling it
	// directly removes the time element from this assertion.
	c.flush()

	if _, ok := c.GetWeight("a", "b"); ok {
		t.Fatalf("edge a->b survived DeleteVertex(a) + flush")
	}
	if _, ok := c.GetWeight("a", "c"); ok {
		t.Fatalf("edge a->c survived DeleteVertex(a) + flush")
	}
	if _, ok := c.GetWeight("x", "a"); ok {
		t.Fatalf("edge x->a survived DeleteVertex(a) + flush")
	}
}

// testConsistencyEdgeTTL asserts that an edge whose every contribution has
// expired is reported as missing by GetEdgeDetail (which lazily evicts via
// the inner `weight.isZero` path).
func testConsistencyEdgeTTL(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.AddEdgeWithExpiration("a", "b", 1, time.Now().Add(20*time.Millisecond))

	if _, ok := c.GetWeight("a", "b"); !ok {
		t.Fatalf("edge a->b should be live immediately after AddEdge")
	}

	time.Sleep(80 * time.Millisecond)

	if _, _, ok := c.GetEdgeDetail("a", "b"); ok {
		t.Fatalf("edge a->b should have decayed past its TTL")
	}
}

// ---------------------------------------------------------------------------
// I — Isolation
// ---------------------------------------------------------------------------

// testIsolationEdgeImpliesEndpoints is the concurrent variant of the
// Atomicity/AddEdgeAutoCreatesBothEndpoints sub-test: it interleaves writes
// across many distinct edges with readers that sample the invariant
// "edge ⇒ both endpoints visible" and asserts zero violations under -race.
func testIsolationEdgeImpliesEndpoints(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	exp := time.Now().Add(time.Minute)

	const writers = 4
	const readers = 8
	const writesPerWriter = 500

	var violations int64
	stop := make(chan struct{})

	var readerWG sync.WaitGroup
	for r := 0; r < readers; r++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for i := 0; i < 32; i++ {
					tail := keyFromInt(i)
					head := tail + "_h"
					if _, ok := c.GetWeight(tail, head); ok {
						if _, ok := c.GetVertex(tail); !ok {
							atomic.AddInt64(&violations, 1)
						}
						if _, ok := c.GetVertex(head); !ok {
							atomic.AddInt64(&violations, 1)
						}
					}
				}
			}
		}()
	}

	var writerWG sync.WaitGroup
	for w := 0; w < writers; w++ {
		writerWG.Add(1)
		go func(w int) {
			defer writerWG.Done()
			for i := 0; i < writesPerWriter; i++ {
				slot := (w*writesPerWriter + i) % 32
				tail := keyFromInt(slot)
				head := tail + "_h"
				if i%2 == 0 {
					c.AddEdgeWithExpiration(tail, head, 1, exp)
				} else {
					c.PutEdgeWithExpiration(tail, head, 1, exp)
				}
			}
		}(w)
	}
	writerWG.Wait()
	close(stop)
	readerWG.Wait()

	if violations != 0 {
		t.Fatalf("edge visible without endpoint vertex under concurrent load: %d violations", violations)
	}
}

// testIsolationMixedOps runs a hostile mix of Add / Put / Delete / GetVertex /
// GetEdgeDetail against the same key space and only asserts that nothing
// panics, deadlocks, or trips the race detector. The cache is allowed to
// converge to any legal state; the test exists to prove the public API never
// exposes a torn read.
func testIsolationMixedOps(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	exp := time.Now().Add(time.Minute)

	const goroutines = 16
	const iters = 500

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				slot := (g*iters + i) % 8
				tail := keyFromInt(slot)
				head := tail + "_h"
				switch i % 5 {
				case 0:
					c.AddEdgeWithExpiration(tail, head, 1, exp)
				case 1:
					c.PutEdgeWithExpiration(tail, head, float32(i+1), exp)
				case 2:
					c.DeleteEdge(tail, head)
				case 3:
					_, _ = c.GetVertex(tail)
				case 4:
					_, _, _ = c.GetEdgeDetail(tail, head)
				}
			}
		}(g)
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// D — Durability (out of scope)
// ---------------------------------------------------------------------------

// testDurabilityDocumented is a checklist marker: Lantern is in-memory by
// design, so D in ACID is not a property we promise. When/if a persistence
// backend lands, replace this with a real recovery test instead of removing
// the slot.
func testDurabilityDocumented(t *testing.T) {
	t.Log("Lantern is an in-memory store; durability is intentionally out of scope. " +
		"If persistence is added later, replace this marker with a crash-and-recover test.")
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func keyFromInt(i int) string {
	// Tiny stable encoding (no fmt to keep this allocation-light in hot loops).
	const digits = "0123456789abcdef"
	if i < 16 {
		return string([]byte{'k', digits[i]})
	}
	return string([]byte{'k', digits[(i>>4)&0xf], digits[i&0xf]})
}
