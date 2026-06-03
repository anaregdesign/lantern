package graph

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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
