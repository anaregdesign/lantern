package oidc

// Deterministic fixture evidence only. These local certificates and RSA tokens
// are not Google/Apple credentials and do not qualify either real provider.
import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/golang-jwt/jwt/v5"
)

type nativeProviderFixture struct {
	fetcher      *Fetcher
	verifier     *Verifier
	transactions *NativeTransactions
	trust        Trust
	profile      security.CurrentProfile
	discovery    Discovery
	private      *rsa.PrivateKey
	secrets      *SecretRegistry
	nanos        atomic.Int64
	exchanges    atomic.Int64
	mu           sync.Mutex
	exchange     func(http.ResponseWriter, *http.Request)
}

func newNativeProviderFixture(t *testing.T, kind string) *nativeProviderFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &nativeProviderFixture{private: key}
	p.nanos.Store(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC).UnixNano())
	names := []string{"accounts.google.com", "oauth2.googleapis.com", "www.googleapis.com", "appleid.apple.com"}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "local native login fixture"}, DNSNames: names, NotBefore: p.now().Add(-time.Hour), NotAfter: p.now().Add(24 * time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	p.profile = security.CurrentProfile{Version: security.CurrentPublicVersion, Domain: [32]byte{1}, Cohort: [32]byte{2}, Generation: [16]byte{3}, Protocol: [32]byte{4}, Time: [32]byte{5}, Membership: [32]byte{6}, Configuration: [32]byte{7}}
	client := security.NativeClient{ID: "mobile", Kind: kind, ClientID: "com.example.client", ServiceOrigin: "https://lantern.example"}
	issuer := security.AppleIssuer
	p.discovery = Discovery{Issuer: issuer, AuthorizationEndpoint: issuer + "/auth/authorize", TokenEndpoint: issuer + "/auth/token", JWKSURI: issuer + "/auth/keys", ResponseTypes: []string{"code", "id_token"}}
	if kind == security.GoogleIDToken {
		issuer = security.GoogleIssuer
		client.ClientID = "backend.apps.googleusercontent.com"
		client.Presenters = []string{"android-client", "ios-client"}
		p.discovery = Discovery{Issuer: issuer, AuthorizationEndpoint: issuer + "/o/oauth2/v2/auth", TokenEndpoint: "https://oauth2.googleapis.com/token", JWKSURI: "https://www.googleapis.com/oauth2/v3/certs", ResponseTypes: []string{"code"}, CodeChallengeMethods: []string{"S256"}}
	} else {
		client.SecretRef = "apple"
		if kind == security.AppleAndroidWeb {
			client.AndroidPackage = "com.example.app"
			client.RedirectURI = client.ServiceOrigin + "/auth/native/apple/callback/mobile"
		}
	}
	p.trust = Trust{Issuer: security.Issuer{URL: issuer, Enabled: true, ConfigRevision: 1, Algorithms: []string{"RS256"}, NativeClients: []security.NativeClient{client}}, Generation: p.profile.Generation, ConfigRevision: 1}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(p.discovery)
		case "/auth/keys", "/oauth2/v3/certs":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "native-key", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB"}}})
		case "/auth/token":
			p.exchanges.Add(1)
			p.mu.Lock()
			handler := p.exchange
			p.mu.Unlock()
			if handler == nil {
				http.Error(w, "unavailable", 503)
			} else {
				handler(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	origins := map[string][]netip.Prefix{}
	for _, name := range names {
		origins["https://"+name] = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	}
	p.fetcher, err = NewFetcher(FetcherOptions{Roots: pool, PrivateOrigins: origins, TimeBounds: p.bounds})
	if err != nil {
		t.Fatal(err)
	}
	p.fetcher.resolve = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	p.fetcher.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	p.verifier = NewVerifierWithClock(NewKeyCacheWithClock(p.fetcher, p.now), p.now)
	p.transactions, err = NewNativeTransactions(p.bounds, "v2.1."+base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(t.TempDir(), "client.secret")
	if err := os.WriteFile(secretPath, []byte("fixture-server-private-client-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	p.secrets, err = NewSecretRegistry(map[string]SecretBinding{"apple": {Issuer: security.AppleIssuer, ClientID: client.ClientID, TokenOrigin: security.AppleIssuer, Path: secretPath}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.transactions.Close(); p.fetcher.CloseIdleConnections(); server.Close() })
	return p
}
func (p *nativeProviderFixture) now() time.Time { return time.Unix(0, p.nanos.Load()).UTC() }
func (p *nativeProviderFixture) bounds() (time.Time, time.Time, error) {
	now := p.now()
	return now, now, nil
}
func (p *nativeProviderFixture) begin(t *testing.T) (NativeStart, NativeAttempt) {
	t.Helper()
	start, err := p.transactions.Begin(p.trust, p.profile, "mobile", p.discovery)
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.transactions.Lookup(start.AttemptID, start.CompletionSecret)
	if err != nil {
		t.Fatal(err)
	}
	return start, a
}
func (p *nativeProviderFixture) claims(start NativeStart) jwt.MapClaims {
	c := jwt.MapClaims{"iss": p.trust.Issuer.URL, "sub": "existing-user", "aud": p.trust.Issuer.NativeClients[0].ClientID, "iat": p.now().Add(-time.Minute).Unix(), "exp": p.now().Add(time.Hour).Unix()}
	if start.Nonce != "" {
		c["nonce"] = start.Nonce
	}
	return c
}
func (p *nativeProviderFixture) sign(t *testing.T, c jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	token.Header["kid"] = "native-key"
	raw, err := token.SignedString(p.private)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
