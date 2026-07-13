package search

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExpiryPurge(t *testing.T) {
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	now := base
	clock := func() time.Time { return now }
	newIndex := func() *InvertedIndex[string, Text] {
		return NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID, WithPositions(), WithIndexClock(clock))
	}
	resultBits := func(results []Result[string]) map[string]uint64 {
		out := make(map[string]uint64, len(results))
		for _, result := range results {
			out[result.ID] = math.Float64bits(result.Score)
		}
		return out
	}
	budget := Budget{
		MaxQueryTerms: 100, MaxDictionaryVisits: 1000, MaxPostingVisits: 1000,
		MaxPositionVisits: 1000, MaxExpirationVisits: 1000,
	}

	t.Run("ranking and phrase equal never-contained baseline", func(t *testing.T) {
		now = base
		idx := newIndex()
		baseline := newIndex()
		liveExpiration := base.Add(time.Hour)
		if err := idx.IndexWithExpiration("live", Text("common data set"), liveExpiration); err != nil {
			t.Fatal(err)
		}
		if err := idx.IndexWithExpiration("expired", Text("common data set data set obsoleteonly"), base.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := baseline.IndexWithExpiration("live", Text("common data set"), liveExpiration); err != nil {
			t.Fatal(err)
		}
		now = base.Add(2 * time.Minute)
		before := idx.MemoryStats()
		if before.Documents != 1 || before.PhysicalDocuments != 2 || before.ExpiredDocuments != 1 || before.ExpirationQueueEntries != 2 {
			t.Fatalf("pre-purge stats = %+v", before)
		}
		baselineStats := baseline.MemoryStats()
		if before.LiveTerms != baselineStats.LiveTerms || before.Postings != baselineStats.Postings || before.PositionEntries != baselineStats.PositionEntries || before.EstimatedLiveBytes != baselineStats.EstimatedLiveBytes || before.EstimatedRetainedBytes <= before.EstimatedLiveBytes {
			t.Fatalf("logical stats = %+v, never-contained baseline = %+v", before, baselineStats)
		}

		got, stats, err := idx.SearchMatchTopKContext(context.Background(), "common", 10, nil, MatchOptions{}, budget)
		if err != nil {
			t.Fatal(err)
		}
		want, _, err := baseline.SearchMatchTopKContext(context.Background(), "common", 10, nil, MatchOptions{}, budget)
		if err != nil {
			t.Fatal(err)
		}
		if gotBits, wantBits := resultBits(got), resultBits(want); len(gotBits) != len(wantBits) || gotBits["live"] != wantBits["live"] {
			t.Fatalf("result bits = %v, want %v", gotBits, wantBits)
		}
		if stats.ExpirationVisits != 1 {
			t.Fatalf("expiration visits = %d, want 1", stats.ExpirationVisits)
		}
		if after := idx.MemoryStats(); after.Documents != 1 || after.PhysicalDocuments != 1 || after.ExpiredDocuments != 0 || after.ExpirationQueueEntries != 1 || after.ExpirationPurged != 1 {
			t.Fatalf("post-purge stats = %+v", after)
		}

		gotPhrase := idx.SearchPhrase("data set")
		wantPhrase := baseline.SearchPhrase("data set")
		if gotBits, wantBits := resultBits(gotPhrase), resultBits(wantPhrase); len(gotBits) != len(wantBits) || gotBits["live"] != wantBits["live"] {
			t.Fatalf("phrase bits = %v, want %v", gotBits, wantBits)
		}
	})

	t.Run("expired-only vocabulary cannot expand", func(t *testing.T) {
		now = base
		idx := newIndex()
		baseline := newIndex()
		if err := idx.IndexWithExpiration("expired", Text("obsoleteonly"), base.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		now = base.Add(2 * time.Minute)
		got, gotStats, err := idx.SearchMatchTopKContext(context.Background(), "obso", 10, nil, MatchOptions{PrefixTerms: true}, budget)
		if err != nil {
			t.Fatal(err)
		}
		want, wantStats, err := baseline.SearchMatchTopKContext(context.Background(), "obso", 10, nil, MatchOptions{PrefixTerms: true}, budget)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 || len(want) != 0 || gotStats.DictionaryVisits != wantStats.DictionaryVisits {
			t.Fatalf("got=%v stats=%+v baseline=%v stats=%+v", got, gotStats, want, wantStats)
		}
		got, gotStats, err = idx.SearchMatchTopKContext(context.Background(), "obsoleteonlx", 10, nil, MatchOptions{Fuzziness: 1}, budget)
		if err != nil {
			t.Fatal(err)
		}
		want, wantStats, err = baseline.SearchMatchTopKContext(context.Background(), "obsoleteonlx", 10, nil, MatchOptions{Fuzziness: 1}, budget)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 || len(want) != 0 || gotStats.DictionaryVisits != wantStats.DictionaryVisits {
			t.Fatalf("fuzzy got=%v stats=%+v baseline=%v stats=%+v", got, gotStats, want, wantStats)
		}
	})
}

func TestExpiryPurgeBudgets(t *testing.T) {
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	now := base
	clock := func() time.Time { return now }
	newIndex := func() *InvertedIndex[string, Text] {
		return NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID, WithIndexClock(clock))
	}

	t.Run("expiration visits bound incremental progress", func(t *testing.T) {
		idx := newIndex()
		for _, id := range []string{"a", "b"} {
			if err := idx.IndexWithExpiration(id, Text("shared"), base.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
		now = base.Add(2 * time.Minute)
		_, stats, err := idx.SearchMatchTopKContext(context.Background(), "shared", 10, nil, MatchOptions{}, Budget{MaxExpirationVisits: 1, MaxPostingVisits: 10})
		var exhausted *BudgetExceededError
		if !errors.As(err, &exhausted) || exhausted.Kind != WorkExpirationVisits {
			t.Fatalf("error = %v, stats=%+v", err, stats)
		}
		if got := idx.MemoryStats().PhysicalDocuments; got != 1 {
			t.Fatalf("physical documents after bounded progress = %d, want 1", got)
		}
		if _, _, err := idx.SearchMatchTopKContext(context.Background(), "shared", 10, nil, MatchOptions{}, Budget{MaxExpirationVisits: 10, MaxPostingVisits: 10}); err != nil {
			t.Fatal(err)
		}
		if got := idx.MemoryStats().PhysicalDocuments; got != 0 {
			t.Fatalf("physical documents after recovery = %d", got)
		}
	})

	t.Run("posting removal is charged before mutation", func(t *testing.T) {
		now = base
		idx := newIndex()
		if err := idx.IndexWithExpiration("doc", Text("alpha beta"), base.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		now = base.Add(2 * time.Minute)
		_, _, err := idx.SearchMatchTopKContext(context.Background(), "alpha", 10, nil, MatchOptions{}, Budget{MaxExpirationVisits: 10, MaxPostingVisits: 1})
		var exhausted *BudgetExceededError
		if !errors.As(err, &exhausted) || exhausted.Kind != WorkPostingVisits {
			t.Fatalf("error = %v", err)
		}
		if got := idx.MemoryStats().PhysicalDocuments; got != 1 {
			t.Fatalf("document mutated after rejected posting charge: %d", got)
		}
	})

	t.Run("cancellation returns no results before mutation", func(t *testing.T) {
		now = base
		idx := newIndex()
		if err := idx.IndexWithExpiration("doc", Text("alpha"), base.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		now = base.Add(2 * time.Minute)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		results, _, err := idx.SearchMatchTopKContext(ctx, "alpha", 10, nil, MatchOptions{}, Budget{})
		if !errors.Is(err, context.Canceled) || results != nil {
			t.Fatalf("results=%v error=%v, want nil/context canceled", results, err)
		}
		if got := idx.MemoryStats().PhysicalDocuments; got != 1 {
			t.Fatalf("documents after canceled purge = %d, want 1", got)
		}
	})
}

func TestExpiryHeapLifecycle(t *testing.T) {
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	now := base
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID, WithIndexClock(func() time.Time { return now }))
	for i := range 1000 {
		if err := idx.IndexWithExpiration("same", Text("alpha"), base.Add(time.Duration(i+1)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if stats := idx.MemoryStats(); stats.PhysicalDocuments != 1 || stats.ExpirationQueueEntries != 1 {
		t.Fatalf("overwrite stats = %+v", stats)
	}
	idx.Compact()
	if stats := idx.MemoryStats(); stats.PhysicalDocuments != 1 || stats.ExpirationQueueEntries != 1 {
		t.Fatalf("compacted stats = %+v", stats)
	}
	now = base.Add(1001 * time.Hour)
	if got := idx.Search("alpha"); len(got) != 0 {
		t.Fatalf("expired compacted result = %v", got)
	}
	if stats := idx.MemoryStats(); stats.PhysicalDocuments != 0 || stats.ExpirationQueueEntries != 0 {
		t.Fatalf("purged stats = %+v", stats)
	}
}

func TestExpiryOverwriteAndDeleteLifecycle(t *testing.T) {
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	now := base
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID, WithIndexClock(func() time.Time { return now }))

	if err := idx.IndexWithExpiration("earlier", Text("alpha"), base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexWithExpiration("earlier", Text("alpha"), base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexWithExpiration("later", Text("alpha"), base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexWithExpiration("later", Text("alpha"), base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexWithExpiration("delete", Text("alpha"), base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	idx.Delete("delete")
	if err := idx.IndexWithExpiration("born-expired", Text("alpha"), base.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if stats := idx.MemoryStats(); stats.PhysicalDocuments != 2 || stats.ExpirationQueueEntries != 2 {
		t.Fatalf("pre-expiry lifecycle stats = %+v", stats)
	}
	now = base.Add(2 * time.Minute)
	results := idx.Search("alpha")
	if len(results) != 1 || results[0].ID != "later" {
		t.Fatalf("results after expiration overwrite = %+v, want later", results)
	}
	if stats := idx.MemoryStats(); stats.PhysicalDocuments != 1 || stats.ExpirationQueueEntries != 1 {
		t.Fatalf("post-expiry lifecycle stats = %+v", stats)
	}
}

func TestExpiryPurgeConcurrentMutation(t *testing.T) {
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	var nowNanos atomic.Int64
	nowNanos.Store(base.UnixNano())
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID, WithPositions(), WithIndexClock(func() time.Time {
		return time.Unix(0, nowNanos.Load())
	}))
	for i := range 200 {
		if err := idx.IndexWithExpiration(fmt.Sprintf("expired-%03d", i), Text("alpha beta"), base.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	nowNanos.Store(base.Add(2 * time.Minute).UnixNano())

	start := make(chan struct{})
	errs := make(chan error, 3)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		<-start
		for range 100 {
			if _, _, err := idx.SearchMatchTopKContext(context.Background(), "alpha", 10, nil, MatchOptions{}, Budget{}); err != nil {
				errs <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := range 200 {
			if err := idx.IndexWithExpiration(fmt.Sprintf("live-%03d", i), Text("alpha gamma"), base.Add(time.Hour)); err != nil {
				errs <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := range 200 {
			idx.Delete(fmt.Sprintf("expired-%03d", i))
		}
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	results := idx.Search("alpha")
	if len(results) != 200 {
		t.Fatalf("live results = %d, want 200", len(results))
	}
	if stats := idx.MemoryStats(); stats.Documents != 200 || stats.PhysicalDocuments != 200 || stats.ExpiredDocuments != 0 || stats.ExpirationQueueEntries != 200 {
		t.Fatalf("concurrent lifecycle stats = %+v", stats)
	}
}

func BenchmarkExpirationPurge(b *testing.B) {
	for _, documents := range []int{1_000, 10_000, 65_536} {
		for _, positions := range []bool{false, true} {
			name := fmt.Sprintf("documents=%d/positions=%t", documents, positions)
			b.Run(name, func(b *testing.B) {
				base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
				now := base
				options := []IndexOption{WithIndexClock(func() time.Time { return now })}
				if positions {
					options = append(options, WithPositions())
				}
				budget := Budget{MaxExpirationVisits: int64(documents), MaxPostingVisits: int64(documents * 2)}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					b.StopTimer()
					now = base
					idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID, options...)
					for i := range documents {
						if err := idx.IndexWithExpiration(fmt.Sprintf("doc-%06d", i), Text("alpha beta"), base.Add(time.Minute)); err != nil {
							b.Fatal(err)
						}
					}
					now = base.Add(2 * time.Minute)
					b.StartTimer()
					_, stats, err := idx.SearchMatchTopKContext(context.Background(), "missing", 10, nil, MatchOptions{}, budget)
					if err != nil {
						b.Fatal(err)
					}
					if stats.ExpirationVisits != int64(documents) {
						b.Fatalf("expiration visits = %d, want %d", stats.ExpirationVisits, documents)
					}
				}
				if elapsed := b.Elapsed().Seconds(); elapsed > 0 {
					b.ReportMetric(float64(documents*b.N)/elapsed, "docs/s")
				}
			})
		}
	}
}
