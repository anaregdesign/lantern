package graphcache

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

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
