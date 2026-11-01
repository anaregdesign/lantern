package config

import (
	"fmt"
	"math"
	"time"
)

// Validate checks operational value relationships only. It does not certify
// runtime ownership, inspect material, report parser findings, or start resources.
func (c Config) Validate() error {
	if _, ok := ParseMatchMode(c.Search.DefaultMode); !ok {
		return fmt.Errorf("LANTERN_SEARCH_DEFAULT_MODE=%q: must be one of any|all|min-should", c.Search.DefaultMode)
	}
	if c.Cache.MaxVertexCausalEntries < 0 {
		return fmt.Errorf("LANTERN_MAX_VERTEX_CAUSAL_ENTRIES must be zero (unlimited) or positive")
	}
	if c.Cache.MaxEdgeCausalEntries < 0 {
		return fmt.Errorf("LANTERN_MAX_EDGE_CAUSAL_ENTRIES must be zero (unlimited) or positive")
	}
	if c.Cache.GCEdgeBudget < 0 {
		return fmt.Errorf("LANTERN_GC_EDGE_BUDGET must be zero (full sweep) or positive")
	}
	return validateSearchConfig(c.Search)
}

func validateSearchConfig(c SearchConfig) error {
	if c.DefaultLimit == 0 || c.MaxLimit == 0 || c.DefaultLimit > c.MaxLimit {
		return fmt.Errorf("search limits require 0 < default_limit <= max_limit")
	}
	if c.DefaultMinShould == 0 {
		return fmt.Errorf("LANTERN_SEARCH_DEFAULT_MIN_SHOULD must be positive")
	}
	if c.MaxSessionHits <= 0 || uint64(c.MaxSessionHits) <= uint64(c.MaxLimit) {
		return fmt.Errorf("LANTERN_SEARCH_MAX_SESSION_HITS must exceed LANTERN_SEARCH_MAX_LIMIT")
	}
	values := []struct {
		name  string
		value int64
	}{
		{"LANTERN_SEARCH_TIMEOUT_MS", c.Timeout.Milliseconds()},
		{"LANTERN_SEARCH_MAX_QUERY_BYTES", int64(c.MaxQueryBytes)},
		{"LANTERN_SEARCH_MAX_QUERY_TERMS", int64(c.MaxQueryTerms)},
		{"LANTERN_SEARCH_MAX_DICTIONARY_VISITS", int64(c.MaxDictionaryVisits)},
		{"LANTERN_SEARCH_MAX_POSTING_VISITS", int64(c.MaxPostingVisits)},
		{"LANTERN_SEARCH_MAX_POSITION_VISITS", int64(c.MaxPositionVisits)},
		{"LANTERN_SEARCH_MAX_EXPIRATION_VISITS", int64(c.MaxExpirationVisits)},
		{"LANTERN_SEARCH_MAX_IN_FLIGHT", int64(c.MaxInFlight)},
		{"LANTERN_SEARCH_CURSOR_TTL_SECONDS", int64(c.CursorTTL / time.Second)},
		{"LANTERN_SEARCH_MAX_SESSIONS", int64(c.MaxSessions)},
		{"LANTERN_SEARCH_MAX_SESSION_HITS", int64(c.MaxSessionHits)},
		{"LANTERN_SEARCH_MAX_SESSION_BYTES", c.MaxSessionBytes},
		{"LANTERN_SEARCH_MAX_DOCUMENT_BYTES", int64(c.AnalysisLimits.MaxDocumentBytes)},
		{"LANTERN_SEARCH_MAX_DOCUMENT_TOKENS", int64(c.AnalysisLimits.MaxDocumentTokens)},
		{"LANTERN_SEARCH_MAX_DOCUMENT_TERMS", int64(c.AnalysisLimits.MaxDocumentTerms)},
		{"LANTERN_SEARCH_MAX_LIVE_TERMS", int64(c.AnalysisLimits.MaxLiveTerms)},
		{"LANTERN_SEARCH_MAX_LIVE_POSTINGS", c.AnalysisLimits.MaxLivePostings},
		{"LANTERN_SEARCH_MAX_POSITION_ENTRIES", c.AnalysisLimits.MaxPositionEntries},
		{"LANTERN_SEARCH_COMPACTION_MIN_RETIRED", int64(c.AnalysisLimits.CompactionMinRetired)},
	}
	if c.AnalysisLimits.CompactionRatio <= 1 || math.IsNaN(c.AnalysisLimits.CompactionRatio) || math.IsInf(c.AnalysisLimits.CompactionRatio, 0) {
		return fmt.Errorf("LANTERN_SEARCH_COMPACTION_RATIO must be greater than 1")
	}
	for _, item := range values {
		if item.value <= 0 {
			return fmt.Errorf("%s must be positive", item.name)
		}
	}
	if c.MaxQueryTerms > c.MaxQueryBytes {
		return fmt.Errorf("LANTERN_SEARCH_MAX_QUERY_TERMS must not exceed LANTERN_SEARCH_MAX_QUERY_BYTES")
	}
	return nil
}
