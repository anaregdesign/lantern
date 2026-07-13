package search

import (
	"container/heap"
	"math"
	"slices"
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
}

type scoringClause struct {
	class TokenClass
	terms []scoringTerm
}

type scoringPlan struct {
	clauses   []scoringClause
	wordCount int
	threshold int
	proximity [numDocumentFields][]*postingList
}

type executorScratch struct {
	positions [][]uint64
	present   [][]uint64
	pointers  []int
	instances []uint32
	instance  [][]uint64
}

func (s *executorScratch) decode(lists []*postingList, ord uint32, field FieldID, work *workTracker) ([][]uint64, error) {
	if len(s.positions) < len(lists) {
		s.positions = append(s.positions, make([][]uint64, len(lists)-len(s.positions))...)
	}
	s.present = s.present[:0]
	for i, pl := range lists {
		s.positions[i] = pl.positionsInto(ord, field, s.positions[i])
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
				resolved.terms = append(resolved.terms, scoringTerm{class: clause.class, pl: pl})
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
					for field := FieldID(0); field < numDocumentFields; field++ {
						plan.proximity[field] = append(plan.proximity[field], pl)
					}
				}
			}
		}
	}
	return plan, nil
}

func (idx *InvertedIndex[S, D]) scoreCandidateLocked(ord uint32, plan scoringPlan, scratch *executorScratch, work *workTracker, chargeProbes bool) (float64, bool, error) {
	_, ok := idx.docs[ord]
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
		if idx.classes[clause.class].docCount == 0 {
			continue
		}
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
			contribution := idx.termScoreLocked(ord, term.class, term.pl)
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
	var bestProximity float64
	for field := FieldID(0); field < numDocumentFields; field++ {
		if len(plan.proximity[field]) < 2 {
			continue
		}
		present, err := scratch.decode(plan.proximity[field], ord, field, work)
		if err != nil {
			return 0, false, err
		}
		bonus, err := scratch.proximityBonus(present, work)
		if err != nil {
			return 0, false, err
		}
		if bonus > bestProximity {
			bestProximity = bonus
		}
	}
	score += idx.proximityWeight * bestProximity
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return 0, false, nil
	}
	return score, true, nil
}

func (s *executorScratch) proximityBonus(lists [][]uint64, work *workTracker) (float64, error) {
	if len(lists) < 2 {
		return 0, nil
	}
	s.instances = s.instances[:0]
	for _, positions := range lists {
		for _, position := range positions {
			instance := uint32(position >> 32)
			if len(s.instances) == 0 || s.instances[len(s.instances)-1] != instance {
				s.instances = append(s.instances, instance)
			}
		}
	}
	slices.Sort(s.instances)
	s.instances = slices.Compact(s.instances)
	var best float64
	for _, instance := range s.instances {
		s.instance = s.instance[:0]
		lo := uint64(instance) << 32
		hi := lo | uint64(1<<32-1)
		for _, positions := range lists {
			start := sort.Search(len(positions), func(i int) bool { return positions[i] >= lo })
			end := sort.Search(len(positions), func(i int) bool { return positions[i] > hi })
			if start < end {
				s.instance = append(s.instance, positions[start:end])
			}
		}
		if len(s.instance) < 2 {
			continue
		}
		if len(s.pointers) < len(s.instance) {
			s.pointers = make([]int, len(s.instance))
		}
		window, err := smallestWindowWithPointersTracked(s.instance, s.pointers[:len(s.instance)], work)
		if err != nil {
			return 0, err
		}
		span := window - (len(s.instance) - 1)
		if span < 0 {
			span = 0
		}
		bonus := float64(len(s.instance)-1) / float64(span+1)
		if bonus > best {
			best = bonus
		}
	}
	return best, nil
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
		matchedPhrase := false
		for field := FieldID(0); field < numDocumentFields && !matchedPhrase; field++ {
			present := true
			for _, list := range lists {
				if chargeProbes {
					if err := work.visit(WorkPostingVisits, 1); err != nil {
						return err
					}
				}
				if !list.containsField(ord, field) {
					present = false
					break
				}
			}
			if !present {
				continue
			}
			positions, err := scratch.decode(lists, ord, field, work)
			if err != nil {
				return err
			}
			matchedPhrase = phraseAdjacentDecoded(positions)
		}
		if !matchedPhrase {
			return work.candidateSkip()
		}
		var score float64
		for _, list := range distinct {
			contribution := idx.termScoreLocked(ord, ClassWord, list)
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

func phraseAdjacentDecoded(positions [][]uint64) bool {
	if len(positions) < 2 {
		return true
	}
	for _, start := range positions[0] {
		matched := true
		for i := 1; i < len(positions); i++ {
			if !containsSortedU64(positions[i], start+uint64(i)) {
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
