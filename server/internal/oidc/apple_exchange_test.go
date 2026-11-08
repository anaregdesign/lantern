package oidc

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestAppleNativeAndWebCompletion(t *testing.T) {
	for _, kind := range []string{security.AppleNative, security.AppleAndroidWeb} {
		t.Run(kind, func(t *testing.T) {
			p := newNativeProviderFixture(t, kind)
			if err := p.fetcher.ValidateIssuer(t.Context(), p.trust.Issuer, p.secrets); err != nil {
				t.Fatal("native-only issuer required browser S256", err)
			}
			start, a := p.begin(t)
			code := "one-time-provider-code"
			claims := p.claims(start)
			hash := sha256.Sum256([]byte(code))
			claims["c_hash"] = base64.RawURLEncoding.EncodeToString(hash[:16])
			claims["auth_time"] = p.now().Add(-48 * time.Hour).Unix()
			original := p.sign(t, claims)
			input := NativeInput{Provider: "apple", IDToken: original, AuthorizationCode: code, State: start.State}
			for _, mutate := range []func(*NativeInput){func(i *NativeInput) { i.State = "wrong" }, func(i *NativeInput) { i.AuthorizationCode = "other" }, func(i *NativeInput) { i.Provider = "google" }} {
				bad := input
				mutate(&bad)
				if _, err := p.verifier.VerifyNativeCredential(t.Context(), a, bad); err == nil {
					t.Fatal("state/provider/code mixup accepted")
				}
			}
			wrongNonce := p.claims(start)
			wrongNonce["nonce"] = "sha256-or-other-representation"
			bad := input
			bad.IDToken = p.sign(t, wrongNonce)
			if _, err := p.verifier.VerifyNativeCredential(t.Context(), a, bad); err == nil {
				t.Fatal("wrong nonce accepted")
			}
			verified, err := p.verifier.VerifyNativeCredential(t.Context(), a, input)
			if err != nil {
				t.Fatal(err)
			}
			if kind == security.AppleAndroidWeb {
				if _, err := p.transactions.Consume(a, verified); err == nil {
					t.Fatal("unstaged web callback accepted")
				}
				callback, err := p.transactions.CallbackAttempt("mobile", start.State)
				if err != nil {
					t.Fatal(err)
				}
				if callback != a {
					t.Fatal("callback selected different transaction")
				}
				if pkg, err := p.transactions.StageCallback(callback, verified); err != nil || pkg != "com.example.app" {
					t.Fatal("callback staging", pkg, err)
				}
				if _, err := p.transactions.StageCallback(callback, verified); err == nil {
					t.Fatal("duplicate callback accepted")
				}
			}
			completion, err := p.transactions.Consume(a, verified)
			if err != nil {
				t.Fatal(err)
			}
			access := "exchange-access-token"
			exchangeClaims := p.claims(start)
			accessHash := sha256.Sum256([]byte(access))
			exchangeClaims["at_hash"] = base64.RawURLEncoding.EncodeToString(accessHash[:16])
			exchangeClaims["auth_time"] = p.now().Add(-time.Minute).Unix()
			exchanged := p.sign(t, exchangeClaims)
			p.mu.Lock()
			p.exchange = func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
					return
				}
				if r.Method != http.MethodPost || r.Header.Get("Authorization") != "" || r.Form.Get("client_secret") != "fixture-server-private-client-secret" || r.Form.Get("client_id") != start.ClientID || r.Form.Get("code") != code || r.Form.Get("code_verifier") != "" {
					t.Error("Apple exchange credential contract")
				}
				redirect, present := r.Form["redirect_uri"]
				if kind == security.AppleNative && present || kind == security.AppleAndroidWeb && (len(redirect) != 1 || redirect[0] != start.RedirectURI) {
					t.Error("incorrect native/web redirect semantics")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id_token": exchanged, "access_token": access, "token_type": "Bearer", "refresh_token": "discard-this-refresh-token"})
			}
			p.mu.Unlock()
			tokens, err := p.fetcher.ExchangeApple(t.Context(), completion, p.secrets)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				result, err := p.verifier.VerifyNativeCompletion(t.Context(), completion, tokens)
				if err != nil {
					t.Fatal(err)
				}
				e := result.Evidence()
				if !e.Native.NonceChecked || !e.Native.CodeHashChecked || !e.Native.ExchangeAccessHashChecked || e.AuthTime.Time() != p.now().Add(-48*time.Hour) || e.Native.ExchangeAuthTime.Time() != p.now().Add(-time.Minute) {
					t.Fatal("original/exchange evidence conflated")
				}
			}
			if _, err := p.fetcher.ExchangeApple(t.Context(), completion, p.secrets); err == nil {
				t.Fatal("code exchange repeated")
			}
			if p.exchanges.Load() != 1 {
				t.Fatal("more than one provider exchange", p.exchanges.Load())
			}
			if _, err := p.transactions.Consume(a, verified); err == nil {
				t.Fatal("consumed completion replay")
			}
		})
	}
}

func TestAppleExchangeResponseLossIsNotRetried(t *testing.T) {
	p := newNativeProviderFixture(t, security.AppleNative)
	start, a := p.begin(t)
	c, err := p.verifier.VerifyNativeCredential(t.Context(), a, NativeInput{Provider: "apple", IDToken: p.sign(t, p.claims(start)), AuthorizationCode: "one-time-code", State: start.State})
	if err != nil {
		t.Fatal(err)
	}
	completion, err := p.transactions.Consume(a, c)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.exchange = func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}
	p.mu.Unlock()
	if _, err := p.fetcher.ExchangeApple(t.Context(), completion, p.secrets); err == nil {
		t.Fatal("lost response accepted")
	}
	if _, err := p.fetcher.ExchangeApple(t.Context(), completion, p.secrets); err == nil {
		t.Fatal("uncertain exchange retried")
	}
	if p.exchanges.Load() != 1 {
		t.Fatal("provider received code twice", p.exchanges.Load())
	}
}

func TestAppleExchangeResponseCleanupPreservesSingleDispatch(t *testing.T) {
	p := newNativeProviderFixture(t, security.AppleNative)
	privateClose := errors.New("private provider cleanup detail")
	for _, tc := range []struct {
		name, body string
		closeErr   error
		primary    error
	}{
		{"success", `{"id_token":"fixture-id","access_token":"fixture-access","token_type":"Bearer"}`, nil, nil},
		{"close failure", `{"id_token":"fixture-id","access_token":"fixture-access","token_type":"Bearer"}`, privateClose, nil},
		{"primary failure", `{`, nil, ErrInvalidToken},
		{"primary and close failure", `{`, privateClose, ErrInvalidToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, attempt := p.begin(t)
			credential, err := p.verifier.VerifyNativeCredential(t.Context(), attempt, NativeInput{Provider: "apple", IDToken: p.sign(t, p.claims(start)), AuthorizationCode: "one-time-code", State: start.State})
			if err != nil {
				t.Fatal(err)
			}
			completion, err := p.transactions.Consume(attempt, credential)
			if err != nil {
				t.Fatal(err)
			}
			fetcher, transport := newCloseFaultFetcher(tc.body, tc.closeErr)
			tokens, err := fetcher.ExchangeApple(t.Context(), completion, p.secrets)
			if tc.primary == nil && tc.closeErr == nil {
				if err != nil || tokens.idToken != "fixture-id" || tokens.accessToken != "fixture-access" {
					t.Fatal("complete exchange was rejected")
				}
			} else {
				if err == nil || tokens != (NativeTokens{}) || tc.primary != nil && !errors.Is(err, tc.primary) {
					t.Fatal("failed exchange returned tokens or lost its primary error")
				}
				if tc.closeErr == nil && err != tc.primary {
					t.Fatal("successful cleanup changed primary error identity")
				}
				if tc.closeErr != nil && (!errors.Is(err, ErrFetch) || tc.primary == nil && err != ErrFetch || strings.Contains(err.Error(), privateClose.Error())) {
					t.Fatal("cleanup failure was lost or exposed private details")
				}
			}
			if _, err := fetcher.ExchangeApple(t.Context(), completion, p.secrets); !errors.Is(err, ErrInvalidToken) {
				t.Fatal("possibly dispatched Apple exchange was retried")
			}
			if transport.calls != 1 || transport.body.closes != 1 {
				t.Fatal("response cleanup caused another dispatch or Close")
			}
		})
	}
}

// The original and exchanged tokens must name the exact same client, subject
// and raw nonce. All tokens here carry valid fixture signatures.
func TestAppleExchangeIdentityAndProfileMixups(t *testing.T) {
	for _, kind := range []string{security.AppleNative, security.AppleAndroidWeb} {
		t.Run(kind, func(t *testing.T) {
			p := newNativeProviderFixture(t, kind)
			other := p.trust.Issuer.NativeClients[0]
			other.ID, other.ClientID = "other", "com.example.other-profile"
			if kind == security.AppleNative {
				other.Kind, other.AndroidPackage = security.AppleAndroidWeb, "com.example.app"
				other.RedirectURI = other.ServiceOrigin + "/auth/native/apple/callback/other"
			} else {
				other.Kind, other.AndroidPackage, other.RedirectURI = security.AppleNative, "", ""
			}
			p.trust.Issuer.NativeClients = append(p.trust.Issuer.NativeClients, other)
			for _, claim := range []string{"sub", "aud", "nonce"} {
				t.Run(claim, func(t *testing.T) {
					start, a := p.begin(t)
					c, err := p.verifier.VerifyNativeCredential(t.Context(), a, NativeInput{Provider: "apple", IDToken: p.sign(t, p.claims(start)), AuthorizationCode: "one-code", State: start.State})
					if err != nil {
						t.Fatal(err)
					}
					if kind == security.AppleAndroidWeb {
						if _, err := p.transactions.StageCallback(a, c); err != nil {
							t.Fatal(err)
						}
					}
					completion, err := p.transactions.Consume(a, c)
					if err != nil {
						t.Fatal(err)
					}
					changed := p.claims(start)
					changed[claim] = "another-" + claim
					raw := p.sign(t, changed)
					p.mu.Lock()
					p.exchange = func(w http.ResponseWriter, _ *http.Request) {
						_ = json.NewEncoder(w).Encode(map[string]any{"id_token": raw, "access_token": "access", "token_type": "Bearer"})
					}
					p.mu.Unlock()
					before := p.exchanges.Load()
					tokens, err := p.fetcher.ExchangeApple(t.Context(), completion, p.secrets)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := p.verifier.VerifyNativeCompletion(t.Context(), completion, tokens); err == nil {
						t.Fatal("exchange mixup accepted")
					}
					if _, err := p.fetcher.ExchangeApple(t.Context(), completion, p.secrets); err == nil || p.exchanges.Load() != before+1 {
						t.Fatal("rejected exchange retried", err)
					}
				})
			}
			start, a := p.begin(t)
			claims := p.claims(start)
			// Distinct registered Bundle and Services IDs are never interchangeable.
			claims["aud"] = "com.example.other-profile"
			if _, err := p.verifier.VerifyNativeCredential(t.Context(), a, NativeInput{Provider: "apple", IDToken: p.sign(t, claims), AuthorizationCode: "code", State: start.State}); err == nil {
				t.Fatal("Bundle/Services ID mixup accepted")
			}
		})
	}
}
