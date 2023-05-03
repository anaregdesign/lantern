package provider

import (
	v1 "github.com/anaregdesign/lantern-proto/go/graph/v1"
	"github.com/anaregdesign/papaya/cache/graph"
	"google.golang.org/grpc"
	"net"
	"time"
)

type Config struct {
	ttl time.Duration
}

func NewConfig() *Config {
	return &Config{
		ttl: 1 * time.Minute,
	}
}

func NewGraphCache(c *Config) *graph.GraphCache[string, *v1.Vertex] {
	return graph.NewGraphCache[string, *v1.Vertex](c.ttl)
}

func NewListener() (net.Listener, error) {
	return net.Listen("tcp", ":8080")
}

func NewGrpcServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{}
}

func NewGrpcServer(options []grpc.ServerOption) *grpc.Server {
	return grpc.NewServer(options...)
}
