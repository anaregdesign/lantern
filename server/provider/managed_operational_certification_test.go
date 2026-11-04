package provider

import (
	"io"
	"log/slog"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

func operationalPublicTestRate(t *testing.T, manager *ManagedOperationalRuntime, config RateLimitConfig) managedRateCertified {
	t.Helper()
	owner := NewRateLimitInterceptorProvider(config, nil)
	recovery, err := NewRateRecoveryRuntime(manager.runtime, owner)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := NewManagedRateRuntime(t.Context(), manager, owner, recovery)
	if err != nil {
		t.Fatal(err)
	}
	return marker
}

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
	rate := operationalPublicTestRate(t, manager, cfg.RateLimit)
	certified, err := NewManagedOperationalCertified(t.Context(), runtime, manager, group, rate)
	if err != nil {
		t.Fatal(err)
	}
	return certified
}

func TestManagedOperationalCertificationRefusesUnsupportedPreparedGroup(t *testing.T) {
	p := operationalRuntimeTestPrepare(operationalRuntimeTestProjection(1), operationalpolicy.GroupSearch)
	manager, _ := operationalRuntimeTestManager(t, p)
	// The authority fake tests reader-floor enforcement. Its resource owners
	// use the real OFF binding rather than inventing an OIDC recovery grant.
	manager.runtime.mode = "off"
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
	rate := operationalPublicTestRate(t, manager, cfg.RateLimit)
	if _, err := NewManagedOperationalCertified(t.Context(), manager.runtime, manager, group, rate); err == nil {
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
	rate := operationalPublicTestRate(t, manager, cfg.RateLimit)
	actual, err := NewManagedOperationalCertified(t.Context(), runtime, manager, group, rate)
	if err != nil || actual.slowRPC != slow || actual.rate != rate.owner || actual.recovery != rate.recovery {
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
		if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, wrong, rate); err == nil {
			t.Fatal("foreign or absent resource certified")
		}
	}
	for name, change := range map[string]func(*managedRateCertified){
		"manager":       func(c *managedRateCertified) { c.manager = &ManagedOperationalRuntime{} },
		"owner":         func(c *managedRateCertified) { c.owner = NewRateLimitInterceptor(0, 0) },
		"missing owner": func(c *managedRateCertified) { c.owner = nil },
		"adapter": func(c *managedRateCertified) {
			c.adapter = &managedRateRuntime{manager: manager, owner: c.owner, recovery: c.recovery}
		},
		"missing recovery": func(c *managedRateCertified) { c.recovery = nil },
		"foreign recovery": func(c *managedRateCertified) { c.recovery = &rateRecoveryRuntime{runtime: runtime, owner: c.owner} },
	} {
		t.Run(name, func(t *testing.T) {
			wrong := rate
			change(&wrong)
			if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, group, wrong); err == nil {
				t.Fatal("foreign or absent Rate resource certified")
			}
		})
	}
	registered := manager.rate
	manager.rate = nil
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, group, rate); err == nil {
		t.Fatal("missing Rate registration certified")
	}
	manager.rate = registered
}

func TestManagedOperationalCertificationKeepsInvalidRateAvailableForRepair(t *testing.T) {
	for _, raw := range []string{"NaN", "+Inf", "-Inf"} {
		t.Run(raw, func(t *testing.T) {
			cfg, err := LoadConfig(envconfig.NewReader([]string{"LANTERN_RATE_LIMIT_RPS=" + raw}))
			if err != nil {
				t.Fatal(err)
			}
			runtime := &SecurityRuntime{mode: "off"}
			manager, err := NewManagedOperationalRuntime(t.Context(), runtime, NewOperationalBootstrapConfig(cfg))
			if err != nil {
				t.Fatal(err)
			}
			slow := NewSlowRPCInterceptor(0, slog.New(slog.NewTextHandler(io.Discard, nil)))
			slowMarker, err := NewManagedSlowRPCRuntime(t.Context(), manager, slow, cfg.Observability)
			if err != nil {
				t.Fatal(err)
			}
			rate := operationalPublicTestRate(t, manager, cfg.RateLimit)
			certified, err := NewManagedOperationalCertified(t.Context(), runtime, manager, slowMarker, rate)
			if err != nil || certified.rate != rate.owner || certified.recovery != rate.recovery || rate.owner.Allow() {
				t.Fatal("invalid bootstrap lost its installed repair owner", err)
			}
			observation, err := rate.adapter.ObserveOperationalPolicy()
			if err != nil || observation.Effective != nil || observation.Error != securitytransport.OperationalErrorInvalidPolicy {
				t.Fatal("invalid Rate bootstrap became a fabricated effective value", observation, err)
			}
		})
	}
}
