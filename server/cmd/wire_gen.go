// Code generated by Wire. DO NOT EDIT.

//go:generate go run github.com/google/wire/cmd/wire
//go:build !wireinject
// +build !wireinject

package main

import (
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
)

// Injectors from wire.go:

func initializeLanternServer() (*service.LanternServer, error) {
	config := provider.NewConfig()
	graphCache := provider.NewGraphCache(config)
	lanternService := service.NewLanternService(graphCache)
	v := provider.NewGrpcServerOptions()
	server := provider.NewGrpcServer(v)
	listener, err := provider.NewListener()
	if err != nil {
		return nil, err
	}
	lanternServer := service.NewLanternServer(lanternService, server, listener)
	return lanternServer, nil
}
