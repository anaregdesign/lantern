package graphcache

import "errors"

// KeyPairRange is a directed rectangle in the projected keyspace.
type KeyPairRange struct{ Tail, Head KeyRange }

// EdgeRangeFilter is a generic two-dimensional point set. A point belongs
// when both endpoints belong to Endpoints OR it belongs to an Included
// rectangle, and neither endpoint/rectangle is excluded. Multiple filters
// intersect. Empty inclusion is empty, never an unrestricted query.
type EdgeRangeFilter struct {
	Endpoints         []KeyRange
	Included          []KeyPairRange
	ExcludedEndpoints []KeyRange
	Excluded          []KeyPairRange
}

func pointInRange(r KeyRange, key string) bool {
	return key >= r.Lower && (r.Upper == "" || key < r.Upper)
}
func pointInPairs(pairs []KeyPairRange, tail, head string) bool {
	for _, pair := range pairs {
		if pointInRange(pair.Tail, tail) && pointInRange(pair.Head, head) {
			return true
		}
	}
	return false
}
func (f *EdgeRangeFilter) contains(tail, head string) bool {
	if containsRange(f.ExcludedEndpoints, tail) || containsRange(f.ExcludedEndpoints, head) || pointInPairs(f.Excluded, tail, head) {
		return false
	}
	return containsRange(f.Endpoints, tail) && containsRange(f.Endpoints, head) || pointInPairs(f.Included, tail, head)
}

// NewQueryViewWithEdgeFilters copies bounded detached range sets. Core owns
// only index constraints; it never receives a policy evaluator or callback.
func NewQueryViewWithEdgeFilters(vertices, edges []KeyRange, filters []EdgeRangeFilter) (*QueryView, error) {
	view, err := NewQueryView(vertices, edges)
	if err != nil {
		return nil, err
	}
	if len(filters) > 16 {
		return nil, errors.New("too many edge range filters")
	}
	count, bytes := 0, 0
	valid := func(r KeyRange) bool {
		count++
		bytes += len(r.Lower) + len(r.Upper)
		return count <= 16384 && bytes <= 8<<20 && (r.Upper == "" || r.Lower < r.Upper)
	}
	copyRanges := func(ranges []KeyRange) ([]KeyRange, bool) {
		for i, r := range ranges {
			if !valid(r) || i > 0 && (ranges[i-1].Upper == "" || r.Lower < ranges[i-1].Upper) {
				return nil, false
			}
		}
		return append([]KeyRange(nil), ranges...), true
	}
	copyPairs := func(pairs []KeyPairRange) ([]KeyPairRange, bool) {
		for _, pair := range pairs {
			if !valid(pair.Tail) || !valid(pair.Head) {
				return nil, false
			}
		}
		return append([]KeyPairRange(nil), pairs...), true
	}
	view.filters = make([]EdgeRangeFilter, len(filters))
	for i, input := range filters {
		var ok bool
		target := &view.filters[i]
		if target.Endpoints, ok = copyRanges(input.Endpoints); !ok {
			return nil, errors.New("invalid endpoint ranges")
		}
		if target.ExcludedEndpoints, ok = copyRanges(input.ExcludedEndpoints); !ok {
			return nil, errors.New("invalid excluded endpoint ranges")
		}
		if target.Included, ok = copyPairs(input.Included); !ok {
			return nil, errors.New("invalid pair ranges")
		}
		if target.Excluded, ok = copyPairs(input.Excluded); !ok {
			return nil, errors.New("invalid excluded pair ranges")
		}
	}
	return view, nil
}
