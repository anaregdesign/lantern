//go:build wireinject
// +build wireinject

package main

import (
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/google/wire"
)

func initializeApp() (*App, error) {
	wire.Build(
		provider.NewConfig,
		provider.NewLogger,
		provider.NewGraphCache,
		provider.NewListener,
		provider.NewPrometheusRegistry,
		provider.NewGrpcServerMetrics,
		provider.NewGrpcServerOptions,
		provider.NewGrpcServer,
		provider.NewHealthServer,
		provider.NewMetricsServer,
		service.NewLanternService,
		service.NewLanternServer,
		newGCInterval,
		registerHealthAndReflection,
		newApp,
	)
	return nil, nil
}
