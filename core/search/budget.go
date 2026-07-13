package search

import (
	"context"
	"errors"
	"fmt"
)

// WorkKind identifies one deterministic search-work counter.
type WorkKind string

const (
	WorkQueryTerms       WorkKind = "query_terms"
	WorkDictionaryVisits WorkKind = "dictionary_visits"
	WorkPostingVisits    WorkKind = "posting_visits"
	WorkPositionVisits   WorkKind = "position_visits"
	WorkExpirationVisits WorkKind = "expiration_visits"
)

// ErrBudgetExceeded classifies deterministic query-work exhaustion.
var ErrBudgetExceeded = errors.New("search work budget exceeded")

// Budget caps work performed by one query. Zero means unlimited and is used by
// compatibility helpers; the server validates and supplies positive limits.
type Budget struct {
	MaxQueryTerms       int64
	MaxDictionaryVisits int64
	MaxPostingVisits    int64
	MaxPositionVisits   int64
	MaxExpirationVisits int64
}

// Stats is the exact work charged to one search attempt.
type Stats struct {
	QueryTerms       int64
	DictionaryVisits int64
	PostingVisits    int64
	PositionVisits   int64
	ExpirationVisits int64
}

// BudgetExceededError records which stable counter exhausted its limit.
type BudgetExceededError struct {
	Kind  WorkKind
	Limit int64
}

func (e *BudgetExceededError) Error() string {
	return fmt.Sprintf("%s: %s limit %d", ErrBudgetExceeded, e.Kind, e.Limit)
}

func (e *BudgetExceededError) Unwrap() error { return ErrBudgetExceeded }

type workTracker struct {
	ctx    context.Context
	budget Budget
	stats  Stats
}

func newWorkTracker(ctx context.Context, budget Budget) *workTracker {
	if ctx == nil {
		ctx = context.Background()
	}
	return &workTracker{ctx: ctx, budget: budget}
}

func (w *workTracker) check() error { return w.ctx.Err() }

func (w *workTracker) visit(kind WorkKind, n int64) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	var value *int64
	var limit int64
	switch kind {
	case WorkQueryTerms:
		value, limit = &w.stats.QueryTerms, w.budget.MaxQueryTerms
	case WorkDictionaryVisits:
		value, limit = &w.stats.DictionaryVisits, w.budget.MaxDictionaryVisits
	case WorkPostingVisits:
		value, limit = &w.stats.PostingVisits, w.budget.MaxPostingVisits
	case WorkPositionVisits:
		value, limit = &w.stats.PositionVisits, w.budget.MaxPositionVisits
	case WorkExpirationVisits:
		value, limit = &w.stats.ExpirationVisits, w.budget.MaxExpirationVisits
	default:
		panic("search: unknown work kind")
	}
	*value += n
	if limit > 0 && *value > limit {
		return &BudgetExceededError{Kind: kind, Limit: limit}
	}
	return nil
}
