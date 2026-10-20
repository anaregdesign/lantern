//go:build !lantern_test_time

package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/decisiontrace"
	"github.com/anaregdesign/lantern/server/internal/listenerlaunch"
	"github.com/anaregdesign/lantern/server/provider"
)

// Set by the preparation command, and verified without starting an owner.
var offSourceRevision = "unprepared"

// TestOFFConfiguredSourceChild is a dedicated normal-build App child, never a
// public server entry or a configurable time factory. Its parent owns the pipes,
// three-child allocation, readiness, wire checks, cancellation and final wait.
func TestOFFConfiguredSourceChild(t *testing.T) {
	nonce := os.Getenv("OFF_SOURCE_CHILD_NONCE")
	if nonce == "" {
		t.Skip("explicit bounded OFF source supervisor only")
	}
	if !listenerlaunch.ValidNonce(nonce) {
		t.Fatal("invalid source child nonce")
	}
	started := time.Now()
	emit := func(event string, fields map[string]any) {
		if fields == nil {
			fields = map[string]any{}
		}
		fields["event"], fields["nonce"], fields["pid"] = event, nonce, os.Getpid()
		fields["elapsed_ns"] = time.Since(started).Nanoseconds()
		raw, err := json.Marshal(fields)
		if err != nil || len(raw) > listenerlaunch.MaxFrame {
			t.Fatal("invalid source child record")
		}
		if _, err = fmt.Fprintln(os.Stdout, "off_source "+string(raw)); err != nil {
			t.Fatal("source child output failed")
		}
	}
	if os.Getenv("OFF_SOURCE_CHILD_IDENTITY") == "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(raw)
		info, ok := debug.ReadBuildInfo()
		if !ok {
			t.Fatal("missing build identity")
		}
		emit("identity", map[string]any{"revision": offSourceRevision, "binary_sha256": hex.EncodeToString(digest[:]), "go_version": info.GoVersion, "goos": runtime.GOOS, "goarch": runtime.GOARCH, "normal_build": true})
		return // No listener, App, native sampler or source owner is constructed.
	}
	ctx, cancel := signal.NotifyContext(t.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	watchdog := time.AfterFunc(110*time.Second, cancel)
	defer watchdog.Stop()
	hardStop := time.AfterFunc(115*time.Second, func() { os.Exit(3) })
	defer hardStop.Stop()
	interrupt := context.AfterFunc(ctx, func() { _ = os.Stdin.Close() })
	defer interrupt()
	input := bufio.NewReaderSize(os.Stdin, listenerlaunch.MaxFrame)
	request, err := listenerlaunch.Read(input)
	if err != nil || !request.Control("reserve", nonce) || !request.Private {
		t.Fatal("invalid source reservation")
	}
	launch, err := listenerlaunch.Reserve(ctx, true)
	if err != nil {
		t.Fatal("source listener reservation failed")
	}
	defer launch.Close()
	public, peer := launch.Addresses()
	emit("reserved", map[string]any{"public": public, "peer": peer})
	configuration, err := listenerlaunch.Read(input)
	if err != nil || !configuration.Configuration(nonce) {
		t.Fatal("invalid source configuration")
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "LANTERN_") {
			_ = os.Unsetenv(key)
		}
	}
	for _, entry := range configuration.Environment {
		key, value, _ := strings.Cut(entry, "=")
		if err := os.Setenv(key, value); err != nil {
			t.Fatal("source environment installation failed")
		}
	}
	if os.Getenv("LANTERN_AUTH_MODE") != "off" || offSourceRevision == "unprepared" {
		t.Fatal("source child requires prepared OFF composition")
	}
	app, cleanup, err := initializeAppWithListeners(ctx, launch)
	if err != nil {
		decisiontrace.WriteStartupFailure(os.Stderr, err)
		emit("failure", map[string]any{"stage": "construction", "counters_available": false})
		t.Fatal("OFF App construction failed") // Wire joins partial ownership.
	}
	defer cleanup()
	security, ok := app.security.(*provider.SecurityRuntime)
	identity, peerOK := app.peerIdentity.(*provider.PeerIdentityRuntime)
	if !ok || !peerOK || security.Mode() != "off" || launch.Adopted() != nil {
		t.Fatal("source App ownership/adoption mismatch")
	}
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	emit("adopted", map[string]any{"mode": security.Mode(), "revision": offSourceRevision, "goos": runtime.GOOS, "goarch": runtime.GOARCH, "initializers": 1})
	stopped := false
	for !stopped {
		command, readErr := listenerlaunch.Read(input)
		if readErr != nil {
			cancel()
			<-done
			t.Fatal("source parent ended without stop")
		}
		switch {
		case command.Control("probe", nonce):
			probeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
			probeOK := offSourcePrivateProbe(probeCtx, identity, app.cfg.PeerPlane.ListenAddress)
			stop()
			emit("probe", map[string]any{"ok": probeOK})
		case command.Control("stop", nonce):
			stopped = true
		default:
			cancel()
			<-done
			t.Fatal("invalid source child command")
		}
	}
	emit("stop_requested", nil)
	cancel()
	runErr := <-done // App joins workers, then peer/time, security and data.
	emit("app_returned", map[string]any{"ok": runErr == nil})
	cleanup() // Preserve the production idempotent Wire listener cleanup too.
	emit("cleanup_returned", nil)
	first, second := security.TimeAcquisitionCounts(), security.TimeAcquisitionCounts()
	emit("final", map[string]any{"mode": security.Mode(), "counters": offSourceCounterFields(first), "repeat": offSourceCounterFields(second), "closed_refused": identity.CheckWorkload(context.Background()) != nil, "app_ok": runErr == nil})
	if runErr != nil {
		t.Fatal("OFF App shutdown failed")
	}
}

func offSourceCounterFields(c decisiontrace.AcquisitionCounts) map[string]any {
	return map[string]any{"attempts": c.Attempts, "writes": c.Writes, "installations": c.Installations, "in_flight": c.InFlight, "failed": c.Failed, "denied": c.Denied}
}
