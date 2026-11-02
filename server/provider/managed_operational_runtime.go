package provider

import (
	"context"
	"errors"
	"reflect"
	"sync"

	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

var errManagedOperational = errors.New("managed operational runtime unavailable")

// OperationalRuntimeOwner is an actual installed group resource. Callbacks
// never read authority, including when native support holds its own lock.
// Observation reports installation independently from an Apply return value.
type OperationalRuntimeOwner interface {
	security.OperationalPolicyRuntime
	ObserveOperationalPolicy() (OperationalOwnerObservation, error)
}

type OperationalOwnerObservation struct {
	Effective           *operationalpolicy.Values
	EffectiveGeneration uint64
	Sources             map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource
	Fingerprint         string
	Mode                securitytransport.OperationalSettingsChangeMode
	Error               securitytransport.OperationalSettingsError
}

type operationalPolicyAuthority interface {
	CurrentOperationalPolicies(context.Context) (security.OperationalPolicyProjection, error)
	BindOperationalPolicyRuntime(operationalpolicy.Group, security.OperationalPolicyRuntime) error
	RequireOperationalPolicyOwners(context.Context) error
}

type operationalRegistration struct {
	owner      OperationalRuntimeOwner
	applied    *operationalpolicy.Policy
	appliedCut security.SemanticCut
	lastError  securitytransport.OperationalSettingsError
}

// ManagedOperationalRuntime owns the closed registration and verified
// installation provenance. Group providers supply actual resources separately;
// an unimplemented group has no owner and advertises no support.
type ManagedOperationalRuntime struct {
	refresh                                                                  sync.Mutex
	mu                                                                       sync.Mutex
	runtime                                                                  *SecurityRuntime
	authority                                                                operationalPolicyAuthority
	bootstrap                                                                OperationalBootstrapConfig
	projection                                                               security.OperationalPolicyProjection
	slowRPC, rate, request, backup, capacity, lifecycle, search, replication *operationalRegistration
}

func NewManagedOperationalRuntime(ctx context.Context, runtime *SecurityRuntime, bootstrap OperationalBootstrapConfig) (*ManagedOperationalRuntime, error) {
	if runtime == nil || runtime.managedOperational != nil || ctx.Err() != nil {
		return nil, errManagedOperational
	}
	m := &ManagedOperationalRuntime{runtime: runtime, bootstrap: bootstrap}
	switch runtime.mode {
	case "off":
	case "oidc":
		if runtime.current == nil {
			return nil, errManagedOperational
		}
		m.authority = runtime.current
		p, err := m.authority.CurrentOperationalPolicies(ctx)
		if err != nil || m.acceptProjection(p) != nil {
			return nil, errManagedOperational
		}
	default:
		return nil, errManagedOperational
	}
	runtime.managedOperational = m
	return m, nil
}

func (m *ManagedOperationalRuntime) entry(group operationalpolicy.Group) *operationalRegistration {
	switch group {
	case operationalpolicy.GroupSlowRPC:
		return m.slowRPC
	case operationalpolicy.GroupRate:
		return m.rate
	case operationalpolicy.GroupRequest:
		return m.request
	case operationalpolicy.GroupBackup:
		return m.backup
	case operationalpolicy.GroupCapacity:
		return m.capacity
	case operationalpolicy.GroupLifecycle:
		return m.lifecycle
	case operationalpolicy.GroupSearch:
		return m.search
	case operationalpolicy.GroupReplication:
		return m.replication
	default:
		return nil
	}
}
func (m *ManagedOperationalRuntime) setEntry(group operationalpolicy.Group, entry *operationalRegistration) {
	switch group {
	case operationalpolicy.GroupSlowRPC:
		m.slowRPC = entry
	case operationalpolicy.GroupRate:
		m.rate = entry
	case operationalpolicy.GroupRequest:
		m.request = entry
	case operationalpolicy.GroupBackup:
		m.backup = entry
	case operationalpolicy.GroupCapacity:
		m.capacity = entry
	case operationalpolicy.GroupLifecycle:
		m.lifecycle = entry
	case operationalpolicy.GroupSearch:
		m.search = entry
	case operationalpolicy.GroupReplication:
		m.replication = entry
	}
}

func detachOperationalProjection(p security.OperationalPolicyProjection) security.OperationalPolicyProjection {
	p.Policies = p.Policies.Clone()
	p.Preparations = append([]security.OperationalPolicyPreparation(nil), p.Preparations...)
	return p
}

func validateOperationalProjection(p security.OperationalPolicyProjection) error {
	c := p.Cut
	if p.Member == 0 || c.Version != security.S1Version || c.Sequence == 0 || c.Domain == [32]byte{} || c.Cohort == [32]byte{} || c.Generation == [16]byte{} || c.Projection == [32]byte{} || c.Frontier == [32]byte{} || c.Fences == [32]byte{} || c.Policy == [32]byte{} || p.Policies.ValidateNilAllowed() != nil || len(p.Preparations) > 8 {
		return errManagedOperational
	}
	seen := map[operationalpolicy.Group]bool{}
	for _, prep := range p.Preparations {
		id := prep.ChangeID
		if !prep.Group.Valid() || seen[prep.Group] || prep.Version != operationalpolicy.Version || id.Version != security.S1Version || id.Domain != c.Domain || id.Cohort != c.Cohort || id.Namespace == 0 || id.Nonce == [16]byte{} || prep.IntentDigest == [32]byte{} || prep.ActorDigest == [32]byte{} {
			return errManagedOperational
		}
		seen[prep.Group] = true
	}
	for _, group := range operationalpolicy.Groups() {
		if p.Policies.Get(group) != nil && !seen[group] {
			return errManagedOperational
		}
	}
	return nil
}

// Caller owns mu. A prepared floor may appear at the same semantic cut, but
// no retained floor/policy can disappear or change identity on observation.
func (m *ManagedOperationalRuntime) acceptProjection(p security.OperationalPolicyProjection) error {
	if validateOperationalProjection(p) != nil {
		return errManagedOperational
	}
	previous := m.projection
	if previous.Member != 0 {
		a, b := previous.Cut, p.Cut
		if previous.Member != p.Member || a.Version != b.Version || a.Domain != b.Domain || a.Cohort != b.Cohort || a.Generation != b.Generation || b.Sequence < a.Sequence || b.Sequence == a.Sequence && b != a {
			return errManagedOperational
		}
		if b.Sequence == a.Sequence && !reflect.DeepEqual(previous.Policies, p.Policies) {
			return errManagedOperational
		}
		for _, group := range operationalpolicy.Groups() {
			old, next := previous.Policies.Get(group), p.Policies.Get(group)
			if old != nil && (next == nil || next.Generation < old.Generation || next.Generation == old.Generation && !reflect.DeepEqual(old, next)) {
				return errManagedOperational
			}
		}
		for _, old := range previous.Preparations {
			found := false
			for _, next := range p.Preparations {
				if next.Group == old.Group {
					found = next == old
					break
				}
			}
			if !found {
				return errManagedOperational
			}
		}
	}
	m.projection = detachOperationalProjection(p)
	return nil
}

// Register restores one supplied actual owner. Missing future owners remain
// absent. A hot recovered policy must actually install before native binding.
func (m *ManagedOperationalRuntime) Register(ctx context.Context, group operationalpolicy.Group, owner OperationalRuntimeOwner) error {
	if m == nil || !group.Valid() || owner == nil || owner.AvailableOperationalPolicy(group, operationalpolicy.Version) != nil {
		return errManagedOperational
	}
	m.refresh.Lock()
	defer m.refresh.Unlock()
	m.mu.Lock()
	if m.entry(group) != nil {
		m.mu.Unlock()
		return errManagedOperational
	}
	m.mu.Unlock()
	entry := &operationalRegistration{owner: owner, lastError: securitytransport.OperationalErrorNone}
	if m.authority != nil {
		p, err := m.authority.CurrentOperationalPolicies(ctx)
		if err != nil {
			return errManagedOperational
		}
		m.mu.Lock()
		err = m.acceptProjection(p)
		m.mu.Unlock()
		if err != nil {
			return err
		}
		if policy := p.Policies.Get(group); policy != nil {
			if owner.ApplyOperationalPolicy(*policy) != nil {
				return errManagedOperational
			}
			actual, observeErr := owner.ObserveOperationalPolicy()
			if observeErr != nil || !operationalActualMatches(actual, policy) {
				return errManagedOperational
			}
		}
		if err = m.authority.BindOperationalPolicyRuntime(group, owner); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.setEntry(group, entry)
	m.mu.Unlock()
	// The private listener can catch up during construction. Re-read after
	// binding; never certify an owner against only its initial boot read.
	return m.reconcileLocked(ctx)
}

func operationalActualMatches(actual OperationalOwnerObservation, policy *operationalpolicy.Policy) bool {
	return policy != nil && actual.Effective != nil && actual.EffectiveGeneration == policy.Generation && reflect.DeepEqual(*actual.Effective, policy.Values) && actual.Fingerprint == policy.CompatibilityFingerprint
}

func (m *ManagedOperationalRuntime) Reconcile(ctx context.Context) error {
	if m == nil {
		return errManagedOperational
	}
	m.refresh.Lock()
	defer m.refresh.Unlock()
	return m.reconcileLocked(ctx)
}
func (m *ManagedOperationalRuntime) reconcileLocked(ctx context.Context) error {
	if ctx.Err() != nil {
		return errManagedOperational
	}
	if m.authority == nil {
		return nil
	}
	p, err := m.authority.CurrentOperationalPolicies(ctx)
	if err != nil {
		return errManagedOperational
	}
	m.mu.Lock()
	err = m.acceptProjection(p)
	m.mu.Unlock()
	if err != nil {
		return err
	}
	for _, group := range operationalpolicy.Groups() {
		m.mu.Lock()
		entry := m.entry(group)
		m.mu.Unlock()
		policy := p.Policies.Get(group)
		if entry == nil || policy == nil {
			continue
		}
		applyErr := entry.owner.ApplyOperationalPolicy(*policy)
		actual, observeErr := entry.owner.ObserveOperationalPolicy()
		m.mu.Lock()
		entry.lastError = securitytransport.OperationalErrorNone
		if applyErr != nil || observeErr != nil {
			entry.lastError = securitytransport.OperationalErrorApplyFailed
		}
		m.mu.Unlock()
		if observeErr != nil || !operationalActualMatches(actual, policy) {
			continue
		}
		next, readErr := m.authority.CurrentOperationalPolicies(ctx)
		if readErr != nil {
			return errManagedOperational
		}
		m.mu.Lock()
		err = m.acceptProjection(next)
		if err == nil && next.Cut == p.Cut && (entry.applied == nil || entry.applied.Generation != policy.Generation) {
			entry.applied, entry.appliedCut = policy.Clone(), p.Cut
		}
		m.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func cloneOperationalSources(sources map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource) map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource {
	if sources == nil {
		return nil
	}
	copy := make(map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource, len(sources))
	for key, value := range sources {
		copy[key] = value
	}
	return copy
}

func (m *ManagedOperationalRuntime) Observe(p security.OperationalPolicyProjection) (map[operationalpolicy.Group]securitytransport.OperationalSettingsObservation, error) {
	if m == nil || m.authority == nil {
		return nil, errManagedOperational
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.acceptProjection(p) != nil {
		return nil, errManagedOperational
	}
	result := make(map[operationalpolicy.Group]securitytransport.OperationalSettingsObservation, 8)
	for _, group := range operationalpolicy.Groups() {
		entry := m.entry(group)
		if entry == nil {
			result[group] = securitytransport.UnavailableOperationalSettingsObservation()
			continue
		}
		actual, err := entry.owner.ObserveOperationalPolicy()
		if err != nil {
			return nil, errManagedOperational
		}
		policy := p.Policies.Get(group)
		desiredGeneration := uint64(0)
		if policy != nil {
			desiredGeneration = policy.Generation
		}
		if actual.EffectiveGeneration > desiredGeneration {
			return nil, errManagedOperational
		}
		observed := securitytransport.OperationalSettingsObservation{EffectiveCompatibilityFingerprint: actual.Fingerprint, Effective: actual.Effective, Sources: cloneOperationalSources(actual.Sources), AppliedGeneration: actual.EffectiveGeneration, Mode: actual.Mode, Error: actual.Error, Pending: actual.EffectiveGeneration < desiredGeneration}
		if actual.Effective != nil {
			copy := actual.Effective.Clone()
			observed.Effective = &copy
		}
		if observed.Pending && (actual.Mode == securitytransport.OperationalModeProcessRestart || actual.Mode == securitytransport.OperationalModeCoordinatedRestart) {
			observed.RestartRequired = true
		}
		if actual.EffectiveGeneration != 0 {
			if entry.applied == nil || !operationalActualMatches(actual, entry.applied) {
				return nil, errManagedOperational
			}
			observed.AppliedCut = entry.appliedCut
			if actual.EffectiveGeneration == desiredGeneration && !operationalActualMatches(actual, policy) {
				return nil, errManagedOperational
			}
		} else if entry.applied != nil {
			return nil, errManagedOperational
		}
		if entry.lastError != securitytransport.OperationalErrorNone {
			observed.Error = entry.lastError
		}
		result[group] = observed
	}
	return result, nil
}

func (m *ManagedOperationalRuntime) BootstrapSources(group operationalpolicy.Group) (map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource, error) {
	if m == nil || !group.Valid() {
		return nil, errManagedOperational
	}
	fields, known := m.bootstrap.Fields(group)
	if !known {
		return nil, errManagedOperational
	}
	result := make(map[securitytransport.OperationalSetting]securitytransport.OperationalSettingsSource, len(fields))
	for _, field := range fields {
		for index, known := range operationalBootstrapFields {
			if known.key != field.Key {
				continue
			}
			source := securitytransport.OperationalSourceDefault
			switch field.Source {
			case OperationalBootstrapDefault:
			case OperationalBootstrapEnvironment:
				source = securitytransport.OperationalSourceEnvironment
			case OperationalBootstrapDerived:
				source = securitytransport.OperationalSourceDerived
			default:
				return nil, errManagedOperational
			}
			result[securitytransport.OperationalSetting(index+1)] = source
		}
	}
	return result, nil
}

func (r *SecurityRuntime) operationalSettingsObservation(p security.OperationalPolicyProjection) (map[operationalpolicy.Group]securitytransport.OperationalSettingsObservation, error) {
	if r == nil || r.managedOperational == nil {
		return nil, errManagedOperational
	}
	return r.managedOperational.Observe(p)
}
