package oidc

import (
	"context"
	"encoding/json"
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
