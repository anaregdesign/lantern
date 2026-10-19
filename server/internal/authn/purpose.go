package authn

import (
	"context"
	"encoding/base64"
	"errors"
	"time"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func (w *Workflow) AttemptAffinity(id [32]byte) string {
	if w == nil || w.current == nil || w.attemptPrefix == "" || id == [32]byte{} {
		return ""
	}
	return w.attemptPrefix + "." + base64.RawURLEncoding.EncodeToString(id[:])
}
func (w *Workflow) BeginCurrentAuthorization(ctx context.Context, a *security.Admission, review security.CurrentReview) (security.AuthorizationStart, error) {
	p, err := w.current.RequestCredential(ctx)
	if err != nil {
		return security.AuthorizationStart{}, err
	}
	if a.CheckManagement(ctx, w.oidc.Now()) != nil {
		return security.AuthorizationStart{}, security.ErrPermissionDenied
	}
	return w.current.BeginPurpose(ctx, review, p)
}
func (w *Workflow) ReadCurrentAuthorization(ctx context.Context, a *security.Admission, id [32]byte, affinity string) (security.AuthorizationStatus, error) {
	if affinity == "" || affinity != w.AttemptAffinity(id) {
		return security.AuthorizationStatus{}, errors.New("operation attempt belongs to a different process")
	}
	return w.current.ReadPurpose(ctx, a, id)
}
func (w *Workflow) StartAuthorization(ctx context.Context, ticket [32]byte, affinity string) (oidc.LoginStart, error) {
	notBefore, err := w.current.PurposeNotBefore(ctx, ticket)
	if err != nil {
		return oidc.LoginStart{}, ErrUnauthenticated
	}
	// Timer merely paces navigation. startPurpose independently requires the
	// qualified lower endpoint; neither this timer nor host wall is evidence.
	for {
		low, _, err := w.current.TimeBounds()
		if err != nil {
			return oidc.LoginStart{}, ErrUnavailable
		}
		if !low.Before(notBefore) {
			break
		}
		timer := time.NewTimer(min(notBefore.Sub(low), 250*time.Millisecond))
		select {
		case <-ctx.Done():
			timer.Stop()
			return oidc.LoginStart{}, ctx.Err()
		case <-timer.C:
		}
	}
	navigation, err := w.current.StartPurpose(ctx, ticket)
	if err != nil || affinity != w.AttemptAffinity(navigation.ID) {
		return oidc.LoginStart{}, ErrUnauthenticated
	}
	d, err := w.oidc.Fetcher.Discover(ctx, navigation.Issuer)
	if err != nil || !w.current.Ready(ctx) {
		w.current.RejectPurpose(navigation.ID)
		return oidc.LoginStart{}, ErrUnavailable
	}
	start, err := w.oidc.Logins.BeginAuthorization(oidc.Trust{Issuer: navigation.Issuer, Generation: navigation.Generation, ConfigRevision: navigation.Issuer.ConfigRevision}, d, navigation.ID)
	if err != nil {
		w.current.RejectPurpose(navigation.ID)
		return oidc.LoginStart{}, ErrUnauthenticated
	}
	if w.current.BindLoginOutput(ctx, navigation.Cut, navigation.Issuer.URL) != nil {
		w.current.RejectPurpose(navigation.ID)
		return oidc.LoginStart{}, ErrUnavailable
	}
	return start, nil

}
