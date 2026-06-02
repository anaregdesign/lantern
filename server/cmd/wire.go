//go:build wireinject
// +build wireinject

package main

import (
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/google/wire"
	"google.golang.org/grpc/health"
)

func initializeApp() (*App, error) {
	wire.Build(
		provider.NewConfig,
		provider.NewLogger,
		provider.NewTracing,
		provider.NewGraphCache,
		provider.NewListener,
		provider.NewPrometheusRegistry,
		provider.NewGrpcServerMetrics,
		provider.NewGrpcServerOptions,
		provider.NewGrpcServer,
		provider.NewHealthServer,
		provider.NewMetricsServer,
		provider.NewLifecycleConfig,
		service.NewLanternService,
		service.NewLanternServer,
		wire.Bind(new(service.HealthSetter), new(*health.Server)),
		registerHealthAndReflection,
		newApp,
	)
	return nil, nil
}
