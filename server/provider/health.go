// Package provider: health.go owns the gRPC-Health-v1 surface that the
// primary listener exposes. The underlying handler is mounted on the
// Connect mux via connectrpc.com/grpchealth in lantern_listener.go;
// HealthChecker wraps a *grpchealth.StaticChecker so the readiness
// Gate and LanternServer can drive per-service status transitions
// through a single typed API.
//
//   - SetServingStatus(service, grpchealth.Status) is the call shape
//     used at every transition site.
//   - Shutdown() flips every known service to StatusNotServing — the
//     drain signal infra probes look for during teardown.
//
// The Checker is mounted by lantern_listener.go via
// grpchealth.NewHandler(checker), so existing infra clients
// (grpc-health-probe, Kubernetes gRPC startup/liveness probes, grpcurl
// against grpc.health.v1.Health) keep working byte-for-byte.
package provider

import (
	"connectrpc.com/grpchealth"
)

// HealthChecker is the gRPC-Health-v1 implementation exposed on the
// primary listener. It wraps a *grpchealth.StaticChecker so the
// per-service status table is owned by the Connect adapter.
type HealthChecker struct {
	inner    *grpchealth.StaticChecker
	services []string
}

// NewHealthChecker constructs the checker pre-populated with the
// LanternService and LanternReplicationService entries. Both start
// Unknown; callers (LanternServer.Run + readiness.Gate) flip them to
// StatusServing / StatusNotServing via SetServingStatus as the
// lifecycle progresses. The overall ("") status is handled by
// StaticChecker's built-in process-level status.
func NewHealthChecker() *HealthChecker {
	// Registering the per-service names up front is what makes
	// grpc-health-probe with --service= work (StaticChecker only
	// answers SERVING for services passed to NewStaticChecker; any
	// unknown service maps to NotFound). Both Lantern services
	// start Unknown; LanternServer.Run flips them as it starts and
	// stops.
	//
	// We declare the service names inline (rather than importing
	// graphv1connect) to avoid the import cycle the wire layer can
	// hit when both files exist in the same package.
	services := []string{
		"graph.v1.LanternService",
		"graph.v1.LanternReplicationService",
	}
	c := grpchealth.NewStaticChecker(services...)
	return &HealthChecker{inner: c, services: services}
}

// Inner exposes the underlying *grpchealth.StaticChecker so
// lantern_listener.go can mount it via grpchealth.NewHandler. Hiding
// the field forces callers to go through SetServingStatus for state
// transitions.
func (h *HealthChecker) Inner() *grpchealth.StaticChecker { return h.inner }

// SetServingStatus flips the service entry to the supplied status.
// readiness.Gate and service.LanternServer call into this; they don't
// need to know about the Connect-side type.
func (h *HealthChecker) SetServingStatus(service string, status grpchealth.Status) {
	h.inner.SetStatus(service, status)
}

// Shutdown flips every registered service entry — and the overall
// process status — to StatusNotServing. Called from App teardown so
// probes see the drain signal before the listener stops accepting
// connections.
func (h *HealthChecker) Shutdown() {
	for _, s := range h.services {
		h.inner.SetStatus(s, grpchealth.StatusNotServing)
	}
	// StaticChecker treats SetStatus("") as the process-level entry.
	// Flipping it to NotServing is what tells overall ("") health
	// probes to drain.
	h.inner.SetStatus("", grpchealth.StatusNotServing)
}
