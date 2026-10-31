package securitytransport

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/security"
)

const sessionCookieName = "__Host-lantern-session"
const csrfCookieName = "__Host-lantern-csrf"
const transactionCookieName = "__Host-lantern-login"
const browserCSRFHeader = "X-Lantern-CSRF"

var errBrowserBoundary = errors.New("browser session boundary rejected request")

// browserOrigin accepts direct TLS or one explicitly pinned gateway hop.
// Forwarded headers from any other source can never establish HTTPS or Host.
func (r *CredentialAdapter) browserOrigin(req *http.Request, requireOrigin bool) error {
	if r == nil || r.mode != "oidc" || req.URL.RawPath != "" || len(req.URL.RawQuery) > 16<<10 {
		return errBrowserBoundary
	}
	origin, err := url.Parse(r.config.BrowserOrigin)
	if err != nil || req.Host != origin.Host {
		return errBrowserBoundary
	}
	if req.TLS == nil {
		host, _, err := net.SplitHostPort(req.RemoteAddr)
		if err != nil {
			return errBrowserBoundary
		}
		ip, err := netip.ParseAddr(host)
		trusted := false
		for _, allowed := range r.config.TrustedProxyIPs {
			trusted = trusted || allowed.Unmap() == ip.Unmap()
		}
		if err != nil || !trusted || !exactBrowserHeader(req.Header, "X-Forwarded-Proto", "https") || !exactBrowserHeader(req.Header, "X-Forwarded-Host", origin.Host) {
			return errBrowserBoundary
		}
	}
	values := req.Header.Values("Origin")
	if requireOrigin || len(values) > 0 {
		if len(values) != 1 || values[0] != r.config.BrowserOrigin {
			return errBrowserBoundary
		}
	}
	if sites := req.Header.Values("Sec-Fetch-Site"); requireOrigin && len(sites) > 0 && (len(sites) != 1 || sites[0] != "same-origin") {
		return errBrowserBoundary
	}
	return nil
}
func exactBrowserHeader(headers http.Header, name, value string) bool {
	values := headers.Values(name)
	return len(values) == 1 && values[0] == value
}
func browserCookie(req *http.Request, name string) (string, error) {
	total := 0
	for _, value := range req.Header.Values("Cookie") {
		total += len(value)
		if total > 4096 {
			return "", errBrowserBoundary
		}
	}
	value := ""
	found := false
	for _, cookie := range req.Cookies() {
		if cookie.Name != name {
			continue
		}
		if found {
			return "", errBrowserBoundary
		}
		found = true
		value = cookie.Value
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(value) != 43 || len(raw) != 32 {
		return "", errBrowserBoundary
	}
	return value, nil
}
func browserDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
func (r *AuthenticationAdapter) authenticateBrowser(req *http.Request, requireCSRF bool) (context.Context, string, error) {
	ctx := req.Context()
	if err := r.browserOrigin(req, requireCSRF); err != nil {
		return ctx, "", connect.NewError(connect.CodePermissionDenied, errBrowserBoundary)
	}
	// API bearer credentials have a separate surface. Accepting both here
	// would let an attacker substitute authentication for the cookie session.
	if len(req.Header.Values("Authorization")) != 0 {
		return ctx, "", connect.NewError(connect.CodeUnauthenticated, errBrowserBoundary)
	}
	value, err := browserCookie(req, sessionCookieName)
	if err != nil {
		return ctx, "", connect.NewError(connect.CodeUnauthenticated, errBrowserBoundary)
	}
	csrf, err := browserCookie(req, csrfCookieName)
	if err != nil {
		return ctx, "", connect.NewError(connect.CodeUnauthenticated, errBrowserBoundary)
	}
	if r.current != nil {
		var p security.CurrentCredentialProducer
		if requireCSRF {
			p, err = r.CurrentBrowserProducer(req)
		} else if req.Method == http.MethodGet && (req.URL.Path == "/auth/session" || req.URL.Path == "/auth/operations" || req.URL.Path == "/auth/login") {
			p, err = r.currentBrowserProducer(req, true)
		} else {
			err = errBrowserBoundary
		}
		if err != nil {
			return ctx, "", connect.NewError(connect.CodeUnauthenticated, errBrowserBoundary)
		}
		verified, a, err := r.current.WithRequestCredential(ctx, p)
		if err != nil {
			return ctx, "", connect.NewError(connect.CodeUnauthenticated, errBrowserBoundary)
		}
		return security.WithAdmission(verified, a.WithBrowserProof(csrf)), browserDigest(value), nil
	}
	return ctx, "", connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
}

// BrowserRPCHandler is a distinct same-origin session surface. StripPrefix
// preserves the generated Connect procedure and its cursor binding internally.
func (r *AuthenticationAdapter) BrowserRPCHandler(path string, handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if req.Method != http.MethodPost || !strings.HasPrefix(req.URL.Path, "/browser"+path) {
			WritePublicAdmissionError(w, req, connect.CodePermissionDenied)
			return
		}
		if r.mode == "off" {
			http.StripPrefix("/browser", handler).ServeHTTP(w, req)
			return
		}
		ctx, _, err := r.authenticateBrowser(req, true)
		if err != nil {
			WritePublicAdmissionError(w, req, connect.CodeOf(err))
			return
		}
		http.StripPrefix("/browser", handler).ServeHTTP(w, req.WithContext(ctx))
	})
}
func browserHTTPError(w http.ResponseWriter, code connect.Code) {
	security.BindCurrentFailure(w)
	status := http.StatusForbidden
	if code == connect.CodeUnauthenticated {
		status = http.StatusUnauthorized
	}
	if code == connect.CodeUnavailable {
		status = http.StatusServiceUnavailable
	}
	if code == connect.CodeInvalidArgument {
		status = http.StatusBadRequest
	}
	if code == connect.CodeFailedPrecondition {
		status = http.StatusConflict
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"code":"` + code.String() + `","message":"browser session request rejected"}`))
}
func browserSetCookie(w http.ResponseWriter, name, value string, expiry, now time.Time, sameSite http.SameSite) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: sameSite, Expires: expiry.UTC(), MaxAge: int(expiry.Sub(now) / time.Second)})
}
func browserClearCookie(w http.ResponseWriter, name string, sameSite http.SameSite) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: sameSite, Expires: time.Unix(1, 0).UTC(), MaxAge: -1})
}
