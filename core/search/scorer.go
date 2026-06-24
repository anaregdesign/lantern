package search

import "math"

// Default BM25 parameters, matching the values most search engines ship with.
const (
	// DefaultBM25K1 controls term-frequency saturation: how quickly extra
	// occurrences of a term stop adding to the score.
	DefaultBM25K1 = 1.2
	// DefaultBM25B controls document-length normalization, from 0 (off) to 1
	// (full): how strongly a long document is penalized for diluting a term.
	DefaultBM25B = 0.75
)

// TermStats carries the corpus statistics a Scorer needs to weight one
// (query term, matching document) pairing. The Searcher fills it in from the
// index while computing a result; a Scorer treats it as read-only input and
// holds no state of its own, so a single Scorer is safe for concurrent use.
type TermStats struct {
	// TF is how many times the term occurs in this document (term frequency).
	TF int
	// DF is how many documents in the corpus contain the term (document
	// frequency); it drives the inverse-document-frequency weight.
	DF int
	// N is the total number of documents in the corpus.
	N int
	// DocLen is the length, in tokens, of this document.
	DocLen int
	// AvgLen is the mean document length across the corpus, used to normalize
	// DocLen so long and short documents compete fairly.
	AvgLen float64
}

// Scorer assigns a relevance weight to a single (query term, document)
// pairing from the corpus statistics in stats. The Searcher sums a document's
// per-term scores to rank it, so a Scorer only has to express how one term
// contributes. Separating this from the index keeps "which documents match"
// (the index's job) independent of "how relevant each match is" (the Scorer's
// job), and lets the ranking formula be swapped without touching the postings.
// A higher score means more relevant; implementations should stay non-negative
// so that summing across terms is well behaved.
type Scorer interface {
	Score(stats TermStats) float64
}

// ScorerFunc adapts an ordinary function to the Scorer interface.
type ScorerFunc func(stats TermStats) float64

// Score calls f(stats).
func (f ScorerFunc) Score(stats TermStats) float64 { return f(stats) }

// BM25 scores a term/document pairing with the Okapi BM25 ranking function,
// the modern default for keyword relevance. It improves on plain TF-IDF in two
// ways: term frequency saturates (the 10th occurrence of a word adds far less
// than the 2nd, tuned by K1), and the score is normalized by document length
// so a long document does not rank highly just for being wordy (tuned by B).
// The inverse-document-frequency factor still rewards rare, discriminating
// terms over common ones.
//
// The zero value is usable but disables length normalization (B == 0); prefer
// the parameters installed by NewInvertedIndex when scorer is nil, namely
// K1 = 1.2 and B = 0.75. A non-positive K1 falls back to 1.2, and B is clamped
// to [0, 1]. BM25 holds no mutable state and is safe for concurrent use.
type BM25 struct {
	// K1 tunes term-frequency saturation (typical range 1.2–2.0).
	K1 float64
	// B tunes document-length normalization, from 0 (off) to 1 (full).
	B float64
}

// Score returns the BM25 contribution of one term occurrence set in a
// document. It is 0 when the term matches nothing useful (DF or N non-positive)
// so unmatched or empty corpora contribute no weight.
func (s BM25) Score(stats TermStats) float64 {
	return BM25Score(float64(stats.TF), stats.AvgLen, stats.DF, stats.N, stats.DocLen, s.K1, s.B)
}

// BM25Score is the Okapi BM25 ranking kernel shared by every BM25 surface in
// the codebase: the full-text Scorer above (integer term frequencies via
// BM25.Score) and the graph edge-weighting path in core/graphcache (float term
// frequencies, since additive decaying edge weights are routinely < 1). Keeping
// the formula in exactly one place guarantees the two ranking surfaces stay
// numerically identical.
//
// tf is the term frequency (the live edge weight for the graph path); avgLen is
// the mean document length; df, n, and docLen are the document frequency, corpus
// size, and this document's length. k1 tunes TF saturation (a non-positive value
// falls back to DefaultBM25K1) and b tunes length normalization (clamped to
// [0, 1]). It returns 0 whenever the term matches nothing useful (df, n, or tf
// non-positive), so empty or unmatched corpora contribute no weight.
func BM25Score(tf, avgLen float64, df, n, docLen int, k1, b float64) float64 {
	if df <= 0 || n <= 0 || tf <= 0 {
		return 0
	}

	if k1 <= 0 {
		k1 = DefaultBM25K1
	}
	switch {
	case b < 0:
		b = 0
	case b > 1:
		b = 1
	}

	// IDF in the ln(1 + (N - df + 0.5)/(df + 0.5)) form, which stays
	// non-negative even for terms that appear in more than half the corpus.
	idf := math.Log(1 + (float64(n)-float64(df)+0.5)/(float64(df)+0.5))

	// Length normalization: a document longer than average inflates the
	// denominator, damping its score. Guard avgLen so callers passing an
	// empty corpus do not divide by zero.
	lengthRatio := 1.0
	if avgLen > 0 {
		lengthRatio = float64(docLen) / avgLen
	}

	denom := tf + k1*(1-b+b*lengthRatio)
	if denom == 0 {
		return 0
	}
	return idf * (tf * (k1 + 1)) / denom
}

// Interface assertion: BM25 is a Scorer.
var _ Scorer = BM25{}
