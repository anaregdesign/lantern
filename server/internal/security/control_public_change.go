package security

import (
	"context"
	"errors"
	"time"
)

type CurrentReview struct {
	Profile   CurrentProfile
	ID        FullChangeID
	Operation OperationIdentity
}

func (r CurrentReview) Command() (S1Command, error) { return r.Operation.command() }

// Original disclosure is either explicit security.manage or the same human's
// session operation. Possession of a random ID/intent alone grants nothing.
func (o *CurrentAuthority) CheckOriginalDisclosure(ctx context.Context, a *Admission, id FullChangeID, intent [32]byte) error {
	if err := o.currentAdmission(ctx, a, false); err != nil {
		return err
	}
	if a.Access().AllowsGlobal(SecurityManage) {
		return nil
	}
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	if !a.current.credential.matches(k.replayState.projection) {
		return ErrAuthorityUnavailable
	}
	var op OperationIdentity
	if result := k.replayState.ledger[id]; result != nil {
		op = result.operation
	} else {
		for _, h := range k.origins {
			if h.handoff.id == id {
				op = h.handoff.operation
				break
			}
		}
		if op.digest == [32]byte{} {
			for r := range o.origin.requests {
				if r.id == id {
					op = r.operation
					break
				}
			}
		}
	}
	command, err := op.command()
	if err != nil || op.digest != intent || op.actor != a.Identity() || a.Authentication().Class != EndUser || (command.Kind != S1IssueSession && command.Kind != S1RevokeSession) {
		return ErrPermissionDenied
	}
	return nil
}

type CurrentProgress string

const (
	CurrentUnresolved    CurrentProgress = "unresolved"
	CurrentOriginDurable CurrentProgress = "origin_durable"
	CurrentChosen        CurrentProgress = "chosen"
	CurrentApplied       CurrentProgress = "applied"
)

type CurrentChangeResult struct {
	Profile         CurrentProfile
	ID              FullChangeID
	Intent          [32]byte
	Progress        CurrentProgress
	Original        *OriginalOutcome
	StopObservation CurrentStopObservation
}

// DecodeReview validates public data, not an authorization or H. An ID which
// was not minted by an enrolled origin cannot allocate a new consume below.
func (o *CurrentAuthority) DecodeReview(profile CurrentProfile, id FullChangeID, actor Identity, cut SemanticCut, command S1Command, intent [32]byte) (CurrentReview, error) {
	if o == nil || o.origin == nil || profile != o.Profile() || !id.valid() || id.Domain != profile.Domain || id.Cohort != profile.Cohort {
		return CurrentReview{}, ErrS1Contract
	}
	op, err := NewS1Operation(actor, cut, command, o.origin.network.kernel.trust.genesis.state.configuration.Policy)
	if err != nil || intent == [32]byte{} || op.digest != intent {
		return CurrentReview{}, ErrChangeConflict
	}
	return CurrentReview{profile, id, op}, nil
}

func (o *CurrentAuthority) currentAdmission(ctx context.Context, a *Admission, management bool) error {
	if a == nil || a.current == nil || a.current.owner != o {
		return ErrAuthorityUnavailable
	}
	if err := a.Check(ctx, time.Time{}); err != nil {
		return err
	}
	if management && (!a.Access().AllowsGlobal(SecurityManage) || a.current.credential.browserReadOnly || a.Authentication().Class != EndUser) {
		return ErrPermissionDenied
	}
	return nil
}

// Prepare binds one server-minted namespace/nonce to the exact reviewed cut,
// actor and canonical command. It makes no durability or Apply claim.
func (o *CurrentAuthority) Prepare(ctx context.Context, a *Admission, producer CurrentCredentialProducer, profile CurrentProfile, cut SemanticCut, command S1Command) (CurrentReview, bool, error) {
	if o == nil || profile != o.Profile() || o.currentAdmission(ctx, a, command.Kind == S1Management) != nil || a.BrowserReadOnly() || a.Authentication().Class != EndUser {
		return CurrentReview{}, false, ErrPermissionDenied
	}
	captured, _ := a.CurrentCut()
	if captured != cut {
		return CurrentReview{}, false, ErrRevisionConflict
	}
	r, err := o.origin.prepare(ctx, producer, cut, command)
	if err != nil {
		return CurrentReview{}, false, err
	}
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if r.operation.actor != a.Identity() || !a.current.credential.matches(k.replayState.projection) {
		o.origin.discardLocked(r)
		return CurrentReview{}, false, ErrPermissionDenied
	}
	assessment, err := AssessS1(k.replayState.projection, r.operation)
	if err != nil {
		o.origin.discardLocked(r)
		return CurrentReview{}, false, err
	}
	return CurrentReview{o.Profile(), r.id, r.operation}, assessment.NeedsPurpose, nil
}

func (o *CurrentAuthority) prepared(review CurrentReview) (*authorityOriginRequest, error) {
	if o == nil || o.origin == nil || review.Profile != o.Profile() || !review.ID.valid() {
		return nil, ErrS1Contract
	}
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	if k.readyLocked() != nil || o.origin.closed {
		return nil, ErrAuthorityUnavailable
	}
	for r := range o.origin.requests {
		if r.id == review.ID {
			if r.operation != review.Operation {
				return nil, ErrChangeConflict
			}
			return r, nil
		}
	}
	return nil, errS2CUnknown
}

func (o *CurrentAuthority) ReviewRequirement(ctx context.Context, a *Admission, review CurrentReview) (bool, error) {
	if err := o.currentAdmission(ctx, a, true); err != nil {
		return false, err
	}
	r, err := o.prepared(review)
	if err != nil {
		return false, err
	}
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if r.operation.actor != a.Identity() || !a.current.credential.matches(k.replayState.projection) {
		return false, ErrPermissionDenied
	}
	assessment, err := AssessS1(k.replayState.projection, r.operation)
	return assessment.NeedsPurpose, err
}

func (o *CurrentAuthority) resultLocked(id FullChangeID, intent [32]byte) (CurrentChangeResult, error) {
	r := CurrentChangeResult{Profile: o.Profile(), ID: id, Intent: intent, Progress: CurrentUnresolved}
	k := o.origin.network.kernel
	if k.readyLocked() != nil {
		return r, ErrAuthorityUnavailable
	}
	if outcome := k.replayState.ledger[id]; outcome != nil {
		if outcome.operation.digest != intent {
			return r, ErrChangeConflict
		}
		r.Progress, r.Original = CurrentApplied, outcome
		return r, nil
	}
	if k.chosen != nil && k.chosen.h != nil && k.chosen.h.handoff.id == id {
		if k.chosen.h.handoff.operation.digest != intent {
			return r, ErrChangeConflict
		}
		r.Progress = CurrentChosen
		return r, nil
	}
	for _, h := range k.origins {
		if h.handoff.id == id {
			if h.handoff.operation.digest != intent {
				return r, ErrChangeConflict
			}
			r.Progress = CurrentOriginDurable
			return r, nil
		}
	}
	if serial := k.originIDs[id]; serial != 0 && k.originReservations[serial].Operation != intent {
		return r, ErrChangeConflict
	}
	return r, nil // Includes a bare reservation; absence is never safe nonexecution.
}

// Inspect returns only locally held proof. It does not authorize disclosure;
// the service must attach a fresh current admission to each output unit.
func (o *CurrentAuthority) Inspect(ctx context.Context, profile CurrentProfile, id FullChangeID, intent [32]byte) (CurrentChangeResult, error) {
	if o == nil || o.origin == nil || profile != o.Profile() || !id.valid() || id.Domain != profile.Domain || id.Cohort != profile.Cohort || intent == [32]byte{} || !o.origin.network.enterCall() {
		return CurrentChangeResult{}, ErrS1Contract
	}
	defer o.origin.network.calls.Done()
	if err := ctx.Err(); err != nil {
		return CurrentChangeResult{}, err
	}
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	return o.resultLocked(id, intent)
}

// Apply first resolves retained H/outcome. It never restamps an unknown or
// reserved ID, and a purpose error concerns only this invocation. Once H is
// durable, the existing driver can complete it after credential expiry.
func (o *CurrentAuthority) Apply(ctx context.Context, review CurrentReview, producer CurrentCredentialProducer, proof [32]byte) (CurrentChangeResult, error) {
	result, err := o.Inspect(ctx, review.Profile, review.ID, review.Operation.digest)
	if err != nil || result.Progress == CurrentApplied {
		return result, err
	}
	if o == nil || !o.origin.network.enterCall() {
		return result, ErrAuthorityUnavailable
	}
	defer o.origin.network.calls.Done()
	retained, lookupErr := o.origin.lookupOriginal(ctx, review.ID, review.Operation)
	if lookupErr != nil && !errors.Is(lookupErr, errS2CUnknown) {
		return result, lookupErr
	}
	if lookupErr != nil {
		r, preparedErr := o.prepared(review)
		if preparedErr == nil {
			retained, err = o.origin.consume(ctx, r, producer, proof)
		} else if errors.Is(preparedErr, errS2CUnknown) {
			var h *s2cHistoricalH
			h, err = o.origin.network.findOriginal(ctx, review.ID, review.Operation.digest)
			if err == nil && h.handoff.operation != review.Operation {
				err = ErrChangeConflict
			}
			if err == nil {
				retained, err = o.origin.carryOriginal(ctx, []byte(h.raw))
			}
		} else {
			err = preparedErr
		}
		if err != nil {
			return result, err
		}
	}
	if retained.outcome != nil {
		return o.Inspect(ctx, review.Profile, review.ID, review.Operation.digest)
	}
	result.Progress = CurrentOriginDurable
	// A finite scheduling attempt may choose a competing value first. Keep the
	// original candidate until its own result is installed or the budget ends.
	driveCtx, cancel := context.WithTimeout(ctx, o.origin.network.limits.ScheduleWindow)
	defer cancel()
	for driveCtx.Err() == nil {
		if _, err = o.origin.network.Drive(driveCtx, retained.digest); err != nil {
			break
		}
		result, err = o.Inspect(driveCtx, review.Profile, review.ID, review.Operation.digest)
		if err != nil || result.Progress == CurrentApplied {
			return result, err
		}
	}
	// Preserve the strongest actual local proof even if the request context
	// expired while waiting; this read makes no new admission or mutation.
	latest, inspectErr := o.Inspect(context.WithoutCancel(ctx), review.Profile, review.ID, review.Operation.digest)
	if inspectErr == nil {
		result = latest
	}
	return result, errors.Join(err, driveCtx.Err(), inspectErr)
}

func (o *CurrentAuthority) BeginPurpose(ctx context.Context, review CurrentReview, producer CurrentCredentialProducer) (AuthorizationStart, error) {
	result, err := o.Inspect(ctx, review.Profile, review.ID, review.Operation.digest)
	if err != nil || result.Progress != CurrentUnresolved {
		return AuthorizationStart{}, ErrOperationAuthorization
	}
	r, err := o.prepared(review)
	if err != nil {
		return AuthorizationStart{}, err
	}
	return o.origin.beginPurpose(ctx, r, producer)
}

// OriginalObservation is process-owned and cannot be restored from timestamps.
// It starts only after authentic APPLIED; loss starts a new conservative wait.
type OriginalObservation struct{ wait *authorityAppliedWait }

func (o *CurrentAuthority) ObserveApplied(ctx context.Context, review CurrentReview) (*OriginalObservation, error) {
	if o == nil || o.origin == nil || review.Profile != o.Profile() {
		return nil, ErrS1Contract
	}
	w, err := o.origin.observeApplied(ctx, review.ID, review.Operation)
	if err != nil {
		return nil, err
	}
	return &OriginalObservation{w}, nil
}

func (w *OriginalObservation) Complete(ctx context.Context) (bool, error) {
	if w == nil {
		return false, ErrAuthorityUnavailable
	}
	return w.wait.complete(ctx)
}
