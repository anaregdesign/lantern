package service

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func (r *AuthenticationAdapter) BeginCurrentManagementAuthorization(ctx context.Context, a *security.Admission, review security.CurrentReview) (security.AuthorizationStart, string, string, error) {
	start, err := r.workflow.BeginCurrentAuthorization(ctx, a, review)
	if err != nil {
		return security.AuthorizationStart{}, "", "", err
	}
	affinity := r.workflow.AttemptAffinity(start.ID)
	return start, r.config.BrowserOrigin + "/auth/management-authorization/" + base64.RawURLEncoding.EncodeToString(start.Ticket[:]) + "?affinity=" + affinity, affinity, nil
}
func (r *AuthenticationAdapter) ReadCurrentManagementAuthorization(ctx context.Context, a *security.Admission, id [32]byte, affinity string) (security.AuthorizationStatus, error) {
	return r.workflow.ReadCurrentAuthorization(ctx, a, id, affinity)
}
func (r *AuthenticationAdapter) browserManagementAuthorization(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet || !r.browserWriter(req) || r.current == nil && req.URL.RawQuery != "" {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	affinity := ""
	if r.current != nil {
		q, err := browserQuery(req, "affinity")
		if err != nil || !strings.HasPrefix(q.Get("affinity"), r.attemptPrefix+".") {
			browserHTTPError(w, connect.CodeUnauthenticated)
			return
		}
		affinity = q.Get("affinity")
	}
	text := strings.TrimPrefix(req.URL.Path, "/auth/management-authorization/")
	raw, err := base64.RawURLEncoding.Strict().DecodeString(text)
	if err != nil || len(text) != 43 || len(raw) != 32 {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	start, err := r.workflow.StartAuthorization(req.Context(), [32]byte(raw), affinity)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		browserHTTPError(w, workflowCode(err))
		return
	}
	browserSetCookie(w, transactionCookieName, start.TransactionCookie, start.ExpiresAt, r.now(), http.SameSiteLaxMode)
	http.Redirect(w, req, start.AuthorizationURL, http.StatusFound)
}
