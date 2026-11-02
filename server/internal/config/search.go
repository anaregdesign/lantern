package config

import (
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/search"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

// SearchConfig governs the full-text SearchVertices RPC (#624). Enabled is
// opt-out: the index is built by default because content search is the
// headline reason most callers reach for Lantern's memory surface, and the
// per-put cost is only paid while the feature is on. Operators who want the
// leaner put hot path set LANTERN_SEARCH_ENABLED=false, which both skips
// EnableSearchIndex at cache construction AND makes the RPC return
// FAILED_PRECONDITION. DefaultLimit / MaxLimit cap the per-call ranked-hit
// count the same way ScanConfig caps the prefix RPCs.
//
//   - LANTERN_SEARCH_ENABLED           (default true)
//   - LANTERN_SEARCH_POSITIONS         (default true)
//   - LANTERN_SEARCH_DEFAULT_LIMIT     (default 100)
//   - LANTERN_SEARCH_MAX_LIMIT         (default 1000)
//   - LANTERN_SEARCH_DEFAULT_MODE      (default "any": any|all|min-should)
//   - LANTERN_SEARCH_DEFAULT_MIN_SHOULD (default 1)
//   - LANTERN_SEARCH_TIMEOUT_MS         (default 5000)
//   - LANTERN_SEARCH_MAX_QUERY_BYTES    (default 16384)
//   - LANTERN_SEARCH_MAX_QUERY_TERMS    (default 1024)
//   - LANTERN_SEARCH_MAX_DICTIONARY_VISITS (default 1000000)
//   - LANTERN_SEARCH_MAX_POSTING_VISITS (default 10000000)
//   - LANTERN_SEARCH_MAX_POSITION_VISITS (default 10000000)
//   - LANTERN_SEARCH_MAX_EXPIRATION_VISITS (default 100000)
//   - LANTERN_SEARCH_MAX_IN_FLIGHT      (default 32)
//   - LANTERN_SEARCH_CURSOR_TTL_SECONDS (default 60)
//   - LANTERN_SEARCH_MAX_SESSIONS       (default 128)
//   - LANTERN_SEARCH_MAX_SESSION_HITS   (default 10000)
//   - LANTERN_SEARCH_MAX_SESSION_BYTES  (default 67108864)
//   - LANTERN_SEARCH_MAX_DOCUMENT_BYTES (default 1048576)
//   - LANTERN_SEARCH_MAX_DOCUMENT_TOKENS (default 250000)
//   - LANTERN_SEARCH_MAX_DOCUMENT_TERMS (default 100000)
//   - LANTERN_SEARCH_MAX_LIVE_TERMS     (default 5000000)
//   - LANTERN_SEARCH_MAX_LIVE_POSTINGS  (default 50000000)
//   - LANTERN_SEARCH_MAX_POSITION_ENTRIES (default 50000000)
//   - LANTERN_SEARCH_COMPACTION_RATIO   (default 2)
//   - LANTERN_SEARCH_COMPACTION_MIN_RETIRED (default 10000)
type SearchConfig struct {
	Enabled      bool
	DefaultLimit uint32
	MaxLimit     uint32
	// Positions is opt-out (LANTERN_SEARCH_POSITIONS, default true): when true
	// the search index records positional postings so phrase queries verify
	// adjacency and the proximity boost ranks tight matches higher. Setting it
	// false drops the per-(word term, vertex) position store. Phrase requests
	// then fail closed with SEARCH_POSITIONS_DISABLED because adjacency cannot
	// be verified; the proximity boost is inert. This trades the phrase feature
	// for a smaller index on large corpora (#908/#1054). Ignored when Enabled is
	// false.
	Positions bool
	// DefaultMode is the match mode applied when a SearchVertices request omits
	// options or leaves match_mode unspecified: "any" (OR), "all" (AND), or
	// "min-should". The value is validated at startup (Config.Validate);
	// an unrecognised spelling fails boot rather than silently defaulting (#911).
	DefaultMode string
	// DefaultMinShould is the minimum-should-match count applied when the mode
	// resolves to min-should but the request leaves min_should_match at 0.
	DefaultMinShould    uint32
	Timeout             time.Duration
	MaxQueryBytes       int
	MaxQueryTerms       int
	MaxDictionaryVisits int
	MaxPostingVisits    int
	MaxPositionVisits   int
	MaxExpirationVisits int
	MaxInFlight         int
	CursorTTL           time.Duration
	MaxSessions         int
	MaxSessionHits      int
	MaxSessionBytes     int64
	AnalysisLimits      search.SearchAnalysisLimits
}

func loadSearch(reader *envconfig.Reader) SearchConfig {
	return SearchConfig{
		Enabled:             reader.Bool("LANTERN_SEARCH_ENABLED", true),
		Positions:           reader.Bool("LANTERN_SEARCH_POSITIONS", true),
		DefaultLimit:        reader.Uint32("LANTERN_SEARCH_DEFAULT_LIMIT", 100),
		MaxLimit:            reader.Uint32("LANTERN_SEARCH_MAX_LIMIT", 1000),
		DefaultMode:         reader.String("LANTERN_SEARCH_DEFAULT_MODE", "any"),
		DefaultMinShould:    reader.Uint32("LANTERN_SEARCH_DEFAULT_MIN_SHOULD", 1),
		Timeout:             time.Duration(reader.Int("LANTERN_SEARCH_TIMEOUT_MS", 5000)) * time.Millisecond,
		MaxQueryBytes:       reader.Int("LANTERN_SEARCH_MAX_QUERY_BYTES", 16*1024),
		MaxQueryTerms:       reader.Int("LANTERN_SEARCH_MAX_QUERY_TERMS", 1024),
		MaxDictionaryVisits: reader.Int("LANTERN_SEARCH_MAX_DICTIONARY_VISITS", 1_000_000),
		MaxPostingVisits:    reader.Int("LANTERN_SEARCH_MAX_POSTING_VISITS", 10_000_000),
		MaxPositionVisits:   reader.Int("LANTERN_SEARCH_MAX_POSITION_VISITS", 10_000_000),
		MaxExpirationVisits: reader.Int("LANTERN_SEARCH_MAX_EXPIRATION_VISITS", 100_000),
		MaxInFlight:         reader.Int("LANTERN_SEARCH_MAX_IN_FLIGHT", 32),
		CursorTTL:           time.Duration(reader.Int("LANTERN_SEARCH_CURSOR_TTL_SECONDS", 60)) * time.Second,
		MaxSessions:         reader.Int("LANTERN_SEARCH_MAX_SESSIONS", 128),
		MaxSessionHits:      reader.Int("LANTERN_SEARCH_MAX_SESSION_HITS", 10_000),
		MaxSessionBytes:     int64(reader.Int("LANTERN_SEARCH_MAX_SESSION_BYTES", 64<<20)),
		AnalysisLimits: search.SearchAnalysisLimits{
			MaxDocumentBytes:     reader.Int("LANTERN_SEARCH_MAX_DOCUMENT_BYTES", 1<<20),
			MaxDocumentTokens:    reader.Int("LANTERN_SEARCH_MAX_DOCUMENT_TOKENS", 250_000),
			MaxDocumentTerms:     reader.Int("LANTERN_SEARCH_MAX_DOCUMENT_TERMS", 100_000),
			MaxLiveTerms:         reader.Int("LANTERN_SEARCH_MAX_LIVE_TERMS", 5_000_000),
			MaxLivePostings:      int64(reader.Int("LANTERN_SEARCH_MAX_LIVE_POSTINGS", 50_000_000)),
			MaxPositionEntries:   int64(reader.Int("LANTERN_SEARCH_MAX_POSITION_ENTRIES", 50_000_000)),
			CompactionRatio:      reader.Float("LANTERN_SEARCH_COMPACTION_RATIO", 2),
			CompactionMinRetired: reader.Int("LANTERN_SEARCH_COMPACTION_MIN_RETIRED", 10_000),
		},
	}
}

// ParseMatchMode shares the accepted operational spellings with the query
// adapter. An empty value selects the default; unknown spellings return false.
func ParseMatchMode(s string) (search.MatchMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "any":
		return search.MatchAny, true
	case "all":
		return search.MatchAll, true
	case "min-should", "minshould", "min_should":
		return search.MatchMinShould, true
	default:
		return search.MatchAny, false
	}
}
