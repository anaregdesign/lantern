package provider

import (
	"crypto/tls"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func browserSessionFixture(t *testing.T) (*SecurityRuntime, *securityTestClock, string, string) {
	t.Helper()
	config, data, clock := securityRuntimeFixture(t)
	config.TrustedProxyIPs = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	runtime, cleanup, err := NewSecurityRuntime(config, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	clock.advance(35 * time.Second)
	session, csrf := base64.RawURLEncoding.EncodeToString(make([]byte, 32)), base64.RawURLEncoding.EncodeToString([]byte("12345678901234567890123456789012"))
	identity := security.Identity{Kind: security.OIDCPrincipal, Issuer: config.Bootstrap.Issuer.URL, Subject: "admin"}
	current, _ := runtime.native.Store().Current()
	issuer, _ := current.Snapshot().Issuer(identity.Issuer)
	if _, err := runtime.native.Store().IssueSession(t.Context(), security.SessionRequest{ChangeID: [16]byte{1}, Identity: identity, IssuerConfigRevision: issuer.ConfigRevision, Digest: browserDigest(session), CSRFDigest: browserDigest(csrf), AuthTime: clock.Now(), Now: clock.Now(), Lifetime: security.MaxSessionLifetime}); err != nil {
		t.Fatal(err)
	}
	return runtime, clock, session, csrf
}
func browserFixtureRequest(session, csrf string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "https://admin.example/browser/graph.v1.LanternSecurityService/ListRoles", nil)
	req.TLS = &tls.ConnectionState{}
	req.Header.Set("Origin", "https://admin.example")
	req.Header.Set(browserCSRFHeader, csrf)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
	return req
}
func TestSecurityBrowserBoundarySessionOriginCSRF(t *testing.T) {
	runtime, clock, session, csrf := browserSessionFixture(t)
	ctx, digest, err := runtime.authenticateBrowser(browserFixtureRequest(session, csrf), true)
	if err != nil || digest != browserDigest(session) {
		t.Fatal(err)
	}
	admission, known := security.AdmissionFromContext(ctx)
	if !known || !admission.Browser() || admission.CSRFToken() != csrf {
		t.Fatal("verified browser proof missing")
	}
	for name, mutate := range map[string]func(*http.Request){
		"missing csrf":      func(r *http.Request) { r.Header.Del(browserCSRFHeader) },
		"wrong csrf":        func(r *http.Request) { r.Header.Set(browserCSRFHeader, "wrong") },
		"foreign origin":    func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") },
		"missing origin":    func(r *http.Request) { r.Header.Del("Origin") },
		"spoofed host":      func(r *http.Request) { r.Host = "attacker.example" },
		"duplicate session": func(r *http.Request) { r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session}) },
		"empty then valid cookie": func(r *http.Request) {
			r.Header.Set("Cookie", sessionCookieName+"=; "+sessionCookieName+"="+session+"; "+csrfCookieName+"="+csrf)
		},
		"bearer substitution": func(r *http.Request) { r.Header.Set("Authorization", "Bearer token") },
		"untrusted forwarding": func(r *http.Request) {
			r.TLS = nil
			r.RemoteAddr = "192.0.2.1:443"
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set("X-Forwarded-Host", "admin.example")
		},
		"proxy chain": func(r *http.Request) {
			r.TLS = nil
			r.RemoteAddr = "127.0.0.1:443"
			r.Header.Set("X-Forwarded-Proto", "https,http")
			r.Header.Set("X-Forwarded-Host", "admin.example")
		},
		"cross site":   func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
		"encoded path": func(r *http.Request) { r.URL.RawPath = "/browser/%67raph.v1.LanternSecurityService/ListRoles" },
	} {
		t.Run(name, func(t *testing.T) {
			req := browserFixtureRequest(session, csrf)
			mutate(req)
			if _, _, err := runtime.authenticateBrowser(req, true); err == nil {
				t.Fatal("untrusted browser boundary accepted")
			}
		})
	}
	proxy := browserFixtureRequest(session, csrf)
	proxy.TLS = nil
	proxy.RemoteAddr = "127.0.0.1:443"
	proxy.Header.Set("X-Forwarded-Proto", "https")
	proxy.Header.Set("X-Forwarded-Host", "admin.example")
	if _, _, err := runtime.authenticateBrowser(proxy, true); err != nil {
		t.Fatal("pinned gateway rejected", err)
	}
	clock.advance(security.MaxSessionLifetime)
	if _, _, err := runtime.authenticateBrowser(browserFixtureRequest(session, csrf), true); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("fixed session expiry bypassed", err)
	}
}

func TestSecurityBrowserCookieFlags(t *testing.T) {
	now := time.Now()
	w := httptest.NewRecorder()
	browserSetCookie(w, sessionCookieName, "value", now.Add(time.Hour), now, http.SameSiteStrictMode)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Domain != "" || cookies[0].Path != "/" || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].MaxAge != 3600 {
		t.Fatal("host-bound cookie flags")
	}
	w = httptest.NewRecorder()
	browserClearCookie(w, transactionCookieName, http.SameSiteLaxMode)
	cookie := w.Result().Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.MaxAge != -1 || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("transaction cleanup flags")
	}
}
