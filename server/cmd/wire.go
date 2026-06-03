//go:build wireinject
// +build wireinject

package main

import (
	"github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/google/wire"
	"google.golang.org/grpc/health"
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
		provider.NewScanConfig,
		provider.NewMutationLogConfig,
		provider.NewReplicationConfig,
		provider.NewPeerConfig,
		provider.NewAntiEntropyConfig,
		provider.NewHLCClock,
		provider.NewMutationLog,
		provider.NewLogger,
		provider.NewTracing,
		provider.NewGraphCache,
		provider.NewDomainMetrics,
		provider.NewListener,
		provider.NewPrometheusRegistry,
		provider.NewGrpcServerMetrics,
		provider.NewGrpcServerOptions,
		provider.NewGrpcServer,
		provider.NewHealthServer,
		provider.NewMetricsServer,
		provider.NewLifecycleConfig,
		newLanternService,
		newLanternReplicationService,
		provider.NewReplicationPump,
		provider.NewAntiEntropyDriver,
		service.NewLanternServer,
		wire.Bind(new(service.HealthSetter), new(*health.Server)),
		wire.Bind(new(service.Backend), new(*graph.GraphCache[string, *pb.Vertex])),
		wire.Bind(new(service.Watcher), new(*graph.GraphCache[string, *pb.Vertex])),
		registerHealthAndReflection,
		newApp,
	)
	return nil, nil
}
