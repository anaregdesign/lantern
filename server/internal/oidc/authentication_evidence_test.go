package oidc

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestAuthenticationEvidenceSignedAccessFacts(t *testing.T) {
	p := newTestProvider(t)
	verifier := NewVerifierWithClock(NewKeyCacheWithClock(p.fetcher, p.now), p.now)
	for _, tc := range []struct {
		name    string
		present bool
		nbf     any
	}{
		{"absent", false, nil}, {"null", true, nil}, {"explicit", true, p.now().Add(-time.Minute).Unix()}, {"leeway", true, p.now().Add(20 * time.Second).Unix()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := p.accessClaims()
			if tc.present {
				claims["nbf"] = tc.nbf
			}
			claims["auth_time"] = p.now().Add(-time.Hour).Unix()
			if tc.name == "leeway" {
				claims["iat"] = p.now().Add(20 * time.Second).Unix()
			}
			raw := p.sign(t, claims, "at+jwt")
			verified, err := verifier.VerifyAccess(t.Context(), raw, p.trust)
			if err != nil {
				t.Fatal(err)
			}
			e := verified.Evidence()
			key, _ := x509.MarshalPKIXPublicKey(p.private.Public())
			if e.Mode != "access" || e.Profile != "rfc9068" || e.Identity != verified.Identity() || e.IssuedAt.Time().Unix() != claims["iat"] || e.NotBefore.Present != tc.present || e.NotBefore.Numeric != (tc.nbf != nil) || e.ExpiresAt.Time() != p.now().Add(time.Hour) || e.AuthTime.Time() != p.now().Add(-time.Hour) || e.Credential != evidenceDigest("credential", raw) || e.Key != evidenceDigest("key-spki", key) || e.Configuration != evidenceDigest("trust", p.trust) || e.ConfigRevision != 1 || e.Generation != p.trust.Generation {
				t.Fatalf("lost signed facts: %+v", e)
			}
			before, _ := e.Commitment()
			e.KeyID = "changed"
			e.Code.Flow = "operation"
			after, _ := verified.Evidence().Commitment()
			if before != after {
				t.Fatal("returned copy mutated retained evidence")
			}
			encoded, _ := json.Marshal(verified)
			if string(encoded) != "{}" {
				t.Fatal("opaque producer exported facts")
			}
			var decoded VerifiedIdentity
			if json.Unmarshal([]byte(`{"Identity":{"Subject":"forged"},"evidence":{"Mode":"code"}}`), &decoded) != nil || decoded != (VerifiedIdentity{}) {
				t.Fatal("JSON minted verifier evidence")
			}
		})
	}
	claims := p.accessClaims()
	absent, err := verifier.VerifyAccess(t.Context(), p.sign(t, claims, "at+jwt"), p.trust)
	if err != nil || absent.Evidence().AuthTime.Present || !absent.AuthTime().IsZero() {
		t.Fatal("missing auth_time fabricated", err)
	}
	// A signed future iat/nbf within parser leeway remains a fact, never a strict
	// consume guarantee. Expiry equality retains the existing strict refusal.
	for _, mutate := range []func(){func() { claims["exp"] = p.now().Unix(); claims["iat"] = p.now().Add(-time.Hour).Unix() }, func() { claims = p.accessClaims(); claims["aud"] = "wrong" }, func() { claims = p.accessClaims(); claims["auth_time"] = p.now().Add(time.Second).Unix() }} {
		mutate()
		result, err := verifier.VerifyAccess(t.Context(), p.sign(t, claims, "at+jwt"), p.trust)
		if err == nil || result != (VerifiedIdentity{}) {
			t.Fatal("invalid credential retained evidence")
		}
	}
	// The same kid with a different actual key has a different commitment.
	original := absent.Evidence().Key
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	p.mu.Lock()
	p.private = private
	p.document = map[string]any{"keys": []jsonKey{{Kind: "OKP", ID: "key", Algorithm: "EdDSA", Curve: "Ed25519", X: base64.RawURLEncoding.EncodeToString(public)}}}
	p.mu.Unlock()
	second := NewVerifierWithClock(NewKeyCacheWithClock(p.fetcher, p.now), p.now)
	rotated, err := second.VerifyAccess(t.Context(), p.sign(t, p.accessClaims(), "at+jwt"), p.trust)
	if err != nil || rotated.Evidence().Key == original {
		t.Fatal("kid substituted for actual key", err)
	}
	old := rotated.Evidence().Configuration
	trust := p.trust
	trust.ConfigRevision++
	changed, err := second.VerifyAccess(t.Context(), p.sign(t, p.accessClaims(), "at+jwt"), trust)
	if err != nil || changed.Evidence().Configuration == old {
		t.Fatal("configuration identity lost", err)
	}
	trust.Issuer.Algorithms[0] = "invalid"
	if changed.Evidence().Algorithm != "EdDSA" || rotated.Evidence().Configuration != old {
		t.Fatal("source mutation changed evidence")
	}
}

func TestAuthenticationEvidenceMaximumSignedToken(t *testing.T) {
	p := newTestProvider(t)
	claims := p.accessClaims()
	claims["sub"] = strings.Repeat("\x01", 255)
	claims["client_id"] = strings.Repeat("c", 512)
	claims["jti"] = strings.Repeat("j", 512)
	// Unknown signed extension claims are not copied into the bounded envelope.
	claims["extension"] = strings.Repeat("s", 10000)
	raw := p.sign(t, claims, "at+jwt")
	for len(raw) > maxTokenBytes {
		claims["extension"] = claims["extension"].(string)[:len(claims["extension"].(string))-1]
		raw = p.sign(t, claims, "at+jwt")
	}
	if len(raw) < maxTokenBytes-4 {
		t.Fatal("fixture does not reach input cap", len(raw))
	}
	verified, err := NewVerifierWithClock(NewKeyCacheWithClock(p.fetcher, p.now), p.now).VerifyAccess(t.Context(), raw, p.trust)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(verified.Evidence())
	if len(encoded) > security.MaxAuthenticationEvidenceBytes || strings.Contains(string(encoded), raw) || strings.Contains(string(encoded), strings.Repeat("s", 100)) {
		t.Fatal("unbounded or secret-bearing record")
	}
}

func TestAuthenticationEvidenceGenuineCodeCompletion(t *testing.T) {
	for _, flow := range []string{"login", "step-up", "operation"} {
		t.Run(flow, func(t *testing.T) {
			p := newTestProvider(t)
			p.trust.Issuer.RedirectURI = "https://admin.example" + CallbackPath(p.trust.Issuer.URL)
			discovery, err := p.fetcher.Discover(t.Context(), p.trust.Issuer)
			if err != nil {
				t.Fatal(err)
			}
			verifier := NewVerifierWithClock(NewKeyCacheWithClock(p.fetcher, p.now), p.now)
			if _, err := verifier.VerifyAccess(t.Context(), p.sign(t, p.accessClaims(), "at+jwt"), p.trust); err != nil {
				t.Fatal(err)
			}
			manager, _ := NewLoginTransactionsWithClock("https://admin.example", []string{"/", "/security/roles"}, p.now)
			var start LoginStart
			if flow == "operation" {
				start, err = manager.BeginAuthorization(p.trust, discovery, [32]byte{9})
			} else {
				start, err = manager.Begin(p.trust, discovery, "/", "", flow == "step-up")
			}
			if err != nil {
				t.Fatal(err)
			}
			redirect, _ := url.Parse(start.AuthorizationURL)
			completion, err := manager.Consume(redirect.Query().Get("state"), start.TransactionCookie, CallbackPath(p.trust.Issuer.URL), p.trust.Issuer.URL)
			if err != nil {
				t.Fatal(err)
			}
			trustCopy := completion.Trust()
			trustCopy.Issuer.Algorithms[0] = "invalid"
			discoveryCopy := completion.Discovery()
			discoveryCopy.ResponseTypes[0] = "invalid"
			if completion.Trust().Issuer.Algorithms[0] != "EdDSA" || completion.Discovery().ResponseTypes[0] != "code" {
				t.Fatal("completion getters alias retained configuration")
			}
			claims := p.accessClaims()
			claims["aud"] = "admin"
			claims["nonce"] = completion.Nonce()
			claims["auth_time"] = p.now().Add(-time.Hour).Unix()
			access := "co-returned-secret"
			hash := sha512.Sum512([]byte(access))
			claims["at_hash"] = base64.RawURLEncoding.EncodeToString(hash[:32])
			raw := p.sign(t, claims, "JWT")
			p.mu.Lock()
			p.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/token" {
					t.Error("unexpected second key fetch")
				}
				if r.ParseForm() != nil || r.Form.Get("code_verifier") != completion.Verifier() || r.Form.Get("code") != "authorization-secret" {
					t.Error("PKCE exchange mismatch")
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"id_token": raw, "access_token": access, "token_type": "Bearer"})
			})
			p.mu.Unlock()
			tokens, err := p.fetcher.ExchangeCode(t.Context(), completion.Trust(), completion.Discovery(), nil, "authorization-secret", completion.Verifier())
			if err != nil {
				t.Fatal(err)
			}
			bare, err := verifier.VerifyID(t.Context(), raw, p.trust, completion.Nonce())
			if err != nil || bare.Evidence().Mode != "id" || bare.Evidence().Code.Flow != "" || bare.Evidence().AccessHashChecked {
				t.Fatal("bare ID promoted to Code")
			}
			checked, err := verifier.VerifyLogin(t.Context(), raw, p.trust, completion.Nonce(), access)
			if err != nil || checked.Evidence().Mode != "login" || checked.Evidence().Code.Flow != "" {
				t.Fatal("raw token check promoted to Code")
			}
			verified, err := verifier.VerifyLoginCompletion(t.Context(), completion, tokens)
			if err != nil {
				t.Fatal(err)
			}
			e := verified.Evidence()
			if e.Mode != "code" || e.Code.Flow != flow || !e.AccessHashPresent || !e.AccessHashChecked || e.AuthTime.Time() != p.now().Add(-time.Hour) || e.Code.CreatedAt != p.now() || e.Code.ConsumedAt != p.now() {
				t.Fatalf("incomplete Code facts: %+v", e)
			}
			encoded, _ := json.Marshal(e)
			for _, secret := range []string{raw, access, "authorization-secret", completion.Verifier(), completion.Nonce(), start.TransactionCookie, redirect.Query().Get("state")} {
				if strings.Contains(string(encoded), secret) {
					t.Fatal("secret retained")
				}
			}
			for _, bad := range []struct {
				completion LoginCompletion
				tokens     CodeTokens
			}{{LoginCompletion{}, tokens}, {completion, CodeTokens{}}, {func() LoginCompletion { c := completion; c.transaction.verifier = "different"; return c }(), tokens}} {
				v, err := verifier.VerifyLoginCompletion(t.Context(), bad.completion, bad.tokens)
				if err == nil || v != (VerifiedIdentity{}) {
					t.Fatal("incomplete exchange minted Code evidence")
				}
			}
			for _, field := range []string{"nonce", "at_hash"} {
				old := claims[field]
				claims[field] = "wrong"
				p.mu.Lock()
				raw = p.sign(t, claims, "JWT")
				p.mu.Unlock()
				badTokens, err := p.fetcher.ExchangeCode(t.Context(), completion.Trust(), completion.Discovery(), nil, "authorization-secret", completion.Verifier())
				if err != nil {
					t.Fatal(err)
				}
				v, err := verifier.VerifyLoginCompletion(t.Context(), completion, badTokens)
				if err == nil || v != (VerifiedIdentity{}) {
					t.Fatal("invalid additional verification minted evidence")
				}
				claims[field] = old
			}
			p.mu.Lock()
			p.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "failed", 500) })
			p.mu.Unlock()
			failed, err := p.fetcher.ExchangeCode(t.Context(), completion.Trust(), completion.Discovery(), nil, "authorization-secret", completion.Verifier())
			if err == nil || failed != (CodeTokens{}) {
				t.Fatal("failed exchange minted producer result")
			}
		})
	}
}
