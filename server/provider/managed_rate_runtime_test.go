package provider

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

func TestManagedRateRuntimeBootstrapValuesAndSources(t *testing.T) {
	for _, test := range []struct {
		name                   string
		input                  []string
		want                   RateLimitConfig
		rpsSource, burstSource securitytransport.OperationalSettingsSource
	}{
		{"default", nil, RateLimitConfig{}, securitytransport.OperationalSourceDefault, securitytransport.OperationalSourceDefault},
		{"explicit-disabled", []string{"LANTERN_RATE_LIMIT_RPS=0", "LANTERN_RATE_LIMIT_BURST=0"}, RateLimitConfig{}, securitytransport.OperationalSourceEnvironment, securitytransport.OperationalSourceEnvironment},
		{"explicit-pair", []string{"LANTERN_RATE_LIMIT_RPS=2.5", "LANTERN_RATE_LIMIT_BURST=6"}, RateLimitConfig{RPS: 2.5, Burst: 6}, securitytransport.OperationalSourceEnvironment, securitytransport.OperationalSourceEnvironment},
		{"fractional-burst", []string{"LANTERN_RATE_LIMIT_RPS=0.25"}, RateLimitConfig{RPS: 0.25, Burst: 1}, securitytransport.OperationalSourceEnvironment, securitytransport.OperationalSourceDerived},
		{"derived-burst", []string{"LANTERN_RATE_LIMIT_RPS=2.5", "LANTERN_RATE_LIMIT_BURST=-4"}, RateLimitConfig{RPS: 2.5, Burst: 5}, securitytransport.OperationalSourceEnvironment, securitytransport.OperationalSourceDerived},
		{"derived-disabled", []string{"LANTERN_RATE_LIMIT_RPS=-2", "LANTERN_RATE_LIMIT_BURST=7"}, RateLimitConfig{}, securitytransport.OperationalSourceDerived, securitytransport.OperationalSourceDerived},
		{"negative-zero", []string{"LANTERN_RATE_LIMIT_RPS=-0", "LANTERN_RATE_LIMIT_BURST=0"}, RateLimitConfig{}, securitytransport.OperationalSourceDerived, securitytransport.OperationalSourceEnvironment},
		{"malformed", []string{"LANTERN_RATE_LIMIT_RPS=bad"}, RateLimitConfig{}, securitytransport.OperationalSourceDefault, securitytransport.OperationalSourceDefault},
		{"empty", []string{"LANTERN_RATE_LIMIT_RPS="}, RateLimitConfig{}, securitytransport.OperationalSourceDefault, securitytransport.OperationalSourceDefault},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := LoadConfig(envconfig.NewReader(test.input))
			if err != nil {
				t.Fatal(err)
			}
			runtime := &SecurityRuntime{mode: "off"}
			manager, err := NewManagedOperationalRuntime(t.Context(), runtime, NewOperationalBootstrapConfig(cfg))
			if err != nil {
				t.Fatal(err)
			}
			owner := NewRateLimitInterceptor(cfg.RateLimit.RPS, cfg.RateLimit.Burst)
			recovery, err := NewRateRecoveryRuntime(runtime, owner)
			if err != nil {
				t.Fatal(err)
			}
			cert, err := NewManagedRateRuntime(t.Context(), manager, owner, recovery)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := cert.adapter.ObserveOperationalPolicy()
			if err != nil || actual.Effective == nil || actual.EffectiveGeneration != 0 || actual.Mode != securitytransport.OperationalModeHot || actual.Error != securitytransport.OperationalErrorNone || actual.Fingerprint != "" ||
				actual.Effective.Rate.RPS != test.want.RPS || actual.Effective.Rate.Burst != int64(test.want.Burst) ||
				actual.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_RATE_RPS] != test.rpsSource || actual.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_RATE_BURST] != test.burstSource {
				t.Fatalf("bootstrap actual/source = %+v, %v", actual, err)
			}
		})
	}
}

func TestManagedRateRuntimeInvalidBootstrapRepairAndRecoveryIdentity(t *testing.T) {
	for _, raw := range []string{"NaN", "+Inf", "-Inf"} {
		t.Run(raw, func(t *testing.T) {
			cfg, err := LoadConfig(envconfig.NewReader([]string{"LANTERN_RATE_LIMIT_RPS=" + raw}))
			if err != nil {
				t.Fatal(err)
			}
			// Synthetic transport ownership only; the paired authority fixture
			// below separately exercises verified provider reconciliation.
			runtime := &SecurityRuntime{mode: "oidc", current: &security.CurrentAuthority{}}
			manager := &ManagedOperationalRuntime{runtime: runtime, bootstrap: NewOperationalBootstrapConfig(cfg)}
			owner := NewRateLimitInterceptor(cfg.RateLimit.RPS, cfg.RateLimit.Burst)
			owner.now = func() time.Time { return time.Unix(100, 0) }
			recovery, err := NewRateRecoveryRuntime(runtime, owner)
			if err != nil {
				t.Fatal(err)
			}
			cert, err := NewManagedRateRuntime(t.Context(), manager, owner, recovery)
			if err != nil || cert.adapter.AvailableOperationalPolicy(operationalpolicy.GroupRate, 1) != nil {
				t.Fatal("invalid bootstrap hid installed recovery capability", err)
			}
			actual, err := cert.adapter.ObserveOperationalPolicy()
			if err != nil || actual.Effective != nil || actual.EffectiveGeneration != 0 || actual.Mode != securitytransport.OperationalModeHot || actual.Error != securitytransport.OperationalErrorInvalidPolicy ||
				len(actual.Sources) != 2 || actual.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_RATE_RPS] != securitytransport.OperationalSourceEnvironment || actual.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_RATE_BURST] != securitytransport.OperationalSourceDerived || owner.Allow() {
				t.Fatal("invalid bootstrap fabricated effective defaults or provenance", actual, err)
			}
			request := httptest.NewRequest(http.MethodGet, "/auth/session", nil)
			for range rateRecoveryBurst {
				if !recovery.allow(request) {
					t.Fatal("invalid ordinary owner lost bounded recovery")
				}
			}
			policy := operationalpolicy.Policy{Version: 1, Generation: 1, Values: operationalpolicy.Values{Rate: &operationalpolicy.Rate{RPS: 0.0001, Burst: 1}}}
			if err := cert.adapter.PrepareOperationalPolicy(operationalpolicy.Change{Values: policy.Values}); err != nil || owner.Snapshot().Generation != 0 || owner.Allow() {
				t.Fatal("preparation mutated invalid bootstrap", err)
			}
			if err := cert.adapter.ApplyOperationalPolicy(policy); err != nil {
				t.Fatal(err)
			}
			applied, err := cert.adapter.ObserveOperationalPolicy()
			if err != nil || applied.EffectiveGeneration != 1 || applied.Effective == nil || applied.Effective.Rate.RPS != 0.0001 || applied.Error != securitytransport.OperationalErrorNone || applied.Fingerprint != "" ||
				applied.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_RATE_RPS] != securitytransport.OperationalSourceManaged || applied.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_RATE_BURST] != securitytransport.OperationalSourceManaged || recovery.allow(request) {
				t.Fatal("managed repair state or recovery credit is wrong", applied, err)
			}
			applied.Effective.Rate.RPS = 999
			applied.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_RATE_RPS] = securitytransport.OperationalSourceDefault
			fresh, _ := cert.adapter.ObserveOperationalPolicy()
			if fresh.Effective.Rate.RPS != 0.0001 || fresh.Sources[pb.OperationalSetting_OPERATIONAL_SETTING_RATE_RPS] != securitytransport.OperationalSourceManaged {
				t.Fatal("observation aliases installed owner")
			}
			if err := owner.Configure(0, 0); err == nil {
				t.Fatal("ordinary setter overrode adopted generation")
			}
			for _, change := range []operationalpolicy.Change{
				{Values: operationalpolicy.Values{Rate: &operationalpolicy.Rate{RPS: math.NaN(), Burst: 1}}},
				{Values: operationalpolicy.Values{Rate: &operationalpolicy.Rate{RPS: math.Copysign(0, -1)}}},
				{Values: operationalpolicy.Values{Rate: &operationalpolicy.Rate{RPS: 0, Burst: 1}}},
				{Values: operationalpolicy.Values{SlowRPC: &operationalpolicy.SlowRPC{ThresholdMS: 1}}},
			} {
				if err := cert.adapter.PrepareOperationalPolicy(change); err == nil {
					t.Fatal("invalid or foreign candidate accepted")
				}
			}
			if cert.adapter.AvailableOperationalPolicy(operationalpolicy.GroupSlowRPC, 1) == nil || cert.adapter.AvailableOperationalPolicy(operationalpolicy.GroupRate, 2) == nil {
				t.Fatal("rate owner advertised unrelated application contract")
			}
			foreign := &managedRateRuntime{manager: manager, owner: NewRateLimitInterceptor(0, 0), recovery: recovery}
			if foreign.AvailableOperationalPolicy(operationalpolicy.GroupRate, 1) == nil {
				t.Fatal("foreign rate owner borrowed mounted recovery proof")
			}
		})
	}
}

func TestManagedRateRuntimeUncapturedSourcesRefuseBeforeBinding(t *testing.T) {
	runtime := &SecurityRuntime{mode: "off"}
	authority := &managedRateAuthorityTestFake{}
	manager := &ManagedOperationalRuntime{runtime: runtime, authority: authority}
	owner := NewRateLimitInterceptor(2, 3)
	before := owner.Snapshot()
	recovery, err := NewRateRecoveryRuntime(runtime, owner)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := NewManagedRateRuntime(t.Context(), manager, owner, recovery)
	if err == nil || cert.adapter != nil || manager.rate != nil {
		t.Fatal("uncaptured sources bound or certified a managed rate owner", err)
	}
	authority.mu.Lock()
	reads, bound := authority.reads, authority.bound
	authority.mu.Unlock()
	if reads != 0 || bound != nil {
		t.Fatal("missing provenance reached authority registration", reads, bound)
	}
	after := owner.Snapshot()
	if before != after || !recovery.bound(runtime, owner) {
		t.Fatal("missing provenance mutated effective owner or recovery binding", after)
	}
}

// This fake proves provider recovery/observation ordering, not native consensus.
type managedRateAuthorityTestFake struct {
	mu         sync.Mutex
	projection security.OperationalPolicyProjection
	bound      security.OperationalPolicyRuntime
	reads      int
}

func (f *managedRateAuthorityTestFake) CurrentOperationalPolicies(context.Context) (security.OperationalPolicyProjection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	p := f.projection
	p.Policies = p.Policies.Clone()
	p.Preparations = append([]security.OperationalPolicyPreparation(nil), p.Preparations...)
	return p, nil
}
func (f *managedRateAuthorityTestFake) BindOperationalPolicyRuntime(group operationalpolicy.Group, owner security.OperationalPolicyRuntime) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if group != operationalpolicy.GroupRate || f.bound != nil || owner.AvailableOperationalPolicy(group, 1) != nil {
		return errManagedOperational
	}
	f.bound = owner
	return nil
}
func (f *managedRateAuthorityTestFake) RequireOperationalPolicyOwners(context.Context) error {
	return nil
}

func TestManagedRateRuntimeRestoresVerifiedPolicyInsteadOfEnvironment(t *testing.T) {
	cfg, err := LoadConfig(envconfig.NewReader([]string{"LANTERN_RATE_LIMIT_RPS=100", "LANTERN_RATE_LIMIT_BURST=999"}))
	if err != nil {
		t.Fatal(err)
	}
	runtime := &SecurityRuntime{mode: "oidc", current: &security.CurrentAuthority{}}
	cut := security.SemanticCut{Version: security.S1Version, Domain: [32]byte{1}, Cohort: [32]byte{2}, Generation: [16]byte{3}, Sequence: 7, Projection: [32]byte{4}, Frontier: [32]byte{5}, Fences: [32]byte{6}, Policy: [32]byte{7}}
	policy := &operationalpolicy.Policy{Version: 1, Generation: 3, Values: operationalpolicy.Values{Rate: &operationalpolicy.Rate{RPS: 0.0001, Burst: 1}}}
	projection := security.OperationalPolicyProjection{Cut: cut, Member: 101, Policies: operationalpolicy.Policies{Rate: policy}, Preparations: []security.OperationalPolicyPreparation{{Group: operationalpolicy.GroupRate, Version: 1, ChangeID: security.FullChangeID{Version: security.S1Version, Domain: cut.Domain, Cohort: cut.Cohort, Namespace: 1, Nonce: [16]byte{1}}, IntentDigest: [32]byte{8}, ActorDigest: [32]byte{9}}}}
	authority := &managedRateAuthorityTestFake{projection: projection}
	manager := &ManagedOperationalRuntime{runtime: runtime, authority: authority, bootstrap: NewOperationalBootstrapConfig(cfg)}
	owner := NewRateLimitInterceptor(cfg.RateLimit.RPS, cfg.RateLimit.Burst)
	owner.now = func() time.Time { return time.Unix(100, 0) }
	recovery, err := NewRateRecoveryRuntime(runtime, owner)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := NewManagedRateRuntime(t.Context(), manager, owner, recovery)
	if err != nil || authority.bound != cert.adapter {
		t.Fatal("verified rate policy recovery/binding failed", err)
	}
	actual := owner.Snapshot()
	if actual.Generation != 3 || actual.Config != (RateLimitConfig{RPS: 0.0001, Burst: 1}) {
		t.Fatal("environment overwrote recovered committed pair", actual)
	}
	view, err := manager.Observe(projection)
	if err != nil || view[operationalpolicy.GroupRate].AppliedGeneration != 3 || view[operationalpolicy.GroupRate].AppliedCut != cut || view[operationalpolicy.GroupRate].Pending {
		t.Fatal("restored actual observation lacks verified cut", view, err)
	}
	first, second := owner.Allow(), owner.Allow()
	if !first || second {
		t.Fatal("recovered pair did not own actual admission")
	}
	if err := manager.Reconcile(t.Context()); err != nil || owner.Allow() {
		t.Fatal("ordinary reconciliation refilled recovered bucket", err)
	}
	authority.mu.Lock()
	authority.projection.Cut.Sequence++
	newProjection := authority.projection
	authority.mu.Unlock()
	if err := manager.Reconcile(t.Context()); err != nil || owner.Allow() {
		t.Fatal("unrelated security generation refilled bucket", err)
	}
	view, err = manager.Observe(newProjection)
	if err != nil || view[operationalpolicy.GroupRate].AppliedCut != cut {
		t.Fatal("unrelated cut rewrote actual installation provenance", view, err)
	}
}
