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
func (idx *InvertedIndex[S, D]) applyProximityLocked(scores map[uint32]float64, queryTerms []Token) {
	_ = idx.applyProximityTrackedLocked(scores, queryTerms, newWorkTracker(nil, Budget{}))
}

func (idx *InvertedIndex[S, D]) applyProximityTrackedLocked(scores map[uint32]float64, queryTerms []Token, work *workTracker) error {
	if !idx.positions || idx.proximityWeight == 0 || len(scores) == 0 {
		return nil
	}
	cp := &idx.classes[ClassWord]
	if cp.docCount == 0 {
		return nil
	}
	// Resolve the distinct word-channel query terms that exist in the corpus;
	// fewer than two means there is no pair whose distance could matter.
	var lists []*postingList
	for _, token := range queryTerms {
		if err := work.check(); err != nil {
			return err
		}
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
		return nil
	}
	var scratch executorScratch
	for ord := range scores {
		if err := work.check(); err != nil {
			return err
		}
		var best float64
		for field := FieldID(0); field < numDocumentFields; field++ {
			present, err := scratch.decode(lists, ord, field, work)
			if err != nil {
				return err
			}
			bonus, err := scratch.proximityBonus(present, work)
			if err != nil {
				return err
			}
			if bonus > best {
				best = bonus
			}
		}
		if best > 0 {
			addScore(scores, ord, idx.proximityWeight*best)
		}
	}
	return nil
}

// smallestWindow returns the width (largest position minus smallest) of the
// smallest window that includes at least one position from each list, or -1 if
// any list is empty. Each list must be ascending. It is the classic "smallest
// range covering elements from k sorted lists" sweep: repeatedly advance the
// pointer sitting at the current minimum — the only move that can shrink the
// window — until that list is exhausted.
func smallestWindow(lists [][]uint64) int {
	best, _ := smallestWindowTracked(lists, newWorkTracker(nil, Budget{}))
	return best
}

func smallestWindowTracked(lists [][]uint64, work *workTracker) (int, error) {
	ptr := make([]int, len(lists))
	return smallestWindowWithPointersTracked(lists, ptr, work)
}

func smallestWindowWithPointersTracked(lists [][]uint64, ptr []int, work *workTracker) (int, error) {
	clear(ptr)
	best := -1
	for {
		if err := work.check(); err != nil {
			return -1, err
		}
		lo := ^uint64(0)
		var hi uint64
		loList := -1
		for i, l := range lists {
			if len(l) == 0 {
				return -1, nil
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
				return 0, nil
			}
		}
		ptr[loList]++
		if ptr[loList] == len(lists[loList]) {
			return best, nil
		}
	}
}
