package config

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestValidateOperationalValues(t *testing.T) {
	cleanEnvironment(t)
	base := Load(":9090")
	if err := base.Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	for _, mode := range []string{"", "any", "all", "ALL", "min-should", "minshould", "min_should"} {
		candidate := base
		candidate.Search.DefaultMode = mode
		if err := candidate.Validate(); err != nil {
			t.Fatalf("accepted match mode %q: %v", mode, err)
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
		key    string
	}{
		{"match mode", func(c *Config) { c.Search.DefaultMode = "min_shold" }, "LANTERN_SEARCH_DEFAULT_MODE"},
		{"vertex causal budget", func(c *Config) { c.Cache.MaxVertexCausalEntries = -1 }, "LANTERN_MAX_VERTEX_CAUSAL_ENTRIES"},
		{"edge causal budget", func(c *Config) { c.Cache.MaxEdgeCausalEntries = -1 }, "LANTERN_MAX_EDGE_CAUSAL_ENTRIES"},
		{"GC edge budget", func(c *Config) { c.Cache.GCEdgeBudget = -1 }, "LANTERN_GC_EDGE_BUDGET"},
		{"default search limit", func(c *Config) { c.Search.DefaultLimit = 0 }, "search limits"},
		{"maximum search limit", func(c *Config) { c.Search.MaxLimit = 0 }, "search limits"},
		{"inverted search limits", func(c *Config) { c.Search.DefaultLimit = c.Search.MaxLimit + 1 }, "search limits"},
		{"minimum should", func(c *Config) { c.Search.DefaultMinShould = 0 }, "LANTERN_SEARCH_DEFAULT_MIN_SHOULD"},
		{"cursor capacity", func(c *Config) { c.Search.MaxSessionHits = int(c.Search.MaxLimit) }, "LANTERN_SEARCH_MAX_SESSION_HITS"},
		{"timeout", func(c *Config) { c.Search.Timeout = 0 }, "LANTERN_SEARCH_TIMEOUT_MS"},
		{"query bytes", func(c *Config) { c.Search.MaxQueryBytes = 0 }, "LANTERN_SEARCH_MAX_QUERY_BYTES"},
		{"query terms", func(c *Config) { c.Search.MaxQueryTerms = 0 }, "LANTERN_SEARCH_MAX_QUERY_TERMS"},
		{"dictionary visits", func(c *Config) { c.Search.MaxDictionaryVisits = 0 }, "LANTERN_SEARCH_MAX_DICTIONARY_VISITS"},
		{"posting visits", func(c *Config) { c.Search.MaxPostingVisits = 0 }, "LANTERN_SEARCH_MAX_POSTING_VISITS"},
		{"position visits", func(c *Config) { c.Search.MaxPositionVisits = 0 }, "LANTERN_SEARCH_MAX_POSITION_VISITS"},
		{"expiration visits", func(c *Config) { c.Search.MaxExpirationVisits = 0 }, "LANTERN_SEARCH_MAX_EXPIRATION_VISITS"},
		{"in flight", func(c *Config) { c.Search.MaxInFlight = 0 }, "LANTERN_SEARCH_MAX_IN_FLIGHT"},
		{"cursor TTL", func(c *Config) { c.Search.CursorTTL = time.Millisecond }, "LANTERN_SEARCH_CURSOR_TTL_SECONDS"},
		{"sessions", func(c *Config) { c.Search.MaxSessions = 0 }, "LANTERN_SEARCH_MAX_SESSIONS"},
		{"session bytes", func(c *Config) { c.Search.MaxSessionBytes = 0 }, "LANTERN_SEARCH_MAX_SESSION_BYTES"},
		{"document bytes", func(c *Config) { c.Search.AnalysisLimits.MaxDocumentBytes = 0 }, "LANTERN_SEARCH_MAX_DOCUMENT_BYTES"},
		{"document tokens", func(c *Config) { c.Search.AnalysisLimits.MaxDocumentTokens = 0 }, "LANTERN_SEARCH_MAX_DOCUMENT_TOKENS"},
		{"document terms", func(c *Config) { c.Search.AnalysisLimits.MaxDocumentTerms = 0 }, "LANTERN_SEARCH_MAX_DOCUMENT_TERMS"},
		{"live terms", func(c *Config) { c.Search.AnalysisLimits.MaxLiveTerms = 0 }, "LANTERN_SEARCH_MAX_LIVE_TERMS"},
		{"live postings", func(c *Config) { c.Search.AnalysisLimits.MaxLivePostings = 0 }, "LANTERN_SEARCH_MAX_LIVE_POSTINGS"},
		{"position entries", func(c *Config) { c.Search.AnalysisLimits.MaxPositionEntries = 0 }, "LANTERN_SEARCH_MAX_POSITION_ENTRIES"},
		{"compaction retired", func(c *Config) { c.Search.AnalysisLimits.CompactionMinRetired = 0 }, "LANTERN_SEARCH_COMPACTION_MIN_RETIRED"},
		{"compaction ratio", func(c *Config) { c.Search.AnalysisLimits.CompactionRatio = 1 }, "LANTERN_SEARCH_COMPACTION_RATIO"},
		{"NaN compaction ratio", func(c *Config) { c.Search.AnalysisLimits.CompactionRatio = math.NaN() }, "LANTERN_SEARCH_COMPACTION_RATIO"},
		{"infinite compaction ratio", func(c *Config) { c.Search.AnalysisLimits.CompactionRatio = math.Inf(1) }, "LANTERN_SEARCH_COMPACTION_RATIO"},
		{"terms exceed bytes", func(c *Config) { c.Search.MaxQueryTerms = c.Search.MaxQueryBytes + 1 }, "LANTERN_SEARCH_MAX_QUERY_TERMS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := base
			tc.mutate(&candidate)
			if err := candidate.Validate(); err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("Validate error = %v, want %s", err, tc.key)
			}
		})
	}
}
