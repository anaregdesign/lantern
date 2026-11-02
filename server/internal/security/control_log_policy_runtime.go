package security

import (
	"context"
	"errors"
)

var ErrLogPolicyUnavailable = errors.New("managed log policy owner unavailable")

// LogPolicyRuntime is a trusted in-process owner, installed by the composition
// root. It is never populated by management requests or persisted booleans.
// Prepare verifies the actual resident logger without changing its level.
// Apply installs one validated committed policy; it must be bounded and must
// not call back into the authority while the policy is being applied.
type LogPolicyRuntime interface {
	PrepareLogPolicy() error
	ApplyLogPolicy(LogPolicy) error
}

// LogPolicyPreparation links an irreversible local reader-version floor to
// its reviewed original. It is not a commit or cluster-application result.
type LogPolicyPreparation struct {
	ChangeID     FullChangeID
	IntentDigest [32]byte
	ActorDigest  [32]byte
}

// LogPolicyProjection is locally verified committed data, not fresh admission
// or a claim that this policy has been applied on any other participant.
type LogPolicyProjection struct {
	Policy       *LogPolicy
	Cut          SemanticCut
	VersionFloor bool
	Member       uint32
	Preparation  *LogPolicyPreparation
}

func (o *CurrentAuthority) BindLogPolicyRuntime(owner LogPolicyRuntime) error {
	if owner == nil || owner.PrepareLogPolicy() != nil || o == nil || o.origin == nil || !o.origin.network.enterCall() {
		return ErrLogPolicyUnavailable
	}
	defer o.origin.network.calls.Done()
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if k.readyLocked() != nil || k.logRuntime != nil {
		return ErrLogPolicyUnavailable
	}
	k.logRuntime = owner
	return nil
}

// CurrentLogPolicy supports restore before fresh quorum eligibility. Complete
// native M/P/B reconstruction has already happened; no env fallback is supplied.
// Public callers must use LogPolicyForAdmission and their normal output check.
func (o *CurrentAuthority) CurrentLogPolicy(ctx context.Context) (LogPolicyProjection, error) {
	if o == nil || o.origin == nil || !o.origin.network.enterCall() {
		return LogPolicyProjection{}, ErrAuthorityUnavailable
	}
	defer o.origin.network.calls.Done()
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if ctx.Err() != nil || k.readyLocked() != nil || k.chosen != nil {
		return LogPolicyProjection{}, ErrAuthorityUnavailable
	}
	return k.logPolicyProjectionLocked(), nil
}

func (k *s2cParticipant) logPolicyProjectionLocked() LogPolicyProjection {
	p := LogPolicyProjection{Policy: k.replayState.projection.snapshot.LogPolicy(), Cut: k.replayState.projection.cut, VersionFloor: k.logPolicyFloor.Index != 0, Member: k.config.Member}
	if p.VersionFloor {
		preparation := k.logPolicyPreparation
		p.Preparation = &preparation
	}
	return p
}

func (o *CurrentAuthority) LogPolicyForAdmission(ctx context.Context, a *Admission) (LogPolicyProjection, error) {
	if err := o.currentAdmission(ctx, a, false); err != nil {
		return LogPolicyProjection{}, err
	}
	if !a.Access().AllowsGlobal(SecurityManage) {
		return LogPolicyProjection{}, ErrPermissionDenied
	}
	p, err := o.CurrentLogPolicy(ctx)
	cut, known := a.CurrentCut()
	if err != nil || !known || p.Cut != cut {
		return LogPolicyProjection{}, ErrAuthorityUnavailable
	}
	if err = a.Check(ctx, a.expiresAt); err != nil {
		return LogPolicyProjection{}, err
	}
	return p, nil
}

func (o *CurrentAuthority) ReconcileLogPolicy(ctx context.Context) (LogPolicyProjection, error) {
	p, err := o.CurrentLogPolicy(ctx)
	if err != nil {
		return p, err
	}
	k := o.origin.network.kernel
	k.gate.Lock()
	owner := k.logRuntime
	k.gate.Unlock()
	if owner == nil || owner.PrepareLogPolicy() != nil {
		return p, ErrLogPolicyUnavailable
	}
	if p.Policy != nil {
		if err = owner.ApplyLogPolicy(*p.Policy); err != nil {
			return p, ErrLogPolicyUnavailable
		}
	}
	next, err := o.CurrentLogPolicy(ctx)
	if err != nil || next.Cut != p.Cut {
		return next, ErrAuthorityUnavailable
	}
	return next, nil
}
