package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/service"
)

// newInProcessClientWithHealth stands up a Connect-on-h2c httptest
// server that mounts both the Lantern service AND a
// connectrpc.com/grpchealth handler. The serving flag drives the
// overall ("") status the handler reports. Mirrors how
// server/provider/health.go + lantern_listener.go wire production.
func newInProcessClientWithHealth(t *testing.T, serving bool) (*client.Lantern, func()) {
	t.Helper()
	cache := cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache)

	// Pre-register the canonical empty service name so the static
	// checker recognises Check(""). The grpchealth handler answers
	// CodeNotFound for any service it has never seen.
	checker := grpchealth.NewStaticChecker("")
	if serving {
		checker.SetStatus("", grpchealth.StatusServing)
	} else {
		checker.SetStatus("", grpchealth.StatusNotServing)
	}

	mux := http.NewServeMux()
	mux.Handle(graphv1connect.NewLanternServiceHandler(
		service.NewLanternServiceConnectHandler(svc),
	))
	mux.Handle(grpchealth.NewHandler(checker))

	srv := httptest.NewUnstartedServer(mux)
	// Enable unencrypted HTTP/2 via the Go 1.24+ Server.Protocols
	// surface (same pattern as helpers_test.go newConnectTestServer).
	protos := new(http.Protocols)
	protos.SetHTTP1(true)
	protos.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = protos
	srv.Start()
	t.Cleanup(srv.Close)

	l, err := client.NewLantern(srv.URL, client.WithHTTPClient(h2cClient()))
	if err != nil {
		t.Fatalf("NewLanternConnect: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, func() {}
}

func TestLantern_Ping_Serving(t *testing.T) {
	l, _ := newInProcessClientWithHealth(t, true)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := l.Ping(ctx); err != nil {
		t.Fatalf("Ping when SERVING: unexpected error %v", err)
	}
}

func TestLantern_Ping_NotServing(t *testing.T) {
	l, _ := newInProcessClientWithHealth(t, false)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := l.Ping(ctx); err == nil {
		t.Fatal("Ping when NOT_SERVING: expected error, got nil")
	}
}

// silenceUnused keeps cosmetic imports referenced when individual
// tests above are commented out during local debugging.
var _ = connect.CodeOf
