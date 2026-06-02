package client

import (
	"context"

	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// Ping issues a gRPC health check against the server's grpc.health.v1 service
// and returns nil iff the server reports SERVING. Useful as a readiness probe
// or as a one-liner connectivity test.
//
// The default service ("") is queried, which on Lantern's server is wired to
// the overall server status. Pass a context with a deadline to bound the call.
func (l *Lantern) Ping(ctx context.Context) error {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := healthpb.NewHealthClient(l.conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return wrapStatus(err)
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return &healthStatusError{status: resp.GetStatus()}
	}
	return nil
}

type healthStatusError struct {
	status healthpb.HealthCheckResponse_ServingStatus
}

func (e *healthStatusError) Error() string {
	return "lantern: server health status = " + e.status.String()
}
