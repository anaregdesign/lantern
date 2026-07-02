//go:build wireinject
// +build wireinject

package main

import (
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/google/wire"
)

func initializeApp() (*App, error) {
	wire.Build(
		provider.NewConfig,
		provider.NewNetConfig,
		provider.NewTLSConfig,
		provider.NewRateLimitConfig,
		provider.NewObservabilityConfig,
		provider.NewCacheConfig,
		provider.NewShutdownConfig,
		provider.NewValidationLimits,
		provider.NewTraversalConfig,
		provider.NewScanConfig,
		provider.NewSearchConfig,
		provider.NewMutationLogConfig,
		provider.NewReplicationConfig,
		provider.NewReadinessConfig,
		provider.NewPeerConfig,
		provider.NewAntiEntropyConfig,
		provider.NewHLCClock,
		provider.NewMutationLog,
		provider.NewLogger,
		provider.NewTracing,
		provider.NewGraphCache,
		provider.NewDomainMetrics,
		provider.WireCacheGCHooks,
		provider.NewReadinessGate,
		provider.NewPumpMetrics,
		provider.NewAntiEntropyMetrics,
		provider.NewListener,
		provider.NewPrometheusRegistry,
		provider.NewPrometheusInterceptor,
		provider.NewLoggingInterceptor,
		provider.NewSlowRPCInterceptorProvider,
		provider.NewValidationInterceptorProvider,
		provider.NewRateLimitInterceptorProvider,
		provider.NewHealthChecker,
		provider.NewLanternListener,
		provider.NewMetricsServer,
		provider.NewCORSConfig,
		provider.NewLifecycleConfig,
		provider.NewBackupConfig,
		provider.NewBackupper,
		newLanternService,
		newLanternReplicationService,
		provider.NewReplicationPump,
		provider.NewAntiEntropyDriver,
		service.NewLanternServer,
		wire.Bind(new(service.Listener), new(*provider.LanternListener)),
		wire.Bind(new(service.HealthSetter), new(*provider.HealthChecker)),
		wire.Bind(new(service.Backend), new(*graphcache.GraphCache[string, *pb.Vertex])),
		wire.Bind(new(service.Watcher), new(*graphcache.GraphCache[string, *pb.Vertex])),
		newApp,
	)
	return nil, nil
}
