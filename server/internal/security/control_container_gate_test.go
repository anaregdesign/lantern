//go:build linux && (amd64 || arm64)

package security_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/golang-jwt/jwt/v5"
)

// Only isolated test credentials are generated. No system/real issuer, trust
// store, user token, keychain or authentication settings are accessed.
type containerIssuerFixture struct {
	URL                                string
	SigningKey, TLSCertificate, TLSKey []byte
}

func TestContainerCurrentAuthority(t *testing.T) {
	dir, phase, mode := os.Getenv("LANTERN_CONTAINER_DIR"), os.Getenv("LANTERN_CONTAINER_PHASE"), os.Getenv("LANTERN_CONTAINER_MODE")
	if dir == "" {
		t.Skip("explicit container preparation harness only")
	}
	if mode != "container" {
		t.Fatal("explicit mode required")
	}
	if phase != "pause-before" && phase != "pause-after" && phase != "pre-stop" && phase != "resume-process" && phase != "peer-loss" && phase != "time-loss-fixture" {
		t.Fatal("unknown phase")
	}
	resume := phase == "resume-process"
	if !resume {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal("fresh case must not replace previous evidence", err)
		}
	}
	var fixture containerIssuerFixture
	if resume {
		raw, err := os.ReadFile(filepath.Join(dir, "issuer-fixture.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(raw, &fixture); err != nil {
			t.Fatal(err)
		}
	} else {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		fixture.SigningKey = key
	}
	key := ed25519.PrivateKey(fixture.SigningKey)
	pub := key.Public().(ed25519.PublicKey)
	var server *httptest.Server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(oidc.Discovery{Issuer: server.URL, AuthorizationEndpoint: server.URL + "/authorize", TokenEndpoint: server.URL + "/token", JWKSURI: server.URL + "/jwks", ResponseTypes: []string{"code"}, CodeChallengeMethods: []string{"S256"}})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "OKP", "kid": "container-fixture", "alg": "EdDSA", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(pub)}}})
		default:
			http.NotFound(w, r)
		}
	})
	server = httptest.NewUnstartedServer(handler)
	if resume {
		_ = server.Listener.Close()
		u, err := url.Parse(fixture.URL)
		if err != nil {
			t.Fatal(err)
		}
		server.Listener, err = net.Listen("tcp", u.Host)
		if err != nil {
			t.Fatal("original issuer endpoint occupied; no fallback", err)
		}
		pair, err := tls.X509KeyPair(fixture.TLSCertificate, fixture.TLSKey)
		if err != nil {
			t.Fatal(err)
		}
		server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	}
	server.StartTLS()
	defer server.Close()
	if resume {
		if server.URL != fixture.URL {
			t.Fatal("issuer identity changed")
		}
	} else {
		fixture.URL = server.URL
		fixture.TLSCertificate = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
		keyDER, err := x509.MarshalPKCS8PrivateKey(server.TLS.Certificates[0].PrivateKey)
		if err != nil {
			t.Fatal(err)
		}
		fixture.TLSKey = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
		raw, err := json.Marshal(fixture)
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(filepath.Join(dir, "issuer-fixture.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write(raw); err != nil {
			t.Fatal(err)
		}
		if err = file.Sync(); err != nil {
			t.Fatal(err)
		}
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	issuer := security.Issuer{URL: server.URL, ConfigRevision: 1, Enabled: true, ClientID: "admin-client", APIAudience: "lantern", RedirectURI: "https://admin.example" + oidc.CallbackPath(server.URL), Algorithms: []string{"EdDSA"}, HumanSubjectNamespaceQualified: true}
	g := security.NewContainerAuthorityGate(t, issuer, dir, resume)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	fetcher, err := oidc.NewFetcher(oidc.FetcherOptions{Roots: roots, PrivateOrigins: map[string][]netip.Prefix{server.URL: {netip.MustParsePrefix("127.0.0.1/32")}}, TimeBounds: g.TimeBounds})
	if err != nil {
		t.Fatal(err)
	}
	defer fetcher.CloseIdleConnections()
	runtime, err := provider.NewCurrentCredentialRuntime(fetcher, "https://admin.example", g.Now)
	if err != nil {
		t.Fatal(err)
	}
	bearer := func(life time.Duration) security.CurrentCredentialProducer {
		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{"iss": issuer.URL, "sub": "admin", "aud": "lantern", "client_id": "cli", "jti": "container-test-only", "iat": g.Now().Add(-time.Minute).Unix(), "exp": g.Now().Add(life).Unix()})
		token.Header["kid"], token.Header["typ"] = "container-fixture", "at+jwt"
		raw, err := token.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		p, err := runtime.Bearer(http.Header{"Authorization": {"Bearer " + raw}})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := bearer(time.Hour)
	if resume {
		g.ResumeContainerProcess(p)
		return
	}
	g.Renew()
	if phase == "time-loss-fixture" {
		g.ExerciseOutput(p)
	}
	if phase == "peer-loss" || phase == "time-loss-fixture" {
		g.ExerciseNetwork(p, phase)
		return
	}
	if phase != "pre-stop" {
		g.ExerciseContainerBoundary(p, phase, nil, nil, [32]byte{})
		return
	}
	role := func(id string) security.S1Command {
		return security.S1Command{Kind: security.S1Management, Changes: []security.Change{{Kind: security.PutRole, Role: &security.Role{ID: id, Rules: []security.PermissionRule{{ID: "read", Effect: security.Allow, Action: security.VertexRead, Resource: security.DataResource, Prefix: new(string)}}}}}}
	}
	r, err := g.Prepare(p, role("container_applied"))
	if err != nil {
		t.Fatal(err)
	}
	applied, err := g.Consume(r, p, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	g.Apply(applied)
	g.Renew()
	short := bearer(3 * time.Second)
	r, err = g.Prepare(short, role("container_pending"))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := g.Consume(r, short, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	bob := security.Identity{Kind: security.OIDCPrincipal, Issuer: issuer.URL, Subject: "bob"}
	expand := security.S1Command{Kind: security.S1Management, Changes: []security.Change{{Kind: security.PutAssignment, Identity: &bob, RoleID: "security_admin"}}}
	r, err = g.Prepare(p, expand)
	if err != nil {
		t.Fatal(err)
	}
	purpose, err := g.Begin(r, p)
	if err != nil {
		t.Fatal(err)
	}
	g.ExerciseContainerBoundary(p, phase, applied, pending, purpose.Ticket)
}
