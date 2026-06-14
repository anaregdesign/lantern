package search

import (
	"math"
	"testing"
)

func TestBM25ZeroCases(t *testing.T) {
	s := BM25{K1: defaultBM25K1, B: defaultBM25B}
	cases := []struct {
		name  string
		stats TermStats
	}{
		{"no documents contain term", TermStats{TF: 1, DF: 0, N: 10, DocLen: 5, AvgLen: 5}},
		{"empty corpus", TermStats{TF: 1, DF: 1, N: 0, DocLen: 5, AvgLen: 5}},
		{"term absent from document", TermStats{TF: 0, DF: 3, N: 10, DocLen: 5, AvgLen: 5}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := s.Score(c.stats); got != 0 {
				t.Fatalf("Score = %v, want 0", got)
			}
		})
	}
}

func TestBM25IDFRewardsRareTerms(t *testing.T) {
	s := BM25{K1: defaultBM25K1, B: defaultBM25B}
	base := TermStats{TF: 1, N: 1000, DocLen: 10, AvgLen: 10}
	rare := base
	rare.DF = 1
	common := base
	common.DF = 500
	if s.Score(rare) <= s.Score(common) {
		t.Fatalf("rare term (df=1) should outscore common term (df=500): %v vs %v",
			s.Score(rare), s.Score(common))
	}
}

func TestBM25TermFrequencySaturates(t *testing.T) {
	s := BM25{K1: defaultBM25K1, B: defaultBM25B}
	base := TermStats{DF: 10, N: 1000, DocLen: 10, AvgLen: 10}
	score := func(tf int) float64 { st := base; st.TF = tf; return s.Score(st) }

	s1, s2, s3 := score(1), score(2), score(3)
	if !(s2 > s1) {
		t.Fatalf("more occurrences must score higher: tf=2 (%v) vs tf=1 (%v)", s2, s1)
	}
	// Saturation: each additional occurrence adds less than the previous one,
	// so the marginal gain from 2->3 is smaller than from 1->2.
	if (s2 - s1) <= (s3 - s2) {
		t.Fatalf("term frequency should saturate: delta(1->2)=%v, delta(2->3)=%v",
			s2-s1, s3-s2)
	}
}

func TestBM25LengthNormalizationPenalizesLongDocs(t *testing.T) {
	s := BM25{K1: defaultBM25K1, B: defaultBM25B}
	base := TermStats{TF: 1, DF: 10, N: 1000, AvgLen: 10}
	short := base
	short.DocLen = 2
	long := base
	long.DocLen = 100
	if s.Score(short) <= s.Score(long) {
		t.Fatalf("shorter doc should outscore longer doc at equal tf: %v vs %v",
			s.Score(short), s.Score(long))
	}
}

func TestBM25BZeroDisablesLengthNormalization(t *testing.T) {
	s := BM25{K1: defaultBM25K1, B: 0}
	base := TermStats{TF: 1, DF: 10, N: 1000, AvgLen: 10}
	short := base
	short.DocLen = 2
	long := base
	long.DocLen = 100
	if s.Score(short) != s.Score(long) {
		t.Fatalf("with B=0 length must not matter: %v vs %v", s.Score(short), s.Score(long))
	}
}

func TestBM25ParameterGuards(t *testing.T) {
	stats := TermStats{TF: 3, DF: 10, N: 1000, DocLen: 10, AvgLen: 10}
	// Non-positive K1 falls back to the default.
	if got, want := (BM25{B: defaultBM25B}).Score(stats), (BM25{K1: defaultBM25K1, B: defaultBM25B}).Score(stats); got != want {
		t.Fatalf("K1<=0 should default to %v: got %v, want %v", defaultBM25K1, got, want)
	}
	// B above 1 is clamped to 1.
	if got, want := (BM25{K1: defaultBM25K1, B: 5}).Score(stats), (BM25{K1: defaultBM25K1, B: 1}).Score(stats); got != want {
		t.Fatalf("B>1 should clamp to 1: got %v, want %v", got, want)
	}
}

func TestBM25KnownValue(t *testing.T) {
	// A single occurrence of a term in an average-length document, with the
	// term in 1 of 2 documents, reduces BM25 to idf*(k1+1)/(1+k1) = idf, where
	// idf = ln(1 + (N-df+0.5)/(df+0.5)).
	s := BM25{K1: defaultBM25K1, B: defaultBM25B}
	got := s.Score(TermStats{TF: 1, DF: 1, N: 2, DocLen: 10, AvgLen: 10})
	want := math.Log(1 + (2-1+0.5)/(1+0.5))
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("Score = %v, want %v", got, want)
	}
}

func TestScorerFunc(t *testing.T) {
	var s Scorer = ScorerFunc(func(TermStats) float64 { return 4.2 })
	if got := s.Score(TermStats{}); got != 4.2 {
		t.Fatalf("Score = %v, want 4.2", got)
	}
}
