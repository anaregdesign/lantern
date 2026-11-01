package config

import (
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

func TestSearchLoading(t *testing.T) {
	cleanEnvironment(t)
	got := Load(":9090").Search
	if !got.Enabled || !got.Positions || got.DefaultLimit != 100 || got.MaxLimit != 1000 || got.DefaultMode != "any" || got.DefaultMinShould != 1 || got.Timeout != 5*time.Second || got.MaxQueryBytes != 16*1024 || got.MaxSessions != 128 || got.MaxSessionBytes != 64<<20 || got.AnalysisLimits.CompactionRatio != 2 {
		t.Fatalf("search defaults = %+v", got)
	}
	for key, value := range map[string]string{
		"LANTERN_SEARCH_ENABLED": "false", "LANTERN_SEARCH_POSITIONS": "false",
		"LANTERN_SEARCH_DEFAULT_MODE": "min_should", "LANTERN_SEARCH_DEFAULT_MIN_SHOULD": "3",
		"LANTERN_SEARCH_DEFAULT_LIMIT": "4", "LANTERN_SEARCH_MAX_LIMIT": "5",
		"LANTERN_SEARCH_TIMEOUT_MS": "6", "LANTERN_SEARCH_MAX_QUERY_BYTES": "700",
		"LANTERN_SEARCH_MAX_QUERY_TERMS": "8", "LANTERN_SEARCH_MAX_DICTIONARY_VISITS": "9",
		"LANTERN_SEARCH_MAX_POSTING_VISITS": "10", "LANTERN_SEARCH_MAX_POSITION_VISITS": "11",
		"LANTERN_SEARCH_MAX_EXPIRATION_VISITS": "12", "LANTERN_SEARCH_MAX_IN_FLIGHT": "13",
		"LANTERN_SEARCH_CURSOR_TTL_SECONDS": "14", "LANTERN_SEARCH_MAX_SESSIONS": "15",
		"LANTERN_SEARCH_MAX_SESSION_HITS": "16", "LANTERN_SEARCH_MAX_SESSION_BYTES": "17",
		"LANTERN_SEARCH_MAX_DOCUMENT_BYTES": "18", "LANTERN_SEARCH_MAX_DOCUMENT_TOKENS": "19",
		"LANTERN_SEARCH_MAX_DOCUMENT_TERMS": "20", "LANTERN_SEARCH_MAX_LIVE_TERMS": "21",
		"LANTERN_SEARCH_MAX_LIVE_POSTINGS": "22", "LANTERN_SEARCH_MAX_POSITION_ENTRIES": "23",
		"LANTERN_SEARCH_COMPACTION_RATIO": "3.5", "LANTERN_SEARCH_COMPACTION_MIN_RETIRED": "24",
	} {
		t.Setenv(key, value)
	}
	got = Load(":9090").Search
	if got.Enabled || got.Positions || got.DefaultMode != "min_should" || got.DefaultMinShould != 3 || got.DefaultLimit != 4 || got.MaxLimit != 5 || got.Timeout != 6*time.Millisecond || got.MaxQueryBytes != 700 || got.MaxQueryTerms != 8 || got.MaxDictionaryVisits != 9 || got.MaxPostingVisits != 10 || got.MaxPositionVisits != 11 || got.MaxExpirationVisits != 12 || got.MaxInFlight != 13 || got.CursorTTL != 14*time.Second || got.MaxSessions != 15 || got.MaxSessionHits != 16 || got.MaxSessionBytes != 17 {
		t.Fatalf("search overrides = %+v", got)
	}
	analysis := got.AnalysisLimits
	if analysis.MaxDocumentBytes != 18 || analysis.MaxDocumentTokens != 19 || analysis.MaxDocumentTerms != 20 || analysis.MaxLiveTerms != 21 || analysis.MaxLivePostings != 22 || analysis.MaxPositionEntries != 23 || analysis.CompactionRatio != 3.5 || analysis.CompactionMinRetired != 24 {
		t.Fatalf("search index overrides = %+v", analysis)
	}
	envconfig.ResetForTesting()
	t.Setenv("LANTERN_SEARCH_MAX_LIMIT", "4294967296")
	if got := Load(":9090").Search.MaxLimit; got != 1000 || len(envconfig.Findings()) != 1 {
		t.Fatalf("out-of-range uint32 search limit = %d; findings = %+v", got, envconfig.Findings())
	}
}
