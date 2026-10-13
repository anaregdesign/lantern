package authn

import (
	"context"
	"crypto/rand"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func (w *Workflow) establishLegacySession(ctx context.Context, completion oidc.LoginCompletion, verified oidc.VerifiedIdentity) (CallbackResult, error) {
	value, err := newSessionSecret()
	if err != nil {
		return CallbackResult{}, ErrUnavailable
	}
	csrf, err := newSessionSecret()
	if err != nil {
		return CallbackResult{}, ErrUnavailable
	}
	var changeID [16]byte
	if _, err = rand.Read(changeID[:]); err != nil {
		return CallbackResult{}, ErrUnavailable
	}
	now := w.oidc.Now()
	_, err = w.legacy.Store.IssueSession(ctx, security.SessionRequest{ChangeID: changeID, Identity: verified.Identity(), IssuerConfigRevision: completion.Trust().ConfigRevision, Digest: sessionDigest(value), CSRFDigest: sessionDigest(csrf), ReplacesDigest: completion.ReplacesDigest(), RequireRecentAuth: completion.RequiresRecentAuthentication(), AuthTime: verified.AuthTime(), Now: now, Lifetime: security.MaxSessionLifetime})
	if err != nil {
		return CallbackResult{}, ErrUnauthenticated
	}
	return CallbackResult{Session: value, CSRF: csrf, CreatedAt: now, ExpiresAt: now.Add(security.MaxSessionLifetime), ReturnPath: completion.ReturnPath(), Context: ctx}, nil
}
func (w *Workflow) Logout(ctx context.Context, a *security.Admission) (security.ChangeResult, bool, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return security.ChangeResult{}, false, ErrUnavailable
	}
	result, err := w.legacy.Store.RevokeSession(ctx, a.Identity(), a.Authentication().SessionDigest, id, w.oidc.Now())
	if err != nil {
		return security.ChangeResult{}, false, ErrUnavailable
	}
	return result, w.legacy.Enforced(result), nil
}
