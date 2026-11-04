package provider

import (
	"io"
	"log/slog"
	"testing"

	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
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

func operationalPublicTestRequest(t *testing.T, manager *ManagedOperationalRuntime, primary *dataruntime.Executor, config *Config) *ManagedRequestRuntime {
	t.Helper()
	validator := NewValidationInterceptor(config.Validation)
	request, err := NewManagedRequestRuntime(t.Context(), manager, primary, validator)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func operationalPublicTestBackup(t *testing.T, manager *ManagedOperationalRuntime, primary *dataruntime.Executor, config *Config) managedBackupCertified {
	t.Helper()
	owner := backup.New(primary, config.Backup, nil, nil)
	marker, err := NewManagedBackupRuntime(t.Context(), manager, owner)
	if err != nil {
		t.Fatal(err)
	}
	return marker
}

func operationalPublicTestCertification(t *testing.T, runtime *SecurityRuntime, primary *dataruntime.Executor, slow *SlowRPCInterceptor) managedOperationalCertified {
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
	request := operationalPublicTestRequest(t, manager, primary, cfg)
	backupMarker := operationalPublicTestBackup(t, manager, primary, cfg)
	certified, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker)
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
	manager.runtime.data = securityDataFixture(t)
	primary := manager.runtime.data.NewExecutor(nil)
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
	request := operationalPublicTestRequest(t, manager, primary, cfg)
	backupMarker := operationalPublicTestBackup(t, manager, primary, cfg)
	if _, err := NewManagedOperationalCertified(t.Context(), manager.runtime, manager, primary, group, rate, request, backupMarker); err == nil {
		t.Fatal("prepared-only future group bypassed public startup refusal")
	}
}

func TestManagedOperationalCertificationRequiresExactOwners(t *testing.T) {
	runtime := &SecurityRuntime{mode: "off", data: securityDataFixture(t)}
	primary := runtime.data.NewExecutor(nil)
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
	request := operationalPublicTestRequest(t, manager, primary, cfg)
	backupMarker := operationalPublicTestBackup(t, manager, primary, cfg)
	actual, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker)
	if err != nil || actual.slowRPC != slow || actual.rate != rate.owner || actual.recovery != rate.recovery || actual.primary != primary || actual.request != request || actual.validator != request.validator || actual.backup != backupMarker.owner {
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
		if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, wrong, rate, request, backupMarker); err == nil {
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
			if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, wrong, request, backupMarker); err == nil {
				t.Fatal("foreign or absent Rate resource certified")
			}
		})
	}
	registered := manager.rate
	manager.rate = nil
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker); err == nil {
		t.Fatal("missing Rate registration certified")
	}
	manager.rate = registered
	for name, change := range map[string]func(*ManagedRequestRuntime){
		"manager":  func(r *ManagedRequestRuntime) { r.manager = &ManagedOperationalRuntime{} },
		"executor": func(r *ManagedRequestRuntime) { r.executor = runtime.data.NewExecutor(nil) },
		"owner": func(r *ManagedRequestRuntime) {
			r.owner = dataruntime.NewBootstrapRequestPolicyOwner(r.owner.RequestPolicySnapshot().Policy)
		},
		"validator": func(r *ManagedRequestRuntime) { r.validator = NewValidationInterceptor(cfg.Validation) },
	} {
		t.Run("Request/"+name, func(t *testing.T) {
			original := *request
			change(request)
			_, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker)
			*request = original
			if err == nil {
				t.Fatal("foreign Request resource certified")
			}
		})
	}
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, runtime.data.NewExecutor(nil), group, rate, request, backupMarker); err == nil {
		t.Fatal("equal-runtime foreign primary certified")
	}
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, nil, backupMarker); err == nil {
		t.Fatal("absent Request resource certified")
	}
	requestEntry := manager.request
	manager.request = nil
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker); err == nil {
		t.Fatal("missing Request registration certified")
	}
	manager.request = requestEntry
	foreignOwner := dataruntime.NewBootstrapRequestPolicyOwner(request.owner.RequestPolicySnapshot().Policy)
	request.validator.WithRequestPolicySource(foreignOwner)
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker); err == nil {
		t.Fatal("substituted validator policy source certified")
	}
	request.validator.WithRequestPolicySource(request.owner)
	primary.WithRequestPolicySource(foreignOwner)
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker); err == nil {
		t.Fatal("substituted executor policy source certified")
	}
	primary.WithRequestPolicySource(request.owner)
	for name, change := range map[string]func(*managedBackupCertified){
		"manager":       func(c *managedBackupCertified) { c.manager = &ManagedOperationalRuntime{} },
		"missing owner": func(c *managedBackupCertified) { c.owner = nil },
		"foreign owner": func(c *managedBackupCertified) {
			c.owner = backup.New(runtime.data.NewExecutor(nil), cfg.Backup, nil, nil)
		},
		"missing adapter": func(c *managedBackupCertified) { c.adapter = nil },
		"foreign adapter": func(c *managedBackupCertified) { c.adapter = &managedBackupRuntime{owner: c.owner} },
	} {
		t.Run("Backup/"+name, func(t *testing.T) {
			wrong := backupMarker
			change(&wrong)
			if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, wrong); err == nil {
				t.Fatal("foreign or absent Backup producer certified")
			}
		})
	}
	backupEntry := manager.backup
	manager.backup = nil
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker); err == nil {
		t.Fatal("missing Backup registration certified")
	}
	manager.backup = backupEntry
}

func TestManagedOperationalCertificationKeepsInvalidRateAvailableForRepair(t *testing.T) {
	for _, raw := range []string{"NaN", "+Inf", "-Inf"} {
		t.Run(raw, func(t *testing.T) {
			cfg, err := LoadConfig(envconfig.NewReader([]string{"LANTERN_RATE_LIMIT_RPS=" + raw}))
			if err != nil {
				t.Fatal(err)
			}
			runtime := &SecurityRuntime{mode: "off", data: securityDataFixture(t)}
			primary := runtime.data.NewExecutor(nil)
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
			request := operationalPublicTestRequest(t, manager, primary, cfg)
			backupMarker := operationalPublicTestBackup(t, manager, primary, cfg)
			certified, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, slowMarker, rate, request, backupMarker)
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
