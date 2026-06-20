package graphcache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

func TestGraphCache_GCFlushMaintainsIndexesWatermarksAndDanglingEdges(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(identityExtract)
	c.EnableSearchIndex(textExtract)

	now := time.Now()
	liveExp := now.Add(time.Minute)
	expired := now.Add(-time.Millisecond)

	if !c.PutVertexWithExpirationHLC("expired:vertex", "expired searchable payload", expired, hlc.Timestamp{WallNs: 1}) {
		t.Fatal("PutVertexWithExpirationHLC(expired:vertex) reported false")
	}
	if got := c.VertexHLCCount(); got != 1 {
		t.Fatalf("VertexHLCCount before flush = %d, want 1", got)
	}
	if got := c.CountByPrefix("expired:"); got != 1 {
		t.Fatalf("CountByPrefix(expired:) before vertex flush = %d, want 1", got)
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
	if got := collectScan(c, "tail", "head"); len(got) != 1 {
		t.Fatalf("ScanEdgesByPrefix before dangling sweep = %v, want one dangling edge still present", got)
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

		if !c.PutVertexWithExpirationHLC("hlc:expired", "x", past, hlc.Timestamp{WallNs: 1}) {
			t.Fatal("PutVertexWithExpirationHLC reported false")
		}
		c.DeleteVertexHLC("tomb:old", hlc.Timestamp{WallNs: 2}, past)
		if c.VertexHLCCount() != 1 || len(c.vertexTombstones) != 1 {
			t.Fatalf("precondition: hlc=%d tombstones=%d, want 1/1", c.VertexHLCCount(), len(c.vertexTombstones))
		}
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
