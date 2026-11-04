package provider

import (
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

// The authority fake proves provider composition only. These tests do not
// confer native admission, qualify a restart recovery capsule, or run workers.
func replicationBindingFixture(t *testing.T, projection security.OperationalPolicyProjection, environment ...string) (*ManagedOperationalRuntime, *operationalAuthorityTestFake, *Config, *dataruntime.Executor, runtimeCertified, managedSearchCertified) {
	t.Helper()
	config, err := LoadConfig(envconfig.NewReader(environment))
	if err != nil {
		t.Fatal(err)
	}
	data, primary := managedSearchTestPrimary(t)
	manager, authority := operationalRuntimeTestManager(t, projection)
	manager.bootstrap = NewOperationalBootstrapConfig(config)
	manager.runtime.data = data
	manager.runtime.managedOperational = manager
	search, err := NewManagedSearchRuntime(t.Context(), manager, primary)
	if err != nil {
		t.Fatal(err)
	}
	replication, err := data.NewReplication(primary)
	if err != nil {
		t.Fatal(err)
	}
	certified := runtimeCertified{valid: true, runtime: data, primary: primary, replication: replication}
	return manager, authority, config, primary, certified, search
}

func constructReplicationBinding(t *testing.T, manager *ManagedOperationalRuntime, config *Config, primary *dataruntime.Executor, certified runtimeCertified, search managedSearchCertified) (managedReplicationCertified, error) {
	t.Helper()
	return NewManagedReplicationRuntime(t.Context(), manager, config.Peer, config.AntiEntropy, nil, config.Replication, nil, primary, certified.runtime.GraphCache(), nil, nil, nil, nil, certified, search)
}

func TestManagedReplicationBindingRestoresActualWorkersAndStagesOnly(t *testing.T) {
	values, _ := operationalpolicy.Defaults(operationalpolicy.GroupReplication)
	values.Replication.PumpBackoffMinMS = 17
	values.Replication.PumpBackoffMaxMS = 53
	values.Replication.AntiEntropyIntervalMS = 0
	policy := &operationalpolicy.Policy{Version: 1, Generation: 7, Values: values}
	projection := operationalRuntimeTestPrepare(operationalRuntimeTestProjection(3), operationalpolicy.GroupReplication)
	if err := projection.Policies.Set(operationalpolicy.GroupReplication, policy); err != nil {
		t.Fatal(err)
	}
	manager, authority, config, primary, runtime, search := replicationBindingFixture(t, projection, "LANTERN_PUMP_BACKOFF_MIN_MS=999", "LANTERN_PUMP_BACKOFF_MAX_MS=1001")
	workers, err := constructReplicationBinding(t, manager, config, primary, runtime, search)
	if err != nil {
		t.Fatal(err)
	}
	pump, _ := workers.pump.Scheduling()
	anti, _ := workers.anti.Scheduling()
	if pump.BackoffMin != 17*time.Millisecond || pump.BackoffMax != 53*time.Millisecond || anti.Interval != 0 || anti.Pump != workers.pump || pump.SearchFingerprint != primary.SearchConfigFingerprint() || anti.SearchFingerprint != primary.SearchConfigFingerprint() {
		t.Fatalf("actual restored worker pair = %+v / %+v", pump, anti)
	}
	selectedPump, pumpErr := NewManagedReplicationPump(workers)
	selectedAnti, antiErr := NewManagedAntiEntropyDriver(workers)
	if pumpErr != nil || antiErr != nil || selectedPump != workers.pump || selectedAnti != workers.anti {
		t.Fatal("selectors replaced actual worker objects")
	}
	observations, err := manager.Observe(projection)
	if err != nil {
		t.Fatal(err)
	}
	actual := observations[operationalpolicy.GroupReplication]
	if actual.AppliedGeneration != 7 || actual.AppliedCut != projection.Cut || actual.Pending || actual.RestartRequired || *actual.Effective.Replication != *values.Replication || actual.EffectiveCompatibilityFingerprint != "" {
		t.Fatalf("restored application observation = %+v", actual)
	}
	for _, source := range actual.Sources {
		if source != securitytransport.OperationalSourceManaged {
			t.Fatal("restored generation retained environment precedence")
		}
	}

	next := operationalRuntimeTestProjection(4)
	next.Preparations = projection.Preparations
	nextValues := values.Clone()
	nextValues.Replication.PumpBackoffMaxMS = 71
	if err := next.Policies.Set(operationalpolicy.GroupReplication, &operationalpolicy.Policy{Version: 1, Generation: 8, Values: nextValues}); err != nil {
		t.Fatal(err)
	}
	authority.setProjection(next)
	if err := manager.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	observations, err = manager.Observe(next)
	if err != nil {
		t.Fatal(err)
	}
	actual = observations[operationalpolicy.GroupReplication]
	if actual.AppliedGeneration != 7 || actual.AppliedCut != projection.Cut || !actual.Pending || !actual.RestartRequired || actual.Mode != securitytransport.OperationalModeProcessRestart || actual.Effective.Replication.PumpBackoffMaxMS != 53 {
		t.Fatalf("committed successor falsely installed = %+v", actual)
	}
	pumpAfter, _ := workers.pump.Scheduling()
	if pumpAfter != pump {
		t.Fatal("staging changed live worker configuration")
	}
	authority.mu.Lock()
	reads := authority.reads
	authority.mu.Unlock()
	if err := workers.owner.ApplyOperationalPolicy(operationalpolicy.Policy{Version: 1, Generation: 8, Values: nextValues}); err != nil {
		t.Fatal(err)
	}
	authority.mu.Lock()
	if authority.reads != reads {
		authority.mu.Unlock()
		t.Fatal("native callback re-entered authority")
	}
	authority.mu.Unlock()

	// Only new actual constructors can establish generation eight.
	restartedManager, _, restartedConfig, restartedPrimary, restartedRuntime, restartedSearch := replicationBindingFixture(t, next)
	restarted, err := constructReplicationBinding(t, restartedManager, restartedConfig, restartedPrimary, restartedRuntime, restartedSearch)
	if err != nil {
		t.Fatal(err)
	}
	after, err := restarted.owner.ObserveOperationalPolicy()
	if err != nil || after.EffectiveGeneration != 8 || after.Effective.Replication.PumpBackoffMaxMS != 71 {
		t.Fatalf("new process worker generation = %+v, %v", after, err)
	}
}

func TestManagedReplicationBindingIdentityAndInvalidBootstrap(t *testing.T) {
	t.Run("requires final exact Search installation", func(t *testing.T) {
		projection := operationalRuntimeTestProjection(1)
		manager, authority, config, primary, runtime, search := replicationBindingFixture(t, projection)
		if _, err := constructReplicationBinding(t, manager, config, primary, runtime, managedSearchCertified{}); err == nil {
			t.Fatal("zero Search installation accepted")
		}
		authority.mu.Lock()
		_, bound := authority.bound[operationalpolicy.GroupReplication]
		authority.mu.Unlock()
		if bound {
			t.Fatal("invalid composition bound an owner")
		}
		foreign := securityDataFixture(t)
		if _, err := NewManagedReplicationRuntime(t.Context(), manager, config.Peer, config.AntiEntropy, nil, config.Replication, nil, primary, foreign.GraphCache(), nil, nil, nil, nil, runtime, search); err == nil {
			t.Fatal("foreign graph cache accepted")
		}
		// An exact replay is intentionally a no-op. Stage a real successor
		// before simulating an out-of-contract manual engine replacement.
		policy := managedSearchTestPolicy(t, primary, 1)
		if err := search.runtime.ApplyOperationalPolicy(*policy); err != nil {
			t.Fatal(err)
		}
		if err := search.runtime.owner.Restart(search.runtime.owner.Status().Desired); err != nil {
			t.Fatal(err)
		}
		if _, err := constructReplicationBinding(t, manager, config, primary, runtime, search); err == nil {
			t.Fatal("substituted engine accepted")
		}
	})
	t.Run("unknown effective bootstrap can stage repair", func(t *testing.T) {
		projection := operationalRuntimeTestProjection(1)
		manager, authority, config, primary, runtime, search := replicationBindingFixture(t, projection, "LANTERN_ANTI_ENTROPY_GAP_WARN_THRESHOLD=-1")
		workers, err := constructReplicationBinding(t, manager, config, primary, runtime, search)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := workers.owner.ObserveOperationalPolicy()
		if err != nil || actual.Effective != nil || actual.EffectiveGeneration != 0 || actual.Error != securitytransport.OperationalErrorInvalidPolicy || actual.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_REPLICATION_ANTI_ENTROPY_GAP_WARN_THRESHOLD] != securitytransport.OperationalSourceEnvironment {
			t.Fatalf("invalid bootstrap was hidden or defaulted: %+v, %v", actual, err)
		}
		next := operationalRuntimeTestPrepare(operationalRuntimeTestProjection(2), operationalpolicy.GroupReplication)
		values, _ := operationalpolicy.Defaults(operationalpolicy.GroupReplication)
		if err := next.Policies.Set(operationalpolicy.GroupReplication, &operationalpolicy.Policy{Version: 1, Generation: 1, Values: values}); err != nil {
			t.Fatal(err)
		}
		authority.setProjection(next)
		if err := manager.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
		observations, err := manager.Observe(next)
		if err != nil {
			t.Fatal(err)
		}
		observed := observations[operationalpolicy.GroupReplication]
		if observed.Effective != nil || observed.AppliedGeneration != 0 || !observed.Pending || !observed.RestartRequired || observed.Error != securitytransport.OperationalErrorInvalidPolicy {
			t.Fatalf("staged repair falsely applied: %+v", observed)
		}
	})
}

func TestManagedReplicationBindingSelectorsRequireExactRegisteredPair(t *testing.T) {
	projection := operationalRuntimeTestProjection(1)
	manager, _, config, primary, runtime, search := replicationBindingFixture(t, projection)
	workers, err := constructReplicationBinding(t, manager, config, primary, runtime, search)
	if err != nil {
		t.Fatal(err)
	}
	foreignManager, _, foreignConfig, foreignPrimary, foreignRuntime, foreignSearch := replicationBindingFixture(t, projection)
	foreign, err := constructReplicationBinding(t, foreignManager, foreignConfig, foreignPrimary, foreignRuntime, foreignSearch)
	if err != nil {
		t.Fatal(err)
	}
	if workers.pump.SearchConfigFingerprint() != foreign.pump.SearchConfigFingerprint() {
		t.Fatal("fixture must exercise equal-fingerprint foreign workers")
	}
	if !manager.runtime.UsesManagedReplicationWorkers(primary, workers.pump, workers.anti) ||
		manager.runtime.UsesManagedReplicationWorkers(foreignPrimary, workers.pump, workers.anti) ||
		manager.runtime.UsesManagedReplicationWorkers(primary, foreign.pump, workers.anti) ||
		manager.runtime.UsesManagedReplicationWorkers(primary, workers.pump, foreign.anti) ||
		manager.runtime.UsesManagedReplicationWorkers(primary, foreign.pump, foreign.anti) {
		t.Fatal("registered worker identity accepted an equal-valued foreign pair")
	}
	var missing *SecurityRuntime
	if missing.UsesManagedReplicationWorkers(primary, workers.pump, workers.anti) || (&SecurityRuntime{}).UsesManagedReplicationWorkers(primary, workers.pump, workers.anti) {
		t.Fatal("missing management runtime advertised actual workers")
	}
	for name, mutate := range map[string]func(*managedReplicationCertified){
		"missing manager":      func(c *managedReplicationCertified) { c.manager = nil },
		"foreign manager":      func(c *managedReplicationCertified) { c.manager = foreignManager },
		"foreign primary":      func(c *managedReplicationCertified) { c.primary = foreignPrimary },
		"missing owner":        func(c *managedReplicationCertified) { c.owner = nil },
		"foreign owner":        func(c *managedReplicationCertified) { c.owner = foreign.owner },
		"foreign Pump":         func(c *managedReplicationCertified) { c.pump = foreign.pump },
		"foreign anti-entropy": func(c *managedReplicationCertified) { c.anti = foreign.anti },
		"foreign pair":         func(c *managedReplicationCertified) { c.pump, c.anti = foreign.pump, foreign.anti },
	} {
		t.Run(name, func(t *testing.T) {
			wrong := workers
			mutate(&wrong)
			if _, err := NewManagedReplicationPump(wrong); err == nil {
				t.Fatal("invalid actual Pump selector accepted")
			}
			if _, err := NewManagedAntiEntropyDriver(wrong); err == nil {
				t.Fatal("invalid actual anti-entropy selector accepted")
			}
		})
	}
	registered := manager.replication
	manager.replication = nil
	if manager.runtime.UsesManagedReplicationWorkers(primary, workers.pump, workers.anti) {
		t.Fatal("missing registration retained worker ownership")
	}
	if _, err := NewManagedReplicationPump(workers); err == nil {
		t.Fatal("unregistered workers selected")
	}
	manager.replication = registered
	// Desired Search staging leaves the installed engine and worker identities
	// intact. A manual engine replacement invalidates that construction proof.
	policy := managedSearchTestPolicy(t, primary, 1)
	if err := search.runtime.ApplyOperationalPolicy(*policy); err != nil {
		t.Fatal(err)
	}
	if !manager.runtime.UsesManagedReplicationWorkers(primary, workers.pump, workers.anti) {
		t.Fatal("pending Search policy replaced actual worker ownership")
	}
	if err := search.runtime.owner.Restart(search.runtime.owner.Status().Desired); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManagedAntiEntropyDriver(workers); err == nil {
		t.Fatal("stale engine installation selected workers")
	}
}
