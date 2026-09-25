package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/provider"
)

func TestAppRunClosesMutationLogOnRequiredRestoreFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	log := mutationlog.New(mutationlog.Options{})
	runtime := &provider.MutationLogRuntime{Log: log}
	app := &App{
		backupper:   backup.New(nil, backup.Config{RestoreOnStart: true, Dir: path}, nil, nil),
		restoreReq:  true,
		mutationLog: runtime,
	}
	if err := app.Run(context.Background()); err == nil {
		t.Fatal("required restore failure did not stop startup")
	}
	if _, err := log.Append(&pb.Mutation{}, hlc.Timestamp{}); !errors.Is(err, mutationlog.ErrClosed) {
		t.Fatalf("Log after failed startup = %v, want closed", err)
	}
}

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
