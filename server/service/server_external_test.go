package service_test

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"connectrpc.com/grpchealth"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
)

// recordingHealth captures every SetServingStatus call so we can assert
// that Run flips NOT_SERVING after Serve returns, regardless of cause.
type recordingHealth struct {
	mu     sync.Mutex
	events []grpchealth.Status
}

func (r *recordingHealth) SetServingStatus(_ string, s grpchealth.Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, s)
}

func (r *recordingHealth) last() grpchealth.Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == 0 {
		return grpchealth.StatusUnknown
	}
	return r.events[len(r.events)-1]
}

// brokenListener.Accept returns immediately with a non-temporary error,
// causing http.Server.Serve to return without ctx cancellation.
type brokenListener struct{ addr net.Addr }

func (b *brokenListener) Accept() (net.Conn, error) { return nil, errBroken }
func (b *brokenListener) Close() error              { return nil }
func (b *brokenListener) Addr() net.Addr            { return b.addr }

var errBroken = errors.New("listener is broken")

type fakeAddr struct{}

func (fakeAddr) Network() string { return "fake" }
func (fakeAddr) String() string  { return "fake://broken" }

// fakeListenerWrapper satisfies service.Listener for tests by composing
// a *http.Server + net.Listener directly. Production wiring uses
// *provider.LanternListener which exposes the same surface.
type fakeListenerWrapper struct {
	server *http.Server
	lis    net.Listener
}

func (f *fakeListenerWrapper) Server() *http.Server { return f.server }
func (f *fakeListenerWrapper) Listener() net.Listener {
	return f.lis
}
func (f *fakeListenerWrapper) TLSEnabled() bool { return false }

// noopWatcher satisfies service.Watcher without touching the cache.
type noopWatcher struct{}

func (noopWatcher) Watch(ctx context.Context, _ time.Duration) { <-ctx.Done() }

func TestLanternServer_Run_FlipsHealthOnServeReturn(t *testing.T) {
	cache := cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache)
	httpSrv := &http.Server{Handler: http.NewServeMux()}
	lis := &brokenListener{addr: fakeAddr{}}
	hs := &recordingHealth{}

	srv := service.NewLanternServer(
		&fakeListenerWrapper{server: httpSrv, lis: lis},
		slog.New(slog.NewTextHandler(discardWriter{}, nil)),
		service.LifecycleConfig{GCInterval: time.Hour, ShutdownTimeout: time.Second},
		hs,
		noopWatcher{},
		svc,
		nil,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := srv.Run(ctx)
	if err == nil {
		t.Fatal("Run returned nil error; want listener error")
	}

	if got := hs.last(); got != grpchealth.StatusNotServing {
		t.Errorf("last health status = %v, want NOT_SERVING", got)
	}
	// Must have transitioned through SERVING first.
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if len(hs.events) < 2 || hs.events[0] != grpchealth.StatusServing {
		t.Errorf("expected SERVING then NOT_SERVING, got %v", hs.events)
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
