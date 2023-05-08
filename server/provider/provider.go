package provider

import (
	v1 "github.com/anaregdesign/lantern-proto/go/graph/v1"
	"github.com/anaregdesign/papaya/cache/graph"
	"google.golang.org/grpc"
	"net"
	"os"
	"strconv"
	"time"
)

type Config struct {
	ttl  time.Duration
	port int
}

func NewConfig() *Config {
	ttl, err := strconv.Atoi(os.Getenv("LANTERN_DEFAULT_TTL_SECONDS"))
	if err != nil {
		ttl = 60
	}

	port, err := strconv.Atoi(os.Getenv("LANTERN_PORT"))
	if err != nil {
		port = 6380
	}

	return &Config{
		ttl:  time.Duration(ttl) * time.Second,
		port: port,
	}
}

func NewGraphCache(c *Config) *graph.GraphCache[string, *v1.Vertex] {
	return graph.NewGraphCache[string, *v1.Vertex](c.ttl)
}

func NewListener() (net.Listener, error) {
	return net.Listen("tcp", ":"+strconv.Itoa(NewConfig().port))
}

func NewGrpcServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{}
}

func NewGrpcServer(options []grpc.ServerOption) *grpc.Server {
	return grpc.NewServer(options...)
}
