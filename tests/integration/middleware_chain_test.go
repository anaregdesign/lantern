package integration_test

import (
	"context"
	"net"
	"testing"
	"time"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
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
		_, err := c.Illuminate(ctx, "k", client.WithStep(99), client.WithK(1))
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

// TestIntegration_BatchDeletes exercises the DeleteVertices and DeleteEdges
// RPCs end-to-end through bufconn. It uses its own server with no rate
// limiter so the assertions aren't sensitive to token bucket state from
// other tests in this file.
func TestIntegration_BatchDeletes(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	cache := cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache)
	val := provider.NewValidationInterceptor(provider.ValidationLimits{
		MaxKeyLen:         32,
		MaxBatchSize:      5,
		IlluminateMaxStep: 4,
		IlluminateMaxK:    8,
	})

	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(val.UnaryServerInterceptor()))
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
		client.WithDefaultServiceConfig(""),
	)
	if err != nil {
		t.Fatalf("NewLantern: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("delete vertices round-trip", func(t *testing.T) {
		keys := []string{"d1", "d2", "d3"}
		for _, k := range keys {
			if err := c.PutVertex(ctx, k, int64(1), time.Minute); err != nil {
				t.Fatalf("PutVertex(%s): %v", k, err)
			}
		}
		if err := c.DeleteVertices(ctx, keys); err != nil {
			t.Fatalf("DeleteVertices: %v", err)
		}
		for _, k := range keys {
			if _, err := c.GetVertex(ctx, k); status.Code(err) != codes.NotFound {
				t.Errorf("GetVertex(%s) after delete: code = %v, want NotFound", k, status.Code(err))
			}
		}
	})

	t.Run("delete edges round-trip", func(t *testing.T) {
		for _, k := range []string{"ea", "eb", "ex"} {
			if err := c.PutVertex(ctx, k, int64(0), time.Minute); err != nil {
				t.Fatalf("PutVertex(%s): %v", k, err)
			}
		}
		if err := c.PutEdge(ctx, "ea", "eb", 1.0, time.Minute); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
		if err := c.PutEdge(ctx, "ea", "ex", 1.0, time.Minute); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
		refs := []client.EdgeRef{{Tail: "ea", Head: "eb"}, {Tail: "ea", Head: "ex"}}
		if err := c.DeleteEdges(ctx, refs); err != nil {
			t.Fatalf("DeleteEdges: %v", err)
		}
		for _, r := range refs {
			if _, err := c.GetEdge(ctx, r.Tail, r.Head); status.Code(err) != codes.NotFound {
				t.Errorf("GetEdge(%s,%s) after delete: code = %v, want NotFound", r.Tail, r.Head, status.Code(err))
			}
		}
	})

	t.Run("delete vertices empty is no-op", func(t *testing.T) {
		if err := c.DeleteVertices(ctx, nil); err != nil {
			t.Errorf("DeleteVertices(nil): %v", err)
		}
	})

	t.Run("delete edges empty is no-op", func(t *testing.T) {
		if err := c.DeleteEdges(ctx, nil); err != nil {
			t.Errorf("DeleteEdges(nil): %v", err)
		}
	})

	t.Run("validation rejects oversize delete vertices", func(t *testing.T) {
		// Bypass SDK auto-chunking by raising chunk size, so the server
		// sees a single >MaxBatchSize request.
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
		keys := make([]string, 6)
		for i := range keys {
			keys[i] = string(rune('a' + i))
		}
		if err := bigClient.DeleteVertices(ctx, keys); status.Code(err) != codes.InvalidArgument {
			t.Errorf("DeleteVertices(6) code = %v, want InvalidArgument", status.Code(err))
		}
	})

	t.Run("validation rejects oversize delete edges", func(t *testing.T) {
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
		refs := make([]client.EdgeRef, 6)
		for i := range refs {
			refs[i] = client.EdgeRef{Tail: string(rune('a' + i)), Head: "z"}
		}
		if err := bigClient.DeleteEdges(ctx, refs); status.Code(err) != codes.InvalidArgument {
			t.Errorf("DeleteEdges(6) code = %v, want InvalidArgument", status.Code(err))
		}
	})
}
