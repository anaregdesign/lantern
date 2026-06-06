// Package provider: health.go owns the gRPC-Health-v1 surface that the
// new primary listener exposes (#347). The legacy *health.Server from
// google.golang.org/grpc/health is replaced by a thin adapter over
// connectrpc.com/grpchealth.StaticChecker so the same SetServingStatus
// API the readiness Gate and LanternServer drive continues to work, but
// the underlying handler is mounted on the Connect mux instead of a
// grpc.Server.
//
// The wrapper preserves the historical surface:
//   - SetServingStatus(service, healthpb.HealthCheckResponse_ServingStatus)
//     mirrors *grpc/health/v1.Server.SetServingStatus exactly so neither
//     readiness.Gate nor service.LanternServer notice the swap.
//   - Shutdown() flips every known service to NOT_SERVING — the same end
//     state *health.Server.Shutdown() leaves the table in.
//
// The Checker is mounted by lantern_listener.go via
// grpchealth.NewHandler(checker), so existing infra clients
// (grpc-health-probe, Kubernetes gRPC startup/liveness probes, grpcurl
// against grpc.health.v1.Health) keep working byte-for-byte.
package provider

import (
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"connectrpc.com/grpchealth"
)

// HealthChecker is the gRPC-Health-v1 implementation exposed on the
// primary listener. It wraps a *grpchealth.StaticChecker so the
// per-service status table is owned by the Connect adapter, but the
// SetServingStatus / Shutdown API is shaped like the legacy
// google.golang.org/grpc/health.Server so existing wiring continues to
// work unmodified.
type HealthChecker struct {
	inner    *grpchealth.StaticChecker
	services []string
}

// NewHealthChecker constructs the checker pre-populated with the
// LanternService and LanternReplicationService entries. Both start
// Unknown; callers (LanternServer.Run + readiness.Gate) flip them to
// SERVING / NOT_SERVING via SetServingStatus as the lifecycle
// progresses. The overall ("") status is handled by StaticChecker's
// built-in process-level status.
func NewHealthChecker() *HealthChecker {
	// Registering the per-service names up front is what makes
	// grpc-health-probe with --service= work (StaticChecker only
	// answers SERVING for services passed to NewStaticChecker; any
	// unknown service maps to NotFound). Both Lantern services
	// start Unknown; LanternServer.Run flips them as it starts and
	// stops.
	services := []string{
		// Mirror the gRPC service names: graph.v1.LanternService /
		// graph.v1.LanternReplicationService. The constants live in
		// the generated graphv1connect package; declaring them
		// inline here avoids the import cycle the wire layer can
		// hit when both files exist in the same package.
		"graph.v1.LanternService",
		"graph.v1.LanternReplicationService",
	}
	c := grpchealth.NewStaticChecker(services...)
	return &HealthChecker{inner: c, services: services}
}

// Inner exposes the underlying *grpchealth.StaticChecker so
// lantern_listener.go can mount it via grpchealth.NewHandler. Hiding
// the field forces callers to go through SetServingStatus for state
// transitions, which is the contract HealthSetter promises.
func (h *HealthChecker) Inner() *grpchealth.StaticChecker { return h.inner }

// SetServingStatus mirrors *grpc/health/v1.Server.SetServingStatus so
// callers don't notice the Connect swap. UNKNOWN maps to
// grpchealth.StatusUnknown; SERVING to StatusServing; NOT_SERVING to
// StatusNotServing; SERVICE_UNKNOWN is collapsed to StatusUnknown
// because StaticChecker has no equivalent (it returns NotFound for
// services not registered at construction, which is the same
// observable behaviour for a probe).
func (h *HealthChecker) SetServingStatus(service string, status healthpb.HealthCheckResponse_ServingStatus) {
	h.inner.SetStatus(service, mapServingStatus(status))
}

// Shutdown flips every registered service entry — and the overall
// process status — to NOT_SERVING. Called from App teardown to
// mirror *health.Server.Shutdown() so probes see the drain signal
// before the listener stops accepting connections.
func (h *HealthChecker) Shutdown() {
	for _, s := range h.services {
		h.inner.SetStatus(s, grpchealth.StatusNotServing)
	}
	// StaticChecker treats SetStatus("") as the process-level entry.
	// Flipping it to NotServing is what tells overall ("") health
	// probes to drain.
	h.inner.SetStatus("", grpchealth.StatusNotServing)
}

func mapServingStatus(s healthpb.HealthCheckResponse_ServingStatus) grpchealth.Status {
	switch s {
	case healthpb.HealthCheckResponse_SERVING:
		return grpchealth.StatusServing
	case healthpb.HealthCheckResponse_NOT_SERVING:
		return grpchealth.StatusNotServing
	default:
		return grpchealth.StatusUnknown
	}
}
