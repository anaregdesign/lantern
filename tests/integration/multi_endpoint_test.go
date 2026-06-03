package integration_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/grpc"
)

type countingServer struct {
	srv     *grpc.Server
	lis     net.Listener
	addr    string
	calls   atomic.Int64
	stopped atomic.Bool
}

func startCountingServer(t *testing.T) *countingServer {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	cs := &countingServer{lis: lis, addr: lis.Addr().String()}

	counter := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		cs.calls.Add(1)
		return handler(ctx, req)
	}
	vi := provider.NewValidationInterceptor(provider.ValidationLimits{
		MaxKeyLen:         256,
		MaxBatchSize:      1024,
		IlluminateMaxStep: 32,
		IlluminateMaxK:    256,
	})
	cs.srv = grpc.NewServer(grpc.ChainUnaryInterceptor(counter, vi.UnaryServerInterceptor()))
	svc := service.NewLanternService(cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute))
	pb.RegisterLanternServiceServer(cs.srv, svc)
	go func() {
		_ = cs.srv.Serve(lis)
	}()
	return cs
}

func (cs *countingServer) stop() {
	if cs.stopped.Swap(true) {
		return
	}
	cs.srv.Stop()
	_ = cs.lis.Close()
}

// TestNewLanternWithEndpoints_RoundRobin verifies that the explicit-endpoint
// constructor (a) dials every supplied address, and (b) actually spreads
// load across them via round_robin. Both server-side counters must observe
// at least one call after a small batch of RPCs.
func TestNewLanternWithEndpoints_RoundRobin(t *testing.T) {
	a := startCountingServer(t)
	defer a.stop()
	b := startCountingServer(t)
	defer b.stop()

	l, err := client.NewLanternWithEndpoints([]string{a.addr, b.addr})
	if err != nil {
		t.Fatalf("NewLanternWithEndpoints: %v", err)
	}
	defer func() { _ = l.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Warm up — first RPC triggers connect to every subconn before
	// round_robin starts handing out picks.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := l.PutVertex(ctx, "warmup", "v", time.Minute); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("warmup PutVertex never succeeded: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	const n = 50
	for i := 0; i < n; i++ {
		if err := l.PutVertex(ctx, "k", "v", time.Minute); err != nil {
			t.Fatalf("PutVertex[%d]: %v", i, err)
		}
	}

	// Allow a short window for round_robin to converge across both subconns.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a.calls.Load() > 0 && b.calls.Load() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if a.calls.Load() == 0 || b.calls.Load() == 0 {
		t.Fatalf("expected both backends to receive RPCs; got a=%d b=%d", a.calls.Load(), b.calls.Load())
	}
}

// TestNewLanternWithEndpoints_FailoverOnNodeLoss kills one backend and
// verifies that subsequent RPCs continue to succeed against the surviving
// node thanks to round_robin re-picking + the default retry policy on
// UNAVAILABLE.
func TestNewLanternWithEndpoints_FailoverOnNodeLoss(t *testing.T) {
	a := startCountingServer(t)
	defer a.stop()
	b := startCountingServer(t)
	defer b.stop()

	l, err := client.NewLanternWithEndpoints([]string{a.addr, b.addr})
	if err != nil {
		t.Fatalf("NewLanternWithEndpoints: %v", err)
	}
	defer func() { _ = l.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Establish both subconns first.
	for i := 0; i < 20; i++ {
		if err := l.PutVertex(ctx, "warmup", "v", time.Minute); err != nil {
			// Tolerate connect race on the first iterations.
			if i > 5 {
				t.Fatalf("warmup[%d]: %v", i, err)
			}
		}
	}

	// Drop one backend.
	b.stop()

	// Wait briefly for the balancer to mark the dead subconn as
	// TransientFailure. Until that happens round_robin may still hand out
	// a stale pick that fails with UNAVAILABLE on the in-flight RPC.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := l.PutVertex(ctx, "post-failover", "v", time.Minute); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// All subsequent calls must succeed on the survivor.
	const n = 30
	for i := 0; i < n; i++ {
		if err := l.PutVertex(ctx, "post-failover", "v", time.Minute); err != nil {
			t.Fatalf("PutVertex after failover [%d]: %v", i, err)
		}
	}
	if a.calls.Load() == 0 {
		t.Fatalf("expected survivor backend to receive RPCs after failover; got 0")
	}
}

// TestNewLanternWithEndpoints_RejectsEmpty guards against accidentally
// constructing a client that would dial nothing.
func TestNewLanternWithEndpoints_RejectsEmpty(t *testing.T) {
	if _, err := client.NewLanternWithEndpoints(nil); err == nil {
		t.Fatalf("expected error for nil endpoints")
	}
	if _, err := client.NewLanternWithEndpoints([]string{"  "}); err == nil {
		t.Fatalf("expected error for blank endpoint")
	}
}
