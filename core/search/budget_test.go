package search

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

type stepCancelContext struct {
	context.Context
	cancelAt int
	calls    int
	done     chan struct{}
	once     sync.Once
}

func newStepCancelContext(cancelAt int) *stepCancelContext {
	return &stepCancelContext{Context: context.Background(), cancelAt: cancelAt, done: make(chan struct{})}
}

func (c *stepCancelContext) Done() <-chan struct{} { return c.done }

func (c *stepCancelContext) Err() error {
	c.calls++
	if c.calls < c.cancelAt {
		return nil
	}
	c.once.Do(func() { close(c.done) })
	return context.Canceled
}

func TestWorkTracker(t *testing.T) {
	t.Run("charges and classifies exhaustion", func(t *testing.T) {
		w := newWorkTracker(context.Background(), Budget{MaxPostingVisits: 2})
		if err := w.visit(WorkPostingVisits, 2); err != nil {
			t.Fatalf("visit within budget: %v", err)
		}
		err := w.visit(WorkPostingVisits, 1)
		if !errors.Is(err, ErrBudgetExceeded) {
			t.Fatalf("error = %v, want ErrBudgetExceeded", err)
		}
		var exhausted *BudgetExceededError
		if !errors.As(err, &exhausted) || exhausted.Kind != WorkPostingVisits {
			t.Fatalf("error = %#v, want posting kind", err)
		}
	})

	t.Run("context wins before charging", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		w := newWorkTracker(ctx, Budget{})
		if err := w.visit(WorkDictionaryVisits, 1); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if w.stats.DictionaryVisits != 0 {
			t.Fatalf("visits = %d, want 0", w.stats.DictionaryVisits)
		}
	})
}

func TestContextCancellationObservedInsideSearchLoops(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID, WithPositions())
	for i := 0; i < 100; i++ {
		idx.Index(fmt.Sprintf("doc-%03d", i), Text(fmt.Sprintf("alpha beta candidate%03d", i)))
	}

	t.Run("dictionary expansion", func(t *testing.T) {
		results, stats, err := idx.SearchMatchTopKContext(newStepCancelContext(8), "candidata", 10, nil, MatchOptions{Fuzziness: 2}, Budget{})
		if results != nil || !errors.Is(err, context.Canceled) || stats.DictionaryVisits == 0 {
			t.Fatalf("results=%v stats=%+v err=%v, want cancellation during dictionary work", results, stats, err)
		}
	})

	t.Run("posting scoring", func(t *testing.T) {
		results, stats, err := idx.SearchMatchTopKContext(newStepCancelContext(8), "alpha", 10, nil, MatchOptions{}, Budget{})
		if results != nil || !errors.Is(err, context.Canceled) || stats.PostingVisits == 0 {
			t.Fatalf("results=%v stats=%+v err=%v, want cancellation during posting work", results, stats, err)
		}
	})

	t.Run("phrase verification", func(t *testing.T) {
		results, stats, err := idx.SearchPhraseTopKContext(newStepCancelContext(10), "alpha beta", 10, nil, Budget{})
		if results != nil || !errors.Is(err, context.Canceled) || stats.PositionVisits == 0 {
			t.Fatalf("results=%v stats=%+v err=%v, want cancellation during phrase positions", results, stats, err)
		}
	})

	t.Run("proximity work", func(t *testing.T) {
		results, stats, err := idx.SearchMatchTopKContext(newStepCancelContext(210), "alpha beta", 10, nil, MatchOptions{}, Budget{})
		if results != nil || !errors.Is(err, context.Canceled) || stats.PositionVisits == 0 {
			t.Fatalf("results=%v stats=%+v err=%v, want cancellation during proximity work", results, stats, err)
		}
	})

	t.Run("top-k selection", func(t *testing.T) {
		plain := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
		for i := 0; i < 100; i++ {
			plain.Index(fmt.Sprintf("doc-%03d", i), Text("alpha"))
		}
		results, stats, err := plain.SearchMatchTopKContext(newStepCancelContext(110), "alpha", 10, nil, MatchOptions{}, Budget{})
		if results != nil || !errors.Is(err, context.Canceled) || stats.PostingVisits != 100 {
			t.Fatalf("results=%v stats=%+v err=%v, want cancellation during selection", results, stats, err)
		}
	})
}

func BenchmarkSearchExecutionBudgets(b *testing.B) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID, WithPositions())
	for i := 0; i < 20_000; i++ {
		idx.Index(fmt.Sprintf("doc-%05d", i), Text(fmt.Sprintf("shared phrase candidate%05d", i)))
	}
	budget := Budget{
		MaxQueryTerms:       1024,
		MaxDictionaryVisits: 1_000_000,
		MaxPostingVisits:    10_000_000,
		MaxPositionVisits:   10_000_000,
	}
	run := func(b *testing.B, query string, opts MatchOptions, phrase bool) {
		b.ReportAllocs()
		var stats Stats
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var err error
			if phrase {
				_, stats, err = idx.SearchPhraseTopKContext(context.Background(), query, 20, nil, budget)
			} else {
				_, stats, err = idx.SearchMatchTopKContext(context.Background(), query, 20, nil, opts, budget)
			}
			if err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(stats.DictionaryVisits), "dictionary_visits/op")
		b.ReportMetric(float64(stats.PostingVisits), "posting_visits/op")
		b.ReportMetric(float64(stats.PositionVisits), "position_visits/op")
	}
	b.Run("BroadPosting", func(b *testing.B) { run(b, "shared", MatchOptions{}, false) })
	b.Run("FuzzyDictionary", func(b *testing.B) { run(b, "candidatx12345", MatchOptions{Fuzziness: 1}, false) })
	b.Run("BroadPhrase", func(b *testing.B) { run(b, "shared phrase", MatchOptions{}, true) })
}
