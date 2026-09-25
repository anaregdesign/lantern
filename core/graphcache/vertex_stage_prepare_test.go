package graphcache

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

func TestVertexTransactionsRejectBeforeMutation(t *testing.T) {
	t.Run("search analysis", func(t *testing.T) {
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		c.EnableSearchIndex(
			func(_ string, value string) search.Document { return search.Text(value) },
			strings.Compare,
			WithSearchAnalysisLimits(search.SearchAnalysisLimits{MaxDocumentBytes: 4}),
		)
		if err := c.PutVertexWithExpiration("seed", "ok", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		before := captureVertexTransactionState(c)
		tx, err := c.BeginVertexPut(
			[]VertexItem[string, string]{{Key: "new", Value: "oversized", Expiration: time.Now().Add(time.Hour)}},
			hlc.Timestamp{WallNs: 20}, true,
		)
		if tx != nil || err == nil {
			if tx != nil {
				tx.Abort()
			}
			t.Fatalf("oversized staged Put = (%v, %v), want error", tx, err)
		}
		var limit *search.AnalysisLimitError
		if !errors.As(err, &limit) {
			t.Fatalf("search error = %v, want AnalysisLimitError", err)
		}
		if after := captureVertexTransactionState(c); !reflect.DeepEqual(after, before) {
			t.Fatalf("search failure mutated state: before=%+v after=%+v", before, after)
		}

		beforeVertices, beforeEdges, beforeDict := c.vertices, c.edges, c.dict
		beforeSearch := c.searchIndex
		conditionMiss, err := c.BeginVertexPut(
			[]VertexItem[string, string]{{Key: "seed", Value: "oversized", Expiration: time.Now().Add(time.Hour)}},
			hlc.Timestamp{WallNs: 20}, true,
		)
		if err != nil {
			t.Fatalf("condition-miss payload was analyzed: %v", err)
		}
		if got := conditionMiss.Result(); !slices.Equal(got.Outcomes, []PutOutcome{PutOutcomeConditionNotMet}) ||
			len(got.Accepted) != 0 {
			conditionMiss.Abort()
			t.Fatalf("all-condition-miss result = %+v", got)
		}
		conditionMiss.Commit()
		if c.vertices != beforeVertices || c.edges != beforeEdges || c.dict != beforeDict ||
			c.searchIndex != beforeSearch {
			t.Fatal("committed condition miss replaced state owners")
		}
		if after := captureVertexTransactionState(c); !reflect.DeepEqual(after, before) {
			t.Fatalf("committed condition miss changed state: before=%+v after=%+v", before, after)
		}
	})

	t.Run("causal capacity", func(t *testing.T) {
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		c.SetCausalMetadataLimits(CausalMetadataLimits{MaxVertexEntries: 1})
		expiration := time.Now().Add(time.Hour)
		if _, err := c.PutVerticesWithExpirationHLCOutcomesChecked(
			[]VertexItem[string, string]{{Key: "first", Value: "first", Expiration: expiration}},
			hlc.Timestamp{WallNs: 10},
		); err != nil {
			t.Fatal(err)
		}
		before := captureVertexTransactionState(c)
		tx, err := c.BeginVertexPut(
			[]VertexItem[string, string]{{Key: "second", Value: "second", Expiration: expiration}},
			hlc.Timestamp{WallNs: 20}, false,
		)
		if tx != nil || err == nil {
			if tx != nil {
				tx.Abort()
			}
			t.Fatalf("over-capacity staged Put = (%v, %v)", tx, err)
		}
		var capacity *CausalMetadataCapacityError
		if !errors.As(err, &capacity) {
			t.Fatalf("capacity error = %v", err)
		}
		after := captureVertexTransactionState(c)
		if after.base.stats.VertexRejected != before.base.stats.VertexRejected+1 {
			t.Fatalf(
				"capacity rejection counter = %d, want %d",
				after.base.stats.VertexRejected, before.base.stats.VertexRejected+1,
			)
		}
		after.base.stats.VertexRejected = before.base.stats.VertexRejected
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("capacity failure mutated state: before=%+v after=%+v", before, after)
		}
	})

	t.Run("Delete search analysis", func(t *testing.T) {
		failAnalysis := false
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		c.EnableSearchIndex(
			func(_ string, value string) search.Document {
				if failAnalysis {
					return search.Text("oversized")
				}
				return search.Text(value)
			},
			strings.Compare,
			WithSearchAnalysisLimits(search.SearchAnalysisLimits{MaxDocumentBytes: 4}),
		)
		expiration := time.Now().Add(time.Hour)
		if err := c.PutVertexWithExpiration("seed", "ok", expiration); err != nil {
			t.Fatal(err)
		}
		before := captureVertexTransactionState(c)
		failAnalysis = true
		tx, err := c.BeginVertexDelete(
			[]string{"seed"}, hlc.Timestamp{WallNs: 20}, expiration,
		)
		if tx != nil || err == nil {
			if tx != nil {
				tx.Abort()
			}
			t.Fatalf("Delete with invalid rebuilt index = (%v, %v), want error", tx, err)
		}
		var limit *search.AnalysisLimitError
		if !errors.As(err, &limit) {
			t.Fatalf("Delete search error = %v, want AnalysisLimitError", err)
		}
		if after := captureVertexTransactionState(c); !reflect.DeepEqual(after, before) {
			t.Fatalf("Delete search failure mutated state: before=%+v after=%+v", before, after)
		}
	})

	t.Run("all condition misses reject incomplete search", func(t *testing.T) {
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		c.EnableSearchIndex(
			func(_ string, value string) search.Document { return search.Text(value) },
			strings.Compare,
		)
		expiration := time.Now().Add(time.Hour)
		if err := c.PutVertexWithExpiration("seed", "value", expiration); err != nil {
			t.Fatal(err)
		}
		c.searchIndex.MarkIncomplete()
		before := captureVertexTransactionState(c)
		tx, err := c.BeginVertexPut(
			[]VertexItem[string, string]{{Key: "seed", Value: "ignored", Expiration: expiration}},
			hlc.Timestamp{WallNs: 20}, true,
		)
		if tx != nil || !errors.Is(err, search.ErrIndexIncomplete) {
			if tx != nil {
				tx.Abort()
			}
			t.Fatalf("condition-miss on incomplete search = (%v, %v)", tx, err)
		}
		if after := captureVertexTransactionState(c); !reflect.DeepEqual(after, before) {
			t.Fatalf("incomplete-search rejection mutated state: before=%+v after=%+v", before, after)
		}
	})
}

func TestVertexTransactionsOnlyAnalyzeTouchedSearchDocuments(t *testing.T) {
	const corpusSize = 1024
	var extracted atomic.Int64
	var enforceUnlocked atomic.Bool
	var c *GraphCache[string, string]
	c = NewGraphCacheWithStaging[string, string](time.Hour)
	c.EnableSearchIndex(
		func(key, value string) search.Document {
			if enforceUnlocked.Load() {
				if !c.mu.TryLock() {
					panic("staged search extraction ran under GraphCache lock")
				}
				c.mu.Unlock()
				if !c.publicationGate.TryLock() {
					panic("staged search extraction ran under publication gate")
				}
				c.publicationGate.Unlock()
			}
			extracted.Add(1)
			return search.Fields{
				{ID: search.FieldKey, Text: key},
				{ID: search.FieldValue, Text: value},
			}
		},
		strings.Compare,
	)
	expiration := time.Now().Add(time.Hour)
	items := make([]VertexItem[string, string], corpusSize)
	for i := range items {
		items[i] = VertexItem[string, string]{
			Key: fmt.Sprintf("key-%04d", i), Value: fmt.Sprintf("value-%04d", i), Expiration: expiration,
		}
	}
	if err := c.PutVerticesWithExpirationChecked(items); err != nil {
		t.Fatal(err)
	}

	enforceUnlocked.Store(true)
	extracted.Store(0)
	put, err := c.BeginVertexPut(
		[]VertexItem[string, string]{{Key: "key-0000", Value: "replacement", Expiration: expiration}},
		hlc.Timestamp{WallNs: 20}, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := extracted.Load(); got != 2 {
		put.Abort()
		t.Fatalf("one-key staged Put extracted %d documents, want new and prior touched documents only", got)
	}
	put.Abort()

	extracted.Store(0)
	del, err := c.BeginVertexDelete(
		[]string{"key-0001"}, hlc.Timestamp{WallNs: 20}, expiration,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := extracted.Load(); got != 1 {
		del.Abort()
		t.Fatalf("one-key staged Delete extracted %d documents, want prior touched document only", got)
	}
	del.Abort()
}

func TestReplicatedVertexTransactionsFailClosedOnSearchErrors(t *testing.T) {
	expiration := time.Now().Add(time.Hour)

	t.Run("Put analysis failure", func(t *testing.T) {
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		c.EnableSearchIndex(
			func(_ string, value string) search.Document { return search.Text(value) },
			strings.Compare,
			WithSearchAnalysisLimits(search.SearchAnalysisLimits{MaxDocumentBytes: 4}),
		)
		before := captureVertexTransactionState(c)
		tx, err := c.BeginReplicatedVertexPut(
			[]VertexItem[string, string]{{Key: "key", Value: "oversized", Expiration: expiration}},
			hlc.Timestamp{WallNs: 20},
		)
		if tx != nil {
			tx.Abort()
			t.Fatal("replicated Put returned a transaction after analysis failure")
		}
		var limit *search.AnalysisLimitError
		if !errors.As(err, &limit) {
			t.Fatalf("replicated Put error = %v, want AnalysisLimitError", err)
		}
		if after := captureVertexTransactionState(c); !reflect.DeepEqual(after, before) {
			t.Fatalf("replicated Put analysis failure mutated state: before=%+v after=%+v", before, after)
		}
	})

	t.Run("Put incomplete index", func(t *testing.T) {
		c := newVertexTransactionTestCache()
		c.searchIndex.MarkIncomplete()
		before := captureVertexTransactionState(c)
		tx, err := c.BeginReplicatedVertexPut(
			[]VertexItem[string, string]{{Key: "key", Value: "value", Expiration: expiration}},
			hlc.Timestamp{WallNs: 20},
		)
		if tx != nil {
			tx.Abort()
			t.Fatal("replicated Put returned a transaction for an incomplete index")
		}
		if !errors.Is(err, search.ErrIndexIncomplete) {
			t.Fatalf("replicated Put error = %v, want ErrIndexIncomplete", err)
		}
		if after := captureVertexTransactionState(c); !reflect.DeepEqual(after, before) {
			t.Fatalf("replicated Put incomplete-index failure mutated state: before=%+v after=%+v", before, after)
		}
	})

	t.Run("Delete analysis failure", func(t *testing.T) {
		failAnalysis := false
		c := NewGraphCacheWithStaging[string, string](time.Hour)
		c.EnableSearchIndex(
			func(_ string, value string) search.Document {
				if failAnalysis {
					return search.Text("oversized")
				}
				return search.Text(value)
			},
			strings.Compare,
			WithSearchAnalysisLimits(search.SearchAnalysisLimits{MaxDocumentBytes: 4}),
		)
		if err := c.PutVertexWithExpiration("key", "ok", expiration); err != nil {
			t.Fatal(err)
		}
		before := captureVertexTransactionState(c)
		failAnalysis = true
		tx, err := c.BeginReplicatedVertexDelete(
			[]string{"key"}, hlc.Timestamp{WallNs: 20}, expiration,
		)
		if tx != nil {
			tx.Abort()
			t.Fatal("replicated Delete returned a transaction after analysis failure")
		}
		var limit *search.AnalysisLimitError
		if !errors.As(err, &limit) {
			t.Fatalf("replicated Delete error = %v, want AnalysisLimitError", err)
		}
		if after := captureVertexTransactionState(c); !reflect.DeepEqual(after, before) {
			t.Fatalf("replicated Delete analysis failure mutated state: before=%+v after=%+v", before, after)
		}
	})

	t.Run("Delete incomplete index", func(t *testing.T) {
		c := newVertexTransactionTestCache()
		if err := c.PutVertexWithExpiration("key", "value", expiration); err != nil {
			t.Fatal(err)
		}
		c.searchIndex.MarkIncomplete()
		before := captureVertexTransactionState(c)
		tx, err := c.BeginReplicatedVertexDelete(
			[]string{"key"}, hlc.Timestamp{WallNs: 20}, expiration,
		)
		if tx != nil {
			tx.Abort()
			t.Fatal("replicated Delete returned a transaction for an incomplete index")
		}
		if !errors.Is(err, search.ErrIndexIncomplete) {
			t.Fatalf("replicated Delete error = %v, want ErrIndexIncomplete", err)
		}
		if after := captureVertexTransactionState(c); !reflect.DeepEqual(after, before) {
			t.Fatalf("replicated Delete incomplete-index failure mutated state: before=%+v after=%+v", before, after)
		}
	})
}
