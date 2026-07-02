// Package provider: lantern_listener.go owns the primary :6380
// listener. A single http.Server bound to LANTERN_PORT accepts all
// three Connect protocols (Connect, gRPC, gRPC-Web) over h2c — exactly
// what graphv1connect.NewLanternServiceHandler advertises — and adds
// two addon handlers so existing infra continues to work:
//
//   - grpchealth: serves grpc.health.v1.Health (consumed by
//     grpc-health-probe, Kubernetes startup/liveness probes, grpcurl)
//   - grpcreflect: serves grpc.reflection.v1/v1alpha1.ServerReflection
//     (consumed by grpcurl -plaintext, Postman, BloomRPC) when
//     LANTERN_REFLECTION=true.
//
// h2c is enabled via the Go 1.24+ http.Server.Protocols field
// (SetUnencryptedHTTP2) rather than the deprecated
// golang.org/x/net/http2/h2c.NewHandler wrapper. SA1019-clean and
// removes the x/net/http2/h2c dependency from this listener.
package provider

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"connectrpc.com/grpcreflect"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/service"
)

// connectHandlerOptions assembles the connect.HandlerOption chain
// shared by every mounted Connect handler. Order matters (#850):
// logging and metrics wrap everything so even rejected calls are
// observable; the rate limiter runs BEFORE auth so an unauthenticated
// flood is shed by the cheaper token-bucket check before paying even a
// constant-time compare; auth runs before validation so no validation
// CPU (batch walks, key checks) is spent on unauthenticated requests;
// slow-rpc fires last so the duration captures the full chain.
// (Pre-#850 the chain ran validation before rate-limit so a malformed
// request never burned a token; with auth in the middle the
// security-first ordering wins — a malformed request from an
// AUTHENTICATED client now consumes a token, which is the cheaper
// trade.)
//
// Nil entries are skipped so call sites with a disabled component
// (e.g. RateLimitConfig.RPS<=0 → rl.lim==nil; SlowRPCThreshold==0 →
// slow.Enabled()==false; auth.Enabled()==false when
// LANTERN_AUTH_TOKENS is unset) don't need to special-case
// construction.
//
// connect.WithRecover catches panics in handlers and returns
// CodeInternal — the equivalent of the grpc-middleware recovery
// interceptor.
func connectHandlerOptions(
	val *ValidationInterceptor,
	rl *RateLimitInterceptor,
	auth *AuthInterceptor,
	log *LoggingInterceptor,
	met *PrometheusInterceptor,
	slow *SlowRPCInterceptor,
	logger *slog.Logger,
) []connect.HandlerOption {
	var ints []connect.Interceptor
	if log != nil {
		ints = append(ints, log.ConnectInterceptor())
	}
	if met != nil {
		ints = append(ints, met.ConnectInterceptor())
	}
	if rl != nil && rl.lim != nil {
		ints = append(ints, rl.ConnectInterceptor())
	}
	if auth != nil && auth.Enabled() {
		ints = append(ints, auth)
	}
	if val != nil {
		ints = append(ints, val.ConnectInterceptor())
	}
	if slow.Enabled() {
		ints = append(ints, slow.ConnectInterceptor())
	}
	opts := []connect.HandlerOption{
		connect.WithRecover(func(ctx context.Context, _ connect.Spec, _ http.Header, p any) error {
			logger.ErrorContext(ctx, "connect handler panic", slog.Any("panic", p))
			return connect.NewError(connect.CodeInternal, errors.New("internal server error"))
		}),
	}
	if len(ints) > 0 {
		opts = append(opts, connect.WithInterceptors(ints...))
	}
	return opts
}

// LanternListener is the primary :6380 listener. NewLanternListener
// returns an *http.Server already configured to speak Connect over
// h2c (and TLS when configured); LanternServer.Run drives its
// lifecycle.
//
// The struct exists so wire can inject ONE thing into LanternServer
// while the constructor still composes the mux, TLS, and protocol
// negotiation in one place.
type LanternListener struct {
	server   *http.Server
	listener net.Listener
	tls      bool
}

// Server returns the underlying *http.Server so LanternServer can
// call Serve / ServeTLS / Shutdown on it. Exposed (rather than
// inlining the lifecycle here) so service.LanternServer keeps owning
// the health-flip + cache-GC + graceful-stop orchestration without
// importing the provider package.
func (l *LanternListener) Server() *http.Server { return l.server }

// Listener returns the wire-injected net.Listener bound by NewListener.
// Tests can swap it via wire.Bind(new(net.Listener), new(*bufconn.Listener))
// to drive the server over bufconn without a real port.
func (l *LanternListener) Listener() net.Listener { return l.listener }

// Addr is the listen address (host:port) the underlying listener is bound
// to. Used by LanternServer for the "lantern server starting" slog line so
// operators see a stable field across restarts.
func (l *LanternListener) Addr() string {
	if l.listener == nil {
		return l.server.Addr
	}
	return l.listener.Addr().String()
}

// TLSEnabled reports whether the listener is wired with a
// *tls.Config. LanternServer picks ServeTLS over Serve when true.
func (l *LanternListener) TLSEnabled() bool { return l.tls }

// NewLanternListener mounts both Lantern service handlers, the
// gRPC-Health-v1 surface, and (when enabled) the gRPC reflection
// surface on a single http.Server. The net.Listener is wire-injected
// (so tests can substitute bufconn) and consumed by LanternServer.Run.
//
//   - Net is unused for the bind itself (lis owns that) but supplies
//     per-RPC message size caps. The Connect handlers honour these
//     implicitly via http2 default frame caps; they remain on Config
//     for documentation continuity and are slated for explicit wiring
//     in #342.
//   - tlsCfg switches Serve → ServeTLS.
//   - svc / rep are the wire bindings to the in-process services.
//   - val / rl / log / met / slow are the Connect interceptors.
//   - hc is the gRPC-Health-v1 implementation served at
//     /grpc.health.v1.Health/.
//   - cors wraps the mux with the CORS middleware (no-op when
//     LANTERN_CORS_ALLOWED_ORIGINS is empty).
//   - obs.EnableReflection mounts grpcreflect when true (default
//     true).
//
// h2c is enabled via http.Server.Protocols.SetUnencryptedHTTP2()
// (Go 1.24+). This is SA1019-clean and replaces the deprecated
// h2c.NewHandler wrapper pattern.
func NewLanternListener(
	lis net.Listener,
	_ NetConfig,
	tlsCfg TLSConfig,
	obs ObservabilityConfig,
	cors CORSConfig,
	svc *service.LanternService,
	rep *service.LanternReplicationService,
	val *ValidationInterceptor,
	rl *RateLimitInterceptor,
	auth *AuthInterceptor,
	logInt *LoggingInterceptor,
	met *PrometheusInterceptor,
	slow *SlowRPCInterceptor,
	hc *HealthChecker,
	logger *slog.Logger,
) (*LanternListener, error) {
	handlerOpts := connectHandlerOptions(val, rl, auth, logInt, met, slow, logger)

	mux := http.NewServeMux()
	mux.Handle(graphv1connect.NewLanternServiceHandler(
		service.NewLanternServiceConnectHandler(svc),
		handlerOpts...,
	))
	if rep != nil {
		mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(
			service.NewLanternReplicationServiceConnectHandler(rep),
			handlerOpts...,
		))
	}
	// gRPC-Health-v1 surface so grpc-health-probe + Kubernetes gRPC
	// probes keep working. HealthChecker pre-registers both Lantern
	// service entries; readiness Gate + LanternServer flip them via
	// SetServingStatus.
	mux.Handle(grpchealth.NewHandler(hc.Inner()))

	// gRPC reflection so grpcurl -plaintext keeps working. Mounted
	// only when reflection is enabled — same env-var contract as
	// before (LANTERN_REFLECTION, default true).
	if obs.EnableReflection {
		reflector := grpcreflect.NewStaticReflector(
			graphv1connect.LanternServiceName,
			graphv1connect.LanternReplicationServiceName,
			grpchealth.HealthV1ServiceName,
		)
		// Reflection is mounted OUTSIDE the Lantern interceptor chain, so
		// with auth enabled it stays reachable by default (schema discovery
		// is not data access). LANTERN_AUTH_EXEMPT_REFLECTION=false wraps
		// both mounts with the same bearer check for locked-down deploys.
		// grpc.health.v1 below is ALWAYS exempt: Kubernetes gRPC probes
		// cannot attach headers.
		v1path, v1h := grpcreflect.NewHandlerV1(reflector)
		v1apath, v1ah := grpcreflect.NewHandlerV1Alpha(reflector)
		if auth != nil && auth.Enabled() && !auth.ExemptReflection() {
			v1h = auth.RequireHTTP(v1h)
			v1ah = auth.RequireHTTP(v1ah)
		}
		mux.Handle(v1path, v1h)
		mux.Handle(v1apath, v1ah)
	}

	// CORS sits outside the mux so preflight responses (OPTIONS +
	// Access-Control-Request-Method) get short-circuited before any
	// handler runs. No-op when LANTERN_CORS_ALLOWED_ORIGINS is empty.
	handler := CORSMiddleware(cors)(mux)

	// otelhttp wraps the whole thing so every request — Connect,
	// grpchealth, grpcreflect — produces a server-side OpenTelemetry
	// span. The span name is derived from the URL path, which for
	// Connect equals "/<service>/<method>". This replaces the
	// previous otelgrpc StatsHandler.
	handler = otelhttp.NewHandler(handler, "lantern",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.URL.Path
		}),
	)

	addr := lis.Addr().String()
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second,
	}
	// Enable HTTP/2 cleartext (h2c) so Connect, gRPC, and gRPC-Web
	// clients can negotiate without TLS. Setting via the Protocols
	// field is the Go 1.24+ recommended path. Calling
	// SetUnencryptedHTTP2 on the zero-value Protocols also implicitly
	// preserves HTTP/1.1 and HTTP/2 (over TLS) so the same listener
	// serves browser fetch() + grpcurl + Connect-Go clients without
	// further tuning.
	var protos http.Protocols
	protos.SetHTTP1(true)
	protos.SetHTTP2(true)
	protos.SetUnencryptedHTTP2(true)
	srv.Protocols = &protos

	tlsConf, err := loadTLSConfig(tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("load tls: %w", err)
	}
	if tlsConf != nil {
		srv.TLSConfig = tlsConf
		logger.Info("tls enabled",
			slog.String("cert", tlsCfg.CertFile),
			slog.Bool("mtls", tlsCfg.ClientCAFile != ""),
		)
	}

	return &LanternListener{
		server:   srv,
		listener: lis,
		tls:      tlsConf != nil,
	}, nil
}

// loadTLSConfig parses the LANTERN_TLS_* env vars into a *tls.Config
// suitable for http.Server.TLSConfig. Returns (nil, nil) when no
// cert / key are set so callers can serve plain h2c without
// special-casing the disabled path.
//
// When LANTERN_TLS_CLIENT_CA_FILE is set, ClientAuth is bumped to
// RequireAndVerifyClientCert so the server only accepts connections
// presenting a certificate signed by the configured CA — the
// mTLS-equivalent of the legacy
// credentials.NewTLS(...).WithClientCAs(...) path.
func loadTLSConfig(c TLSConfig) (*tls.Config, error) {
	if c.CertFile == "" && c.KeyFile == "" {
		if c.ClientCAFile != "" {
			return nil, errors.New("LANTERN_TLS_CLIENT_CA_FILE set but LANTERN_TLS_CERT_FILE/LANTERN_TLS_KEY_FILE are empty")
		}
		return nil, nil
	}
	if c.CertFile == "" || c.KeyFile == "" {
		return nil, errors.New("LANTERN_TLS_CERT_FILE and LANTERN_TLS_KEY_FILE must both be set")
	}
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load tls cert/key: %w", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2", "http/1.1"},
	}
	if c.ClientCAFile != "" {
		pool, err := loadClientCAPool(c.ClientCAFile)
		if err != nil {
			return nil, err
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}
