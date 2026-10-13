package authn

import (
	"context"
	"errors"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

var errSessionOutput = errors.New("login/session output unavailable")

func (w *Workflow) establishCurrentSession(ctx context.Context, completion oidc.LoginCompletion, producer security.CurrentCredentialProducer) (CallbackResult, error) {
	value, err := newSessionSecret()
	if err != nil {
		return CallbackResult{}, ErrUnavailable
	}
	csrf, err := newSessionSecret()
	if err != nil {
		return CallbackResult{}, ErrUnavailable
	}
	ctx, session, err := w.issueCurrentSession(ctx, producer, sessionDigest(value), sessionDigest(csrf), completion.ReplacesDigest())
	if errors.Is(err, errSessionOutput) {
		// Preserve the browser callback's existing fail-closed error contract.
		return CallbackResult{}, ErrUnauthenticated
	}
	if err != nil {
		return CallbackResult{}, err
	}
	return CallbackResult{Session: value, CSRF: csrf, CreatedAt: session.CreatedAt, ExpiresAt: session.ExpiresAt, ReturnPath: completion.ReturnPath(), Context: ctx}, nil
}

// issueCurrentSession is the shared durable transition and output sequence.
// Browser/native adapters supply only their distinct credential material.
func (w *Workflow) issueCurrentSession(ctx context.Context, producer security.CurrentCredentialProducer, digest, csrf, replaces string) (context.Context, security.Session, error) {
	ctx, a, err := w.current.WithRequestCredential(ctx, producer)
	if err != nil {
		return ctx, security.Session{}, ErrUnauthenticated
	}
	review, session, err := w.current.PrepareSession(ctx, a, producer, digest, csrf, replaces)
	if err != nil {
		return ctx, security.Session{}, ErrUnauthenticated
	}
	result, err := w.current.Apply(ctx, review, producer, [32]byte{})
	if err != nil || result.Original == nil || result.Original.Disposition() != security.S1Applied {
		return ctx, security.Session{}, ErrUnavailable
	}
	ctx, a, err = w.current.RefreshRequest(ctx)
	if err != nil || w.current.BindIssuedSessionOutput(ctx, a, session) != nil {
		return ctx, security.Session{}, errSessionOutput
	}
	// Publish material only after Apply and a new current view. The old admission
	// cannot authorize output for the newly installed session.
	return ctx, session, nil
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
	return w.applyLogout(ctx, a, review, security.CurrentLocalCookieClear)
}

func (w *Workflow) applyLogout(ctx context.Context, a *security.Admission, review security.CurrentReview, clear security.CurrentPublicMetadata) (*security.CurrentChangeResult, error) {
	producer, err := w.current.RequestCredential(ctx)
	if err != nil {
		return nil, ErrUnauthenticated
	}
	if a == nil || review.Operation.Actor() != a.Identity() {
		return nil, ErrInvalidInput
	}
	result, _ := w.current.Apply(ctx, review, producer, [32]byte{})
	// Local deletion is distinct from cluster outcome. The retained review is
	// already client-owned; a timeout is not rollback and must not cause reissue.
	if w.current.BindPublicMetadata(ctx, clear) != nil {
		return nil, ErrUnavailable
	}
	if freshCtx, fresh, e := w.current.RefreshRequest(ctx); e == nil && result.Profile == w.current.Profile() && w.current.BindOriginalOutput(freshCtx, fresh, review.ID, review.Operation.Digest()) == nil {
		observed := w.current.ObserveResult(freshCtx, result)
		return &observed, nil
	}
	return nil, nil
}
