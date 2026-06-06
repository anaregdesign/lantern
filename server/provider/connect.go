// Package provider: connect.go boots the additive Connect-Go listener
// introduced by #337. It mirrors the GatewayServer / MetricsServer pattern
// (interface + NoopConnectServer + real httpConnectServer) so the App
// errgroup can always call Run without nil-checks.
//
// The endpoint is **disabled by default** (port 0). It exists purely so
// downstream Connect-Web (#339), sdks/go Connect transport (#338), and
// sdks/node (#340) can develop against a real Connect+JSON surface during
// the migration window. The cutover that retires this parallel listener
// and moves Connect onto the primary :6380 port is owned by #347.
package provider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/service"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// ConnectListenerConfig governs the additive Connect endpoint.
//
//   - LANTERN_CONNECT_PORT                       host port for the Connect
//     listener (h2c). Default 0 = disabled. Set non-zero to enable.
//   - LANTERN_CONNECT_READ_HEADER_TIMEOUT_MS     slowloris guard. Default
//     5000 (5s). Mirrors LANTERN_GATEWAY_READ_HEADER_TIMEOUT_MS.
type ConnectListenerConfig struct {
	Port              int
	ReadHeaderTimeout time.Duration
}

// NewConnectListenerConfig selects the ConnectListener slice of Config.
func NewConnectListenerConfig(c *Config) ConnectListenerConfig { return c.ConnectListener }

// loadConnectListenerConfig reads ConnectListenerConfig from the environment.
// Called once at startup from NewConfig.
func loadConnectListenerConfig() ConnectListenerConfig {
	return ConnectListenerConfig{
		Port: envconfig.Int("LANTERN_CONNECT_PORT", 0),
		ReadHeaderTimeout: time.Duration(envconfig.Int(
			"LANTERN_CONNECT_READ_HEADER_TIMEOUT_MS", 5000,
		)) * time.Millisecond,
	}
}

// ConnectServer is the long-running goroutine that exposes the additive
// Connect-Go RPC surface. NewConnectServer returns a real http.Server when
// ConnectListenerConfig.Port > 0 and a NoopConnectServer otherwise.
type ConnectServer interface {
	Run(ctx context.Context) error
}

// NoopConnectServer is the disabled-listener implementation. Its Run waits
// for ctx to be canceled and returns nil, so the App errgroup behaves
// identically whether or not the additive listener is enabled.
type NoopConnectServer struct{}

func (NoopConnectServer) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

type httpConnectServer struct {
	srv    *http.Server
	logger *slog.Logger
	addr   string
}

// NewConnectServer mounts both the LanternService and (when wired) the
// LanternReplicationService Connect handlers onto an h2c-wrapped
// http.Server. The mount uses the path returned by
// NewLanternServiceHandler / NewLanternReplicationServiceHandler (a stable
// "/graph.v1.LanternService/" prefix), so the resulting URLs are
// directly addressable by `curl -X POST` for Connect+JSON.
//
// The ValidationInterceptor and RateLimitInterceptor — already wired
// onto the primary gRPC server via grpc.ChainUnaryInterceptor — are
// mounted here as connect.WithInterceptors so the additive listener
// enforces identical limits and bumps the same
// lantern_validation_rejected_total / lantern_rate_limit_rejected_total
// counters. Behaviour is observable as identical across both
// transports; see #349.
//
// rep MAY be nil — in that case the replication handler is not mounted
// (mirroring the gRPC path where replication can be disabled).
func NewConnectServer(
	cfg ConnectListenerConfig,
	svc *service.LanternService,
	rep *service.LanternReplicationService,
	val *ValidationInterceptor,
	rl *RateLimitInterceptor,
	logger *slog.Logger,
) ConnectServer {
	if cfg.Port <= 0 {
		return NoopConnectServer{}
	}
	handlerOpts := connectHandlerOptions(val, rl)
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
	addr := fmt.Sprintf(":%d", cfg.Port)
	return &httpConnectServer{
		srv: &http.Server{
			Addr:              addr,
			Handler:           h2c.NewHandler(mux, &http2.Server{}),
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		},
		logger: logger,
		addr:   addr,
	}
}

// connectHandlerOptions assembles the connect.HandlerOption chain shared
// by every mounted Connect handler. Pulled out so adding a new
// interceptor (auth, OTel, etc.) is a one-line edit at a single site
// rather than two parallel handler-mount blocks.
//
// Skips nil interceptors so call sites with a disabled rate limiter
// (RateLimitConfig.RPS<=0) don't have to special-case construction.
func connectHandlerOptions(val *ValidationInterceptor, rl *RateLimitInterceptor) []connect.HandlerOption {
	var ints []connect.Interceptor
	if val != nil {
		ints = append(ints, val.ConnectInterceptor())
	}
	if rl != nil && rl.lim != nil {
		ints = append(ints, rl.ConnectInterceptor())
	}
	if len(ints) == 0 {
		return nil
	}
	return []connect.HandlerOption{connect.WithInterceptors(ints...)}
}

func (c *httpConnectServer) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.srv.Shutdown(shutdownCtx)
	}()
	c.logger.Info("connect server starting", slog.String("addr", c.addr))
	if err := c.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
