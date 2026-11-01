package query

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/config"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	searchMaxFuzziness      = uint32(2)
	searchAnalyzerVersion   = "script-aware-v2"
	searchProjectionVersion = "vertex-fields-v2"
)

// Limits configures the SearchVertices RPC. Enabled gates the RPC: a
// false value makes SearchVertices return FAILED_PRECONDITION, mirroring
// the composition root's decision NOT to call GraphCache.EnableSearchIndex
// (the core search backend returns nil for a disabled index, a no-match,
// and an empty query alike, so the gate cannot live in the cache — #624).
// PositionsEnabled records whether adjacency can be verified; phrase requests
// fail closed when false.
// DefaultLimit / MaxLimit cap the per-call ranked-hit count.
type Limits struct {
	Enabled          bool
	PositionsEnabled bool
	DefaultLimit     uint32
	MaxLimit         uint32
	// DefaultMode is the match mode applied when a request leaves match_mode
	// unspecified (#892); the zero value is MatchAny (the OR-union default).
	DefaultMode search.MatchMode
	// DefaultMinShould is the minimum-should-match count applied when the mode
	// is MatchMinShould but the request leaves min_should_match at 0.
	DefaultMinShould uint32
	Timeout          time.Duration
	MaxQueryBytes    int
	WorkBudget       search.Budget
	MaxInFlight      int
	CursorTTL        time.Duration
	MaxSessions      int
	MaxSessionHits   int
	MaxSessionBytes  int64
	AnalysisLimits   search.SearchAnalysisLimits
}

// DefaultLimits returns the constructor defaults. Search remains opt-in.
func DefaultLimits() Limits {
	return Limits{
		Enabled:       false,
		DefaultLimit:  100,
		MaxLimit:      1000,
		Timeout:       5 * time.Second,
		MaxQueryBytes: 16 * 1024,
		WorkBudget: search.Budget{
			MaxQueryTerms:       1024,
			MaxDictionaryVisits: 1_000_000,
			MaxPostingVisits:    10_000_000,
			MaxPositionVisits:   10_000_000,
			MaxExpirationVisits: 100_000,
		},
		MaxInFlight:     32,
		CursorTTL:       time.Minute,
		MaxSessions:     128,
		MaxSessionHits:  10_000,
		MaxSessionBytes: 64 << 20,
	}
}

// NormalizeLimits fills only the defaults historically applied by WithSearchLimits.
// Zero timeout, work, admission and query-byte limits retain their explicit meaning.
func NormalizeLimits(l Limits) Limits {
	defaults := DefaultLimits()
	if l.DefaultLimit == 0 {
		l.DefaultLimit = defaults.DefaultLimit
	}
	if l.MaxLimit == 0 {
		l.MaxLimit = defaults.MaxLimit
	}
	if l.CursorTTL <= 0 {
		l.CursorTTL = defaults.CursorTTL
	}
	if l.MaxSessions <= 0 {
		l.MaxSessions = defaults.MaxSessions
	}
	if l.MaxSessionHits <= 0 {
		l.MaxSessionHits = defaults.MaxSessionHits
	}
	if l.MaxSessionBytes <= 0 {
		l.MaxSessionBytes = defaults.MaxSessionBytes
	}
	return l
}

// matchModeFromPB maps the wire enum to the core match mode, using fallback when
// the field is unspecified.
func matchModeFromPB(m pb.MatchMode, fallback search.MatchMode) search.MatchMode {
	switch m {
	case pb.MatchMode_MATCH_MODE_ANY:
		return search.MatchAny
	case pb.MatchMode_MATCH_MODE_ALL:
		return search.MatchAll
	case pb.MatchMode_MATCH_MODE_MIN_SHOULD:
		return search.MatchMinShould
	default:
		return fallback
	}
}

func matchModeToPB(m search.MatchMode) pb.MatchMode {
	switch m {
	case search.MatchAll:
		return pb.MatchMode_MATCH_MODE_ALL
	case search.MatchMinShould:
		return pb.MatchMode_MATCH_MODE_MIN_SHOULD
	default:
		return pb.MatchMode_MATCH_MODE_ANY
	}
}

// ParseMatchMode maps a LANTERN_SEARCH_DEFAULT_MODE value to a core match mode.
// It is deliberately tolerant — the empty string and any unrecognised value
// resolve to MatchAny — because the provider validates the value at startup
// (config.Config.Validate) before this wire seam converts provider config to service
// config, so an unrecognised value can no longer reach it in a booted server.
// Exported for that seam in package main.
func ParseMatchMode(s string) search.MatchMode {
	mode, _ := config.ParseMatchMode(s)
	return mode
}

// Capabilities projects the search configuration and one committed index sample.
func Capabilities(l Limits, indexStats search.IndexMemoryStats) *pb.SearchCapabilities {
	limits := l.AnalysisLimits
	health := pb.SearchIndexHealth_SEARCH_INDEX_HEALTH_UNSPECIFIED
	if !l.Enabled {
		health = pb.SearchIndexHealth_SEARCH_INDEX_HEALTH_DISABLED
	} else if indexStats.Health == search.IndexHealthy {
		health = pb.SearchIndexHealth_SEARCH_INDEX_HEALTH_HEALTHY
	} else if indexStats.Health == search.IndexIncomplete {
		health = pb.SearchIndexHealth_SEARCH_INDEX_HEALTH_INCOMPLETE
	}
	capabilities := &pb.SearchCapabilities{
		Enabled:               l.Enabled,
		PositionsEnabled:      l.Enabled && l.PositionsEnabled,
		DefaultLimit:          l.DefaultLimit,
		MaxLimit:              l.MaxLimit,
		DefaultMatchMode:      matchModeToPB(l.DefaultMode),
		DefaultMinShouldMatch: l.DefaultMinShould,
		MaxFuzziness:          searchMaxFuzziness,
		AnalyzerVersion:       searchAnalyzerVersion,
		ProjectionVersion:     searchProjectionVersion,
		TimeoutMs:             uint32(l.Timeout.Milliseconds()),
		MaxQueryBytes:         uint32(l.MaxQueryBytes),
		MaxQueryTerms:         uint32(l.WorkBudget.MaxQueryTerms),
		MaxDictionaryVisits:   uint64(l.WorkBudget.MaxDictionaryVisits),
		MaxPostingVisits:      uint64(l.WorkBudget.MaxPostingVisits),
		MaxPositionVisits:     uint64(l.WorkBudget.MaxPositionVisits),
		MaxExpirationVisits:   uint64(l.WorkBudget.MaxExpirationVisits),
		MaxInFlight:           uint32(l.MaxInFlight),
		CursorTtlSeconds:      uint32(l.CursorTTL / time.Second),
		MaxSessions:           uint32(l.MaxSessions),
		MaxSessionHits:        uint32(l.MaxSessionHits),
		MaxSessionBytes:       uint64(l.MaxSessionBytes),
		MaxDocumentBytes:      uint32(limits.MaxDocumentBytes),
		MaxDocumentTokens:     uint32(limits.MaxDocumentTokens),
		MaxDocumentTerms:      uint32(limits.MaxDocumentTerms),
		MaxLiveTerms:          uint64(limits.MaxLiveTerms),
		MaxLivePostings:       uint64(limits.MaxLivePostings),
		MaxPositionEntries:    uint64(limits.MaxPositionEntries),
		CompactionRatio:       limits.CompactionRatio,
		CompactionMinRetired:  uint64(limits.CompactionMinRetired),
		IndexStats: &pb.SearchIndexStats{
			Health:                 health,
			Documents:              uint64(indexStats.Documents),
			PhysicalDocuments:      uint64(indexStats.PhysicalDocuments),
			ExpiredDocuments:       uint64(indexStats.ExpiredDocuments),
			ExpirationQueueEntries: uint64(indexStats.ExpirationQueueEntries),
			ExpirationPurged:       indexStats.ExpirationPurged,
			LiveTerms:              uint64(indexStats.LiveTerms),
			RetainedTermSlots:      uint64(indexStats.RetainedTermSlots),
			RetainedOrdinals:       uint64(indexStats.RetainedOrdinals),
			Postings:               uint64(indexStats.Postings),
			PositionEntries:        uint64(indexStats.PositionEntries),
			EstimatedLiveBytes:     uint64(indexStats.EstimatedLiveBytes),
			EstimatedRetainedBytes: uint64(indexStats.EstimatedRetainedBytes),
			RebuildCount:           indexStats.RebuildCount,
			Generation:             indexStats.Generation,
		},
		ConfigFingerprint: ConfigFingerprint(l),
	}
	if indexStats.RebuildCount > 0 {
		capabilities.IndexStats.LastRebuildDuration = durationpb.New(indexStats.LastRebuildDuration)
	}
	if indexStats.ExpirationPurged > 0 {
		capabilities.IndexStats.LastExpirationPurgeDuration = durationpb.New(indexStats.LastExpirationPurge)
	}
	return capabilities
}

// ConfigFingerprint binds the exact search capability contract used by peers and cursors.
func ConfigFingerprint(limits Limits) string {
	analysis := limits.AnalysisLimits
	canonical := fmt.Sprintf(
		"enabled=%t\npositions=%t\ndefault_limit=%d\nmax_limit=%d\ndefault_match_mode=%d\ndefault_min_should_match=%d\nmax_fuzziness=%d\nanalyzer=%s\nprojection=%s\ntimeout_ms=%d\nmax_query_bytes=%d\nmax_query_terms=%d\nmax_dictionary_visits=%d\nmax_posting_visits=%d\nmax_position_visits=%d\nmax_expiration_visits=%d\nmax_in_flight=%d\ncursor_ttl_seconds=%d\nmax_sessions=%d\nmax_session_hits=%d\nmax_session_bytes=%d\nmax_document_bytes=%d\nmax_document_tokens=%d\nmax_document_terms=%d\nmax_live_terms=%d\nmax_live_postings=%d\nmax_position_entries=%d\ncompaction_ratio=%g\ncompaction_min_retired=%d\n",
		limits.Enabled,
		limits.PositionsEnabled,
		limits.DefaultLimit,
		limits.MaxLimit,
		matchModeToPB(limits.DefaultMode),
		limits.DefaultMinShould,
		searchMaxFuzziness,
		searchAnalyzerVersion,
		searchProjectionVersion,
		limits.Timeout.Milliseconds(),
		limits.MaxQueryBytes,
		limits.WorkBudget.MaxQueryTerms,
		limits.WorkBudget.MaxDictionaryVisits,
		limits.WorkBudget.MaxPostingVisits,
		limits.WorkBudget.MaxPositionVisits,
		limits.WorkBudget.MaxExpirationVisits,
		limits.MaxInFlight,
		limits.CursorTTL/time.Second,
		limits.MaxSessions,
		limits.MaxSessionHits,
		limits.MaxSessionBytes,
		analysis.MaxDocumentBytes,
		analysis.MaxDocumentTokens,
		analysis.MaxDocumentTerms,
		analysis.MaxLiveTerms,
		analysis.MaxLivePostings,
		analysis.MaxPositionEntries,
		analysis.CompactionRatio,
		analysis.CompactionMinRetired,
	)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}
