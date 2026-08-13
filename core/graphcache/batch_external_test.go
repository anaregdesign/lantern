package graphcache_test

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

type countingSearchDocument struct {
	text  string
	calls *atomic.Int64
}

func (d countingSearchDocument) String() string {
	d.calls.Add(1)
	return d.text
}

func countingSearchExtract(_ string, d countingSearchDocument) search.Document { return d }

func compareStringID(a, b string) int { return strings.Compare(a, b) }

func TestPutVerticesWithExpiration_SearchBatchPreparation(t *testing.T) {
	t.Run("one write lock and final duplicate state", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(func(_ string, value string) search.Document { return search.Text(value) }, compareStringID)
		exp := time.Now().Add(time.Minute)
		if err := c.PutVerticesWithExpiration([]graphcache.VertexItem[string, string]{
			{Key: "gone", Value: "old gone", Expiration: exp},
		}); err != nil {
			t.Fatal(err)
		}
		before := c.SearchIndexMemoryStats().WriteLockAcquisitions
		if err := c.PutVerticesWithExpiration([]graphcache.VertexItem[string, string]{
			{Key: "duplicate", Value: "obsolete", Expiration: exp},
			{Key: "gone", Value: "must disappear", Expiration: time.Now().Add(-time.Second)},
			{Key: "fresh", Value: "batchterm", Expiration: exp},
			{Key: "duplicate", Value: "final batchterm", Expiration: exp},
		}); err != nil {
			t.Fatal(err)
		}
		after := c.SearchIndexMemoryStats().WriteLockAcquisitions
		if got := after - before; got != 1 {
			t.Fatalf("index write-lock acquisitions = %d, want 1", got)
		}
		if got := c.SearchVerticesMatch("obsolete", 10, "", search.MatchOptions{Mode: search.MatchAll}, false); len(got) != 0 {
			t.Fatalf("obsolete duplicate remained searchable: %v", got)
		}
		if got := c.SearchVerticesMatch("gone", 10, "", search.MatchOptions{Mode: search.MatchAll}, false); len(got) != 0 {
			t.Fatalf("born-expired overwrite remained searchable: %v", got)
		}
		if got := c.SearchVertices("batchterm", 10, ""); len(got) != 2 {
			t.Fatalf("batchterm results = %v, want 2", got)
		}
		if stats := c.SearchIndexMemoryStats(); stats.Documents != 2 {
			t.Fatalf("indexed documents = %d, want 2", stats.Documents)
		}
		if deleted := c.DeleteVertices([]string{"duplicate", "gone", "fresh"}); deleted != 2 {
			t.Fatalf("DeleteVertices = %d, want 2", deleted)
		}
		if stats := c.SearchIndexMemoryStats(); stats.Documents != 0 || stats.LiveTerms != 0 || stats.Postings != 0 || stats.PositionEntries != 0 {
			t.Fatalf("index refs after delete = %+v, want empty live state", stats)
		}
	})

	t.Run("stable skips and duplicates avoid analysis", func(t *testing.T) {
		var calls atomic.Int64
		value := func(text string) countingSearchDocument {
			return countingSearchDocument{text: text, calls: &calls}
		}
		c := graphcache.NewGraphCache[string, countingSearchDocument](time.Minute)
		c.EnableSearchIndex(countingSearchExtract, compareStringID)
		exp := time.Now().Add(time.Minute)
		if err := c.PutVerticesWithExpiration([]graphcache.VertexItem[string, countingSearchDocument]{
			{Key: "taken", Value: value("taken"), Expiration: exp},
		}); err != nil {
			t.Fatal(err)
		}

		calls.Store(0)
		written, skipped, err := c.PutVerticesWithExpirationIfAbsentChecked([]graphcache.VertexItem[string, countingSearchDocument]{
			{Key: "taken", Value: value("must not analyze"), Expiration: exp},
			{Key: "fresh", Value: value("accepted"), Expiration: exp},
			{Key: "fresh", Value: value("duplicate must not analyze"), Expiration: exp},
			{Key: "expired", Value: value("born expired"), Expiration: time.Now().Add(-time.Second)},
		})
		if err != nil || written != 1 || len(skipped) != 2 || skipped[0] != "taken" || skipped[1] != "fresh" {
			t.Fatalf("written=%d skipped=%v err=%v", written, skipped, err)
		}
		if got := calls.Load(); got != 1 {
			t.Fatalf("if-absent document analyses = %d, want 1", got)
		}

		calls.Store(0)
		if err := c.PutVerticesWithExpiration([]graphcache.VertexItem[string, countingSearchDocument]{
			{Key: "duplicate", Value: value("first"), Expiration: exp},
			{Key: "duplicate", Value: value("second"), Expiration: exp},
			{Key: "duplicate", Value: value("final"), Expiration: exp},
			{Key: "expired", Value: value("born expired"), Expiration: time.Now().Add(-time.Second)},
		}); err != nil {
			t.Fatal(err)
		}
		if got := calls.Load(); got != 1 {
			t.Fatalf("unconditional document analyses = %d, want final duplicate only", got)
		}

		calls.Store(0)
		if rejected, err := c.PutVerticesWithExpirationHLCChecked([]graphcache.VertexItem[string, countingSearchDocument]{
			{Key: "taken", Value: value("newer"), Expiration: exp},
		}, hlc.Timestamp{WallNs: 20}); err != nil || rejected != 0 {
			t.Fatalf("seed HLC rejected=%d err=%v", rejected, err)
		}
		calls.Store(0)
		rejected, err := c.PutVerticesWithExpirationHLCChecked([]graphcache.VertexItem[string, countingSearchDocument]{
			{Key: "taken", Value: value("stale must not analyze"), Expiration: exp},
			{Key: "hlc-fresh", Value: value("accepted"), Expiration: exp},
		}, hlc.Timestamp{WallNs: 10})
		if err != nil || rejected != 1 {
			t.Fatalf("stale HLC rejected=%d err=%v", rejected, err)
		}
		if got := calls.Load(); got != 1 {
			t.Fatalf("HLC document analyses = %d, want 1", got)
		}
	})
}

func TestPutVerticesWithExpiration_SearchBatchVisibility(t *testing.T) {
	const documents = 128
	c := graphcache.NewGraphCache[string, string](time.Minute)
	c.EnableSearchIndex(func(_ string, value string) search.Document { return search.Text(value) }, compareStringID)
	exp := time.Now().Add(time.Minute)
	items := make([]graphcache.VertexItem[string, string], documents)
	keys := make([]string, documents)
	for i := range items {
		key := "batch-" + itoa(i)
		keys[i] = key
		items[i] = graphcache.VertexItem[string, string]{Key: key, Value: "atomicbatchterm", Expiration: exp}
	}

	var stop atomic.Bool
	var reads atomic.Int64
	var partial atomic.Int64
	var readers sync.WaitGroup
	readers.Add(4)
	for range 4 {
		go func() {
			defer readers.Done()
			for !stop.Load() {
				got := len(c.SearchVertices("atomicbatchterm", documents+1, ""))
				reads.Add(1)
				if got != 0 && got != documents {
					partial.Add(1)
				}
			}
		}()
	}
	for range 50 {
		if err := c.PutVerticesWithExpiration(items); err != nil {
			t.Fatal(err)
		}
		c.DeleteVertices(keys)
	}
	stop.Store(true)
	readers.Wait()
	if reads.Load() == 0 {
		t.Fatal("reader never executed")
	}
	if got := partial.Load(); got != 0 {
		t.Fatalf("search observed %d partial batch snapshots", got)
	}
}

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
				g := c.Neighbor("s", 1, fanOut+1, graphcache.WeightingRaw, false, nil)
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
				g := c.Neighbor("s", 1, fanOut+1, graphcache.WeightingRaw, false, nil)
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
	c.EnableSearchIndex(func(_ string, v string) search.Document { return search.Text(v) }, strings.Compare)

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
		effective, deduped := c.AddEdgesWithExpirationContrib(batch)
		if deduped != 0 {
			t.Fatalf("first apply: deduped = %d, want 0", deduped)
		}
		if len(effective) != 1 || effective[0] != 2.5 {
			t.Fatalf("first apply: effective = %v, want [2.5]", effective)
		}
		// Replay the identical batch: must be an exact no-op, but the
		// effective weight still reports the current live sum (#897).
		effective, deduped = c.AddEdgesWithExpirationContrib(batch)
		if deduped != 1 {
			t.Fatalf("replay: deduped = %d, want 1", deduped)
		}
		if len(effective) != 1 || effective[0] != 2.5 {
			t.Fatalf("replay: effective = %v, want [2.5] (live sum on dedup no-op)", effective)
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
		effective, deduped := c.AddEdgesWithExpirationContrib(batch)
		if deduped != 0 {
			t.Fatalf("first apply: deduped = %d, want 0", deduped)
		}
		if effective[0] != 2 {
			t.Fatalf("first apply: effective = %v, want [2]", effective)
		}
		effective, deduped = c.AddEdgesWithExpirationContrib(batch)
		if deduped != 0 {
			t.Fatalf("replay: deduped = %d, want 0 (zero id must not dedup)", deduped)
		}
		if effective[0] != 4 {
			t.Fatalf("replay: effective = %v, want [4] (accumulated sum)", effective)
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
		effective, deduped := c.AddEdgesWithExpirationContrib([]graphcache.EdgeItem[string]{
			{Tail: "s", Head: "h", Weight: 1, Expiration: exp, ContribID: id2},
		})
		if deduped != 0 {
			t.Fatalf("distinct id: deduped = %d, want 0", deduped)
		}
		if effective[0] != 2 {
			t.Fatalf("distinct id: effective = %v, want [2] (post-accumulation sum)", effective)
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
		effective, deduped := c.AddEdgesWithExpirationContrib([]graphcache.EdgeItem[string]{
			{Tail: "s", Head: "a", Weight: 1, Expiration: exp, ContribID: id},
			{Tail: "s", Head: "b", Weight: 1, Expiration: exp},
		})
		if deduped != 1 {
			t.Fatalf("mixed batch: deduped = %d, want 1", deduped)
		}
		// effective is index-aligned: [0] is the deduped edge's live sum, [1]
		// is the freshly-applied edge's live sum.
		if len(effective) != 2 || effective[0] != 1 || effective[1] != 1 {
			t.Fatalf("mixed batch: effective = %v, want [1 1]", effective)
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

	// #917: the effective weight returned by the add path must agree with the
	// read path once contributions expire below the compaction floor. Guards
	// the wiring up to AddEdgesWithExpirationContrib, not just the weight unit.
	t.Run("effective agrees with GetWeight after contributions expire", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, int](time.Minute)
		shortExp := time.Now().Add(40 * time.Millisecond)
		for i := 0; i < 50; i++ { // below the compaction floor: no structural flush
			c.AddEdgesWithExpirationContrib([]graphcache.EdgeItem[string]{
				{Tail: "s", Head: "h", Weight: 1, Expiration: shortExp},
			})
		}
		time.Sleep(60 * time.Millisecond) // let them expire
		effective, _ := c.AddEdgesWithExpirationContrib([]graphcache.EdgeItem[string]{
			{Tail: "s", Head: "h", Weight: 1, Expiration: time.Now().Add(time.Hour)},
		})
		w, ok := c.GetWeight("s", "h")
		if !ok {
			t.Fatal("edge missing")
		}
		if len(effective) != 1 || effective[0] != w {
			t.Fatalf("AddEdges effective = %v but GetWeight = %v — #917: the add path leaked expired weight", effective, w)
		}
		if effective[0] != 1 {
			t.Fatalf("effective = %v, want 1 (unfixed code reports 51)", effective[0])
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

	// The rejected return (#840) counts exactly the tombstone/LWW-skipped
	// items — never applied items, and never born-expired items (those are
	// applied dead-on-arrival with their watermark recorded).
	t.Run("RejectedCount", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		if got := c.PutVerticesWithExpirationHLC([]graphcache.VertexItem[string, string]{
			{Key: "a", Value: "v", Expiration: exp},
			{Key: "b", Value: "v", Expiration: exp},
		}, newer); got != 0 {
			t.Fatalf("first batch rejected = %d, want 0", got)
		}
		if got := c.PutVerticesWithExpirationHLC([]graphcache.VertexItem[string, string]{
			{Key: "a", Value: "stale", Expiration: exp},                       // rejected: LWW
			{Key: "b", Value: "stale", Expiration: exp},                       // rejected: LWW
			{Key: "c", Value: "fresh", Expiration: exp},                       // applied
			{Key: "x", Value: "dead", Expiration: time.Now().Add(-time.Hour)}, // applied dead-on-arrival, NOT rejected
		}, older); got != 2 {
			t.Fatalf("mixed batch rejected = %d, want 2 (LWW losers only)", got)
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

	// The rejected return (#840) counts tombstone-fenced and per-edge
	// LWW-lost items — the same set the singular PutEdgeWithExpirationHLC
	// reports as applied=false — and nothing else.
	t.Run("RejectedCount", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		if got := c.PutEdgesWithExpirationHLC([]graphcache.EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 1.0, Expiration: exp},
		}, newer); got != 0 {
			t.Fatalf("first batch rejected = %d, want 0", got)
		}
		if got := c.PutEdgesWithExpirationHLC([]graphcache.EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 9.9, Expiration: exp}, // rejected: LWW watermark
			{Tail: "x", Head: "y", Weight: 1.0, Expiration: exp}, // applied: brand-new edge
		}, older); got != 1 {
			t.Fatalf("mixed batch rejected = %d, want 1 (LWW loser only)", got)
		}
		if n := c.DeleteEdgesHLC([]graphcache.EdgeKey[string]{{Tail: "x", Head: "y"}}, newer, exp); n != 1 {
			t.Fatalf("delete: got %d, want 1", n)
		}
		if got := c.PutEdgesWithExpirationHLC([]graphcache.EdgeItem[string]{
			{Tail: "x", Head: "y", Weight: 2.0, Expiration: exp}, // rejected: tombstone fence
		}, older); got != 1 {
			t.Fatalf("tombstone-fenced batch rejected = %d, want 1", got)
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
		effective, deduped := c.AddEdgesWithExpirationContribHLC([]graphcache.EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 5.0, Expiration: exp, ContribID: graphcache.ContribID{0: 2}},
		}, older)
		if deduped != 1 {
			t.Errorf("deduped: got %d, want 1 (tombstone-dropped)", deduped)
		}
		// A tombstone-dropped item applied nothing; its effective entry reports
		// the current live sum, which is 0 here because the edge is deleted (#918).
		if len(effective) != 1 || effective[0] != 0 {
			t.Errorf("effective: got %v, want [0] (tombstone-dropped adds nothing)", effective)
		}
		if _, ok := c.GetWeight("a", "b"); ok {
			t.Errorf("older contribution resurrected a tombstoned edge")
		}
	})

	t.Run("FencedItemReportsCurrentLiveSum", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		// Record a newer tombstone, then rebuild live weight via the non-HLC
		// contrib path (which does not consult the tombstone fence).
		if n := c.DeleteEdgesHLC([]graphcache.EdgeKey[string]{{Tail: "a", Head: "b"}}, newer, exp); n != 0 {
			t.Fatalf("delete of absent edge: got %d, want 0", n)
		}
		c.AddEdgesWithExpirationContrib([]graphcache.EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 5.0, Expiration: exp, ContribID: graphcache.ContribID{0: 1}},
		})
		// An older HLC contribution is fenced by the tombstone: it adds nothing,
		// but its effective entry must report the current live sum (5), not 0 (#918).
		effective, deduped := c.AddEdgesWithExpirationContribHLC([]graphcache.EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 7.0, Expiration: exp, ContribID: graphcache.ContribID{0: 2}},
		}, older)
		if deduped != 1 {
			t.Errorf("deduped: got %d, want 1 (tombstone-fenced)", deduped)
		}
		if len(effective) != 1 || effective[0] != 5.0 {
			t.Errorf("effective: got %v, want [5] (fenced item reports current live sum)", effective)
		}
		if got, ok := c.GetWeight("a", "b"); !ok || got != 5.0 {
			t.Errorf("a->b weight: got (%v,%v), want (5.0,true) — fenced contribution must not add", got, ok)
		}
	})

	t.Run("ContribIDDedup", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		items := []graphcache.EdgeItem[string]{
			{Tail: "a", Head: "b", Weight: 1.0, Expiration: exp, ContribID: graphcache.ContribID{0: 9}},
		}
		if effective, d := c.AddEdgesWithExpirationContribHLC(items, older); d != 0 {
			t.Errorf("first apply deduped: got %d, want 0", d)
		} else if effective[0] != 1.0 {
			t.Errorf("first apply effective: got %v, want [1]", effective)
		}
		if effective, d := c.AddEdgesWithExpirationContribHLC(items, newer); d != 1 {
			t.Errorf("replay deduped: got %d, want 1 (same ContribID is idempotent)", d)
		} else if effective[0] != 1.0 {
			t.Errorf("replay effective: got %v, want [1] (live sum on dedup no-op)", effective)
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

// TestPutVerticesWithExpirationIfAbsent pins the batched SET NX contract
// (#896): only keys with no live vertex are written, live keys are reported in
// skipped (request order), a key that becomes live earlier in the same batch
// fences its own later duplicate, and an expired-but-uncollected vertex does
// not block its write (#750 live-visibility).
func TestPutVerticesWithExpirationIfAbsent(t *testing.T) {
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)

	t.Run("WritesAbsentSkipsLive", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		c.PutVertexWithExpiration("live", "old", future)
		written, skipped := c.PutVerticesWithExpirationIfAbsent([]graphcache.VertexItem[string, string]{
			{Key: "fresh", Value: "a", Expiration: future},
			{Key: "live", Value: "b", Expiration: future}, // skipped: already live
		})
		if written != 1 {
			t.Fatalf("written = %d, want 1", written)
		}
		if len(skipped) != 1 || skipped[0] != "live" {
			t.Fatalf("skipped = %v, want [live]", skipped)
		}
		if v, _ := c.GetVertex("live"); v != "old" {
			t.Fatalf("live value = %q, want \"old\" (must be untouched)", v)
		}
		if v, ok := c.GetVertex("fresh"); !ok || v != "a" {
			t.Fatalf("fresh = (%q,%v), want (\"a\",true)", v, ok)
		}
	})

	t.Run("IntraBatchDuplicateSkipped", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		written, skipped := c.PutVerticesWithExpirationIfAbsent([]graphcache.VertexItem[string, string]{
			{Key: "dup", Value: "first", Expiration: future},
			{Key: "dup", Value: "second", Expiration: future}, // skipped: first made it live
		})
		if written != 1 {
			t.Fatalf("written = %d, want 1", written)
		}
		if len(skipped) != 1 || skipped[0] != "dup" {
			t.Fatalf("skipped = %v, want [dup]", skipped)
		}
		if v, _ := c.GetVertex("dup"); v != "first" {
			t.Fatalf("dup value = %q, want \"first\"", v)
		}
	})

	t.Run("ExpiredDoesNotBlock", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		c.PutVertexWithExpiration("k", "stale", past)
		written, skipped := c.PutVerticesWithExpirationIfAbsent([]graphcache.VertexItem[string, string]{
			{Key: "k", Value: "fresh", Expiration: future},
		})
		if written != 1 || len(skipped) != 0 {
			t.Fatalf("written=%d skipped=%v, want written=1 skipped=[]", written, skipped)
		}
		if v, _ := c.GetVertex("k"); v != "fresh" {
			t.Fatalf("value = %q, want \"fresh\"", v)
		}
	})

	t.Run("BornExpiredNeitherWrittenNorSkipped", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		written, skipped := c.PutVerticesWithExpirationIfAbsent([]graphcache.VertexItem[string, string]{
			{Key: "dead", Value: "x", Expiration: past}, // discarded: born expired
			{Key: "fresh", Value: "y", Expiration: future},
		})
		if written != 1 {
			t.Fatalf("written = %d, want 1 (born-expired must not count)", written)
		}
		if len(skipped) != 0 {
			t.Fatalf("skipped = %v, want [] (born-expired is discarded, not skipped)", skipped)
		}
		if _, ok := c.GetVertex("dead"); ok {
			t.Fatal("born-expired vertex was stored, want miss")
		}
		if v, ok := c.GetVertex("fresh"); !ok || v != "y" {
			t.Fatalf("fresh = (%q,%v), want (\"y\",true)", v, ok)
		}
	})
}

// TestPutVerticesWithExpirationIfAbsentHLC pins the replication-aware SET NX
// path (#896): its legacy indices count only the live-written subset, while
// the exact-outcome sibling also exposes accepted EXPIRED delete-like writes
// for replication. It skips keys that are already live and refuses to
// resurrect a key fenced by a strictly newer tombstone.
func TestPutVerticesWithExpirationIfAbsentHLC(t *testing.T) {
	exp := time.Now().Add(time.Hour)
	older := hlc.Timestamp{WallNs: 1000}
	newer := hlc.Timestamp{WallNs: 2000}

	t.Run("WrittenIndicesAndLiveSkip", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		c.PutVerticesWithExpirationHLC([]graphcache.VertexItem[string, string]{
			{Key: "live", Value: "old", Expiration: exp},
		}, older)
		writtenIdx, skipped := c.PutVerticesWithExpirationIfAbsentHLC([]graphcache.VertexItem[string, string]{
			{Key: "live", Value: "b", Expiration: exp},  // idx 0: skipped (live)
			{Key: "fresh", Value: "c", Expiration: exp}, // idx 1: written
		}, newer)
		if len(writtenIdx) != 1 || writtenIdx[0] != 1 {
			t.Fatalf("writtenIdx = %v, want [1]", writtenIdx)
		}
		if len(skipped) != 1 || skipped[0] != "live" {
			t.Fatalf("skipped = %v, want [live]", skipped)
		}
		if v, _ := c.GetVertex("live"); v != "old" {
			t.Fatalf("live value = %q, want \"old\"", v)
		}
	})

	t.Run("NewerTombstoneNotResurrected", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		// Delete an absent key with a newer HLC: it removes nothing (returns 0)
		// but still records the tombstone watermark that fences older writes.
		c.DeleteVerticesHLC([]string{"d"}, newer, exp)
		writtenIdx, skipped := c.PutVerticesWithExpirationIfAbsentHLC([]graphcache.VertexItem[string, string]{
			{Key: "d", Value: "resurrect", Expiration: exp}, // older than tombstone: fenced
		}, older)
		if len(writtenIdx) != 0 {
			t.Fatalf("writtenIdx = %v, want [] (fence must block)", writtenIdx)
		}
		if len(skipped) != 1 || skipped[0] != "d" {
			t.Fatalf("skipped = %v, want [d]", skipped)
		}
		if _, ok := c.GetVertex("d"); ok {
			t.Fatal("older if-absent put resurrected a tombstoned key")
		}
	})

	t.Run("BornExpiredExcludedFromWrittenIdx", func(t *testing.T) {
		c := graphcache.NewGraphCache[string, string](time.Minute)
		past := time.Now().Add(-time.Hour)
		writtenIdx, skipped := c.PutVerticesWithExpirationIfAbsentHLC([]graphcache.VertexItem[string, string]{
			{Key: "dead", Value: "x", Expiration: past}, // idx 0: discarded (born expired)
			{Key: "fresh", Value: "y", Expiration: exp}, // idx 1: written
		}, newer)
		if len(writtenIdx) != 1 || writtenIdx[0] != 1 {
			t.Fatalf("writtenIdx = %v, want [1] (born-expired must be excluded)", writtenIdx)
		}
		if len(skipped) != 0 {
			t.Fatalf("skipped = %v, want [] (born-expired is discarded, not skipped)", skipped)
		}
		if _, ok := c.GetVertex("dead"); ok {
			t.Fatal("born-expired vertex was stored, want miss")
		}
	})
}
