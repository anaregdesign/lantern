package main

import (
	"context"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// App is the composition root produced by wire. It owns every long-running
// goroutine (gRPC server, metrics HTTP server) so main only has to call Run.
type App struct {
	cfg         *provider.Config
	logger      *slog.Logger
	grpc        *service.LanternServer
	metrics     provider.MetricsServer
	tracing     *provider.Tracing
	domain      *domainmetrics.DomainMetrics
	health      *health.Server
	pump        *replication.Pump
	antiEntropy *replication.AntiEntropy
}

func newApp(
	cfg *provider.Config,
	logger *slog.Logger,
	svc *service.LanternService,
	grpcServer *service.LanternServer,
	metricsServer provider.MetricsServer,
	tracing *provider.Tracing,
	domain *domainmetrics.DomainMetrics,
	hs *health.Server,
	pump *replication.Pump,
	antiEntropy *replication.AntiEntropy,
	pc provider.PeerConfig,
	rc provider.ReplicationConfig,
	_ registeredHealth,
	_ provider.CacheGCHooksWired,
) *App {
	// Wire the replication snapshotter onto svc here (rather than inside
	// newLanternService) because pump itself depends on svc; that cycle
	// is broken by deferring the binding until after both have been
	// constructed. Safe to write the field now — gRPC has not started
	// serving yet, so no goroutine can be reading
	// replicationSnapshotter concurrently. enabled reflects "this server
	// is wired to talk to peers": static peer list non-empty OR DNS
	// discovery configured.
	enabled := len(pc.Peers) > 0 || pc.Discovery == "dns"
	svc.WithReplicationStatus(pump, service.ReplicationStatusInfo{
		NodeID:  rc.NodeID,
		Enabled: enabled,
	})

	return &App{
		cfg:         cfg,
		logger:      logger,
		grpc:        grpcServer,
		metrics:     metricsServer,
		tracing:     tracing,
		domain:      domain,
		health:      hs,
		pump:        pump,
		antiEntropy: antiEntropy,
	}
}

// registeredHealth is a marker emitted after the health and reflection
// services have been registered on the gRPC server. wire injects it into
// newApp purely to enforce ordering.
type registeredHealth struct{}

func registerHealthAndReflection(o provider.ObservabilityConfig, s *grpc.Server, hs *health.Server) registeredHealth {
	provider.RegisterHealth(s, hs)
	provider.RegisterReflection(o, s)
	return registeredHealth{}
}

// clampU32 safely narrows an arbitrary platform-sized int (e.g. a value
// loaded by envconfig.Int) into the uint32 surface exposed by
// GetServerStatusResponse. Negative values collapse to 0 and oversized
// values saturate at math.MaxUint32 — both are the "ceiling does not
// apply" sentinel the admin UI already treats as "no configured limit".
func clampU32(v int) uint32 {
	if v <= 0 {
		return 0
	}
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}

// newLanternService is the wire seam between provider.ScanConfig and the
// service-layer ScanLimits value. Keeping the conversion here (rather than
// in package service) preserves the rule that service/ has zero imports
// from provider/.
func newLanternService(
	backend service.Backend,
	sc provider.ScanConfig,
	rc provider.ReplicationConfig,
	vc provider.ValidationLimits,
	tc provider.TLSConfig,
	cc provider.CacheConfig,
	obs provider.ObservabilityConfig,
	logger *slog.Logger,
	log *mutationlog.Log,
	clock *hlc.Clock,
	dm *domainmetrics.DomainMetrics,
) *service.LanternService {
	svc := service.NewLanternService(backend).
		WithScanLimits(service.ScanLimits{
			ScanDefaultLimit:           sc.ScanDefaultLimit,
			ScanMaxLimit:               sc.ScanMaxLimit,
			DeleteByPrefixDefaultLimit: sc.DeleteByPrefixDefaultLimit,
			DeleteByPrefixMaxLimit:     sc.DeleteByPrefixMaxLimit,
		}).
		WithReplication(log, clock, dm.OnMutationLogAppend).
		WithAppliedHook(dm.OnReplicationApplied).
		WithReplicationApplyHook(dm.OnReplicationApply).
		WithValidationRejectHook(dm.OnValidationRejected).
		WithTombstoneClampRejectHook(dm.OnTombstoneClampRejected).
		WithTombstoneTTL(rc.TombstoneTTL).
		WithHotPathMetrics(dm).
		WithLogger(logger).
		WithStatusInfo(service.StatusInfo{
			Version:            obs.Version,
			DefaultTTL:         cc.TTL,
			MaxBatchSize:       clampU32(vc.MaxBatchSize),
			MaxKeyBytes:        clampU32(vc.MaxKeyLen),
			ScanDefaultLimit:   sc.ScanDefaultLimit,
			ScanMaxLimit:       sc.ScanMaxLimit,
			TLSEnabled:         tc.CertFile != "" && tc.KeyFile != "",
			ReplicationEnabled: log != nil && clock != nil,
		})
	// Bind the mutation-log + origin-state samplers so DomainMetrics.Run
	// can populate lantern_mutation_log_fill_ratio,
	// lantern_mutation_log_evicted_total, and
	// lantern_origin_states_count on its tick interval (#221). Done here
	// because newLanternService is the only provider with access to both
	// the log/service and the DomainMetrics instance.
	if log != nil {
		dm.BindMutationLogSampler(func() (int, int, uint64) {
			return log.Len(), log.Cap(), log.Evicted()
		})
	}
	dm.BindOriginStatesSampler(svc.OriginStatesCount)
	return svc
}

// newLanternReplicationService wires the streaming replication surface so it
// shares the same *mutationlog.Log as the write path. Metrics are attached
// here (rather than inside the service) to keep service/ free of
// provider/metrics imports.
func newLanternReplicationService(
	log *mutationlog.Log,
	backend service.Backend,
	clock *hlc.Clock,
	logger *slog.Logger,
	dm *domainmetrics.DomainMetrics,
	svc *service.LanternService,
) *service.LanternReplicationService {
	return service.NewLanternReplicationService(log, backend, clock).
		WithMetrics(dm).
		WithLogger(logger).
		WithOriginStates(svc)
}

func (a *App) Run(ctx context.Context) error {
	// The overall ("") gRPC health entry is owned by the readiness Gate
	// (#188): in single-instance mode it is already SERVING; in
	// multi-peer mode it stays NOT_SERVING until bootstrap completes
	// and replication lag is within LANTERN_MAX_REPLICATION_LAG.

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return a.grpc.Run(gctx) })
	g.Go(func() error { return a.metrics.Run(gctx) })
	g.Go(func() error { a.domain.Run(gctx); return nil })
	g.Go(func() error { return a.pump.Run(gctx) })
	g.Go(func() error { return a.antiEntropy.Run(gctx) })
	g.Go(func() error {
		<-gctx.Done()
		a.health.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
		a.health.Shutdown()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.tracing.Shutdown(shutdownCtx); err != nil {
			a.logger.Warn("otel tracer shutdown returned error", slog.Any("err", err))
		}
		return nil
	})
	return g.Wait()
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := initializeApp()
	if err != nil {
		slog.Error("failed to initialize app", slog.Any("err", err))
		os.Exit(1)
	}

	// Apply runtime mutex/block profile sampling rates as early as
	// possible so any subsequent contention is captured. Both knobs are
	// no-ops when the env vars are unset (#239).
	provider.ApplyRuntimeProfiling(app.cfg.Observability, app.logger)

	app.logger.Info("lantern starting",
		slog.Int("port", app.cfg.Net.Port),
		slog.Duration("default_ttl", app.cfg.Cache.TTL),
		slog.String("metrics_addr", app.cfg.Observability.MetricsAddr),
		slog.Bool("reflection", app.cfg.Observability.EnableReflection),
	)

	if err := app.Run(ctx); err != nil {
		app.logger.Error("server exited with error", slog.Any("err", err))
		os.Exit(1)
	}
	app.logger.Info("server stopped cleanly")
}
