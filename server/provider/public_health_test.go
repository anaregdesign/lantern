package provider

import (
	"connectrpc.com/grpchealth"
	"testing"
	"time"
)

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
