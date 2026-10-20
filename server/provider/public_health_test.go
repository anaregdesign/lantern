package provider

import (
	"bytes"
	"testing"
	"time"

	"connectrpc.com/grpchealth"
	"github.com/anaregdesign/lantern/server/internal/readinessdiag"
)

func TestPublicHealthObservationPreservesShortCircuitAndClockSamples(t *testing.T) {
	clock := &securityTestClock{now: time.Now()}
	samples := 0
	peer := securityWorkloadFixture(t, func() time.Time { samples++; return clock.Now() })
	clock.advance(2 * time.Minute)
	r := &SecurityRuntime{mode: "off", peer: peer}
	hc := NewHealthChecker()
	hc.SetServingStatus("", grpchealth.StatusNotServing)
	var output bytes.Buffer
	r.readinessObservation = readinessdiag.New(&output)
	check := publicHealthChecker{hc.Inner(), r}
	before := samples
	response, err := check.Check(t.Context(), &grpchealth.CheckRequest{})
	if err != nil || response.Status != grpchealth.StatusNotServing || samples != before {
		t.Fatal("static rejection sampled authority")
	}
	if s := r.readinessObservation.Snapshot(); s.HealthBranch != "static_rejection" || s.HealthReason != "not_evaluated" || s.WorkerState != "never_started" {
		t.Fatal(s)
	}
	hc.SetServingStatus("", grpchealth.StatusServing)
	response, err = check.Check(t.Context(), &grpchealth.CheckRequest{})
	if err != nil || response.Status != grpchealth.StatusNotServing || samples-before != 1 {
		t.Fatal("runtime rejection changed authority samples")
	}
	if s := r.readinessObservation.Snapshot(); s.HealthBranch != "runtime_rejection" || s.HealthReason != "unavailable" {
		t.Fatal(s)
	}
	// A different healthy owner proves the ready branch; the expired owner is
	// never repaired by moving its scalar clock backward.
	clock = &securityTestClock{now: time.Now()}
	r.peer = securityWorkloadFixture(t, func() time.Time { samples++; return clock.Now() })
	before = samples
	response, err = check.Check(t.Context(), &grpchealth.CheckRequest{})
	if err != nil || response.Status != grpchealth.StatusServing || samples-before != 1 {
		t.Fatal("ready check changed authority samples")
	}
	clock.advance(-time.Nanosecond)
	_, _ = check.Check(t.Context(), &grpchealth.CheckRequest{})
	if s := r.readinessObservation.Snapshot(); s.HealthReason != "unavailable" || samples-before != 2 {
		t.Fatal(s)
	}
	_, _ = check.Check(t.Context(), &grpchealth.CheckRequest{Service: "absent"})
	if s := r.readinessObservation.Snapshot(); s.HealthBranch != "error" || s.HealthReason != "not_evaluated" || samples-before != 2 {
		t.Fatal(s)
	}
}

func TestPublicHealthCannotBypassAuthorityWithNamedService(t *testing.T) {
	runtime := &SecurityRuntime{mode: "oidc"}
	hc := NewHealthChecker()
	hc.SetServingStatus("", grpchealth.StatusServing)
	hc.SetServingStatus("graph.v1.LanternService", grpchealth.StatusServing)
	check := publicHealthChecker{hc.Inner(), runtime}
	for _, service := range []string{"", "graph.v1.LanternService"} {
		response, err := check.Check(t.Context(), &grpchealth.CheckRequest{Service: service})
		if err != nil || response.Status != grpchealth.StatusNotServing {
			t.Fatal("named health bypassed authority", service, err)
		}
	}
	check.runtime = &SecurityRuntime{mode: "off"}
	response, err := check.Check(t.Context(), &grpchealth.CheckRequest{Service: "graph.v1.LanternService"})
	if err != nil || response.Status != grpchealth.StatusServing {
		t.Fatal(err)
	}
}
