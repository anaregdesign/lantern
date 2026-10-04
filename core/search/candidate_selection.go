package search

import "github.com/RoaringBitmap/roaring/v2"

// scopedCandidatesLocked combines existing posting bitmaps and intersects them
// with admitted matching IDs before payload scoring and top-k. Work scales
// with matching postings; it never enumerates every admitted non-match or
// constructs a permission-specific corpus/statistics cache.
func (idx *InvertedIndex[S, D]) scopedCandidatesLocked(lists []*postingList, phrase bool, source CandidateSource[S], work *workTracker) (CandidateSource[S], error) {
	contains := candidateFilterFromContext[S](work.ctx)
	if contains == nil {
		return source, nil
	}
	matching := roaring.New()
	for i, list := range lists {
		if err := work.visit(WorkPostingVisits, int64(list.docs.GetCardinality())); err != nil {
			return nil, err
		}
		if phrase && i > 0 {
			matching.And(list.docs)
		} else {
			matching.Or(list.docs)
		}
		if phrase && matching.IsEmpty() {
			break
		}
	}
	admitted := roaring.New()
	for iterator := matching.Iterator(); iterator.HasNext(); {
		if err := work.check(); err != nil {
			return nil, err
		}
		ord := iterator.Next()
		if entry, exists := idx.docs[ord]; exists && contains(entry.id) {
			admitted.Add(ord)
		}
	}
	matching.And(admitted)
	if source != nil {
		requested := roaring.New()
		var executionErr error
		source(func(id S) bool {
			if executionErr = work.check(); executionErr != nil {
				return false
			}
			ord, known := idx.ords.lookup(id)
			if known && matching.Contains(ord) {
				requested.Add(ord)
			}
			return true
		})
		if executionErr != nil {
			return nil, executionErr
		}
		matching.And(requested)
	}
	return func(yield func(S) bool) {
		for iterator := matching.Iterator(); iterator.HasNext(); {
			if !yield(idx.docs[iterator.Next()].id) {
				return
			}
		}
	}, nil
}
