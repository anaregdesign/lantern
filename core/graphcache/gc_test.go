package graphcache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

func TestGraphCache_GCFlushMaintainsIndexesWatermarksAndDanglingEdges(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	c.EnableSearchIndex(textExtract, compareStringID)

	now := time.Now()
	liveExp := now.Add(time.Minute)
	expired := now.Add(-time.Millisecond)
	shortExp := now.Add(50 * time.Millisecond)

	if !c.PutVertexWithExpirationHLC("expired:vertex", "expired searchable payload", shortExp, hlc.Timestamp{WallNs: 1}) {
		t.Fatal("PutVertexWithExpirationHLC(expired:vertex) reported false")
	}
	if got := c.VertexHLCCount(); got != 1 {
		t.Fatalf("VertexHLCCount before flush = %d, want 1", got)
	}
	time.Sleep(100 * time.Millisecond)
	if got := c.CountByPrefix("expired:"); got != 0 {
		t.Fatalf("CountByPrefix(expired:) before vertex flush = %d, want 0 because the liveness filter hides expired-but-not-flushed entries", got)
	}
	if got := c.SearchVertices("expired", 10, ""); got != nil {
		t.Fatalf("SearchVertices(expired) before vertex flush = %v, want nil because liveness filter hides expired entries", keys(got))
	}

	c.PutVertexWithExpiration("tail", "tail payload", liveExp)
	c.PutVertexWithExpiration("head", "head payload", liveExp)
	c.AddEdgeWithExpiration("tail", "head", 3, liveExp)
	if !c.DeleteVertex("head") {
		t.Fatal("DeleteVertex(head) reported false")
	}
	// The edge survives physically until the dangling sweep, but the public
	// scan surface must hide it immediately because its head vertex is gone
	// (#750).
	if got := collectScan(c, "tail", "head"); len(got) != 0 {
		t.Fatalf("ScanEdgesByPrefix before dangling sweep = %v, want empty because the head vertex was deleted", got)
	}

	c.DeleteVertexHLC("tombstone:old", hlc.Timestamp{WallNs: 2}, expired)
	if len(c.vertexTombstones) != 1 {
		t.Fatalf("vertex tombstones before flush = %d, want 1", len(c.vertexTombstones))
	}

	if removed := c.vertices.Flush(); removed != 1 {
		t.Fatalf("vertices.Flush removed %d, want 1 expired vertex", removed)
	}
	zero, dangling := c.flush()
	if zero != 0 || dangling != 1 {
		t.Fatalf("c.flush removed zero=%d dangling=%d, want zero=0 dangling=1", zero, dangling)
	}

	if got := c.CountByPrefix("expired:"); got != 0 {
		t.Fatalf("CountByPrefix(expired:) after flush = %d, want 0", got)
	}
	if got := c.VertexHLCCount(); got != 0 {
		t.Fatalf("VertexHLCCount after flush = %d, want 0", got)
	}
	if len(c.vertexTombstones) != 0 {
		t.Fatalf("vertex tombstones after flush = %d, want 0", len(c.vertexTombstones))
	}
	if _, _, ok := c.GetEdgeDetail("tail", "head"); ok {
		t.Fatal("GetEdgeDetail(tail, head) returned ok=true after dangling sweep")
	}
	if got := collectScan(c, "tail", "head"); len(got) != 0 {
		t.Fatalf("ScanEdgesByPrefix after dangling sweep = %v, want empty", got)
	}
	if completed := c.ScanByPrefix(context.Background(), "expired:", func(_, _ string, _ string) bool {
		t.Fatal("ScanByPrefix yielded an expired vertex after flush")
		return false
	}); !completed {
		t.Fatal("ScanByPrefix after flush returned completed=false")
	}
}

func TestGraphCache_GCDoesNotPromoteZeroHLCToCausalBarrier(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	now := time.Now()
	c.applicationClock = func() time.Time { return now.Add(-2 * time.Hour) }
	expiration := now.Add(-time.Hour)
	if !c.PutVertexWithExpirationHLC("v", "restored", expiration, hlc.Timestamp{}) {
		t.Fatal("zero-HLC live vertex Put was rejected")
	}
	if !c.PutEdgeWithExpirationHLC("tail", "head", 1, expiration, hlc.Timestamp{}) {
		t.Fatal("zero-HLC live edge Put was rejected")
	}
	c.flushVertices()
	c.flush()
	if got := c.VertexHLCCount(); got != 0 {
		t.Fatalf("zero-HLC live watermark count after GC = %d, want 0", got)
	}
	if vertices, edges := c.CausalBarrierCounts(); vertices != 0 || edges != 0 {
		t.Fatalf("barriers after zero-HLC TTL GC = %d/%d, want 0/0", vertices, edges)
	}
	if _, ok := c.GetVertex("v"); ok {
		t.Fatal("expired zero-HLC vertex survived GC")
	}
	if _, _, ok := c.GetEdgeDetail("tail", "head"); ok {
		t.Fatal("expired zero-HLC edge survived GC")
	}
}

func TestGraphCache_GCSweepStatsDistinguishesLiveContributionCompaction(t *testing.T) {
	for _, budget := range []int{0, 1} {
		t.Run(fmt.Sprintf("budget_%d", budget), func(t *testing.T) {
			c := NewGraphCache[string, string](time.Hour)
			live := time.Now().Add(time.Hour)
			past := time.Now().Add(-time.Second)
			for _, key := range []string{"a", "b", "c", "d"} {
				c.PutVertexWithExpiration(key, key, live)
			}
			c.AddEdgeWithExpiration("a", "b", 1, live)
			c.AddEdgeWithExpiration("a", "b", 2, past)
			c.AddEdgeWithExpiration("a", "c", 1, past)
			c.AddEdgeWithExpiration("c", "d", 1, live)
			c.DeleteVertex("d")
			c.SetGCEdgeBudget(budget)

			var total GCSweepStats
			ticks := 1
			if budget > 0 {
				ticks = 2 // one tail per tick
			}
			for range ticks {
				c.flush()
				got := c.LastGCSweepStats()
				if budget > 0 && got.ScannedTails > budget {
					t.Fatalf("scanned tails = %d, budget = %d", got.ScannedTails, budget)
				}
				total.ScannedTails += got.ScannedTails
				total.ScannedEdges += got.ScannedEdges
				total.ExpiredContributions += got.ExpiredContributions
				total.CompactedContributionsInLiveBuckets += got.CompactedContributionsInLiveBuckets
				total.ZeroRemoved += got.ZeroRemoved
				total.DanglingRemoved += got.DanglingRemoved
			}
			if total.ScannedTails != 2 || total.ScannedEdges != 3 || total.ExpiredContributions != 2 ||
				total.CompactedContributionsInLiveBuckets != 1 || total.ZeroRemoved != 1 || total.DanglingRemoved != 1 {
				t.Errorf("sweep stats = %+v, want tails=2 edges=3 expired=2 compacted-live=1 zero=1 dangling=1", total)
			}
			if got := c.GCSweepBacklog(); got != 0 {
				t.Errorf("backlog after full cycle = %d, want 0", got)
			}
		})
	}
}

func TestGraphCache_VertexFlushAndReplicationSnapshotRetainOneCausalRepresentation(t *testing.T) {
	newer := hlc.Timestamp{WallNs: 20}
	older := hlc.Timestamp{WallNs: 10}
	c := NewGraphCache[string, string](time.Hour)
	expiration := time.Now().Add(40 * time.Millisecond)
	if !c.PutVertexWithExpirationHLC("v", "value", expiration, newer) {
		t.Fatal("PutVertex rejected")
	}
	time.Sleep(80 * time.Millisecond)

	// Start both production operations together. flushVertices and
	// SnapshotReplication share c.mu, so regardless of which wins, Snapshot
	// must observe either the live record or the HLC left behind by Flush and
	// convert the latter to a barrier.
	start := make(chan struct{})
	flushed := make(chan int, 1)
	go func() {
		<-start
		flushed <- c.flushVertices()
	}()
	close(start)
	snapshot := c.SnapshotReplication()
	<-flushed

	if len(snapshot.Graph.Vertices)+len(snapshot.Barriers.Vertices) != 1 {
		t.Fatalf("snapshot causal representations = live:%d barrier:%d, want exactly one", len(snapshot.Graph.Vertices), len(snapshot.Barriers.Vertices))
	}
	if c.PutVertexWithExpirationHLC("v", "older", time.Now().Add(time.Hour), older) {
		t.Fatal("older Put resurrected after concurrent vertex Flush/Snapshot")
	}
}

// TestGraphCache_GCIncrementalEdgeSweep exercises the bounded per-tick edge
// sweep (#744): convergence under a budget, deferral that never surfaces dead
// data, dangling reclamation, the tombstone/vertexHLC sweeps running every
// tick regardless of the edge budget, and disabling the budget mid-cycle.
func TestGraphCache_GCIncrementalEdgeSweep(t *testing.T) {
	const tails = 6

	// setupDecayed builds `tails` live tails each with one already-decayed
	// (zero-weight) edge to a single live head. The endpoints stay live so the
	// edges are reclaimed via the zero-weight path, not the dangling path.
	setupDecayed := func() *GraphCache[string, string] {
		c := NewGraphCache[string, string](time.Minute)
		c.EnablePrefixIndex(identityExtract)
		live := time.Now().Add(time.Minute)
		past := time.Now().Add(-time.Second)
		c.PutVertexWithExpiration("h", "head", live)
		for i := 0; i < tails; i++ {
			tk := fmt.Sprintf("t%02d", i)
			c.PutVertexWithExpiration(tk, "tail", live)
			c.AddEdgeWithExpiration(tk, "h", 1, past) // born decayed
		}
		return c
	}

	t.Run("EventuallyRemovesWithBoundedTicks", func(t *testing.T) {
		c := setupDecayed()
		c.SetGCEdgeBudget(2)
		if got := c.edges.count(); got != tails {
			t.Fatalf("edge count before sweep = %d, want %d", got, tails)
		}

		var totalZero, ticks int
		for c.edges.count() > 0 {
			ticks++
			if ticks > tails+2 {
				t.Fatalf("incremental sweep did not converge; remaining=%d", c.edges.count())
			}
			z, d := c.flush()
			if d != 0 {
				t.Fatalf("tick %d removed dangling=%d, want 0 (endpoints live)", ticks, d)
			}
			if z > 2 {
				t.Fatalf("tick %d removed zero=%d edges, exceeds budget of 2 tails", ticks, z)
			}
			totalZero += z
		}
		if totalZero != tails {
			t.Fatalf("cumulative zero-weight removals = %d, want %d", totalZero, tails)
		}
		// 6 tails at budget 2 must take >= 3 ticks — proves the pause was spread
		// across ticks rather than done in one O(E) pass.
		if ticks < 3 {
			t.Fatalf("converged in %d ticks; expected the budget to spread it over >= 3", ticks)
		}
		if got := c.GCSweepBacklog(); got != 0 {
			t.Fatalf("backlog after convergence = %d, want 0", got)
		}
	})

	t.Run("BoundedTickDefersWorkWithoutSurfacingDeadData", func(t *testing.T) {
		c := setupDecayed()
		c.SetGCEdgeBudget(2)

		z, _ := c.flush() // one bounded tick
		if z == 0 || z >= tails {
			t.Fatalf("one bounded tick removed %d edges; want a partial 0 < n < %d", z, tails)
		}
		if c.edges.count() == 0 {
			t.Fatal("one bounded tick reclaimed everything; budget not enforced")
		}
		if c.GCSweepBacklog() == 0 {
			t.Fatal("backlog = 0 after a partial tick; want pending work")
		}
		// Despite deferred physical cleanup, no decayed edge is visible through
		// point reads or scans.
		for i := 0; i < tails; i++ {
			tk := fmt.Sprintf("t%02d", i)
			if _, _, ok := c.GetEdgeDetail(tk, "h"); ok {
				t.Fatalf("GetEdgeDetail(%s,h) surfaced a decayed edge before its sweep", tk)
			}
		}
		if got := collectScan(c, "t", ""); len(got) != 0 {
			t.Fatalf("ScanEdgesByPrefix surfaced decayed edges before sweep: %v", got)
		}
	})

	t.Run("DanglingEdgesReclaimedIncrementally", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnablePrefixIndex(identityExtract)
		live := time.Now().Add(time.Minute)
		c.PutVertexWithExpiration("h", "head", live)
		for i := 0; i < tails; i++ {
			tk := fmt.Sprintf("t%02d", i)
			c.PutVertexWithExpiration(tk, "tail", live)
			c.AddEdgeWithExpiration(tk, "h", 1, live) // live edge
		}
		if !c.DeleteVertex("h") {
			t.Fatal("DeleteVertex(h) reported false")
		}
		c.SetGCEdgeBudget(2)

		var totalDangling, ticks int
		for c.edges.count() > 0 {
			ticks++
			if ticks > tails+2 {
				t.Fatalf("dangling sweep did not converge; remaining=%d", c.edges.count())
			}
			_, d := c.flush()
			if d > 2 {
				t.Fatalf("tick %d removed dangling=%d, exceeds budget of 2 tails", ticks, d)
			}
			totalDangling += d
		}
		if totalDangling != tails {
			t.Fatalf("cumulative dangling removals = %d, want %d", totalDangling, tails)
		}
		if ticks < 3 {
			t.Fatalf("dangling sweep converged in %d ticks; expected >= 3 under budget 2", ticks)
		}
	})

	t.Run("TombstoneAndVertexHLCSweptEachTickRegardlessOfBudget", func(t *testing.T) {
		c := setupDecayed() // many tails => edge sweep is bounded at budget 1
		c.SetGCEdgeBudget(1)
		past := time.Now().Add(-time.Millisecond)
		shortExp := time.Now().Add(50 * time.Millisecond)

		if !c.PutVertexWithExpirationHLC("hlc:expired", "x", shortExp, hlc.Timestamp{WallNs: 1}) {
			t.Fatal("PutVertexWithExpirationHLC reported false")
		}
		c.DeleteVertexHLC("tomb:old", hlc.Timestamp{WallNs: 2}, past)
		if c.VertexHLCCount() != 1 || len(c.vertexTombstones) != 1 {
			t.Fatalf("precondition: hlc=%d tombstones=%d, want 1/1", c.VertexHLCCount(), len(c.vertexTombstones))
		}
		time.Sleep(100 * time.Millisecond)
		// Drop the expired HLC vertex so the stale-HLC sweep has a dead key to
		// reconcile against the vertex cache.
		c.vertices.Flush()

		// A single budget-1 tick cannot sweep all edges (proves it is bounded)...
		c.flush()
		if c.edges.count() == 0 {
			t.Fatal("budget-1 tick reclaimed all edges; not bounded")
		}
		// ...yet the tombstone and vertexHLC sweeps ran in full on that tick.
		if got := c.VertexHLCCount(); got != 0 {
			t.Fatalf("stale vertexHLC after one bounded tick = %d, want 0", got)
		}
		if got := len(c.vertexTombstones); got != 0 {
			t.Fatalf("expired tombstones after one bounded tick = %d, want 0", got)
		}
	})

	t.Run("DisablingBudgetRestoresFullSweepAndClearsCursor", func(t *testing.T) {
		c := setupDecayed()
		c.SetGCEdgeBudget(2)
		c.flush() // partial — leaves a cursor + backlog
		if c.GCSweepBacklog() == 0 {
			t.Fatal("expected pending backlog after a partial tick")
		}
		c.SetGCEdgeBudget(0) // disable mid-cycle
		if got := c.GCSweepBacklog(); got != 0 {
			t.Fatalf("backlog after disabling budget = %d, want 0 (cursor cleared)", got)
		}
		if z, _ := c.flush(); c.edges.count() != 0 {
			t.Fatalf("full sweep left %d edges (removed %d this tick)", c.edges.count(), z)
		}
	})
}

// TestGraphCache_GCLivenessMemoScope pins the #839 contract that the
// endpoint-liveness memo lives for exactly ONE flush call: a vertex deleted
// between budgeted ticks must be seen as dead by the next tick's sweep, and
// a full (unbudgeted) sweep after a delete reclaims every dangling edge in a
// single call — including edges into a shared, formerly-memoized head.
func TestGraphCache_GCLivenessMemoScope(t *testing.T) {
	t.Run("full sweep after delete reclaims shared-head fan-in at once", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		exp := time.Now().Add(time.Minute)
		for _, tail := range []string{"a", "b", "c"} {
			c.AddEdgeWithExpiration(tail, "hub", 1, exp)
		}
		if !c.DeleteVertex("hub") {
			t.Fatal("DeleteVertex(hub) = false")
		}
		zero, dangling := c.flush()
		if zero != 0 || dangling != 3 {
			t.Fatalf("flush = (%d, %d), want (0, 3)", zero, dangling)
		}
		if got := c.EdgeCount(); got != 0 {
			t.Fatalf("EdgeCount = %d, want 0", got)
		}
	})

	t.Run("budgeted ticks never keep a mid-cycle delete alive", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		exp := time.Now().Add(time.Minute)
		c.AddEdgeWithExpiration("a", "hub", 1, exp)
		c.AddEdgeWithExpiration("b", "hub", 1, exp)
		c.SetGCEdgeBudget(1)

		// Tick 1 sweeps one tail while hub is live: nothing is reclaimed.
		if zero, dangling := c.flush(); zero != 0 || dangling != 0 {
			t.Fatalf("tick1 = (%d, %d), want (0, 0)", zero, dangling)
		}

		if !c.DeleteVertex("hub") {
			t.Fatal("DeleteVertex(hub) = false")
		}

		// The remaining ticks — finishing this cycle and running the next
		// full cycle — must reclaim BOTH dangling edges. A memo leaking
		// across flush calls would keep hub "live" and strand them.
		removed := 0
		for i := 0; i < 4; i++ {
			_, dangling := c.flush()
			removed += dangling
		}
		if removed != 2 {
			t.Fatalf("dangling reclaimed across ticks = %d, want 2", removed)
		}
		if got := c.EdgeCount(); got != 0 {
			t.Fatalf("EdgeCount = %d, want 0", got)
		}
	})
}

// BenchmarkGCFlushDanglingSweep measures one full GC edge sweep over a graph
// whose hub endpoints were just deleted — the workload #839's per-flush
// liveness memo targets (high fan-out tails plus popular shared heads).
func BenchmarkGCFlushDanglingSweep(b *testing.B) {
	const tails, headsPerTail = 500, 20
	exp := time.Now().Add(time.Hour)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		c := NewGraphCache[string, string](time.Hour)
		for t := 0; t < tails; t++ {
			tail := "tail-" + strconv.Itoa(t)
			for h := 0; h < headsPerTail; h++ {
				c.AddEdgeWithExpiration(tail, "head-"+strconv.Itoa(h), 1, exp)
			}
		}
		// Delete every shared head so the sweep reclaims all edges.
		for h := 0; h < headsPerTail; h++ {
			c.DeleteVertex("head-" + strconv.Itoa(h))
		}
		b.StartTimer()
		if _, dangling := c.flush(); dangling != tails*headsPerTail {
			b.Fatalf("dangling = %d, want %d", dangling, tails*headsPerTail)
		}
	}
}

// TestGraphCache_GCStress is an opt-in host-only measurement. It is skipped
// during ordinary go test; testbed/bench/gc_stress.sh supplies its environment
// and writes content-free artifacts outside the repository. The measured
// duration is GraphCache.Watch's complete tick, not a standalone flush or an
// RPC latency proxy.
func TestGraphCache_GCStress(t *testing.T) {
	out := os.Getenv("LANTERN_GC_STRESS_OUT")
	if out == "" {
		t.Skip("set LANTERN_GC_STRESS_OUT to run target-scale GC stress")
	}
	vertices := gcStressEnvInt(t, "LANTERN_GC_STRESS_VERTICES", 100_000)
	degree := gcStressEnvInt(t, "LANTERN_GC_STRESS_DEGREE", 32)
	ticks := gcStressEnvInt(t, "LANTERN_GC_STRESS_TICKS", 100)
	intervalMS := gcStressEnvInt(t, "LANTERN_GC_STRESS_INTERVAL_MS", 1_000)
	budget := gcStressEnvInt(t, "LANTERN_GC_STRESS_BUDGET", 0)
	shape := os.Getenv("LANTERN_GC_STRESS_SHAPE")
	if shape == "" {
		shape = "uniform"
	}
	if vertices < 100 || degree < 2 || degree+6 >= vertices || ticks < 10 || intervalMS < 1 || budget < 0 || (shape != "uniform" && shape != "hub") {
		t.Fatalf("invalid GC stress parameters: vertices=%d degree=%d ticks=%d interval_ms=%d budget=%d shape=%q", vertices, degree, ticks, intervalMS, budget, shape)
	}
	sha := os.Getenv("LANTERN_GC_STRESS_SHA")
	if sha == "" {
		t.Fatal("LANTERN_GC_STRESS_SHA must identify the measured build")
	}
	interval := time.Duration(intervalMS) * time.Millisecond
	cache := NewGraphCache[string, string](time.Hour)
	cache.SetGCEdgeBudget(budget)
	keys := make([]string, vertices)
	vertexBatch := make([]VertexItem[string, string], 0, 1000)
	liveUntil := time.Now().Add(time.Hour)
	for i := range keys {
		keys[i] = makeKey("https://example.com", i, 80)
		vertexBatch = append(vertexBatch, VertexItem[string, string]{Key: keys[i], Value: "", Expiration: liveUntil})
		if len(vertexBatch) == cap(vertexBatch) {
			if err := cache.PutVerticesWithExpiration(vertexBatch); err != nil {
				t.Fatal(err)
			}
			vertexBatch = vertexBatch[:0]
		}
	}
	if len(vertexBatch) > 0 {
		if err := cache.PutVerticesWithExpiration(vertexBatch); err != nil {
			t.Fatal(err)
		}
	}

	// The hub shape keeps approximately the same edge count as uniform, but
	// puts 100k heads under one tail. A tail budget cannot cap that tick's
	// edge scan, and the comparison makes the limitation observable.
	edgeBatch := make([]EdgeItem[string], 0, 10_000)
	seededEdges := 0
	for tail := range keys {
		fanOut := degree
		if shape == "hub" {
			fanOut = degree - 1
			if tail == 0 {
				fanOut = vertices
			}
		}
		for offset := 1; offset <= fanOut; offset++ {
			head := (tail + offset) % vertices
			edgeBatch = append(edgeBatch, EdgeItem[string]{Tail: keys[tail], Head: keys[head], Weight: 1, Expiration: liveUntil})
			seededEdges++
			if len(edgeBatch) == cap(edgeBatch) {
				cache.AddEdgesWithExpiration(edgeBatch)
				edgeBatch = edgeBatch[:0]
			}
		}
	}
	if len(edgeBatch) > 0 {
		cache.AddEdgesWithExpiration(edgeBatch)
	}
	if got := cache.EdgeCount(); got != seededEdges {
		t.Fatalf("seeded edges = %d, want %d", got, seededEdges)
	}

	// Five absolute deadlines keep expiry work spread across different
	// Watch ticks. Each group adds short-lived weight to already-live edges
	// and short-only edge buckets. A small deterministic sample of the latter
	// tracks physical reclamation without perturbing the timed sweep.
	total := time.Duration(ticks) * interval
	contribsPerGroup := max(1, vertices/10)
	zeroPerGroup := max(1, vertices/200)
	probesPerGroup := min(20, zeroPerGroup)
	type probe struct {
		tail, head string
		expires    time.Time
		reclaimed  bool
	}
	probes := make([]probe, 0, 5*probesPerGroup)
	for group := range 5 {
		deadline := time.Now().Add(time.Duration(20+10*group) * total / 100)
		for n := range contribsPerGroup {
			tail := 1 + (group*contribsPerGroup+n)%(vertices/2)
			cache.AddEdgeWithExpiration(keys[tail], keys[(tail+1)%vertices], 1, deadline)
		}
		for n := range zeroPerGroup {
			tail := 1 + (group*zeroPerGroup+n)%(vertices/2)
			head := (tail + degree + 5) % vertices
			cache.AddEdgeWithExpiration(keys[tail], keys[head], 1, deadline)
			if n < probesPerGroup {
				probes = append(probes, probe{tail: keys[tail], head: keys[head], expires: deadline})
			}
		}
	}

	type sample struct {
		FinishedAt time.Time    `json:"finished_at"`
		DurationNS int64        `json:"duration_ns"`
		Sweep      GCSweepStats `json:"sweep"`
	}
	tickCh := make(chan sample, 1)
	cache.SetGCHooks(nil, func(d time.Duration) {
		tickCh <- sample{FinishedAt: time.Now().UTC(), DurationNS: d.Nanoseconds(), Sweep: cache.LastGCSweepStats()}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	started := time.Now()
	go func() {
		cache.Watch(ctx, interval)
		close(done)
	}()
	// Delete two disjoint one-percent vertex bands. Their incoming edges
	// survive physically until GC and exercise the dangling sweep.
	deletionsDone := make(chan struct{})
	go func() {
		defer close(deletionsDone)
		for _, fraction := range []int{30, 50} {
			deadline := started.Add(time.Duration(fraction) * total / 100)
			if wait := time.Until(deadline); wait > 0 {
				time.Sleep(wait)
			}
			band := 0
			if fraction == 50 {
				band = 1
			}
			width := max(1, vertices/100)
			for i := vertices - (band+1)*width; i < vertices-band*width; i++ {
				cache.DeleteVertex(keys[i])
			}
		}
	}()

	samples := make([]sample, 0, ticks)
	var scannedEdges, compactedLive, expiredContribs, zeroRemoved, danglingRemoved int64
	maxBacklog := 0
	var maxReclaimLag time.Duration
	for range ticks {
		select {
		case got := <-tickCh:
			samples = append(samples, got)
			scannedEdges += int64(got.Sweep.ScannedEdges)
			compactedLive += int64(got.Sweep.CompactedContributionsInLiveBuckets)
			expiredContribs += int64(got.Sweep.ExpiredContributions)
			zeroRemoved += int64(got.Sweep.ZeroRemoved)
			danglingRemoved += int64(got.Sweep.DanglingRemoved)
			maxBacklog = max(maxBacklog, got.Sweep.BacklogTails)
			for i := range probes {
				p := &probes[i]
				if p.reclaimed || got.FinishedAt.Before(p.expires) {
					continue
				}
				if _, exists := cache.edges.lastPutHLC(p.tail, p.head); !exists {
					p.reclaimed = true
					maxReclaimLag = max(maxReclaimLag, got.FinishedAt.Sub(p.expires))
				}
			}
		case <-time.After(2 * total):
			cancel()
			<-done
			t.Fatal("GC stress timed out waiting for a tick")
		}
	}
	cancel()
	<-done
	<-deletionsDone
	missingProbes := 0
	for _, p := range probes {
		if !p.reclaimed {
			missingProbes++
		}
	}
	durations := make([]int64, len(samples))
	for i, s := range samples {
		durations[i] = s.DurationNS
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	quantile := func(p int) int64 {
		index := (p*len(durations) + 99) / 100 // nearest rank
		return durations[index-1]
	}
	artifact := struct {
		SHA                  string        `json:"sha"`
		GoVersion            string        `json:"go_version"`
		GOOS                 string        `json:"goos"`
		GOARCH               string        `json:"goarch"`
		CPUs                 int           `json:"cpus"`
		Vertices             int           `json:"vertices"`
		SeededEdges          int           `json:"seeded_edges"`
		Shape                string        `json:"shape"`
		Degree               int           `json:"degree"`
		BudgetTails          int           `json:"budget_tails"`
		IntervalMS           int           `json:"interval_ms"`
		Samples              []sample      `json:"samples"`
		P95NS                int64         `json:"gc_p95_ns"`
		P99NS                int64         `json:"gc_p99_ns"`
		MaxNS                int64         `json:"gc_max_ns"`
		ScannedEdges         int64         `json:"scanned_edges"`
		ExpiredContributions int64         `json:"expired_contributions"`
		CompactedLive        int64         `json:"compacted_contributions_in_live_edges"`
		ZeroRemoved          int64         `json:"zero_edges_removed"`
		DanglingRemoved      int64         `json:"dangling_edges_removed"`
		MaxBacklogTails      int           `json:"max_backlog_tails"`
		MaxReclaimLag        time.Duration `json:"max_probe_reclaim_lag_ns"`
		UnreclaimedProbes    int           `json:"unreclaimed_probes"`
	}{sha, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), vertices, seededEdges,
		shape, degree, budget, intervalMS, samples, quantile(95), quantile(99), durations[len(durations)-1],
		scannedEdges, expiredContribs, compactedLive, zeroRemoved, danglingRemoved, maxBacklog,
		maxReclaimLag, missingProbes}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("GC stress: shape=%s budget=%d p99=%s scanned=%d compacted_live=%d backlog=%d unreclaimed_probes=%d artifact=%s",
		shape, budget, time.Duration(quantile(99)), scannedEdges, compactedLive, maxBacklog, missingProbes, out)
}

func gcStressEnvInt(t *testing.T, key string, def int) int {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		return def
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return n
}
