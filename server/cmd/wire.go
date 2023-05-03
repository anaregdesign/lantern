//go:build wireinject
// +build wireinject

package main

import (
	"github.com/anaregdesign/lantern/server/service"
	"github.com/google/wire"
	"google.golang.org/grpc"
	"net"
)

func newListener() (net.Listener, error) {
	return net.Listen("tcp", ":8080")
}

func newGrpcServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{}
}

func newGrpcServer(options []grpc.ServerOption) *grpc.Server {
	return grpc.NewServer(options...)
}

func newLanternService(cache *service.GrpcGraphCache) *service.LanternService {
	return service.NewLanternService(cache)
}

func newLanternServer(svc *service.LanternService, s *grpc.Server, listener net.Listener) *service.LanternServer {
	return service.NewLanternServer(svc, s, listener)
}

func initializeLanternServer() (*service.LanternServer, error) {
	wire.Build(
		service.NewGraphCache,
		newListener,
		newGrpcServerOptions,
		newGrpcServer,
		newLanternService,
		newLanternServer,
	)
	return nil, nil
}
