package provider

import (
	"io"
	"log/slog"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

func operationalPublicTestCertification(t *testing.T, runtime *SecurityRuntime, slow *SlowRPCInterceptor) managedOperationalCertified {
	t.Helper()
	cfg, err := LoadConfig(envconfig.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManagedOperationalRuntime(t.Context(), runtime, NewOperationalBootstrapConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	group, err := NewManagedSlowRPCRuntime(t.Context(), manager, slow, ObservabilityConfig{SlowRPCThreshold: slow.Threshold()})
	if err != nil {
		t.Fatal(err)
	}
	certified, err := NewManagedOperationalCertified(t.Context(), runtime, manager, group)
	if err != nil {
		t.Fatal(err)
	}
	return certified
}

func TestManagedOperationalCertificationRefusesUnsupportedPreparedGroup(t *testing.T) {
	p := operationalRuntimeTestPrepare(operationalRuntimeTestProjection(1), operationalpolicy.GroupSearch)
	manager, _ := operationalRuntimeTestManager(t, p)
	manager.runtime.managedOperational = manager
	cfg, err := LoadConfig(envconfig.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	manager.bootstrap = NewOperationalBootstrapConfig(cfg)
	owner := NewSlowRPCInterceptor(0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	group, err := NewManagedSlowRPCRuntime(t.Context(), manager, owner, ObservabilityConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewManagedOperationalCertified(t.Context(), manager.runtime, manager, group); err == nil {
		t.Fatal("prepared-only future group bypassed public startup refusal")
	}
}

func TestManagedOperationalCertificationRequiresExactOwners(t *testing.T) {
	runtime := &SecurityRuntime{mode: "off"}
	cfg, err := LoadConfig(envconfig.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManagedOperationalRuntime(t.Context(), runtime, NewOperationalBootstrapConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	slow := NewSlowRPCInterceptor(0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	group, err := NewManagedSlowRPCRuntime(t.Context(), manager, slow, ObservabilityConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, group); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*managedSlowRPCCertified){
		func(c *managedSlowRPCCertified) { c.owner = nil },
		func(c *managedSlowRPCCertified) { c.owner = NewSlowRPCInterceptor(0, slow.logger) },
		func(c *managedSlowRPCCertified) { c.adapter = &managedSlowRPCRuntime{owner: slow} },
		func(c *managedSlowRPCCertified) { c.manager = &ManagedOperationalRuntime{} },
	} {
		wrong := group
		change(&wrong)
		if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, wrong); err == nil {
			t.Fatal("foreign or absent resource certified")
		}
	}
}
