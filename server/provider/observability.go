package provider

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/search"
	v1 "github.com/anaregdesign/lantern/pb/graph/v1"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewLogger builds the process-wide structured logger and installs it as the
// slog default so any package-level slog.* call inherits the same handler.
func NewLogger(o ObservabilityConfig) *slog.Logger {
	l := newLogger(o, os.Stderr)
	slog.SetDefault(l)
	return l
}

func newLogger(o ObservabilityConfig, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: o.LogLevel}
	var h slog.Handler
	if strings.EqualFold(o.LogFormat, "text") {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h).With(slog.String("service", "lantern"))
}

// NewDomainMetrics registers the Lantern-specific `lantern_*` collectors on
// the shared Prometheus registry. WireDomainMetrics binds the selected
// runtime's lock-consistent cache, causal, search, and replication samplers.
// The GC hooks themselves are installed separately by WireCacheGCHooks so the
// metrics adapter can be multiplexed with the structured per-tick log (#223).
func NewDomainMetrics(reg *prometheus.Registry, o ObservabilityConfig) *domainmetrics.DomainMetrics {
	return domainmetrics.New(reg, domainmetrics.Options{
		Version: o.Version,
		Commit:  o.Commit,
	})
}

// DomainMetricsWired forces cache samplers to bind only after the selected
// serving runtime has exposed its certified graph.
type DomainMetricsWired struct{}

func WireDomainMetrics(
	cache *graphcache.GraphCache[string, *v1.Vertex],
	runtime *service.ServingRuntime,
	m *domainmetrics.DomainMetrics,
) DomainMetricsWired {
	m.BindSampler(func() (int, int) {
		return cache.VertexCount(), cache.EdgeCount()
	})
	m.BindSearchIndexSampler(func() search.IndexMemoryStats {
		return cache.SearchIndexMemoryStats()
	})
	m.BindVertexHLCSampler(func() int {
		return cache.VertexHLCCount()
	})
	m.BindVertexHLCHighWaterSampler(func() int {
		return cache.VertexHLCHighWater()
	})
	m.BindCausalBarrierSampler(func() (int, int) {
		return cache.CausalBarrierCounts()
	})
	m.BindCausalMetadataSampler(func() domainmetrics.CausalMetadataSample {
		stats := cache.CausalMetadataStats()
		return domainmetrics.CausalMetadataSample{
			VertexLimit:                   stats.MaxVertexEntries,
			EdgeLimit:                     stats.MaxEdgeEntries,
			VertexEntries:                 stats.VertexEntries,
			EdgeEntries:                   stats.EdgeEntries,
			VertexEstimatedBytes:          stats.VertexEstimatedBytes,
			EdgeEstimatedBytes:            stats.EdgeEstimatedBytes,
			VertexEntriesHighWater:        stats.VertexEntriesHighWater,
			EdgeEntriesHighWater:          stats.EdgeEntriesHighWater,
			VertexEstimatedBytesHighWater: stats.VertexEstimatedBytesHighWater,
			EdgeEstimatedBytesHighWater:   stats.EdgeEstimatedBytesHighWater,
			VertexRejected:                stats.VertexRejected,
			EdgeRejected:                  stats.EdgeRejected,
			VertexOverLimit:               stats.VertexOverLimit,
			EdgeOverLimit:                 stats.EdgeOverLimit,
			OldestVertexRetentionDeadline: stats.OldestVertexRetentionDeadline,
			OldestEdgeRetentionDeadline:   stats.OldestEdgeRetentionDeadline,
		}
	})
	m.BindReceiptSampler(func() domainmetrics.ReceiptSample {
		stats := runtime.ReceiptStats()
		return domainmetrics.ReceiptSample{
			Entries:                   stats.Entries,
			Bytes:                     stats.Bytes,
			OldestDeadlineMillis:      stats.OldestDeadlineMillis,
			LocalAdmissionRejects:     stats.LocalAdmissionRejects,
			ReplicationCapacityStalls: stats.ReplicationCapacityStalls,
			NoLongerProvableLookups:   stats.NoLongerProvableLookups,
		}
	})
	return DomainMetricsWired{}
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

// NewGrpcServerMetrics has been removed; PrometheusInterceptor in
// connect_middleware.go registers the canonical `grpc_server_*` metric
// names so dashboards keep working.

// NewGrpcServerOptions and NewGrpcServer have been removed.
// The primary :6380 listener runs *http.Server with Connect-Go
// handlers and Connect interceptors (PrometheusInterceptor +
// LoggingInterceptor + SlowRPCInterceptor + ValidationInterceptor +
// RateLimitInterceptor + connect.WithRecover).
// See lantern_listener.go and connect_middleware.go.

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

func NewMetricsServer(o ObservabilityConfig, reg *prometheus.Registry, gate *readiness.Gate, logger *slog.Logger, _ runtimeCertified) MetricsServer {
	if o.MetricsAddr == "" {
		return NoopMetricsServer{}
	}
	return &httpMetricsServer{
		srv: &http.Server{
			Addr:              o.MetricsAddr,
			Handler:           newMetricsMux(reg, gate, o.EnablePprof),
			ReadHeaderTimeout: 5 * time.Second,
		},
		logger: logger,
	}
}

// newMetricsMux builds the /metrics + /healthz + /readyz + /healthz/ready
// handler tree. Extracted so tests can exercise the readiness-aware HTTP
// shim with httptest without binding a real port. When enablePprof is true,
// /debug/pprof/* is additionally mounted; otherwise those paths 404.
func newMetricsMux(reg *prometheus.Registry, gate *readiness.Gate, enablePprof bool) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	if enablePprof {
		registerPprofHandlers(mux)
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// /readyz and /healthz/ready both consult the readiness Gate so HTTP
	// probes (k8s httpGet, container-platform startup probes, plain LB
	// health probes) see the same drain signal as the `grpc.health.v1`
	// overall ("") entry served on the primary listener. Single-instance
	// mode returns 200 immediately — platform startup behaviour is unchanged.
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
