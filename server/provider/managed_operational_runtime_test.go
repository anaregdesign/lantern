package provider

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

// These fakes exercise the provider's projection/observation contract only.
// They confer no native authority and are never production runtime owners.
type operationalAuthorityTestFake struct {
	mu              sync.Mutex
	projection      security.OperationalPolicyProjection
	readErr         error
	bindErr         error
	bound           map[operationalpolicy.Group]security.OperationalPolicyRuntime
	reads           int
	readTransitions map[int]security.OperationalPolicyProjection
	binds           int
	requires        int
	onBind          func()
}

func (f *operationalAuthorityTestFake) CurrentOperationalPolicies(context.Context) (security.OperationalPolicyProjection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if next, exists := f.readTransitions[f.reads]; exists {
		f.projection = next
	}
	p := f.projection
	p.Policies = p.Policies.Clone()
	p.Preparations = append([]security.OperationalPolicyPreparation(nil), p.Preparations...)
	return p, f.readErr
}

func (f *operationalAuthorityTestFake) BindOperationalPolicyRuntime(group operationalpolicy.Group, owner security.OperationalPolicyRuntime) error {
	f.mu.Lock()
	if f.bindErr != nil || f.bound[group] != nil {
		f.mu.Unlock()
		return errManagedOperational
	}
	if f.bound == nil {
		f.bound = make(map[operationalpolicy.Group]security.OperationalPolicyRuntime)
	}
	f.bound[group], f.binds = owner, f.binds+1
	hook := f.onBind
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

func (f *operationalAuthorityTestFake) RequireOperationalPolicyOwners(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requires++
	for _, prep := range f.projection.Preparations {
		owner := f.bound[prep.Group]
		if owner == nil || owner.AvailableOperationalPolicy(prep.Group, prep.Version) != nil {
			return security.ErrOperationalPolicyUnavailable
		}
	}
	return nil
}

func (f *operationalAuthorityTestFake) setProjection(p security.OperationalPolicyProjection) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projection = p
	f.projection.Policies = p.Policies.Clone()
	f.projection.Preparations = append([]security.OperationalPolicyPreparation(nil), p.Preparations...)
}

type operationalOwnerTestFake struct {
	mu                     sync.Mutex
	group                  operationalpolicy.Group
	actual                 OperationalOwnerObservation
	availableErr, applyErr error
	observeErr             error
	deferApply             bool
	applies, observations  int
}

func newOperationalOwnerTestFake(t *testing.T, group operationalpolicy.Group, mode securitytransport.OperationalSettingsChangeMode) *operationalOwnerTestFake {
	t.Helper()
	values, err := operationalpolicy.Defaults(group)
	if err != nil {
		t.Fatal(err)
	}
	owner := &operationalOwnerTestFake{group: group, actual: OperationalOwnerObservation{Effective: &values, Sources: make(map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource), Mode: mode, Error: securitytransport.OperationalErrorNone}}
	if group == operationalpolicy.GroupSearch {
		owner.actual.Fingerprint = strings.Repeat("ab", 32)
	}
	for index, field := range operationalBootstrapFields {
		if field.group == group {
			owner.actual.Sources[securitytransport.OperationalSetting(index+1)] = securitytransport.OperationalSourceDefault
		}
	}
	return owner
}

func (o *operationalOwnerTestFake) AvailableOperationalPolicy(group operationalpolicy.Group, version uint32) error {
	if o == nil {
		return security.ErrOperationalPolicyUnavailable
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if group != o.group || version != operationalpolicy.Version {
		return security.ErrOperationalPolicyUnavailable
	}
	return o.availableErr
}

func (o *operationalOwnerTestFake) PrepareOperationalPolicy(change operationalpolicy.Change) error {
	group, err := change.Values.Group()
	if err != nil || group != o.group {
		return security.ErrOperationalPolicyUnavailable
	}
	return change.Validate()
}

func (o *operationalOwnerTestFake) ApplyOperationalPolicy(policy operationalpolicy.Policy) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.applies++
	if o.applyErr != nil {
		return o.applyErr
	}
	if o.deferApply {
		return nil
	}
	if policy.Validate() != nil {
		return security.ErrOperationalPolicyUnavailable
	}
	values := policy.Values.Clone()
	o.actual.Effective, o.actual.EffectiveGeneration, o.actual.Fingerprint = &values, policy.Generation, policy.CompatibilityFingerprint
	for key := range o.actual.Sources {
		o.actual.Sources[key] = securitytransport.OperationalSourceManaged
	}
	return nil
}

func (o *operationalOwnerTestFake) ObserveOperationalPolicy() (OperationalOwnerObservation, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.observations++
	actual := o.actual
	if actual.Effective != nil {
		values := actual.Effective.Clone()
		actual.Effective = &values
	}
	actual.Sources = cloneOperationalSources(actual.Sources)
	return actual, o.observeErr
}

func operationalRuntimeTestProjection(sequence uint64) security.OperationalPolicyProjection {
	return security.OperationalPolicyProjection{Member: 1, Cut: security.SemanticCut{Version: security.S1Version,
		Domain: [32]byte{1}, Cohort: [32]byte{2}, Generation: [16]byte{3}, Sequence: sequence,
		Projection: [32]byte{4, byte(sequence)}, Frontier: [32]byte{5}, Fences: [32]byte{6}, Policy: [32]byte{7}}}
}

func operationalRuntimeTestPolicy(t *testing.T, group operationalpolicy.Group, generation uint64) *operationalpolicy.Policy {
	t.Helper()
	values, err := operationalpolicy.Defaults(group)
	if err != nil {
		t.Fatal(err)
	}
	if group == operationalpolicy.GroupSlowRPC {
		values.SlowRPC.ThresholdMS = int64(generation) * 10
	}
	policy := &operationalpolicy.Policy{Version: operationalpolicy.Version, Generation: generation, Values: values}
	if group == operationalpolicy.GroupSearch {
		policy.CompatibilityFingerprint = strings.Repeat("ab", 32)
	}
	return policy
}

func operationalRuntimeTestPrepare(p security.OperationalPolicyProjection, group operationalpolicy.Group) security.OperationalPolicyProjection {
	p.Preparations = append(p.Preparations, security.OperationalPolicyPreparation{Group: group, Version: operationalpolicy.Version,
		ChangeID:     security.FullChangeID{Version: security.S1Version, Domain: p.Cut.Domain, Cohort: p.Cut.Cohort, Namespace: 1, Nonce: [16]byte{8}},
		IntentDigest: [32]byte{9}, ActorDigest: [32]byte{10}})
	return p
}

func operationalRuntimeTestManaged(t *testing.T, sequence, generation uint64) security.OperationalPolicyProjection {
	t.Helper()
	p := operationalRuntimeTestPrepare(operationalRuntimeTestProjection(sequence), operationalpolicy.GroupSlowRPC)
	if err := p.Policies.Set(operationalpolicy.GroupSlowRPC, operationalRuntimeTestPolicy(t, operationalpolicy.GroupSlowRPC, generation)); err != nil {
		t.Fatal(err)
	}
	return p
}

func operationalRuntimeTestManager(t *testing.T, p security.OperationalPolicyProjection) (*ManagedOperationalRuntime, *operationalAuthorityTestFake) {
	t.Helper()
	fake := &operationalAuthorityTestFake{}
	fake.setProjection(p)
	m := &ManagedOperationalRuntime{runtime: &SecurityRuntime{mode: "oidc"}, authority: fake}
	if err := m.acceptProjection(p); err != nil {
		t.Fatal(err)
	}
	return m, fake
}

func TestManagedOperationalRuntimeClosedRegistrationAndDetachedBootstrap(t *testing.T) {
	p := operationalRuntimeTestProjection(1)
	m, authority := operationalRuntimeTestManager(t, p)
	if m.Register(t.Context(), "unknown", newOperationalOwnerTestFake(t, operationalpolicy.GroupSlowRPC, securitytransport.OperationalModeHot)) == nil ||
		m.Register(t.Context(), operationalpolicy.GroupSlowRPC, nil) == nil ||
		m.Register(t.Context(), operationalpolicy.GroupSlowRPC, newOperationalOwnerTestFake(t, operationalpolicy.GroupRate, securitytransport.OperationalModeHot)) == nil {
		t.Fatal("unknown, absent or unsupported owner registered")
	}
	for _, group := range operationalpolicy.Groups() {
		owner := newOperationalOwnerTestFake(t, group, securitytransport.OperationalModeHot)
		if err := m.Register(t.Context(), group, owner); err != nil {
			t.Fatal(err)
		}
		if m.Register(t.Context(), group, owner) == nil {
			t.Fatal("duplicate registration accepted")
		}
	}
	observed, err := m.Observe(p)
	if err != nil || len(observed) != 8 || authority.binds != 8 {
		t.Fatal("closed bootstrap registration failed", err)
	}
	for _, actual := range observed {
		if actual.Effective == nil || actual.AppliedGeneration != 0 || actual.AppliedCut != (security.SemanticCut{}) || actual.Pending {
			t.Fatal("bootstrap observation invented managed application")
		}
	}
	slow := observed[operationalpolicy.GroupSlowRPC]
	slow.Effective.SlowRPC.ThresholdMS++
	for key := range slow.Sources {
		slow.Sources[key] = securitytransport.OperationalSourceEnvironment
	}
	again, err := m.Observe(p)
	if err != nil || again[operationalpolicy.GroupSlowRPC].Effective.SlowRPC.ThresholdMS != 500 ||
		again[operationalpolicy.GroupSlowRPC].Sources[1] != securitytransport.OperationalSourceDefault {
		t.Fatal("observer leaked bootstrap effective/source ownership", err)
	}
}

func TestManagedOperationalRuntimeProjectionMonotonicity(t *testing.T) {
	initial := operationalRuntimeTestManaged(t, 2, 1)
	for name, edit := range map[string]func(*security.OperationalPolicyProjection){
		"older cut":                   func(p *security.OperationalPolicyProjection) { p.Cut.Sequence-- },
		"same sequence different cut": func(p *security.OperationalPolicyProjection) { p.Cut.Frontier[0] ^= 1 },
		"same cut different policy": func(p *security.OperationalPolicyProjection) {
			p.Policies.SlowRPC = operationalRuntimeTestPolicy(t, operationalpolicy.GroupSlowRPC, 2)
		},
		"same generation different value": func(p *security.OperationalPolicyProjection) {
			p.Cut.Sequence++
			p.Policies.SlowRPC.Values.SlowRPC.ThresholdMS++
		},
		"removed policy":      func(p *security.OperationalPolicyProjection) { p.Cut.Sequence++; p.Policies.SlowRPC = nil },
		"removed preparation": func(p *security.OperationalPolicyProjection) { p.Cut.Sequence++; p.Preparations = nil },
		"changed preparation": func(p *security.OperationalPolicyProjection) { p.Preparations[0].ActorDigest[0] ^= 1 },
		"member":              func(p *security.OperationalPolicyProjection) { p.Member++ },
		"domain":              func(p *security.OperationalPolicyProjection) { p.Cut.Domain[0] ^= 1 },
	} {
		t.Run(name, func(t *testing.T) {
			m, _ := operationalRuntimeTestManager(t, initial)
			bad := detachOperationalProjection(initial)
			edit(&bad)
			if err := m.acceptProjection(bad); err == nil || !reflect.DeepEqual(m.projection, initial) {
				t.Fatal("contradictory projection accepted or replaced last verified state", err)
			}
		})
	}
	p := operationalRuntimeTestProjection(1)
	m, _ := operationalRuntimeTestManager(t, p)
	prepared := operationalRuntimeTestPrepare(p, operationalpolicy.GroupSlowRPC)
	if err := m.acceptProjection(prepared); err != nil || m.projection.Policies.SlowRPC != nil {
		t.Fatal("prepared-only same-cut floor was treated as application", err)
	}
}

func TestManagedOperationalRuntimeStartupCatchupAndAppliedCut(t *testing.T) {
	initial := operationalRuntimeTestManaged(t, 1, 1)
	next := operationalRuntimeTestManaged(t, 2, 2)
	m, authority := operationalRuntimeTestManager(t, initial)
	authority.onBind = func() { authority.setProjection(next) }
	owner := newOperationalOwnerTestFake(t, operationalpolicy.GroupSlowRPC, securitytransport.OperationalModeHot)
	if err := m.Register(t.Context(), operationalpolicy.GroupSlowRPC, owner); err != nil {
		t.Fatal(err)
	}
	observed, err := m.Observe(next)
	actual := observed[operationalpolicy.GroupSlowRPC]
	if err != nil || actual.AppliedGeneration != 2 || actual.AppliedCut != next.Cut || actual.Pending || owner.applies < 2 {
		t.Fatal("registration certified only its stale first read", actual, err)
	}
	unrelated := detachOperationalProjection(next)
	unrelated.Cut.Sequence++
	unrelated.Cut.Frontier[0]++
	authority.setProjection(unrelated)
	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	observed, err = m.Observe(unrelated)
	if err != nil || observed[operationalpolicy.GroupSlowRPC].AppliedCut != next.Cut {
		t.Fatal("unrelated security revision rewrote application proof", err)
	}
	authority.mu.Lock()
	authority.readErr = errors.New("authority unavailable")
	authority.mu.Unlock()
	if m.Reconcile(t.Context()) == nil {
		t.Fatal("failed authority read silently succeeded")
	}
	retained, err := owner.ObserveOperationalPolicy()
	if err != nil || retained.EffectiveGeneration != 2 || retained.Effective.SlowRPC.ThresholdMS != 20 {
		t.Fatal("authority failure reverted to bootstrap/default", err)
	}
}

func TestManagedOperationalRuntimeApplyReturnIsNotInstallation(t *testing.T) {
	p := operationalRuntimeTestProjection(1)
	m, authority := operationalRuntimeTestManager(t, p)
	owner := newOperationalOwnerTestFake(t, operationalpolicy.GroupSlowRPC, securitytransport.OperationalModeProcessRestart)
	if err := m.Register(t.Context(), operationalpolicy.GroupSlowRPC, owner); err != nil {
		t.Fatal(err)
	}
	owner.deferApply = true
	next := operationalRuntimeTestManaged(t, 2, 1)
	authority.setProjection(next)
	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	observed, err := m.Observe(next)
	actual := observed[operationalpolicy.GroupSlowRPC]
	if err != nil || !actual.Pending || !actual.RestartRequired || actual.AppliedGeneration != 0 || actual.AppliedCut != (security.SemanticCut{}) || m.slowRPC.applied != nil {
		t.Fatal("nil Apply error masqueraded as installed restart policy", actual, err)
	}
	startup, startupAuthority := operationalRuntimeTestManager(t, next)
	if startup.Register(t.Context(), operationalpolicy.GroupSlowRPC, owner) == nil || startupAuthority.binds != 0 {
		t.Fatal("startup bound an owner without restoring committed desired values")
	}
}

func TestManagedOperationalRuntimeWithholdsProofWhenFreshCutMoves(t *testing.T) {
	initial := operationalRuntimeTestManaged(t, 1, 1)
	next := operationalRuntimeTestManaged(t, 2, 2)
	m, authority := operationalRuntimeTestManager(t, initial)
	// Register's first read and post-bind reconcile see generation1. The
	// independent post-application verification sees generation2 instead.
	authority.readTransitions = map[int]security.OperationalPolicyProjection{3: next}
	owner := newOperationalOwnerTestFake(t, operationalpolicy.GroupSlowRPC, securitytransport.OperationalModeHot)
	if err := m.Register(t.Context(), operationalpolicy.GroupSlowRPC, owner); err != nil {
		t.Fatal(err)
	}
	if m.slowRPC.applied != nil {
		t.Fatal("moving cut created a falsely fresh installation proof")
	}
	if _, err := m.Observe(next); err == nil {
		t.Fatal("status certified actual values without a stable applied cut")
	}
	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	observed, err := m.Observe(next)
	if err != nil || observed[operationalpolicy.GroupSlowRPC].AppliedGeneration != 2 || observed[operationalpolicy.GroupSlowRPC].AppliedCut != next.Cut {
		t.Fatal("stable subsequent reconciliation did not restore evidence", err)
	}
}

func TestManagedOperationalRuntimeFreshObserveNeverApplies(t *testing.T) {
	initial := operationalRuntimeTestManaged(t, 1, 1)
	m, authority := operationalRuntimeTestManager(t, initial)
	owner := newOperationalOwnerTestFake(t, operationalpolicy.GroupSlowRPC, securitytransport.OperationalModeHot)
	if err := m.Register(t.Context(), operationalpolicy.GroupSlowRPC, owner); err != nil {
		t.Fatal(err)
	}
	before := owner.applies
	next := operationalRuntimeTestManaged(t, 2, 2)
	authority.setProjection(next)
	observed, err := m.Observe(next)
	actual := observed[operationalpolicy.GroupSlowRPC]
	if err != nil || owner.applies != before || !actual.Pending || actual.AppliedGeneration != 1 || actual.AppliedCut != initial.Cut {
		t.Fatal("fresh observer applied state or fabricated freshness", actual, err)
	}
	owner.applyErr = errors.New("owner could not apply")
	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	observed, err = m.Observe(next)
	if err != nil || observed[operationalpolicy.GroupSlowRPC].Error != securitytransport.OperationalErrorApplyFailed || !observed[operationalpolicy.GroupSlowRPC].Pending {
		t.Fatal("apply failure hidden by previous effective values", err)
	}
	owner.observeErr = errors.New("actual state unavailable")
	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Observe(next); err == nil {
		t.Fatal("unobservable owner reported an applied status")
	}
}

func TestManagedOperationalRuntimeConstructorAndUnsupportedGroups(t *testing.T) {
	for _, runtime := range []*SecurityRuntime{nil, {mode: "unknown"}, {mode: "oidc"}} {
		if _, err := NewManagedOperationalRuntime(t.Context(), runtime, OperationalBootstrapConfig{}); err == nil {
			t.Fatal("missing native runtime accepted")
		}
	}
	runtime := &SecurityRuntime{mode: "off"}
	m, err := NewManagedOperationalRuntime(t.Context(), runtime, OperationalBootstrapConfig{})
	if err != nil || runtime.managedOperational != m {
		t.Fatal("OFF composition failed", err)
	}
	if _, err := NewManagedOperationalRuntime(t.Context(), runtime, OperationalBootstrapConfig{}); err == nil {
		t.Fatal("duplicate runtime manager accepted")
	}
	prepared := operationalRuntimeTestPrepare(operationalRuntimeTestProjection(1), operationalpolicy.GroupSearch)
	m, _ = operationalRuntimeTestManager(t, prepared)
	observed, err := m.Observe(prepared)
	if err != nil || observed[operationalpolicy.GroupSearch].Effective != nil || observed[operationalpolicy.GroupSearch].Mode != securitytransport.OperationalModeUnavailable {
		t.Fatal("prepared-but-unsupported group invented an actual owner", err)
	}
}
