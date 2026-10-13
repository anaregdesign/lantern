package service

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
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/authn"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
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

func newSecurityProviderFixture(t *testing.T, config *authenticationConfig, clock *securityTestClock) *securityProviderFixture {
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

type authenticationConfig struct {
	Mode            string
	BrowserOrigin   string
	TrustedProxyIPs []netip.Addr
	Bootstrap       security.Bootstrap
	PrivateOrigins  map[string][]netip.Prefix
	RootCAFile      string
	Clock           func() time.Time
}
type authenticationFixture struct {
	*AuthenticationAdapter
	native         *security.NativeStore
	authority      *security.LeaseAuthority
	fetcher        *oidc.Fetcher
	logins         *oidc.LoginTransactions
	authorizations *security.ManagementAuthorizations
	testConfig     authenticationConfig
}

func authenticationConfigFixture(t *testing.T) (authenticationConfig, *securityTestClock) {
	t.Helper()
	clock := &securityTestClock{now: time.Now()}
	issuer := security.Issuer{URL: "https://idp.example", Enabled: true, ClientID: "admin", APIAudience: "api", RedirectURI: "https://admin.example" + oidc.CallbackPath("https://idp.example"), Algorithms: []string{"EdDSA"}}
	return authenticationConfig{Mode: "oidc", BrowserOrigin: "https://admin.example", Bootstrap: security.Bootstrap{Revision: 1, Issuer: issuer, AdminSubjects: []string{"admin", "other"}}, Clock: clock.Now}, clock
}
func newAuthenticationFixture(t *testing.T, config authenticationConfig) (*authenticationFixture, func(), error) {
	t.Helper()
	r := &authenticationFixture{testConfig: config}
	now := config.Clock
	if now == nil {
		now = time.Now
	}
	r.AuthenticationAdapter = NewAuthenticationAdapter(AuthenticationOptions{Credentials: NewCredentialAdapter(config.Mode, BrowserConfig{BrowserOrigin: config.BrowserOrigin, TrustedProxyIPs: config.TrustedProxyIPs}, nil), Now: now})
	if config.Mode == "off" {
		return r, func() {}, nil
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	graph := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	r.native, err = security.CreateNativeStore(security.NativeStoreOptions{Path: filepath.Join(t.TempDir(), "policy.wal"), Generation: [16]byte{1}, PublicKey: public, PrivateKey: private, Limits: security.DefaultPolicyLimits(), Graph: graph, MaxJournalBytes: security.DefaultSystemJournalMax})
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		r.authorizations.Close()
		if r.fetcher != nil {
			r.fetcher.CloseIdleConnections()
		}
		_ = r.native.Close()
	}
	t.Cleanup(cleanup)
	if _, err = r.native.Store().ApplyBootstrap(t.Context(), config.Bootstrap); err != nil {
		return nil, nil, err
	}
	r.authority, err = security.NewLeaseAuthority(r.native.Store(), security.LeaseAuthorityOptions{Clock: &securityTestClockAdapter{now}})
	if err != nil {
		return nil, nil, err
	}
	roots := x509.NewCertPool()
	if config.RootCAFile != "" {
		raw, e := os.ReadFile(config.RootCAFile)
		if e != nil {
			return nil, nil, e
		}
		roots.AppendCertsFromPEM(raw)
	}
	r.fetcher, err = oidc.NewFetcher(oidc.FetcherOptions{PrivateOrigins: config.PrivateOrigins, Roots: roots})
	if err != nil {
		return nil, nil, err
	}
	secrets, err := oidc.NewSecretRegistry(nil)
	if err != nil {
		return nil, nil, err
	}
	keys := oidc.NewKeyCacheWithClock(r.fetcher, now)
	credentials := authn.NewCredentials(keys)
	verifier := oidc.NewVerifierWithClock(keys, now)
	r.logins, err = oidc.NewLoginTransactionsWithClock(config.BrowserOrigin, []string{"/", "/vertices", "/security/roles"}, now)
	if err != nil {
		return nil, nil, err
	}
	r.authorizations = security.NewManagementAuthorizations(oidc.LoginTransactionLifetime, oidc.MaxLoginTransactions)
	workflow, err := authn.NewLegacyWorkflow(authn.LegacyAuthority{Store: r.native.Store(), Authorizations: r.authorizations, Check: r.authority.Check, Enforced: r.authority.Enforced}, authn.OIDCResources{Fetcher: r.fetcher, Secrets: secrets, Verifier: verifier, Credentials: credentials, Logins: r.logins, Now: now})
	if err != nil {
		return nil, nil, err
	}
	r.AuthenticationAdapter = NewAuthenticationAdapter(AuthenticationOptions{Credentials: NewCredentialAdapter(config.Mode, BrowserConfig{BrowserOrigin: config.BrowserOrigin, TrustedProxyIPs: config.TrustedProxyIPs}, credentials), Workflow: workflow, Store: r.native.Store(), Verifier: verifier, Now: now, AuthorityCheck: r.authority.Check, WriterEnabled: func() bool { return true }})
	return r, cleanup, nil
}

type securityTestClockAdapter struct{ now func() time.Time }

func (c *securityTestClockAdapter) Now() time.Time { return c.now() }
func newBrowserSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
