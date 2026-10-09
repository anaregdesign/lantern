package security_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/golang-jwt/jwt/v5"
)

// Crosses actual HTTPS discovery/JWKS, signature verifier, opaque one-use Code
// transaction + exchange, provider adapter, installed S1, native M/P/B/mTLS,
// exclusive origin signing/serial ownership and deterministic Apply.
func TestCurrentAuthorityGenuineProducerGate(t *testing.T) {
	native := os.Getenv("LANTERN_TEST_CURRENT_NATIVE") == "1"
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	idToken := ""
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(oidc.Discovery{Issuer: server.URL, AuthorizationEndpoint: server.URL + "/authorize", TokenEndpoint: server.URL + "/token", JWKSURI: server.URL + "/jwks", ResponseTypes: []string{"code"}, CodeChallengeMethods: []string{"S256"}})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "OKP", "kid": "current-native", "alg": "EdDSA", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(pub)}}})
		case "/token":
			if err := r.ParseForm(); err != nil || r.Form.Get("code_verifier") == "" || r.Form.Get("code") != "actual-exchange" {
				t.Error("missing genuine exchange")
				http.Error(w, "bad", 400)
				return
			}
			mu.Lock()
			token := idToken
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"token_type": "Bearer", "id_token": token})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	issuer := security.Issuer{URL: server.URL, ConfigRevision: 1, Enabled: true, ClientID: "admin-client", APIAudience: "lantern", RedirectURI: "https://admin.example" + oidc.CallbackPath(server.URL), Algorithms: []string{"EdDSA"}, HumanSubjectNamespaceQualified: true}
	g := security.NewCurrentAuthorityGate(t, issuer, nil, native)
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
	sign := func(claims jwt.MapClaims, typ string) string {
		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
		token.Header["kid"], token.Header["typ"] = "current-native", typ
		raw, err := token.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	rawAccess := sign(jwt.MapClaims{"iss": issuer.URL, "sub": "admin", "aud": "lantern", "client_id": "cli", "jti": "original", "iat": g.Now().Add(-time.Minute).Unix(), "exp": g.Now().Add(time.Hour).Unix()}, "at+jwt")
	bearer, err := runtime.Bearer(http.Header{"Authorization": {"Bearer " + rawAccess}})
	if err != nil {
		t.Fatal(err)
	}
	role := security.S1Command{Kind: security.S1Management, Changes: []security.Change{{Kind: security.PutRole, Role: &security.Role{ID: "genuine_reader", Rules: []security.PermissionRule{{ID: "read", Effect: security.Allow, Action: security.VertexRead, Resource: security.DataResource, Prefix: new(string)}}}}}}
	g.Renew()
	if phase := os.Getenv("LANTERN_CURRENT_OUTPUT_CHILD"); phase != "" {
		g.ExercisePausedOutput(bearer, phase, os.Getenv("LANTERN_CURRENT_OUTPUT_CHILD_DIR"))
		return
	}
	g.ExerciseOutput(bearer)
	if native {
		g.MeasureWarmOutput(bearer)
	}
	shortAccess := sign(jwt.MapClaims{"iss": issuer.URL, "sub": "admin", "aud": "lantern", "client_id": "cli", "jti": "short-original", "iat": g.Now().Add(-time.Minute).Unix(), "exp": g.Now().Add(3 * time.Second).Unix()}, "at+jwt")
	shortProducer, err := runtime.Bearer(http.Header{"Authorization": {"Bearer " + shortAccess}})
	if err != nil {
		t.Fatal(err)
	}
	shortRequest, err := g.Prepare(shortProducer, role)
	if err != nil {
		t.Fatal(err)
	}
	shortH, err := g.Consume(shortRequest, shortProducer, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := g.Prepare(bearer, role)
	if err != nil {
		t.Fatal("real signed access", err)
	}
	h, err := g.Consume(r, bearer, [32]byte{})
	if err != nil || len(h) == 0 {
		t.Fatal("actual access -> durable H", err)
	}
	again, err := g.Consume(r, nil, [32]byte{})
	if err != nil || string(again) != string(h) {
		t.Fatal("byte identical retry", err)
	}
	bad, _ := runtime.Bearer(http.Header{"Authorization": {"Bearer " + rawAccess[:len(rawAccess)-3] + "bad"}})
	if _, err := g.Prepare(bad, role); err == nil {
		t.Fatal("bad signature admitted")
	}
	login, err := oidc.NewLoginTransactionsWithClock("https://admin.example", []string{"/", "/security/roles"}, g.Now)
	if err != nil {
		t.Fatal(err)
	}
	trust := oidc.Trust{Issuer: issuer, Generation: g.Cut().Generation, ConfigRevision: 1}
	discovery, err := fetcher.Discover(t.Context(), issuer)
	if err != nil {
		t.Fatal(err)
	}
	code := func(id [32]byte, stepUp bool) (security.CurrentCredentialProducer, time.Time) {
		var start oidc.LoginStart
		var err error
		if id != [32]byte{} {
			start, err = login.BeginAuthorization(trust, discovery, id)
		} else {
			start, err = login.Begin(trust, discovery, "/", "", stepUp)
		}
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(start.AuthorizationURL)
		g.Advance(time.Second)
		completion, err := login.Consume(u.Query().Get("state"), start.TransactionCookie, oidc.CallbackPath(issuer.URL), issuer.URL)
		if err != nil {
			t.Fatal(err)
		}
		authTime := g.Now().Add(-time.Second).Truncate(time.Second)
		claims := jwt.MapClaims{"iss": issuer.URL, "sub": "admin", "aud": "admin-client", "iat": g.Now().Unix(), "exp": g.Now().Add(time.Hour).Unix(), "auth_time": authTime.Unix(), "nonce": u.Query().Get("nonce")}
		mu.Lock()
		idToken = sign(claims, "JWT")
		mu.Unlock()
		tokens, err := fetcher.ExchangeCode(t.Context(), trust, discovery, nil, "actual-exchange", completion.Verifier())
		if err != nil {
			t.Fatal(err)
		}
		p, err := runtime.Code(completion, tokens)
		if err != nil {
			t.Fatal(err)
		}
		g.AwaitLower(g.Now())
		return p, authTime
	}
	ordinary, authTime := code([32]byte{}, false)
	cookie := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	csrfBytes := make([]byte, 32)
	csrfBytes[0] = 1
	csrf := base64.RawURLEncoding.EncodeToString(csrfBytes)
	digest := func(raw string) string { d := sha256.Sum256([]byte(raw)); return hex.EncodeToString(d[:]) }
	session := security.Session{CSRFDigest: digest(csrf), IssuerConfigRevision: 1, Digest: digest(cookie), Identity: security.Identity{Kind: security.OIDCPrincipal, Issuer: issuer.URL, Subject: "admin"}, CreatedAt: g.Now().UTC(), ExpiresAt: g.Now().Add(30 * time.Minute).UTC(), AuthTime: authTime.UTC()}
	g.AwaitLower(session.CreatedAt)
	issue := security.S1Command{Kind: security.S1IssueSession, Session: &session, SessionLineage: 1}
	sr, err := g.Prepare(ordinary, issue)
	if err != nil {
		t.Fatal("Code session prepare", err)
	}
	sh, err := g.Consume(sr, ordinary, [32]byte{})
	if err != nil {
		t.Fatal("Code -> H", err)
	}
	g.Apply(sh)
	g.Renew()
	req := httptest.NewRequest(http.MethodPost, "https://admin.example/private", nil)
	req.TLS = &tls.ConnectionState{}
	req.Header.Set("Origin", "https://admin.example")
	req.Header.Set("X-Lantern-CSRF", csrf)
	req.AddCookie(&http.Cookie{Name: "__Host-lantern-session", Value: cookie})
	req.AddCookie(&http.Cookie{Name: "__Host-lantern-csrf", Value: csrf})
	browser, err := runtime.Browser(req)
	if err != nil {
		t.Fatal(err)
	}
	cr, err := g.Prepare(browser, role)
	if err != nil {
		t.Fatal("native cookie", err)
	}
	if _, err := g.Consume(cr, browser, [32]byte{}); err != nil {
		t.Fatal("native cookie -> H", err)
	}
	req.Header.Set("X-Lantern-CSRF", "wrong")
	if _, err := runtime.Browser(req); err == nil {
		t.Fatal("wrong CSRF")
	}
	bob := security.Identity{Kind: security.OIDCPrincipal, Issuer: issuer.URL, Subject: "bob"}
	expand := security.S1Command{Kind: security.S1Management, Changes: []security.Change{{Kind: security.PutAssignment, Identity: &bob, RoleID: "security_admin"}}}
	pr, err := g.Prepare(bearer, expand)
	if err != nil {
		t.Fatal(err)
	}
	begin, err := g.Begin(pr, bearer)
	if err != nil {
		t.Fatal("full-S1 purpose begin", err)
	}
	g.AwaitLower(begin.NotBefore)
	g.Renew()
	id, err := g.Start(begin.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	approval, _ := code(id, false)
	g.Renew()
	approved, err := g.Complete(id, approval)
	if err != nil {
		t.Fatal("genuine operation Code", err)
	}
	g.AwaitLower(g.Now())
	g.Renew()
	ph, err := g.Consume(pr, bearer, approved.Proof)
	if err != nil {
		t.Fatal("full-S1 purpose -> H", err)
	}
	if _, err := g.Complete(id, approval); err == nil {
		t.Fatal("purpose replay")
	}
	g.Apply(ph)
	g.RestartAndRecover(h)
	g.LogEvidence(native)
	g.ExpiredOriginalAfterOriginLoss(shortH)
}
