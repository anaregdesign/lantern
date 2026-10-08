package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"encoding/base64"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestVerifierLoginOptionalTokenHash(t *testing.T) {
	p := newTestProvider(t)
	v := NewVerifierWithClock(NewKeyCacheWithClock(p.fetcher, p.now), p.now)
	claims := p.accessClaims()
	claims["aud"], claims["nonce"], claims["auth_time"] = "admin", "nonce", p.now().Unix()
	digest := sha512.Sum512([]byte("access-token"))
	claims["at_hash"] = base64.RawURLEncoding.EncodeToString(digest[:32])
	if _, err := v.VerifyLogin(t.Context(), p.sign(t, claims, "JWT"), p.trust, "nonce", "access-token"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{"", nil, "wrong"} {
		claims["at_hash"] = value
		if _, err := v.VerifyLogin(t.Context(), p.sign(t, claims, "JWT"), p.trust, "nonce", "access-token"); err == nil {
			t.Fatal("invalid hash ignored", value)
		}
	}
	delete(claims, "at_hash")
	if _, err := v.VerifyLogin(t.Context(), p.sign(t, claims, "JWT"), p.trust, "nonce", ""); err != nil {
		t.Fatal("optional token hash required", err)
	}
}

func TestVerifierAccessProfile(t *testing.T) {
	p := newTestProvider(t)
	keys := NewKeyCache(p.fetcher)
	keys.now = p.now
	v := NewVerifier(keys)
	v.now = p.now
	raw := p.sign(t, p.accessClaims(), "at+jwt")
	identity, err := v.VerifyAccess(context.Background(), raw, p.trust)
	if err != nil || identity.Identity().Issuer != p.trust.Issuer.URL || identity.Identity().Subject != "user" {
		t.Fatalf("valid RFC 9068 token: %v", err)
	}
	if !identity.AuthTime().IsZero() {
		t.Fatal("issuance time used as authentication time")
	}
	recentClaims := p.accessClaims()
	recentClaims["auth_time"] = p.now().Add(-time.Minute).Unix()
	recent, err := v.VerifyAccess(context.Background(), p.sign(t, recentClaims, "at+jwt"), p.trust)
	if err != nil || recent.AuthTime().Unix() != p.now().Add(-time.Minute).Unix() {
		t.Fatal("verified auth_time lost", err)
	}
	for name, mutate := range map[string]func(jwt.MapClaims){
		"expired":            func(c jwt.MapClaims) { c["exp"] = p.now().Add(-time.Second).Unix() },
		"wrong audience":     func(c jwt.MapClaims) { c["aud"] = "admin" },
		"wrong Issuer":       func(c jwt.MapClaims) { c["iss"] = "https://unknown.example" },
		"missing iat":        func(c jwt.MapClaims) { delete(c, "iat") },
		"future auth time":   func(c jwt.MapClaims) { c["auth_time"] = p.now().Add(time.Second).Unix() },
		"future iat":         func(c jwt.MapClaims) { c["iat"] = p.now().Add(time.Minute).Unix() },
		"future nbf":         func(c jwt.MapClaims) { c["nbf"] = p.now().Add(time.Minute).Unix() },
		"missing client ID":  func(c jwt.MapClaims) { delete(c, "client_id") },
		"missing jti":        func(c jwt.MapClaims) { delete(c, "jti") },
		"missing subject":    func(c jwt.MapClaims) { delete(c, "sub") },
		"non-ASCII subject":  func(c jwt.MapClaims) { c["sub"] = "ユーザ" },
		"oversized subject":  func(c jwt.MapClaims) { c["sub"] = strings.Repeat("x", 256) },
		"excessive lifetime": func(c jwt.MapClaims) { c["exp"] = p.now().Add(25 * time.Hour).Unix() },
	} {
		t.Run(name, func(t *testing.T) {
			claims := p.accessClaims()
			mutate(claims)
			if _, err := v.VerifyAccess(context.Background(), p.sign(t, claims, "at+jwt"), p.trust); err == nil {
				t.Fatal("invalid profile accepted")
			}
		})
	}
	for _, typ := range []string{"JWT", "", "id+jwt"} {
		if _, err := v.VerifyAccess(context.Background(), p.sign(t, p.accessClaims(), typ), p.trust); err == nil {
			t.Fatal("ID token accepted as API token")
		}
	}
	calls := p.discovery.Load()
	trust := p.trust
	trust.Issuer.URL = "https://unregistered.example"
	if _, err := v.VerifyAccess(context.Background(), raw, trust); err == nil || p.discovery.Load() != calls {
		t.Fatal("Issuer mismatch fetched metadata")
	}
}

func TestVerifierIDProfileAndNonce(t *testing.T) {
	p := newTestProvider(t)
	v := NewVerifier(NewKeyCache(p.fetcher))
	v.now = p.now
	claims := p.accessClaims()
	claims["aud"], claims["nonce"], claims["auth_time"] = "admin", "expected", p.now().Unix()
	raw := p.sign(t, claims, "JWT")
	if _, err := v.VerifyID(context.Background(), raw, p.trust, "expected"); err != nil {
		t.Fatal(err)
	}
	for _, nonce := range []string{"", "wrong"} {
		if _, err := v.VerifyID(context.Background(), raw, p.trust, nonce); err == nil {
			t.Fatal("wrong nonce accepted")
		}
	}
	claims["aud"] = []string{"admin", "other"}
	if _, err := v.VerifyID(context.Background(), p.sign(t, claims, "JWT"), p.trust, "expected"); err == nil {
		t.Fatal("multiple audiences without azp accepted")
	}
	claims["azp"] = "admin"
	if _, err := v.VerifyID(context.Background(), p.sign(t, claims, "JWT"), p.trust, "expected"); err != nil {
		t.Fatal(err)
	}
	delete(claims, "auth_time")
	identity, err := v.VerifyID(context.Background(), p.sign(t, claims, "JWT"), p.trust, "expected")
	if err != nil || !identity.AuthTime().IsZero() {
		t.Fatal("ordinary login lost unknown authentication time", err)
	}
	claims["auth_time"] = p.now().Add(-time.Hour).Unix()
	identity, err = v.VerifyID(context.Background(), p.sign(t, claims, "JWT"), p.trust, "expected")
	if err != nil || identity.AuthTime().Unix() != p.now().Add(-time.Hour).Unix() {
		t.Fatal("old signed authentication time was refreshed", err)
	}
	for _, value := range []any{nil, "not numeric", p.now().Add(time.Second).Unix(), time.Time{}.Unix()} {
		claims["auth_time"] = value
		if _, err := v.VerifyID(context.Background(), p.sign(t, claims, "JWT"), p.trust, "expected"); err == nil {
			t.Fatal("invalid authentication evidence accepted", value)
		}
	}
	claims["iat"] = p.now().Add(-2 * time.Minute).Unix()
	claims["auth_time"] = p.now().Add(-time.Minute).Unix()
	if _, err := v.VerifyID(context.Background(), p.sign(t, claims, "JWT"), p.trust, "expected"); err == nil {
		t.Fatal("authentication after issuance accepted")
	}
}

func TestTokenSelectorAmbiguityAndHeaderURLs(t *testing.T) {
	p := newTestProvider(t)
	parts := strings.Split(p.sign(t, p.accessClaims(), "at+jwt"), ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://a.example","iss":"https://b.example"}`))
	if _, err := TokenIssuer(strings.Join(parts, ".")); err == nil {
		t.Fatal("duplicate Issuer selected")
	}
	parts = strings.Split(p.sign(t, p.accessClaims(), "at+jwt"), ".")
	for _, header := range []string{`{"alg":"EdDSA","kid":"key","jku":""}`, `{"alg":"EdDSA","kid":"key","b64":null}`, `{"alg":"EdDSA","kid":"key","crit":[]}`, `{"alg":"EdDSA","kid":"key","jwk":null}`, `{"alg":"EdDSA","kid":"key","jku":"https://attacker.example"}`, `{"alg":"EdDSA","kid":"key","crit":["custom"]}`, `{"alg":"EdDSA","kid":"key","b64":false}`, `{"alg":"none","kid":"key","alg":"EdDSA"}`} {
		parts[0] = base64.RawURLEncoding.EncodeToString([]byte(header))
		if _, err := TokenIssuer(strings.Join(parts, ".")); err == nil {
			t.Fatal("ambiguous or token-selected trust header accepted")
		}
	}
	for _, token := range []string{"opaque", "a.b.c.d", strings.Repeat("x", maxTokenBytes+1)} {
		if _, err := TokenIssuer(token); err == nil {
			t.Fatal("unbounded or opaque token selected Issuer")
		}
	}
}

func TestVerifierPS256RequiresJOSESaltLength(t *testing.T) {
	provider := newTestProvider(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.document = map[string]any{"keys": []jsonKey{{Kind: "RSA", ID: "key", Algorithm: "PS256", N: base64.RawURLEncoding.EncodeToString(key.N.Bytes()), E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}}
	provider.mu.Unlock()
	trust := provider.trust
	trust.Issuer.Algorithms = []string{"PS256"}
	verifier := NewVerifier(NewKeyCache(provider.fetcher))
	verifier.now = provider.now
	for _, salt := range []int{rsa.PSSSaltLengthEqualsHash, 1, rsa.PSSSaltLengthAuto} {
		method := *jwt.SigningMethodPS256
		method.Options = &rsa.PSSOptions{SaltLength: salt}
		token := jwt.NewWithClaims(&method, provider.accessClaims())
		token.Header["kid"], token.Header["typ"] = "key", "at+jwt"
		raw, err := token.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		_, err = verifier.VerifyAccess(t.Context(), raw, trust)
		if (err == nil) != (salt == rsa.PSSSaltLengthEqualsHash) {
			t.Fatal("non-JOSE salt accepted or valid signature denied", salt, err)
		}
	}
	if jwt.SigningMethodPS256.VerifyOptions.SaltLength != rsa.PSSSaltLengthAuto {
		t.Fatal("global library method mutated")
	}
}
