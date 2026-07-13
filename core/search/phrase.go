package search

import (
	"context"
	"sort"
	"time"
)

// SearchPhrase returns the documents that contain the query's word-channel
// terms as a consecutive phrase, ranked by BM25 over those terms. It is the
// precision counterpart to Search's recall-oriented OR-union: "data set"
// matches a document with "... data set ..." but not one that merely scatters
// "data" and "set" far apart. Matching is the AND-intersection of the query's
// primary (ClassWord) postings, refined by position — the query terms must
// occur at consecutive positions (p, p+1, ...) in query order.
//
// When the index was built without WithPositions the position refinement is
// unavailable and SearchPhrase degrades to the pure AND-intersection (every
// term present, order unverified). A query with no word terms, or one that
// matches nothing, returns nil. Equal scores use the index's required typed
// document-ID comparator ascending.
func (idx *InvertedIndex[S, D]) SearchPhrase(query string) []Result[S] {
	if err := idx.purgeExpired(newWorkTracker(context.Background(), Budget{}), idx.clock()); err != nil {
		return nil
	}
	terms := idx.phraseQueryTerms(query)
	if len(terms) == 0 {
		return nil
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	ords := idx.phraseMatchLocked(terms)
	if len(ords) == 0 {
		return nil
	}
	scores := idx.scorePhraseLocked(terms, ords)
	return idx.rankedResultsLocked(scores)
}

// SearchPhraseTopK is the bounded-selection sibling of SearchPhrase, mirroring
// SearchTopK (#841): it phrase-matches then returns only the k highest-scoring
// documents that satisfy accept, without fully sorting the match set. accept
// gates a document before it can occupy one of the k slots (dead vertices,
// out-of-scope keys) and may be nil (accept everything). The lock order and
// accept contract are identical to SearchTopK. k <= 0, a query with no word
// terms, or zero accepted matches return nil.
func (idx *InvertedIndex[S, D]) SearchPhraseTopK(query string, k int, accept func(id S) bool) []Result[S] {
	results, _, _ := idx.SearchPhraseTopKContext(context.Background(), query, k, accept, Budget{})
	return results
}

// SearchPhraseTopKContext is SearchPhraseTopK with cancellation and
// deterministic work accounting. It never returns partial results on error.
func (idx *InvertedIndex[S, D]) SearchPhraseTopKContext(ctx context.Context, query string, k int, accept func(id S) bool, budget Budget) ([]Result[S], Stats, error) {
	return idx.SearchPhraseTopKContextAt(ctx, query, k, accept, budget, idx.clock())
}

// SearchPhraseTopKContextAt is SearchPhraseTopKContext with an explicit
// liveness instant shared with a layered store's accept callback.
func (idx *InvertedIndex[S, D]) SearchPhraseTopKContextAt(ctx context.Context, query string, k int, accept func(id S) bool, budget Budget, now time.Time) ([]Result[S], Stats, error) {
	work := newWorkTracker(ctx, budget)
	if idx.Health() != IndexHealthy {
		return nil, work.stats, ErrIndexIncomplete
	}
	if k <= 0 {
		return nil, work.stats, nil
	}
	if err := idx.purgeExpired(work, now); err != nil {
		return nil, work.stats, err
	}
	terms := idx.phraseQueryTerms(query)
	if err := work.visit(WorkQueryTerms, int64(len(terms))); err != nil {
		return nil, work.stats, err
	}
	if len(terms) == 0 {
		return nil, work.stats, nil
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if idx.health != IndexHealthy {
		return nil, work.stats, ErrIndexIncomplete
	}

	ords, err := idx.phraseMatchTrackedLocked(terms, work)
	if err != nil {
		return nil, work.stats, err
	}
	if len(ords) == 0 {
		return nil, work.stats, nil
	}
	scores, err := idx.scorePhraseTrackedLocked(terms, ords, work)
	if err != nil {
		return nil, work.stats, err
	}
	results, err := idx.selectTopKTrackedLocked(scores, k, accept, work)
	if err != nil {
		return nil, work.stats, err
	}
	return results, work.stats, nil
}

// phraseQueryTerms analyzes query and returns its primary (ClassWord) tokens in
// order — the sequence a phrase must match consecutively. Auxiliary gram tokens
// are dropped because phrase adjacency is defined on the word channel; a CJK
// run's grams are themselves ClassWord, so they are kept and verified by the
// same pos+1 rule.
func (idx *InvertedIndex[S, D]) phraseQueryTerms(query string) []string {
	tokens := idx.analyzer.Analyze(query)
	var terms []string
	for _, t := range tokens {
		if t.Class == ClassWord {
			terms = append(terms, t.Term)
		}
	}
	return terms
}

// phraseMatchLocked returns the ordinals whose primary-channel postings satisfy
// the phrase: every query term present (AND-intersection) and, when the index
// tracks positions, occurring at consecutive positions in query order. terms is
// the ordered ClassWord query terms with duplicates kept, so a repeated word
// must appear repeated and adjacent. Callers must hold idx.mu.
func (idx *InvertedIndex[S, D]) phraseMatchLocked(terms []string) []uint32 {
	ords, _ := idx.phraseMatchTrackedLocked(terms, newWorkTracker(nil, Budget{}))
	return ords
}

func (idx *InvertedIndex[S, D]) phraseMatchTrackedLocked(terms []string, work *workTracker) ([]uint32, error) {
	cp := &idx.classes[ClassWord]
	if cp.docCount == 0 {
		return nil, nil
	}
	// Resolve every query term's posting list; a missing term means the phrase
	// cannot occur anywhere.
	lists := make([]*postingList, len(terms))
	for i, term := range terms {
		if err := work.check(); err != nil {
			return nil, err
		}
		tid, ok := cp.dict.lookup(term)
		if !ok {
			return nil, nil
		}
		pl := cp.postings[tid]
		if pl == nil {
			return nil, nil
		}
		lists[i] = pl
	}
	// AND-intersect membership, starting from the rarest term so the running
	// intersection shrinks fastest.
	rarest := 0
	for i := 1; i < len(lists); i++ {
		if lists[i].cardinality() < lists[rarest].cardinality() {
			rarest = i
		}
	}
	out := make([]uint32, 0, lists[rarest].cardinality())
	for it := lists[rarest].docs.Iterator(); it.HasNext(); {
		if err := work.visit(WorkPostingVisits, 1); err != nil {
			return nil, err
		}
		ord := it.Next()
		present := true
		for i, list := range lists {
			if i == rarest {
				continue
			}
			if err := work.visit(WorkPostingVisits, 1); err != nil {
				return nil, err
			}
			if !list.docs.Contains(ord) {
				present = false
				break
			}
		}
		if !present {
			continue
		}
		adjacent, err := phraseAdjacentTracked(lists, ord, work)
		if err != nil {
			return nil, err
		}
		if adjacent {
			out = append(out, ord)
		}
	}
	return out, nil
}

// scorePhraseLocked scores the phrase-matched ordinals over the query's
// distinct word terms with the same BM25 statistics as the OR-union path,
// restricted to the ClassWord channel (phrase terms are word-class). Callers
// must hold idx.mu.
func (idx *InvertedIndex[S, D]) scorePhraseLocked(terms []string, ords []uint32) map[uint32]float64 {
	scores, _ := idx.scorePhraseTrackedLocked(terms, ords, newWorkTracker(nil, Budget{}))
	return scores
}

func (idx *InvertedIndex[S, D]) scorePhraseTrackedLocked(terms []string, ords []uint32, work *workTracker) (map[uint32]float64, error) {
	cp := &idx.classes[ClassWord]
	if cp.docCount == 0 {
		return nil, nil
	}
	avgLen := float64(cp.totalLen) / float64(cp.docCount)
	scores := make(map[uint32]float64, len(ords))
	seen := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		if err := work.check(); err != nil {
			return nil, err
		}
		if _, dup := seen[term]; dup {
			continue
		}
		seen[term] = struct{}{}
		tid, ok := cp.dict.lookup(term)
		if !ok {
			continue
		}
		pl := cp.postings[tid]
		if pl == nil {
			continue
		}
		df := pl.cardinality()
		for _, ord := range ords {
			if err := work.visit(WorkPostingVisits, 1); err != nil {
				return nil, err
			}
			addScore(scores, ord, idx.scorer.Score(TermStats{
				TF:     pl.tf(ord),
				DF:     df,
				N:      cp.docCount,
				DocLen: idx.docs[ord].lengths[ClassWord],
				AvgLen: avgLen,
				Class:  ClassWord,
			}))
		}
	}
	dropNonFiniteScores(scores)
	return scores, nil
}

// phraseAdjacent reports whether the query terms occur at consecutive positions
// in document ord, in query order. When the index tracks no positions (the
// first term's slice is nil) it returns true, degrading the phrase to the
// AND-intersection the caller already established. A single-term phrase is
// trivially adjacent. lists holds one posting list per query term in order; ord
// is a member of all of them.
func phraseAdjacent(lists []*postingList, ord uint32) bool {
	ok, _ := phraseAdjacentTracked(lists, ord, newWorkTracker(nil, Budget{}))
	return ok
}

func phraseAdjacentTracked(lists []*postingList, ord uint32, work *workTracker) (bool, error) {
	if len(lists) < 2 {
		return true, nil
	}
	first := lists[0].positionsOf(ord)
	if first == nil {
		return true, nil // positions not tracked: degrade to AND
	}
	if err := work.visit(WorkPositionVisits, int64(len(first))); err != nil {
		return false, err
	}
	// The phrase occurs iff some position p of the first term is followed by
	// term i at p+i for every i. Positions are ascending, so each follow-on
	// check is a binary search.
	for _, p := range first {
		ok, err := phraseStartsAtTracked(lists, ord, p, work)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func phraseStartsAtTracked(lists []*postingList, ord uint32, start uint32, work *workTracker) (bool, error) {
	for i := 1; i < len(lists); i++ {
		pos := lists[i].positionsOf(ord)
		if pos == nil {
			return true, nil
		}
		if err := work.visit(WorkPositionVisits, int64(len(pos))); err != nil {
			return false, err
		}
		if !containsSortedU32(pos, start+uint32(i)) {
			return false, nil
		}
	}
	return true, nil
}

// phraseStartsAt reports whether every term i (i >= 1) occurs at position
// start+i in document ord, i.e. the phrase begins at start.
func phraseStartsAt(lists []*postingList, ord uint32, start uint32) bool {
	for i := 1; i < len(lists); i++ {
		pos := lists[i].positionsOf(ord)
		if pos == nil {
			return true // positions not tracked mid-list: degrade to AND
		}
		if !containsSortedU32(pos, start+uint32(i)) {
			return false
		}
	}
	return true
}

// containsSortedU32 reports whether the ascending slice s contains v, via binary
// search.
func containsSortedU32(s []uint32, v uint32) bool {
	i := sort.Search(len(s), func(i int) bool { return s[i] >= v })
	return i < len(s) && s[i] == v
}
