package search

import "context"

// MatchMode selects how a multi-term query's word-channel terms combine to
// decide which documents match. It governs membership only — the Scorer still
// ranks whatever matches — and counts distinct query terms on the primary
// (word) channel, so the auxiliary intra-word grams a script-aware analyzer
// emits are evidence for ranking but never change how many terms a document
// must satisfy. For a single-channel (word-only) analyzer every match is a word
// match, so the modes reduce to the textbook boolean semantics.
type MatchMode uint8

const (
	// MatchAny keeps every document sharing at least one query term (the
	// OR-union). It is the zero value and the package default, because the
	// graph-seeding path Search feeds wants maximal recall.
	MatchAny MatchMode = iota
	// MatchAll keeps only documents containing every distinct query word term
	// (boolean AND), the precision end of the scale.
	MatchAll
	// MatchMinShould keeps documents containing at least MinShouldMatch distinct
	// query word terms — the tunable middle ground between Any and All.
	MatchMinShould
)

// MatchOptions configures document membership for a query. The zero value is
// MatchAny with no term expansion, so an empty MatchOptions reproduces Search's
// OR-union exactly.
type MatchOptions struct {
	// Mode selects the boolean combination of the query's word terms.
	Mode MatchMode
	// MinShouldMatch is the minimum number of distinct query word terms a
	// document must contain when Mode is MatchMinShould. It is clamped to
	// [1, number of distinct query word terms]; other modes ignore it.
	MinShouldMatch int
	// Fuzziness is the maximum edit distance (0, 1, or 2) at which a word-channel
	// query term also matches dictionary terms, so a typo like "serach" still
	// finds "search". 0 (the default) disables fuzzy matching. CJK grams are
	// exempt. Expansion per query term is capped at MaxTermExpansions.
	Fuzziness int
	// PrefixTerms, when set, also matches dictionary terms that extend a
	// word-channel query term, so "lan" finds "lantern" (a prefix query). CJK
	// grams are exempt; expansion is capped at MaxTermExpansions.
	PrefixTerms bool
}

// SearchMatch is Search with an explicit match mode: it ranks the documents
// that satisfy opts by descending BM25 score. Search is exactly
// SearchMatch(query, MatchOptions{}) (MatchAny). MatchAll and MatchMinShould
// narrow the OR-union to documents that cover enough of the query's word terms,
// raising precision on multi-word queries; the score is unchanged (still the
// full word+gram BM25 sum), so ranking within the narrowed set is identical to
// the OR-union's ranking of the same documents. A query with no analyzable
// terms, or one that nothing satisfies, returns nil. Equal scores use the
// index's required typed document-ID comparator ascending.
func (idx *InvertedIndex[S, D]) SearchMatch(query string, opts MatchOptions) []Result[S] {
	queryTerms := idx.queryTerms(query)
	if len(queryTerms) == 0 {
		return nil
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	scores := idx.scoredMatchesLocked(queryTerms, opts)
	if len(scores) == 0 {
		return nil
	}
	return idx.rankedResultsLocked(scores)
}

// SearchMatchTopK is the bounded-selection sibling of SearchMatch, combining
// its match mode with the size-k selection and accept gating of SearchTopK.
// SearchTopK is SearchMatchTopK(query, k, accept, MatchOptions{}). The lock
// order and accept contract are identical to SearchTopK. k <= 0, an unanalyzable
// query, or zero accepted matches return nil.
func (idx *InvertedIndex[S, D]) SearchMatchTopK(query string, k int, accept func(id S) bool, opts MatchOptions) []Result[S] {
	results, _, _ := idx.SearchMatchTopKContext(context.Background(), query, k, accept, opts, Budget{})
	return results
}

// SearchMatchTopKContext is SearchMatchTopK with cancellation and deterministic
// work accounting. It never returns partial results on error.
func (idx *InvertedIndex[S, D]) SearchMatchTopKContext(ctx context.Context, query string, k int, accept func(id S) bool, opts MatchOptions, budget Budget) ([]Result[S], Stats, error) {
	work := newWorkTracker(ctx, budget)
	if k <= 0 {
		return nil, work.stats, nil
	}
	queryTerms := idx.queryTerms(query)
	if err := work.visit(WorkQueryTerms, int64(len(queryTerms))); err != nil {
		return nil, work.stats, err
	}
	if len(queryTerms) == 0 {
		return nil, work.stats, nil
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	scores, err := idx.scoredMatchesTrackedLocked(queryTerms, opts, work)
	if err != nil {
		return nil, work.stats, err
	}
	if len(scores) == 0 {
		return nil, work.stats, nil
	}
	results, err := idx.selectTopKTrackedLocked(scores, k, accept, work)
	if err != nil {
		return nil, work.stats, err
	}
	return results, work.stats, nil
}

// scoredMatchesLocked builds the final score map for a query under opts: the
// OR-union BM25 scores, narrowed by the match mode's word-coverage filter when
// the mode is not MatchAny, then lifted by the proximity boost. When opts request
// prefix or fuzzy expansion it runs the clause-based path instead (which folds
// expansion into both scoring and coverage); the exact-term default keeps using
// scoreLocked + filterByCoverageLocked so it is byte-for-byte unchanged. Callers
// must hold idx.mu.
func (idx *InvertedIndex[S, D]) scoredMatchesLocked(queryTerms []Token, opts MatchOptions) map[uint32]float64 {
	scores, _ := idx.scoredMatchesTrackedLocked(queryTerms, opts, newWorkTracker(nil, Budget{}))
	return scores
}

func (idx *InvertedIndex[S, D]) scoredMatchesTrackedLocked(queryTerms []Token, opts MatchOptions, work *workTracker) (map[uint32]float64, error) {
	var scores map[uint32]float64
	if opts.expandsTerms() {
		clauses, err := idx.buildClausesTrackedLocked(queryTerms, opts, work)
		if err != nil {
			return nil, err
		}
		scores, err = idx.scoreClausesTrackedLocked(clauses, opts, work)
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		scores, err = idx.scoreTrackedLocked(queryTerms, work)
		if err != nil {
			return nil, err
		}
		if opts.Mode != MatchAny {
			if err := idx.filterByCoverageTrackedLocked(scores, queryTerms, opts, work); err != nil {
				return nil, err
			}
		}
	}
	if len(scores) == 0 {
		return scores, nil
	}
	if err := idx.applyProximityTrackedLocked(scores, queryTerms, work); err != nil {
		return nil, err
	}
	dropNonFiniteScores(scores)
	return scores, nil
}

// filterByCoverageLocked drops documents from scores that do not cover enough
// of the query's distinct word-channel terms for opts.Mode: every term for
// MatchAll, at least MinShouldMatch for MatchMinShould. Coverage counts primary
// (ClassWord) terms only — the auxiliary gram channel is ranking evidence, not
// a membership vote — so requiring "all terms" of a CJK run means all of its
// bigrams, matching Lucene's CJKAnalyzer under AND. A query word term absent
// from the corpus still raises the bar it can never meet, so an AND with any
// missing term is correctly empty. Callers must hold idx.mu.
func (idx *InvertedIndex[S, D]) filterByCoverageLocked(scores map[uint32]float64, queryTerms []Token, opts MatchOptions) {
	_ = idx.filterByCoverageTrackedLocked(scores, queryTerms, opts, newWorkTracker(nil, Budget{}))
}

func (idx *InvertedIndex[S, D]) filterByCoverageTrackedLocked(scores map[uint32]float64, queryTerms []Token, opts MatchOptions, work *workTracker) error {
	cp := &idx.classes[ClassWord]
	numWords := 0
	coverage := make(map[uint32]int, len(scores))
	for _, token := range queryTerms {
		if err := work.check(); err != nil {
			return err
		}
		if token.Class != ClassWord {
			continue
		}
		numWords++
		tid, ok := cp.dict.lookup(token.Term)
		if !ok {
			continue // absent term: raises the bar, covers no document
		}
		pl := cp.postings[tid]
		if pl == nil {
			continue
		}
		for it := pl.docs.Iterator(); it.HasNext(); {
			if err := work.visit(WorkPostingVisits, 1); err != nil {
				return err
			}
			coverage[it.Next()]++
		}
	}
	threshold := coverageThreshold(opts, numWords)
	if threshold <= 0 {
		return nil // nothing required (MatchAny, or a query with no word terms)
	}
	for ord := range scores {
		if err := work.check(); err != nil {
			return err
		}
		if coverage[ord] < threshold {
			delete(scores, ord)
		}
	}
	return nil
}

// coverageThreshold resolves the minimum word-term coverage a document needs
// under opts, given the query's numWords distinct word terms. MatchAll requires
// them all; MatchMinShould requires MinShouldMatch clamped to [1, numWords];
// MatchAny (or a query with no word terms) requires nothing (0).
func coverageThreshold(opts MatchOptions, numWords int) int {
	if numWords == 0 {
		return 0
	}
	switch opts.Mode {
	case MatchAll:
		return numWords
	case MatchMinShould:
		msm := opts.MinShouldMatch
		if msm < 1 {
			msm = 1
		}
		if msm > numWords {
			msm = numWords
		}
		return msm
	default:
		return 0
	}
}
