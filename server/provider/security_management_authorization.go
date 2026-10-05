package provider

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func (r *SecurityRuntime) beginManagementAuthorization(ctx context.Context, admission *security.Admission, binding security.ManagementBinding) (security.AuthorizationStart, string, error) {
	current, known := r.native.Store().Current()
	if !known || admission.CheckManagement(ctx, current, r.now()) != nil || binding.Actor != admission.Identity() {
		return security.AuthorizationStart{}, "", security.ErrPermissionDenied
	}
	start, err := r.authorizations.Begin(binding, r.now())
	if err != nil {
		return security.AuthorizationStart{}, "", err
	}
	return start, r.config.BrowserOrigin + "/auth/management-authorization/" + base64.RawURLEncoding.EncodeToString(start.Ticket[:]), nil
}
func (r *SecurityRuntime) readManagementAuthorization(ctx context.Context, admission *security.Admission, id [32]byte) (security.AuthorizationStatus, error) {
	current, known := r.native.Store().Current()
	if !known || admission.CheckManagement(ctx, current, r.now()) != nil {
		return security.AuthorizationStatus{}, security.ErrPermissionDenied
	}
	return r.authorizations.Status(id, admission.Identity(), current, r.now())
}

// The navigation ticket is created only after authenticated, CSRF-checked
// final review. It permits starting the exact actor-bound Code challenge, not
// approval or session access. This also supports qualified human Bearer users.
func (r *SecurityRuntime) browserManagementAuthorization(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet || req.URL.RawQuery != "" || !r.browserWriter(req) {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	raw := strings.TrimPrefix(req.URL.Path, "/auth/management-authorization/")
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(raw) != 43 || len(decoded) != 32 {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	var ticket [32]byte
	copy(ticket[:], decoded)
	current, known := r.native.Store().Current()
	if !known {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	notBefore, err := r.authorizations.NavigationNotBefore(ticket, current, r.now())
	if err != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	if delay := notBefore.Sub(r.now()); delay > 0 {
		// At most the remainder of this NumericDate second. Cancellation leaves
		// the ticket unconsumed, and no ordinary session/cookie is changed.
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-req.Context().Done():
			return
		case <-timer.C:
		}
		current, known = r.native.Store().Current()
		if !known {
			browserHTTPError(w, connect.CodeUnavailable)
			return
		}
	}
	id, binding, err := r.authorizations.Start(ticket, current, r.now())
	if err != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	issuer, registered := current.Snapshot().Issuer(binding.Actor.Issuer)
	if !registered || issuer.ConfigRevision != binding.IssuerConfigRevision {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	discovery, err := r.fetcher.Discover(req.Context(), issuer)
	if err != nil || r.authorityCheck(req.Context(), current) != nil {
		r.authorizations.Reject(id)
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	start, err := r.logins.BeginAuthorization(oidc.Trust{Issuer: issuer, Generation: current.Generation(), ConfigRevision: issuer.ConfigRevision}, discovery, id)
	if err != nil {
		r.authorizations.Reject(id)
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	browserSetCookie(w, transactionCookieName, start.TransactionCookie, start.ExpiresAt, r.now(), http.SameSiteLaxMode)
	http.Redirect(w, req, start.AuthorizationURL, http.StatusFound)
}
