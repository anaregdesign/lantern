package query

import (
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestNormalizeLimits(t *testing.T) {
	defaults := DefaultLimits()
	got := NormalizeLimits(Limits{Enabled: true, CursorTTL: -time.Second, MaxSessions: -1, MaxSessionHits: -1, MaxSessionBytes: -1})
	if got.DefaultLimit != defaults.DefaultLimit || got.MaxLimit != defaults.MaxLimit || got.CursorTTL != defaults.CursorTTL || got.MaxSessions != defaults.MaxSessions || got.MaxSessionHits != defaults.MaxSessionHits || got.MaxSessionBytes != defaults.MaxSessionBytes {
		t.Fatalf("missing defaults: %+v", got)
	}
	if !got.Enabled || got.Timeout != 0 || got.MaxQueryBytes != 0 || got.MaxInFlight != 0 || got.WorkBudget != (search.Budget{}) {
		t.Fatalf("explicit opt-in or unlimited fields changed: %+v", got)
	}
	configured := Limits{DefaultLimit: 7, MaxLimit: 11, CursorTTL: time.Second, MaxSessions: 3, MaxSessionHits: 13, MaxSessionBytes: 1024}
	if got := NormalizeLimits(configured); got != configured {
		t.Fatalf("configured limits changed: got %+v, want %+v", got, configured)
	}
}

func TestConfigFingerprintPreservesCapabilityContract(t *testing.T) {
	// Captured from the pre-extraction service implementation. Cursor and peer
	// compatibility must not change when configuration ownership moves packages.
	const want = "25467ce187496aec2c3076d65570f8216a6b1c7a05ef48a1b0192a5671babb39"
	if got := ConfigFingerprint(DefaultLimits()); got != want {
		t.Fatalf("default capability fingerprint = %q, want %q", got, want)
	}
}

func TestCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		health  search.IndexHealth
		want    pb.SearchIndexHealth
	}{
		{"disabled", false, search.IndexHealthy, pb.SearchIndexHealth_SEARCH_INDEX_HEALTH_DISABLED},
		{"healthy", true, search.IndexHealthy, pb.SearchIndexHealth_SEARCH_INDEX_HEALTH_HEALTHY},
		{"incomplete", true, search.IndexIncomplete, pb.SearchIndexHealth_SEARCH_INDEX_HEALTH_INCOMPLETE},
		{"unknown", true, "", pb.SearchIndexHealth_SEARCH_INDEX_HEALTH_UNSPECIFIED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limits := DefaultLimits()
			limits.Enabled, limits.PositionsEnabled = tc.enabled, true
			stats := search.IndexMemoryStats{Health: tc.health, Documents: 3, RebuildCount: 2, LastRebuildDuration: time.Second, ExpirationPurged: 1, LastExpirationPurge: time.Millisecond}
			got := Capabilities(limits, stats)
			if got.GetIndexStats().GetHealth() != tc.want || got.GetPositionsEnabled() != tc.enabled || got.GetIndexStats().GetDocuments() != 3 {
				t.Fatalf("capabilities = %+v", got)
			}
			if got.GetConfigFingerprint() != ConfigFingerprint(limits) || got.GetIndexStats().GetLastRebuildDuration().AsDuration() != time.Second || got.GetIndexStats().GetLastExpirationPurgeDuration().AsDuration() != time.Millisecond {
				t.Fatalf("configuration/sample binding changed: %+v", got)
			}
		})
	}
}

func TestParseMatchMode(t *testing.T) {
	tests := []struct {
		in   string
		want search.MatchMode
	}{
		{"", search.MatchAny},
		{"any", search.MatchAny},
		{"ANY", search.MatchAny},
		{"  all  ", search.MatchAll},
		{"min-should", search.MatchMinShould},
		{"minshould", search.MatchMinShould},
		{"min_should", search.MatchMinShould},
		// Unrecognised launders to MatchAny — a dead path in a booted server
		// because the provider rejects the value at startup, but kept tolerant
		// so the wire seam never has to handle an error.
		{"min_shold", search.MatchAny},
		{"or", search.MatchAny},
	}
	for _, tc := range tests {
		if got := ParseMatchMode(tc.in); got != tc.want {
			t.Errorf("ParseMatchMode(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
