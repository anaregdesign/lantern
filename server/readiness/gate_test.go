package readiness

import (
	"sync"
	"testing"

	"connectrpc.com/grpchealth"
)

type fakeHealth struct {
	mu     sync.Mutex
	calls  []grpchealth.Status
	latest grpchealth.Status
}

func (f *fakeHealth) SetServingStatus(_ string, s grpchealth.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
	f.latest = s
}

func (f *fakeHealth) snapshot() (latest grpchealth.Status, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.latest, len(f.calls)
}

func TestGate_SingleInstanceBypass(t *testing.T) {
	hs := &fakeHealth{}
	g := NewGate(100, false, hs)

	if !g.Ready() {
		t.Fatalf("single-instance gate must be Ready at construction")
	}
	latest, n := hs.snapshot()
	if n != 1 || latest != grpchealth.StatusServing {
		t.Fatalf("expected 1 SERVING transition, got %d calls latest=%v", n, latest)
	}

	// Lag updates are bypassed entirely.
	g.SetLag("peer-a", "abc", 1_000_000)
	if !g.Ready() {
		t.Fatalf("single-instance gate must ignore SetLag")
	}
	_, n2 := hs.snapshot()
	if n2 != 1 {
		t.Fatalf("single-instance gate must not re-emit health transitions, got %d calls", n2)
	}
}

func TestGate_MultiPeer_BootstrapAndLag(t *testing.T) {
	hs := &fakeHealth{}
	g := NewGate(100, true, hs)

	if g.Ready() {
		t.Fatalf("multi-peer gate must start NOT_SERVING")
	}
	_, n := hs.snapshot()
	if n != 0 {
		t.Fatalf("no health transitions expected before evaluate, got %d", n)
	}

	// Lag observations before bootstrap stay NOT_SERVING.
	g.SetLag("peer-a", "abc", 50)
	if g.Ready() {
		t.Fatalf("must remain NOT_SERVING before MarkBootstrapped")
	}

	// Bootstrap with one healthy lag row → SERVING.
	g.MarkBootstrapped()
	if !g.Ready() {
		t.Fatalf("expected SERVING after bootstrap with lag below threshold")
	}
	latest, _ := hs.snapshot()
	if latest != grpchealth.StatusServing {
		t.Fatalf("expected SERVING transition, got %v", latest)
	}

	// Lag exceeds threshold → NOT_SERVING.
	g.SetLag("peer-a", "abc", 101)
	if g.Ready() {
		t.Fatalf("expected NOT_SERVING when lag > maxLag")
	}
	latest, _ = hs.snapshot()
	if latest != grpchealth.StatusNotServing {
		t.Fatalf("expected NOT_SERVING transition, got %v", latest)
	}

	// Caught up → SERVING again.
	g.SetLag("peer-a", "abc", 0)
	if !g.Ready() {
		t.Fatalf("expected SERVING after lag clears")
	}
}

func TestGate_PumpAndAntiEntropyAdapters(t *testing.T) {
	hs := &fakeHealth{}
	g := NewGate(10, true, hs)

	// OnPumpConnect should mark bootstrapped.
	g.OnPumpConnect("peer-a")
	if !g.Ready() {
		t.Fatalf("expected SERVING after first OnPumpConnect with no lag rows")
	}

	// AntiEntropy Behind beyond threshold should flip NOT_SERVING.
	g.OnAntiEntropyBehind("peer-a", "origin-a", 11)
	if g.Ready() {
		t.Fatalf("expected NOT_SERVING after Behind > maxLag")
	}

	// CaughtUp clears the row.
	g.OnAntiEntropyCaughtUp("peer-a", "origin-a", 42)
	if !g.Ready() {
		t.Fatalf("expected SERVING after CaughtUp")
	}

	// Idempotent transitions: identical SetLag should not double-fire.
	before := func() int { _, n := hs.snapshot(); return n }()
	g.SetLag("peer-a", "origin-a", 0)
	after := func() int { _, n := hs.snapshot(); return n }()
	if after != before {
		t.Fatalf("idempotent SetLag(0) should not re-emit, got %d → %d", before, after)
	}
}

func TestGate_MultiplePeersOriginsTracked(t *testing.T) {
	g := NewGate(10, true, nil)
	g.MarkBootstrapped()
	if !g.Ready() {
		t.Fatalf("expected SERVING after bootstrap")
	}

	g.SetLag("peer-a", "o1", 5)
	g.SetLag("peer-b", "o2", 5)
	if !g.Ready() {
		t.Fatalf("expected SERVING when all rows below threshold")
	}

	g.SetLag("peer-b", "o2", 11)
	if g.Ready() {
		t.Fatalf("expected NOT_SERVING when any row exceeds threshold")
	}

	// Clearing the bad row but leaving the healthy one returns to SERVING.
	g.SetLag("peer-b", "o2", 0)
	if !g.Ready() {
		t.Fatalf("expected SERVING after bad row clears")
	}
}
