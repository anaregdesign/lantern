package integration_test

import (
	"context"
	"net"
	"testing"
	"time"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	client "github.com/anaregdesign/lantern/sdks/go"
	pb "github.com/anaregdesign/lantern/sdks/go/gen/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// newInProcessClient wires a Lantern client to an in-process gRPC server
// backed by a real LanternService, so we exercise the actual wire format.
func newInProcessClient(t *testing.T) (*client.Lantern, func()) {
	t.Helper()

	lis := bufconn.Listen(1 << 16)
	srv := grpc.NewServer()
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

func TestLantern_PutGetDeleteVertex(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := l.PutVertex(ctx, "k", "v", time.Minute); err != nil {
		t.Fatalf("PutVertex: %v", err)
	}

	v, err := l.GetVertex(ctx, "k")
	if err != nil {
		t.Fatalf("GetVertex: %v", err)
	}
	got, err := v.StringValue()
	if err != nil {
		t.Fatalf("StringValue: %v", err)
	}
	if got != "v" {
		t.Errorf("StringValue = %q, want \"v\"", got)
	}

	if _, err := l.DeleteVertex(ctx, "k"); err != nil {
		t.Fatalf("DeleteVertex: %v", err)
	}
	if _, err := l.GetVertex(ctx, "k"); err == nil {
		t.Error("expected error after DeleteVertex, got nil")
	}
}

func TestLantern_AddPutDeleteEdge(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := l.AddEdge(ctx, "a", "b", 1.5, time.Minute); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	e, err := l.GetEdge(ctx, "a", "b")
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if e.Weight != 1.5 {
		t.Errorf("weight = %v, want 1.5", e.Weight)
	}

	// PutEdge replaces.
	if err := l.PutEdge(ctx, "a", "b", 9, time.Minute); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
	e, err = l.GetEdge(ctx, "a", "b")
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if e.Weight != 9 {
		t.Errorf("weight after PutEdge = %v, want 9", e.Weight)
	}

	if _, err := l.DeleteEdge(ctx, "a", "b"); err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}
	if _, err := l.GetEdge(ctx, "a", "b"); err == nil {
		t.Error("expected error after DeleteEdge, got nil")
	}
}

func TestLantern_Illuminate(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, k := range []string{"a", "b", "c"} {
		if err := l.PutVertex(ctx, k, k, time.Minute); err != nil {
			t.Fatalf("PutVertex %s: %v", k, err)
		}
	}
	if err := l.PutEdge(ctx, "a", "b", 1, time.Minute); err != nil {
		t.Fatalf("PutEdge a->b: %v", err)
	}
	if err := l.PutEdge(ctx, "b", "c", 1, time.Minute); err != nil {
		t.Fatalf("PutEdge b->c: %v", err)
	}

	g, err := l.Illuminate(ctx, "a", 3, 10, false)
	if err != nil {
		t.Fatalf("Illuminate: %v", err)
	}
	for _, want := range []string{"a", "b", "c"} {
		if _, ok := g.Vertices[want]; !ok {
			t.Errorf("Illuminate result missing vertex %q (got %v)", want, g.Vertices)
		}
	}
	if _, ok := g.Edges["a"]["b"]; !ok {
		t.Errorf("Illuminate missing edge a->b (got %v)", g.Edges)
	}
}
