package graphcache_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

// TestAddEdgesWithExpiration_AtomicNeighborSnapshot verifies that a
// concurrent reader using Neighbor (which holds the cache RLock for the
// duration of a single call) observes either the pre-batch state (no
// edges) or the post-batch state (all batched edges), never a partial
// fan-in.
func TestAddEdgesWithExpiration_AtomicNeighborSnapshot(t *testing.T) {
	const fanOut = 128
	c := graphcache.NewGraphCache[string, int](time.Minute)

	exp := time.Now().Add(time.Minute)
	c.PutVertexWithExpiration("s", 0, exp)

	addBatch := make([]graphcache.EdgeItem[string], fanOut)
	delBatch := make([]graphcache.EdgeKey[string], fanOut)
	for i := 0; i < fanOut; i++ {
		head := "h" + itoa(i)
		addBatch[i] = graphcache.EdgeItem[string]{Tail: "s", Head: head, Weight: 1, Expiration: exp}
		delBatch[i] = graphcache.EdgeKey[string]{Tail: "s", Head: head}
	}

	var (
		stop      atomic.Bool
		readerWG  sync.WaitGroup
		violation atomic.Int64
		reads     atomic.Int64
	)

	readerWG.Add(4)
	for r := 0; r < 4; r++ {
		go func() {
			defer readerWG.Done()
			for !stop.Load() {
				g := c.Neighbor("s", 1, fanOut+1, false, false, nil)
				reads.Add(1)
				if g == nil {
					continue
				}
				count := 0
				for _, heads := range g.Edges {
					count += len(heads)
				}
				if count != 0 && count != fanOut {
					violation.Add(1)
				}
			}
		}()
	}

	for i := 0; i < 100; i++ {
		c.DeleteEdges(delBatch)
		c.AddEdgesWithExpiration(addBatch)
	}

	stop.Store(true)
	readerWG.Wait()

	if reads.Load() == 0 {
		t.Fatal("reader never executed; test is meaningless")
	}
	if v := violation.Load(); v != 0 {
		t.Fatalf("reader observed %d intermediate snapshots out of %d reads; batch is not atomic under Neighbor", v, reads.Load())
	}
}

// TestDeleteEdges_AtomicNeighborSnapshot is the symmetric test: every
// Neighbor call sees either all batched edges or none.
func TestDeleteEdges_AtomicNeighborSnapshot(t *testing.T) {
	const fanOut = 128
	c := graphcache.NewGraphCache[string, int](time.Minute)

	exp := time.Now().Add(time.Minute)
	c.PutVertexWithExpiration("s", 0, exp)

	addBatch := make([]graphcache.EdgeItem[string], fanOut)
	delBatch := make([]graphcache.EdgeKey[string], fanOut)
	for i := 0; i < fanOut; i++ {
		head := "h" + itoa(i)
		addBatch[i] = graphcache.EdgeItem[string]{Tail: "s", Head: head, Weight: 1, Expiration: exp}
		delBatch[i] = graphcache.EdgeKey[string]{Tail: "s", Head: head}
	}

	var (
		stop      atomic.Bool
		readerWG  sync.WaitGroup
		violation atomic.Int64
		reads     atomic.Int64
	)

	readerWG.Add(4)
	for r := 0; r < 4; r++ {
		go func() {
			defer readerWG.Done()
			for !stop.Load() {
				g := c.Neighbor("s", 1, fanOut+1, false, false, nil)
				reads.Add(1)
				if g == nil {
					continue
				}
				count := 0
				for _, heads := range g.Edges {
					count += len(heads)
				}
				if count != 0 && count != fanOut {
					violation.Add(1)
				}
			}
		}()
	}

	for i := 0; i < 100; i++ {
		c.AddEdgesWithExpiration(addBatch)
		c.DeleteEdges(delBatch)
	}

	stop.Store(true)
	readerWG.Wait()

	if reads.Load() == 0 {
		t.Fatal("reader never executed; test is meaningless")
	}
	if v := violation.Load(); v != 0 {
		t.Fatalf("reader observed %d intermediate snapshots out of %d reads; batch delete is not atomic under Neighbor", v, reads.Load())
	}
}

// TestPutEdgesWithExpiration_NoTransientNotFound verifies the per-edge
// invariant of PutEdges: callers replacing edges in a batch never expose a
// transient NotFound where a concurrent GetEdge sees the edge missing
// between the per-item delete and re-add.
func TestPutEdgesWithExpiration_NoTransientNotFound(t *testing.T) {
	const batchSize = 128
	c := graphcache.NewGraphCache[string, int](time.Minute)

	items := make([]graphcache.EdgeItem[string], batchSize)
	keys := make([]graphcache.EdgeKey[string], batchSize)
	for i := 0; i < batchSize; i++ {
		tail, head := "t"+itoa(i), "h"+itoa(i)
		items[i] = graphcache.EdgeItem[string]{Tail: tail, Head: head, Weight: 1, Expiration: time.Now().Add(time.Minute)}
		keys[i] = graphcache.EdgeKey[string]{Tail: tail, Head: head}
	}
	c.AddEdgesWithExpiration(items)

	var (
		stop     atomic.Bool
		readerWG sync.WaitGroup
		missing  atomic.Int64
	)

	readerWG.Add(4)
	for r := 0; r < 4; r++ {
		go func() {
			defer readerWG.Done()
			for !stop.Load() {
				for _, k := range keys {
					if _, _, ok := c.GetEdgeDetail(k.Tail, k.Head); !ok {
						missing.Add(1)
					}
				}
			}
		}()
	}

	for i := 0; i < 100; i++ {
		c.PutEdgesWithExpiration(items)
	}

	stop.Store(true)
	readerWG.Wait()

	if m := missing.Load(); m != 0 {
		t.Fatalf("reader observed %d transient NotFound during PutEdges; batch replace is not atomic", m)
	}
}

// TestBatchAPIs_ReturnCounts spot-checks the bookkeeping returned by
// DeleteVertices / DeleteEdges (count of entries actually removed).
func TestBatchAPIs_ReturnCounts(t *testing.T) {
	c := graphcache.NewGraphCache[string, int](time.Minute)
	exp := time.Now().Add(time.Minute)

	c.PutVerticesWithExpiration([]graphcache.VertexItem[string, int]{
		{Key: "a", Value: 1, Expiration: exp},
		{Key: "b", Value: 2, Expiration: exp},
		{Key: "c", Value: 3, Expiration: exp},
	})
	if n := c.DeleteVertices([]string{"a", "b", "missing"}); n != 2 {
		t.Fatalf("DeleteVertices: got %d, want 2", n)
	}

	c.AddEdgesWithExpiration([]graphcache.EdgeItem[string]{
		{Tail: "x", Head: "y", Weight: 1, Expiration: exp},
		{Tail: "y", Head: "z", Weight: 1, Expiration: exp},
	})
	got := c.DeleteEdges([]graphcache.EdgeKey[string]{
		{Tail: "x", Head: "y"},
		{Tail: "nope", Head: "nope"},
	})
	if got != 1 {
		t.Fatalf("DeleteEdges: got %d, want 1", got)
	}
}

// TestDeleteVertices_ClearsPrefixAndSearchIndexes verifies the batched
// DeleteVertices (#738) removes deleted keys from the prefix radix and the
// search index in addition to the vertex cache, while leaving untouched keys
// fully indexed. Content words share no bigrams (NGram{N:2} analyzer) so a
// search match is unambiguous.
func TestDeleteVertices_ClearsPrefixAndSearchIndexes(t *testing.T) {
	c := graphcache.NewGraphCache[string, string](time.Minute)
	c.EnablePrefixIndex(func(k string) string { return k })
	c.EnableSearchIndex(func(v string) search.Document { return search.Text(v) })

	c.PutVertex("ns:a", "alpha")
	c.PutVertex("ns:b", "bravo")
	c.PutVertex("keep:1", "zulu")

	if n := c.DeleteVertices([]string{"ns:a", "ns:b", "missing"}); n != 2 {
		t.Fatalf("DeleteVertices = %d, want 2", n)
	}

	if got := c.CountByPrefix("ns:"); got != 0 {
		t.Fatalf("CountByPrefix(ns:) after DeleteVertices = %d, want 0", got)
	}
	if got := c.CountByPrefix("keep:"); got != 1 {
		t.Fatalf("CountByPrefix(keep:) = %d, want 1", got)
	}
	for _, term := range []string{"alpha", "bravo"} {
		if got := c.SearchVertices(term, 10, ""); got != nil {
			t.Fatalf("SearchVertices(%q) after DeleteVertices = non-nil, want nil", term)
		}
	}
	results := c.SearchVertices("zulu", 10, "")
	if len(results) != 1 || results[0].ID != "keep:1" {
		t.Fatalf(`SearchVertices("zulu") = %v, want [keep:1]`, results)
	}
}

// TestAddEdgesWithExpirationContrib_Dedup verifies the #588 idempotency
// contract: a non-zero ContribID makes an additive contribution apply at
// most once (so a retried batch is an exact no-op), while a zero ContribID
// preserves the legacy additive (sum-on-replay) behavior. The deduped count
// reports exactly the items suppressed by a matching live ContribID.
func TestAddEdgesWithExpirationContrib_Dedup(t *testing.T) {
	exp := time.Now().Add(time.Minute)

	t.Run("same id twice contributes once", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, int](time.Minute)
		var id graphcache.ContribID
		id[0], id[23] = 0xAB, 0x01

		batch := []graphcache.EdgeItem[string]{
			{Tail: "s", Head: "h", Weight: 2.5, Expiration: exp, ContribID: id},
		}
		if deduped := c.AddEdgesWithExpirationContrib(batch); deduped != 0 {
			t.Fatalf("first apply: deduped = %d, want 0", deduped)
		}
		// Replay the identical batch: must be an exact no-op.
		if deduped := c.AddEdgesWithExpirationContrib(batch); deduped != 1 {
			t.Fatalf("replay: deduped = %d, want 1", deduped)
		}
		w, ok := c.GetWeight("s", "h")
		if !ok {
			t.Fatal("edge missing after contrib writes")
		}
		if w != 2.5 {
			t.Fatalf("weight = %v, want 2.5 (contribution must apply exactly once)", w)
		}
	})

	t.Run("zero id stays additive", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, int](time.Minute)
		batch := []graphcache.EdgeItem[string]{
			{Tail: "s", Head: "h", Weight: 2, Expiration: exp}, // ContribID zero
		}
		if deduped := c.AddEdgesWithExpirationContrib(batch); deduped != 0 {
			t.Fatalf("first apply: deduped = %d, want 0", deduped)
		}
		if deduped := c.AddEdgesWithExpirationContrib(batch); deduped != 0 {
			t.Fatalf("replay: deduped = %d, want 0 (zero id must not dedup)", deduped)
		}
		if w, _ := c.GetWeight("s", "h"); w != 4 {
			t.Fatalf("weight = %v, want 4 (zero id must sum on replay)", w)
		}
	})

	t.Run("distinct ids both contribute", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, int](time.Minute)
		var id1, id2 graphcache.ContribID
		id1[0], id1[23] = 0x01, 0x01
		id2[0], id2[23] = 0x01, 0x02
		c.AddEdgesWithExpirationContrib([]graphcache.EdgeItem[string]{
			{Tail: "s", Head: "h", Weight: 1, Expiration: exp, ContribID: id1},
		})
		deduped := c.AddEdgesWithExpirationContrib([]graphcache.EdgeItem[string]{
			{Tail: "s", Head: "h", Weight: 1, Expiration: exp, ContribID: id2},
		})
		if deduped != 0 {
			t.Fatalf("distinct id: deduped = %d, want 0", deduped)
		}
		if w, _ := c.GetWeight("s", "h"); w != 2 {
			t.Fatalf("weight = %v, want 2 (distinct ids both contribute)", w)
		}
	})

	t.Run("mixed batch counts only deduped items", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, int](time.Minute)
		var id graphcache.ContribID
		id[0] = 0x7F
		c.AddEdgesWithExpirationContrib([]graphcache.EdgeItem[string]{
			{Tail: "s", Head: "a", Weight: 1, Expiration: exp, ContribID: id},
		})
		// Mix the already-seen id (deduped) with a fresh zero-id edge (applies).
		deduped := c.AddEdgesWithExpirationContrib([]graphcache.EdgeItem[string]{
			{Tail: "s", Head: "a", Weight: 1, Expiration: exp, ContribID: id},
			{Tail: "s", Head: "b", Weight: 1, Expiration: exp},
		})
		if deduped != 1 {
			t.Fatalf("mixed batch: deduped = %d, want 1", deduped)
		}
		if w, _ := c.GetWeight("s", "a"); w != 1 {
			t.Fatalf("weight s->a = %v, want 1 (deduped)", w)
		}
		if w, ok := c.GetWeight("s", "b"); !ok || w != 1 {
			t.Fatalf("weight s->b = %v ok=%v, want 1 true (applied)", w, ok)
		}
	})

	t.Run("facade preserves additive semantics", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, int](time.Minute)
		batch := []graphcache.EdgeItem[string]{
			{Tail: "s", Head: "h", Weight: 3, Expiration: exp},
		}
		c.AddEdgesWithExpiration(batch)
		c.AddEdgesWithExpiration(batch)
		if w, _ := c.GetWeight("s", "h"); w != 6 {
			t.Fatalf("facade weight = %v, want 6 (AddEdgesWithExpiration stays additive)", w)
		}
	})
}

// itoa avoids pulling in strconv just to label test keys.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	const digits = "0123456789"
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = digits[i%10]
		i /= 10
	}
	return string(buf[pos:])
}

// PutVerticesWithExpirationHLC stamps every item with the supplied HLC so the
// LOCAL batch write path participates in last-writer-wins exactly like the
// per-item apply path. A strictly-older batch is dropped per key, a brand-new
// key in the same batch still applies, and a key under a newer tombstone is
// not resurrected by an older batch put.
func TestPutVerticesWithExpirationHLC_BatchLWW(t *testing.T) {
	exp := time.Now().Add(time.Hour)
	older := hlc.Timestamp{WallNs: 1000}
	newer := hlc.Timestamp{WallNs: 2000}

	t.Run("LWW", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		c.PutVerticesWithExpirationHLC([]graphcache.VertexItem[string, string]{
			{Key: "a", Value: "newA", Expiration: exp},
			{Key: "b", Value: "newB", Expiration: exp},
		}, newer)
		c.PutVerticesWithExpirationHLC([]graphcache.VertexItem[string, string]{
			{Key: "a", Value: "staleA", Expiration: exp}, // dropped: strictly older
			{Key: "b", Value: "staleB", Expiration: exp}, // dropped: strictly older
			{Key: "c", Value: "freshC", Expiration: exp}, // applied: brand-new key
		}, older)
		for _, tc := range []struct{ key, want string }{
			{"a", "newA"}, {"b", "newB"}, {"c", "freshC"},
		} {
			if got, ok := c.GetVertex(tc.key); !ok || got != tc.want {
				t.Errorf("%s: got (%q,%v), want (%q,true) — newer HLC must win", tc.key, got, ok, tc.want)
			}
		}
	})

	t.Run("TombstoneFencesOlder", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		c.PutVerticesWithExpirationHLC([]graphcache.VertexItem[string, string]{
			{Key: "d", Value: "live", Expiration: exp},
		}, older)
		if n := c.DeleteVerticesHLC([]string{"d"}, newer, exp); n != 1 {
			t.Fatalf("delete: got %d, want 1", n)
		}
		c.PutVerticesWithExpirationHLC([]graphcache.VertexItem[string, string]{
			{Key: "d", Value: "resurrect", Expiration: exp}, // dropped: older than tombstone
		}, older)
		if _, ok := c.GetVertex("d"); ok {
			t.Errorf("older batch put resurrected a tombstoned key")
		}
	})
}

// PutEdgesWithExpirationHLC is the edge LWW sibling: a strictly-older batch is
// dropped per (tail, head), a brand-new edge in the same batch applies, and a
// newer edge tombstone fences an older batch put.
func TestPutEdgesWithExpirationHLC_BatchLWW(t *testing.T) {
	exp := time.Now().Add(time.Hour)
	older := hlc.Timestamp{WallNs: 1000}
	newer := hlc.Timestamp{WallNs: 2000}

	t.Run("LWW", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		c.PutEdgesWithExpirationHLC([]graphcache.EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 2.5, Expiration: exp},
		}, newer)
		c.PutEdgesWithExpirationHLC([]graphcache.EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 9.9, Expiration: exp}, // dropped: strictly older
			{Tail: "x", Head: "y", Weight: 1.0, Expiration: exp}, // applied: brand-new edge
		}, older)
		if got, ok := c.GetWeight("a", "b"); !ok || got != 2.5 {
			t.Errorf("a->b: got (%v,%v), want (2.5,true) — newer HLC must win", got, ok)
		}
		if got, ok := c.GetWeight("x", "y"); !ok || got != 1.0 {
			t.Errorf("x->y: got (%v,%v), want (1.0,true)", got, ok)
		}
	})

	t.Run("TombstoneFencesOlder", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		c.PutEdgesWithExpirationHLC([]graphcache.EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 1.0, Expiration: exp},
		}, older)
		if n := c.DeleteEdgesHLC([]graphcache.EdgeKey[string]{{Tail: "a", Head: "b"}}, newer, exp); n != 1 {
			t.Fatalf("delete: got %d, want 1", n)
		}
		c.PutEdgesWithExpirationHLC([]graphcache.EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 9.0, Expiration: exp}, // dropped: older than tombstone
		}, older)
		if _, ok := c.GetWeight("a", "b"); ok {
			t.Errorf("older batch put resurrected a tombstoned edge")
		}
	})
}

// AddEdgesWithExpirationContribHLC keeps additive ContribID-set semantics but
// fences each contribution by the edge tombstone at the supplied HLC: an
// older-than-tombstone contribution adds nothing (and is counted as deduped),
// an exact ContribID replay is idempotent, and two distinct contributions sum.
func TestAddEdgesWithExpirationContribHLC_TombstoneFenced(t *testing.T) {
	exp := time.Now().Add(time.Hour)
	older := hlc.Timestamp{WallNs: 1000}
	newer := hlc.Timestamp{WallNs: 2000}

	t.Run("TombstoneDropsOlderContribution", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		c.AddEdgesWithExpirationContribHLC([]graphcache.EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 1.0, Expiration: exp, ContribID: graphcache.ContribID{0: 1}},
		}, older)
		if n := c.DeleteEdgesHLC([]graphcache.EdgeKey[string]{{Tail: "a", Head: "b"}}, newer, exp); n != 1 {
			t.Fatalf("delete: got %d, want 1", n)
		}
		deduped := c.AddEdgesWithExpirationContribHLC([]graphcache.EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 5.0, Expiration: exp, ContribID: graphcache.ContribID{0: 2}},
		}, older)
		if deduped != 1 {
			t.Errorf("deduped: got %d, want 1 (tombstone-dropped)", deduped)
		}
		if _, ok := c.GetWeight("a", "b"); ok {
			t.Errorf("older contribution resurrected a tombstoned edge")
		}
	})

	t.Run("ContribIDDedup", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		items := []graphcache.EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 1.0, Expiration: exp, ContribID: graphcache.ContribID{0: 9}},
		}
		if d := c.AddEdgesWithExpirationContribHLC(items, older); d != 0 {
			t.Errorf("first apply deduped: got %d, want 0", d)
		}
		if d := c.AddEdgesWithExpirationContribHLC(items, newer); d != 1 {
			t.Errorf("replay deduped: got %d, want 1 (same ContribID is idempotent)", d)
		}
		if got, ok := c.GetWeight("a", "b"); !ok || got != 1.0 {
			t.Errorf("a->b weight: got (%v,%v), want (1.0,true) — replay must not double-count", got, ok)
		}
	})

	t.Run("AdditiveMerge", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		c.AddEdgesWithExpirationContribHLC([]graphcache.EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 1.0, Expiration: exp, ContribID: graphcache.ContribID{0: 1}},
		}, older)
		c.AddEdgesWithExpirationContribHLC([]graphcache.EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 2.0, Expiration: exp, ContribID: graphcache.ContribID{0: 2}},
		}, newer)
		if got, ok := c.GetWeight("a", "b"); !ok || got != 3.0 {
			t.Errorf("a->b weight: got (%v,%v), want (3.0,true) — distinct contributions must sum", got, ok)
		}
	})
}
