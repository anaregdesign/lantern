package provider

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/golang-jwt/jwt/v5"
)

type securityTestClock struct {
	mu  sync.Mutex
	now time.Time
}

type securityProviderFixture struct {
	server     *httptest.Server
	private    ed25519.PrivateKey
	fetches    atomic.Int64
	clock      *securityTestClock
	exchangeMu sync.Mutex
	exchange   http.Handler
}

func newSecurityProviderFixture(t *testing.T, config *SecurityConfig, clock *securityTestClock) *securityProviderFixture {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p := &securityProviderFixture{private: private, clock: clock}
	p.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.fetches.Add(1)
		if r.URL.Path == "/token" {
			p.exchangeMu.Lock()
			handler := p.exchange
			p.exchangeMu.Unlock()
			if handler != nil {
				handler.ServeHTTP(w, r)
				return
			}
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("credentials sent to metadata endpoint")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(oidc.Discovery{Issuer: p.server.URL, AuthorizationEndpoint: p.server.URL + "/authorize", TokenEndpoint: p.server.URL + "/token", JWKSURI: p.server.URL + "/jwks", ResponseTypes: []string{"code"}, CodeChallengeMethods: []string{"S256"}})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "OKP", "kid": "key", "alg": "EdDSA", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(public)}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(p.server.Close)
	config.Bootstrap.Issuer.URL = p.server.URL
	config.Bootstrap.Issuer.RedirectURI = config.BrowserOrigin + oidc.CallbackPath(p.server.URL)
	config.PrivateOrigins = map[string][]netip.Prefix{p.server.URL: {netip.MustParsePrefix("127.0.0.1/32")}}
	config.RootCAFile = filepath.Join(t.TempDir(), "provider.pem")
	if err := os.WriteFile(config.RootCAFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: p.server.Certificate().Raw}), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p *securityProviderFixture) token(t *testing.T, subject string, mutate func(jwt.MapClaims)) string {
	t.Helper()
	now := p.clock.Now()
	claims := jwt.MapClaims{"iss": p.server.URL, "sub": subject, "aud": "api", "client_id": "client", "jti": "request", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "auth_time": now.Unix()}
	if mutate != nil {
		mutate(claims)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"], token.Header["typ"] = "key", "at+jwt"
	raw, err := token.SignedString(p.private)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (c *securityTestClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *securityTestClock) advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}
func securityKeyFixture(t *testing.T, dir string) (string, string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	privatePath, publicPath := filepath.Join(dir, "writer.key"), filepath.Join(dir, "writer.pub")
	if err = os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), 0644); err != nil {
		t.Fatal(err)
	}
	return privatePath, publicPath
}
func securityRuntimeFixture(t *testing.T) (SecurityConfig, *service.ServingRuntime, *securityTestClock) {
	t.Helper()
	dir := t.TempDir()
	privatePath, publicPath := securityKeyFixture(t, dir)
	clock := &securityTestClock{now: time.Now()}
	issuer := security.Issuer{URL: "https://idp.example", Enabled: true, ClientID: "admin", APIAudience: "api", RedirectURI: "https://admin.example" + oidc.CallbackPath("https://idp.example"), Algorithms: []string{"EdDSA"}}
	config := SecurityConfig{Mode: "oidc", StoreMode: "fresh", StorePath: filepath.Join(dir, "security.wal"), Generation: [16]byte{1}, WriterKeyFile: privatePath, WriterPublicKeyFile: publicPath, NodeRole: "writer", WriterEndpoint: "https://writer-peer.example", BrowserOrigin: "https://admin.example", Bootstrap: security.Bootstrap{Revision: 1, Issuer: issuer, AdminSubjects: []string{"admin", "other"}}, MaxJournalBytes: security.DefaultSystemJournalMax, ClockQualified: true, Clock: clock.Now}
	graph := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	log := mutationlog.New(mutationlog.Options{Capacity: 16})
	hlcClock := hlc.New(hlc.NodeID{7}, hlc.Options{})
	data, err := service.NewGraphOnlyServingRuntime(graph, log, hlcClock, "namespaced-v1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.Close() })
	return config, data, clock
}
