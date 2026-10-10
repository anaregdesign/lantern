package security

import (
	"context"
	"slices"
)

// PublicLoginIssuers preserves the public sign-in choices (URLs only). Unlike
// the non-secret unavailable capability schema, this installed configuration
// list requires its exact cut and native renewal again at every output unit.
// No management issuer fields, credentials, subject or ledger are exposed.
func (o *CurrentAuthority) PublicLoginIssuers(ctx context.Context) ([]string, error) {
	if o == nil || o.origin == nil || !o.origin.network.enterCall() {
		return nil, ErrAuthorityUnavailable
	}
	defer o.origin.network.calls.Done()
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	if k.readyLocked() != nil || k.chosen != nil {
		return nil, ErrAuthorityUnavailable
	}
	var issuers []string
	for url, issuer := range k.replayState.projection.snapshot.issuers {
		if issuer.Enabled && !issuer.Deleted {
			issuers = append(issuers, url)
		}
	}
	slices.Sort(issuers)
	cut := k.replayState.projection.cut
	err := bindCurrentGrant(ctx, o, currentOutputGrant{kind: "login-catalog", cut: cut, resource: s2cHash("public-login-choices", struct {
		Cut     SemanticCut
		Issuers []string
	}{cut, issuers})})
	return issuers, err
}

// LoginIssuer is a local configuration read for a named login route. The
// detached view is not an admission; the eventual redirect/cookie output must
// still use the owner's finite current output boundary.
func (o *CurrentAuthority) LoginIssuer(ctx context.Context, url string) (Issuer, SemanticCut, error) {
	if o == nil || o.origin == nil || !o.origin.network.enterCall() {
		return Issuer{}, SemanticCut{}, ErrAuthorityUnavailable
	}
	defer o.origin.network.calls.Done()
	k := o.origin.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if k.readyLocked() != nil || k.chosen != nil {
		return Issuer{}, SemanticCut{}, ErrAuthorityUnavailable
	}
	if _, _, err := o.origin.eligibilityLocked(ctx); err != nil {
		return Issuer{}, SemanticCut{}, err
	}
	if _, _, err := o.origin.network.receiver.currentLocked(); err != nil {
		return Issuer{}, SemanticCut{}, ErrAuthorityUnavailable
	}
	i, known := k.replayState.projection.snapshot.Issuer(url)
	if !known || !i.Enabled || i.Deleted {
		return Issuer{}, SemanticCut{}, ErrPermissionDenied
	}
	return i, k.replayState.projection.cut, nil
}

// PrepareSession fixes the original creation/expiry and lineage once. A later
// retry can only recover that exact operation; it cannot extend the session.
func (o *CurrentAuthority) PrepareSession(ctx context.Context, a *Admission, producer CurrentCredentialProducer, digest, csrf, replaces string) (CurrentReview, Session, error) {
	if err := o.currentAdmission(ctx, a, false); err != nil {
		return CurrentReview{}, Session{}, err
	}
	e := a.current.credential.claim.Token
	if e == nil || e.Mode != "code" || e.Code.Flow != "login" || !o.OriginEnabled() {
		return CurrentReview{}, Session{}, ErrPermissionDenied
	}
	low, high, err := o.TimeBounds()
	if err != nil {
		return CurrentReview{}, Session{}, err
	}
	expires := low.Add(MaxSessionLifetime)
	if low.Before(e.Code.ConsumedAt) || !high.Before(expires) {
		return CurrentReview{}, Session{}, ErrPermissionDenied
	}
	s := Session{CSRFDigest: csrf, IssuerConfigRevision: e.ConfigRevision, Digest: digest, Identity: a.Identity(), CreatedAt: low, ExpiresAt: expires, AuthTime: e.AuthTime.Time()}
	command := S1Command{Kind: S1IssueSession, Session: &s, SessionLineage: a.current.credential.lineage, ReplacesDigest: replaces}
	review, _, err := o.Prepare(ctx, a, producer, o.Profile(), a.current.credential.cut, command)
	return review, s, err
}

func (o *CurrentAuthority) ConfirmIssuedSession(ctx context.Context, a *Admission, s Session) error {
	if err := o.currentAdmission(ctx, a, false); err != nil {
		return err
	}
	e := a.current.credential.claim.Token
	installed, known := a.Snapshot().Session(s.Digest)
	if e == nil || e.Mode != "code" || e.Code.Flow != "login" || !known || installed != s || s.Revoked || s.Identity != a.Identity() || s.IssuerConfigRevision != e.ConfigRevision {
		return ErrPermissionDenied
	}
	return nil
}

func (o *CurrentAuthority) PrepareSessionRevocation(ctx context.Context, a *Admission, producer CurrentCredentialProducer) (CurrentReview, error) {
	if err := o.currentAdmission(ctx, a, false); err != nil {
		return CurrentReview{}, err
	}
	s := a.current.credential.claim.Session
	if s == nil || a.BrowserReadOnly() {
		return CurrentReview{}, ErrPermissionDenied
	}
	command := S1Command{Kind: S1RevokeSession, SessionDigest: s.Digest, SessionLineage: a.current.credential.lineage}
	review, _, err := o.Prepare(ctx, a, producer, o.Profile(), a.current.credential.cut, command)
	return review, err
}
