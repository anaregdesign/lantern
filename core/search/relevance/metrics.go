package relevance

import (
	"math"
	"sort"
)

// EvalDepth is the ranking window every metric is computed over: a ranked list
// is truncated to its top EvalDepth entries before any metric sees it. Pinning
// one depth keeps Lantern runs and replayed baseline runs (which record only
// their top EvalDepth results) exactly comparable — a first relevant document
// below the window contributes zero to every metric on both sides.
const EvalDepth = 50

// NDCGDepth is the cut-off for the primary ranking metric, nDCG@10.
const NDCGDepth = 10

// Metrics is the corpus-level relevance scorecard: each field is the macro
// average (mean over queries) of the per-query metric.
type Metrics struct {
	// NDCG10 is normalized discounted cumulative gain at rank 10 — the primary
	// metric: it rewards putting highly-graded documents near the top.
	NDCG10 float64
	// MRR is the mean reciprocal rank of the first relevant (grade > 0)
	// document within the EvalDepth window.
	MRR float64
	// Recall50 is the share of relevant (grade > 0) documents surfaced within
	// the EvalDepth window.
	Recall50 float64
}

// NDCGAt returns nDCG@k for one ranked list against graded judgments: DCG with
// the standard exponential gain (2^grade − 1) and log2(rank+1) discount,
// normalized by the ideal DCG of the same judgments. A query with no positive
// grades returns 0. Documents absent from qrels count as grade 0.
func NDCGAt(k int, ranked []string, qrels map[string]int) float64 {
	ideal := idealDCG(k, qrels)
	if ideal == 0 {
		return 0
	}
	var dcg float64
	for i, id := range ranked {
		if i >= k {
			break
		}
		if grade := qrels[id]; grade > 0 {
			dcg += gain(grade) / math.Log2(float64(i)+2)
		}
	}
	return dcg / ideal
}

// ReciprocalRank returns 1/rank of the first document with a positive grade,
// or 0 when the list surfaces none.
func ReciprocalRank(ranked []string, qrels map[string]int) float64 {
	for i, id := range ranked {
		if qrels[id] > 0 {
			return 1 / float64(i+1)
		}
	}
	return 0
}

// RecallAt returns the fraction of the relevant (grade > 0) documents that
// appear in the top k of the ranked list. A query with no relevant documents
// returns 0.
func RecallAt(k int, ranked []string, qrels map[string]int) float64 {
	relevant := 0
	for _, grade := range qrels {
		if grade > 0 {
			relevant++
		}
	}
	if relevant == 0 {
		return 0
	}
	found := 0
	for i, id := range ranked {
		if i >= k {
			break
		}
		if qrels[id] > 0 {
			found++
		}
	}
	return float64(found) / float64(relevant)
}

// Evaluate runs every query of the corpus through rank — a function returning
// document IDs ordered most-relevant first — and macro-averages the metrics.
// Each ranked list is truncated to EvalDepth first (see EvalDepth for why), so
// rank may return more without affecting the result. Rankers built on a
// Searcher should go through RankSearcher so score ties are broken
// deterministically.
func Evaluate(c Corpus, rank func(q Query) []string) Metrics {
	if len(c.Queries) == 0 {
		return Metrics{}
	}
	var m Metrics
	for _, q := range c.Queries {
		ranked := rank(q)
		if len(ranked) > EvalDepth {
			ranked = ranked[:EvalDepth]
		}
		m.NDCG10 += NDCGAt(NDCGDepth, ranked, q.Qrels)
		m.MRR += ReciprocalRank(ranked, q.Qrels)
		m.Recall50 += RecallAt(EvalDepth, ranked, q.Qrels)
	}
	n := float64(len(c.Queries))
	m.NDCG10 /= n
	m.MRR /= n
	m.Recall50 /= n
	return m
}

// gain is the standard exponential nDCG gain for a relevance grade.
func gain(grade int) float64 {
	return math.Exp2(float64(grade)) - 1
}

// idealDCG is the DCG of the best possible ordering of the judged documents:
// grades sorted descending, discounted at ranks 1..k.
func idealDCG(k int, qrels map[string]int) float64 {
	grades := make([]int, 0, len(qrels))
	for _, grade := range qrels {
		if grade > 0 {
			grades = append(grades, grade)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(grades)))
	var dcg float64
	for i, grade := range grades {
		if i >= k {
			break
		}
		dcg += gain(grade) / math.Log2(float64(i)+2)
	}
	return dcg
}
