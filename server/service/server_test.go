package service

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestLanternServer_gracefulShutdown_TimeoutForcesClose pins an
// in-flight HTTP request open so http.Server.Shutdown cannot drain,
// then asserts gracefulShutdown honours ShutdownTimeout by escalating
// to Close after the deadline elapses.
//
// Verified behaviour: gracefulShutdown returns within
// (ShutdownTimeout, 5s) — i.e. it waited the full timeout (proving
// Shutdown was called) but did not block forever (proving Close was
// the fallback).
func TestLanternServer_gracefulShutdown_TimeoutForcesClose(t *testing.T) {
	released := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		// Block until either the test releases the handler (clean
		// path) or the underlying connection is force-closed
		// (gracefulShutdown's Close branch cancels r.Context).
		select {
		case <-released:
		case <-r.Context().Done():
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(func() {
		close(released)
		ts.Close()
	})

	// Fire a slow request in the background. The handler blocks, so
	// http.Server.Shutdown will see one active connection until the
	// deadline trips.
	reqStarted := make(chan struct{})
	go func() {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/slow", nil)
		close(reqStarted)
		// Use the test server's client so the connection is tracked
		// by ts.Server (the same server LanternServer.Shutdown will
		// drain).
		resp, _ := ts.Client().Do(req)
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	<-reqStarted
	// Give the handler a moment to enter; httptest.Server's
	// connection-active counter increments inside ServeHTTP. 50ms is
	// plenty for the goroutine + TCP handshake on loopback.
	time.Sleep(50 * time.Millisecond)

	s := &LanternServer{
		server:          ts.Config, // *http.Server backing the test server
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		shutdownTimeout: 200 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // gracefulShutdown waits on ctx.Done, so pre-cancel.

	start := time.Now()
	s.gracefulShutdown(ctx)
	elapsed := time.Since(start)

	if elapsed < s.shutdownTimeout {
		t.Fatalf("gracefulShutdown returned before timeout: %v < %v", elapsed, s.shutdownTimeout)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("gracefulShutdown blocked far past timeout: %v", elapsed)
	}
}
