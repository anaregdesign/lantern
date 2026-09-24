package graphcache

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

func TestAddEdgesWithExpirationContribHLC_DedupDoesNotReviveEndpoint(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	expiration := time.Now().Add(time.Hour)
	stamp := hlc.Timestamp{WallNs: time.Now().UnixNano(), NodeID: hlc.NodeID{0x91}}
	id := ContribID{0: 1}
	if weights, deduped := c.AddEdgesWithExpirationContribHLC([]EdgeItem[string]{{
		Tail: "tail", Head: "head", Weight: 1, Expiration: expiration, ContribID: id,
	}}, stamp); deduped != 0 || len(weights) != 1 || weights[0] != 1 {
		t.Fatalf("first Add = %v/%d, want [1]/0", weights, deduped)
	}
	if err := c.PutVertexWithExpiration("tail", "expired", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	weights, deduped := c.AddEdgesWithExpirationContribHLC([]EdgeItem[string]{
		{Tail: "tail", Head: "head", Weight: 1, Expiration: expiration, ContribID: id},
		{Tail: "fresh", Head: "neighbor", Weight: 3, Expiration: expiration, ContribID: ContribID{0: 2}},
	}, hlc.Timestamp{WallNs: stamp.WallNs + 1, NodeID: stamp.NodeID})
	if deduped != 1 || len(weights) != 2 || weights[0] != 1 || weights[1] != 3 {
		t.Fatalf("mixed Add = %v/%d, want [1 3]/1", weights, deduped)
	}
	if _, ok := c.GetVertex("tail"); ok {
		t.Fatal("deduped batch item revived its endpoint")
	}
	if _, ok := c.GetWeight("tail", "head"); ok {
		t.Fatal("deduped batch item revealed its Edge")
	}
	if _, ok := c.GetVertex("fresh"); !ok {
		t.Fatal("new batch item did not create its endpoint")
	}
	if weight, ok := c.GetWeight("fresh", "neighbor"); !ok || weight != 3 {
		t.Fatalf("new batch item Edge = %v/%t, want 3/true", weight, ok)
	}
}

func TestVertexBatchSearchPreparationRevalidatesReplacedIndex(t *testing.T) {
	live := time.Now().Add(time.Hour)
	item := []VertexItem[string, string]{{Key: "k", Value: "oversized", Expiration: live}}
	for _, tc := range []struct {
		name  string
		write func(*GraphCache[string, string]) error
	}{
		{"put", func(c *GraphCache[string, string]) error {
			_, err := c.PutVerticesWithExpirationOutcomesChecked(item)
			return err
		}},
		{"put-if-absent", func(c *GraphCache[string, string]) error {
			_, err := c.PutVerticesWithExpirationIfAbsentOutcomesChecked(item)
			return err
		}},
		{"hlc-put", func(c *GraphCache[string, string]) error {
			_, err := c.PutVerticesWithExpirationHLCOutcomesChecked(item, hlc.Timestamp{WallNs: 20})
			return err
		}},
		{"hlc-put-if-absent", func(c *GraphCache[string, string]) error {
			_, _, err := c.PutVerticesWithExpirationIfAbsentHLCOutcomesChecked(item, hlc.Timestamp{WallNs: 20})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entered, resume := make(chan struct{}), make(chan struct{})
			first := true
			c := NewGraphCache[string, string](time.Hour)
			c.EnableSearchIndex(func(_ string, value string) search.Document {
				if first {
					first = false
					close(entered)
					<-resume
				}
				return search.Text(value)
			}, strings.Compare)
			result := make(chan error, 1)
			go func() { result <- tc.write(c) }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("writer did not reach out-of-lock projection")
			}
			// Model a future staged pointer publication while the old index is
			// still analyzing. The replacement has a stricter admission limit.
			replacement := newSearchIndex[string](true, search.SearchAnalysisLimits{MaxDocumentBytes: 4}, strings.Compare)
			c.mu.Lock()
			c.searchCommitMu.Lock()
			c.searchIndex = replacement
			c.searchCommitMu.Unlock()
			c.mu.Unlock()
			close(resume)
			var err error
			select {
			case err = <-result:
			case <-time.After(time.Second):
				t.Fatal("writer did not retry against replacement index")
			}
			var limit *search.AnalysisLimitError
			if !errors.As(err, &limit) || limit.Kind != search.LimitDocumentBytes {
				t.Fatalf("error = %v, want replacement's document byte limit", err)
			}
			if _, ok := c.GetVertex("k"); ok {
				t.Fatal("rejected batch mutated the vertex cache")
			}
			if got := c.SearchIndexMemoryStats().Documents; got != 0 {
				t.Fatalf("replacement index documents = %d, want 0", got)
			}
		})
	}
}

func TestPutOutcomesUseFinalApplicationTime(t *testing.T) {
	wallNow := time.Now()
	applicationTime := wallNow.Add(time.Hour)
	expiredAtApplication := wallNow.Add(30 * time.Minute)
	liveAtApplication := wallNow.Add(2 * time.Hour)

	t.Run("vertices and search index", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		c.applicationClock = func() time.Time { return applicationTime }
		c.EnableSearchIndex(
			func(_ string, value string) search.Document { return search.Text(value) },
			strings.Compare,
		)
		if err := c.PutVertexWithExpiration("replace", "old searchable", liveAtApplication); err != nil {
			t.Fatal(err)
		}

		outcomes, err := c.PutVerticesWithExpirationOutcomesChecked([]VertexItem[string, string]{
			{Key: "replace", Value: "must not remain searchable", Expiration: expiredAtApplication},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(outcomes) != 1 || outcomes[0] != PutOutcomeExpired {
			t.Fatalf("outcomes = %v, want [Expired]", outcomes)
		}
		if c.vertices.HasAt("replace", applicationTime) {
			t.Fatal("expired overwrite remained live at the application sample")
		}
		if got := c.SearchVertices("searchable", 10, ""); len(got) != 0 {
			t.Fatalf("expired overwrite remained indexed: %v", got)
		}

		hlcOutcomes, err := c.PutVerticesWithExpirationHLCOutcomesChecked(
			[]VertexItem[string, string]{
				{Key: "replicated", Value: "must not index", Expiration: expiredAtApplication},
			},
			hlc.Timestamp{WallNs: 10},
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(hlcOutcomes) != 1 || hlcOutcomes[0] != PutOutcomeExpired {
			t.Fatalf("HLC outcomes = %v, want [Expired]", hlcOutcomes)
		}
		if got := c.SearchVertices("index", 10, ""); len(got) != 0 {
			t.Fatalf("expired HLC Put remained indexed: %v", got)
		}
	})

	t.Run("crossed expiry ignores search preparation and aggregate errors", func(t *testing.T) {
		newCache := func(limits search.SearchAnalysisLimits) *GraphCache[string, string] {
			c := NewGraphCache[string, string](time.Hour)
			c.EnableSearchIndex(
				func(_ string, value string) search.Document { return search.Text(value) },
				strings.Compare,
				WithSearchAnalysisLimits(limits),
			)
			return c
		}

		t.Run("unconditional analysis limit", func(t *testing.T) {
			c := newCache(search.SearchAnalysisLimits{MaxDocumentBytes: 8})
			if err := c.PutVertexWithExpiration("replace", "old", time.Time{}); err != nil {
				t.Fatal(err)
			}
			c.applicationClock = func() time.Time { return applicationTime }
			outcomes, err := c.PutVerticesWithExpirationOutcomesChecked([]VertexItem[string, string]{
				{Key: "replace", Value: "too-large", Expiration: expiredAtApplication},
			})
			if err != nil {
				t.Fatalf("crossed-expiry analysis error escaped: %v", err)
			}
			if len(outcomes) != 1 || outcomes[0] != PutOutcomeExpired {
				t.Fatalf("outcomes = %v, want [Expired]", outcomes)
			}
			if _, ok := c.GetVertex("replace"); ok {
				t.Fatal("crossed-expiry overwrite did not remove prior live value")
			}
		})

		t.Run("if absent analysis limit", func(t *testing.T) {
			c := newCache(search.SearchAnalysisLimits{MaxDocumentBytes: 8})
			clockSamples := 0
			c.applicationClock = func() time.Time {
				clockSamples++
				return applicationTime
			}
			outcomes, err := c.PutVerticesWithExpirationIfAbsentOutcomesChecked([]VertexItem[string, string]{
				{Key: "fresh", Value: "too-large", Expiration: expiredAtApplication},
			})
			if err != nil {
				t.Fatalf("crossed-expiry analysis error escaped: %v", err)
			}
			if len(outcomes) != 1 || outcomes[0] != PutOutcomeExpired {
				t.Fatalf("outcomes = %v, want [Expired]", outcomes)
			}
			if clockSamples != 1 {
				t.Fatalf("application clock samples = %d, want 1", clockSamples)
			}
		})

		t.Run("HLC analysis limit", func(t *testing.T) {
			c := newCache(search.SearchAnalysisLimits{MaxDocumentBytes: 8})
			c.applicationClock = func() time.Time { return applicationTime }
			outcomes, err := c.PutVerticesWithExpirationHLCOutcomesChecked(
				[]VertexItem[string, string]{{Key: "replicated", Value: "too-large", Expiration: expiredAtApplication}},
				hlc.Timestamp{WallNs: 10},
			)
			if err != nil {
				t.Fatalf("crossed-expiry analysis error escaped: %v", err)
			}
			if len(outcomes) != 1 || outcomes[0] != PutOutcomeExpired {
				t.Fatalf("outcomes = %v, want [Expired]", outcomes)
			}
		})

		t.Run("aggregate limit", func(t *testing.T) {
			c := newCache(search.SearchAnalysisLimits{MaxLivePostings: 1})
			if err := c.PutVertexWithExpiration("existing", "a", time.Time{}); err != nil {
				t.Fatal(err)
			}
			c.applicationClock = func() time.Time { return applicationTime }
			outcomes, err := c.PutVerticesWithExpirationOutcomesChecked([]VertexItem[string, string]{
				{Key: "fresh", Value: "b", Expiration: expiredAtApplication},
			})
			if err != nil {
				t.Fatalf("crossed-expiry aggregate error escaped: %v", err)
			}
			if len(outcomes) != 1 || outcomes[0] != PutOutcomeExpired {
				t.Fatalf("outcomes = %v, want [Expired]", outcomes)
			}
			if got := c.SearchIndexMemoryStats().Documents; got != 1 {
				t.Fatalf("indexed documents = %d, want existing document only", got)
			}
		})
	})

	t.Run("if absent precedence and duplicates", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		c.applicationClock = func() time.Time { return applicationTime }
		if err := c.PutVertexWithExpiration("existing", "old", liveAtApplication); err != nil {
			t.Fatal(err)
		}
		outcomes, err := c.PutVerticesWithExpirationIfAbsentOutcomesChecked([]VertexItem[string, string]{
			{Key: "existing", Value: "born expired", Expiration: expiredAtApplication},
			{Key: "absent-expired", Value: "born expired", Expiration: expiredAtApplication},
			{Key: "duplicate", Value: "first", Expiration: liveAtApplication},
			{Key: "duplicate", Value: "second", Expiration: liveAtApplication},
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []PutOutcome{
			PutOutcomeConditionNotMet,
			PutOutcomeExpired,
			PutOutcomeAppliedAndLive,
			PutOutcomeConditionNotMet,
		}
		if len(outcomes) != len(want) {
			t.Fatalf("outcomes = %v, want %v", outcomes, want)
		}
		for i := range want {
			if outcomes[i] != want[i] {
				t.Fatalf("outcomes[%d] = %v, want %v (all=%v)", i, outcomes[i], want[i], outcomes)
			}
		}
	})

	t.Run("edges", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Hour)
		c.applicationClock = func() time.Time { return applicationTime }
		live := EdgeItem[string]{Tail: "tail", Head: "head", Weight: 1, Expiration: liveAtApplication}
		if got := c.PutEdgesWithExpirationOutcomes([]EdgeItem[string]{live}); got[0] != PutOutcomeAppliedAndLive {
			t.Fatalf("seed outcome = %v", got)
		}
		expired := live
		expired.Weight = 2
		expired.Expiration = expiredAtApplication
		if got := c.PutEdgesWithExpirationOutcomes([]EdgeItem[string]{expired}); got[0] != PutOutcomeExpired {
			t.Fatalf("outcome = %v, want Expired", got)
		}
		if sum := c.edges.liveSumAt("tail", "head", applicationTime); sum != 0 {
			t.Fatalf("edge live sum at application = %v, want 0", sum)
		}

		live.Tail, live.Head = "hlc-tail", "hlc-head"
		if got := c.PutEdgesWithExpirationHLCOutcomes([]EdgeItem[string]{live}, hlc.Timestamp{WallNs: 10}); got[0] != PutOutcomeAppliedAndLive {
			t.Fatalf("HLC seed outcome = %v", got)
		}
		expired.Tail, expired.Head = live.Tail, live.Head
		if got := c.PutEdgesWithExpirationHLCOutcomes([]EdgeItem[string]{expired}, hlc.Timestamp{WallNs: 20}); got[0] != PutOutcomeExpired {
			t.Fatalf("HLC outcome = %v, want Expired", got)
		}
		if sum := c.edges.liveSumAt(live.Tail, live.Head, applicationTime); sum != 0 {
			t.Fatalf("HLC edge live sum at application = %v, want 0", sum)
		}
	})

	t.Run("expired edges never materialize under clock rollback", func(t *testing.T) {
		assertAbsent := func(t *testing.T, c *GraphCache[string, string], tail, head string) {
			t.Helper()
			if _, _, ok := c.GetEdgeDetail(tail, head); ok {
				t.Fatalf("GetEdgeDetail(%q,%q) surfaced accepted-expired edge", tail, head)
			}
			if _, ok := c.GetVertex(tail); ok {
				t.Fatalf("tail endpoint %q was materialized", tail)
			}
			if _, ok := c.GetVertex(head); ok {
				t.Fatalf("head endpoint %q was materialized", head)
			}
			if got := c.edges.count(); got != 0 {
				t.Fatalf("physical edge buckets = %d, want 0", got)
			}
		}

		t.Run("non-HLC", func(t *testing.T) {
			c := NewGraphCache[string, string](time.Hour)
			c.applicationClock = func() time.Time { return applicationTime }
			got := c.PutEdgesWithExpirationOutcomes([]EdgeItem[string]{
				{Tail: "local-tail", Head: "local-head", Weight: 2, Expiration: expiredAtApplication},
			})
			if len(got) != 1 || got[0] != PutOutcomeExpired {
				t.Fatalf("outcomes = %v, want [Expired]", got)
			}
			assertAbsent(t, c, "local-tail", "local-head")

			// Move the injected wall clock back to a point before the supplied
			// expiration. No expired bucket/contribution exists to become live.
			c.applicationClock = func() time.Time { return wallNow }
			assertAbsent(t, c, "local-tail", "local-head")
		})

		t.Run("HLC", func(t *testing.T) {
			c := NewGraphCache[string, string](time.Hour)
			c.applicationClock = func() time.Time { return applicationTime }
			got := c.PutEdgesWithExpirationHLCOutcomes([]EdgeItem[string]{
				{Tail: "hlc-tail", Head: "hlc-head", Weight: 2, Expiration: expiredAtApplication},
			}, hlc.Timestamp{WallNs: 20})
			if len(got) != 1 || got[0] != PutOutcomeExpired {
				t.Fatalf("outcomes = %v, want [Expired]", got)
			}
			assertAbsent(t, c, "hlc-tail", "hlc-head")
			if len(c.edgeCausalBarriers) != 1 {
				t.Fatalf("edge causal barriers = %d, want 1", len(c.edgeCausalBarriers))
			}
			c.applicationClock = func() time.Time { return wallNow }
			assertAbsent(t, c, "hlc-tail", "hlc-head")
		})
	})
}

func TestPutEdgesWithExpirationHLCRejectedBatchDoesNotReviveEndpoints(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	live := time.Now().Add(time.Hour)
	expired := time.Now().Add(-time.Hour)
	newer := hlc.Timestamp{WallNs: 20}
	older := hlc.Timestamp{WallNs: 10}
	if outcome, err := c.PutEdgesWithExpirationHLCOutcomesChecked(
		[]EdgeItem[string]{{Tail: "tail", Head: "head", Weight: 2, Expiration: live}}, newer,
	); err != nil || len(outcome) != 1 || outcome[0] != PutOutcomeAppliedAndLive {
		t.Fatalf("seed Edge Put = %v, %v", outcome, err)
	}
	c.DeleteVertices([]string{"tail"}) // Preserve the newer Edge LWW floor.
	items := []EdgeItem[string]{
		{Tail: "tail", Head: "head", Weight: 9, Expiration: live},
		{Tail: "fresh", Head: "new", Weight: 3, Expiration: live},
		{Tail: "tail", Head: "head", Weight: 10, Expiration: live},
		{Tail: "expired", Head: "edge", Weight: 4, Expiration: expired},
	}
	outcomes, err := c.PutEdgesWithExpirationHLCOutcomesChecked(items, older)
	if err != nil || !slices.Equal(outcomes, []PutOutcome{
		PutOutcomeSuperseded, PutOutcomeAppliedAndLive,
		PutOutcomeSuperseded, PutOutcomeExpired,
	}) {
		t.Fatalf("mixed Edge Put outcomes = %v, %v", outcomes, err)
	}
	if _, ok := c.GetVertex("tail"); ok {
		t.Fatal("superseded duplicate Edge Puts revived their endpoint")
	}
	if _, ok := c.GetVertex("fresh"); !ok {
		t.Fatal("accepted live Edge Put did not create its endpoint")
	}
	if _, ok := c.GetVertex("expired"); ok {
		t.Fatal("accepted-expired Edge Put created an endpoint")
	}
	if weight, ok := c.GetWeight("fresh", "new"); !ok || weight != 3 {
		t.Fatalf("accepted Edge weight = %v/%v, want 3/true", weight, ok)
	}

	// The same early floor check must preserve a tombstone and a permanent
	// accepted-expired Put barrier without creating endpoint vertices.
	if _, err := c.DeleteEdgesHLCChecked([]EdgeKey[string]{{Tail: "tomb", Head: "stone"}}, newer, live); err != nil {
		t.Fatal(err)
	}
	barrier, err := c.PutEdgesWithExpirationHLCOutcomesChecked(
		[]EdgeItem[string]{{Tail: "barrier", Head: "edge", Expiration: expired}}, newer,
	)
	if err != nil || !slices.Equal(barrier, []PutOutcome{PutOutcomeExpired}) {
		t.Fatalf("barrier Edge Put = %v, %v", barrier, err)
	}
	for _, key := range []EdgeKey[string]{{Tail: "tomb", Head: "stone"}, {Tail: "barrier", Head: "edge"}} {
		got, err := c.PutEdgesWithExpirationHLCOutcomesChecked(
			[]EdgeItem[string]{{Tail: key.Tail, Head: key.Head, Expiration: live}}, older,
		)
		if err != nil || !slices.Equal(got, []PutOutcome{PutOutcomeSuperseded}) {
			t.Fatalf("rejected %v Edge Put = %v, %v", key, got, err)
		}
		if _, ok := c.GetVertex(key.Tail); ok {
			t.Fatalf("rejected %v Edge Put created endpoint", key)
		}
	}
}

func BenchmarkPutEdgesWithExpirationHLCAcceptedLive(b *testing.B) {
	c := NewGraphCache[string, string](time.Hour)
	item := []EdgeItem[string]{{Tail: "hot/tail", Head: "hot/head", Weight: 1, Expiration: time.Now().Add(time.Hour)}}
	if _, err := c.PutEdgesWithExpirationHLCOutcomesChecked(item, hlc.Timestamp{WallNs: 1}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.PutEdgesWithExpirationHLCOutcomesChecked(item, hlc.Timestamp{WallNs: int64(i + 2)}); err != nil {
			b.Fatal(err)
		}
	}
}

func TestDeleteOutcomesPreserveBatchOrder(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	expiration := time.Now().Add(time.Hour)
	for _, key := range []string{"a", "b"} {
		if err := c.PutVertexWithExpiration(key, key, expiration); err != nil {
			t.Fatal(err)
		}
	}
	vertexOutcomes := c.DeleteVerticesOutcomes([]string{"a", "missing", "a", "b"})
	if want := []bool{true, false, false, true}; !slices.Equal(vertexOutcomes, want) {
		t.Fatalf("vertex outcomes = %v, want %v", vertexOutcomes, want)
	}
	if got := c.DeleteVerticesOutcomes(nil); got == nil || len(got) != 0 {
		t.Fatalf("empty vertex outcomes = %v", got)
	}

	c.PutEdgeWithExpiration("a", "b", 1, expiration)
	c.PutEdgeWithExpiration("a", "c", 1, expiration)
	edgeOutcomes := c.DeleteEdgesOutcomes([]EdgeKey[string]{
		{Tail: "a", Head: "b"}, {Tail: "missing", Head: "edge"},
		{Tail: "a", Head: "b"}, {Tail: "a", Head: "c"},
	})
	if want := []bool{true, false, false, true}; !slices.Equal(edgeOutcomes, want) {
		t.Fatalf("edge outcomes = %v, want %v", edgeOutcomes, want)
	}
	if got := c.DeleteEdgesOutcomes(nil); got == nil || len(got) != 0 {
		t.Fatalf("empty edge outcomes = %v", got)
	}
}

func TestPutVertexSearchPreparationRevalidatesClockRollback(t *testing.T) {
	realNow := time.Now()
	expiration := realNow.Add(-time.Minute)          // expired during optimistic preparation
	applicationTime := realNow.Add(-2 * time.Minute) // live at the final injected sample

	newCache := func() *GraphCache[string, string] {
		c := NewGraphCache[string, string](time.Hour)
		c.EnableSearchIndex(
			func(_ string, value string) search.Document { return search.Text(value) },
			strings.Compare,
		)
		c.applicationClock = func() time.Time { return applicationTime }
		return c
	}
	assertSearchable := func(t *testing.T, c *GraphCache[string, string], key, term string) {
		t.Helper()
		value, ok := c.vertices.GetAt(key, applicationTime)
		if !ok || value != term {
			t.Fatalf("vertex storage at rolled-back application time %q = %q/%v, want %q/true", key, value, ok, term)
		}
		results, _, err := c.searchIndex.SearchMatchTopKContextAt(
			context.Background(), term, 10, nil, search.MatchOptions{}, search.Budget{}, applicationTime,
		)
		if err != nil {
			t.Fatalf("search at rolled-back application time: %v", err)
		}
		if len(results) != 1 || results[0].ID != key {
			t.Fatalf("search index at rolled-back application time for %q = %v, want only %q", term, results, key)
		}
	}

	t.Run("non-HLC unconditional", func(t *testing.T) {
		c := newCache()
		outcomes, err := c.PutVerticesWithExpirationOutcomesChecked([]VertexItem[string, string]{
			{Key: "local", Value: "rollbacksearchlocal", Expiration: expiration},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(outcomes) != 1 || outcomes[0] != PutOutcomeAppliedAndLive {
			t.Fatalf("outcomes = %v, want [AppliedAndLive]", outcomes)
		}
		assertSearchable(t, c, "local", "rollbacksearchlocal")
	})

	t.Run("HLC unconditional", func(t *testing.T) {
		c := newCache()
		outcomes, err := c.PutVerticesWithExpirationHLCOutcomesChecked(
			[]VertexItem[string, string]{{Key: "hlc", Value: "rollbacksearchhlc", Expiration: expiration}},
			hlc.Timestamp{WallNs: 20},
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(outcomes) != 1 || outcomes[0] != PutOutcomeAppliedAndLive {
			t.Fatalf("outcomes = %v, want [AppliedAndLive]", outcomes)
		}
		assertSearchable(t, c, "hlc", "rollbacksearchhlc")
	})

	t.Run("non-HLC if-absent", func(t *testing.T) {
		c := newCache()
		outcomes, err := c.PutVerticesWithExpirationIfAbsentOutcomesChecked([]VertexItem[string, string]{
			{Key: "nx-local", Value: "rollbacksearchnxlocal", Expiration: expiration},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(outcomes) != 1 || outcomes[0] != PutOutcomeAppliedAndLive {
			t.Fatalf("outcomes = %v, want [AppliedAndLive]", outcomes)
		}
		assertSearchable(t, c, "nx-local", "rollbacksearchnxlocal")
	})

	t.Run("HLC if-absent", func(t *testing.T) {
		c := newCache()
		_, outcomes, err := c.PutVerticesWithExpirationIfAbsentHLCOutcomesChecked(
			[]VertexItem[string, string]{{Key: "nx-hlc", Value: "rollbacksearchnxhlc", Expiration: expiration}},
			hlc.Timestamp{WallNs: 20},
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(outcomes) != 1 || outcomes[0] != PutOutcomeAppliedAndLive {
			t.Fatalf("outcomes = %v, want [AppliedAndLive]", outcomes)
		}
		assertSearchable(t, c, "nx-hlc", "rollbacksearchnxhlc")
	})
}

func TestReplicatedOrderedVertexBarriersKeepSearchConsistent(t *testing.T) {
	newCache := func() *GraphCache[string, string] {
		c := NewGraphCache[string, string](time.Hour)
		c.EnableSearchIndex(
			func(_ string, value string) search.Document { return search.Text(value) },
			strings.Compare,
		)
		return c
	}
	ts := hlc.Timestamp{WallNs: 20}
	live := time.Now().Add(time.Hour)

	t.Run("barrier then live", func(t *testing.T) {
		c := newCache()
		c.PutVerticesWithExpirationHLC([]VertexItem[string, string]{
			{Key: "k", CausalBarrier: true},
			{Key: "k", Value: "final searchable", Expiration: live},
		}, ts)
		if value, ok := c.GetVertex("k"); !ok || value != "final searchable" {
			t.Fatalf("GetVertex = %q/%v, want final searchable/true", value, ok)
		}
		if got := c.SearchVertices("searchable", 10, ""); len(got) != 1 || got[0].ID != "k" {
			t.Fatalf("SearchVertices = %v, want k", got)
		}
	})

	t.Run("live then barrier", func(t *testing.T) {
		c := newCache()
		c.PutVerticesWithExpirationHLC([]VertexItem[string, string]{
			{Key: "k", Value: "must disappear", Expiration: live},
			{Key: "k", CausalBarrier: true},
		}, ts)
		if _, ok := c.GetVertex("k"); ok {
			t.Fatal("final barrier left vertex live")
		}
		if got := c.SearchVertices("disappear", 10, ""); len(got) != 0 {
			t.Fatalf("final barrier left Search document: %v", got)
		}
	})
}
