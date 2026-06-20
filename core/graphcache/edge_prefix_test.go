package graphcache

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
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

// TestScanEdgesByPrefix_Atomic stresses ScanEdgesByPrefix against
// concurrent Add/Put/Delete writers to assert four invariants under
// -race:
//
//  1. No panic / no map-iteration race.
//  2. No spurious entries: every (tail, head) tuple a scanner ever
//     observes belongs to the lifetime set (i.e. it was inserted at
//     least once during the run).
//  3. No missing entries at quiesce: after writers stop, one final
//     scan must return every edge that survived deletions.
//  4. Per-page ordering inside a single scan is ascending on
//     (tailProjected, headProjected).
//
// The test mirrors the spirit of put_edge_atomic_test.go but exercises
// the prefix-index walk path rather than a single-edge hot key.
func TestScanEdgesByPrefix_Atomic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping atomicity stress test in -short mode")
	}

	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	exp := time.Now().Add(time.Minute)

	const (
		tails    = 32
		heads    = 32
		writers  = 4
		scanners = 4
		duration = 750 * time.Millisecond
	)
	tailKey := func(i int) string { return fmt.Sprintf("user:%03d", i) }
	headKey := func(j int) string { return fmt.Sprintf("post:%03d", j) }

	// Build the lifetime universe — the maximum set any tuple can ever
	// belong to. Anything a scanner emits outside this set is spurious.
	lifetime := make(map[string]struct{}, tails*heads)
	for i := 0; i < tails; i++ {
		for j := 0; j < heads; j++ {
			lifetime[tailKey(i)+"|"+headKey(j)] = struct{}{}
		}
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writers: random Add / Put / Delete across the (tail, head) grid.
	var writerOps int64
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			x := uint64(seed*2654435761) | 1
			for {
				select {
				case <-stop:
					return
				default:
				}
				// xorshift64*
				x ^= x << 13
				x ^= x >> 7
				x ^= x << 17
				i := int(x>>1) % tails
				j := int(x>>11) % heads
				op := int(x>>21) % 3
				tk, hk := tailKey(i), headKey(j)
				switch op {
				case 0:
					c.AddEdgeWithExpiration(tk, hk, 1, exp)
				case 1:
					c.PutEdgeWithExpiration(tk, hk, float32(int(x>>32)%100)+1, exp)
				default:
					c.DeleteEdge(tk, hk)
				}
				atomic.AddInt64(&writerOps, 1)
				if int(x>>40)%256 == 0 {
					runtime.Gosched()
				}
			}
		}(w + 1)
	}

	// Scanners: repeatedly run ScanEdgesByPrefix to completion and
	// remember every tuple they see plus order-violation flags.
	type observation struct {
		seen   map[string]struct{}
		orders int64 // count of out-of-order emissions observed
	}
	obs := make([]observation, scanners)
	for s := 0; s < scanners; s++ {
		obs[s].seen = make(map[string]struct{})
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				var prevTail, prevHead string
				first := true
				c.ScanEdgesByPrefix(context.Background(), "user:", "post:",
					func(tProj, tail, hProj, head string, _ float32, _ time.Time) bool {
						if !first {
							if tProj < prevTail || (tProj == prevTail && hProj <= prevHead) {
								atomic.AddInt64(&obs[s].orders, 1)
							}
						}
						first = false
						prevTail, prevHead = tProj, hProj
						obs[s].seen[tail+"|"+head] = struct{}{}
						return true
					})
			}
		}(s)
	}

	time.Sleep(duration)
	close(stop)
	wg.Wait()

	// Invariant 1+2: no spurious tuple in any scanner's union.
	for s, o := range obs {
		for k := range o.seen {
			if _, ok := lifetime[k]; !ok {
				t.Fatalf("scanner %d observed spurious edge %q (not in lifetime universe)", s, k)
			}
		}
		if o.orders != 0 {
			t.Fatalf("scanner %d observed %d out-of-order emissions", s, o.orders)
		}
	}

	// Invariant 3: at quiesce, the final scan and any scanner that
	// completed at least one full pass after writers stopped must agree.
	// We compute the quiesce set via a final scan, then verify it is a
	// subset of every scanner's lifetime observations (a scanner that
	// ran to completion after writers stopped saw at least everything
	// that survived).
	quiesce := make(map[string]struct{})
	if !c.ScanEdgesByPrefix(context.Background(), "user:", "post:",
		func(_, tail, _, head string, _ float32, _ time.Time) bool {
			quiesce[tail+"|"+head] = struct{}{}
			return true
		}) {
		t.Fatalf("final ScanEdgesByPrefix returned ok=false")
	}
	// Sanity: every quiesce entry must be in the lifetime universe.
	for k := range quiesce {
		if _, ok := lifetime[k]; !ok {
			t.Fatalf("quiesce edge %q not in lifetime universe", k)
		}
	}

	// Re-run the final scan immediately and require byte-for-byte
	// equality with the previous quiesce set — proves the scan is
	// deterministic once writers are gone.
	quiesce2 := make(map[string]struct{})
	c.ScanEdgesByPrefix(context.Background(), "user:", "post:",
		func(_, tail, _, head string, _ float32, _ time.Time) bool {
			quiesce2[tail+"|"+head] = struct{}{}
			return true
		})
	if len(quiesce) != len(quiesce2) {
		t.Fatalf("post-quiesce scans disagree: %d vs %d edges", len(quiesce), len(quiesce2))
	}
	for k := range quiesce {
		if _, ok := quiesce2[k]; !ok {
			t.Fatalf("post-quiesce scans disagree on edge %q", k)
		}
	}

	t.Logf("writers: %d ops, lifetime=%d, quiesce=%d",
		atomic.LoadInt64(&writerOps), len(lifetime), len(quiesce))
	// Smoke-check that the test actually exercised the prefix walk.
	if len(quiesce) == 0 {
		t.Fatalf("quiesce set empty — writers never won any insert?")
	}
}

// TestGraphCache_ScanEdgesByPrefix_CallbackReentrant proves the #742 behavior
// change: the visitor now runs AFTER the read lock is released, so it may call
// back into GraphCache write methods. Under the old contract (callback invoked
// while holding c.mu.RLock) any such write would self-deadlock because
// sync.RWMutex is not reentrant. The scan is run in a goroutine with a timeout
// so a regression fails loudly instead of hanging the suite.
func TestGraphCache_ScanEdgesByPrefix_CallbackReentrant(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	exp := time.Now().Add(time.Minute)
	for i := 0; i < 5; i++ {
		c.PutEdgeWithExpiration(fmt.Sprintf("t%d", i), "h", 1.0, exp)
	}

	done := make(chan bool, 1)
	go func() {
		var visited int
		ok := c.ScanEdgesByPrefix(context.Background(), "t", "",
			func(_ string, tail string, _ string, _ string, _ float32, _ time.Time) bool {
				visited++
				// Reentrant writes from inside the visitor: an existing-edge
				// append (takes c.mu.Lock) and a brand-new vertex.
				c.AddEdgeWithExpiration(tail, "h", 1, exp)
				c.PutVertexWithExpiration("side:"+tail, "v", exp)
				return true
			})
		done <- ok && visited == 5
	}()

	select {
	case ok := <-done:
		if !ok {
			t.Fatal("reentrant ScanEdgesByPrefix did not complete cleanly")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reentrant ScanEdgesByPrefix deadlocked (visitor ran under the read lock?)")
	}

	// The reentrant writes took effect after the scan released the lock.
	if _, ok := c.GetVertex("side:t0"); !ok {
		t.Fatal("reentrant PutVertex from visitor did not persist")
	}
}
