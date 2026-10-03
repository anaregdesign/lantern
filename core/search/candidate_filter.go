package search

import "context"

type candidateFilterKey[S comparable] struct{}

// WithCandidateFilter restricts matching IDs before top-k selection. The
// immutable, storage-level predicate runs synchronously under the index read
// lock. It must not perform writes, I/O, or acquire a lock above the index.
// Corpus statistics remain those of this index, independently of this filter.
func WithCandidateFilter[S comparable](ctx context.Context, contains func(S) bool) context.Context {
	if contains == nil {
		panic("nil candidate filter")
	}
	return context.WithValue(ctx, candidateFilterKey[S]{}, contains)
}

func candidateFilterFromContext[S comparable](ctx context.Context) func(S) bool {
	if ctx == nil {
		return nil
	}
	filter, _ := ctx.Value(candidateFilterKey[S]{}).(func(S) bool)
	return filter
}
