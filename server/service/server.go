// Package service: server.go owns the primary :6380 listener lifecycle.
// The pre-#347 implementation wrapped *grpc.Server + GracefulStop. The
// cutover replaces that with *http.Server + Shutdown so the listener can
// serve the Connect mux (LanternService + LanternReplicationService +
// grpc-health-v1 + grpc-reflection) over a single port.
//
// The exported surface is preserved verbatim — NewLanternServer, Run,
// HealthSetter, LifecycleConfig, Watcher — so call sites only need to
// swap the listener wiring; the lifecycle contract is unchanged.
package service

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// LanternServer owns the lifecycle of the primary listener: starts the
// http.Server, flips the per-service health entries, drives the cache
// GC loop, and shuts everything down on ctx cancellation. Constructors
// in package provider compose the http.Server (with all Connect
// handlers and middleware) and pass the wrapper here as Listener.
type LanternServer struct {
	server          *http.Server
	listener        net.Listener
	useTLS          bool
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

// HealthSetter is the narrow surface of the health checker LanternServer
// needs to publish SERVING / NOT_SERVING per service. Defined here so
// callers can stub it in tests, and matched by *provider.HealthChecker
// (the connectrpc.com/grpchealth-backed implementation) so the wire
// binding is one line.
type HealthSetter interface {
	SetServingStatus(service string, status healthpb.HealthCheckResponse_ServingStatus)
}

// Listener is the narrow surface of provider.LanternListener that
// LanternServer consumes. Declared here (instead of as a
// *provider.LanternListener parameter) so service/ keeps zero imports
// from provider/.
type Listener interface {
	Server() *http.Server
	Listener() net.Listener
	TLSEnabled() bool
}

// LifecycleConfig groups the tunables wire injects into LanternServer so the
// constructor signature stays stable as new options are added.
type LifecycleConfig struct {
	GCInterval      time.Duration
	ShutdownTimeout time.Duration
}

// NewLanternServer takes the composed Listener (an http.Server bound to
// a net.Listener with all handlers + interceptors already mounted) and
// the lifecycle wiring. Run blocks until ctx is cancelled or the
// server exits with an error.
//
// svc and rep are unused inside LanternServer (the Connect handlers
// are mounted by NewLanternListener) but accepted as parameters so
// wire enforces construction order: both services must be wired before
// the listener is created.
func NewLanternServer(
	listener Listener,
	logger *slog.Logger,
	cfg LifecycleConfig,
	hs HealthSetter,
	watcher Watcher,
	_ *LanternService,
	_ *LanternReplicationService,
) *LanternServer {
	return &LanternServer{
		server:          listener.Server(),
		listener:        listener.Listener(),
		useTLS:          listener.TLSEnabled(),
		logger:          logger,
		gcInterval:      cfg.GCInterval,
		shutdownTimeout: cfg.ShutdownTimeout,
		health:          hs,
		watcher:         watcher,
	}
}

// Run marks the services healthy, starts the cache GC loop, and serves
// HTTP until ctx is cancelled. On shutdown Shutdown drains in-flight
// requests but is bounded by ShutdownTimeout; past that, Close forces
// a hard tear-down so the process can exit.
func (s *LanternServer) Run(ctx context.Context) error {
	if s.health != nil {
		s.health.SetServingStatus(ServiceName, healthpb.HealthCheckResponse_SERVING)
		s.health.SetServingStatus(ReplicationServiceName, healthpb.HealthCheckResponse_SERVING)
	}

	go s.gracefulShutdown(ctx)
	go s.watcher.Watch(ctx, s.gcInterval)

	s.logger.Info("lantern server starting",
		slog.String("addr", s.listener.Addr().String()),
		slog.Bool("tls", s.useTLS),
		slog.Duration("gc_interval", s.gcInterval),
		slog.Duration("shutdown_timeout", s.shutdownTimeout),
	)

	var err error
	if s.useTLS {
		// TLS cert/key are already loaded into s.server.TLSConfig by
		// NewLanternListener; ServeTLS with empty filenames falls
		// through to that, matching the legacy grpc.Creds() behaviour.
		err = s.server.ServeTLS(s.listener, "", "")
	} else {
		err = s.server.Serve(s.listener)
	}
	// http.ErrServerClosed is the post-Shutdown sentinel; surface as
	// nil so the App errgroup doesn't treat a clean drain as fatal.
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}

	if s.health != nil {
		s.health.SetServingStatus(ServiceName, healthpb.HealthCheckResponse_NOT_SERVING)
		s.health.SetServingStatus(ReplicationServiceName, healthpb.HealthCheckResponse_NOT_SERVING)
	}
	return err
}

// gracefulShutdown waits for ctx to be cancelled, then drains in-flight
// HTTP requests via http.Server.Shutdown. If ShutdownTimeout > 0 and
// drain does not complete within that bound, it escalates to Close so
// the process can exit. A non-positive ShutdownTimeout disables the
// deadline.
func (s *LanternServer) gracefulShutdown(ctx context.Context) {
	<-ctx.Done()
	s.logger.Info("shutting down lantern server",
		slog.Duration("timeout", s.shutdownTimeout))
	if s.health != nil {
		s.health.SetServingStatus(ServiceName, healthpb.HealthCheckResponse_NOT_SERVING)
		s.health.SetServingStatus(ReplicationServiceName, healthpb.HealthCheckResponse_NOT_SERVING)
	}
	shutdownCtx := context.Background()
	if s.shutdownTimeout > 0 {
		var cancel context.CancelFunc
		shutdownCtx, cancel = context.WithTimeout(shutdownCtx, s.shutdownTimeout)
		defer cancel()
	}
	if err := s.server.Shutdown(shutdownCtx); err != nil {
		// Shutdown returned context-deadline-exceeded or another
		// error: force-close so the listener stops accepting and
		// Serve returns.
		s.logger.Warn("graceful shutdown deadline exceeded; forcing close",
			slog.Any("err", err))
		_ = s.server.Close()
	}
}
