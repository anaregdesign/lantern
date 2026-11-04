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
	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
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

func operationalPublicTestCapacity(t *testing.T, manager *ManagedOperationalRuntime, primary *dataruntime.Executor, config *Config) managedCapacityCertified {
	t.Helper()
	cache := config.Cache
	owner, err := dataruntime.NewGraphPolicyOwner(manager.runtime.data, primary, dataruntime.GraphPolicyConfig{
		MaxVertices: max(cache.MaxVertices, 0), MaxEdges: max(cache.MaxEdges, 0),
		MaxVertexCausalEntries: cache.MaxVertexCausalEntries, MaxEdgeCausalEntries: cache.MaxEdgeCausalEntries,
		GCEdgeBudget: cache.GCEdgeBudget, GCInterval: cache.GCInterval,
	})
	if err != nil {
		t.Fatal(err)
	}
	marker, err := NewManagedCapacityRuntime(t.Context(), manager, owner)
	if err != nil {
		t.Fatal(err)
	}
	return marker
}

func operationalPublicTestLifecycle(t *testing.T, manager *ManagedOperationalRuntime, config *Config) managedLifecycleCertified {
	t.Helper()
	gate := readiness.NewGate(config.Readiness.MaxLag, false, nil)
	marker, err := NewManagedLifecycleRuntime(t.Context(), manager, config.Shutdown, config.Readiness, gate)
	if err != nil {
		t.Fatal(err)
	}
	return marker
}

func operationalPublicTestSearch(t *testing.T, manager *ManagedOperationalRuntime, primary *dataruntime.Executor) managedSearchCertified {
	t.Helper()
	marker, err := NewManagedSearchRuntime(t.Context(), manager, primary)
	if err != nil {
		t.Fatal(err)
	}
	return marker
}

func operationalPublicTestDataCertification(t *testing.T, manager *ManagedOperationalRuntime, primary *dataruntime.Executor) runtimeCertified {
	t.Helper()
	data := manager.runtime.data
	peer, err := data.NewReplication(primary)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewRuntimeRestored(data, primary)
	if err != nil {
		t.Fatal(err)
	}
	certified, err := NewRuntimeCertified(data, primary, peer, restored, NetConfig{})
	if err != nil {
		t.Fatal(err)
	}
	return certified
}

func operationalPublicTestReplication(t *testing.T, manager *ManagedOperationalRuntime, primary *dataruntime.Executor, config *Config, certified runtimeCertified, search managedSearchCertified) managedReplicationCertified {
	t.Helper()
	marker, err := NewManagedReplicationRuntime(t.Context(), manager, config.Peer, config.AntiEntropy, nil, config.Replication, nil, primary, certified.runtime.GraphCache(), nil, nil, nil, nil, certified, search)
	if err != nil {
		t.Fatal(err)
	}
	return marker
}

func operationalPublicTestCertification(t *testing.T, runtime *SecurityRuntime, primary *dataruntime.Executor, slow *SlowRPCInterceptor, dataCertified runtimeCertified) managedOperationalCertified {
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
	capacityMarker := operationalPublicTestCapacity(t, manager, primary, cfg)
	lifecycleMarker := operationalPublicTestLifecycle(t, manager, cfg)
	searchMarker := operationalPublicTestSearch(t, manager, primary)
	replicationMarker := operationalPublicTestReplication(t, manager, primary, cfg, dataCertified, searchMarker)
	certified, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker)
	if err != nil {
		t.Fatal(err)
	}
	return certified
}

func TestManagedOperationalCertificationRefusesMissingPreparedOwner(t *testing.T) {
	p := operationalRuntimeTestPrepare(operationalRuntimeTestProjection(1), operationalpolicy.GroupReplication)
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
	capacityMarker := operationalPublicTestCapacity(t, manager, primary, cfg)
	lifecycleMarker := operationalPublicTestLifecycle(t, manager, cfg)
	searchMarker := operationalPublicTestSearch(t, manager, primary)
	replicationMarker := managedReplicationCertified{}
	if _, err := NewManagedOperationalCertified(t.Context(), manager.runtime, manager, primary, group, rate, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker); err == nil {
		t.Fatal("prepared Replication without its actual owner bypassed public startup refusal")
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
	capacityMarker := operationalPublicTestCapacity(t, manager, primary, cfg)
	lifecycleMarker := operationalPublicTestLifecycle(t, manager, cfg)
	searchMarker := operationalPublicTestSearch(t, manager, primary)
	replicationMarker := operationalPublicTestReplication(t, manager, primary, cfg, operationalPublicTestDataCertification(t, manager, primary), searchMarker)
	actual, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker)
	if err != nil || actual.slowRPC != slow || actual.rate != rate.owner || actual.recovery != rate.recovery || actual.primary != primary || actual.request != request || actual.validator != request.validator || actual.backup != backupMarker.owner || actual.capacity != capacityMarker.owner || actual.plan != lifecycleMarker.plan || actual.search != searchMarker || actual.pump != replicationMarker.pump || actual.anti != replicationMarker.anti {
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
		if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, wrong, rate, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker); err == nil {
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
			if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, wrong, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker); err == nil {
				t.Fatal("foreign or absent Rate resource certified")
			}
		})
	}
	registered := manager.rate
	manager.rate = nil
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker); err == nil {
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
			_, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker)
			*request = original
			if err == nil {
				t.Fatal("foreign Request resource certified")
			}
		})
	}
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, runtime.data.NewExecutor(nil), group, rate, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker); err == nil {
		t.Fatal("equal-runtime foreign primary certified")
	}
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, nil, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker); err == nil {
		t.Fatal("absent Request resource certified")
	}
	requestEntry := manager.request
	manager.request = nil
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker); err == nil {
		t.Fatal("missing Request registration certified")
	}
	manager.request = requestEntry
	foreignOwner := dataruntime.NewBootstrapRequestPolicyOwner(request.owner.RequestPolicySnapshot().Policy)
	request.validator.WithRequestPolicySource(foreignOwner)
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker); err == nil {
		t.Fatal("substituted validator policy source certified")
	}
	request.validator.WithRequestPolicySource(request.owner)
	primary.WithRequestPolicySource(foreignOwner)
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker); err == nil {
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
			if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, wrong, capacityMarker, lifecycleMarker, searchMarker, replicationMarker); err == nil {
				t.Fatal("foreign or absent Backup producer certified")
			}
		})
	}
	backupEntry := manager.backup
	manager.backup = nil
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker); err == nil {
		t.Fatal("missing Backup registration certified")
	}
	manager.backup = backupEntry
	for name, change := range map[string]func(*managedCapacityCertified){
		"manager":         func(c *managedCapacityCertified) { c.manager = &ManagedOperationalRuntime{} },
		"missing owner":   func(c *managedCapacityCertified) { c.owner = nil },
		"foreign owner":   func(c *managedCapacityCertified) { c.owner = &dataruntime.GraphPolicyOwner{} },
		"missing adapter": func(c *managedCapacityCertified) { c.adapter = nil },
		"foreign adapter": func(c *managedCapacityCertified) { c.adapter = &managedCapacityRuntime{owner: c.owner} },
	} {
		t.Run("Capacity/"+name, func(t *testing.T) {
			wrong := capacityMarker
			change(&wrong)
			if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker, wrong, lifecycleMarker, searchMarker, replicationMarker); err == nil {
				t.Fatal("foreign or absent Capacity owner certified")
			}
		})
	}
	capacityEntry := manager.capacity
	manager.capacity = nil
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker); err == nil {
		t.Fatal("missing Capacity registration certified")
	}
	manager.capacity = capacityEntry
	for name, change := range map[string]func(*managedLifecycleCertified){
		"manager":       func(c *managedLifecycleCertified) { c.manager = &ManagedOperationalRuntime{} },
		"missing owner": func(c *managedLifecycleCertified) { c.owner = nil },
		"foreign owner": func(c *managedLifecycleCertified) {
			c.owner = service.NewBootstrapLifecyclePolicyOwner(c.owner.LifecycleSnapshot().Policy)
		},
		"missing plan": func(c *managedLifecycleCertified) { c.plan = nil },
		"foreign plan": func(c *managedLifecycleCertified) {
			var planErr error
			c.plan, planErr = service.NewShutdownPlan(c.owner)
			if planErr != nil {
				t.Fatal(planErr)
			}
		},
		"missing gate":    func(c *managedLifecycleCertified) { c.gate = nil },
		"foreign gate":    func(c *managedLifecycleCertified) { c.gate = readiness.NewGate(cfg.Readiness.MaxLag, false, nil) },
		"missing adapter": func(c *managedLifecycleCertified) { c.adapter = nil },
		"foreign adapter": func(c *managedLifecycleCertified) {
			c.adapter = &managedLifecycleRuntime{owner: c.owner, plan: c.plan, gate: c.gate}
		},
	} {
		t.Run("Lifecycle/"+name, func(t *testing.T) {
			wrong := lifecycleMarker
			change(&wrong)
			if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker, capacityMarker, wrong, searchMarker, replicationMarker); err == nil {
				t.Fatal("foreign or absent Lifecycle owner, gate or plan certified")
			}
		})
	}
	lifecycleEntry := manager.lifecycle
	manager.lifecycle = nil
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker); err == nil {
		t.Fatal("missing Lifecycle registration certified")
	}
	manager.lifecycle = lifecycleEntry
	for name, change := range map[string]func(*managedSearchCertified){
		"manager":              func(c *managedSearchCertified) { c.manager = &ManagedOperationalRuntime{} },
		"primary":              func(c *managedSearchCertified) { c.primary = runtime.data.NewExecutor(nil) },
		"missing runtime":      func(c *managedSearchCertified) { c.runtime = nil },
		"foreign runtime":      func(c *managedSearchCertified) { c.runtime = &ManagedSearchRuntime{manager: manager, primary: primary} },
		"missing installation": func(c *managedSearchCertified) { c.installation = dataruntime.SearchPolicyInstallation{} },
	} {
		t.Run("Search/"+name, func(t *testing.T) {
			wrong := searchMarker
			change(&wrong)
			if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker, capacityMarker, lifecycleMarker, wrong, replicationMarker); err == nil {
				t.Fatal("foreign or absent Search installation certified")
			}
		})
	}
	searchEntry := manager.search
	manager.search = nil
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker); err == nil {
		t.Fatal("missing Search registration certified")
	}
	manager.search = searchEntry
	for name, change := range map[string]func(*managedReplicationCertified){
		"manager": func(c *managedReplicationCertified) { c.manager = &ManagedOperationalRuntime{} },
		"primary": func(c *managedReplicationCertified) { c.primary = runtime.data.NewExecutor(nil) },
		"missing owner": func(c *managedReplicationCertified) { c.owner = nil },
		"foreign owner": func(c *managedReplicationCertified) { copied := *c.owner; c.owner = &copied },
		"missing Pump": func(c *managedReplicationCertified) { c.pump = nil },
		"missing anti-entropy": func(c *managedReplicationCertified) { c.anti = nil },
		"equal fingerprint foreign pair": func(c *managedReplicationCertified) {
			fingerprint := primary.SearchConfigFingerprint()
			c.pump = replication.NewPump(replication.Config{SearchConfigFingerprint: fingerprint}, nil, nil)
			c.anti = replication.NewAntiEntropy(replication.AntiEntropyConfig{Pump: c.pump, SearchConfigFingerprint: fingerprint}, nil, nil, nil)
		},
	} {
		t.Run("Replication/"+name, func(t *testing.T) {
			wrong := replicationMarker
			change(&wrong)
			if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, wrong); err == nil {
				t.Fatal("foreign or absent actual replication workers certified")
			}
		})
	}
	replicationEntry := manager.replication
	manager.replication = nil
	if _, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, group, rate, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker); err == nil {
		t.Fatal("missing Replication registration certified")
	}
	manager.replication = replicationEntry
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
			capacityMarker := operationalPublicTestCapacity(t, manager, primary, cfg)
			lifecycleMarker := operationalPublicTestLifecycle(t, manager, cfg)
			searchMarker := operationalPublicTestSearch(t, manager, primary)
			replicationMarker := operationalPublicTestReplication(t, manager, primary, cfg, operationalPublicTestDataCertification(t, manager, primary), searchMarker)
			certified, err := NewManagedOperationalCertified(t.Context(), runtime, manager, primary, slowMarker, rate, request, backupMarker, capacityMarker, lifecycleMarker, searchMarker, replicationMarker)
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
