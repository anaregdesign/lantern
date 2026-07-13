package graphcache

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

// textExtract is the value projection used by the string-keyed tests: it makes
// the stored string value itself the searchable document.
func textExtract(s string) search.Document { return search.Text(s) }
func compareStringID(a, b string) int      { return strings.Compare(a, b) }

func TestGraphCacheSearchAnalysisLimits(t *testing.T) {
	limits := search.SearchAnalysisLimits{
		MaxDocumentBytes:     8,
		MaxDocumentTokens:    20,
		MaxDocumentTerms:     20,
		MaxLiveTerms:         20,
		MaxLivePostings:      20,
		MaxPositionEntries:   20,
		CompactionRatio:      2,
		CompactionMinRetired: 1,
	}
	newCache := func() *GraphCache[string, string] {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID, WithSearchAnalysisLimits(limits))
		return c
	}

	t.Run("local batch atomic", func(t *testing.T) {
		c := newCache()
		err := c.PutVerticesWithExpirationChecked([]VertexItem[string, string]{
			{Key: "ok", Value: "small", Expiration: time.Now().Add(time.Minute)},
			{Key: "bad", Value: "too-large", Expiration: time.Now().Add(time.Minute)},
		})
		var limit *search.AnalysisLimitError
		if !errors.As(err, &limit) || limit.Kind != search.LimitDocumentBytes {
			t.Fatalf("error = %v", err)
		}
		if _, ok := c.GetVertex("ok"); ok {
			t.Fatal("valid prefix of rejected batch committed")
		}
		if got := c.SearchIndexMemoryStats().Documents; got != 0 {
			t.Fatalf("indexed docs = %d", got)
		}
	})

	t.Run("if absent ignores oversized skipped item", func(t *testing.T) {
		c := newCache()
		if err := c.PutVerticesWithExpirationChecked([]VertexItem[string, string]{{Key: "taken", Value: "old", Expiration: time.Now().Add(time.Minute)}}); err != nil {
			t.Fatal(err)
		}
		written, skipped, err := c.PutVerticesWithExpirationIfAbsentChecked([]VertexItem[string, string]{
			{Key: "taken", Value: "too-large", Expiration: time.Now().Add(time.Minute)},
			{Key: "fresh", Value: "new", Expiration: time.Now().Add(time.Minute)},
		})
		if err != nil || written != 1 || !reflect.DeepEqual(skipped, []string{"taken"}) {
			t.Fatalf("written=%d skipped=%v err=%v", written, skipped, err)
		}
		if got, _ := c.GetVertex("fresh"); got != "new" {
			t.Fatalf("fresh = %q", got)
		}
	})

	t.Run("replication converges graph and fails search closed", func(t *testing.T) {
		c := newCache()
		c.PutVerticesWithExpirationHLC([]VertexItem[string, string]{{Key: "legacy", Value: "too-large", Expiration: time.Now().Add(time.Minute)}}, hlc.Timestamp{WallNs: 1})
		if got, ok := c.GetVertex("legacy"); !ok || got != "too-large" {
			t.Fatalf("replicated graph value = %q, %t", got, ok)
		}
		if got := c.SearchIndexMemoryStats().Health; got != search.IndexIncomplete {
			t.Fatalf("health = %q", got)
		}
		if _, _, err := c.SearchVerticesMatchContext(context.Background(), "large", 10, "", search.MatchOptions{}, false, search.Budget{}); !errors.Is(err, search.ErrIndexIncomplete) {
			t.Fatalf("search err = %v", err)
		}
		if err := c.RebuildSearchIndex(); err == nil {
			t.Fatal("rebuild unexpectedly accepted the oversized live value")
		}
		if got := c.SearchIndexMemoryStats().Health; got != search.IndexIncomplete {
			t.Fatalf("health after failed rebuild = %q", got)
		}
		c.DeleteVertex("legacy")
		if got := c.SearchIndexMemoryStats().Health; got != search.IndexHealthy {
			t.Fatalf("automatic bounded rebuild health = %q", got)
		}
	})
}

// keys returns just the IDs of a ranked result set, in rank order, for terse
// assertions.
func keys(results []search.Result[string]) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.ID
	}
	return out
}

// TestGraphCache_SearchExpirationIndependentOfBackgroundGC pins the TTL
// contract at the GraphCache boundary. Search must rank from the same live
// corpus whether or not the comparatively slow cache GC tick has reclaimed
// the expired vertex yet.
func TestGraphCache_SearchExpirationIndependentOfBackgroundGC(t *testing.T) {
	newCache := func() *GraphCache[string, string] {
		c := NewGraphCache[string, string](time.Hour)
		c.EnableSearchIndex(textExtract, compareStringID)
		return c
	}
	withExpired := newCache()
	baseline := newCache()
	expiration := time.Now().Add(20 * time.Millisecond)
	for _, c := range []*GraphCache[string, string]{withExpired, baseline} {
		if err := c.PutVertexWithExpiration("live", "alpha beta", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := withExpired.PutVertexWithExpiration("expired", "alpha alpha zzuniqueonly", expiration); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(expiration) + 10*time.Millisecond)

	before := withExpired.SearchIndexMemoryStats()
	if before.Documents != 1 || before.PhysicalDocuments != 2 || before.ExpiredDocuments != 1 {
		t.Fatalf("pre-purge stats = %+v, want live=1 physical=2 expired=1", before)
	}
	query := func(c *GraphCache[string, string]) ([]search.Result[string], search.Stats) {
		results, stats, err := c.SearchVerticesMatchContext(context.Background(), "alpha", 10, "", search.MatchOptions{}, false, search.Budget{})
		if err != nil {
			t.Fatal(err)
		}
		return results, stats
	}
	got, work := query(withExpired)
	want, _ := query(baseline)
	if !reflect.DeepEqual(keys(got), keys(want)) || len(got) != 1 || math.Float64bits(got[0].Score) != math.Float64bits(want[0].Score) {
		t.Fatalf("query-time purge results = %+v, baseline = %+v", got, want)
	}
	if work.ExpirationVisits != 1 {
		t.Fatalf("expiration visits = %d, want 1", work.ExpirationVisits)
	}
	if got := withExpired.SearchVerticesMatch("zzuniq", 10, "", search.MatchOptions{PrefixTerms: true}, false); len(got) != 0 {
		t.Fatalf("expired-only prefix candidates = %+v, want none", got)
	}
	afterPurge := withExpired.SearchIndexMemoryStats()
	if afterPurge.Documents != 1 || afterPurge.PhysicalDocuments != 1 || afterPurge.ExpirationPurged != 1 {
		t.Fatalf("post-purge stats = %+v", afterPurge)
	}

	// The graph cache still physically retains the dead vertex until GC. Its
	// later eviction hook is idempotent with the index's earlier purge and must
	// not perturb ranking.
	if removed := withExpired.vertices.Flush(); removed != 1 {
		t.Fatalf("background Flush removed %d vertices, want 1", removed)
	}
	afterGC, _ := query(withExpired)
	if !reflect.DeepEqual(got, afterGC) {
		t.Fatalf("ranking changed after background GC: before=%+v after=%+v", got, afterGC)
	}
}

func TestGraphCache_SearchConcurrentMutationAndCancellation(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnableSearchIndex(textExtract, compareStringID)
	c.EnablePrefixIndex(func(key string) string { return key })
	for i := 0; i < 200; i++ {
		c.PutVertex(fmt.Sprintf("seed-%03d", i), "alpha beta")
	}

	start := make(chan struct{})
	errs := make(chan error, 3)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 500; i++ {
			c.PutVertex(fmt.Sprintf("hot-%03d", i%50), "alpha beta gamma")
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 500; i++ {
			c.DeleteVertex(fmt.Sprintf("hot-%03d", i%50))
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 500; i++ {
			ctx, cancel := context.WithCancel(context.Background())
			if i%2 == 0 {
				cancel()
			}
			_, _, err := c.SearchVerticesMatchContext(ctx, "alpha beta", 10, "seed-", search.MatchOptions{}, false, search.Budget{})
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				errs <- err
				return
			}
		}
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent search: %v", err)
	}
}

func TestGraphCache_SearchDisabledByDefault(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.PutVertex("k", "lantern graph database")
	// Without EnableSearchIndex the search path is inert: no index work on
	// put, and SearchVertices returns nil.
	if got := c.SearchVertices("database", 10, ""); got != nil {
		t.Fatalf("SearchVertices without enable: got %v want nil", got)
	}
}

func TestGraphCache_EnableSearchIndex_AfterPutPanics(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.PutVertex("k", "v")
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when enabling search index on non-empty cache")
		}
	}()
	c.EnableSearchIndex(textExtract, compareStringID)
}

func TestGraphCache_EnableSearchIndex_NilExtractPanics(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when extract is nil")
		}
	}()
	c.EnableSearchIndex(nil, compareStringID)
}

func TestGraphCache_EnableSearchIndex_NilComparatorPanics(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when compareID is nil")
		}
	}()
	c.EnableSearchIndex(textExtract, nil)
}

func TestGraphCache_EnableSearchIndex_Idempotent(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnableSearchIndex(textExtract, compareStringID)
	c.EnableSearchIndex(textExtract, compareStringID) // must not panic
}

// TestGraphCache_EnableSearchIndex_WithoutPositions covers the #908 opt-out:
// a position-free index answers phrase queries by the AND-intersection (both
// query words present, adjacency unverified), while the default position-
// tracking index verifies adjacency. Doc "adj" carries the words adjacently and
// matches either way; doc "split" scatters them and matches only when positions
// are dropped.
func TestGraphCache_EnableSearchIndex_WithoutPositions(t *testing.T) {
	seed := func(c *GraphCache[string, string]) {
		c.PutVertex("adj", "alpha beta gamma")   // "alpha beta" adjacent, in order
		c.PutVertex("split", "alpha gamma beta") // same words, not adjacent
	}
	phraseHits := func(c *GraphCache[string, string]) map[string]bool {
		got := map[string]bool{}
		for _, r := range c.SearchVerticesMatch("alpha beta", 50, "", search.MatchOptions{}, true) {
			got[r.ID] = true
		}
		return got
	}

	t.Run("positions on verifies adjacency", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID) // default: positions on
		seed(c)
		if got := phraseHits(c); !reflect.DeepEqual(got, map[string]bool{"adj": true}) {
			t.Fatalf("phrase hits = %v, want only {adj} (split's words are not adjacent)", got)
		}
	})

	t.Run("positions off degrades to AND-intersection", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID, WithoutSearchPositions())
		seed(c)
		if got := phraseHits(c); !reflect.DeepEqual(got, map[string]bool{"adj": true, "split": true}) {
			t.Fatalf("phrase hits = %v, want {adj, split} (positions off => AND-intersection)", got)
		}
	})
}

func TestGraphCache_SearchVertices(t *testing.T) {
	t.Run("Ranking", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID)
		c.PutVertex("exact", "arch")
		c.PutVertex("infix", "research laboratory")
		c.PutVertex("other", "a giant panda")

		got := c.SearchVertices("arch", 10, "")
		// "panda" never shares a gram with "arch"; the exact short doc must
		// outrank the longer infix match (BM25 length normalization).
		if want := []string{"exact", "infix"}; !equalKeys(keys(got), want) {
			t.Fatalf("SearchVertices(arch) = %v, want %v", keys(got), want)
		}
	})

	t.Run("Overwrite drops stale postings", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID)
		c.PutVertex("k", "alpha")
		if got := keys(c.SearchVertices("alpha", 10, "")); !equalKeys(got, []string{"k"}) {
			t.Fatalf("before overwrite: SearchVertices(alpha) = %v, want [k]", got)
		}
		// Re-index on overwrite is the key difference from the prefix index:
		// the old value's postings must disappear, the new value's appear.
		c.PutVertex("k", "omega zulu")
		if got := c.SearchVertices("alpha", 10, ""); got != nil {
			t.Fatalf("after overwrite: SearchVertices(alpha) = %v, want nil", keys(got))
		}
		if got := keys(c.SearchVertices("omega", 10, "")); !equalKeys(got, []string{"k"}) {
			t.Fatalf("after overwrite: SearchVertices(omega) = %v, want [k]", got)
		}
	})

	t.Run("Delete stops matching", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID)
		c.PutVertex("k", "gamma unique")
		if got := keys(c.SearchVertices("gamma", 10, "")); !equalKeys(got, []string{"k"}) {
			t.Fatalf("before delete: got %v, want [k]", got)
		}
		// DeleteVertex routes through the vertices.SetOnEvict hook, which must
		// physically drop the posting from the search index.
		if !c.DeleteVertex("k") {
			t.Fatal("DeleteVertex(k) reported not-present")
		}
		if got := c.SearchVertices("gamma", 10, ""); got != nil {
			t.Fatalf("after delete: got %v, want nil", keys(got))
		}
	})

	t.Run("Clear stops matching", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID)
		c.PutVertex("a", "shared term")
		c.PutVertex("b", "shared word")
		// The inner cache's Clear fires SetOnEvict for every key, so the
		// search index must end up empty too.
		c.vertices.Clear()
		if got := c.SearchVertices("shared", 10, ""); got != nil {
			t.Fatalf("after clear: got %v, want nil", keys(got))
		}
	})

	t.Run("TTL expiry stops matching", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID)
		// Live and already-expired vertices share the term; only the live one
		// must surface, even before the GC Flush runs (liveness filter).
		c.PutVertex("live", "delta payload")
		c.PutVertexWithExpiration("dead", "delta payload", time.Now().Add(-time.Minute))
		if got := keys(c.SearchVertices("delta", 10, "")); !equalKeys(got, []string{"live"}) {
			t.Fatalf("before flush: got %v, want [live]", got)
		}
		// After the Flush physically evicts the expired vertex, the index drops
		// its posting via SetOnEvict and the result is unchanged.
		c.vertices.Flush()
		if got := keys(c.SearchVertices("delta", 10, "")); !equalKeys(got, []string{"live"}) {
			t.Fatalf("after flush: got %v, want [live]", got)
		}
	})

	t.Run("KeyPrefix scopes results", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID)
		c.PutVertex("user:1", "common keyword")
		c.PutVertex("user:2", "common keyword")
		c.PutVertex("session:a", "common keyword")
		got := c.SearchVertices("keyword", 10, "user:")
		if want := []string{"user:1", "user:2"}; !equalKeys(keys(got), want) {
			t.Fatalf("SearchVertices(keyword, prefix user:) = %v, want %v", keys(got), want)
		}
	})

	t.Run("Prefix index pushes candidate scope into retrieval", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID)
		c.EnablePrefixIndex(func(key string) string { return key })
		items := make([]VertexItem[string, string], 0, 2010)
		expiration := time.Now().Add(time.Hour)
		for i := 0; i < 2000; i++ {
			items = append(items, VertexItem[string, string]{Key: fmt.Sprintf("other:%04d", i), Value: "shared keyword", Expiration: expiration})
		}
		for i := 0; i < 10; i++ {
			items = append(items, VertexItem[string, string]{Key: fmt.Sprintf("scope:%02d", i), Value: "shared keyword", Expiration: expiration})
		}
		if err := c.PutVerticesWithExpiration(items); err != nil {
			t.Fatal(err)
		}
		scoped, scopedStats, err := c.SearchVerticesMatchContext(context.Background(), "keyword", 5, "scope:", search.MatchOptions{}, false, search.Budget{})
		if err != nil {
			t.Fatal(err)
		}
		global, globalStats, err := c.SearchVerticesMatchContext(context.Background(), "keyword", 5, "", search.MatchOptions{}, false, search.Budget{})
		if err != nil {
			t.Fatal(err)
		}
		if len(scoped) != 5 || len(global) != 5 {
			t.Fatalf("result sizes: scoped=%d global=%d, want 5/5", len(scoped), len(global))
		}
		if scopedStats.CandidateVisits != 10 {
			t.Fatalf("scoped candidate visits=%d, want prefix population 10", scopedStats.CandidateVisits)
		}
		if globalStats.CandidateVisits != 2010 {
			t.Fatalf("global candidate visits=%d, want corpus population 2010", globalStats.CandidateVisits)
		}
		if scopedStats.PostingVisits >= globalStats.PostingVisits/10 {
			t.Fatalf("prefix pushdown did not bound posting work: scoped=%d global=%d", scopedStats.PostingVisits, globalStats.PostingVisits)
		}
	})

	t.Run("Limit caps results", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID)
		for _, k := range []string{"k1", "k2", "k3", "k4"} {
			c.PutVertex(k, "common keyword")
		}
		if got := keys(c.SearchVertices("keyword", 2, "")); !equalKeys(got, []string{"k1", "k2"}) {
			t.Fatalf("SearchVertices(keyword, limit 2) = %v, want [k1 k2]", got)
		}
	})

	t.Run("Empty query returns nil", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID)
		c.PutVertex("k", "anything")
		if got := c.SearchVertices("", 10, ""); got != nil {
			t.Fatalf("SearchVertices(empty) = %v, want nil", keys(got))
		}
	})

	t.Run("Non-positive limit returns nil", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID)
		c.PutVertex("k", "anything")
		if got := c.SearchVertices("anything", 0, ""); got != nil {
			t.Fatalf("SearchVertices(limit 0) = %v, want nil", keys(got))
		}
		if got := c.SearchVertices("anything", -1, ""); got != nil {
			t.Fatalf("SearchVertices(limit -1) = %v, want nil", keys(got))
		}
	})

	t.Run("No match returns nil", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID)
		c.PutVertex("k", "alpha beta")
		if got := c.SearchVertices("zzzzz", 10, ""); got != nil {
			t.Fatalf("SearchVertices(no-match) = %v, want nil", keys(got))
		}
	})
}

// TestGraphCache_SearchVertices_Concurrent exercises the #741 lock-splitting
// refactor: SearchVertices runs query analysis, BM25 ranking, and
// liveness/prefix filtering without holding GraphCache.mu. Concurrent
// PutVertex / DeleteVertex / SearchVertices must therefore be free of data
// races (run under -race) and every returned hit must be in scope at filter
// time.
func TestGraphCache_SearchVertices_Concurrent(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	c.EnableSearchIndex(textExtract, compareStringID)
	c.EnablePrefixIndex(func(s string) string { return s })

	const keysN = 200
	// Seed so searches have something to match from the very first iteration.
	for i := 0; i < keysN; i++ {
		c.PutVertex(fmt.Sprintf("user:%03d", i), "common shared payload")
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writers churn the same key space with puts and deletes.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(seed)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				k := fmt.Sprintf("user:%03d", r.Intn(keysN))
				if r.Intn(2) == 0 {
					c.PutVertex(k, "common shared payload")
				} else {
					c.DeleteVertex(k)
				}
			}
		}(w)
	}

	// Searchers run a broad query, both unscoped and prefix-scoped. Every hit
	// from the scoped search must be within the prefix at filter time.
	for s := 0; s < 4; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				select {
				case <-stop:
					return
				default:
				}
				for _, r := range c.SearchVertices("payload", 50, "user:") {
					if !strings.HasPrefix(r.ID, "user:") {
						t.Errorf("scoped search returned out-of-prefix key %q", r.ID)
						return
					}
				}
				_ = c.SearchVertices("payload", 50, "")
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// equalKeys reports whether got equals want in the same order.
func equalKeys(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestGraphCache_PutVertices_SearchLifecycle verifies that moving search-document
// analysis outside the aggregate write lock (#739) keeps the batch put path in
// perfect lockstep with the search index: hits are visible synchronously after
// the batch returns, an overwrite drops the stale document, duplicate keys in
// one batch keep last-write semantics, and a born-expired item is neither stored
// nor indexed. Test words have mutually disjoint bigrams so the NGram(2)
// analyzer cannot produce spurious cross-matches.
func TestGraphCache_PutVertices_SearchLifecycle(t *testing.T) {
	future := func() time.Time { return time.Now().Add(time.Minute) }

	t.Run("batch puts are searchable synchronously", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID)
		c.PutVerticesWithExpiration([]VertexItem[string, string]{
			{Key: "a", Value: "alpha", Expiration: future()},
			{Key: "b", Value: "bravo", Expiration: future()},
		})
		if got := keys(c.SearchVertices("alpha", 10, "")); !equalKeys(got, []string{"a"}) {
			t.Fatalf(`Search("alpha") = %v, want [a]`, got)
		}
		if got := keys(c.SearchVertices("bravo", 10, "")); !equalKeys(got, []string{"b"}) {
			t.Fatalf(`Search("bravo") = %v, want [b]`, got)
		}
	})

	t.Run("overwrite drops stale document", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID)
		c.PutVerticesWithExpiration([]VertexItem[string, string]{
			{Key: "a", Value: "alpha", Expiration: future()},
		})
		c.PutVerticesWithExpiration([]VertexItem[string, string]{
			{Key: "a", Value: "zulu", Expiration: future()},
		})
		if got := c.SearchVertices("alpha", 10, ""); got != nil {
			t.Fatalf(`Search("alpha") after overwrite = %v, want nil`, keys(got))
		}
		if got := keys(c.SearchVertices("zulu", 10, "")); !equalKeys(got, []string{"a"}) {
			t.Fatalf(`Search("zulu") after overwrite = %v, want [a]`, got)
		}
	})

	t.Run("duplicate keys in one batch keep last write", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID)
		c.PutVerticesWithExpiration([]VertexItem[string, string]{
			{Key: "a", Value: "alpha", Expiration: future()},
			{Key: "a", Value: "zulu", Expiration: future()},
		})
		if got := c.SearchVertices("alpha", 10, ""); got != nil {
			t.Fatalf(`Search("alpha") = %v, want nil (last write wins)`, keys(got))
		}
		if got := keys(c.SearchVertices("zulu", 10, "")); !equalKeys(got, []string{"a"}) {
			t.Fatalf(`Search("zulu") = %v, want [a]`, got)
		}
	})

	t.Run("born-expired item is neither stored nor indexed", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		c.EnableSearchIndex(textExtract, compareStringID)
		c.PutVerticesWithExpiration([]VertexItem[string, string]{
			{Key: "live", Value: "alpha", Expiration: future()},
			{Key: "dead", Value: "bravo", Expiration: time.Now().Add(-time.Minute)},
		})
		if _, ok := c.GetVertex("dead"); ok {
			t.Fatal("born-expired vertex was stored")
		}
		if got := c.SearchVertices("bravo", 10, ""); got != nil {
			t.Fatalf(`Search("bravo") = %v, want nil (born-expired not indexed)`, keys(got))
		}
		if got := keys(c.SearchVertices("alpha", 10, "")); !equalKeys(got, []string{"live"}) {
			t.Fatalf(`Search("alpha") = %v, want [live]`, got)
		}
	})
}

// TestGraphCache_SearchVertices_LifecycleIntersection pins the #752 search
// agreement invariant: SearchVertices returns only keys that are simultaneously
// (a) live in the vertex cache, (b) still carry the queried term after any
// overwrite, and (c) inside the requested key prefix. Deleted, overwritten-away,
// and expired-but-not-flushed documents never surface.
func TestGraphCache_SearchVertices_LifecycleIntersection(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	c.EnablePrefixIndex(identityExtract)
	c.EnableSearchIndex(textExtract, compareStringID)
	live := time.Now().Add(time.Hour)

	c.PutVertexWithExpiration("eu:1", "harbor", live)
	c.PutVertexWithExpiration("us:1", "harbor", live)
	c.PutVertexWithExpiration("eu:2", "harbor", live)
	c.PutVertexWithExpiration("eu:2", "summit", live) // overwrite drops "harbor"
	c.DeleteVertex("us:1")                            // delete removes the doc
	// eu:3 is expired-but-not-flushed: a stale search posting.
	c.mu.Lock()
	c.putVertexLocked("eu:3", "harbor", time.Now().Add(-time.Minute))
	c.mu.Unlock()

	set := func(rs []search.Result[string]) map[string]bool {
		m := make(map[string]bool, len(rs))
		for _, r := range rs {
			m[r.ID] = true
		}
		return m
	}

	// Only eu:1 is live AND still tagged "harbor".
	if got := set(c.SearchVertices("harbor", 50, "")); !reflect.DeepEqual(got, map[string]bool{"eu:1": true}) {
		t.Errorf("Search(harbor) = %v, want {eu:1} (us:1 deleted, eu:2 overwritten, eu:3 expired)", got)
	}
	// Prefix scoping intersects with liveness: us: has no live harbor doc.
	if got := set(c.SearchVertices("harbor", 50, "us:")); len(got) != 0 {
		t.Errorf("Search(harbor, us:) = %v, want empty (us:1 deleted)", got)
	}
	// The overwrite term is searchable on the surviving key only.
	if got := set(c.SearchVertices("summit", 50, "")); !got["eu:2"] {
		t.Errorf("Search(summit) = %v, want eu:2", got)
	}
}
