// Package service: server.go owns the primary :6380 listener lifecycle.
// LanternServer wraps *http.Server + Shutdown so the listener can serve
// the Connect mux (LanternService + LanternReplicationService +
// grpc.health.v1 + grpc.reflection.v1) over a single port. The exported
// surface — NewLanternServer, Run, HealthSetter, LifecycleConfig,
// Watcher — is the only contract callers depend on.
package service

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"connectrpc.com/grpchealth"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
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
	shutdownPlan    *ShutdownPlan
	health          HealthSetter
	watcher         Watcher
}

// UsesWatcher verifies the installed GC owner before any lifecycle starts.
func (s *LanternServer) UsesWatcher(watcher Watcher) bool {
	if s == nil {
		return false
	}
	owner, ok := watcher.(*dataruntime.GraphPolicyOwner)
	installed, installedOK := s.watcher.(*dataruntime.GraphPolicyOwner)
	return ok && installedOK && owner != nil && installed == owner
}

// WithShutdownPlan binds App's exact shared plan during composition, before
// Run. An omitted plan preserves the existing static lifecycle behavior.
func (s *LanternServer) WithShutdownPlan(plan *ShutdownPlan) *LanternServer {
	s.shutdownPlan = plan
	return s
}

// UsesShutdownPlan verifies composition identity before Run without capturing
// a generation or changing the server's shutdown behavior.
func (s *LanternServer) UsesShutdownPlan(plan *ShutdownPlan) bool {
	return s != nil && plan != nil && s.shutdownPlan == plan
}

// Watcher is the lifecycle hook LanternServer uses to drive the cache GC
// loop. *graphcache.GraphCache satisfies it; tests can stub it.
type Watcher interface {
	Watch(ctx context.Context, interval time.Duration)
}

// HealthSetter is the narrow surface of the health checker LanternServer
// needs to publish SERVING / NOT_SERVING per service. Defined here so
// callers can stub it in tests, and matched by *provider.HealthChecker
// (the connectrpc.com/grpchealth-backed implementation) so the wire
// binding is one line.
type HealthSetter interface {
	SetServingStatus(service string, status grpchealth.Status)
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
	ShutdownPlan    *ShutdownPlan
}

// NewLanternServer takes the composed Listener (an http.Server bound to
// a net.Listener with all handlers + interceptors already mounted) and
// the lifecycle wiring. Run blocks until ctx is cancelled or the
// server exits with an error.
//
// svc and rep are unused inside LanternServer (the Connect handlers
// are mounted during listener construction) but accepted as parameters so
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
		shutdownPlan:    cfg.ShutdownPlan,
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
		s.health.SetServingStatus(ServiceName, grpchealth.StatusServing)
		s.health.SetServingStatus(ReplicationServiceName, grpchealth.StatusServing)
	}

	owned, cancelOwned := context.WithCancel(ctx)
	defer cancelOwned()
	stopped := make(chan error, 1)
	go func() { stopped <- s.gracefulShutdown(owned) }()
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		if s.watcher != nil {
			s.watcher.Watch(owned, s.gcInterval)
		}
	}()

	s.logger.Info("lantern server starting",
		slog.String("addr", s.listener.Addr().String()),
		slog.Bool("tls", s.useTLS),
		slog.Duration("gc_interval", s.gcInterval),
		slog.Duration("shutdown_timeout", s.currentShutdownTimeout()),
	)

	var err error
	if s.useTLS {
		// TLS cert/key are already loaded into s.server.TLSConfig by
		// the listener provider; ServeTLS with empty filenames falls
		// through to that.
		err = s.server.ServeTLS(s.listener, "", "")
	} else {
		err = s.server.Serve(s.listener)
	}
	// Serve returns when listeners close, before Shutdown has joined handlers.
	// Keep ownership through that join and propagate timeout/forced-close errors.
	if errors.Is(err, http.ErrServerClosed) {
		if ctx.Err() == nil {
			err = errors.New("public listener stopped without shutdown request")
		} else {
			err = nil
		}
	}
	cancelOwned()
	err = errors.Join(err, <-stopped)
	<-watched

	if s.health != nil {
		s.health.SetServingStatus(ServiceName, grpchealth.StatusNotServing)
		s.health.SetServingStatus(ReplicationServiceName, grpchealth.StatusNotServing)
	}
	return err
}

// gracefulShutdown waits for ctx to be cancelled, then drains in-flight
// HTTP requests via http.Server.Shutdown. If ShutdownTimeout > 0 and
// drain does not complete within that bound, it escalates to Close so
// the process can exit. A non-positive ShutdownTimeout disables the
// deadline.
func (s *LanternServer) gracefulShutdown(ctx context.Context) error {
	<-ctx.Done()
	timeout := s.shutdownTimeout
	if s.shutdownPlan != nil {
		timeout = s.shutdownPlan.Capture().Policy.ShutdownTimeout
	}
	s.logger.Info("shutting down lantern server",
		slog.Duration("timeout", timeout))
	if s.health != nil {
		s.health.SetServingStatus(ServiceName, grpchealth.StatusNotServing)
		s.health.SetServingStatus(ReplicationServiceName, grpchealth.StatusNotServing)
	}
	shutdownCtx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		shutdownCtx, cancel = context.WithTimeout(shutdownCtx, timeout)
		defer cancel()
	}
	if err := s.server.Shutdown(shutdownCtx); err != nil {
		// Shutdown returned context-deadline-exceeded or another
		// error: force-close so the listener stops accepting and
		// Serve returns.
		s.logger.Warn("graceful shutdown deadline exceeded; forcing close",
			slog.Any("err", err))
		return errors.Join(err, s.server.Close())
	}
	return nil
}

func (s *LanternServer) currentShutdownTimeout() time.Duration {
	if s.shutdownPlan != nil {
		return s.shutdownPlan.Current().Policy.ShutdownTimeout
	}
	return s.shutdownTimeout
}
