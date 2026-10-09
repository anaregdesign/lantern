package security

import "context"

// These operations register with the same lifetime owner as origin consume.
// The pool is never exposed to a request decoder or a reusable legacy Verify.
func (o *authorityOriginOwner) beginPurpose(ctx context.Context, r *authorityOriginRequest, producer CurrentCredentialProducer) (AuthorizationStart, error) {
	if o == nil || !o.network.enterCall() {
		return AuthorizationStart{}, errS3AClosed
	}
	defer o.network.calls.Done()
	c, err := o.authenticate(ctx, producer)
	if err != nil {
		return AuthorizationStart{}, err
	}
	k := o.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if r == nil || r.owner != o || o.requests[r] == 0 || !c.matches(k.replayState.projection) || c.actor != r.operation.actor || c.cut != r.operation.reviewed {
		return AuthorizationStart{}, ErrOperationAuthorization
	}
	if _, known, err := o.knownLocked(r); known || err != nil {
		return AuthorizationStart{}, ErrOperationAuthorization
	}
	a, err := AssessS1(k.replayState.projection, r.operation)
	if err != nil || !a.NeedsPurpose {
		return AuthorizationStart{}, ErrOperationAuthorization
	}
	if _, _, err = o.eligibilityLocked(ctx); err != nil {
		return AuthorizationStart{}, err
	}
	b := authorityPurposeBinding{r.id, r.operation.digest, r.operation.reviewed, c.actor, c.authentication.IssuerConfigRevision, c.lineage}
	return o.purposes.beginCurrentLocked(o.network.receiver, b, c.claim.AdmissionDeadline)
}

func (o *authorityOriginOwner) startPurpose(ctx context.Context, ticket [32]byte) ([32]byte, authorityPurposeBinding, error) {
	if o == nil || !o.network.enterCall() {
		return [32]byte{}, authorityPurposeBinding{}, errS3AClosed
	}
	defer o.network.calls.Done()
	k := o.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if _, _, err := o.eligibilityLocked(ctx); err != nil {
		return [32]byte{}, authorityPurposeBinding{}, err
	}
	return o.purposes.startCurrentLocked(o.network.receiver, ticket)
}

func (o *authorityOriginOwner) completePurpose(ctx context.Context, id [32]byte, producer CurrentCredentialProducer) (AuthorizationStatus, error) {
	if o == nil || !o.network.enterCall() {
		return AuthorizationStatus{}, errS3AClosed
	}
	defer o.network.calls.Done()
	view, facts, err := o.verifyFacts(ctx, producer)
	if err != nil {
		return AuthorizationStatus{}, err
	}
	if facts.Token == nil || facts.Session != nil || facts.Origin != [32]byte{} || facts.CSRF != [32]byte{} {
		return AuthorizationStatus{}, ErrOperationAuthorization
	}
	event := *facts.Token
	i, known := view.Snapshot().Issuer(event.Identity.Issuer)
	if !known || !i.Enabled || i.Deleted || event.Configuration != authorityIssuerCommitment(i, view.Cut().Generation) || event.ConfigRevision != i.ConfigRevision || event.Generation != view.Cut().Generation {
		return AuthorizationStatus{}, ErrOperationAuthorization
	}
	k := o.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if view.Cut() != k.replayState.projection.cut {
		return AuthorizationStatus{}, ErrRevisionConflict
	}
	if _, _, err = o.eligibilityLocked(ctx); err != nil {
		return AuthorizationStatus{}, err
	}
	return o.purposes.completeCurrentLocked(o.network.receiver, id, event)
}
