package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// App is the composition root produced by wire. It owns every long-running
// goroutine (gRPC server, metrics HTTP server) so main only has to call Run.
type App struct {
	cfg     *provider.Config
	logger  *slog.Logger
	grpc    *service.LanternServer
	metrics *provider.MetricsServer
	tracing *provider.Tracing
	health  *health.Server
}

func newApp(
	cfg *provider.Config,
	logger *slog.Logger,
	grpcServer *service.LanternServer,
	metricsServer *provider.MetricsServer,
	tracing *provider.Tracing,
	hs *health.Server,
	_ registeredHealth,
) *App {
	return &App{
		cfg:     cfg,
		logger:  logger,
		grpc:    grpcServer,
		metrics: metricsServer,
		tracing: tracing,
		health:  hs,
	}
}

// registeredHealth is a marker emitted after the health and reflection
// services have been registered on the gRPC server. wire injects it into
// newApp purely to enforce ordering.
type registeredHealth struct{}

func registerHealthAndReflection(c *provider.Config, s *grpc.Server, hs *health.Server) registeredHealth {
	provider.RegisterHealth(s, hs)
	provider.RegisterReflection(c, s)
	return registeredHealth{}
}

func (a *App) Run(ctx context.Context) error {
	a.health.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return a.grpc.Run(gctx) })
	g.Go(func() error { return a.metrics.Run(gctx) })
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

	app.logger.Info("lantern starting",
		slog.Int("port", app.cfg.Port),
		slog.Duration("default_ttl", app.cfg.TTL),
		slog.String("metrics_addr", app.cfg.MetricsAddr),
		slog.Bool("reflection", app.cfg.EnableReflection),
	)

	if err := app.Run(ctx); err != nil {
		app.logger.Error("server exited with error", slog.Any("err", err))
		os.Exit(1)
	}
	app.logger.Info("server stopped cleanly")
}
