package search

// proximityBoostWeight is the default multiplier for the proximity bonus added
// to a multi-term query's OR-union scores: a document where the query's
// word-channel terms cluster close together is lifted above one where they are
// scattered, the way Lucene's phrase/span proximity rewards tight matches. It
// is deliberately modest so it reorders near-ties without overturning the BM25
// ordering; 0 would disable it. WithProximityWeight overrides it per index, and
// the relevance harness sweeps that override to justify this value (#910): the
// proximity-sensitive qrels in the en and mixed corpora climb as the weight
// rises and plateau at nDCG 1.0 by this default, so a higher weight buys no
// further ranking gain while risking BM25 upsets — the plateau that pins it.
const proximityBoostWeight = 0.3

// applyProximityLocked adds a proximity bonus to the OR-union scores of a
// multi-term query when the index tracks positions (WithPositions). For each
// already-matched document carrying at least two of the query's distinct
// word-channel terms, it finds the smallest window covering one occurrence of
// each present term and adds a bonus that grows as that window tightens, so an
// exact or near phrase outranks the same terms scattered across a long
// document. Documents with fewer than two present query terms — and every
// document when positions are off or the query has a single word term — keep
// their OR-union score untouched, as does every document when the index's
// proximity weight is 0 (WithProximityWeight(0)). Callers must hold idx.mu.
//
// It runs after scoreLocked over the same match set, so it never widens the
// candidate pool: SearchTopK's bounded selection still sees exactly the
// OR-union matches, now with tighter matches ranked higher.
func (idx *InvertedIndex[S, D]) applyProximityLocked(scores map[uint32]float64, queryTerms map[Token]struct{}) {
	if !idx.positions || idx.proximityWeight == 0 || len(scores) == 0 {
		return
	}
	cp := &idx.classes[ClassWord]
	if cp.docCount == 0 {
		return
	}
	// Resolve the distinct word-channel query terms that exist in the corpus;
	// fewer than two means there is no pair whose distance could matter.
	var lists []*postingList
	for token := range queryTerms {
		if token.Class != ClassWord {
			continue
		}
		tid, ok := cp.dict.lookup(token.Term)
		if !ok {
			continue
		}
		if pl := cp.postings[tid]; pl != nil {
			lists = append(lists, pl)
		}
	}
	if len(lists) < 2 {
		return
	}
	present := make([][]uint32, 0, len(lists))
	for ord := range scores {
		present = present[:0]
		for _, pl := range lists {
			if pos := pl.positionsOf(ord); pos != nil {
				present = append(present, pos)
			}
		}
		if len(present) < 2 {
			continue
		}
		w := smallestWindow(present)
		if w < 0 {
			continue
		}
		// The tightest window for t present terms spans t-1 gaps (consecutive
		// positions), so span is the excess spread over that ideal: 0 for an
		// exact adjacency, growing as the terms drift apart. The bonus decays
		// as 1/(span+1) and scales with the number of clustered terms.
		span := w - (len(present) - 1)
		if span < 0 {
			span = 0
		}
		scores[ord] += idx.proximityWeight * float64(len(present)-1) / float64(span+1)
	}
}

// smallestWindow returns the width (largest position minus smallest) of the
// smallest window that includes at least one position from each list, or -1 if
// any list is empty. Each list must be ascending. It is the classic "smallest
// range covering elements from k sorted lists" sweep: repeatedly advance the
// pointer sitting at the current minimum — the only move that can shrink the
// window — until that list is exhausted.
func smallestWindow(lists [][]uint32) int {
	ptr := make([]int, len(lists))
	best := -1
	for {
		lo := ^uint32(0)
		var hi uint32
		loList := -1
		for i, l := range lists {
			if len(l) == 0 {
				return -1
			}
			p := l[ptr[i]]
			if p < lo {
				lo = p
				loList = i
			}
			if p > hi {
				hi = p
			}
		}
		if w := int(hi - lo); best < 0 || w < best {
			best = w
			if best == 0 {
				return 0
			}
		}
		ptr[loList]++
		if ptr[loList] == len(lists[loList]) {
			return best
		}
	}
}
