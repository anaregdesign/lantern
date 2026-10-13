package service

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/authn"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func (r *AuthenticationAdapter) AuthHTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		switch req.URL.Path {
		case "/auth/login":
			r.browserLogin(w, req)
		case "/auth/operations":
			r.browserOperations(w, req)
		case "/auth/session":
			r.browserSession(w, req)
		case "/auth/logout":
			r.browserLogout(w, req)
		default:
			if strings.HasPrefix(req.URL.Path, nativeAppleCallbackPrefix) {
				r.nativeAppleCallback(w, req)
			} else if strings.HasPrefix(req.URL.Path, "/auth/management-authorization/") {
				r.browserManagementAuthorization(w, req)
			} else if strings.HasPrefix(req.URL.Path, "/auth/callback/") {
				r.browserCallback(w, req)
			} else {
				http.NotFound(w, req)
			}
		}
	})
}

func browserQuery(req *http.Request, allowed ...string) (url.Values, error) {
	if len(req.URL.RawQuery) > 16<<10 {
		return nil, errBrowserBoundary
	}
	query, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		return nil, errBrowserBoundary
	}
	for key, values := range query {
		known := false
		for _, name := range allowed {
			known = known || name == key
		}
		if !known || len(values) != 1 || len(values[0]) > 4096 {
			return nil, errBrowserBoundary
		}
	}
	return query, nil
}

func (r *AuthenticationAdapter) writeBrowserProto(w http.ResponseWriter, ctx context.Context, message proto.Message) {
	var raw []byte
	var err error
	if r.current != nil {
		raw, err = r.output().MarshalBrowserJSON(ctx, message)
	} else {
		raw, err = protojson.Marshal(message)
	}
	if err != nil {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func (r *AuthenticationAdapter) browserOperations(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet || req.URL.RawQuery != "" {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	if r.mode == "off" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ctx, _, err := r.authenticateBrowser(req, false)
	if err != nil {
		browserHTTPError(w, connect.CodeOf(err))
		return
	}
	admission, known := security.AdmissionFromContext(ctx)
	if !known || !admission.Access().AllowsGlobal(security.OperationsRead) {
		browserHTTPError(w, connect.CodePermissionDenied)
		return
	}
	if admission.Check(ctx, r.now()) != nil {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	if admission.BindCurrentGlobalOutput(ctx, security.OperationsRead) != nil {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func workflowCode(err error) connect.Code {
	switch {
	case errors.Is(err, authn.ErrInvalidInput):
		return connect.CodeInvalidArgument
	case errors.Is(err, authn.ErrUnauthenticated):
		return connect.CodeUnauthenticated
	default:
		return connect.CodeUnavailable
	}
}
func (r *AuthenticationAdapter) browserWriter(req *http.Request) bool {
	if r != nil && r.current != nil {
		return r.current.OriginEnabled() && r.browserOrigin(req, false) == nil && r.ready(req.Context())
	}
	if r == nil || r.mode != "oidc" || r.writerEnabled == nil || !r.writerEnabled() || r.browserOrigin(req, false) != nil {
		return false
	}
	current, known := r.store.Current()
	return known && r.authorityCheck(req.Context(), current) == nil
}
func (r *AuthenticationAdapter) browserLogin(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet || !r.browserWriter(req) {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	allowed := []string{"issuer", "return"}
	if r.current == nil {
		allowed = append(allowed, "step_up")
	}
	q, err := browserQuery(req, allowed...)
	if err != nil || q.Get("issuer") == "" || q.Get("step_up") != "" && q.Get("step_up") != "true" {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	path := q.Get("return")
	if path == "" {
		path = "/"
	}
	var previous *security.Admission
	if ctx, _, e := r.authenticateBrowser(req, false); e == nil {
		previous, _ = security.AdmissionFromContext(ctx)
	}
	start, err := r.workflow.BeginLogin(req.Context(), authn.LoginRequest{Issuer: q.Get("issuer"), ReturnPath: path, StepUp: q.Get("step_up") == "true", Previous: previous})
	if err != nil {
		browserHTTPError(w, workflowCode(err))
		return
	}
	browserSetCookie(w, transactionCookieName, start.TransactionCookie, start.ExpiresAt, r.now(), http.SameSiteLaxMode)
	http.Redirect(w, req, start.AuthorizationURL, http.StatusFound)
}
func (r *AuthenticationAdapter) browserCallback(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet || !r.browserWriter(req) {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	q, err := browserQuery(req, "code", "state", "iss", "error", "error_description", "error_uri", "session_state", "scope", "authuser", "prompt", "hd")
	if err != nil {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	cookie, err := browserCookie(req, transactionCookieName)
	if err != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	result, err := r.workflow.CompleteCallback(req.Context(), authn.CallbackInput{State: q.Get("state"), Transaction: cookie, Path: req.URL.Path, Issuer: q.Get("iss"), Code: q.Get("code"), ProviderError: q.Get("error") != ""})
	if result.Consumed {
		browserClearCookie(w, transactionCookieName, http.SameSiteLaxMode)
	}
	if err != nil {
		browserHTTPError(w, workflowCode(err))
		return
	}
	if result.Purpose {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		if r.current != nil {
			_, _ = w.Write([]byte("<!doctype html><html lang=\"en\"><title>Operation authentication</title><body><p>Return to your reviewed change in Admin to check the result.</p></body></html>"))
		} else {
			_, _ = w.Write([]byte("<!doctype html><html lang=\"en\"><title>Operation authentication complete</title><body><p>Authentication recorded. Return to your reviewed change in Admin.</p></body></html>"))
		}
		return
	}
	if r.current != nil {
		browserSetCookie(w, sessionCookieName, result.Session, result.ExpiresAt, r.now(), http.SameSiteStrictMode)
		browserSetCookie(w, csrfCookieName, result.CSRF, result.ExpiresAt, r.now(), http.SameSiteStrictMode)
	} else {
		browserSetCookie(w, sessionCookieName, result.Session, result.ExpiresAt, result.CreatedAt, http.SameSiteStrictMode)
		browserSetCookie(w, csrfCookieName, result.CSRF, result.ExpiresAt, result.CreatedAt, http.SameSiteStrictMode)
	}
	http.Redirect(w, req.WithContext(result.Context), result.ReturnPath, http.StatusSeeOther)
}
func (r *AuthenticationAdapter) browserSession(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet || req.URL.RawQuery != "" {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	if r.mode == "off" {
		r.writeBrowserProto(w, req.Context(), &pb.BrowserSession{Mode: pb.AuthMode_AUTH_MODE_OFF})
		return
	}
	ctx, _, err := r.authenticateBrowser(req, false)
	if err != nil {
		browserHTTPError(w, connect.CodeOf(err))
		return
	}
	a, _ := security.AdmissionFromContext(ctx)
	view, err := authn.InspectSession(ctx, a, r.store, r.current, r.now)
	if err != nil {
		browserHTTPError(w, sessionErrorCode(err))
		return
	}
	principal := encodeSessionView(view)
	response := &pb.BrowserSession{Mode: pb.AuthMode_AUTH_MODE_OIDC, Principal: principal}
	if r.current != nil {
		response.CurrentProfile = principal.Version.CurrentProfile
	}
	r.writeBrowserProto(w, req.Context(), response)
}
func (r *AuthenticationAdapter) browserLogout(w http.ResponseWriter, req *http.Request) {
	if r.current != nil {
		r.currentBrowserLogout(w, req)
		return
	}
	if req.Method != http.MethodPost || req.URL.RawQuery != "" || !r.browserWriter(req) {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	ctx, _, err := r.authenticateBrowser(req, true)
	if err != nil {
		browserHTTPError(w, connect.CodeOf(err))
		return
	}
	a, _ := security.AdmissionFromContext(ctx)
	result, enforced, err := r.workflow.Logout(ctx, a)
	if err != nil {
		browserHTTPError(w, workflowCode(err))
		return
	}
	browserClearCookie(w, sessionCookieName, http.SameSiteStrictMode)
	browserClearCookie(w, csrfCookieName, http.SameSiteStrictMode)
	generation := a.Revision().Generation()
	response := &pb.SessionRevocation{Version: &pb.SecurityVersion{Revision: result.Revision, Digest: result.Digest[:], Generation: generation[:]}, Enforcement: pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_COMMITTED_PENDING}
	if enforced {
		response.Enforcement = pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_ENFORCED
	}
	r.writeBrowserProto(w, req.Context(), response)
}
