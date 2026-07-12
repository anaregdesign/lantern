package main

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/server/backup"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"

	"connectrpc.com/grpchealth"
	"golang.org/x/sync/errgroup"
)

// App is the composition root produced by wire. It owns every long-running
// goroutine (lantern http server, metrics HTTP server) so main only has to
// call Run.
type App struct {
	cfg         *provider.Config
	logger      *slog.Logger
	svc         *service.LanternService
	server      *service.LanternServer
	metrics     provider.MetricsServer
	tracing     *provider.Tracing
	domain      *domainmetrics.DomainMetrics
	health      *provider.HealthChecker
	gate        *readiness.Gate
	drainDelay  time.Duration
	backupper   *backup.Backupper
	restoreReq  bool
	pump        *replication.Pump
	llm         *provider.LLMEngine
	antiEntropy *replication.AntiEntropy
}

func newApp(
	cfg *provider.Config,
	logger *slog.Logger,
	svc *service.LanternService,
	server *service.LanternServer,
	metricsServer provider.MetricsServer,
	tracing *provider.Tracing,
	domain *domainmetrics.DomainMetrics,
	hc *provider.HealthChecker,
	pump *replication.Pump,
	antiEntropy *replication.AntiEntropy,
	gate *readiness.Gate,
	sc provider.ShutdownConfig,
	backupper *backup.Backupper,
	bcfg backup.Config,
	pc provider.PeerConfig,
	rc provider.ReplicationConfig,
	engine *provider.LLMEngine,
	_ provider.CacheGCHooksWired,
) *App {
	// Wire the replication snapshotter onto svc here (rather than inside
	// newLanternService) because pump itself depends on svc; that cycle
	// is broken by deferring the binding until after both have been
	// constructed. Safe to write the field now — the listener has not
	// started serving yet, so no goroutine can be reading
	// replicationSnapshotter concurrently. enabled reflects "this server
	// is wired to talk to peers": static peer list non-empty OR DNS
	// discovery configured.
	enabled := len(pc.Peers) > 0 || pc.Discovery == "dns"
	svc.WithReplicationStatus(pump, service.ReplicationStatusInfo{
		NodeID:  rc.NodeID,
		Enabled: enabled,
	})

	// The LLM engine has no service consumer yet (#828 is wiring only) —
	// holding it here keeps it in the wire graph and gives operators a
	// boot-time signal that their LANTERN_LLM_* config parsed.
	logger.Info("llm engine", slog.String("provider", engine.Provider()))

	return &App{
		cfg:         cfg,
		logger:      logger,
		llm:         engine,
		svc:         svc,
		server:      server,
		metrics:     metricsServer,
		tracing:     tracing,
		domain:      domain,
		health:      hc,
		gate:        gate,
		drainDelay:  sc.DrainDelay,
		backupper:   backupper,
		restoreReq:  bcfg.RestoreRequired,
		pump:        pump,
		antiEntropy: antiEntropy,
	}
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
	src provider.SearchConfig,
	rc provider.ReplicationConfig,
	vc provider.ValidationLimits,
	trc provider.TraversalConfig,
	tc provider.TLSConfig,
	cc provider.CacheConfig,
	obs provider.ObservabilityConfig,
	logger *slog.Logger,
	log *mutationlog.Log,
	clock *hlc.Clock,
	dm *domainmetrics.DomainMetrics,
) *service.LanternService {
	dm.SetCapacityLimits(cc.MaxVertices, cc.MaxEdges)
	svc := service.NewLanternService(backend).
		WithScanLimits(service.ScanLimits{
			ScanDefaultLimit:           sc.ScanDefaultLimit,
			ScanMaxLimit:               sc.ScanMaxLimit,
			DeleteByPrefixDefaultLimit: sc.DeleteByPrefixDefaultLimit,
			DeleteByPrefixMaxLimit:     sc.DeleteByPrefixMaxLimit,
		}).
		WithSearchLimits(service.SearchLimits{
			Enabled:          src.Enabled,
			PositionsEnabled: src.Positions,
			DefaultLimit:     src.DefaultLimit,
			MaxLimit:         src.MaxLimit,
			DefaultMode:      service.ParseMatchMode(src.DefaultMode),
			DefaultMinShould: src.DefaultMinShould,
		}).
		WithReplication(log, clock, dm.OnMutationLogAppend).
		WithAppliedHook(dm.OnReplicationApplied).
		WithReplicationApplyHook(dm.OnReplicationApply).
		WithValidationRejectHook(dm.OnValidationRejected).
		WithCapacityLimits(service.CapacityLimits{
			MaxVertices: cc.MaxVertices,
			MaxEdges:    cc.MaxEdges,
		}).
		WithTombstoneClampRejectHook(dm.OnTombstoneClampRejected).
		WithTombstoneTTL(rc.TombstoneTTL).
		WithTraversalTimeout(trc.Timeout).
		WithTraversalLimits(service.TraversalLimits{
			WorkBudget: graphcache.PPRWorkBudget{
				MaxPushes:       trc.MaxPushes,
				MaxTouchedEdges: trc.MaxTouchedEdges,
			},
			MaxResults: trc.MaxResults,
		}).
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
	// Restore-on-startup (#770, #779) runs BEFORE any listener serves: the
	// newest mounted dump is replayed as a baseline so the node never begins
	// serving an empty graph. When peers exist the subsequent bootstrap
	// overlays this baseline via HLC ordering (newer peer state wins per key,
	// so replicas take priority); a solo instance or a whole-cluster cold
	// start keeps the restored baseline as the recovered state. The Backupper
	// no-ops when backups are disabled or LANTERN_BACKUP_RESTORE_ON_START is
	// false. A restore failure fails boot only when
	// LANTERN_BACKUP_RESTORE_REQUIRED is set.
	if _, err := a.backupper.RestoreOnStartup(ctx); err != nil {
		if a.restoreReq {
			return fmt.Errorf("restore-on-startup: %w", err)
		}
		a.logger.Warn("restore-on-startup failed; starting with current state", slog.Any("err", err))
	}

	// The overall ("") gRPC health entry is owned by the readiness Gate
	// (#188): in single-instance mode it is already SERVING; in
	// multi-peer mode it stays NOT_SERVING until bootstrap completes
	// and replication lag is within LANTERN_MAX_REPLICATION_LAG.
	//
	// Two-phase shutdown for zero-drop rolling updates (#768): the long-
	// running servers serve on serveCtx, NOT the parent ctx. On SIGTERM the
	// drain coordinator first flips readiness to NOT_SERVING (overall ""
	// health + /readyz return 503 so load balancers deregister this
	// instance), holds every listener up for LANTERN_DRAIN_DELAY_SECONDS so
	// in-flight and endpoint-propagation-window requests still complete, and
	// only then cancels serveCtx to begin the real graceful shutdown. With
	// DrainDelay=0 the behaviour matches the historical path bar an
	// immediate, harmless BeginDrain.
	serveCtx, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()

	g, gctx := errgroup.WithContext(serveCtx)
	// Ready-to-serve instant for GetServerStatus.started_at/uptime (#943).
	// Called AFTER RestoreOnStartup (which can take arbitrarily long) and
	// right before the Connect listener starts accepting, so uptime reflects
	// "ready to serve" rather than wire-init time. MarkStarted is
	// sync.Once-guarded, so a hot-reload that re-enters Run does not reset it.
	a.svc.MarkStarted(time.Now())
	g.Go(func() error { return a.server.Run(gctx) })
	g.Go(func() error { return a.metrics.Run(gctx) })
	g.Go(func() error { a.domain.Run(gctx); return nil })
	g.Go(func() error { return a.pump.Run(gctx) })
	g.Go(func() error { return a.antiEntropy.Run(gctx) })
	g.Go(func() error { return a.backupper.Run(gctx) })
	g.Go(func() error {
		drainPhase(ctx, gctx.Done(), a.drainDelay, a.beginDrain)
		cancelServe()
		return nil
	})
	g.Go(func() error {
		<-gctx.Done()
		a.health.SetServingStatus("", grpchealth.StatusNotServing)
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

// beginDrain latches the readiness Gate into NOT_SERVING (#768) so the
// overall ("") gRPC health entry and /readyz report draining immediately.
// nil-safe so an App constructed without a Gate (tests) is a no-op.
func (a *App) beginDrain() {
	if a.gate == nil {
		return
	}
	a.logger.Info("draining: readiness NOT_SERVING, holding listeners for drain window",
		slog.Duration("drain_delay", a.drainDelay))
	a.gate.BeginDrain()
}

// drainPhase implements the graceful-drain window (#768). It blocks until
// the parent context is cancelled (operator SIGTERM) or serveDone fires (a
// server goroutine failed first). On parent cancellation it calls begin
// once — the caller flips readiness to NOT_SERVING — then holds for
// drainDelay (cut short if serveDone fires meanwhile) so the listeners keep
// serving while load balancers deregister the endpoint. When serveDone
// fires first it returns immediately without draining: an already-failing
// server should shut down without an artificial delay.
func drainPhase(parent context.Context, serveDone <-chan struct{}, drainDelay time.Duration, begin func()) {
	select {
	case <-parent.Done():
	case <-serveDone:
		return
	}
	if begin != nil {
		begin()
	}
	if drainDelay <= 0 {
		return
	}
	timer := time.NewTimer(drainDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-serveDone:
	}
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
