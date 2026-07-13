package search

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"testing"
	"time"
)

func exhaustiveTopK[S comparable](all []Result[S], k int, accept func(S) bool) []Result[S] {
	out := make([]Result[S], 0, min(k, len(all)))
	for _, result := range all {
		if accept != nil && !accept(result.ID) {
			continue
		}
		out = append(out, result)
		if len(out) == k {
			break
		}
	}
	return out
}

func TestBoundedExecutorMatchesExhaustive(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	idx := NewInvertedIndex[string, Text](NewScriptAwareAnalyzer(), ClassWeighted{
		Base: BM25{K1: DefaultBM25K1, B: DefaultBM25B}, GramWeight: DefaultGramWeight,
	}, compareStringID, WithPositions(), WithIndexClock(func() time.Time { return now }))
	rng := rand.New(rand.NewSource(1060))
	words := []string{"alpha", "alpine", "beta", "better", "gamma", "garden", "delta", "search", "service", "vertex"}
	ids := make([]string, 240)
	for i := range ids {
		ids[i] = fmt.Sprintf("tenant-%02d/doc-%03d", i%12, i)
		body := words[rng.Intn(len(words))] + " " + words[rng.Intn(len(words))] + " " + words[rng.Intn(len(words))]
		if i%17 == 0 {
			body += " alpha beta"
		}
		expiration := time.Time{}
		if i%19 == 0 {
			expiration = now.Add(time.Minute)
		}
		if err := idx.IndexWithExpiration(ids[i], Text(body), expiration); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(2 * time.Minute)

	tests := []struct {
		query string
		opts  MatchOptions
	}{
		{"alpha beta", MatchOptions{}},
		{"alpha beta", MatchOptions{Mode: MatchAll}},
		{"alpha beta gamma", MatchOptions{Mode: MatchMinShould, MinShouldMatch: 2}},
		{"alp", MatchOptions{PrefixTerms: true}},
		{"serch", MatchOptions{Fuzziness: 1}},
		{"alp serch", MatchOptions{Mode: MatchAll, PrefixTerms: true, Fuzziness: 1}},
	}
	accepts := []func(string) bool{
		nil,
		func(id string) bool { return id[len(id)-1]%2 == 0 },
	}
	for _, tc := range tests {
		full := idx.SearchMatch(tc.query, tc.opts)
		for _, k := range []int{1, 7, 31, 500} {
			for _, accept := range accepts {
				got, _, err := idx.SearchMatchTopKContext(context.Background(), tc.query, k, accept, tc.opts, Budget{})
				if err != nil {
					t.Fatal(err)
				}
				want := exhaustiveTopK(full, k, accept)
				if !slices.Equal(got, want) {
					t.Fatalf("query=%q opts=%+v k=%d:\n got  %v\n want %v", tc.query, tc.opts, k, got, want)
				}
			}
		}

		scopedIDs := make([]string, 0, len(ids)/12)
		for _, id := range ids {
			if len(id) >= len("tenant-03/") && id[:len("tenant-03/")] == "tenant-03/" {
				scopedIDs = append(scopedIDs, id)
			}
		}
		source := CandidateSource[string](func(yield func(string) bool) {
			for _, id := range scopedIDs {
				if !yield(id) {
					return
				}
			}
		})
		got, stats, err := idx.SearchMatchTopKCandidatesContextAt(context.Background(), tc.query, 13, nil, tc.opts, Budget{}, time.Now(), source)
		if err != nil {
			t.Fatal(err)
		}
		inScope := func(id string) bool { return id[:len("tenant-03/")] == "tenant-03/" }
		want := exhaustiveTopK(full, 13, inScope)
		if !slices.Equal(got, want) {
			t.Fatalf("scoped query=%q opts=%+v:\n got  %v\n want %v", tc.query, tc.opts, got, want)
		}
		if stats.CandidateVisits != int64(len(scopedIDs)) {
			t.Fatalf("scoped query=%q candidate visits=%d, want %d", tc.query, stats.CandidateVisits, len(scopedIDs))
		}
	}

	t.Run("randomized modes and expansions", func(t *testing.T) {
		for trial := 0; trial < 100; trial++ {
			queryTerms := 1 + rng.Intn(3)
			query := words[rng.Intn(len(words))]
			for i := 1; i < queryTerms; i++ {
				query += " " + words[rng.Intn(len(words))]
			}
			opts := MatchOptions{Mode: MatchMode(rng.Intn(3))}
			if opts.Mode == MatchMinShould {
				opts.MinShouldMatch = 1 + rng.Intn(queryTerms)
			}
			switch rng.Intn(4) {
			case 1:
				opts.PrefixTerms = true
			case 2:
				opts.Fuzziness = 1
			case 3:
				opts.PrefixTerms = true
				opts.Fuzziness = 1
			}
			k := 1 + rng.Intn(40)
			accept := func(id string) bool { return id[len(id)-1]%3 != 0 }
			want := exhaustiveTopK(idx.SearchMatch(query, opts), k, accept)
			got, _, err := idx.SearchMatchTopKContext(context.Background(), query, k, accept, opts, Budget{})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("trial=%d query=%q opts=%+v k=%d:\n got  %v\n want %v", trial, query, opts, k, got, want)
			}
		}
	})
}

func TestBoundedPhraseExecutorMatchesExhaustive(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID, WithPositions())
	for i := 0; i < 120; i++ {
		body := "alpha gap beta"
		if i%3 == 0 {
			body = "alpha beta gamma"
		}
		if i%7 == 0 {
			body = "beta alpha beta"
		}
		if err := idx.Index(fmt.Sprintf("scope-%d/doc-%03d", i%5, i), Text(body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{"alpha beta", "beta alpha beta"} {
		full := idx.SearchPhrase(query)
		for _, k := range []int{1, 9, 200} {
			got := idx.SearchPhraseTopK(query, k, nil)
			want := exhaustiveTopK(full, k, nil)
			if !slices.Equal(got, want) {
				t.Fatalf("query=%q k=%d:\n got  %v\n want %v", query, k, got, want)
			}
		}
		source := CandidateSource[string](func(yield func(string) bool) {
			for i := 0; i < 120; i++ {
				if i%5 == 2 && !yield(fmt.Sprintf("scope-%d/doc-%03d", i%5, i)) {
					return
				}
			}
		})
		got, _, err := idx.SearchPhraseTopKCandidatesContextAt(context.Background(), query, 10, nil, Budget{}, time.Now(), source)
		if err != nil {
			t.Fatal(err)
		}
		want := exhaustiveTopK(full, 10, func(id string) bool { return id[:len("scope-2/")] == "scope-2/" })
		if !slices.Equal(got, want) {
			t.Fatalf("scoped phrase=%q:\n got  %v\n want %v", query, got, want)
		}
	}
}

func TestBoundedTopKAllocationsDoNotScaleWithMatches(t *testing.T) {
	build := func(n int) *InvertedIndex[string, Text] {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID, WithPositions())
		for i := 0; i < n; i++ {
			if err := idx.Index(fmt.Sprintf("doc-%06d", i), Text("shared phrase")); err != nil {
				t.Fatal(err)
			}
		}
		return idx
	}
	small := build(1_000)
	large := build(20_000)
	smallAllocs := testing.AllocsPerRun(5, func() { _ = small.SearchTopK("shared phrase", 10, nil) })
	largeAllocs := testing.AllocsPerRun(5, func() { _ = large.SearchTopK("shared phrase", 10, nil) })
	if largeAllocs > smallAllocs+4 {
		t.Fatalf("allocations scale with matches: 1k=%0.1f 20k=%0.1f", smallAllocs, largeAllocs)
	}
}

func BenchmarkBoundedTopKOneMillion(b *testing.B) {
	for _, documents := range []int{1_000, 1_000_000} {
		b.Run(fmt.Sprintf("documents=%d", documents), func(b *testing.B) {
			idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID, WithPositions())
			for i := 0; i < documents; i++ {
				if err := idx.Index(fmt.Sprintf("doc-%07d", i), Text("shared phrase")); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				results := idx.SearchTopK("shared phrase", 10, nil)
				if len(results) != 10 {
					b.Fatalf("results=%d, want 10", len(results))
				}
			}
		})
	}
}
