package provider

import (
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/protobuf/encoding/protojson"
)

func (r *SecurityRuntime) currentBrowserLogin(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet || !r.browserWriter(req) {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	q, err := browserQuery(req, "issuer", "return")
	if err != nil || q.Get("issuer") == "" {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	returnPath := q.Get("return")
	if returnPath == "" {
		returnPath = "/"
	}
	i, cut, err := r.current.LoginIssuer(req.Context(), q.Get("issuer"))
	if err != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	replaces := ""
	if _, digest, e := r.authenticateBrowser(req, false); e == nil {
		replaces = digest
	}
	d, err := r.fetcher.Discover(req.Context(), i)
	if err != nil {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	_, after, err := r.current.LoginIssuer(req.Context(), i.URL)
	if err != nil || after != cut {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	start, err := r.logins.Begin(oidc.Trust{Issuer: i, Generation: cut.Generation, ConfigRevision: i.ConfigRevision}, d, returnPath, replaces, false)
	if err != nil {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	if r.current.BindLoginOutput(req.Context(), cut, i.URL) != nil {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	browserSetCookie(w, transactionCookieName, start.TransactionCookie, start.ExpiresAt, r.now(), http.SameSiteLaxMode)
	http.Redirect(w, req, start.AuthorizationURL, http.StatusFound)
}

func (r *SecurityRuntime) currentBrowserCallback(w http.ResponseWriter, req *http.Request) {
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
	completion, err := r.logins.Consume(q.Get("state"), cookie, req.URL.Path, q.Get("iss"))
	if err != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	id, purpose := completion.AuthorizationID()
	approved := false
	if purpose {
		defer func() {
			if !approved {
				r.current.RejectPurpose(id)
			}
		}()
	}
	browserClearCookie(w, transactionCookieName, http.SameSiteLaxMode)
	if q.Get("error") != "" || q.Get("code") == "" {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	trust := completion.Trust()
	i, cut, err := r.current.LoginIssuer(req.Context(), trust.Issuer.URL)
	if err != nil || cut.Generation != trust.Generation || i.ConfigRevision != trust.ConfigRevision {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	// Atomic Consume preceded this one exchange. Replays never reach IdP, and
	// post-Apply re-evaluation keeps this same detached successful Code event.
	tokens, err := r.fetcher.ExchangeCode(req.Context(), trust, completion.Discovery(), r.secrets, q.Get("code"), completion.Verifier())
	if err != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	producer, err := r.CurrentCodeProducer(completion, tokens)
	if err != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	if purpose {
		if _, err = r.current.CompletePurpose(req.Context(), id, producer); err != nil {
			browserHTTPError(w, connect.CodeUnauthenticated)
			return
		}
		approved = true
		if r.current.BindPublicMetadata(req.Context(), security.CurrentPurposeReturn) != nil {
			browserHTTPError(w, connect.CodeUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		_, _ = w.Write([]byte("<!doctype html><html lang=\"en\"><title>Operation authentication</title><body><p>Return to your reviewed change in Admin to check the result.</p></body></html>"))
		return
	}
	ctx, a, err := r.current.WithRequestCredential(req.Context(), producer)
	if err != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
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
	review, session, err := r.current.PrepareSession(ctx, a, producer, browserDigest(value), browserDigest(csrf), completion.ReplacesDigest())
	if err != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	result, err := r.current.Apply(ctx, review, producer, [32]byte{})
	if err != nil || result.Original == nil || result.Original.Disposition() != security.S1Applied {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	ctx, a, err = r.current.RefreshRequest(ctx)
	if err != nil || r.current.BindIssuedSessionOutput(ctx, a, session) != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	// Cookie publication follows authenticated Apply and a new current view.
	// The pre-Apply admission is never reused for the newly installed session.
	browserSetCookie(w, sessionCookieName, value, session.ExpiresAt, r.now(), http.SameSiteStrictMode)
	browserSetCookie(w, csrfCookieName, csrf, session.ExpiresAt, r.now(), http.SameSiteStrictMode)
	http.Redirect(w, req.WithContext(ctx), completion.ReturnPath(), http.StatusSeeOther)
}

func (r *SecurityRuntime) currentBrowserManagementAuthorization(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet || !r.browserWriter(req) {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	q, err := browserQuery(req, "affinity")
	if err != nil || !strings.HasPrefix(q.Get("affinity"), r.currentAttemptPrefix()+".") {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	text := strings.TrimPrefix(req.URL.Path, "/auth/management-authorization/")
	raw, err := base64.RawURLEncoding.Strict().DecodeString(text)
	if err != nil || len(text) != 43 || len(raw) != 32 {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	ticket := [32]byte(raw)
	notBefore, err := r.current.PurposeNotBefore(req.Context(), ticket)
	if err != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	// Timer merely paces navigation. startPurpose independently requires the
	// qualified lower endpoint; neither this timer nor host wall is evidence.
	for {
		low, _, err := r.current.TimeBounds()
		if err != nil {
			browserHTTPError(w, connect.CodeUnavailable)
			return
		}
		if !low.Before(notBefore) {
			break
		}
		timer := time.NewTimer(min(notBefore.Sub(low), 250*time.Millisecond))
		select {
		case <-req.Context().Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	navigation, err := r.current.StartPurpose(req.Context(), ticket)
	if err != nil || q.Get("affinity") != r.currentAttemptAffinity(navigation.ID) {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	d, err := r.fetcher.Discover(req.Context(), navigation.Issuer)
	if err != nil || !r.current.Ready(req.Context()) {
		r.current.RejectPurpose(navigation.ID)
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	start, err := r.logins.BeginAuthorization(oidc.Trust{Issuer: navigation.Issuer, Generation: navigation.Generation, ConfigRevision: navigation.Issuer.ConfigRevision}, d, navigation.ID)
	if err != nil {
		r.current.RejectPurpose(navigation.ID)
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	if r.current.BindLoginOutput(req.Context(), navigation.Cut, navigation.Issuer.URL) != nil {
		r.current.RejectPurpose(navigation.ID)
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	browserSetCookie(w, transactionCookieName, start.TransactionCookie, start.ExpiresAt, r.now(), http.SameSiteLaxMode)
	http.Redirect(w, req, start.AuthorizationURL, http.StatusFound)
}

func (r *SecurityRuntime) currentBrowserLogout(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost || req.URL.RawQuery != "" || r.browserOrigin(req, true) != nil || req.Header.Get("Content-Type") != "application/json" {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, 16<<10+1))
	var request pb.CurrentLogoutRequest
	if err != nil || len(raw) > 16<<10 || oidc.ValidateJSON(raw) != nil || protojson.Unmarshal(raw, &request) != nil {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	profile, err := service.DecodeCurrentProfile(request.Profile)
	if err != nil || profile != r.current.Profile() || request.LocalOnly && (request.PrepareOnly || request.Review != nil) || request.PrepareOnly && request.Review != nil {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	if request.LocalOnly {
		csrf, err := browserCookie(req, csrfCookieName)
		if err != nil || !exactBrowserHeader(req.Header, browserCSRFHeader, csrf) {
			browserHTTPError(w, connect.CodePermissionDenied)
			return
		}
		if r.current.BindPublicMetadata(req.Context(), security.CurrentLocalCookieClear) != nil {
			browserHTTPError(w, connect.CodeUnavailable)
			return
		}
		browserClearCookie(w, sessionCookieName, http.SameSiteStrictMode)
		browserClearCookie(w, csrfCookieName, http.SameSiteStrictMode)
		r.writeBrowserProto(w, req.Context(), &pb.SessionRevocation{LocalCookieCleared: true})
		return
	}
	ctx, _, err := r.authenticateBrowser(req, true)
	if err != nil {
		browserHTTPError(w, connect.CodeOf(err))
		return
	}
	a, _ := security.AdmissionFromContext(ctx)
	producer, err := r.current.RequestCredential(ctx)
	if err != nil {
		browserHTTPError(w, connect.CodeUnauthenticated)
		return
	}
	if request.PrepareOnly {
		review, err := r.current.PrepareSessionRevocation(ctx, a, producer)
		if err != nil {
			browserHTTPError(w, connect.CodeUnavailable)
			return
		}
		wire, err := service.EncodeCurrentSessionReview(review)
		if err != nil || a.BindCurrentSelfOutput(ctx) != nil {
			browserHTTPError(w, connect.CodeUnavailable)
			return
		}
		r.writeBrowserProto(w, req.Context(), &pb.SessionRevocation{CurrentReview: wire})
		return
	}
	review, err := service.DecodeCurrentSessionReview(r.current, request.Review)
	if err != nil || review.Operation.Actor() != a.Identity() {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	result, _ := r.current.Apply(ctx, review, producer, [32]byte{})
	// The client retained the exact review before dispatch. Even when its own
	// session was just revoked, fixed local deletion remains possible. Only a
	// new current read admission can disclose a cluster result.
	response := &pb.SessionRevocation{LocalCookieCleared: true}
	if r.current.BindPublicMetadata(req.Context(), security.CurrentLocalCookieClear) != nil {
		browserHTTPError(w, connect.CodeUnavailable)
		return
	}
	if freshCtx, fresh, e := r.current.RefreshRequest(ctx); e == nil && result.Profile == profile && r.current.BindOriginalOutput(freshCtx, fresh, review.ID, review.Operation.Digest()) == nil {
		response.CurrentResult = service.EncodeCurrentResult(r.current.ObserveResult(freshCtx, result))
	}
	browserClearCookie(w, sessionCookieName, http.SameSiteStrictMode)
	browserClearCookie(w, csrfCookieName, http.SameSiteStrictMode)
	r.writeBrowserProto(w, req.Context(), response)
}
