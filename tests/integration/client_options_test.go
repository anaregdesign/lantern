package integration_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	client "github.com/anaregdesign/lantern/sdks/go"
	pb "github.com/anaregdesign/lantern/sdks/go/gen/graph/v1"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func newBufServer(t *testing.T, lim provider.ValidationLimits, extra ...grpc.UnaryServerInterceptor) func(context.Context, string) (net.Conn, error) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	cache := cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache)
	val := provider.NewValidationInterceptor(lim)

	chain := []grpc.UnaryServerInterceptor{val.UnaryServerInterceptor()}
	chain = append(chain, extra...)
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(chain...))
	pb.RegisterLanternServiceServer(srv, svc)

	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("bufconn serve: %v", err)
		}
	}()

	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})
	return func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }
}

func newOptsClient(t *testing.T, dialer func(context.Context, string) (net.Conn, error), opts ...client.Option) *client.Lantern {
	t.Helper()
	base := []client.Option{
		client.WithTransportCredentials(insecure.NewCredentials()),
		client.WithDialOption(grpc.WithContextDialer(dialer)),
	}
	base = append(base, opts...)
	c, err := client.NewLantern("passthrough://bufnet", base...)
	if err != nil {
		t.Fatalf("NewLantern: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestClient_GetVertex_NotFoundWrapped(t *testing.T) {
	c := newOptsClient(t, newBufServer(t, provider.ValidationLimits{MaxKeyLen: 64, MaxBatchSize: 100}))
	_, err := c.GetVertex(context.Background(), "absent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, client.ErrNotFound) {
		t.Errorf("error %v should wrap ErrNotFound", err)
	}
}

func TestClient_ValidationInterceptor_RejectsLongKey(t *testing.T) {
	c := newOptsClient(t, newBufServer(t, provider.ValidationLimits{MaxKeyLen: 4, MaxBatchSize: 10}))
	err := c.PutVertex(context.Background(), "way-too-long-key", 1, time.Minute)
	if err == nil {
		t.Fatal("expected InvalidArgument, got nil")
	}
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", got)
	}
}

func TestClient_BatchChunking(t *testing.T) {
	// MaxBatchSize=5 on the server would reject an 8-element put. Chunking at
	// 3 keeps every request under the cap.
	dialer := newBufServer(t, provider.ValidationLimits{MaxKeyLen: 32, MaxBatchSize: 5})
	c := newOptsClient(t, dialer, client.WithBatchChunkSize(3))

	ctx := context.Background()
	inputs := make([]client.VertexInput, 0, 8)
	for i := 0; i < 8; i++ {
		inputs = append(inputs, client.VertexInput{
			Key:        fmt.Sprintf("k%d", i),
			Value:      int64(i),
			Expiration: time.Now().Add(time.Minute),
		})
	}
	if err := c.PutVertices(ctx, inputs); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := c.GetVertex(ctx, fmt.Sprintf("k%d", i)); err != nil {
			t.Errorf("missing k%d after chunked put: %v", i, err)
		}
	}
}

// TestClient_RetryOnUnavailable verifies that the default service config
// transparently retries an idempotent RPC after a transient UNAVAILABLE.
func TestClient_RetryOnUnavailable(t *testing.T) {
	var attempts int
	flaky := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if info.FullMethod == "/graph.v1.LanternService/GetVertex" {
			attempts++
			if attempts == 1 {
				return nil, status.Error(codes.Unavailable, "simulated transient")
			}
		}
		return handler(ctx, req)
	}
	dialer := newBufServer(t, provider.ValidationLimits{MaxKeyLen: 64, MaxBatchSize: 10}, flaky)
	c := newOptsClient(t, dialer)

	ctx := context.Background()
	if err := c.PutVertex(ctx, "k", "v", time.Minute); err != nil {
		t.Fatalf("PutVertex: %v", err)
	}
	v, err := c.GetVertex(ctx, "k")
	if err != nil {
		t.Fatalf("GetVertex: %v", err)
	}
	if got, _ := v.StringValue(); got != "v" {
		t.Errorf("StringValue = %q, want %q", got, "v")
	}
	if attempts < 2 {
		t.Errorf("expected retry (attempts >= 2), got %d", attempts)
	}
}

func TestClient_DefaultTimeoutHonoured(t *testing.T) {
	// Server handler that blocks; default timeout must trip.
	blocker := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	dialer := newBufServer(t, provider.ValidationLimits{MaxKeyLen: 64, MaxBatchSize: 10}, blocker)
	// Disable retries to keep the test fast: a retried DeadlineExceeded would
	// burn through the policy budget.
	c := newOptsClient(t, dialer,
		client.WithDefaultTimeout(100*time.Millisecond),
		client.WithDefaultServiceConfig(""),
	)
	start := time.Now()
	_, err := c.GetVertex(context.Background(), "k")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected DeadlineExceeded, got nil")
	}
	if got := status.Code(err); got != codes.DeadlineExceeded {
		t.Errorf("code = %v, want DeadlineExceeded", got)
	}
	if elapsed > time.Second {
		t.Errorf("default timeout did not fire promptly: %v", elapsed)
	}
}

func TestClient_RespectsContextCancel(t *testing.T) {
	c := newOptsClient(t, newBufServer(t, provider.ValidationLimits{MaxKeyLen: 64, MaxBatchSize: 10}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.GetVertex(ctx, "k")
	if err == nil {
		t.Fatal("expected ctx cancel error, got nil")
	}
	if got := status.Code(err); got != codes.Canceled {
		t.Errorf("code = %v, want Canceled", got)
	}
}
