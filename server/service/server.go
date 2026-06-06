package service

import (
	"context"
	"log/slog"
	"net"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type LanternServer struct {
	service         *LanternService
	replication     *LanternReplicationService
	server          *grpc.Server
	listener        net.Listener
	logger          *slog.Logger
	gcInterval      time.Duration
	shutdownTimeout time.Duration
	health          HealthSetter
	watcher         Watcher
}

// Watcher is the lifecycle hook LanternServer uses to drive the cache GC
// loop. *graph.GraphCache satisfies it; tests can stub it.
type Watcher interface {
	Watch(ctx context.Context, interval time.Duration)
}

// HealthSetter is the narrow surface of *health.Server that LanternServer
// needs to publish SERVING / NOT_SERVING per service. Defined here so
// callers can stub it in tests.
type HealthSetter interface {
	SetServingStatus(service string, status healthpb.HealthCheckResponse_ServingStatus)
}

// LifecycleConfig groups the tunables wire injects into LanternServer so the
// constructor signature stays stable as new options are added.
type LifecycleConfig struct {
	GCInterval      time.Duration
	ShutdownTimeout time.Duration
}

func NewLanternServer(
	service *LanternService,
	replication *LanternReplicationService,
	server *grpc.Server,
	listener net.Listener,
	logger *slog.Logger,
	cfg LifecycleConfig,
	hs HealthSetter,
	watcher Watcher,
) *LanternServer {
	return &LanternServer{
		service:         service,
		replication:     replication,
		server:          server,
		listener:        listener,
		logger:          logger,
		gcInterval:      cfg.GCInterval,
		shutdownTimeout: cfg.ShutdownTimeout,
		health:          hs,
		watcher:         watcher,
	}
}

// Run registers the gRPC service, marks it healthy, starts the cache GC
// loop, and serves until ctx is canceled. On shutdown GracefulStop drains
// in-flight RPCs but is bounded by ShutdownTimeout — past that, Stop forces
// a hard close so the process can exit.
func (s *LanternServer) Run(ctx context.Context) error {
	pb.RegisterLanternServiceServer(s.server, s.service)
	if s.replication != nil {
		pb.RegisterLanternReplicationServiceServer(s.server, s.replication)
	}
	if s.health != nil {
		s.health.SetServingStatus(ServiceName, healthpb.HealthCheckResponse_SERVING)
		if s.replication != nil {
			s.health.SetServingStatus(ReplicationServiceName, healthpb.HealthCheckResponse_SERVING)
		}
	}

	// Capture the "ready to serve" instant so GetServerStatus (#314)
	// reports uptime relative to when the server actually started
	// accepting traffic, not wire-init time.
	s.service.MarkStarted(time.Now())

	go s.gracefulShutdown(ctx)

	go s.watcher.Watch(ctx, s.gcInterval)

	s.logger.Info("grpc server starting",
		slog.String("addr", s.listener.Addr().String()),
		slog.Duration("gc_interval", s.gcInterval),
		slog.Duration("shutdown_timeout", s.shutdownTimeout),
	)
	// Serve blocks until the server stops. Whatever the reason — graceful
	// shutdown, listener error, or fatal panic recovered by grpc — flip the
	// health gauge to NOT_SERVING so probes don't keep reporting healthy
	// against a dead server. The shutdown goroutine above also sets this,
	// but only on ctx cancellation; this covers every other exit path.
	err := s.server.Serve(s.listener)
	if s.health != nil {
		s.health.SetServingStatus(ServiceName, healthpb.HealthCheckResponse_NOT_SERVING)
		if s.replication != nil {
			s.health.SetServingStatus(ReplicationServiceName, healthpb.HealthCheckResponse_NOT_SERVING)
		}
	}
	return err
}

// gracefulShutdown waits for ctx to be canceled, then drains in-flight RPCs
// via GracefulStop. If ShutdownTimeout > 0 and graceful drain does not
// complete within that bound, it escalates to Stop so the process can exit.
// A non-positive ShutdownTimeout disables the deadline and waits unboundedly
// for GracefulStop.
func (s *LanternServer) gracefulShutdown(ctx context.Context) {
	<-ctx.Done()
	s.logger.Info("shutting down grpc server", slog.Duration("timeout", s.shutdownTimeout))
	if s.health != nil {
		s.health.SetServingStatus(ServiceName, healthpb.HealthCheckResponse_NOT_SERVING)
		if s.replication != nil {
			s.health.SetServingStatus(ReplicationServiceName, healthpb.HealthCheckResponse_NOT_SERVING)
		}
	}
	done := make(chan struct{})
	go func() {
		s.server.GracefulStop()
		close(done)
	}()
	if s.shutdownTimeout <= 0 {
		<-done
		return
	}
	select {
	case <-done:
	case <-time.After(s.shutdownTimeout):
		s.logger.Warn("graceful shutdown deadline exceeded; forcing stop")
		s.server.Stop()
	}
}
