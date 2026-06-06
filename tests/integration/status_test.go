package integration_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestLantern_GetServerStatus_EndToEnd(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Write a few entries so we can prove the live counters tick.
	if err := l.PutVertex(ctx, "a", "alpha", time.Minute); err != nil {
		t.Fatalf("PutVertex a: %v", err)
	}
	if err := l.PutVertex(ctx, "b", "bravo", time.Minute); err != nil {
		t.Fatalf("PutVertex b: %v", err)
	}
	if err := l.AddEdge(ctx, "a", "b", 1.0, time.Minute); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	st, err := l.GetServerStatus(ctx)
	if err != nil {
		t.Fatalf("GetServerStatus: %v", err)
	}

	// GoVersion is always populated — the SDK never strips it.
	if got, want := st.GetGoVersion(), runtime.Version(); got != want {
		t.Errorf("GoVersion: got %q want %q", got, want)
	}

	// In-process server was constructed via service.NewLanternService
	// without WithStatusInfo / MarkStarted, so StartedAt / Version /
	// MaxBatchSize etc. are intentionally zero-valued. We still expect
	// the response to be well-formed and to surface the live counts.
	if got, want := st.GetVertexCount(), uint64(2); got != want {
		t.Errorf("VertexCount: got %d want %d", got, want)
	}
	if got, want := st.GetEdgeCount(), uint64(1); got != want {
		t.Errorf("EdgeCount: got %d want %d", got, want)
	}
	if got := client.ServerStartedAt(st); !got.IsZero() {
		t.Errorf("ServerStartedAt: got %v, want zero in this test (MarkStarted unwired)", got)
	}
	if got := client.ServerUptime(st); got != 0 {
		t.Errorf("ServerUptime: got %v, want 0 in this test (MarkStarted unwired)", got)
	}
}
