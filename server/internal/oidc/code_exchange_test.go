package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodeExchangeBoundSecretAndPKCE(t *testing.T) {
	for _, source := range []string{"file", "inline"} {
		t.Run(source, func(t *testing.T) {
			p := newTestProvider(t)
			binding := SecretBinding{Issuer: p.trust.Issuer.URL, ClientID: "admin", TokenOrigin: p.server.URL}
			if source == "file" {
				binding.Path = filepath.Join(t.TempDir(), "secret")
				if err := os.WriteFile(binding.Path, []byte("secret:&+"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				binding.Value = "secret:&+"
			}
			registry, err := NewSecretRegistry(map[string]SecretBinding{"admin": binding})
			if err != nil {
				t.Fatal(err)
			}
			trust := p.trust
			trust.Issuer.SecretRef = "admin"
			pkce := strings.Repeat("a", 43)
			id := p.sign(t, p.accessClaims(), "JWT")
			calls := 0
			p.mu.Lock()
			p.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				user, password, ok := r.BasicAuth()
				if !ok || user != url.QueryEscape("admin") || password != url.QueryEscape("secret:&+") ||
					r.Method != http.MethodPost || r.ParseForm() != nil || r.Form.Get("code") != "code" ||
					r.Form.Get("code_verifier") != pkce || r.Form.Get("redirect_uri") != trust.Issuer.RedirectURI {
					t.Error("invalid exchange credentials or PKCE/redirect binding")
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"id_token": id, "access_token": "private", "token_type": "Bearer"})
			})
			p.mu.Unlock()
			discovery := Discovery{Issuer: trust.Issuer.URL, TokenEndpoint: p.server.URL + "/token"}
			tokens, err := p.fetcher.ExchangeCode(context.Background(), trust, discovery, registry, "code", pkce)
			if err != nil || tokens.IDToken() != id || tokens.String() != "[redacted OIDC tokens]" {
				t.Fatalf("exchange: %v", err)
			}
			discovery.TokenEndpoint = "https://attacker.example/token"
			if _, err := p.fetcher.ExchangeCode(context.Background(), trust, discovery, registry, "code", pkce); err == nil {
				t.Fatal("secret sent to unbound token endpoint")
			}
			p.mu.Lock()
			gotCalls := calls
			p.mu.Unlock()
			if gotCalls != 1 {
				t.Fatal("invalid secret binding reached HTTP transport")
			}
		})
	}
}

func TestCodeExchangeRejectsUnconfiguredBrowser(t *testing.T) {
	p := newTestProvider(t)
	trust := p.trust
	trust.Issuer.ClientID, trust.Issuer.RedirectURI, trust.Issuer.SecretRef = "", "", ""
	called := false
	p.mu.Lock()
	p.handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	p.mu.Unlock()
	if _, err := p.fetcher.ExchangeCode(t.Context(), trust, Discovery{Issuer: trust.Issuer.URL, TokenEndpoint: p.server.URL + "/token"}, nil, "code", strings.Repeat("a", 43)); err == nil {
		t.Fatal("API-only issuer performed browser code exchange")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if called {
		t.Fatal("unconfigured browser reached token endpoint")
	}
}

func TestCodeExchangeResponseCleanupCannotReturnTokens(t *testing.T) {
	p := newTestProvider(t)
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
			fetcher, transport := newCloseFaultFetcher(tc.body, tc.closeErr)
			tokens, err := fetcher.ExchangeCode(t.Context(), p.trust, Discovery{Issuer: p.trust.Issuer.URL, TokenEndpoint: p.server.URL + "/token"}, nil, "fixture-code", strings.Repeat("a", 43))
			if tc.primary == nil && tc.closeErr == nil {
				if err != nil || tokens.IDToken() != "fixture-id" || tokens.AccessToken() != "fixture-access" {
					t.Fatal("complete exchange was rejected")
				}
			} else {
				if err == nil || tokens != (CodeTokens{}) || tc.primary != nil && !errors.Is(err, tc.primary) {
					t.Fatal("failed exchange returned tokens or lost its primary error")
				}
				if tc.closeErr == nil && err != tc.primary {
					t.Fatal("successful cleanup changed primary error identity")
				}
				if tc.closeErr != nil && (!errors.Is(err, ErrFetch) || tc.primary == nil && err != ErrFetch || strings.Contains(err.Error(), privateClose.Error())) {
					t.Fatal("cleanup failure was lost or exposed private details")
				}
			}
			if transport.calls != 1 || transport.body.closes != 1 {
				t.Fatal("response cleanup caused another dispatch or Close")
			}
		})
	}
}
