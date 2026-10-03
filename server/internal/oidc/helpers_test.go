package oidc

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/anaregdesign/lantern/server/internal/security"
)

type testProvider struct {
	server    *httptest.Server
	fetcher   *Fetcher
	trust     Trust
	private   ed25519.PrivateKey
	mu        sync.Mutex
	document  any
	badIssuer bool
	fail      bool
	handler   http.Handler
	discovery atomic.Int64
	jwks      atomic.Int64
	nanos     atomic.Int64
}

func newTestProvider(t testing.TB) *testProvider {
	t.Helper()
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p := &testProvider{private: private, document: map[string]any{"keys": []jsonKey{{Kind: "OKP", ID: "key", Algorithm: "EdDSA", Curve: "Ed25519", X: base64.RawURLEncoding.EncodeToString(pub)}}}}
	p.nanos.Store(time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC).UnixNano())
	p.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.handler != nil {
			p.handler.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if p.fail {
			http.Error(w, "failure", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			p.discovery.Add(1)
			issuer := p.server.URL
			if p.badIssuer {
				issuer += "/different"
			}
			_ = json.NewEncoder(w).Encode(Discovery{Issuer: issuer, AuthorizationEndpoint: p.server.URL + "/authorize", TokenEndpoint: p.server.URL + "/token", JWKSURI: p.server.URL + "/jwks", ResponseTypes: []string{"code"}, CodeChallengeMethods: []string{"S256"}})
		case "/jwks":
			p.jwks.Add(1)
			_ = json.NewEncoder(w).Encode(p.document)
		default:
			http.NotFound(w, r)
		}
	}))
	roots := x509.NewCertPool()
	roots.AddCert(p.server.Certificate())
	p.fetcher, err = NewFetcher(FetcherOptions{Roots: roots, PrivateOrigins: map[string][]netip.Prefix{p.server.URL: {netip.MustParsePrefix("127.0.0.1/32")}}})
	if err != nil {
		t.Fatal(err)
	}
	p.trust = Trust{Issuer: security.Issuer{URL: p.server.URL, Enabled: true, ClientID: "admin", APIAudience: "lantern", RedirectURI: "https://admin.example/auth/callback", Algorithms: []string{"EdDSA"}}, Generation: [16]byte{1}, ConfigRevision: 1}
	t.Cleanup(func() { p.fetcher.CloseIdleConnections(); p.server.Close() })
	return p
}

func (p *testProvider) now() time.Time { return time.Unix(0, p.nanos.Load()).UTC() }

func (p *testProvider) accessClaims() jwt.MapClaims {
	return jwt.MapClaims{"iss": p.trust.Issuer.URL, "sub": "user", "aud": "lantern", "client_id": "client", "jti": "unique", "iat": p.now().Unix(), "exp": p.now().Add(time.Hour).Unix()}
}

func (p *testProvider) sign(t testing.TB, claims jwt.MapClaims, typ string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"], token.Header["typ"] = "key", typ
	raw, err := token.SignedString(p.private)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
