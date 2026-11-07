package security

import (
	"context"
	"errors"

	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

var ErrOperationalPolicyUnavailable = errors.New("managed operational policy owner unavailable")

// OperationalPolicyRuntime is an actual trusted resident owner. Available
// checks its installed application contract, independently of candidate values.
// Prepare validates local dependencies without mutation. Apply accepts only
// committed desired state; its success does not assert synchronous application.
// Implementations must not call the authority from any of these methods.
type OperationalPolicyRuntime interface {
	AvailableOperationalPolicy(operationalpolicy.Group, uint32) error
	PrepareOperationalPolicy(operationalpolicy.Change) error
	ApplyOperationalPolicy(operationalpolicy.Policy) error
}

// OperationalPolicyResolver resolves Search compatibility from the actual
// excluded local baseline. Preview is read-only; Prepare independently checks
// the sealed fingerprint again on every participant and every Search update.
type OperationalPolicyResolver interface {
	PreviewOperationalPolicy(operationalpolicy.Values) (string, error)
}

type OperationalPolicyPreparation struct {
	Group        operationalpolicy.Group
	Version      uint32
	ChangeID     FullChangeID
	IntentDigest [32]byte
	ActorDigest  [32]byte
}

// OperationalPolicyProjection contains locally verified desired state only.
// Preparations are reader/application floors, including uncommitted originals.
// Effective values and pending/error state belong to the actual node owners.
type OperationalPolicyProjection struct {
	Policies     operationalpolicy.Policies
	Cut          SemanticCut
	Member       uint32
	Preparations []OperationalPolicyPreparation
}

func (o *CurrentAuthority) BindOperationalPolicyRuntime(group operationalpolicy.Group, owner OperationalPolicyRuntime) error {
	if !group.Valid() || owner == nil || owner.AvailableOperationalPolicy(group, operationalpolicy.Version) != nil || o == nil || o.origin == nil || !o.origin.network.enterCall() {
		return ErrOperationalPolicyUnavailable
	}
	defer o.origin.network.calls.Done()
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if k.readyLocked() != nil || k.operationalRuntimes[group] != nil {
		return ErrOperationalPolicyUnavailable
	}
	k.operationalRuntimes[group] = owner
	return nil
}

func (o *s2cParticipant) operationalOwnersAvailableLocked() error {
	policies := o.replayState.projection.snapshot.OperationalPolicies()
	for _, group := range operationalpolicy.Groups() {
		floor := o.operationalFloors[group]
		if floor == nil && policies.Get(group) == nil {
			continue
		}
		owner := o.operationalRuntimes[group]
		if floor == nil || owner == nil || owner.AvailableOperationalPolicy(group, operationalpolicy.Version) != nil {
			return ErrOperationalPolicyUnavailable
		}
	}
	return nil
}

// RequireOperationalPolicyOwners must run after native/data recovery and owner
// binding, before public certification. Even a PREPARED-only floor requires the
// actual application owner; it never makes the uncommitted candidate effective.
func (o *CurrentAuthority) RequireOperationalPolicyOwners(ctx context.Context) error {
	if o == nil || o.origin == nil || !o.origin.network.enterCall() {
		return ErrAuthorityUnavailable
	}
	defer o.origin.network.calls.Done()
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	if ctx.Err() != nil || k.readyLocked() != nil || k.chosen != nil {
		return ErrAuthorityUnavailable
	}
	return k.operationalOwnersAvailableLocked()
}

func (o *CurrentAuthority) CurrentOperationalPolicies(ctx context.Context) (OperationalPolicyProjection, error) {
	if o == nil || o.origin == nil || !o.origin.network.enterCall() {
		return OperationalPolicyProjection{}, ErrAuthorityUnavailable
	}
	defer o.origin.network.calls.Done()
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if ctx.Err() != nil || k.readyLocked() != nil || k.chosen != nil {
		return OperationalPolicyProjection{}, ErrAuthorityUnavailable
	}
	return k.operationalPolicyProjectionLocked(), nil
}

func (o *s2cParticipant) operationalPolicyProjectionLocked() OperationalPolicyProjection {
	p := OperationalPolicyProjection{Policies: o.replayState.projection.snapshot.OperationalPolicies(), Cut: o.replayState.projection.cut, Member: o.config.Member}
	for _, group := range operationalpolicy.Groups() {
		if floor := o.operationalFloors[group]; floor != nil {
			p.Preparations = append(p.Preparations, floor.preparation)
		}
	}
	return p
}

func (o *CurrentAuthority) OperationalPoliciesForAdmission(ctx context.Context, a *Admission) (OperationalPolicyProjection, error) {
	if err := o.currentAdmission(ctx, a, false); err != nil {
		return OperationalPolicyProjection{}, err
	}
	if !a.Access().AllowsGlobal(SecurityManage) {
		return OperationalPolicyProjection{}, ErrPermissionDenied
	}
	p, err := o.CurrentOperationalPolicies(ctx)
	cut, known := a.CurrentCut()
	if err != nil || !known || p.Cut != cut {
		return OperationalPolicyProjection{}, ErrAuthorityUnavailable
	}
	if err = a.Check(ctx, a.expiresAt); err != nil {
		return OperationalPolicyProjection{}, err
	}
	return p, nil
}

// PreviewOperationalPolicy never allocates an original, consumes credentials,
// persists a floor or applies values. The returned fingerprint must be included
// in the later reviewed Search change and rechecked by actual participant owners.
func (o *CurrentAuthority) PreviewOperationalPolicy(ctx context.Context, a *Admission, values operationalpolicy.Values) (string, error) {
	if values.Validate() != nil {
		return "", ErrInvalidImage
	}
	p, err := o.OperationalPoliciesForAdmission(ctx, a)
	if err != nil {
		return "", err
	}
	group, _ := values.Group()
	k := o.origin.network.kernel
	k.gate.Lock()
	owner := k.operationalRuntimes[group]
	k.gate.Unlock()
	if owner == nil || owner.AvailableOperationalPolicy(group, operationalpolicy.Version) != nil {
		return "", ErrOperationalPolicyUnavailable
	}
	fingerprint := ""
	if group == operationalpolicy.GroupSearch {
		resolver, ok := owner.(OperationalPolicyResolver)
		if !ok {
			return "", ErrOperationalPolicyUnavailable
		}
		fingerprint, err = resolver.PreviewOperationalPolicy(values.Clone())
		if err != nil {
			return "", ErrOperationalPolicyUnavailable
		}
	}
	change := operationalpolicy.Change{Values: values.Clone(), CompatibilityFingerprint: fingerprint}
	if prior := p.Policies.Get(group); prior != nil {
		change.ExpectedGeneration = prior.Generation
	}
	if change.Validate() != nil || group == operationalpolicy.GroupSearch && owner.PrepareOperationalPolicy(change) != nil {
		return "", ErrOperationalPolicyUnavailable
	}
	next, err := o.OperationalPoliciesForAdmission(ctx, a)
	if err != nil || next.Cut != p.Cut {
		return "", ErrAuthorityUnavailable
	}
	return fingerprint, nil
}

func (o *CurrentAuthority) ReconcileOperationalPolicies(ctx context.Context) (OperationalPolicyProjection, error) {
	p, err := o.CurrentOperationalPolicies(ctx)
	if err != nil {
		return p, err
	}
	if err = o.RequireOperationalPolicyOwners(ctx); err != nil {
		return p, err
	}
	k := o.origin.network.kernel
	for _, group := range operationalpolicy.Groups() {
		policy := p.Policies.Get(group)
		if policy == nil {
			continue
		}
		k.gate.Lock()
		owner := k.operationalRuntimes[group]
		k.gate.Unlock()
		if owner == nil || owner.ApplyOperationalPolicy(*policy) != nil {
			return p, ErrOperationalPolicyUnavailable
		}
	}
	next, err := o.CurrentOperationalPolicies(ctx)
	if err != nil || next.Cut != p.Cut {
		return next, ErrAuthorityUnavailable
	}
	return next, nil
}
