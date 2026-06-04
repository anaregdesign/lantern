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
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
	"github.com/anaregdesign/lantern/server/readiness"
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

// NetConfig groups the gRPC listener and message-size / concurrency caps.
//
//   - LANTERN_PORT                       (default 6380)
//   - LANTERN_MAX_RECV_MSG_BYTES         (default 16 MiB)
//   - LANTERN_MAX_SEND_MSG_BYTES         (default 16 MiB)
//   - LANTERN_MAX_CONCURRENT_STREAMS     (default 1024; 0 = unlimited)
type NetConfig struct {
	Port                 int
	MaxRecvMsgBytes      int
	MaxSendMsgBytes      int
	MaxConcurrentStreams uint32
}

// TLSConfig groups the all-or-nothing TLS / mTLS material.
//
//   - LANTERN_TLS_CERT_FILE              PEM cert path (enables TLS)
//   - LANTERN_TLS_KEY_FILE               PEM key  path
//   - LANTERN_TLS_CLIENT_CA_FILE         PEM client CA path (enables mTLS)
type TLSConfig struct {
	CertFile     string
	KeyFile      string
	ClientCAFile string
}

// RateLimitConfig is the process-wide token bucket policy.
//
//   - LANTERN_RATE_LIMIT_RPS             0 disables rate limiting (default 0)
//   - LANTERN_RATE_LIMIT_BURST           token bucket burst (default 2x RPS)
type RateLimitConfig struct {
	RPS   float64
	Burst int
}

// ObservabilityConfig groups logging, metrics endpoint, gRPC reflection and
// build-info knobs.
//
//   - LANTERN_LOG_LEVEL                  debug|info|warn|error (default info)
//   - LANTERN_LOG_FORMAT                 json|text             (default json)
//   - LANTERN_METRICS_ADDR               host:port for /metrics + /healthz
//     (default :9090; set empty to disable)
//   - LANTERN_REFLECTION                 true|false            (default true)
//   - LANTERN_VERSION                    overrides lantern_build_info{version}
//   - LANTERN_COMMIT                     overrides lantern_build_info{commit}
//   - LANTERN_SLOW_RPC_THRESHOLD_MS      milliseconds; RPCs that take longer
//     emit a warn-level "slow rpc" log. Default 500. Set to 0 to disable.
type ObservabilityConfig struct {
	LogLevel         slog.Level
	LogFormat        string
	MetricsAddr      string
	EnableReflection bool
	Version          string
	Commit           string
	SlowRPCThreshold time.Duration
}

// CacheConfig sizes the GraphCache TTL and its GC tick.
//
//   - LANTERN_DEFAULT_TTL_SECONDS        (default 60)
//   - LANTERN_GC_INTERVAL_SECONDS        (default 60) — GraphCache.Watch tick
type CacheConfig struct {
	TTL        time.Duration
	GCInterval time.Duration
}

// ShutdownConfig is the GracefulStop deadline.
//
//   - LANTERN_SHUTDOWN_TIMEOUT_SECONDS   (default 30)
type ShutdownConfig struct {
	Timeout time.Duration
}

// ScanConfig caps the per-call pagination knobs for the prefix RPCs.
// Defaults aim at "safe to leave unconfigured": small enough that a buggy
// client cannot trivially exhaust the server, large enough to make a
// reasonable single page useful. Operators can lift the ceilings via env
// when the workload warrants it.
//
//   - LANTERN_SCAN_DEFAULT_LIMIT                    (default 1000)
//   - LANTERN_SCAN_MAX_LIMIT                        (default 10000)
//   - LANTERN_DELETE_BY_PREFIX_DEFAULT_LIMIT        (default 10000)
//   - LANTERN_DELETE_BY_PREFIX_MAX_LIMIT            (default 100000)
type ScanConfig struct {
	ScanDefaultLimit           uint32
	ScanMaxLimit               uint32
	DeleteByPrefixDefaultLimit uint32
	DeleteByPrefixMaxLimit     uint32
}

// Config aggregates every focused sub-config. It is constructed once at
// startup by NewConfig and then projected into sub-configs that each provider
// consumes — providers MUST NOT take *Config when they only need one slice of
// it (SRP). main / App may keep *Config because they observe several aspects
// for startup logging.
type Config struct {
	Net           NetConfig
	TLS           TLSConfig
	RateLimit     RateLimitConfig
	Observability ObservabilityConfig
	Cache         CacheConfig
	Shutdown      ShutdownConfig
	Validation    ValidationLimits
	Scan          ScanConfig
	MutationLog   MutationLogConfig
	Replication   ReplicationConfig
	Peer          PeerConfig
	AntiEntropy   AntiEntropyConfig
	Readiness     ReadinessConfig
}

func NewConfig() *Config {
	rps := envconfig.Float("LANTERN_RATE_LIMIT_RPS", 0)
	burst := envconfig.Int("LANTERN_RATE_LIMIT_BURST", int(2*rps))
	if burst <= 0 && rps > 0 {
		burst = int(2 * rps)
	}
	return &Config{
		Net: NetConfig{
			Port:                 envconfig.Int("LANTERN_PORT", 6380),
			MaxRecvMsgBytes:      envconfig.Int("LANTERN_MAX_RECV_MSG_BYTES", 16*1024*1024),
			MaxSendMsgBytes:      envconfig.Int("LANTERN_MAX_SEND_MSG_BYTES", 16*1024*1024),
			MaxConcurrentStreams: uint32(envconfig.Int("LANTERN_MAX_CONCURRENT_STREAMS", 1024)),
		},
		TLS: TLSConfig{
			CertFile:     os.Getenv("LANTERN_TLS_CERT_FILE"),
			KeyFile:      os.Getenv("LANTERN_TLS_KEY_FILE"),
			ClientCAFile: os.Getenv("LANTERN_TLS_CLIENT_CA_FILE"),
		},
		RateLimit: RateLimitConfig{
			RPS:   rps,
			Burst: burst,
		},
		Observability: ObservabilityConfig{
			LogLevel:         envconfig.LogLevel(os.Getenv("LANTERN_LOG_LEVEL")),
			LogFormat:        envconfig.String("LANTERN_LOG_FORMAT", "json"),
			MetricsAddr:      envconfig.String("LANTERN_METRICS_ADDR", ":9090"),
			EnableReflection: envconfig.Bool("LANTERN_REFLECTION", true),
			Version:          os.Getenv("LANTERN_VERSION"),
			Commit:           os.Getenv("LANTERN_COMMIT"),
			SlowRPCThreshold: time.Duration(envconfig.Int("LANTERN_SLOW_RPC_THRESHOLD_MS", 500)) * time.Millisecond,
		},
		Cache: CacheConfig{
			TTL:        time.Duration(envconfig.Int("LANTERN_DEFAULT_TTL_SECONDS", 60)) * time.Second,
			GCInterval: time.Duration(envconfig.Int("LANTERN_GC_INTERVAL_SECONDS", 60)) * time.Second,
		},
		Shutdown: ShutdownConfig{
			Timeout: time.Duration(envconfig.Int("LANTERN_SHUTDOWN_TIMEOUT_SECONDS", 30)) * time.Second,
		},
		Validation: ValidationLimits{
			MaxKeyLen:         envconfig.Int("LANTERN_MAX_KEY_LEN", 1024),
			MaxBatchSize:      envconfig.Int("LANTERN_MAX_BATCH_SIZE", 10000),
			IlluminateMaxStep: envconfig.Int("LANTERN_ILLUMINATE_MAX_STEP", 16),
			IlluminateMaxK:    envconfig.Int("LANTERN_ILLUMINATE_MAX_K", 1024),
		},
		Scan: ScanConfig{
			ScanDefaultLimit:           uint32(envconfig.Int("LANTERN_SCAN_DEFAULT_LIMIT", 1000)),
			ScanMaxLimit:               uint32(envconfig.Int("LANTERN_SCAN_MAX_LIMIT", 10000)),
			DeleteByPrefixDefaultLimit: uint32(envconfig.Int("LANTERN_DELETE_BY_PREFIX_DEFAULT_LIMIT", 10000)),
			DeleteByPrefixMaxLimit:     uint32(envconfig.Int("LANTERN_DELETE_BY_PREFIX_MAX_LIMIT", 100000)),
		},
		MutationLog: loadMutationLogConfig(),
		Replication: loadReplicationConfig(),
		Readiness:   loadReadinessConfig(),
		Peer:        loadPeerConfig(),
		AntiEntropy: loadAntiEntropyConfig(),
	}
}

// Sub-config selectors. Wire uses these to inject each focused struct into
// the providers that need it, so no provider has to depend on the full
// *Config aggregate.

func NewNetConfig(c *Config) NetConfig                     { return c.Net }
func NewTLSConfig(c *Config) TLSConfig                     { return c.TLS }
func NewRateLimitConfig(c *Config) RateLimitConfig         { return c.RateLimit }
func NewObservabilityConfig(c *Config) ObservabilityConfig { return c.Observability }
func NewCacheConfig(c *Config) CacheConfig                 { return c.Cache }
func NewShutdownConfig(c *Config) ShutdownConfig           { return c.Shutdown }
func NewValidationLimits(c *Config) ValidationLimits       { return c.Validation }
func NewScanConfig(c *Config) ScanConfig                   { return c.Scan }

// NewLogger builds the process-wide structured logger and installs it as the
// slog default so any package-level slog.* call inherits the same handler.
func NewLogger(o ObservabilityConfig) *slog.Logger {
	opts := &slog.HandlerOptions{Level: o.LogLevel}
	var h slog.Handler
	if strings.EqualFold(o.LogFormat, "text") {
		h = slog.NewTextHandler(os.Stderr, opts)
	} else {
		h = slog.NewJSONHandler(os.Stderr, opts)
	}
	l := slog.New(h).With(slog.String("service", "lantern"))
	slog.SetDefault(l)
	return l
}

func NewGraphCache(c CacheConfig) *graph.GraphCache[string, *v1.Vertex] {
	gc := graph.NewGraphCache[string, *v1.Vertex](c.TTL)
	// Identity projection: vertex keys are themselves the indexed string.
	// Enabling the prefix index up-front (before any insert) is required
	// by the cache contract — EnablePrefixIndex panics on a non-empty
	// cache. Doing it here in the constructor guarantees that invariant.
	gc.EnablePrefixIndex(func(s string) string { return s })
	return gc
}

// NewDomainMetrics registers the Lantern-specific `lantern_*` collectors on
// the shared Prometheus registry and binds the gauge sampler that reads
// cache.VertexCount / EdgeCount on each DomainMetrics.Run tick. The GC
// hooks themselves are installed separately by WireCacheGCHooks so the
// metrics adapter can be multiplexed with the structured per-tick log
// (#223).
func NewDomainMetrics(
	reg *prometheus.Registry,
	o ObservabilityConfig,
	cache *graph.GraphCache[string, *v1.Vertex],
) *domainmetrics.DomainMetrics {
	m := domainmetrics.New(reg, domainmetrics.Options{
		Version: o.Version,
		Commit:  o.Commit,
	})
	m.BindSampler(func() (int, int) {
		return cache.VertexCount(), cache.EdgeCount()
	})
	return m
}

// CacheGCHooksWired is a marker returned by WireCacheGCHooks so wire can
// force ordering: the wiring must happen after both DomainMetrics and the
// logger are constructed, but the result is not consumed by any downstream
// provider directly. newApp accepts it as an unused parameter.
type CacheGCHooksWired struct{}

// WireCacheGCHooks installs a multiplexed pair of GC hooks on the cache:
// one branch updates DomainMetrics (lantern_cache_evicted_total +
// lantern_cache_gc_duration_seconds), the other emits one info-level
// "graph cache: gc tick" slog line per tick summarising the work done
// (#223). The hooks are fired sequentially by GraphCache.Watch on a
// single goroutine, so the per-tick accumulator inside the closure is
// safe without locking.
func WireCacheGCHooks(
	cache *graph.GraphCache[string, *v1.Vertex],
	m *domainmetrics.DomainMetrics,
	logger *slog.Logger,
) CacheGCHooksWired {
	// Per-tick accumulator. GraphCache.Watch calls onExpire 0–3 times
	// (one per non-empty kind) then onGC once — atomically per tick,
	// on a single goroutine — so a plain struct is sufficient.
	var tick struct {
		vertices int
		edges    int
		dangling int
	}
	onExpire := func(kind string, n int) {
		m.OnExpire(kind, n)
		switch kind {
		case "vertex":
			tick.vertices += n
		case "edge":
			tick.edges += n
		case "dangling_edge":
			tick.dangling += n
		}
	}
	onGC := func(d time.Duration) {
		m.OnGCDuration(d)
		logger.LogAttrs(context.Background(), slog.LevelInfo, "graph cache: gc tick",
			slog.Int("vertices_expired", tick.vertices),
			slog.Int("edges_expired", tick.edges),
			slog.Int("dangling_edges_removed", tick.dangling),
			slog.Int("vertices_remaining", cache.VertexCount()),
			slog.Int("edges_remaining", cache.EdgeCount()),
			slog.Int64("duration_ms", d.Milliseconds()),
		)
		tick.vertices, tick.edges, tick.dangling = 0, 0, 0
	}
	cache.SetGCHooks(onExpire, onGC)
	return CacheGCHooksWired{}
}

func NewListener(n NetConfig) (net.Listener, error) {
	return net.Listen("tcp", ":"+strconv.Itoa(n.Port))
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
	net NetConfig,
	tlsCfg TLSConfig,
	rl RateLimitConfig,
	limits ValidationLimits,
	obs ObservabilityConfig,
	logger *slog.Logger,
	metrics *grpcprom.ServerMetrics,
	dm *domainmetrics.DomainMetrics,
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

	validator := NewValidationInterceptor(limits).
		WithRejectHook(dm.OnValidationRejected).
		WithLogger(logger)

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

	if rl.RPS > 0 {
		rli := NewRateLimitInterceptor(rl.RPS, rl.Burst).
			WithRejectHook(dm.OnRateLimitRejected)
		unary = append(unary, validator.UnaryServerInterceptor(), rli.UnaryServerInterceptor())
		stream = append(stream, validator.StreamServerInterceptor(), rli.StreamServerInterceptor())
	} else {
		unary = append(unary, validator.UnaryServerInterceptor())
		stream = append(stream, validator.StreamServerInterceptor())
	}

	// Slow-RPC warn log fires last so it observes the full handler
	// duration including validation + rate-limit decisions (#223).
	if slow := NewSlowRPCInterceptor(obs.SlowRPCThreshold, logger); slow.Enabled() {
		unary = append(unary, slow.UnaryServerInterceptor())
		stream = append(stream, slow.StreamServerInterceptor())
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
	if net.MaxRecvMsgBytes > 0 {
		opts = append(opts, grpc.MaxRecvMsgSize(net.MaxRecvMsgBytes))
	}
	if net.MaxSendMsgBytes > 0 {
		opts = append(opts, grpc.MaxSendMsgSize(net.MaxSendMsgBytes))
	}
	if net.MaxConcurrentStreams > 0 {
		opts = append(opts, grpc.MaxConcurrentStreams(net.MaxConcurrentStreams))
	}

	creds, err := loadServerTLS(tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("load tls: %w", err)
	}
	if creds != nil {
		opts = append(opts, grpc.Creds(creds))
		logger.Info("tls enabled",
			slog.String("cert", tlsCfg.CertFile),
			slog.Bool("mtls", tlsCfg.ClientCAFile != ""),
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

// MetricsServer is the long-running goroutine that exposes /metrics
// (Prometheus) and /healthz + /readyz on a dedicated HTTP port. The
// NewMetricsServer constructor returns a real HTTP server when
// LANTERN_METRICS_ADDR is set and a NoopMetricsServer otherwise, so callers
// never have to nil-check before calling Run (Null Object pattern).
type MetricsServer interface {
	// Run blocks until ctx is canceled or the underlying server exits with
	// a non-shutdown error.
	Run(ctx context.Context) error
}

// NoopMetricsServer is the disabled-metrics implementation. Its Run simply
// waits for ctx to be canceled and returns nil, so the App errgroup behaves
// identically whether or not metrics are enabled.
type NoopMetricsServer struct{}

func (NoopMetricsServer) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// httpMetricsServer is the real /metrics + /healthz + /readyz HTTP server.
type httpMetricsServer struct {
	srv    *http.Server
	logger *slog.Logger
}

func NewMetricsServer(o ObservabilityConfig, reg *prometheus.Registry, gate *readiness.Gate, logger *slog.Logger) MetricsServer {
	if o.MetricsAddr == "" {
		return NoopMetricsServer{}
	}
	return &httpMetricsServer{
		srv: &http.Server{
			Addr:              o.MetricsAddr,
			Handler:           newMetricsMux(reg, gate),
			ReadHeaderTimeout: 5 * time.Second,
		},
		logger: logger,
	}
}

// newMetricsMux builds the /metrics + /healthz + /readyz + /healthz/ready
// handler tree. Extracted so tests can exercise the readiness-aware HTTP
// shim with httptest without binding a real port.
func newMetricsMux(reg *prometheus.Registry, gate *readiness.Gate) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// /readyz and /healthz/ready both consult the readiness Gate so HTTP
	// probes (k8s httpGet, Cloud Run / ACA startup probes, plain LB
	// health probes) see the same drain signal as the gRPC overall ("")
	// health entry. Single-instance mode returns 200 immediately —
	// PaaS startup behaviour is unchanged.
	readyHandler := func(w http.ResponseWriter, _ *http.Request) {
		if gate == nil || gate.Ready() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready"))
	}
	mux.HandleFunc("/readyz", readyHandler)
	mux.HandleFunc("/healthz/ready", readyHandler)
	return mux
}

// Run blocks until ctx is canceled or ListenAndServe returns an error other
// than http.ErrServerClosed.
func (m *httpMetricsServer) Run(ctx context.Context) error {
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
func RegisterReflection(o ObservabilityConfig, s *grpc.Server) {
	if o.EnableReflection {
		reflection.Register(s)
	}
}
