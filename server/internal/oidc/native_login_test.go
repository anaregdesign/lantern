package oidc

import (
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/golang-jwt/jwt/v5"
)

func TestNativeGoogleRepeatedAndRestoredAuthentication(t *testing.T) {
	p := newNativeProviderFixture(t, security.GoogleIDToken)
	start, _ := p.begin(t)
	claims := p.claims(start)
	claims["at_hash"] = "present-without-requested-access-token"
	claims["nonce"] = "incidental-initialize-nonce"
	claims["auth_time"] = p.now().Add(-24 * time.Hour).Unix()
	raw := p.sign(t, claims)
	for range 3 {
		start, a := p.begin(t)
		if start.Nonce != "" || start.State != "" {
			t.Fatal("invented Google per-login nonce")
		}
		verified, err := p.verifier.VerifyNativeCredential(t.Context(), a, NativeInput{Provider: "google", IDToken: raw})
		if err != nil {
			t.Fatal(err)
		}
		completion, err := p.transactions.Consume(a, verified)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 { // current revalidation of the same completion does not consume again.
			identity, err := p.verifier.VerifyNativeCompletion(t.Context(), completion, NativeTokens{})
			if err != nil {
				t.Fatal(err)
			}
			e := identity.Evidence()
			if e.Native.NonceChecked || !e.AccessHashPresent || e.AccessHashChecked || e.AuthTime.Time() != p.now().Add(-24*time.Hour) || e.Identity.Subject != "existing-user" {
				t.Fatal("provider facts were relabeled")
			}
		}
		if _, err := p.transactions.Consume(a, verified); err == nil {
			t.Fatal("transaction replay accepted")
		}
	}
	if p.exchanges.Load() != 0 {
		t.Fatal("Google API authorization attempted")
	}
}

func TestNativeProviderTrustRejections(t *testing.T) {
	p := newNativeProviderFixture(t, security.GoogleIDToken)
	start, a := p.begin(t)
	for _, tc := range []struct {
		name   string
		mutate func(jwt.MapClaims)
	}{
		{"audience", func(c jwt.MapClaims) { c["aud"] = "another-client" }},
		{"multiple audiences", func(c jwt.MapClaims) { c["aud"] = []string{start.ClientID, "another"} }},
		{"bare issuer", func(c jwt.MapClaims) { c["iss"] = "accounts.google.com" }},
		{"Apple mixup", func(c jwt.MapClaims) { c["iss"] = security.AppleIssuer }},
		{"presenter", func(c jwt.MapClaims) { c["azp"] = "unregistered-client" }},
		{"expired", func(c jwt.MapClaims) { c["exp"] = p.now().Unix() }},
		{"missing iat", func(c jwt.MapClaims) { delete(c, "iat") }},
		{"null auth_time", func(c jwt.MapClaims) { c["auth_time"] = nil }},
		{"future auth_time", func(c jwt.MapClaims) { c["auth_time"] = p.now().Add(time.Minute).Unix() }},
		{"code hash on Google", func(c jwt.MapClaims) { c["c_hash"] = "unexpected" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := p.claims(start)
			tc.mutate(c)
			if _, err := p.verifier.VerifyNativeCredential(t.Context(), a, NativeInput{Provider: "google", IDToken: p.sign(t, c)}); err == nil {
				t.Fatal("invalid token accepted")
			}
		})
	}
	claims := p.claims(start)
	claims["azp"] = "android-client"
	if _, err := p.verifier.VerifyNativeCredential(t.Context(), a, NativeInput{Provider: "google", IDToken: p.sign(t, claims)}); err != nil {
		t.Fatal("registered presenter rejected", err)
	}
	if _, err := p.verifier.VerifyNativeCompletion(t.Context(), NativeCompletion{}, NativeTokens{}); err == nil {
		t.Fatal("zero completion accepted")
	}
}
