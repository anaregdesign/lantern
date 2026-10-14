package oidc

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/golang-jwt/jwt/v5"
)

func TestNativeRegistrationPurposeIsolation(t *testing.T) {
	for _, kind := range []string{security.GoogleIDToken, security.AppleNative, security.AppleAndroidWeb} {
		t.Run(kind, func(t *testing.T) {
			p := newNativeProviderFixture(t, kind)
			for _, purpose := range []security.NativeRegistrationPurpose{{Kind: "registration"}, {Kind: "registration-recovery", Original: [32]byte{9}}} {
				t.Run(purpose.Kind, func(t *testing.T) {
					start, err := p.transactions.BeginRegistration(p.trust, p.profile, "mobile", p.discovery, purpose)
					if err != nil {
						t.Fatal(err)
					}
					a, err := p.transactions.Lookup(start.AttemptID, start.CompletionSecret, start.Affinity)
					if err != nil || a.IsLogin() || a.RegistrationPurpose() != purpose {
						t.Fatal("attempt purpose lost", err)
					}
					claims := p.claims(start)
					claims["auth_time"] = p.now().Add(-48 * time.Hour).Unix()
					raw := p.sign(t, claims)
					input := NativeInput{Provider: "google", IDToken: raw}
					if kind != security.GoogleIDToken {
						input.Provider, input.State, input.AuthorizationCode = "apple", start.State, "single-code"
					}
					credential, err := p.verifier.VerifyNativeCredential(t.Context(), a, input)
					if err != nil {
						t.Fatal(err)
					}
					if kind == security.AppleAndroidWeb {
						callback, err := p.transactions.CallbackAttempt("mobile", start.State)
						if err != nil || callback != a || callback.RegistrationPurpose() != purpose {
							t.Fatal("callback lost purpose", err)
						}
						if _, err := p.transactions.StageCallback(callback, credential); err != nil {
							t.Fatal(err)
						}
					}
					completion, err := p.transactions.Consume(a, credential)
					if err != nil || completion.IsLogin() || completion.RegistrationPurpose() != purpose {
						t.Fatal("completion lost purpose", err)
					}
					var tokens NativeTokens
					if kind != security.GoogleIDToken {
						p.mu.Lock()
						p.exchange = func(w http.ResponseWriter, _ *http.Request) {
							_ = json.NewEncoder(w).Encode(map[string]any{"id_token": raw, "token_type": "Bearer"})
						}
						p.mu.Unlock()
						tokens, err = p.fetcher.ExchangeApple(t.Context(), completion, p.secrets)
						if err != nil {
							t.Fatal(err)
						}
					}
					if _, err := p.verifier.VerifyNativeCompletion(t.Context(), completion, tokens); err == nil {
						t.Fatal("registration completion entered ordinary login")
					}
					for range 2 {
						verified, err := p.verifier.VerifyNativeRegistrationCompletion(t.Context(), completion, tokens)
						if err != nil {
							t.Fatal(err)
						}
						e := verified.Evidence()
						if e.Version != 3 || e.Mode != "native-registration" || e.Native.Registration != purpose || e.AuthTime.Time() != p.now().Add(-48*time.Hour) {
							t.Fatal("purpose or signed facts were relabeled")
						}
					}
					if kind != security.GoogleIDToken {
						before := p.exchanges.Load()
						if _, err := p.fetcher.ExchangeApple(t.Context(), completion, p.secrets); err == nil || p.exchanges.Load() != before {
							t.Fatal("registration code exchanged twice", err)
						}
					}
					if _, err := p.transactions.Consume(a, credential); err == nil {
						t.Fatal("registration completion consumed twice")
					}
				})
			}
			start, a := p.begin(t)
			input := NativeInput{Provider: "google", IDToken: p.sign(t, p.claims(start))}
			if kind != security.GoogleIDToken {
				input.Provider, input.State, input.AuthorizationCode = "apple", start.State, "code"
			}
			credential, err := p.verifier.VerifyNativeCredential(t.Context(), a, input)
			if err != nil {
				t.Fatal(err)
			}
			if kind == security.AppleAndroidWeb {
				if _, err := p.transactions.StageCallback(a, credential); err != nil {
					t.Fatal(err)
				}
			}
			completion, err := p.transactions.Consume(a, credential)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.verifier.VerifyNativeRegistrationCompletion(t.Context(), completion, NativeTokens{}); err == nil {
				t.Fatal("ordinary login became registration")
			}
		})
	}
}

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

func TestNativePresenterPresence(t *testing.T) {
	for _, kind := range []string{security.GoogleIDToken, security.AppleNative, security.AppleAndroidWeb} {
		t.Run(kind, func(t *testing.T) {
			p := newNativeProviderFixture(t, kind)
			start, a := p.begin(t)
			for _, tc := range []struct {
				name    string
				present bool
				value   any
				valid   bool
			}{
				{"absent", false, nil, true}, {"empty", true, "", false}, {"null", true, nil, false}, {"other", true, "other", false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					claims := p.claims(start)
					if tc.present {
						claims["azp"] = tc.value
					}
					input := NativeInput{Provider: "google", IDToken: p.sign(t, claims)}
					if kind != security.GoogleIDToken {
						input.Provider, input.AuthorizationCode, input.State = "apple", "code", start.State
					}
					_, err := p.verifier.VerifyNativeCredential(t.Context(), a, input)
					if (err == nil) != tc.valid {
						t.Fatal("presenter presence contract", err)
					}
				})
			}
		})
	}
}
