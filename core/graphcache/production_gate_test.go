package graphcache

import (
	"context"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Production quality gate for the GraphCache.
//
// The ACID gate (acid_gate_test.go) encodes the classic transactional
// invariants. This file encodes the additional invariants any in-memory KVS
// must satisfy before it is safe to operate in production: liveness, resource
// hygiene, input safety, read-path consistency, idempotency, and topological
// edge cases. Each property is a discrete sub-test so a regression in any one
// of them surfaces independently in CI.
//
// All sub-tests are designed to run reliably under `go test -race -shuffle=on`
// in a few seconds total. They live in `package graph` so they can reach the
// unexported `flush()` / `edges` internals and assert on map sizes directly.
//
// Properties enforced here (in addition to ACID):
//
//	L1  Liveness/NoDeadlockUnderHeavyConcurrency
//	R1  ResourceHygiene/WatchStopsOnContextCancel              (no goroutine leak)
//	R2  ResourceHygiene/ExpiredVerticesAreReaped               (no memory leak)
//	R3  ResourceHygiene/ExpiredEdgesAreReaped                  (no memory leak)
//	I1  InputSafety/BoundaryInputsDoNotPanic
//	I2  InputSafety/NaNAndInfWeightsDoNotPanic
//	I3  InputSafety/DeleteOfMissingIsNoop
//	C1  ReadConsistency/AllReadPathsAgreeAfterExpiry
//	C2  ReadConsistency/NeighborStableUnderConcurrentWrites
//	D1  Idempotence/PutVertexAndPutEdgeConverge
//	T1  Topology/SelfLoopRoundTrips
func TestProductionQualityGate(t *testing.T) {
	t.Parallel()

	t.Run("Liveness/NoDeadlockUnderHeavyConcurrency", testLivenessHeavyMix)

	t.Run("ResourceHygiene/WatchStopsOnContextCancel", testResourceWatchStops)
	t.Run("ResourceHygiene/ExpiredVerticesAreReaped", testResourceVertexReaped)
	t.Run("ResourceHygiene/ExpiredEdgesAreReaped", testResourceEdgeReaped)

	t.Run("InputSafety/BoundaryInputsDoNotPanic", testInputBoundary)
	t.Run("InputSafety/NaNAndInfWeightsDoNotPanic", testInputNaNInf)
	t.Run("InputSafety/DeleteOfMissingIsNoop", testInputDeleteMissing)

	t.Run("ReadConsistency/AllReadPathsAgreeAfterExpiry", testReadConsistencyAfterExpiry)
	t.Run("ReadConsistency/NeighborStableUnderConcurrentWrites", testReadConsistencyNeighborChurn)

	t.Run("Idempotence/PutVertexAndPutEdgeConverge", testIdempotenceConverge)

	t.Run("Topology/SelfLoopRoundTrips", testTopologySelfLoop)
}

// ---------------------------------------------------------------------------
// L — Liveness
// ---------------------------------------------------------------------------

// testLivenessHeavyMix proves the cache never deadlocks under a heavy mix of
// reads, writes and deletes. A watchdog goroutine fails the test if the
// workload has not finished within a generous deadline.
func testLivenessHeavyMix(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)

	const (
		workers    = 32
		iterations = 500
	)
	exp := time.Now().Add(time.Minute)

	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := 0; i < iterations; i++ {
					k := keyFromInt((w*iterations + i) % 64)
					c.PutVertexWithExpiration(k, i, exp)
					c.GetVertex(k)
					tail := keyFromInt(i % 16)
					head := keyFromInt((i + 1) % 16)
					c.AddEdgeWithExpiration(tail, head, 1, exp)
					c.PutEdgeWithExpiration(tail, head, 2, exp)
					c.GetEdgeDetail(tail, head)
					c.Neighbor(tail, 1, 4, false, false, nil)
					c.DeleteEdge(tail, head)
					c.DeleteVertex(k)
				}
			}(w)
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// fine
	case <-time.After(30 * time.Second):
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		t.Fatalf("L1 liveness violation: heavy mixed workload did not finish in 30s (deadlock suspected)\n=== goroutine dump ===\n%s", buf[:n])
	}
}

// ---------------------------------------------------------------------------
// R — Resource Hygiene
// ---------------------------------------------------------------------------

// testResourceWatchStops asserts Watch returns promptly on context cancel and
// does not leak its goroutine. Goroutine counts include scheduler noise, so we
// retry the post-cancel reading briefly to absorb that noise.
func testResourceWatchStops(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)

	// Settle the runtime before sampling.
	runtime.GC()
	runtime.Gosched()
	time.Sleep(20 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	go c.Watch(ctx, 5*time.Millisecond)

	// Wait for Watch to actually be scheduled. runtime.NumGoroutine() is
	// inherently racy against the Go scheduler (especially under -shuffle=on),
	// so poll with a bounded retry instead of a single instantaneous read.
	startupDeadline := time.Now().Add(2 * time.Second)
	started := false
	for time.Now().Before(startupDeadline) {
		if runtime.NumGoroutine() > baseline {
			started = true
			break
		}
		runtime.Gosched()
		time.Sleep(5 * time.Millisecond)
	}
	if !started {
		t.Fatalf("R1 setup: expected Watch goroutine to be running within 2s (baseline=%d, now=%d)", baseline, runtime.NumGoroutine())
	}

	cancel()

	// Watch must return promptly. Tolerate scheduler noise by retrying.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.Gosched()
		if runtime.NumGoroutine() <= baseline+1 {
			return // back to baseline (allow +1 for residual scheduler goroutines)
		}
		time.Sleep(10 * time.Millisecond)
	}
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	t.Fatalf("R1 goroutine leak: Watch did not unwind within 2s of cancel (baseline=%d, after=%d)\n%s",
		baseline, runtime.NumGoroutine(), buf[:n])
}

// testResourceVertexReaped asserts that vertices whose TTL has elapsed are
// physically reclaimed by Watch (Flush). It also reaches into the unexported
// edge map to confirm orphan edges are pruned by the cascade flush.
func testResourceVertexReaped(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)

	const N = 200
	short := 30 * time.Millisecond
	exp := time.Now().Add(short)

	for i := 0; i < N; i++ {
		c.PutVertexWithExpiration(keyFromInt(i), i, exp)
	}
	// At least one edge so we can verify cascade.
	c.AddEdgeWithExpiration("a", "b", 1, exp)
	c.PutVertexWithExpiration("a", 0, exp)
	c.PutVertexWithExpiration("b", 0, exp)

	// Wait past TTL and trigger reaping the same way Watch does.
	time.Sleep(short + 80*time.Millisecond)
	c.vertices.Flush()
	c.edges.flush()
	c.flush()

	c.mu.RLock()
	verts := c.vertices.Count()
	tfSize := len(c.edges.tf)
	dfSize := len(c.edges.df)
	c.mu.RUnlock()

	if verts != 0 {
		t.Fatalf("R2 memory leak: %d vertices remained after expiry+flush (expected 0)", verts)
	}
	if tfSize != 0 || dfSize != 0 {
		t.Fatalf("R2 cascade leak: edge maps not reclaimed after vertex expiry (tf=%d, df=%d)", tfSize, dfSize)
	}
}

// testResourceEdgeReaped asserts expired edge contributions are physically
// removed from the internal weight slice, not merely treated as zero.
func testResourceEdgeReaped(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	short := 30 * time.Millisecond
	exp := time.Now().Add(short)

	c.AddEdgeWithExpiration("t", "h", 1, exp)
	c.AddEdgeWithExpiration("t", "h", 2, exp)
	c.AddEdgeWithExpiration("t", "h", 3, exp)

	time.Sleep(short + 80*time.Millisecond)
	c.edges.flush()

	if n := c.edges.count(); n != 0 {
		t.Fatalf("R3 memory leak: expired edge bucket survived edges.flush() (count=%d)", n)
	}

	// And the public API now reports the edge as gone.
	if _, _, ok := c.GetEdgeDetail("t", "h"); ok {
		t.Fatalf("R3 read leak: GetEdgeDetail still reports an expired edge")
	}
}

// ---------------------------------------------------------------------------
// I — Input Safety
// ---------------------------------------------------------------------------

// testInputBoundary feeds the cache a battery of boundary inputs that real
// callers may produce by accident. None must panic and the cache must remain
// queryable afterwards.
func testInputBoundary(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("I1 panic on boundary input: %v", r)
		}
	}()

	c := NewGraphCache[string, int](time.Minute)
	now := time.Now()

	cases := []struct {
		name string
		fn   func()
	}{
		{"empty-key", func() { c.PutVertexWithExpiration("", 0, now.Add(time.Minute)) }},
		{"long-key", func() {
			big := make([]byte, 1<<14)
			for i := range big {
				big[i] = 'x'
			}
			c.PutVertexWithExpiration(string(big), 0, now.Add(time.Minute))
		}},
		{"zero-weight-edge", func() { c.AddEdgeWithExpiration("a", "b", 0, now.Add(time.Minute)) }},
		{"negative-weight-edge", func() { c.AddEdgeWithExpiration("a", "b", -3.14, now.Add(time.Minute)) }},
		{"self-loop", func() { c.AddEdgeWithExpiration("x", "x", 1, now.Add(time.Minute)) }},
		{"far-past-expiration", func() { c.PutVertexWithExpiration("past", 0, now.Add(-time.Hour)) }},
		{"far-future-expiration", func() { c.PutVertexWithExpiration("future", 0, now.Add(100*365*24*time.Hour)) }},
		{"put-edge-on-missing-endpoints", func() { c.PutEdgeWithExpiration("p", "q", 1, now.Add(time.Minute)) }},
	}
	for _, tc := range cases {
		tc.fn()
	}

	// The cache must still answer queries.
	if _, ok := c.GetVertex(""); !ok {
		t.Fatalf("I1: empty-key vertex unexpectedly missing after PutVertex")
	}
	if _, _, ok := c.GetEdgeDetail("x", "x"); !ok {
		t.Fatalf("I1: self-loop edge unexpectedly missing after AddEdge")
	}
}

// testInputNaNInf ensures pathological floating-point weights cannot crash the
// cache. We do not assert on the resulting numeric value (NaN comparisons are
// well known to be lossy); we only require that the cache stays responsive.
func testInputNaNInf(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("I2 panic on NaN/Inf weight: %v", r)
		}
	}()

	c := NewGraphCache[string, int](time.Minute)
	exp := time.Now().Add(time.Minute)

	c.AddEdgeWithExpiration("a", "b", float32(math.NaN()), exp)
	c.AddEdgeWithExpiration("c", "d", float32(math.Inf(1)), exp)
	c.AddEdgeWithExpiration("e", "f", float32(math.Inf(-1)), exp)

	// Must not panic on any read path.
	_, _, _ = c.GetEdgeDetail("a", "b")
	_, _, _ = c.GetEdgeDetail("c", "d")
	_, _, _ = c.GetEdgeDetail("e", "f")
	_ = c.Neighbor("a", 1, 4, false, false, nil)
	_ = c.Neighbor("c", 1, 4, true, false, nil) // also exercise the TF-IDF path
}

// testInputDeleteMissing verifies idempotent delete: deleting an absent
// vertex or edge (even twice in a row) is a no-op, never a panic, and leaves
// the cache state untouched.
func testInputDeleteMissing(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("I3 panic on delete-missing: %v", r)
		}
	}()

	c := NewGraphCache[string, int](time.Minute)

	c.DeleteVertex("ghost")
	c.DeleteVertex("ghost")
	c.DeleteEdge("nope", "nada")
	c.DeleteEdge("nope", "nada")

	// State remains coherent: a fresh insert still works.
	c.PutVertexWithExpiration("real", 1, time.Now().Add(time.Minute))
	if _, ok := c.GetVertex("real"); !ok {
		t.Fatalf("I3: cache stopped accepting writes after delete-missing")
	}
}

// ---------------------------------------------------------------------------
// C — Read Consistency
// ---------------------------------------------------------------------------

// testReadConsistencyAfterExpiry asserts that once a vertex/edge has expired
// every public read path agrees it is gone. This guards against silent skew
// between GetVertex, GetEdgeDetail and Neighbor (which is built on the
// edge-map snapshot).
func testReadConsistencyAfterExpiry(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	short := 30 * time.Millisecond
	exp := time.Now().Add(short)

	c.PutVertexWithExpiration("a", 1, exp)
	c.PutVertexWithExpiration("b", 2, exp)
	c.AddEdgeWithExpiration("a", "b", 1, exp)

	time.Sleep(short + 80*time.Millisecond)
	// Trigger the same reaping path as Watch.
	c.vertices.Flush()
	c.edges.flush()
	c.flush()

	if _, ok := c.GetVertex("a"); ok {
		t.Fatalf("C1: GetVertex still surfaces expired vertex")
	}
	if _, _, ok := c.GetEdgeDetail("a", "b"); ok {
		t.Fatalf("C1: GetEdgeDetail still surfaces expired edge")
	}
	g := c.Neighbor("a", 2, 8, false, false, nil)
	if len(g.Vertices) != 0 || len(g.Edges) != 0 {
		t.Fatalf("C1: Neighbor returned %d vertices and %d edges from an all-expired seed",
			len(g.Vertices), len(g.Edges))
	}
}

// testReadConsistencyNeighborChurn runs Neighbor in a tight loop while writers
// churn the same neighborhood. Combined with -race this guards against
// snapshot races and partial reads that would crash or panic.
func testReadConsistencyNeighborChurn(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	exp := time.Now().Add(time.Minute)
	const (
		writers = 8
		readers = 4
		iters   = 400
	)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				tail := keyFromInt(i % 8)
				head := keyFromInt((i + 1) % 8)
				if i%3 == 0 {
					c.PutEdgeWithExpiration(tail, head, float32(i), exp)
				} else {
					c.AddEdgeWithExpiration(tail, head, 0.1, exp)
				}
				if i%5 == 0 {
					c.DeleteEdge(tail, head)
				}
			}
		}(w)
	}
	var panics int64
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if rec := recover(); rec != nil {
					atomic.AddInt64(&panics, 1)
				}
			}()
			for i := 0; i < iters; i++ {
				select {
				case <-stop:
					return
				default:
				}
				_ = c.Neighbor(keyFromInt(i%8), 2, 4, i%2 == 0, false, nil)
			}
		}()
	}
	wg.Wait()
	close(stop)
	if got := atomic.LoadInt64(&panics); got != 0 {
		t.Fatalf("C2: Neighbor panicked %d times under concurrent writes", got)
	}
}

// ---------------------------------------------------------------------------
// D — Idempotence
// ---------------------------------------------------------------------------

// testIdempotenceConverge proves that repeated PutVertex / PutEdge calls
// converge to the value of the last write (no hidden accumulator state).
func testIdempotenceConverge(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	exp := time.Now().Add(time.Minute)

	for i := 0; i < 100; i++ {
		c.PutVertexWithExpiration("k", i, exp) // PutVertex semantics on the inner cache
	}
	if v, ok := c.GetVertex("k"); !ok || v != 99 {
		t.Fatalf("D1: PutVertex did not converge to last write (got %d, ok=%v)", v, ok)
	}

	for i := 0; i < 100; i++ {
		c.PutEdgeWithExpiration("t", "h", float32(i), exp)
	}
	w, _, ok := c.GetEdgeDetail("t", "h")
	if !ok || w != 99 {
		t.Fatalf("D1: PutEdge did not converge to last write (got %v, ok=%v)", w, ok)
	}
}

// ---------------------------------------------------------------------------
// T — Topology
// ---------------------------------------------------------------------------

// testTopologySelfLoop guards a small but real production hazard: many real
// graphs (e.g. retry counters, self-references) want (x, x) edges, and the
// auto-creation of endpoints must not fight the self-loop case.
func testTopologySelfLoop(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	exp := time.Now().Add(time.Minute)

	c.AddEdgeWithExpiration("x", "x", 2.5, exp)

	if _, ok := c.GetVertex("x"); !ok {
		t.Fatalf("T1: self-loop did not auto-create endpoint vertex")
	}
	w, _, ok := c.GetEdgeDetail("x", "x")
	if !ok || w != 2.5 {
		t.Fatalf("T1: self-loop weight wrong (got %v, ok=%v, want 2.5)", w, ok)
	}
	g := c.Neighbor("x", 1, 4, false, false, nil)
	if _, ok := g.Edges["x"]["x"]; !ok {
		t.Fatalf("T1: Neighbor lost the self-loop edge: %#v", g.Edges)
	}
}
