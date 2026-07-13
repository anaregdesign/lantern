package search

import (
	"errors"
	"fmt"
	"time"
)

// SearchAnalysisLimits bounds the memory amplification of indexing. A zero
// field disables that individual limit. CompactionRatio and
// CompactionMinRetired control reclamation of dictionary/ordinal high-water
// storage after delete or TTL churn.
type SearchAnalysisLimits struct {
	MaxDocumentBytes     int
	MaxDocumentTokens    int
	MaxDocumentTerms     int
	MaxLiveTerms         int
	MaxLivePostings      int64
	MaxPositionEntries   int64
	CompactionRatio      float64
	CompactionMinRetired int
}

// AnalysisStats describes the bounded representation produced for one
// document. ProjectedBytes counts the UTF-8 bytes handed to the analyzer;
// Postings is the number of distinct (class, term) pairs.
type AnalysisStats struct {
	ProjectedBytes  int
	Tokens          int
	UniqueTerms     int
	Postings        int
	PositionEntries int
}

// AnalysisLimitKind is a stable machine-readable indexing limit name.
type AnalysisLimitKind string

const (
	LimitDocumentBytes   AnalysisLimitKind = "document_bytes"
	LimitDocumentTokens  AnalysisLimitKind = "document_tokens"
	LimitDocumentTerms   AnalysisLimitKind = "document_terms"
	LimitLiveTerms       AnalysisLimitKind = "live_terms"
	LimitLivePostings    AnalysisLimitKind = "live_postings"
	LimitPositionEntries AnalysisLimitKind = "position_entries"
)

// AnalysisLimitError reports a deterministic per-document or aggregate index
// budget failure. Callers should branch on Kind, not Error's prose.
type AnalysisLimitError struct {
	Kind  AnalysisLimitKind
	Limit int64
	Got   int64
}

func (e *AnalysisLimitError) Error() string {
	return fmt.Sprintf("search index %s limit exceeded: got %d, limit %d", e.Kind, e.Got, e.Limit)
}

// IndexHealth is the search index consistency state.
type IndexHealth string

const (
	IndexHealthy    IndexHealth = "healthy"
	IndexIncomplete IndexHealth = "incomplete"
)

// ErrIndexIncomplete is returned instead of results when a convergent source
// mutation could not be represented within the local index budgets.
var ErrIndexIncomplete = errors.New("search index is incomplete; bounded rebuild required")

// IndexMemoryStats is an observable snapshot of live and retained index state.
type IndexMemoryStats struct {
	Documents              int
	LiveTerms              int
	RetainedTermSlots      int
	RetainedOrdinals       int
	Postings               int64
	PositionEntries        int64
	EstimatedLiveBytes     int64
	EstimatedRetainedBytes int64
	RebuildCount           uint64
	LastRebuildDuration    time.Duration
	WriteLockAcquisitions  uint64
	Health                 IndexHealth
}

func limitError(kind AnalysisLimitKind, got, limit int64) error {
	if limit > 0 && got > limit {
		return &AnalysisLimitError{Kind: kind, Got: got, Limit: limit}
	}
	return nil
}
