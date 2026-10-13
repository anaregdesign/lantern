package authn

import (
	"context"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func (w *Workflow) establishCurrentSession(ctx context.Context, completion oidc.LoginCompletion, producer security.CurrentCredentialProducer) (CallbackResult, error) {
	ctx, a, err := w.current.WithRequestCredential(ctx, producer)
	if err != nil {
		return CallbackResult{}, ErrUnauthenticated
	}
	value, err := newSessionSecret()
	if err != nil {
		return CallbackResult{}, ErrUnavailable
	}
	csrf, err := newSessionSecret()
	if err != nil {
		return CallbackResult{}, ErrUnavailable
	}
	review, session, err := w.current.PrepareSession(ctx, a, producer, sessionDigest(value), sessionDigest(csrf), completion.ReplacesDigest())
	if err != nil {
		return CallbackResult{}, ErrUnauthenticated
	}
	result, err := w.current.Apply(ctx, review, producer, [32]byte{})
	if err != nil || result.Original == nil || result.Original.Disposition() != security.S1Applied {
		return CallbackResult{}, ErrUnavailable
	}
	ctx, a, err = w.current.RefreshRequest(ctx)
	if err != nil || w.current.BindIssuedSessionOutput(ctx, a, session) != nil {
		return CallbackResult{}, ErrUnauthenticated
	}
	// Publish material only after Apply and a new current view. The old admission
	// cannot authorize output for the newly installed session.
	return CallbackResult{Session: value, CSRF: csrf, ExpiresAt: session.ExpiresAt, ReturnPath: completion.ReturnPath(), Context: ctx}, nil
}
func (w *Workflow) PrepareLogout(ctx context.Context, a *security.Admission) (security.CurrentReview, error) {
	producer, err := w.current.RequestCredential(ctx)
	if err != nil {
		return security.CurrentReview{}, ErrUnauthenticated
	}
	review, err := w.current.PrepareSessionRevocation(ctx, a, producer)
	if err != nil {
		return security.CurrentReview{}, ErrUnavailable
	}
	return review, nil
}
func (w *Workflow) ApplyLogout(ctx context.Context, a *security.Admission, review security.CurrentReview) (*security.CurrentChangeResult, error) {
	producer, err := w.current.RequestCredential(ctx)
	if err != nil {
		return nil, ErrUnauthenticated
	}
	if review.Operation.Actor() != a.Identity() {
		return nil, ErrInvalidInput
	}
	result, _ := w.current.Apply(ctx, review, producer, [32]byte{})
	// Local deletion is distinct from cluster outcome. The retained review is
	// already client-owned; a timeout is not rollback and must not cause reissue.
	if w.current.BindPublicMetadata(ctx, security.CurrentLocalCookieClear) != nil {
		return nil, ErrUnavailable
	}
	if freshCtx, fresh, e := w.current.RefreshRequest(ctx); e == nil && result.Profile == w.current.Profile() && w.current.BindOriginalOutput(freshCtx, fresh, review.ID, review.Operation.Digest()) == nil {
		observed := w.current.ObserveResult(freshCtx, result)
		return &observed, nil
	}
	return nil, nil
}
