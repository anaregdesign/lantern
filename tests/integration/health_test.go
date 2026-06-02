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
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

// newInProcessClientWithHealth is like newInProcessClient but additionally
// registers the grpc.health.v1 service so the SDK's Ping helper can be
// exercised end-to-end.
func newInProcessClientWithHealth(t *testing.T, serving bool) (*client.Lantern, func()) {
	t.Helper()

	lis := bufconn.Listen(1 << 16)
	srv := grpc.NewServer()
	svc := service.NewLanternService(cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute))
	pb.RegisterLanternServiceServer(srv, svc)

	hs := provider.NewHealthServer()
	provider.RegisterHealth(srv, hs)
	if serving {
		hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	} else {
		hs.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	}

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

func TestLantern_Ping_Serving(t *testing.T) {
	l, cleanup := newInProcessClientWithHealth(t, true)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := l.Ping(ctx); err != nil {
		t.Fatalf("Ping when SERVING: unexpected error %v", err)
	}
}

func TestLantern_Ping_NotServing(t *testing.T) {
	l, cleanup := newInProcessClientWithHealth(t, false)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := l.Ping(ctx); err == nil {
		t.Fatal("Ping when NOT_SERVING: expected error, got nil")
	}
}
