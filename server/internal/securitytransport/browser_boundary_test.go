package securitytransport

import (
	"crypto/tls"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

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
	r := NewCredentialAdapter("oidc", BrowserConfig{BrowserOrigin: "https://admin.example", TrustedProxyIPs: []netip.Addr{netip.MustParseAddr("127.0.0.1")}}, nil)
	session, csrf := base64.RawURLEncoding.EncodeToString(make([]byte, 32)), base64.RawURLEncoding.EncodeToString([]byte("12345678901234567890123456789012"))
	if _, err := r.CurrentBrowserProducer(browserFixtureRequest(session, csrf)); err != nil {
		t.Fatal(err)
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
			if _, err := r.CurrentBrowserProducer(req); err == nil {
				t.Fatal("untrusted browser boundary accepted")
			}
		})
	}
	proxy := browserFixtureRequest(session, csrf)
	proxy.TLS = nil
	proxy.RemoteAddr = "127.0.0.1:443"
	proxy.Header.Set("X-Forwarded-Proto", "https")
	proxy.Header.Set("X-Forwarded-Host", "admin.example")
	if _, err := r.CurrentBrowserProducer(proxy); err != nil {
		t.Fatal("pinned gateway rejected", err)
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
