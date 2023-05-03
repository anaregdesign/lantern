//go:build wireinject
// +build wireinject

package main

import (
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/google/wire"
)

func initializeLanternServer() (*service.LanternServer, error) {
	wire.Build(
		provider.NewConfig,
		provider.NewGraphCache,
		provider.NewListener,
		provider.NewGrpcServerOptions,
		provider.NewGrpcServer,
		service.NewLanternService,
		service.NewLanternServer,
	)
	return nil, nil
}
