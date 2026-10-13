package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"net/netip"
	"slices"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/authn"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// BrowserConfig is trusted transport configuration, never request input.
type BrowserConfig struct {
	BrowserOrigin   string
	TrustedProxyIPs []netip.Addr
}

// CredentialAdapter validates transport credentials before producing evidence.
// Its browser producer is private, so callers cannot assert Origin/CSRF checks.
type CredentialAdapter struct {
	mode        string
	config      BrowserConfig
	credentials *authn.Credentials
}

func NewCredentialAdapter(mode string, config BrowserConfig, credentials *authn.Credentials) *CredentialAdapter {
	config.TrustedProxyIPs = slices.Clone(config.TrustedProxyIPs)
	return &CredentialAdapter{mode: mode, config: config, credentials: credentials}
}

// The browser producer is private to the exact-origin/CSRF adapter. No public
// constructor accepts asserted checks or mutable factual authority.
type currentBrowserCredential struct {
	cookie, csrf, origin string
	browserReadOnly      bool
}

func (currentBrowserCredential) String() string   { return "[redacted current credential producer]" }
func (currentBrowserCredential) GoString() string { return "[redacted current credential producer]" }
func bearerCredential(headers http.Header) (string, error) {
	values := headers.Values("Authorization")
	if len(values) != 1 || len(values[0]) > (16<<10)+7 {
		return "", oidc.ErrInvalidToken
	}
	scheme, raw, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || raw == "" || strings.ContainsAny(raw, " \t\r\n") {
		return "", oidc.ErrInvalidToken
	}
	return raw, nil
}
func (r *CredentialAdapter) CurrentBearerProducer(headers http.Header) (security.CurrentCredentialProducer, error) {
	if r == nil || r.mode != "oidc" {
		return nil, oidc.ErrInvalidToken
	}
	raw, err := bearerCredential(headers)
	if err != nil {
		return nil, err
	}
	return r.credentials.Bearer(raw)
}
func (r *CredentialAdapter) CurrentBrowserProducer(req *http.Request) (security.CurrentCredentialProducer, error) {
	return r.currentBrowserProducer(req, false)
}

// Only the same-origin session bootstrap may omit the mutation CSRF header.
// Its facts remain explicitly read-only through capture, admission and output.

func (r *CredentialAdapter) CurrentBrowserReadProducer(req *http.Request) (security.CurrentCredentialProducer, error) {
	if req == nil || req.Method != http.MethodGet || req.URL.Path != "/auth/session" {
		return nil, errBrowserBoundary
	}
	return r.currentBrowserProducer(req, true)
}

func (r *CredentialAdapter) currentBrowserProducer(req *http.Request, readOnly bool) (security.CurrentCredentialProducer, error) {
	if r == nil || req == nil || r.browserOrigin(req, !readOnly) != nil || len(req.Header.Values("Authorization")) != 0 {
		return nil, errBrowserBoundary
	}
	cookie, err := browserCookie(req, sessionCookieName)
	if err != nil {
		return nil, err
	}
	csrf, err := browserCookie(req, csrfCookieName)
	if err != nil || !readOnly && !exactBrowserHeader(req.Header, browserCSRFHeader, csrf) {
		return nil, errBrowserBoundary
	}
	return currentBrowserCredential{cookie: cookie, csrf: csrf, origin: r.config.BrowserOrigin, browserReadOnly: readOnly}, nil
}

func (p currentBrowserCredential) VerifyCurrentCredential(ctx context.Context, view security.CurrentCredentialView) (security.CurrentCredentialFacts, error) {
	if err := ctx.Err(); err != nil {
		return security.CurrentCredentialFacts{}, err
	}
	low, high, err := view.TimeBounds()
	if err != nil || view.Snapshot() == nil {
		return security.CurrentCredentialFacts{}, security.ErrAuthorityUnavailable
	}
	snapshot := view.Snapshot()
	if p.origin != view.BrowserOrigin() {
		return security.CurrentCredentialFacts{}, errBrowserBoundary
	}
	s, known := snapshot.Session(browserDigest(p.cookie))
	_, _, active := snapshot.SessionAccess(s.Digest, high)
	if !known || s.CredentialProfile() != security.BrowserCookieProfile || !active || low.Before(s.CreatedAt) || subtle.ConstantTimeCompare([]byte(s.CSRFDigest), []byte(browserDigest(p.csrf))) != 1 {
		return security.CurrentCredentialFacts{}, errBrowserBoundary
	}
	return security.CurrentCredentialFacts{Session: &s, Origin: requestCredentialCommitment("browser-origin", p.origin), CSRF: sha256.Sum256([]byte("lantern/current-CSRF/v2\x00" + s.Digest + "\x00" + p.csrf)), BrowserReadOnly: p.browserReadOnly}, nil
}
