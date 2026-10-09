package security

import (
	"context"
	"crypto/sha256"
	"time"
)

// PurposeNavigation exposes only the exact issuer and attempt identity after
// consuming the process-owned navigation ticket; it grants no session access.
type PurposeNavigation struct {
	ID         [32]byte
	Issuer     Issuer
	Generation [16]byte
	Cut        SemanticCut
}

func (o *CurrentAuthority) PurposeNotBefore(ctx context.Context, ticket [32]byte) (time.Time, error) {
	if o == nil || o.origin == nil || !o.origin.network.enterCall() {
		return time.Time{}, ErrAuthorityUnavailable
	}
	defer o.origin.network.calls.Done()
	k, m := o.origin.network.kernel, o.origin.purposes
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if _, _, err := o.origin.eligibilityLocked(ctx); err != nil {
		return time.Time{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id, known := m.tickets[sha256.Sum256(ticket[:])]
	p, exists := m.pending[id]
	if m.closed || !known || !exists || p.current == nil || p.state != AuthorizationPending || p.current.started || !authorityPurposeBindingLive(p.current.binding, k.replayState.projection) {
		return time.Time{}, ErrOperationAuthorization
	}
	now, _, err := o.origin.network.receiver.currentLocked()
	if err != nil || !time.Unix(0, int64(now.utc.high)).Before(p.expiresAt) {
		return time.Time{}, ErrOperationAuthorization
	}
	return p.notBefore, nil
}

func (o *CurrentAuthority) StartPurpose(ctx context.Context, ticket [32]byte) (PurposeNavigation, error) {
	if o == nil || o.origin == nil || !o.origin.network.enterCall() {
		return PurposeNavigation{}, ErrAuthorityUnavailable
	}
	defer o.origin.network.calls.Done()
	id, binding, err := o.origin.startPurpose(ctx, ticket)
	if err != nil {
		return PurposeNavigation{}, err
	}
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	if k.readyLocked() != nil || !authorityPurposeBindingLive(binding, k.replayState.projection) {
		return PurposeNavigation{}, ErrOperationAuthorization
	}
	i, ok := k.replayState.projection.snapshot.Issuer(binding.Actor.Issuer)
	if !ok {
		return PurposeNavigation{}, ErrOperationAuthorization
	}
	return PurposeNavigation{ID: id, Issuer: i, Generation: binding.Reviewed.Generation, Cut: binding.Reviewed}, nil
}

func (o *CurrentAuthority) CompletePurpose(ctx context.Context, id [32]byte, code CurrentCredentialProducer) (AuthorizationStatus, error) {
	if o == nil || o.origin == nil {
		return AuthorizationStatus{}, ErrAuthorityUnavailable
	}
	return o.origin.completePurpose(ctx, id, code)
}

func (o *CurrentAuthority) RejectPurpose(id [32]byte) {
	if o != nil && o.origin != nil {
		o.origin.purposes.Reject(id)
	}
}

// ReadPurpose requires the same actor, current complete binding, and live native
// renewal. The one-use consumed state returns no proof and cannot be replayed.
func (o *CurrentAuthority) ReadPurpose(ctx context.Context, a *Admission, id [32]byte) (AuthorizationStatus, error) {
	if err := o.currentAdmission(ctx, a, true); err != nil {
		return AuthorizationStatus{}, err
	}
	if !o.origin.network.enterCall() {
		return AuthorizationStatus{}, ErrAuthorityUnavailable
	}
	defer o.origin.network.calls.Done()
	k, m := o.origin.network.kernel, o.origin.purposes
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if _, err := o.origin.authorizeCurrentCredentialLocked(ctx, a.current.credential); err != nil {
		return AuthorizationStatus{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, known := m.pending[id]
	if m.closed || !known || p.current == nil || p.current.binding.Actor != a.Identity() || !authorityPurposeBindingLive(p.current.binding, k.replayState.projection) {
		return AuthorizationStatus{}, ErrOperationAuthorization
	}
	now, _, err := o.origin.network.receiver.currentLocked()
	if err != nil || !time.Unix(0, int64(now.utc.high)).Before(p.expiresAt) {
		return AuthorizationStatus{}, ErrOperationAuthorization
	}
	state, proof := p.state, p.proof
	if state == authorizationConsumed {
		state, proof = AuthorizationDenied, [32]byte{}
	} else if state == AuthorizationApproved && time.Unix(0, int64(now.utc.low)).Before(p.approvedAt) {
		// Approval captures its upper endpoint. Publish the usable proof only
		// after the native lower endpoint reaches it; an immediate UI Apply
		// must not burn its one dispatch during this conservative interval.
		state, proof = AuthorizationPending, [32]byte{}
	}
	return AuthorizationStatus{ID: id, State: state, Proof: proof, ExpiresAt: p.expiresAt}, nil
}
