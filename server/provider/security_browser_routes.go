package provider

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// AuthHTTPHandler owns login, callback, session bootstrap and explicit logout.
// Gateways pin login/callback/logout to the writer; session reads may use a
// currently leased replica. Unavailable authority is never an OFF response.
func (r *SecurityRuntime) AuthHTTPHandler() http.Handler {
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
			if strings.HasPrefix(req.URL.Path, "/auth/management-authorization/") {
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
func (r *SecurityRuntime) browserWriter(req *http.Request) bool {
	if r != nil && r.current != nil {
		return r.current.OriginEnabled() && r.browserOrigin(req, false) == nil && r.Ready(req.Context())
	}
	if r == nil || r.mode != "oidc" || r.authority == nil || r.browserOrigin(req, false) != nil {
		return false
	}
	current, known := r.native.Store().Current()
	return known && r.authorityCheck(req.Context(), current) == nil
}
func (r *SecurityRuntime) browserLogin(w http.ResponseWriter, req *http.Request) {
	if r.current != nil {
		r.currentBrowserLogin(w, req)
		return
	}
	if req.Method != http.MethodGet || !r.browserWriter(req) {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	query, err := browserQuery(req, "issuer", "return", "step_up")
	if err != nil || query.Get("issuer") == "" || query.Get("step_up") != "" && query.Get("step_up") != "true" {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	returnPath := query.Get("return")
	if returnPath == "" {
		returnPath = "/"
	}
	current, known := r.native.Store().Current()
	if !known {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	issuer, registered := current.Snapshot().Issuer(query.Get("issuer"))
	if !registered || !issuer.Enabled || issuer.Deleted {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	replaces := ""
	_, digest, sessionError := r.authenticateBrowser(req, false)
	if sessionError == nil {
		replaces = digest
	}
	if query.Get("step_up") == "true" && sessionError != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	discovery, err := r.fetcher.Discover(req.Context(), issuer)
	if err != nil {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	if r.authorityCheck(req.Context(), current) != nil {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	start, err := r.logins.Begin(oidc.Trust{Issuer: issuer, Generation: current.Generation(), ConfigRevision: issuer.ConfigRevision}, discovery, returnPath, replaces, query.Get("step_up") == "true")
	if err != nil {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	browserSetCookie(w, transactionCookieName, start.TransactionCookie, start.ExpiresAt, r.now(), http.SameSiteLaxMode)
	http.Redirect(w, req, start.AuthorizationURL, http.StatusFound)
}
func (r *SecurityRuntime) browserCallback(w http.ResponseWriter, req *http.Request) {
	if r.current != nil {
		r.currentBrowserCallback(w, req)
		return
	}
	if req.Method != http.MethodGet || !r.browserWriter(req) {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	query, err := browserQuery(req, "code", "state", "iss", "error", "error_description", "error_uri", "session_state", "scope", "authuser", "prompt", "hd")
	if err != nil {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	cookie, err := browserCookie(req, transactionCookieName)
	if err != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	completion, err := r.logins.Consume(query.Get("state"), cookie, req.URL.Path, query.Get("iss"))
	if err != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	authorizationID, operationAuthorization := completion.AuthorizationID()
	operationApproved := false
	if operationAuthorization {
		defer func() {
			if !operationApproved {
				r.authorizations.Reject(authorizationID)
			}
		}()
	}
	browserClearCookie(w, transactionCookieName, http.SameSiteLaxMode)
	if query.Get("error") != "" || query.Get("code") == "" {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	trust := completion.Trust()
	current, known := r.native.Store().Current()
	if !known || current.Generation() != trust.Generation || r.authorityCheck(req.Context(), current) != nil {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	issuer, registered := current.Snapshot().Issuer(trust.Issuer.URL)
	if !registered || !issuer.Enabled || issuer.ConfigRevision != trust.ConfigRevision {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	tokens, err := r.fetcher.ExchangeCode(req.Context(), trust, completion.Discovery(), r.secrets, query.Get("code"), completion.Verifier())
	if err != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	verified, err := r.verifier.VerifyLoginCompletion(req.Context(), completion, tokens)
	if err != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	if r.authorityCheck(req.Context(), current) != nil {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	evidence := requestEvidenceCut(current, time.Time{})
	evidence.kind, evidence.token = "token", verified
	callbackContext := withRequestAuthenticationEvidence(req.Context(), evidence)
	if operationAuthorization {
		if err := r.completeOperationAuthentication(callbackContext, authorizationID, current, r.now()); err != nil {
			browserHTTPError(w, connect.CodeUnauthenticated)
			return
		}
		operationApproved = true
		// Purpose approval never reaches IssueSession or ordinary cookie writes.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		_, _ = w.Write([]byte("<!doctype html><html lang=\"en\"><title>Operation authentication complete</title><body><p>Authentication recorded. Return to your reviewed change in Admin.</p></body></html>"))
		return
	}
	value, err := newBrowserSecret()
	if err != nil {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	csrf, err := newBrowserSecret()
	if err != nil {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	var changeID [16]byte
	if _, err := rand.Read(changeID[:]); err != nil {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	now := r.now()
	_, err = r.native.Store().IssueSession(callbackContext, security.SessionRequest{ChangeID: changeID, Identity: verified.Identity(), IssuerConfigRevision: trust.ConfigRevision, Digest: browserDigest(value), CSRFDigest: browserDigest(csrf), ReplacesDigest: completion.ReplacesDigest(), RequireRecentAuth: completion.RequiresRecentAuthentication(), AuthTime: verified.AuthTime(), Now: now, Lifetime: security.MaxSessionLifetime})
	if err != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	browserSetCookie(w, sessionCookieName, value, now.Add(security.MaxSessionLifetime), now, http.SameSiteStrictMode)
	browserSetCookie(w, csrfCookieName, csrf, now.Add(security.MaxSessionLifetime), now, http.SameSiteStrictMode)
	http.Redirect(w, req, completion.ReturnPath(), http.StatusSeeOther)
}
func newBrowserSecret() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}
func (r *SecurityRuntime) writeBrowserProto(w http.ResponseWriter, ctx context.Context, message proto.Message) {
	var raw []byte
	var err error
	if r.current != nil {
		raw, err = r.output.MarshalBrowserJSON(ctx, message)
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
func (r *SecurityRuntime) browserSession(w http.ResponseWriter, req *http.Request) {
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
	principal, err := r.control.GetCurrentPrincipal(ctx, connect.NewRequest(&pb.GetCurrentPrincipalRequest{}))
	if err != nil {
		browserHTTPError(w, connect.CodeOf(err))
		return
	}
	response := &pb.BrowserSession{Mode: pb.AuthMode_AUTH_MODE_OIDC, Principal: principal.Msg}
	if r.current != nil {
		response.CurrentProfile = principal.Msg.Version.CurrentProfile
	}
	r.writeBrowserProto(w, req.Context(), response)
}
func (r *SecurityRuntime) browserLogout(w http.ResponseWriter, req *http.Request) {
	if r.current != nil {
		r.currentBrowserLogout(w, req)
		return
	}
	if req.Method != http.MethodPost || req.URL.RawQuery != "" || !r.browserWriter(req) {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	ctx, digest, err := r.authenticateBrowser(req, true)
	if err != nil {
		browserHTTPError(w, connect.CodeOf(err))
		return
	}
	admission, _ := security.AdmissionFromContext(ctx)
	var changeID [16]byte
	if _, err := rand.Read(changeID[:]); err != nil {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	result, err := r.native.Store().RevokeSession(ctx, admission.Identity(), digest, changeID, r.now())
	if err != nil {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	browserClearCookie(w, sessionCookieName, http.SameSiteStrictMode)
	browserClearCookie(w, csrfCookieName, http.SameSiteStrictMode)
	generation := admission.Revision().Generation()
	response := &pb.SessionRevocation{Version: &pb.SecurityVersion{Revision: result.Revision, Digest: result.Digest[:], Generation: generation[:]}, Enforcement: pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_COMMITTED_PENDING}
	if r.authority.Enforced(result) {
		response.Enforcement = pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_ENFORCED
	}
	r.writeBrowserProto(w, req.Context(), response)
}

// browserOperations is a non-cacheable forward-auth decision for a local
// diagnostic gateway. It returns no session, identity, Role or metric data.
func (r *SecurityRuntime) browserOperations(w http.ResponseWriter, req *http.Request) {
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
