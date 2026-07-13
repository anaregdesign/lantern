package search

import (
	"container/heap"
	"math"
	"sort"

	"github.com/RoaringBitmap/roaring/v2"
)

// CandidateSource streams a distinct set of document IDs into a bounded
// top-k query. A layered store can use it to push a selective secondary-index
// scope (for example a key-prefix radix) into retrieval instead of scoring the
// global posting union and filtering afterwards. The source is consumed
// synchronously while the index read lock is held; it must not call an index
// write method.
type CandidateSource[S comparable] func(yield func(S) bool)

type scoringTerm struct {
	class TokenClass
	pl    *postingList
	df    int
}

type scoringClause struct {
	class TokenClass
	terms []scoringTerm
}

type scoringPlan struct {
	clauses   []scoringClause
	wordCount int
	threshold int
	proximity []*postingList
}

type executorScratch struct {
	positions [][]uint32
	present   [][]uint32
	pointers  []int
}

func (s *executorScratch) decode(lists []*postingList, ord uint32, work *workTracker) ([][]uint32, error) {
	if len(s.positions) < len(lists) {
		s.positions = append(s.positions, make([][]uint32, len(lists)-len(s.positions))...)
	}
	s.present = s.present[:0]
	for i, pl := range lists {
		s.positions[i] = pl.positionsInto(ord, s.positions[i])
		if len(s.positions[i]) == 0 {
			continue
		}
		if err := work.visit(WorkPositionVisits, int64(len(s.positions[i]))); err != nil {
			return nil, err
		}
		s.present = append(s.present, s.positions[i])
	}
	return s.present, nil
}

func (idx *InvertedIndex[S, D]) buildScoringPlanLocked(queryTerms []Token, opts MatchOptions, work *workTracker) (scoringPlan, error) {
	var raw []queryClause
	if opts.expandsTerms() {
		var err error
		raw, err = idx.buildClausesTrackedLocked(queryTerms, opts, work)
		if err != nil {
			return scoringPlan{}, err
		}
	} else {
		raw = make([]queryClause, 0, len(queryTerms))
		for _, token := range queryTerms {
			if int(token.Class) >= numTokenClasses {
				continue
			}
			var ids []uint32
			if tid, ok := idx.classes[token.Class].dict.lookup(token.Term); ok {
				ids = []uint32{tid}
			}
			raw = append(raw, queryClause{class: token.Class, termIDs: ids})
		}
	}

	plan := scoringPlan{clauses: make([]scoringClause, 0, len(raw))}
	for _, clause := range raw {
		if err := work.check(); err != nil {
			return scoringPlan{}, err
		}
		resolved := scoringClause{class: clause.class, terms: make([]scoringTerm, 0, len(clause.termIDs))}
		if clause.class == ClassWord {
			plan.wordCount++
		}
		cp := &idx.classes[clause.class]
		for _, tid := range clause.termIDs {
			if pl := cp.postings[tid]; pl != nil {
				resolved.terms = append(resolved.terms, scoringTerm{class: clause.class, pl: pl, df: pl.cardinality()})
			}
		}
		plan.clauses = append(plan.clauses, resolved)
	}
	plan.threshold = coverageThreshold(opts, plan.wordCount)

	if idx.positions && idx.proximityWeight != 0 {
		cp := &idx.classes[ClassWord]
		for _, token := range queryTerms {
			if token.Class != ClassWord {
				continue
			}
			if tid, ok := cp.dict.lookup(token.Term); ok {
				if pl := cp.postings[tid]; pl != nil {
					plan.proximity = append(plan.proximity, pl)
				}
			}
		}
	}
	return plan, nil
}

func (idx *InvertedIndex[S, D]) scoreCandidateLocked(ord uint32, plan scoringPlan, scratch *executorScratch, work *workTracker, chargeProbes bool) (float64, bool, error) {
	entry, ok := idx.docs[ord]
	if !ok {
		return 0, false, nil
	}
	var score float64
	matched := false
	coverage := 0
	poisoned := false
	for _, clause := range plan.clauses {
		if err := work.check(); err != nil {
			return 0, false, err
		}
		clauseMatched := false
		cp := &idx.classes[clause.class]
		if cp.docCount == 0 {
			continue
		}
		avgLen := float64(cp.totalLen) / float64(cp.docCount)
		for _, term := range clause.terms {
			if chargeProbes {
				if err := work.visit(WorkPostingVisits, 1); err != nil {
					return 0, false, err
				}
			}
			if !term.pl.docs.Contains(ord) {
				continue
			}
			matched = true
			clauseMatched = true
			contribution := idx.scorer.Score(TermStats{
				TF:     term.pl.tf(ord),
				DF:     term.df,
				N:      cp.docCount,
				DocLen: entry.lengths[term.class],
				AvgLen: avgLen,
				Class:  term.class,
			})
			if math.IsNaN(contribution) || math.IsInf(contribution, 0) {
				poisoned = true
				continue
			}
			if !poisoned {
				score += contribution
				if math.IsNaN(score) || math.IsInf(score, 0) {
					poisoned = true
				}
			}
		}
		if clause.class == ClassWord && clauseMatched {
			coverage++
		}
	}
	if !matched || coverage < plan.threshold || poisoned {
		return 0, false, nil
	}
	if len(plan.proximity) >= 2 {
		present, err := scratch.decode(plan.proximity, ord, work)
		if err != nil {
			return 0, false, err
		}
		if len(present) >= 2 {
			if len(scratch.pointers) < len(present) {
				scratch.pointers = make([]int, len(present))
			}
			window, err := smallestWindowWithPointersTracked(present, scratch.pointers[:len(present)], work)
			if err != nil {
				return 0, false, err
			}
			if window >= 0 {
				span := window - (len(present) - 1)
				if span < 0 {
					span = 0
				}
				score += idx.proximityWeight * float64(len(present)-1) / float64(span+1)
			}
		}
	}
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return 0, false, nil
	}
	return score, true, nil
}

type postingCursor struct {
	it      roaring.IntPeekable
	current uint32
}

type postingCursorHeap []*postingCursor

func (h postingCursorHeap) Len() int           { return len(h) }
func (h postingCursorHeap) Less(i, j int) bool { return h[i].current < h[j].current }
func (h postingCursorHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *postingCursorHeap) Push(x any)        { *h = append(*h, x.(*postingCursor)) }
func (h *postingCursorHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

func (idx *InvertedIndex[S, D]) executeTopKLocked(plan scoringPlan, k int, accept func(S) bool, source CandidateSource[S], work *workTracker) ([]Result[S], error) {
	h := topKHeap[S]{entries: make([]Result[S], 0, k), better: idx.betterResult}
	var scratch executorScratch
	visit := func(ord uint32, chargeProbes bool) error {
		if err := work.candidateVisit(); err != nil {
			return err
		}
		entry, ok := idx.docs[ord]
		if !ok {
			return work.candidateSkip()
		}
		if accept != nil && !accept(entry.id) {
			return work.candidateSkip()
		}
		score, matched, err := idx.scoreCandidateLocked(ord, plan, &scratch, work, chargeProbes)
		if err != nil {
			return err
		}
		if !matched {
			return work.candidateSkip()
		}
		candidate := Result[S]{ID: entry.id, Score: score}
		if len(h.entries) == k && !idx.betterResult(candidate, h.entries[0]) {
			return nil
		}
		if len(h.entries) < k {
			heap.Push(&h, candidate)
		} else {
			h.entries[0] = candidate
			heap.Fix(&h, 0)
		}
		return nil
	}

	var executionErr error
	if source != nil {
		source(func(id S) bool {
			ord, ok := idx.ords.lookup(id)
			if !ok {
				if err := work.candidateVisit(); err != nil {
					executionErr = err
					return false
				}
				if err := work.candidateSkip(); err != nil {
					executionErr = err
					return false
				}
				return true
			}
			if err := visit(ord, true); err != nil {
				executionErr = err
				return false
			}
			return true
		})
	} else {
		cursors := make(postingCursorHeap, 0)
		for _, clause := range plan.clauses {
			for _, term := range clause.terms {
				it := term.pl.docs.Iterator()
				if it.HasNext() {
					if err := work.visit(WorkPostingVisits, 1); err != nil {
						return nil, err
					}
					cursors = append(cursors, &postingCursor{it: it, current: it.Next()})
				}
			}
		}
		heap.Init(&cursors)
		for len(cursors) > 0 {
			ord := cursors[0].current
			if err := visit(ord, false); err != nil {
				return nil, err
			}
			for len(cursors) > 0 && cursors[0].current == ord {
				cursor := heap.Pop(&cursors).(*postingCursor)
				if cursor.it.HasNext() {
					if err := work.visit(WorkPostingVisits, 1); err != nil {
						return nil, err
					}
					cursor.current = cursor.it.Next()
					heap.Push(&cursors, cursor)
				}
			}
		}
	}
	if executionErr != nil {
		return nil, executionErr
	}
	if len(h.entries) == 0 {
		return nil, nil
	}
	out := h.entries
	sort.Slice(out, func(i, j int) bool { return idx.betterResult(out[i], out[j]) })
	return out, nil
}

func (idx *InvertedIndex[S, D]) executePhraseTopKLocked(terms []string, k int, accept func(S) bool, source CandidateSource[S], work *workTracker) ([]Result[S], error) {
	cp := &idx.classes[ClassWord]
	if cp.docCount == 0 {
		return nil, nil
	}
	lists := make([]*postingList, len(terms))
	for i, term := range terms {
		if err := work.check(); err != nil {
			return nil, err
		}
		tid, ok := cp.dict.lookup(term)
		if !ok || cp.postings[tid] == nil {
			return nil, nil
		}
		lists[i] = cp.postings[tid]
	}
	distinct := make([]*postingList, 0, len(terms))
	seen := make(map[string]struct{}, len(terms))
	for i, term := range terms {
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		distinct = append(distinct, lists[i])
	}
	avgLen := float64(cp.totalLen) / float64(cp.docCount)
	h := topKHeap[S]{entries: make([]Result[S], 0, k), better: idx.betterResult}
	var scratch executorScratch
	visit := func(ord uint32, chargeProbes bool) error {
		if err := work.candidateVisit(); err != nil {
			return err
		}
		entry, ok := idx.docs[ord]
		if !ok {
			return work.candidateSkip()
		}
		if accept != nil && !accept(entry.id) {
			return work.candidateSkip()
		}
		for _, list := range lists {
			if chargeProbes {
				if err := work.visit(WorkPostingVisits, 1); err != nil {
					return err
				}
			}
			if !list.docs.Contains(ord) {
				return work.candidateSkip()
			}
		}
		positions, err := scratch.decode(lists, ord, work)
		if err != nil {
			return err
		}
		adjacent := phraseAdjacentDecoded(positions)
		if !adjacent {
			return work.candidateSkip()
		}
		var score float64
		for _, list := range distinct {
			contribution := idx.scorer.Score(TermStats{
				TF: list.tf(ord), DF: list.cardinality(), N: cp.docCount,
				DocLen: entry.lengths[ClassWord], AvgLen: avgLen, Class: ClassWord,
			})
			if math.IsNaN(contribution) || math.IsInf(contribution, 0) {
				return work.candidateSkip()
			}
			score += contribution
		}
		if math.IsNaN(score) || math.IsInf(score, 0) {
			return work.candidateSkip()
		}
		candidate := Result[S]{ID: entry.id, Score: score}
		if len(h.entries) == k && !idx.betterResult(candidate, h.entries[0]) {
			return nil
		}
		if len(h.entries) < k {
			heap.Push(&h, candidate)
		} else {
			h.entries[0] = candidate
			heap.Fix(&h, 0)
		}
		return nil
	}

	if source != nil {
		var executionErr error
		source(func(id S) bool {
			ord, ok := idx.ords.lookup(id)
			if !ok {
				if executionErr = work.candidateVisit(); executionErr == nil {
					executionErr = work.candidateSkip()
				}
				return executionErr == nil
			}
			executionErr = visit(ord, true)
			return executionErr == nil
		})
		if executionErr != nil {
			return nil, executionErr
		}
	} else {
		rarest := lists[0]
		for _, list := range lists[1:] {
			if list.cardinality() < rarest.cardinality() {
				rarest = list
			}
		}
		for it := rarest.docs.Iterator(); it.HasNext(); {
			if err := work.visit(WorkPostingVisits, 1); err != nil {
				return nil, err
			}
			if err := visit(it.Next(), false); err != nil {
				return nil, err
			}
		}
	}
	if len(h.entries) == 0 {
		return nil, nil
	}
	out := h.entries
	sort.Slice(out, func(i, j int) bool { return idx.betterResult(out[i], out[j]) })
	return out, nil
}

func phraseAdjacentDecoded(positions [][]uint32) bool {
	if len(positions) < 2 {
		return true
	}
	for _, start := range positions[0] {
		matched := true
		for i := 1; i < len(positions); i++ {
			if !containsSortedU32(positions[i], start+uint32(i)) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
