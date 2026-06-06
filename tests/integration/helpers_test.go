package integration_test

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers — Connect-on-h2c in-process harness (#350)
// ─────────────────────────────────────────────────────────────────────────────
//
// The integration suite migrated from bufconn + grpc.NewServer to
// httptest + h2c + Connect-Go handlers in #350 (part of #335). Every
// helper here returns a *client.Lantern wired through the Connect
// transport so the tests exercise the production wire path the
// server will use once #347 cuts over.
//
// Why httptest instead of net.Listen: httptest.Server owns the
// listener lifecycle (auto-close on t.Cleanup), picks an ephemeral
// port that never collides with other parallel tests, and exposes
// `.URL` already in the http://host:port form NewLanternConnect
// requires. h2c lets the test work without juggling TLS certs.

// connectTestServer pairs the live LanternService with the
// httptest.Server hosting its Connect handler. Tests that need to
// inspect or mutate the service after construction (e.g. seeding
// vertices via the service's own Backend) hold this aggregate
// directly; tests that only need an SDK client take the *client.Lantern
// returned by the convenience helpers.
type connectTestServer struct {
	svc *service.LanternService
	rep *service.LanternReplicationService
	srv *httptest.Server
	url string
}

// defaultIntegrationValidationLimits matches the historical
// ValidationLimits the legacy bufconn helper used. Keeping the limits
// constant across the suite means tests that depend on a particular
// "request too large" behaviour stay deterministic.
func defaultIntegrationValidationLimits() provider.ValidationLimits {
	return provider.ValidationLimits{
		MaxKeyLen:         256,
		MaxBatchSize:      1024,
		IlluminateMaxStep: 32,
		IlluminateMaxK:    256,
	}
}

// newConnectTestServer stands up an h2c httptest.Server mounting the
// supplied service + optional replication handler with the supplied
// chain of Connect interceptors. Returns the aggregate; tests usually
// consume the convenience wrappers (newInProcessClient,
// newInProcessClientWithInterceptors, ...) rather than this directly.
//
// rep MAY be nil — the replication handler is then not mounted,
// mirroring the gRPC path where replication can be disabled.
func newConnectTestServer(
	t *testing.T,
	svc *service.LanternService,
	rep *service.LanternReplicationService,
	interceptors ...connect.Interceptor,
) *connectTestServer {
	t.Helper()

	var opts []connect.HandlerOption
	if len(interceptors) > 0 {
		opts = []connect.HandlerOption{connect.WithInterceptors(interceptors...)}
	}

	mux := http.NewServeMux()
	mux.Handle(graphv1connect.NewLanternServiceHandler(
		service.NewLanternServiceConnectHandler(svc),
		opts...,
	))
	if rep != nil {
		mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(
			service.NewLanternReplicationServiceConnectHandler(rep),
			opts...,
		))
	}

	srv := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(srv.Close)
	return &connectTestServer{svc: svc, rep: rep, srv: srv, url: srv.URL}
}

// newConnectClientFor wires a *client.Lantern via NewLanternConnect at
// the supplied URL. The constructor is forced to use an h2c-capable
// http.Client because httptest spins up plain HTTP/2-over-cleartext.
// Optional SDK-side options (WithConnectBatchChunkSize, ...) flow
// through.
func newConnectClientFor(t *testing.T, baseURL string, opts ...client.ConnectOption) *client.Lantern {
	t.Helper()
	all := append([]client.ConnectOption{client.WithHTTPClient(h2cClient())}, opts...)
	l, err := client.NewLanternConnect(baseURL, all...)
	if err != nil {
		t.Fatalf("NewLanternConnect: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// h2cClient returns an h2c-flavoured http.Client that matches what
// httptest.Server speaks. Each call returns a fresh client so a test
// that explicitly calls Close on the *client.Lantern does not affect
// any other in-flight client in a parallel test.
func h2cClient() *http.Client {
	return &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
		},
	}
}

// newInProcessClient is the default helper most integration tests
// consume: a fresh GraphCache-backed LanternService, the standard
// validation interceptor, and an h2c-backed Connect *client.Lantern.
//
// The returned cleanup is a no-op (kept in the signature so
// historical call sites stay source-compatible). The httptest.Server
// and the *client.Lantern register their own Cleanup hooks through
// the supplied *testing.T, so the test does not have to remember to
// tear anything down explicitly.
func newInProcessClient(t *testing.T) (*client.Lantern, func()) {
	t.Helper()
	cache := cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache)
	val := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	srv := newConnectTestServer(t, svc, nil, val.ConnectInterceptor())
	return newConnectClientFor(t, srv.url), func() {}
}

// newInProcessClientChunked mirrors newInProcessClient but with a
// custom batch chunk size. Used by batch_error_test.go to deliberately
// trip mid-batch failures.
func newInProcessClientChunked(t *testing.T, chunkSize int) (*client.Lantern, func()) {
	t.Helper()
	cache := cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache)
	val := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	srv := newConnectTestServer(t, svc, nil, val.ConnectInterceptor())
	l := newConnectClientFor(t, srv.url, client.WithConnectBatchChunkSize(chunkSize))
	return l, func() {}
}

// newInProcessClientWithPrefix mirrors newInProcessClient but enables
// the prefix index on the underlying GraphCache so list_under /
// prefix-scan integration tests pass.
func newInProcessClientWithPrefix(t *testing.T) (*client.Lantern, func()) {
	t.Helper()
	cache := cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute)
	cache.EnablePrefixIndex(func(k string) string { return k })
	svc := service.NewLanternService(cache)
	val := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	srv := newConnectTestServer(t, svc, nil, val.ConnectInterceptor())
	return newConnectClientFor(t, srv.url), func() {}
}
