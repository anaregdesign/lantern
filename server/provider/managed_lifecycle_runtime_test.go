package provider

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/anaregdesign/lantern/server/service"
)

func lifecycleRuntimeTestProjection(t *testing.T, sequence, generation uint64, values operationalpolicy.Lifecycle) security.OperationalPolicyProjection {
	t.Helper()
	p := operationalRuntimeTestPrepare(operationalRuntimeTestProjection(sequence), operationalpolicy.GroupLifecycle)
	policy := &operationalpolicy.Policy{Version: operationalpolicy.Version, Generation: generation, Values: operationalpolicy.Values{Lifecycle: &values}}
	if err := p.Policies.Set(operationalpolicy.GroupLifecycle, policy); err != nil {
		t.Fatal(err)
	}
	return p
}

func lifecycleRuntimeTestBootstrap(t *testing.T, input []string) (*Config, *ManagedOperationalRuntime) {
	t.Helper()
	cfg, err := LoadConfig(envconfig.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManagedOperationalRuntime(t.Context(), &SecurityRuntime{mode: "off"}, NewOperationalBootstrapConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	return cfg, manager
}

func TestManagedLifecycleRuntimeBootstrapAndRepair(t *testing.T) {
	for _, test := range []struct {
		name                       string
		input                      []string
		drain, timeout             time.Duration
		drainSource, timeoutSource securitytransport.OperationalSettingsSource
		invalid                    bool
	}{
		{"defaults", nil, 0, 30 * time.Second, securitytransport.OperationalSourceDefault, securitytransport.OperationalSourceDefault, false},
		{"explicit zero", []string{"LANTERN_DRAIN_DELAY_SECONDS=0", "LANTERN_SHUTDOWN_TIMEOUT_SECONDS=0"}, 0, 0, securitytransport.OperationalSourceEnvironment, securitytransport.OperationalSourceEnvironment, false},
		{"legacy disabled", []string{"LANTERN_DRAIN_DELAY_SECONDS=-1", "LANTERN_SHUTDOWN_TIMEOUT_SECONDS=-2"}, 0, 0, securitytransport.OperationalSourceDerived, securitytransport.OperationalSourceDerived, false},
		{"unrepresentable actual", []string{"LANTERN_DRAIN_DELAY_SECONDS=18446744074", "LANTERN_SHUTDOWN_TIMEOUT_SECONDS=18446744074"}, 290448384 * time.Nanosecond, 290448384 * time.Nanosecond, securitytransport.OperationalSourceEnvironment, securitytransport.OperationalSourceEnvironment, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, manager := lifecycleRuntimeTestBootstrap(t, test.input)
			gate := readiness.NewGate(cfg.Readiness.MaxLag, false, nil)
			cert, err := NewManagedLifecycleRuntime(t.Context(), manager, cfg.Shutdown, cfg.Readiness, gate)
			if err != nil {
				t.Fatal(err)
			}
			actual := cert.owner.LifecycleSnapshot()
			if actual.Generation != 0 || actual.Policy.DrainDelay != test.drain || actual.Policy.ShutdownTimeout != test.timeout || cert.plan.Current() != actual {
				t.Fatal("bootstrap invented future-action behavior", actual)
			}
			observed, err := cert.adapter.ObserveOperationalPolicy()
			if err != nil || observed.EffectiveGeneration != 0 || observed.Mode != securitytransport.OperationalModeHot ||
				observed.Sources[25] != test.drainSource || observed.Sources[27] != test.timeoutSource || len(observed.Sources) != 3 {
				t.Fatal("bootstrap source/effective observation is not actual", observed, err)
			}
			if test.invalid {
				if observed.Error != securitytransport.OperationalErrorInvalidPolicy || observed.Effective != nil {
					t.Fatal("unrepresentable actual bootstrap silently clamped/defaulted", observed)
				}
			} else if observed.Error != securitytransport.OperationalErrorNone || observed.Effective == nil {
				t.Fatal("valid actual bootstrap withheld", observed)
			}
			policy := operationalpolicy.Policy{Version: operationalpolicy.Version, Generation: 1, Values: operationalpolicy.Values{Lifecycle: &operationalpolicy.Lifecycle{DrainDelaySeconds: 2, ShutdownTimeoutSeconds: 3, MaxReplicationLag: 0}}}
			if err := cert.adapter.ApplyOperationalPolicy(policy); err != nil {
				t.Fatal(err)
			}
			managed, err := cert.adapter.ObserveOperationalPolicy()
			if err != nil || managed.EffectiveGeneration != 1 || managed.Effective == nil || managed.Effective.Lifecycle.DrainDelaySeconds != 2 || managed.Effective.Lifecycle.ShutdownTimeoutSeconds != 3 || gate.MaxLag() != 0 {
				t.Fatal("managed repair did not reach actual future-action owner and gate", managed, err)
			}
			for _, source := range managed.Sources {
				if source != securitytransport.OperationalSourceManaged {
					t.Fatal("managed policy retained host ownership")
				}
			}
			managed.Effective.Lifecycle.DrainDelaySeconds++
			managed.Sources[25] = securitytransport.OperationalSourceDefault
			again, _ := cert.adapter.ObserveOperationalPolicy()
			if again.Effective.Lifecycle.DrainDelaySeconds != 2 || again.Sources[25] != securitytransport.OperationalSourceManaged {
				t.Fatal("observation leaked owner/source state")
			}
		})
	}
}

func TestManagedLifecycleRuntimeVerifiedRecoveryAndFrozenShutdown(t *testing.T) {
	cfg, err := LoadConfig(envconfig.NewReader([]string{"LANTERN_DRAIN_DELAY_SECONDS=18446744074", "LANTERN_SHUTDOWN_TIMEOUT_SECONDS=18446744074"}))
	if err != nil {
		t.Fatal(err)
	}
	first := lifecycleRuntimeTestProjection(t, 1, 1, operationalpolicy.Lifecycle{DrainDelaySeconds: 5, ShutdownTimeoutSeconds: 6, MaxReplicationLag: 7})
	latest := lifecycleRuntimeTestProjection(t, 2, 2, operationalpolicy.Lifecycle{DrainDelaySeconds: 2, ShutdownTimeoutSeconds: 3, MaxReplicationLag: 4})
	manager, authority := operationalRuntimeTestManager(t, first)
	manager.bootstrap = NewOperationalBootstrapConfig(cfg)
	authority.setProjection(latest)
	gate := readiness.NewGate(10000, true, nil)
	cert, err := NewManagedLifecycleRuntime(t.Context(), manager, cfg.Shutdown, cfg.Readiness, gate)
	if err != nil {
		t.Fatal(err)
	}
	actual := cert.owner.LifecycleSnapshot()
	if actual.Generation != 2 || actual.Policy.DrainDelay != 2*time.Second || actual.Policy.ShutdownTimeout != 3*time.Second || gate.MaxLag() != 4 {
		t.Fatal("recovery used stale projection or invalid host values", actual)
	}
	frozen := cert.plan.Capture()
	next := lifecycleRuntimeTestProjection(t, 3, 3, operationalpolicy.Lifecycle{MaxReplicationLag: 0})
	authority.setProjection(next)
	if err := manager.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if cert.owner.LifecycleSnapshot().Generation != 3 || cert.plan.Capture() != frozen || gate.MaxLag() != 0 {
		t.Fatal("new future-action generation mutated in-progress shutdown")
	}
	observed, err := manager.Observe(next)
	if err != nil || observed[operationalpolicy.GroupLifecycle].AppliedGeneration != 3 || observed[operationalpolicy.GroupLifecycle].AppliedCut != next.Cut || observed[operationalpolicy.GroupLifecycle].Pending {
		t.Fatal("verified actual future-action generation not reported", err)
	}
	authority.mu.Lock()
	authority.readErr = errors.New("authority unavailable")
	authority.mu.Unlock()
	if manager.Reconcile(t.Context()) == nil || cert.owner.LifecycleSnapshot().Generation != 3 || cert.plan.Capture() != frozen {
		t.Fatal("authority failure fell back to host or changed frozen shutdown")
	}
	if _, err := NewManagedLifecycleRuntime(t.Context(), manager, cfg.Shutdown, cfg.Readiness, readiness.NewGate(1, false, nil)); err == nil {
		t.Fatal("fresh boot authority failure used cached or environmental state")
	}
}

func TestManagedLifecycleRuntimePrepareAndReadinessOwnership(t *testing.T) {
	cfg, manager := lifecycleRuntimeTestBootstrap(t, nil)
	gate := readiness.NewGate(10000, true, nil)
	cert, err := NewManagedLifecycleRuntime(t.Context(), manager, cfg.Shutdown, cfg.Readiness, gate)
	if err != nil {
		t.Fatal(err)
	}
	initial := cert.owner.LifecycleSnapshot()
	change := operationalpolicy.Change{Values: operationalpolicy.Values{Lifecycle: &operationalpolicy.Lifecycle{MaxReplicationLag: 0}}}
	if err := cert.adapter.PrepareOperationalPolicy(change); err != nil || cert.owner.LifecycleSnapshot() != initial || gate.MaxLag() != 10000 {
		t.Fatal("Prepare changed actual settings", err)
	}
	for _, bad := range []operationalpolicy.Lifecycle{{DrainDelaySeconds: -1}, {ShutdownTimeoutSeconds: -1}, {DrainDelaySeconds: math.MaxInt64/int64(time.Second) + 1}, {ShutdownTimeoutSeconds: math.MaxInt64/int64(time.Second) + 1}} {
		change.Values.Lifecycle = &bad
		if cert.adapter.PrepareOperationalPolicy(change) == nil {
			t.Fatal("negative/overflowed duration prepared")
		}
	}
	gate.SetLag("peer", "origin", 5)
	gate.MarkBootstrapped()
	gate.SetServingPermission(false)
	policy := operationalpolicy.Policy{Version: 1, Generation: 1, Values: operationalpolicy.Values{Lifecycle: &operationalpolicy.Lifecycle{MaxReplicationLag: 0}}}
	if err := cert.adapter.ApplyOperationalPolicy(policy); err != nil || gate.Ready() {
		t.Fatal("lag policy restored native serving permission", err)
	}
	gate.SetServingPermission(true)
	if gate.Ready() {
		t.Fatal("zero lag threshold disabled observations")
	}
	policy.Generation, policy.Values.Lifecycle.MaxReplicationLag = 2, 100
	gate.SetSearchConfig("peer", false)
	if err := cert.adapter.ApplyOperationalPolicy(policy); err != nil || gate.Ready() {
		t.Fatal("lag policy bypassed Search mismatch", err)
	}
	gate.SetSearchConfig("peer", true)
	if !gate.Ready() {
		t.Fatal("retained lag did not reevaluate with new threshold")
	}
	gate.BeginDrain()
	policy.Generation, policy.Values.Lifecycle.MaxReplicationLag = 3, math.MaxUint64
	if err := cert.adapter.ApplyOperationalPolicy(policy); err != nil || gate.Ready() {
		t.Fatal("policy reversed drain latch", err)
	}
	if cert.adapter.ApplyOperationalPolicy(policy) != nil {
		t.Fatal("exact generation retry refused")
	}
	policy.Generation = 2
	if cert.adapter.ApplyOperationalPolicy(policy) == nil || gate.MaxLag() != math.MaxUint64 {
		t.Fatal("older callback overwrote actual lag threshold")
	}
	if cert.adapter.AvailableOperationalPolicy(operationalpolicy.GroupSearch, 1) == nil || cert.adapter.AvailableOperationalPolicy(operationalpolicy.GroupLifecycle, 2) == nil || (&managedLifecycleRuntime{}).AvailableOperationalPolicy(operationalpolicy.GroupLifecycle, 1) == nil {
		t.Fatal("missing/foreign application contract advertised")
	}
}

func TestManagedLifecycleRuntimeConcurrentApplyAndBinding(t *testing.T) {
	cfg, manager := lifecycleRuntimeTestBootstrap(t, nil)
	gate := readiness.NewGate(10000, false, nil)
	cert, err := NewManagedLifecycleRuntime(t.Context(), manager, cfg.Shutdown, cfg.Readiness, gate)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Go(func() {
			for i := range 100 {
				generation := uint64(worker*100 + i + 1)
				policy := operationalpolicy.Policy{Version: 1, Generation: generation, Values: operationalpolicy.Values{Lifecycle: &operationalpolicy.Lifecycle{MaxReplicationLag: generation}}}
				_ = cert.adapter.ApplyOperationalPolicy(policy)
				observed, err := cert.adapter.ObserveOperationalPolicy()
				if err != nil || observed.Effective == nil || observed.EffectiveGeneration != observed.Effective.Lifecycle.MaxReplicationLag {
					t.Errorf("actual generation and lag tuple torn: %+v, %v", observed, err)
				}
			}
		})
	}
	workers.Wait()
	if cert.owner.LifecycleSnapshot().Generation != 800 || gate.MaxLag() != 800 {
		t.Fatal("out-of-order callback restored stale gate policy")
	}
	plan, err := NewManagedShutdownPlan(cert)
	config, configErr := NewManagedLifecycleConfig(CacheConfig{GCInterval: time.Minute}, cert)
	if err != nil || configErr != nil || plan != cert.plan || config.ShutdownPlan != plan || config.GCInterval != time.Minute {
		t.Fatal("server configuration selected a foreign shutdown plan", err, configErr)
	}
	foreign := cert
	foreign.manager = &ManagedOperationalRuntime{}
	if _, err := NewManagedShutdownPlan(foreign); err == nil {
		t.Fatal("unregistered owner/plan identity accepted")
	}
	gate.UpdateMaxLag(999)
	if _, err := cert.adapter.ObserveOperationalPolicy(); err == nil {
		t.Fatal("out-of-band gate mutation claimed actual application")
	}
}

func TestManagedLifecycleSelectorsRefuseSubstitutedPlanSource(t *testing.T) {
	cfg, manager := lifecycleRuntimeTestBootstrap(t, nil)
	gate := readiness.NewGate(cfg.Readiness.MaxLag, false, nil)
	marker, err := NewManagedLifecycleRuntime(t.Context(), manager, cfg.Shutdown, cfg.Readiness, gate)
	if err != nil {
		t.Fatal(err)
	}
	foreignOwner := service.NewBootstrapLifecyclePolicyOwner(marker.owner.LifecycleSnapshot().Policy)
	foreignPlan, err := service.NewShutdownPlan(foreignOwner)
	if err != nil {
		t.Fatal(err)
	}
	// Both marker and adapter can agree on a substituted plan. The concrete
	// source identity must still refuse it without capturing either plan.
	marker.plan, marker.adapter.plan = foreignPlan, foreignPlan
	if _, err := NewManagedShutdownPlan(marker); err == nil {
		t.Fatal("plan bound to a foreign owner selected")
	}
	if _, err := NewManagedLifecycleConfig(cfg.Cache, marker); err == nil {
		t.Fatal("HTTP configuration selected a foreign lifecycle source")
	}
	if marker.adapter.AvailableOperationalPolicy(operationalpolicy.GroupLifecycle, operationalpolicy.Version) == nil {
		t.Fatal("substituted plan advertised actual availability")
	}
}

func TestManagedLifecycleRuntimeRefusesUnknownBootstrapAndCanceledConstruction(t *testing.T) {
	manager, err := NewManagedOperationalRuntime(t.Context(), &SecurityRuntime{mode: "off"}, OperationalBootstrapConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewManagedLifecycleRuntime(t.Context(), manager, ShutdownConfig{}, ReadinessConfig{}, readiness.NewGate(0, false, nil)); err == nil {
		t.Fatal("unknown bootstrap provenance became defaults")
	}
	cfg, manager := lifecycleRuntimeTestBootstrap(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewManagedLifecycleRuntime(ctx, manager, cfg.Shutdown, cfg.Readiness, readiness.NewGate(1, false, nil)); err == nil {
		t.Fatal("canceled construction registered actual owner")
	}
	if _, err := NewManagedLifecycleRuntime(t.Context(), manager, cfg.Shutdown, cfg.Readiness, nil); err == nil {
		t.Fatal("absent actual readiness gate accepted")
	}
}

func TestManagedLifecycleRuntimeDuplicateConstructionAndCheckedSeconds(t *testing.T) {
	cfg, manager := lifecycleRuntimeTestBootstrap(t, nil)
	gate := readiness.NewGate(cfg.Readiness.MaxLag, false, nil)
	cert, err := NewManagedLifecycleRuntime(t.Context(), manager, cfg.Shutdown, cfg.Readiness, gate)
	if err != nil {
		t.Fatal(err)
	}
	maximum := int64(math.MaxInt64) / int64(time.Second)
	policy := operationalpolicy.Policy{Version: 1, Generation: 1, Values: operationalpolicy.Values{Lifecycle: &operationalpolicy.Lifecycle{
		DrainDelaySeconds: maximum, ShutdownTimeoutSeconds: maximum, MaxReplicationLag: 0,
	}}}
	if err := cert.adapter.ApplyOperationalPolicy(policy); err != nil {
		t.Fatal("representable seconds boundary refused", err)
	}
	want := time.Duration(maximum) * time.Second
	if actual := cert.owner.LifecycleSnapshot(); actual.Policy.DrainDelay != want || actual.Policy.ShutdownTimeout != want {
		t.Fatal("seconds conversion overflowed or lost precision", actual)
	}
	if _, err := NewManagedLifecycleRuntime(t.Context(), manager, cfg.Shutdown, cfg.Readiness, gate); err == nil || gate.MaxLag() != 0 {
		t.Fatal("failed duplicate construction reset the actual gate", err)
	}
	before := cert.owner.LifecycleSnapshot()
	for _, bad := range []operationalpolicy.Policy{
		{Version: 1, Generation: 2, Values: operationalpolicy.Values{SlowRPC: &operationalpolicy.SlowRPC{ThresholdMS: 1}}},
		{Version: 1, Generation: 2, Values: operationalpolicy.Values{Lifecycle: &operationalpolicy.Lifecycle{ShutdownTimeoutSeconds: maximum + 1}}},
		{Version: 2, Generation: 2, Values: policy.Values.Clone()},
	} {
		if cert.adapter.ApplyOperationalPolicy(bad) == nil || cert.owner.LifecycleSnapshot() != before || gate.MaxLag() != 0 {
			t.Fatal("invalid managed policy mutated actual resources")
		}
	}
}
