package service_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/client"
	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/gen/go/graph/v1"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// TestIntegration_FullMiddlewareChain spins up a real gRPC server with the
// full provider middleware stack (validation + rate-limit) and drives it
// through the public client SDK. It is the closest thing to an end-to-end
// smoke test we have without hitting the wire.
func TestIntegration_FullMiddlewareChain(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	cache := cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache)
	val := provider.NewValidationInterceptor(provider.ValidationLimits{
		MaxKeyLen:         32,
		MaxBatchSize:      5,
		IlluminateMaxStep: 4,
		IlluminateMaxK:    8,
	})
	// 2 rps / burst 2 — generous enough for the happy path, tight enough
	// that a burst of 10 quickly exhausts the bucket.
	rl := provider.NewRateLimitInterceptor(2, 2)

	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(
		val.UnaryServerInterceptor(),
		rl.UnaryServerInterceptor(),
	))
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

	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
	c, err := client.NewLantern("passthrough://bufnet",
		client.WithTransportCredentials(insecure.NewCredentials()),
		client.WithDialOption(grpc.WithContextDialer(dialer)),
		// Disable the SDK's retry policy so we can observe rate limiting
		// directly without it being masked by retry-then-succeed.
		client.WithDefaultServiceConfig(""),
	)
	if err != nil {
		t.Fatalf("NewLantern: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("happy path round-trip", func(t *testing.T) {
		if err := c.PutVertex(ctx, "k", "hello", time.Minute); err != nil {
			t.Fatalf("PutVertex: %v", err)
		}
		v, err := c.GetVertex(ctx, "k")
		if err != nil {
			t.Fatalf("GetVertex: %v", err)
		}
		if got, _ := v.StringValue(); got != "hello" {
			t.Errorf("StringValue = %q, want %q", got, "hello")
		}
	})

	t.Run("validation rejects oversize batch", func(t *testing.T) {
		inputs := make([]client.VertexInput, 0, 6)
		for i := 0; i < 6; i++ {
			inputs = append(inputs, client.VertexInput{
				Key:        string(rune('a' + i)),
				Value:      int64(i),
				Expiration: time.Now().Add(time.Minute),
			})
		}
		// Force a single oversized request by raising the SDK chunk size.
		bigClient, err := client.NewLantern("passthrough://bufnet",
			client.WithTransportCredentials(insecure.NewCredentials()),
			client.WithDialOption(grpc.WithContextDialer(dialer)),
			client.WithDefaultServiceConfig(""),
			client.WithBatchChunkSize(100),
		)
		if err != nil {
			t.Fatalf("NewLantern: %v", err)
		}
		defer func() { _ = bigClient.Close() }()
		err = bigClient.PutVertices(ctx, inputs)
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("PutVertices(6) code = %v, want InvalidArgument", status.Code(err))
		}
	})

	t.Run("illuminate caps enforced", func(t *testing.T) {
		_, err := c.Illuminate(ctx, "k", 99, 1, false)
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("Illuminate step=99 code = %v, want InvalidArgument", status.Code(err))
		}
	})

	t.Run("rate limiter trips under burst", func(t *testing.T) {
		// Hammer a fresh client; SDK retries are disabled so a tripped
		// limiter surfaces directly.
		var hit bool
		for i := 0; i < 20; i++ {
			err := c.PutVertex(ctx, "rl", int64(i), time.Minute)
			if status.Code(err) == codes.ResourceExhausted {
				hit = true
				break
			}
		}
		if !hit {
			t.Error("expected at least one ResourceExhausted within 20 rapid calls")
		}
	})
}
