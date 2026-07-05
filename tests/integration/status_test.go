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
	if _, err := l.AddEdge(ctx, "a", "b", 1.0, time.Minute); err != nil {
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

// TestLantern_GetServerStatus_Started_EndToEnd is the #943 regression: a
// freshly booted server — one that marked itself started at the
// ready-to-serve edge, exactly as App.Run now does — must report a
// StartedAt near the boot instant and a strictly positive Uptime over the
// real Connect/h2c wire path. The shipped bug slipped through because the
// trigger (MarkStarted) was never called in production while the unit
// tests called it themselves; this asserts the RPC surface actually
// returns both fields when the server has been marked started.
func TestLantern_GetServerStatus_Started_EndToEnd(t *testing.T) {
	l, startedAt, cleanup := newInProcessClientStarted(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	st, err := l.GetServerStatus(ctx)
	if err != nil {
		t.Fatalf("GetServerStatus: %v", err)
	}

	got := client.ServerStartedAt(st)
	if got.IsZero() {
		t.Fatal("ServerStartedAt: got zero, want the marked boot instant on the wire")
	}
	if !got.Equal(startedAt) {
		t.Errorf("ServerStartedAt: got %v, want %v (the instant MarkStarted recorded)", got, startedAt)
	}
	if up := client.ServerUptime(st); up <= 0 {
		t.Errorf("ServerUptime: got %v, want > 0 for a freshly booted server", up)
	}
}
