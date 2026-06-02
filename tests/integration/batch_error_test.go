package integration_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// newInProcessClientChunked is like newInProcessClient but lets the test
// force a custom batchChunkSize so partial-write scenarios can be exercised
// with a small input.
func newInProcessClientChunked(t *testing.T, chunkSize int) (*client.Lantern, func()) {
	t.Helper()

	lis := bufconn.Listen(1 << 16)
	vi := provider.NewValidationInterceptor(provider.ValidationLimits{
		MaxKeyLen:         256,
		MaxBatchSize:      1024,
		IlluminateMaxStep: 32,
		IlluminateMaxK:    256,
	})
	srv := grpc.NewServer(grpc.UnaryInterceptor(vi.UnaryServerInterceptor()))
	svc := service.NewLanternService(cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute))
	pb.RegisterLanternServiceServer(srv, svc)

	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("grpc Serve returned: %v", err)
		}
	}()

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}
	l, err := client.NewLantern(
		"passthrough://bufconn",
		client.WithTransportCredentials(insecure.NewCredentials()),
		client.WithDialOption(grpc.WithContextDialer(dialer)),
		client.WithBatchChunkSize(chunkSize),
	)
	if err != nil {
		t.Fatalf("NewLantern: %v", err)
	}

	cleanup := func() {
		_ = l.Close()
		srv.Stop()
		_ = lis.Close()
	}
	return l, cleanup
}

// TestBatchError_PartialWrite verifies that when a later chunk fails, the
// SDK returns a *BatchError whose Written field reflects fully-committed
// prior chunks, and that the wrapped error still satisfies errors.Is for the
// underlying sentinel.
func TestBatchError_PartialWrite(t *testing.T) {
	l, cleanup := newInProcessClientChunked(t, 2)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	exp := time.Now().Add(time.Minute)
	inputs := []client.VertexInput{
		{Key: "ok-1", Value: "v", Expiration: exp},
		{Key: "ok-2", Value: "v", Expiration: exp},
		{Key: "", Value: "v", Expiration: exp}, // empty key trips the server-side validator
		{Key: "ok-4", Value: "v", Expiration: exp},
	}
	err := l.PutVertices(ctx, inputs)
	if err == nil {
		t.Fatal("PutVertices: expected BatchError, got nil")
	}

	var be *client.BatchError
	if !errors.As(err, &be) {
		t.Fatalf("PutVertices: expected *BatchError, got %T: %v", err, err)
	}
	if be.Written != 2 {
		t.Errorf("BatchError.Written = %d, want 2 (first chunk committed)", be.Written)
	}
	if !errors.Is(err, client.ErrInvalidArgument) {
		t.Errorf("expected errors.Is(err, ErrInvalidArgument) to be true; err = %v", err)
	}

	// Confirm the first chunk did commit.
	v, gerr := l.GetVertex(ctx, "ok-1")
	if gerr != nil || v == nil {
		t.Errorf("ok-1 should exist after partial write; v=%v err=%v", v, gerr)
	}
}
