package service

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/authn"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestCurrentBrowserReadProducerOnlyBootstrapsSession(t *testing.T) {
	r := &CredentialAdapter{mode: "oidc", config: BrowserConfig{BrowserOrigin: "https://admin.example"}}
	req := httptest.NewRequest(http.MethodGet, "https://admin.example/auth/session", nil)
	req.TLS = &tls.ConnectionState{}
	cookie := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: cookie})
	p, err := r.CurrentBrowserReadProducer(req)
	if err != nil || !p.(currentBrowserCredential).browserReadOnly {
		t.Fatal("CSRF bootstrap required the unavailable mutation header", err)
	}
	if _, err = r.CurrentBrowserProducer(req); err == nil {
		t.Fatal("read-only HTTP evidence became mutation proof")
	}
	for _, path := range []string{"/auth/logout", "/browser/graph.v1.LanternService/GetVertex"} {
		other := req.Clone(req.Context())
		other.URL.Path = path
		if _, err = r.CurrentBrowserReadProducer(other); err == nil {
			t.Fatal("read bootstrap escaped its exact endpoint", path)
		}
	}
	req.Method = http.MethodPost
	if _, err = r.CurrentBrowserReadProducer(req); err == nil {
		t.Fatal("mutation method used bootstrap evidence")
	}
	req.Header.Set("Origin", "https://admin.example")
	req.Header.Set(browserCSRFHeader, cookie)
	p, err = r.CurrentBrowserProducer(req)
	if err != nil || p.(currentBrowserCredential).browserReadOnly {
		t.Fatal("exact separate mutation proof refused", err)
	}
}

func TestCurrentCredentialProducerBoundary(t *testing.T) {
	r := NewCredentialAdapter("oidc", BrowserConfig{}, authn.NewCredentials(oidc.NewKeyCache(nil)))
	for _, values := range [][]string{nil, {"Bearer "}, {"Bearer a b"}, {"Bearer a", "Bearer b"}, {"Basic a"}, {"Bearer " + security.MachineTokenPrefix + "not-human"}} {
		if _, err := r.CurrentBearerProducer(http.Header{"Authorization": values}); err == nil {
			t.Fatal("ambiguous or nonhuman credential accepted")
		}
	}
	producer, err := r.CurrentBearerProducer(http.Header{"Authorization": {"Bearer confidential-placeholder"}})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%v", producer) != "[redacted current credential producer]" || fmt.Sprintf("%#v", producer) != "[redacted current credential producer]" {
		t.Fatal("producer revealed credential")
	}
	if _, err := producer.VerifyCurrentCredential(t.Context(), security.CurrentCredentialView{}); err == nil {
		t.Fatal("zero installed S1 view accepted")
	}
	if _, err := r.CurrentBrowserProducer(nil); err == nil {
		t.Fatal("missing native browser request")
	}
	if _, err := (*authn.Credentials)(nil).Code(oidc.LoginCompletion{}, oidc.CodeTokens{}); err == nil {
		t.Fatal("missing native Code producer")
	}
}
