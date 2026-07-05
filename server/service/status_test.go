package service

import (
	"context"
	"runtime"
	"testing"
	"time"

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
