package service

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestLanternService_GetServerStatus(t *testing.T) {
	t.Run("EchoesConfigAndLiveCounts", func(t *testing.T) {
		fb := newFakeBackend()
		fb.vertices["a"] = &pb.Vertex{Key: "a"}
		fb.vertices["b"] = &pb.Vertex{Key: "b"}
		fb.edges["a"] = map[string]float32{"b": 1, "c": 2}

		startedAt := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
		svc := NewLanternService(fb).WithStatusInfo(StatusInfo{
			Version:            "1.2.3",
			DefaultTTL:         60 * time.Second,
			MaxBatchSize:       1000,
			MaxKeyBytes:        256,
			ScanDefaultLimit:   500,
			ScanMaxLimit:       5000,
			TLSEnabled:         true,
			ReplicationEnabled: true,
		})
		svc.MarkStarted(startedAt)

		resp, err := svc.GetServerStatus(context.Background(), &pb.GetServerStatusRequest{})
		if err != nil {
			t.Fatalf("GetServerStatus: %v", err)
		}
		if got, want := resp.GetVersion(), "1.2.3"; got != want {
			t.Errorf("Version: got %q want %q", got, want)
		}
		if resp.GetGoVersion() != runtime.Version() {
			t.Errorf("GoVersion: got %q want %q", resp.GetGoVersion(), runtime.Version())
		}
		if resp.GetStartedAt() == nil || !resp.GetStartedAt().AsTime().Equal(startedAt) {
			t.Errorf("StartedAt: got %v want %v", resp.GetStartedAt(), startedAt)
		}
		if resp.GetUptime() == nil || resp.GetUptime().AsDuration() <= 0 {
			t.Errorf("Uptime: got %v want >0", resp.GetUptime())
		}
		if resp.GetDefaultTtl().AsDuration() != 60*time.Second {
			t.Errorf("DefaultTtl: got %v want 60s", resp.GetDefaultTtl().AsDuration())
		}
		if resp.GetMaxBatchSize() != 1000 {
			t.Errorf("MaxBatchSize: got %d want 1000", resp.GetMaxBatchSize())
		}
		if resp.GetMaxKeyBytes() != 256 {
			t.Errorf("MaxKeyBytes: got %d want 256", resp.GetMaxKeyBytes())
		}
		if resp.GetScanDefaultLimit() != 500 {
			t.Errorf("ScanDefaultLimit: got %d want 500", resp.GetScanDefaultLimit())
		}
		if resp.GetScanMaxLimit() != 5000 {
			t.Errorf("ScanMaxLimit: got %d want 5000", resp.GetScanMaxLimit())
		}
		if !resp.GetTlsEnabled() {
			t.Errorf("TlsEnabled: got false want true")
		}
		if !resp.GetReplicationEnabled() {
			t.Errorf("ReplicationEnabled: got false want true")
		}
		if got, want := resp.GetVertexCount(), uint64(2); got != want {
			t.Errorf("VertexCount: got %d want %d", got, want)
		}
		if got, want := resp.GetEdgeCount(), uint64(2); got != want {
			t.Errorf("EdgeCount: got %d want %d", got, want)
		}
	})

	t.Run("DefaultsWhenStatusInfoNotWired", func(t *testing.T) {
		// Hitting the test path that constructs LanternService without
		// WithStatusInfo / MarkStarted must still produce a well-formed
		// response — guards the "additive builder, never required" rule.
		fb := newFakeBackend()
		svc := NewLanternService(fb)

		resp, err := svc.GetServerStatus(context.Background(), &pb.GetServerStatusRequest{})
		if err != nil {
			t.Fatalf("GetServerStatus: %v", err)
		}
		if resp.GetVersion() == "" {
			t.Errorf("Version: got empty, want fallback to runtime build info or 'dev'")
		}
		if resp.GetStartedAt() != nil {
			t.Errorf("StartedAt: got %v, want nil when MarkStarted was not called", resp.GetStartedAt())
		}
		if resp.GetUptime() != nil {
			t.Errorf("Uptime: got %v, want nil when MarkStarted was not called", resp.GetUptime())
		}
		if resp.GetGoVersion() == "" {
			t.Errorf("GoVersion: got empty, want runtime.Version()")
		}
		if resp.GetSearch() == nil || resp.GetSearch().GetConfigFingerprint() == "" {
			t.Errorf("Search: got %+v, want a discoverable capability snapshot", resp.GetSearch())
		}
	})

	t.Run("ReportsSearchCapabilitiesAndConfigFingerprint", func(t *testing.T) {
		fb := newFakeBackend()
		limits := SearchLimits{
			Enabled:          true,
			PositionsEnabled: true,
			DefaultLimit:     25,
			MaxLimit:         250,
			DefaultMode:      search.MatchMinShould,
			DefaultMinShould: 2,
			Timeout:          1500 * time.Millisecond,
			MaxQueryBytes:    4096,
			WorkBudget: search.Budget{
				MaxQueryTerms:       12,
				MaxDictionaryVisits: 34,
				MaxPostingVisits:    56,
				MaxPositionVisits:   78,
				MaxExpirationVisits: 90,
			},
			MaxInFlight: 9,
			AnalysisLimits: search.SearchAnalysisLimits{
				MaxDocumentBytes: 100, MaxDocumentTokens: 90, MaxDocumentTerms: 80,
				MaxLiveTerms: 70, MaxLivePostings: 60, MaxPositionEntries: 50,
				CompactionRatio: 2.5, CompactionMinRetired: 40,
			},
		}
		fb.searchStats = search.IndexMemoryStats{Health: search.IndexHealthy, Documents: 3, PhysicalDocuments: 5, ExpiredDocuments: 2, ExpirationQueueEntries: 2, ExpirationPurged: 9, LastExpirationPurge: 12 * time.Millisecond, LiveTerms: 4, RetainedTermSlots: 5, EstimatedLiveBytes: 100, EstimatedRetainedBytes: 120, RebuildCount: 2}
		withPositions := NewLanternService(fb).WithSearchLimits(limits)
		resp, err := withPositions.GetServerStatus(context.Background(), &pb.GetServerStatusRequest{})
		if err != nil {
			t.Fatalf("GetServerStatus: %v", err)
		}
		got := resp.GetSearch()
		if !got.GetEnabled() || !got.GetPositionsEnabled() {
			t.Errorf("search booleans = enabled:%t positions:%t, want true/true", got.GetEnabled(), got.GetPositionsEnabled())
		}
		if got.GetDefaultLimit() != 25 || got.GetMaxLimit() != 250 {
			t.Errorf("search limits = %d/%d, want 25/250", got.GetDefaultLimit(), got.GetMaxLimit())
		}
		if got.GetDefaultMatchMode() != pb.MatchMode_MATCH_MODE_MIN_SHOULD || got.GetDefaultMinShouldMatch() != 2 {
			t.Errorf("search defaults = %v/%d, want MIN_SHOULD/2", got.GetDefaultMatchMode(), got.GetDefaultMinShouldMatch())
		}
		if got.GetMaxFuzziness() != 2 || got.GetAnalyzerVersion() != "script-aware-v2" || got.GetProjectionVersion() != "vertex-fields-v2" {
			t.Errorf("search implementation capabilities incomplete: %+v", got)
		}
		if got.GetTimeoutMs() != 1500 || got.GetMaxQueryBytes() != 4096 || got.GetMaxQueryTerms() != 12 ||
			got.GetMaxDictionaryVisits() != 34 || got.GetMaxPostingVisits() != 56 ||
			got.GetMaxPositionVisits() != 78 || got.GetMaxExpirationVisits() != 90 || got.GetMaxInFlight() != 9 {
			t.Errorf("search execution capabilities incomplete: %+v", got)
		}
		if len(got.GetConfigFingerprint()) != 64 {
			t.Errorf("fingerprint length = %d, want 64 hex chars", len(got.GetConfigFingerprint()))
		}
		if got.GetMaxDocumentBytes() != 100 || got.GetMaxLivePostings() != 60 || got.GetCompactionRatio() != 2.5 {
			t.Errorf("analysis capabilities incomplete: %+v", got)
		}
		if got.GetIndexStats().GetHealth() != pb.SearchIndexHealth_SEARCH_INDEX_HEALTH_HEALTHY || got.GetIndexStats().GetDocuments() != 3 || got.GetIndexStats().GetPhysicalDocuments() != 5 || got.GetIndexStats().GetExpiredDocuments() != 2 || got.GetIndexStats().GetExpirationQueueEntries() != 2 || got.GetIndexStats().GetExpirationPurged() != 9 || got.GetIndexStats().GetLastExpirationPurgeDuration().AsDuration() != 12*time.Millisecond || got.GetIndexStats().GetEstimatedRetainedBytes() != 120 || got.GetIndexStats().GetRebuildCount() != 2 {
			t.Errorf("index stats incomplete: %+v", got.GetIndexStats())
		}

		limits.PositionsEnabled = false
		withoutPositions := NewLanternService(fb).WithSearchLimits(limits)
		other, err := withoutPositions.GetServerStatus(context.Background(), &pb.GetServerStatusRequest{})
		if err != nil {
			t.Fatalf("GetServerStatus positions off: %v", err)
		}
		if other.GetSearch().GetPositionsEnabled() {
			t.Error("positions_enabled = true, want false")
		}
		if other.GetSearch().GetConfigFingerprint() == got.GetConfigFingerprint() {
			t.Error("heterogeneous search configs reported the same fingerprint")
		}
		limits.PositionsEnabled = true
		limits.AnalysisLimits.MaxDocumentBytes++
		differentBudget, err := NewLanternService(fb).WithSearchLimits(limits).GetServerStatus(context.Background(), &pb.GetServerStatusRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if differentBudget.GetSearch().GetConfigFingerprint() == got.GetConfigFingerprint() {
			t.Error("heterogeneous analysis budgets reported the same fingerprint")
		}
		limits.AnalysisLimits.MaxDocumentBytes--
		limits.WorkBudget.MaxExpirationVisits++
		differentExpirationBudget, err := NewLanternService(fb).WithSearchLimits(limits).GetServerStatus(context.Background(), &pb.GetServerStatusRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if differentExpirationBudget.GetSearch().GetConfigFingerprint() == got.GetConfigFingerprint() {
			t.Error("heterogeneous expiration budgets reported the same fingerprint")
		}
	})

	t.Run("MarkStartedIsIdempotent", func(t *testing.T) {
		fb := newFakeBackend()
		svc := NewLanternService(fb)

		first := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
		second := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
		svc.MarkStarted(first)
		svc.MarkStarted(second)

		resp, err := svc.GetServerStatus(context.Background(), &pb.GetServerStatusRequest{})
		if err != nil {
			t.Fatalf("GetServerStatus: %v", err)
		}
		if !resp.GetStartedAt().AsTime().Equal(first) {
			t.Errorf("StartedAt: got %v, want first call %v (later calls must be no-ops)", resp.GetStartedAt().AsTime(), first)
		}
	})

	t.Run("RecentBootReportsUptime", func(t *testing.T) {
		// #943 regression at the service layer: once App.Run marks the
		// server started at the ready-to-serve edge, GetServerStatus must
		// surface a non-nil StartedAt and a positive Uptime. A server that
		// booted one second ago reports StartedAt at that instant and an
		// Uptime of at least ~1s — never the absent fields that make the
		// admin Ops card fall back to "—".
		fb := newFakeBackend()
		svc := NewLanternService(fb)

		bootedAt := time.Now().Add(-time.Second)
		svc.MarkStarted(bootedAt)

		resp, err := svc.GetServerStatus(context.Background(), &pb.GetServerStatusRequest{})
		if err != nil {
			t.Fatalf("GetServerStatus: %v", err)
		}
		if resp.GetStartedAt() == nil || !resp.GetStartedAt().AsTime().Equal(bootedAt) {
			t.Errorf("StartedAt: got %v want %v", resp.GetStartedAt(), bootedAt)
		}
		if resp.GetUptime() == nil || resp.GetUptime().AsDuration() < time.Second {
			t.Errorf("Uptime: got %v want >= 1s for a server booted 1s ago", resp.GetUptime())
		}
	})

	t.Run("HonorsCanceledContext", func(t *testing.T) {
		fb := newFakeBackend()
		svc := NewLanternService(fb)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := svc.GetServerStatus(ctx, &pb.GetServerStatusRequest{}); err == nil {
			t.Errorf("GetServerStatus: got nil err, want context.Canceled propagation")
		}
	})
}
