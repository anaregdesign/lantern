package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
)

func TestAppRunClosesMutationLogOnRequiredRestoreFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	log := mutationlog.New(mutationlog.Options{})
	runtime, err := service.NewGraphOnlyServingRuntime(
		graphcache.NewGraphCache[string, *pb.Vertex](time.Hour),
		log,
		hlc.New(hlc.NodeID{1}, hlc.Options{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{
		backupper:  backup.New(nil, backup.Config{RestoreOnStart: true, Dir: path}, nil, nil),
		restoreReq: true,
		runtime:    runtime,
	}
	cleanupFailure := errors.New("security abort cleanup failure")
	lifecycle := &appTestSecurityLifecycle{closeErr: cleanupFailure}
	app.security = lifecycle
	if err := app.Run(context.Background()); !errors.Is(err, cleanupFailure) || lifecycle.closed != 1 || lifecycle.orderly != 0 {
		t.Fatal("required restore failure hid abort cleanup or declared CLEAN", err)
	}
	if _, err := log.Append(&pb.Mutation{}, hlc.Timestamp{}); !errors.Is(err, mutationlog.ErrClosed) {
		t.Fatalf("Log after failed startup = %v, want closed", err)
	}
}

type appTestSecurityLifecycle struct {
	owned             securityLifecycle
	closed, orderly   int
	closeErr, stopErr error
}

func (s *appTestSecurityLifecycle) Close() error {
	s.closed++
	if s.owned != nil {
		return errors.Join(s.closeErr, s.owned.Close())
	}
	return s.closeErr
}
func (s *appTestSecurityLifecycle) Shutdown() error {
	s.orderly++
	if s.owned != nil {
		return errors.Join(s.stopErr, s.owned.Shutdown())
	}
	return s.stopErr
}

type appTestMetricsWorker struct {
	started chan struct{}
	failure error
}

func (w appTestMetricsWorker) Run(ctx context.Context) error {
	close(w.started)
	if w.failure != nil {
		return w.failure
	}
	<-ctx.Done()
	return nil
}

func TestAppRunOnlyCompletedDrainUsesOrderlySecurityShutdown(t *testing.T) {
	for _, phase := range []string{"normal", "shutdown-error", "worker-failure", "deadline", "pre-canceled"} {
		t.Run(phase, func(t *testing.T) {
			probe, port := reserveRuntimeTestPort(t)
			if err := probe.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "receipts.wal")
			setDurableRuntimeEnv(t, "fresh", path, port)
			t.Setenv("LANTERN_AUTH_MODE", "off")
			t.Setenv("LANTERN_METRICS_ADDR", "")
			t.Setenv("LANTERN_DRAIN_DELAY_SECONDS", "0")
			app, cleanup, err := initializeApp()
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			lifecycle := &appTestSecurityLifecycle{owned: app.security}
			app.security = lifecycle
			failure := errors.New("injected lifecycle failure")
			worker := appTestMetricsWorker{started: make(chan struct{})}
			if phase == "worker-failure" {
				worker.failure = failure
			}
			app.metrics = worker
			if phase == "shutdown-error" {
				lifecycle.stopErr = failure
			}
			ctx, cancel := context.WithCancel(t.Context())
			if phase == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
			}
			defer cancel()
			if phase == "pre-canceled" {
				cancel()
			}
			done := make(chan error, 1)
			go func() { done <- app.Run(ctx) }()
			if phase != "pre-canceled" {
				<-worker.started
			}
			if phase == "normal" || phase == "shutdown-error" {
				cancel()
			}
			select {
			case err = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("App did not join")
			}
			orderly := phase == "normal" || phase == "shutdown-error"
			if orderly && (lifecycle.orderly != 1 || lifecycle.closed != 0) || !orderly && (lifecycle.orderly != 0 || lifecycle.closed != 1) {
				t.Fatal("wrong terminal lifecycle", lifecycle.orderly, lifecycle.closed)
			}
			switch phase {
			case "normal":
				if err != nil {
					t.Fatal(err)
				}
			case "shutdown-error", "worker-failure":
				if !errors.Is(err, failure) {
					t.Fatal("App lost lifecycle error", err)
				}
			case "deadline":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("deadline became success", err)
				}
			case "pre-canceled":
				if !errors.Is(err, context.Canceled) {
					t.Fatal("unstarted app declared normal shutdown", err)
				}
			}
			// Data remains an independent terminal owner, including on sys failure.
			lease, err := mutationlog.AcquireFileWALLease(path)
			if err != nil {
				t.Fatal("App did not close its data runtime", err)
			}
			_ = lease.Close()
		})
	}
}

var _ provider.MetricsServer = appTestMetricsWorker{}

// TestDrainPhase_SigtermDrainsThenReturns verifies that when the parent
// context is cancelled (SIGTERM), drainPhase invokes begin exactly once and
// then holds for the drain delay before returning — i.e. readiness flips
// before the caller cancels serveCtx, and the listeners stay up for the
// window.
func TestDrainPhase_SigtermDrainsThenReturns(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	serveDone := make(chan struct{}) // never fires
	var begun atomic.Int32

	cancel() // simulate SIGTERM already delivered
	start := time.Now()
	drainPhase(parent, serveDone, 40*time.Millisecond, func() { begun.Add(1) })
	elapsed := time.Since(start)

	if got := begun.Load(); got != 1 {
		t.Fatalf("begin must be called exactly once on drain, got %d", got)
	}
	if elapsed < 40*time.Millisecond {
		t.Fatalf("drainPhase must hold for the drain delay, returned after %s", elapsed)
	}
}

// TestDrainPhase_ServerFailureSkipsDrain verifies that when a server
// goroutine fails first (serveDone fires before SIGTERM), drainPhase returns
// immediately WITHOUT calling begin — a failing server should not be held in
// an artificial drain window.
func TestDrainPhase_ServerFailureSkipsDrain(t *testing.T) {
	parent := context.Background() // never cancelled
	serveDone := make(chan struct{})
	close(serveDone) // a server already failed
	var begun atomic.Int32

	start := time.Now()
	drainPhase(parent, serveDone, time.Hour, func() { begun.Add(1) })
	elapsed := time.Since(start)

	if got := begun.Load(); got != 0 {
		t.Fatalf("begin must NOT be called when a server fails first, got %d", got)
	}
	if elapsed > time.Second {
		t.Fatalf("drainPhase must return promptly on server failure, took %s", elapsed)
	}
}

// TestDrainPhase_ZeroDelayDrainsImmediately verifies that with DrainDelay=0
// drainPhase still flips readiness (calls begin) but returns without waiting.
func TestDrainPhase_ZeroDelayDrainsImmediately(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	serveDone := make(chan struct{})
	var begun atomic.Int32

	start := time.Now()
	drainPhase(parent, serveDone, 0, func() { begun.Add(1) })
	elapsed := time.Since(start)

	if got := begun.Load(); got != 1 {
		t.Fatalf("begin must be called once even with zero delay, got %d", got)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("zero-delay drain must return promptly, took %s", elapsed)
	}
}

// TestDrainPhase_ServerFailureDuringDrainCutsWindow verifies that if a server
// dies *during* the drain window, drainPhase stops waiting early.
func TestDrainPhase_ServerFailureDuringDrainCutsWindow(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	serveDone := make(chan struct{})
	var begun atomic.Int32

	go func() {
		time.Sleep(20 * time.Millisecond)
		close(serveDone)
	}()

	start := time.Now()
	drainPhase(parent, serveDone, time.Hour, func() { begun.Add(1) })
	elapsed := time.Since(start)

	if got := begun.Load(); got != 1 {
		t.Fatalf("begin must be called once, got %d", got)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("drain window must be cut short when a server dies, took %s", elapsed)
	}
}
