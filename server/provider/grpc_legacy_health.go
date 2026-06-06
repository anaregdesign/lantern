// Package provider: grpc_legacy_health.go retains the
// google.golang.org/grpc/health/v1 server constructors purely so
// legacy integration tests that build their own grpc.NewServer
// harnesses (tests/integration/health_test.go) keep compiling.
//
// The primary :6380 listener (lantern_listener.go) does NOT call any
// of these — it mounts the gRPC-Health-v1 surface via
// connectrpc.com/grpchealth and the HealthChecker wrapper. Full
// deletion is deferred to #342.
package provider

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// NewHealthServer returns a fresh google.golang.org/grpc/health-flavoured
// health server. Test-only.
func NewHealthServer() *health.Server { return health.NewServer() }

// RegisterHealth wires the gRPC health service onto an existing
// *grpc.Server. Test-only.
func RegisterHealth(s *grpc.Server, hs *health.Server) {
	healthpb.RegisterHealthServer(s, hs)
}
