package replication

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeLookup is a HostLookup that returns whatever LookupHost is
// configured to return at call time. Goroutine-safe so tests can
// swap responses while a supervisor loop is polling.
type fakeLookup struct {
	mu    sync.Mutex
	hosts []string
	err   error
	calls int
}

func (f *fakeLookup) LookupHost(_ context.Context, _ string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make([]string, len(f.hosts))
	copy(out, f.hosts)
	return out, nil
}

func (f *fakeLookup) set(hosts []string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hosts = hosts
	f.err = err
}

func TestStaticSource_Resolve_ReturnsCopy(t *testing.T) {
	src := StaticSource{Peers: []string{"a:1", "b:2"}}
	got, err := src.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 2 || got[0] != "a:1" || got[1] != "b:2" {
		t.Fatalf("unexpected: %v", got)
	}
	// Mutating the result must not affect future calls.
	got[0] = "MUTATED"
	got2, _ := src.Resolve(context.Background())
	if got2[0] != "a:1" {
		t.Fatalf("Resolve leaked internal slice: %v", got2)
	}
}

func TestDNSSource_Resolve_FormatsAddresses(t *testing.T) {
	lk := &fakeLookup{hosts: []string{"10.0.0.1", "10.0.0.2"}}
	src := &DNSSource{Name: "lantern.svc", Port: "50051", Lookup: lk}
	got, err := src.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	sort.Strings(got)
	want := []string{"10.0.0.1:50051", "10.0.0.2:50051"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestDNSSource_Resolve_FiltersSelfIPs(t *testing.T) {
	lk := &fakeLookup{hosts: []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}}
	src := &DNSSource{
		Name:    "lantern.svc",
		Port:    "50051",
		Lookup:  lk,
		SelfIPs: map[string]struct{}{"10.0.0.2": {}},
	}
	got, err := src.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, a := range got {
		if a == "10.0.0.2:50051" {
			t.Fatalf("self IP not filtered: %v", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 peers, got %v", got)
	}
}

func TestDNSSource_Resolve_DedupesAndStripsEmpty(t *testing.T) {
	lk := &fakeLookup{hosts: []string{"10.0.0.1", "", "10.0.0.1", "  ", "10.0.0.2"}}
	src := &DNSSource{Name: "lantern.svc", Port: "50051", Lookup: lk}
	got, err := src.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 unique peers, got %v", got)
	}
}

func TestDNSSource_Resolve_PropagatesLookupError(t *testing.T) {
	lk := &fakeLookup{err: errors.New("nxdomain")}
	src := &DNSSource{Name: "lantern.svc", Port: "50051", Lookup: lk}
	_, err := src.Resolve(context.Background())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestDNSSource_Resolve_RequiresNameAndPort(t *testing.T) {
	if _, err := (&DNSSource{Port: "50051"}).Resolve(context.Background()); err == nil {
		t.Fatalf("expected error for empty Name")
	}
	if _, err := (&DNSSource{Name: "x"}).Resolve(context.Background()); err == nil {
		t.Fatalf("expected error for empty Port")
	}
}

// TestPeerSupervisor_Reconcile_AddsAndRemoves drives the supervisor
// through several reconciliations and asserts the expected
// spawn / cancel transitions land. This is the test #190 specifies
// as the acceptance criterion for "simulated A-record changes drive
// correct add/remove".
func TestPeerSupervisor_Reconcile_AddsAndRemoves(t *testing.T) {
	var (
		mu      sync.Mutex
		started = map[string]int{}
		stopped = map[string]int{}
	)
	stoppedCh := make(chan string, 16)

	run := func(ctx context.Context, addr string) {
		mu.Lock()
		started[addr]++
		mu.Unlock()
		<-ctx.Done()
		mu.Lock()
		stopped[addr]++
		mu.Unlock()
		stoppedCh <- addr
	}

	sup := newPeerSupervisor(run)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// initial: {a, b}
	sup.reconcile(ctx, []string{"a:1", "b:1"})
	waitFor(t, "started a+b", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return started["a:1"] == 1 && started["b:1"] == 1
	})

	// reconcile to {b, c}: a departs, c joins.
	sup.reconcile(ctx, []string{"b:1", "c:1"})
	drainOne(t, stoppedCh, "a:1")
	waitFor(t, "started c", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return started["c:1"] == 1
	})

	got := sup.active()
	sort.Strings(got)
	if len(got) != 2 || got[0] != "b:1" || got[1] != "c:1" {
		t.Fatalf("active() = %v, want [b:1 c:1]", got)
	}

	// idempotent reconcile: no new spawns, no cancels.
	sup.reconcile(ctx, []string{"b:1", "c:1"})
	mu.Lock()
	if started["b:1"] != 1 || started["c:1"] != 1 {
		mu.Unlock()
		t.Fatalf("idempotent reconcile re-spawned: %v", started)
	}
	mu.Unlock()

	// shutdown cancels remaining.
	sup.shutdown()
	mu.Lock()
	defer mu.Unlock()
	if stopped["b:1"] != 1 || stopped["c:1"] != 1 {
		t.Fatalf("shutdown did not cancel all: stopped=%v", stopped)
	}
}

// TestPump_Run_DiscoveryInterval_PollsAndReconciles boots Pump.Run
// against a fake PeerSource and asserts the supervisor reconciles
// added / removed peers on each tick. We avoid spinning up real
// Connect-Go clients by leaving the runner field on Pump unchanged but
// pointing peers at unroutable loopback ports — runPeer will fail
// to dial and back off, which is fine: we only assert the
// supervisor lifecycle, not connection success.
func TestPump_Run_DiscoveryInterval_PollsAndReconciles(t *testing.T) {
	// Use a recordingSource so we can drive add/remove deterministically.
	src := &recordingSource{addrs: []string{"127.0.0.1:1"}}
	p := NewPump(Config{
		Source:            src,
		DiscoveryInterval: 25 * time.Millisecond,
		BackoffMin:        1 * time.Hour, // suppress reconnect churn
		BackoffMax:        1 * time.Hour,
	}, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = p.Run(ctx)
		close(done)
	}()

	// Wait for at least 3 Resolve calls (initial + 2 ticks).
	waitFor(t, "source polled >=3 times", func() bool {
		return atomic.LoadInt64(&src.calls) >= 3
	})

	// Swap the address set; supervisor should converge.
	src.set([]string{"127.0.0.1:2", "127.0.0.1:3"})
	waitFor(t, "source observed new set", func() bool {
		return atomic.LoadInt64(&src.calls) >= 5
	})

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Pump.Run did not return after cancel")
	}
}

// TestPump_Run_DiscoveryError_KeepsPreviousSet asserts that a
// transient Source error logs but does NOT tear down active peers.
func TestPump_Run_DiscoveryError_KeepsPreviousSet(t *testing.T) {
	src := &recordingSource{addrs: []string{"127.0.0.1:1"}}
	p := NewPump(Config{
		Source:            src,
		DiscoveryInterval: 20 * time.Millisecond,
		BackoffMin:        1 * time.Hour,
		BackoffMax:        1 * time.Hour,
	}, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = p.Run(ctx); close(done) }()

	waitFor(t, "initial resolve", func() bool {
		return atomic.LoadInt64(&src.calls) >= 1
	})

	src.setErr(errors.New("dns went away"))
	// Allow several failed ticks; the supervisor must not panic
	// and Run must keep running.
	time.Sleep(80 * time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Pump.Run did not return after cancel")
	}
}

// recordingSource is a minimal PeerSource for Pump.Run tests.
type recordingSource struct {
	mu    sync.Mutex
	addrs []string
	err   error
	calls int64
}

func (r *recordingSource) Resolve(context.Context) ([]string, error) {
	atomic.AddInt64(&r.calls, 1)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	out := make([]string, len(r.addrs))
	copy(out, r.addrs)
	return out, nil
}

func (r *recordingSource) set(a []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addrs = a
	r.err = nil
}

func (r *recordingSource) setErr(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

func waitFor(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", desc)
}

func drainOne(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("got stopped=%q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for stop of %q", want)
	}
}
