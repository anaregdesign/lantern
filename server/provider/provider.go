package provider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/cache/graph"
	v1 "github.com/anaregdesign/lantern/gen/go/graph/v1"
	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

// Config captures runtime configuration sourced from the environment.
//
// Backwards-compatible vars:
//   - LANTERN_PORT                  (default 6380)
//   - LANTERN_DEFAULT_TTL_SECONDS   (default 60)
//
// Observability / lifecycle additions:
//   - LANTERN_GC_INTERVAL_SECONDS   (default 60) — GraphCache.Watch tick
//   - LANTERN_LOG_LEVEL             debug|info|warn|error (default info)
//   - LANTERN_LOG_FORMAT            json|text             (default json)
//   - LANTERN_METRICS_ADDR          host:port for /metrics + /healthz
//     (default :9090; set empty to disable)
//   - LANTERN_REFLECTION            true|false            (default true)
type Config struct {
	Port             int
	TTL              time.Duration
	GCInterval       time.Duration
	LogLevel         slog.Level
	LogFormat        string
	MetricsAddr      string
	EnableReflection bool
}

func NewConfig() *Config {
	return &Config{
		Port:             envInt("LANTERN_PORT", 6380),
		TTL:              time.Duration(envInt("LANTERN_DEFAULT_TTL_SECONDS", 60)) * time.Second,
		GCInterval:       time.Duration(envInt("LANTERN_GC_INTERVAL_SECONDS", 60)) * time.Second,
		LogLevel:         parseLogLevel(os.Getenv("LANTERN_LOG_LEVEL")),
		LogFormat:        envStr("LANTERN_LOG_FORMAT", "json"),
		MetricsAddr:      envStr("LANTERN_METRICS_ADDR", ":9090"),
		EnableReflection: envBool("LANTERN_REFLECTION", true),
	}
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// NewLogger builds the process-wide structured logger and installs it as the
// slog default so any package-level slog.* call inherits the same handler.
func NewLogger(c *Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.LogLevel}
	var h slog.Handler
	if strings.EqualFold(c.LogFormat, "text") {
		h = slog.NewTextHandler(os.Stderr, opts)
	} else {
		h = slog.NewJSONHandler(os.Stderr, opts)
	}
	l := slog.New(h).With(slog.String("service", "lantern"))
	slog.SetDefault(l)
	return l
}

func NewGraphCache(c *Config) *graph.GraphCache[string, *v1.Vertex] {
	return graph.NewGraphCache[string, *v1.Vertex](c.TTL)
}

func NewListener(c *Config) (net.Listener, error) {
	return net.Listen("tcp", ":"+strconv.Itoa(c.Port))
}

// NewHealthServer is the gRPC health-checking implementation.
func NewHealthServer() *health.Server {
	return health.NewServer()
}

// NewPrometheusRegistry isolates server metrics in a dedicated registry so the
// global default stays clean.
func NewPrometheusRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return reg
}

// NewGrpcServerMetrics exposes per-RPC histograms/counters via the
// grpc-middleware Prometheus provider.
func NewGrpcServerMetrics(reg *prometheus.Registry) *grpcprom.ServerMetrics {
	m := grpcprom.NewServerMetrics(
		grpcprom.WithServerHandlingTimeHistogram(),
	)
	reg.MustRegister(m)
	return m
}

// NewGrpcServerOptions assembles the modern interceptor + stats-handler chain:
//   - otelgrpc stats handler (replaces the deprecated unary interceptor)
//   - recovery interceptor (panic -> Internal status)
//   - slog logging interceptor
//   - Prometheus server metrics interceptor
//   - keepalive policy that pings idle clients and rejects abusive ones
func NewGrpcServerOptions(
	logger *slog.Logger,
	metrics *grpcprom.ServerMetrics,
) []grpc.ServerOption {
	logOpts := []logging.Option{
		logging.WithLogOnEvents(logging.StartCall, logging.FinishCall),
	}
	recoveryOpts := []recovery.Option{
		recovery.WithRecoveryHandlerContext(func(ctx context.Context, p any) error {
			logger.ErrorContext(ctx, "grpc handler panic", slog.Any("panic", p))
			return fmt.Errorf("internal server error")
		}),
	}
	return []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			recovery.UnaryServerInterceptor(recoveryOpts...),
			logging.UnaryServerInterceptor(slogInterceptorLogger(logger), logOpts...),
			metrics.UnaryServerInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			recovery.StreamServerInterceptor(recoveryOpts...),
			logging.StreamServerInterceptor(slogInterceptorLogger(logger), logOpts...),
			metrics.StreamServerInterceptor(),
		),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 15 * time.Minute,
			Time:              30 * time.Second,
			Timeout:           5 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	}
}

// slogInterceptorLogger bridges grpc-middleware's logging.Logger to slog.
// logging.Level constants are numerically identical to slog.Level values
// (debug=-4, info=0, warn=4, error=8) so we pass through directly.
func slogInterceptorLogger(l *slog.Logger) logging.Logger {
	return logging.LoggerFunc(func(ctx context.Context, lvl logging.Level, msg string, fields ...any) {
		l.Log(ctx, slog.Level(lvl), msg, fields...)
	})
}

func NewGrpcServer(options []grpc.ServerOption) *grpc.Server {
	return grpc.NewServer(options...)
}

// MetricsServer hosts /metrics (Prometheus) and /healthz + /readyz on a
// dedicated HTTP port. Returns nil when LANTERN_METRICS_ADDR is empty.
type MetricsServer struct {
	srv    *http.Server
	logger *slog.Logger
}

func NewMetricsServer(c *Config, reg *prometheus.Registry, logger *slog.Logger) *MetricsServer {
	if c.MetricsAddr == "" {
		return nil
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	return &MetricsServer{
		srv: &http.Server{
			Addr:              c.MetricsAddr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		},
		logger: logger,
	}
}

// Run blocks until ctx is canceled or ListenAndServe returns an error other
// than http.ErrServerClosed. Safe to call on a nil *MetricsServer (no-op).
func (m *MetricsServer) Run(ctx context.Context) error {
	if m == nil {
		<-ctx.Done()
		return nil
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = m.srv.Shutdown(shutdownCtx)
	}()
	m.logger.Info("metrics server starting", slog.String("addr", m.srv.Addr))
	if err := m.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// RegisterHealth wires the gRPC health service so probes / LBs can query
// SERVING status.
func RegisterHealth(s *grpc.Server, hs *health.Server) {
	healthpb.RegisterHealthServer(s, hs)
}

// RegisterReflection enables grpcurl-style descriptor reflection when allowed
// by the LANTERN_REFLECTION env var.
func RegisterReflection(c *Config, s *grpc.Server) {
	if c.EnableReflection {
		reflection.Register(s)
	}
}
