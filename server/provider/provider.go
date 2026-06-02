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
	v1 "github.com/anaregdesign/lantern/pb/graph/v1"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	_ "google.golang.org/grpc/encoding/gzip" // register gzip compressor so clients requesting it work
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

// Config captures runtime configuration sourced from the environment.
//
// Backwards-compatible vars:
//   - LANTERN_PORT                       (default 6380)
//   - LANTERN_DEFAULT_TTL_SECONDS        (default 60)
//
// Observability / lifecycle additions:
//   - LANTERN_GC_INTERVAL_SECONDS        (default 60) — GraphCache.Watch tick
//   - LANTERN_SHUTDOWN_TIMEOUT_SECONDS   (default 30) — GracefulStop deadline
//   - LANTERN_LOG_LEVEL                  debug|info|warn|error (default info)
//   - LANTERN_LOG_FORMAT                 json|text             (default json)
//   - LANTERN_METRICS_ADDR               host:port for /metrics + /healthz
//     (default :9090; set empty to disable)
//   - LANTERN_REFLECTION                 true|false            (default true)
//
// Resource & limits:
//   - LANTERN_MAX_RECV_MSG_BYTES         (default 16 MiB)
//   - LANTERN_MAX_SEND_MSG_BYTES         (default 16 MiB)
//   - LANTERN_MAX_CONCURRENT_STREAMS     (default 1024; 0 = unlimited)
//   - LANTERN_RATE_LIMIT_RPS             per-process token bucket replenish rate;
//     0 disables rate limiting (default 0)
//   - LANTERN_RATE_LIMIT_BURST           token bucket burst (default 2x RPS)
//
// Validation guard rails (codes.InvalidArgument):
//   - LANTERN_MAX_KEY_LEN                (default 1024)
//   - LANTERN_MAX_BATCH_SIZE             (default 10000)
//   - LANTERN_ILLUMINATE_MAX_STEP        (default 16)
//   - LANTERN_ILLUMINATE_MAX_K           (default 1024)
//
// TLS (all-or-nothing; mTLS engaged when client CA is set):
//   - LANTERN_TLS_CERT_FILE              PEM cert path (enables TLS)
//   - LANTERN_TLS_KEY_FILE               PEM key  path
//   - LANTERN_TLS_CLIENT_CA_FILE         PEM client CA path (enables mTLS)
type Config struct {
	Port             int
	TTL              time.Duration
	GCInterval       time.Duration
	ShutdownTimeout  time.Duration
	LogLevel         slog.Level
	LogFormat        string
	MetricsAddr      string
	EnableReflection bool

	MaxRecvMsgBytes      int
	MaxSendMsgBytes      int
	MaxConcurrentStreams uint32
	RateLimitRPS         float64
	RateLimitBurst       int

	MaxKeyLen         int
	MaxBatchSize      int
	IlluminateMaxStep int
	IlluminateMaxK    int

	TLSCertFile     string
	TLSKeyFile      string
	TLSClientCAFile string
}

func NewConfig() *Config {
	rps := envFloat("LANTERN_RATE_LIMIT_RPS", 0)
	burst := envInt("LANTERN_RATE_LIMIT_BURST", int(2*rps))
	if burst <= 0 && rps > 0 {
		burst = int(2 * rps)
	}
	return &Config{
		Port:             envInt("LANTERN_PORT", 6380),
		TTL:              time.Duration(envInt("LANTERN_DEFAULT_TTL_SECONDS", 60)) * time.Second,
		GCInterval:       time.Duration(envInt("LANTERN_GC_INTERVAL_SECONDS", 60)) * time.Second,
		ShutdownTimeout:  time.Duration(envInt("LANTERN_SHUTDOWN_TIMEOUT_SECONDS", 30)) * time.Second,
		LogLevel:         parseLogLevel(os.Getenv("LANTERN_LOG_LEVEL")),
		LogFormat:        envStr("LANTERN_LOG_FORMAT", "json"),
		MetricsAddr:      envStr("LANTERN_METRICS_ADDR", ":9090"),
		EnableReflection: envBool("LANTERN_REFLECTION", true),

		MaxRecvMsgBytes:      envInt("LANTERN_MAX_RECV_MSG_BYTES", 16*1024*1024),
		MaxSendMsgBytes:      envInt("LANTERN_MAX_SEND_MSG_BYTES", 16*1024*1024),
		MaxConcurrentStreams: uint32(envInt("LANTERN_MAX_CONCURRENT_STREAMS", 1024)),
		RateLimitRPS:         rps,
		RateLimitBurst:       burst,

		MaxKeyLen:         envInt("LANTERN_MAX_KEY_LEN", 1024),
		MaxBatchSize:      envInt("LANTERN_MAX_BATCH_SIZE", 10000),
		IlluminateMaxStep: envInt("LANTERN_ILLUMINATE_MAX_STEP", 16),
		IlluminateMaxK:    envInt("LANTERN_ILLUMINATE_MAX_K", 1024),

		TLSCertFile:     os.Getenv("LANTERN_TLS_CERT_FILE"),
		TLSKeyFile:      os.Getenv("LANTERN_TLS_KEY_FILE"),
		TLSClientCAFile: os.Getenv("LANTERN_TLS_CLIENT_CA_FILE"),
	}
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
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

// NewDomainMetrics registers the Lantern-specific `lantern_*` collectors on
// the shared Prometheus registry and wires the GraphCache GC hooks so each
// Watch tick updates the eviction counters and histogram. The gauge sampler
// runs from DomainMetrics.Run, started by App alongside the other long-lived
// goroutines.
func NewDomainMetrics(
	reg *prometheus.Registry,
	cache *graph.GraphCache[string, *v1.Vertex],
) *domainmetrics.DomainMetrics {
	m := domainmetrics.New(reg, domainmetrics.Options{})
	m.BindSampler(func() (int, int) {
		return cache.VertexCount(), cache.EdgeCount()
	})
	cache.SetGCHooks(m.OnExpire, m.OnGCDuration)
	return m
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
//   - request validation interceptor (codes.InvalidArgument guard rails)
//   - optional token-bucket rate limiter (codes.ResourceExhausted)
//   - keepalive policy that pings idle clients and rejects abusive ones
//   - message size + concurrent stream caps
//   - optional TLS / mTLS credentials
func NewGrpcServerOptions(
	c *Config,
	logger *slog.Logger,
	metrics *grpcprom.ServerMetrics,
) ([]grpc.ServerOption, error) {
	logOpts := []logging.Option{
		logging.WithLogOnEvents(logging.StartCall, logging.FinishCall),
	}
	recoveryOpts := []recovery.Option{
		recovery.WithRecoveryHandlerContext(func(ctx context.Context, p any) error {
			logger.ErrorContext(ctx, "grpc handler panic", slog.Any("panic", p))
			return fmt.Errorf("internal server error")
		}),
	}

	validator := NewValidationInterceptor(ValidationLimits{
		MaxKeyLen:         c.MaxKeyLen,
		MaxBatchSize:      c.MaxBatchSize,
		IlluminateMaxStep: c.IlluminateMaxStep,
		IlluminateMaxK:    c.IlluminateMaxK,
	})

	unary := []grpc.UnaryServerInterceptor{
		recovery.UnaryServerInterceptor(recoveryOpts...),
		logging.UnaryServerInterceptor(slogInterceptorLogger(logger), logOpts...),
		metrics.UnaryServerInterceptor(),
	}
	stream := []grpc.StreamServerInterceptor{
		recovery.StreamServerInterceptor(recoveryOpts...),
		logging.StreamServerInterceptor(slogInterceptorLogger(logger), logOpts...),
		metrics.StreamServerInterceptor(),
	}

	if c.RateLimitRPS > 0 {
		rl := NewRateLimitInterceptor(c.RateLimitRPS, c.RateLimitBurst)
		unary = append(unary, validator.UnaryServerInterceptor(), rl.UnaryServerInterceptor())
		stream = append(stream, validator.StreamServerInterceptor(), rl.StreamServerInterceptor())
	} else {
		unary = append(unary, validator.UnaryServerInterceptor())
		stream = append(stream, validator.StreamServerInterceptor())
	}

	opts := []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(unary...),
		grpc.ChainStreamInterceptor(stream...),
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
	if c.MaxRecvMsgBytes > 0 {
		opts = append(opts, grpc.MaxRecvMsgSize(c.MaxRecvMsgBytes))
	}
	if c.MaxSendMsgBytes > 0 {
		opts = append(opts, grpc.MaxSendMsgSize(c.MaxSendMsgBytes))
	}
	if c.MaxConcurrentStreams > 0 {
		opts = append(opts, grpc.MaxConcurrentStreams(c.MaxConcurrentStreams))
	}

	creds, err := loadServerTLS(c)
	if err != nil {
		return nil, fmt.Errorf("load tls: %w", err)
	}
	if creds != nil {
		opts = append(opts, grpc.Creds(creds))
		logger.Info("tls enabled",
			slog.String("cert", c.TLSCertFile),
			slog.Bool("mtls", c.TLSClientCAFile != ""),
		)
	}
	return opts, nil
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
