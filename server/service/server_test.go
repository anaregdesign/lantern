package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
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
	if err := s.gracefulShutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("shutdown timeout was hidden from App", err)
	}
	elapsed := time.Since(start)

	if elapsed < s.shutdownTimeout {
		t.Fatalf("gracefulShutdown returned before timeout: %v < %v", elapsed, s.shutdownTimeout)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("gracefulShutdown blocked far past timeout: %v", elapsed)
	}
}

func TestLanternServerRunJoinsHandlersAndWatcher(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-release
	})}
	t.Cleanup(func() { _ = server.Close() })
	watcher := &serverJoinWatcher{stopped: make(chan struct{}), release: make(chan struct{})}
	s := &LanternServer{server: server, listener: listener, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), shutdownTimeout: 3 * time.Second, watcher: watcher}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	request := make(chan struct{})
	go func() {
		defer close(request)
		response, _ := http.Get("http://" + listener.Addr().String())
		if response != nil {
			_ = response.Body.Close()
		}
	}()
	<-entered
	cancel()
	<-watcher.stopped
	select {
	case err := <-done:
		t.Fatal("Serve return bypassed shutdown join", err)
	default:
	}
	close(release)
	<-request
	select {
	case err := <-done:
		t.Fatal("runtime returned before owned watcher joined", err)
	default:
	}
	close(watcher.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("drained runtime did not join")
	}
}

type serverJoinWatcher struct{ stopped, release chan struct{} }

func (w *serverJoinWatcher) Watch(ctx context.Context, _ time.Duration) {
	<-ctx.Done()
	close(w.stopped)
	<-w.release
}
