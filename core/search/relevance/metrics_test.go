package relevance

import (
	"fmt"
	"math"
	"testing"
)

const eps = 1e-12

func almostEqual(a, b float64) bool { return math.Abs(a-b) < eps }

func TestNDCGAt(t *testing.T) {
	qrels := map[string]int{"a": 3, "b": 2, "c": 1}

	t.Run("HandComputed", func(t *testing.T) {
		// ranked: b (grade 2) at rank 1, x (grade 0) at rank 2, a (grade 3) at
		// rank 3. DCG = 3/log2(2) + 0 + 7/log2(4); ideal places grades 3,2,1 at
		// ranks 1,2,3. Both sides derived from the definition, independently of
		// the implementation's helpers.
		got := NDCGAt(10, []string{"b", "x", "a"}, qrels)
		dcg := 3.0/math.Log2(2) + 7.0/math.Log2(4)
		ideal := 7.0/math.Log2(2) + 3.0/math.Log2(3) + 1.0/math.Log2(4)
		if want := dcg / ideal; !almostEqual(got, want) {
			t.Fatalf("NDCGAt = %v, want %v", got, want)
		}
	})

	t.Run("PerfectRankingIsOne", func(t *testing.T) {
		if got := NDCGAt(10, []string{"a", "b", "c"}, qrels); !almostEqual(got, 1) {
			t.Fatalf("perfect ranking nDCG = %v, want 1", got)
		}
	})

	t.Run("CutoffExcludesLateHits", func(t *testing.T) {
		// The only relevant document sits at rank 3, beyond k=2.
		if got := NDCGAt(2, []string{"x", "y", "a"}, qrels); got != 0 {
			t.Fatalf("nDCG@2 with first hit at rank 3 = %v, want 0", got)
		}
	})

	t.Run("EmptyRankingIsZero", func(t *testing.T) {
		if got := NDCGAt(10, nil, qrels); got != 0 {
			t.Fatalf("nDCG of empty ranking = %v, want 0", got)
		}
	})

	t.Run("NoRelevantJudgmentsIsZero", func(t *testing.T) {
		if got := NDCGAt(10, []string{"a"}, map[string]int{}); got != 0 {
			t.Fatalf("nDCG with empty qrels = %v, want 0", got)
		}
	})
}

func TestReciprocalRank(t *testing.T) {
	qrels := map[string]int{"a": 1}
	if got := ReciprocalRank([]string{"x", "y", "a"}, qrels); !almostEqual(got, 1.0/3) {
		t.Fatalf("RR with hit at rank 3 = %v, want 1/3", got)
	}
	if got := ReciprocalRank([]string{"x", "y"}, qrels); got != 0 {
		t.Fatalf("RR with no hit = %v, want 0", got)
	}
	if got := ReciprocalRank(nil, qrels); got != 0 {
		t.Fatalf("RR of empty ranking = %v, want 0", got)
	}
}

func TestRecallAt(t *testing.T) {
	qrels := map[string]int{"a": 3, "b": 1, "c": 2}
	if got := RecallAt(50, []string{"a", "x", "c"}, qrels); !almostEqual(got, 2.0/3) {
		t.Fatalf("recall@50 = %v, want 2/3", got)
	}
	if got := RecallAt(1, []string{"a", "b", "c"}, qrels); !almostEqual(got, 1.0/3) {
		t.Fatalf("recall@1 = %v, want 1/3", got)
	}
	if got := RecallAt(50, []string{"a"}, map[string]int{}); got != 0 {
		t.Fatalf("recall with no relevant docs = %v, want 0", got)
	}
}

func TestEvaluate(t *testing.T) {
	t.Run("MacroAverages", func(t *testing.T) {
		c := Corpus{
			Name: "unit",
			Queries: []Query{
				{ID: "q1", Text: "one", Qrels: map[string]int{"a": 3}},
				{ID: "q2", Text: "two", Qrels: map[string]int{"b": 3}},
			},
		}
		// q1 ranks its hit first (all metrics 1), q2 misses entirely (all 0),
		// so every macro average is exactly one half.
		m := Evaluate(c, func(q Query) []string {
			if q.ID == "q1" {
				return []string{"a"}
			}
			return []string{"x"}
		})
		if !almostEqual(m.NDCG10, 0.5) || !almostEqual(m.MRR, 0.5) || !almostEqual(m.Recall50, 0.5) {
			t.Fatalf("Evaluate = %+v, want all 0.5", m)
		}
	})

	t.Run("TruncatesToEvalDepth", func(t *testing.T) {
		c := Corpus{
			Name:    "unit",
			Queries: []Query{{ID: "q1", Text: "one", Qrels: map[string]int{"hit": 3}}},
		}
		// The only relevant document sits just past the EvalDepth window, so
		// every metric must treat it as not retrieved.
		m := Evaluate(c, func(q Query) []string {
			ranked := make([]string, EvalDepth+1)
			for i := range ranked {
				ranked[i] = fmt.Sprintf("filler-%d", i)
			}
			ranked[EvalDepth] = "hit"
			return ranked
		})
		if m.NDCG10 != 0 || m.MRR != 0 || m.Recall50 != 0 {
			t.Fatalf("Evaluate beyond EvalDepth = %+v, want all 0", m)
		}
	})

	t.Run("NoQueries", func(t *testing.T) {
		if m := Evaluate(Corpus{Name: "empty"}, func(Query) []string { return nil }); m != (Metrics{}) {
			t.Fatalf("Evaluate with no queries = %+v, want zero", m)
		}
	})
}
