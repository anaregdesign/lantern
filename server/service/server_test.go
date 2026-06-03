package service_test

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// recordingHealth captures every SetServingStatus call so we can assert
// that Run flips NOT_SERVING after Serve returns, regardless of cause.
type recordingHealth struct {
	mu     sync.Mutex
	events []healthpb.HealthCheckResponse_ServingStatus
}

func (r *recordingHealth) SetServingStatus(_ string, s healthpb.HealthCheckResponse_ServingStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, s)
}

func (r *recordingHealth) last() healthpb.HealthCheckResponse_ServingStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == 0 {
		return healthpb.HealthCheckResponse_UNKNOWN
	}
	return r.events[len(r.events)-1]
}

// brokenListener.Accept returns immediately with a non-temporary error,
// causing grpc.Server.Serve to return without ctx cancellation.
type brokenListener struct{ addr net.Addr }

func (b *brokenListener) Accept() (net.Conn, error) { return nil, errBroken }
func (b *brokenListener) Close() error              { return nil }
func (b *brokenListener) Addr() net.Addr            { return b.addr }

var errBroken = errors.New("listener is broken")

type fakeAddr struct{}

func (fakeAddr) Network() string { return "fake" }
func (fakeAddr) String() string  { return "fake://broken" }

func TestLanternServer_Run_FlipsHealthOnServeReturn(t *testing.T) {
	cache := cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache)
	grpcSrv := grpc.NewServer()
	lis := &brokenListener{addr: fakeAddr{}}
	hs := &recordingHealth{}

	srv := service.NewLanternServer(
		svc, nil, grpcSrv, lis,
		slog.New(slog.NewTextHandler(discardWriter{}, nil)),
		service.LifecycleConfig{GCInterval: time.Hour, ShutdownTimeout: time.Second},
		hs,
		cache,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := srv.Run(ctx)
	if err == nil {
		t.Fatal("Run returned nil error; want listener error")
	}

	if got := hs.last(); got != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Errorf("last health status = %v, want NOT_SERVING", got)
	}
	// Must have transitioned through SERVING first.
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if len(hs.events) < 2 || hs.events[0] != healthpb.HealthCheckResponse_SERVING {
		t.Errorf("expected SERVING then NOT_SERVING, got %v", hs.events)
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
