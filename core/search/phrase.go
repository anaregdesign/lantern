package search

import "sort"

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
// matches nothing, returns nil; ties in score have an unspecified order.
func (idx *InvertedIndex[S, D]) SearchPhrase(query string) []Result[S] {
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
	results := make([]Result[S], 0, len(scores))
	for ord, score := range scores {
		results = append(results, Result[S]{ID: idx.docs[ord].id, Score: score})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return results
}

// SearchPhraseTopK is the bounded-selection sibling of SearchPhrase, mirroring
// SearchTopK (#841): it phrase-matches then returns only the k highest-scoring
// documents that satisfy accept, without fully sorting the match set. accept
// gates a document before it can occupy one of the k slots (dead vertices,
// out-of-scope keys) and may be nil (accept everything). The lock order and
// accept contract are identical to SearchTopK. k <= 0, a query with no word
// terms, or zero accepted matches return nil.
func (idx *InvertedIndex[S, D]) SearchPhraseTopK(query string, k int, accept func(id S) bool) []Result[S] {
	if k <= 0 {
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
	return idx.selectTopKLocked(scores, k, accept)
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
	cp := &idx.classes[ClassWord]
	if cp.docCount == 0 {
		return nil
	}
	// Resolve every query term's posting list; a missing term means the phrase
	// cannot occur anywhere.
	lists := make([]*postingList, len(terms))
	for i, term := range terms {
		tid, ok := cp.dict.lookup(term)
		if !ok {
			return nil
		}
		pl := cp.postings[tid]
		if pl == nil {
			return nil
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
	intersection := lists[rarest].docs.Clone()
	for i := range lists {
		if i != rarest {
			intersection.And(lists[i].docs)
		}
	}
	if intersection.IsEmpty() {
		return nil
	}
	out := make([]uint32, 0, intersection.GetCardinality())
	for it := intersection.Iterator(); it.HasNext(); {
		ord := it.Next()
		if phraseAdjacent(lists, ord) {
			out = append(out, ord)
		}
	}
	return out
}

// scorePhraseLocked scores the phrase-matched ordinals over the query's
// distinct word terms with the same BM25 statistics as the OR-union path,
// restricted to the ClassWord channel (phrase terms are word-class). Callers
// must hold idx.mu.
func (idx *InvertedIndex[S, D]) scorePhraseLocked(terms []string, ords []uint32) map[uint32]float64 {
	cp := &idx.classes[ClassWord]
	if cp.docCount == 0 {
		return nil
	}
	avgLen := float64(cp.totalLen) / float64(cp.docCount)
	scores := make(map[uint32]float64, len(ords))
	seen := make(map[string]struct{}, len(terms))
	for _, term := range terms {
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
			scores[ord] += idx.scorer.Score(TermStats{
				TF:     pl.tf(ord),
				DF:     df,
				N:      cp.docCount,
				DocLen: idx.docs[ord].lengths[ClassWord],
				AvgLen: avgLen,
				Class:  ClassWord,
			})
		}
	}
	return scores
}

// phraseAdjacent reports whether the query terms occur at consecutive positions
// in document ord, in query order. When the index tracks no positions (the
// first term's slice is nil) it returns true, degrading the phrase to the
// AND-intersection the caller already established. A single-term phrase is
// trivially adjacent. lists holds one posting list per query term in order; ord
// is a member of all of them.
func phraseAdjacent(lists []*postingList, ord uint32) bool {
	if len(lists) < 2 {
		return true
	}
	first := lists[0].positionsOf(ord)
	if first == nil {
		return true // positions not tracked: degrade to AND
	}
	// The phrase occurs iff some position p of the first term is followed by
	// term i at p+i for every i. Positions are ascending, so each follow-on
	// check is a binary search.
	for _, p := range first {
		if phraseStartsAt(lists, ord, p) {
			return true
		}
	}
	return false
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
