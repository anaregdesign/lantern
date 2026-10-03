package search

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestCorpusCandidatesIntersectBeforePayloads(t *testing.T) {
	index := NewInvertedIndex[string, Document](fakeAnalyzer{}, BM25{K1: DefaultBM25K1, B: DefaultBM25B}, compareStringID, WithPositions())
	for i := range 1000 {
		text := "ordinary"
		if i == 99 {
			text = "project launch"
		}
		if err := index.Index(fmt.Sprint(i), Text(text)); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Index("hidden", Text("project launch")); err != nil {
		t.Fatal(err)
	}
	ctx := WithCandidateFilter(context.Background(), func(id string) bool { return id != "hidden" })
	now := time.Now()
	// Every execution visits matching postings rather than the complete
	// admitted corpus. No cache or warm-up is required.
	if _, _, err := index.SearchMatchTopKCandidatesContextAt(ctx, "project", 10, nil, MatchOptions{}, Budget{}, now, nil); err != nil {
		t.Fatal(err)
	}
	hits, stats, err := index.SearchMatchTopKCandidatesContextAt(ctx, "project", 10, nil, MatchOptions{}, Budget{MaxPostingVisits: 4}, now, nil)
	if err != nil || len(hits) != 1 || hits[0].ID != "99" || stats.CandidateVisits != 1 {
		t.Fatalf("hits=%v stats=%+v err=%v", hits, stats, err)
	}
	hits, stats, err = index.SearchPhraseTopKCandidatesContextAt(ctx, "project launch", 10, nil, Budget{MaxPostingVisits: 8}, now, nil)
	if err != nil || len(hits) != 1 || hits[0].ID != "99" || stats.CandidateVisits != 1 {
		t.Fatalf("phrase hits=%v stats=%+v err=%v", hits, stats, err)
	}
	hits, _, err = index.SearchMatchTopKCandidatesContextAt(ctx, "project", 10, nil, MatchOptions{}, Budget{}, now, func(yield func(string) bool) { yield("hidden"); yield("1") })
	if err != nil || len(hits) != 0 {
		t.Fatal("candidate source widened scope or introduced a non-match", hits, err)
	}
}
