package security

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

// CurrentRegistration is an opaque, process-owned restricted verification. It
// is not an Admission, cannot select an actor/UID, and contains no raw provider
// or session credential. authn owns the bounded volatile external proof handle.
type CurrentRegistration struct {
	owner        *CurrentAuthority
	review       CurrentReview
	verification [32]byte
	purpose      NativeRegistrationPurpose
	expires      time.Time
}

func (CurrentRegistration) String() string   { return "[redacted current registration]" }
func (CurrentRegistration) GoString() string { return "[redacted current registration]" }
func (p *CurrentRegistration) Review() CurrentReview {
	if p == nil {
		return CurrentReview{}
	}
	return p.review
}
func (p *CurrentRegistration) ExpiresAt() time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.expires
}
func (p *CurrentRegistration) RecoveryOnly() bool {
	return p != nil && p.purpose.Kind == "registration-recovery"
}

func (o *CurrentAuthority) registrationCredential(ctx context.Context, producer CurrentCredentialProducer) (authorityRegistrationCredential, error) {
	if o == nil || o.origin == nil || !o.origin.network.enterCall() {
		return authorityRegistrationCredential{}, ErrAuthorityUnavailable
	}
	defer o.origin.network.calls.Done()
	view, facts, err := o.origin.verifyFacts(ctx, producer)
	if err != nil {
		return authorityRegistrationCredential{}, err
	}
	c, err := captureRegistrationCredential(view, facts)
	if err != nil {
		return authorityRegistrationCredential{}, err
	}
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	if k.readyLocked() != nil || k.chosen != nil {
		return authorityRegistrationCredential{}, ErrAuthorityUnavailable
	}
	if _, err := o.origin.authorizeRegistrationLocked(ctx, c); err != nil {
		return authorityRegistrationCredential{}, err
	}
	return c, nil
}

func (o *CurrentAuthority) refreshRegistration(ctx context.Context, proof *CurrentRegistration, producer CurrentCredentialProducer) (authorityRegistrationCredential, error) {
	if o == nil || proof == nil || proof.owner != o || proof.review.Profile != o.Profile() || RegistrationReviewBinding(proof.review) == [32]byte{} {
		return authorityRegistrationCredential{}, ErrPermissionDenied
	}
	c, err := o.registrationCredential(ctx, producer)
	if err != nil {
		return authorityRegistrationCredential{}, err
	}
	if c.actor != proof.review.Operation.actor || c.claim.Verification != proof.verification || c.claim.Token.Native.Registration != proof.purpose || c.claim.AdmissionDeadline != proof.expires {
		return authorityRegistrationCredential{}, ErrPermissionDenied
	}
	return c, nil
}

func (o *CurrentAuthority) PrepareRegistration(ctx context.Context, producer CurrentCredentialProducer) (*CurrentRegistration, error) {
	c, err := o.registrationCredential(ctx, producer)
	if err != nil {
		return nil, err
	}
	if c.claim.Token.Native.Registration.Kind != "registration" || !o.OriginEnabled() {
		return nil, ErrPermissionDenied
	}
	uid := c.state.UserUID
	if uid == "" {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, err
		}
		if raw == [16]byte{} {
			return nil, ErrAuthorityUnavailable
		}
		uid = hex.EncodeToString(raw[:])
	}
	command := S1Command{Kind: S1RegisterPrincipal, Registration: &PrincipalRegistration{UserUID: uid, ClientProfileID: c.claim.Token.Native.ClientProfileID, IssuerConfigRevision: c.claim.Token.ConfigRevision}}
	r, err := o.origin.prepareUse(ctx, producer, c.cut, command, authorityOriginUse{kind: "registration"})
	if err != nil {
		return nil, err
	}
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if c.claim.Verification != r.credential.claim.Verification || !r.credential.matches(k.replayState) {
		o.origin.discardLocked(r)
		return nil, ErrPermissionDenied
	}
	if _, err := AssessS1(k.replayState.projection, r.operation); err != nil {
		o.origin.discardLocked(r)
		return nil, err
	}
	review := CurrentReview{o.Profile(), r.id, r.operation}
	return &CurrentRegistration{owner: o, review: review, verification: c.claim.Verification, purpose: c.claim.Token.Native.Registration, expires: c.claim.AdmissionDeadline}, nil
}

// RecoverRegistration only establishes disclosure for the original review.
// Even if no local outcome or origin reservation exists, it creates no request
// and grants no permission to restamp or replay that original operation.
func (o *CurrentAuthority) RecoverRegistration(ctx context.Context, producer CurrentCredentialProducer, review CurrentReview) (*CurrentRegistration, error) {
	binding := RegistrationReviewBinding(review)
	if o == nil || review.Profile != o.Profile() || binding == [32]byte{} {
		return nil, ErrPermissionDenied
	}
	c, err := o.registrationCredential(ctx, producer)
	if err != nil {
		return nil, err
	}
	wanted := NativeRegistrationPurpose{Kind: "registration-recovery", Original: binding}
	if c.actor != review.Operation.actor || c.claim.Token.Native.Registration != wanted {
		return nil, ErrPermissionDenied
	}
	return &CurrentRegistration{owner: o, review: review, verification: c.claim.Verification, purpose: wanted, expires: c.claim.AdmissionDeadline}, nil
}

func (o *CurrentAuthority) ApplyRegistration(ctx context.Context, proof *CurrentRegistration, producer CurrentCredentialProducer) (CurrentChangeResult, error) {
	if proof == nil || proof.RecoveryOnly() {
		return CurrentChangeResult{}, ErrPermissionDenied
	}
	if _, err := o.refreshRegistration(ctx, proof, producer); err != nil {
		return CurrentChangeResult{}, err
	}
	return o.Apply(ctx, proof.review, producer, [32]byte{})
}

// RefreshRegistration is the explicit post-Apply disclosure boundary. It waits
// for the same current owner to renew the installed cut, then revalidates the
// original proof. It neither creates an Admission nor drives/replays an original.
func (o *CurrentAuthority) RefreshRegistration(ctx context.Context, proof *CurrentRegistration, producer CurrentCredentialProducer) error {
	if o == nil || proof == nil || proof.owner != o || proof.review.Profile != o.Profile() || RegistrationReviewBinding(proof.review) == [32]byte{} {
		return ErrPermissionDenied
	}
	if err := o.waitCurrentRenewal(ctx); err != nil {
		return err
	}
	_, err := o.refreshRegistration(ctx, proof, producer)
	return err
}

func (o *CurrentAuthority) RegistrationStatus(ctx context.Context, proof *CurrentRegistration, producer CurrentCredentialProducer) (CurrentChangeResult, error) {
	if _, err := o.refreshRegistration(ctx, proof, producer); err != nil {
		return CurrentChangeResult{}, err
	}
	return o.Inspect(ctx, proof.review.Profile, proof.review.ID, proof.review.Operation.digest)
}

// DiscardRegistration releases only the prepared volatile request. It cannot
// cancel, retire or erase any reserved/dispatched original or durable outcome.
func (o *CurrentAuthority) DiscardRegistration(proof *CurrentRegistration) {
	if o == nil || proof == nil || proof.owner != o {
		return
	}
	if r, err := o.prepared(proof.review); err == nil {
		o.origin.discard(r)
	}
}

func (o *CurrentAuthority) DiscardRegistrationSession(proof *CurrentRegistration, review CurrentReview) {
	if o == nil || proof == nil || proof.owner != o {
		return
	}
	r, err := o.prepared(review)
	if err == nil && r.use.kind == "registered-session" && r.use.original != nil && r.use.original.ID == proof.review.ID && r.use.original.Intent == proof.review.Operation.digest {
		o.origin.discard(r)
	}
}

func (o *CurrentAuthority) PrepareRegistrationSession(ctx context.Context, proof *CurrentRegistration, producer CurrentCredentialProducer, digest string) (CurrentReview, Session, error) {
	if proof == nil || proof.RecoveryOnly() || !validHexDigest(digest) {
		return CurrentReview{}, Session{}, ErrPermissionDenied
	}
	c, err := o.refreshRegistration(ctx, proof, producer)
	if err != nil {
		return CurrentReview{}, Session{}, err
	}
	k := o.origin.network.kernel
	k.gate.Lock()
	original := k.replayState.ledger[proof.review.ID]
	if k.readyLocked() != nil || k.chosen != nil || !c.matches(k.replayState.projection) || original == nil || original.operation != proof.review.Operation || original.disposition != S1Applied {
		k.gate.Unlock()
		return CurrentReview{}, Session{}, ErrPermissionDenied
	}
	command, err := original.operation.command()
	if err != nil || command.Registration == nil {
		k.gate.Unlock()
		return CurrentReview{}, Session{}, ErrPermissionDenied
	}
	applied := authorityRegistrationOriginal{ID: original.id, Intent: original.operation.digest, Commit: original.commit, UserUID: command.Registration.UserUID}
	valid := registrationOriginalMatches(k.replayState, c.actor, &applied)
	k.gate.Unlock()
	if !valid {
		return CurrentReview{}, Session{}, ErrPermissionDenied
	}
	low, high, err := o.TimeBounds()
	if err != nil {
		return CurrentReview{}, Session{}, err
	}
	e := c.claim.Token
	expires := low.Add(MaxSessionLifetime)
	if low.Before(e.Native.ConsumedAt) || !high.Before(expires) {
		return CurrentReview{}, Session{}, ErrPermissionDenied
	}
	s := Session{Profile: NativeEndUserProfile, ClientProfileID: e.Native.ClientProfileID, ServiceOrigin: e.Native.ServiceOrigin, IssuerConfigRevision: e.ConfigRevision,
		Digest: digest, Identity: c.actor, CreatedAt: low, ExpiresAt: expires, AuthTime: e.AuthTime.Time()}
	command = S1Command{Kind: S1IssueSession, Session: &s, SessionLineage: c.lineage}
	r, err := o.origin.prepareUse(ctx, producer, c.cut, command, authorityOriginUse{kind: "registered-session", original: &applied})
	if err != nil {
		return CurrentReview{}, Session{}, err
	}
	if r.credential.claim.Verification != proof.verification {
		o.origin.discard(r)
		return CurrentReview{}, Session{}, ErrPermissionDenied
	}
	return CurrentReview{o.Profile(), r.id, r.operation}, s, nil
}
