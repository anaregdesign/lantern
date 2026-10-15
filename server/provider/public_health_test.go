package provider

import (
	"bytes"
	"connectrpc.com/grpchealth"
	"github.com/anaregdesign/lantern/server/internal/readinessdiag"
	"testing"
	"time"
)

func TestPublicHealthObservationPreservesShortCircuitAndClockSamples(t *testing.T) {
	cfg, data, clock := securityRuntimeFixture(t)
	samples := 0
	cfg.Clock = func() time.Time { samples++; return clock.Now() }
	r, cleanup, err := NewSecurityRuntime(cfg, data)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
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
	if s := r.readinessObservation.Snapshot(); s.HealthBranch != "runtime_rejection" || s.HealthReason != "restart_wait" {
		t.Fatal(s)
	}
	clock.advance(35 * time.Second)
	response, err = check.Check(t.Context(), &grpchealth.CheckRequest{})
	if err != nil || response.Status != grpchealth.StatusServing || samples-before != 2 {
		t.Fatal("ready check changed authority samples")
	}
	clock.advance(-time.Nanosecond)
	_, _ = check.Check(t.Context(), &grpchealth.CheckRequest{})
	if s := r.readinessObservation.Snapshot(); s.HealthReason != "clock_fault" || samples-before != 3 {
		t.Fatal(s)
	}
	_, _ = check.Check(t.Context(), &grpchealth.CheckRequest{Service: "absent"})
	if s := r.readinessObservation.Snapshot(); s.HealthBranch != "error" || s.HealthReason != "not_evaluated" || samples-before != 3 {
		t.Fatal(s)
	}
}

func TestPublicHealthCannotBypassAuthorityWithNamedService(t *testing.T) {
	cfg, data, clock := securityRuntimeFixture(t)
	runtime, cleanup, err := NewSecurityRuntime(cfg, data)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
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
	clock.advance(35 * time.Second)
	response, err := check.Check(t.Context(), &grpchealth.CheckRequest{Service: "graph.v1.LanternService"})
	if err != nil || response.Status != grpchealth.StatusServing {
		t.Fatal(err)
	}
}
