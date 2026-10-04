package oidc

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestIssuerValidationLoginAndPublicKeyContract(t *testing.T) {
	p := newTestProvider(t)
	if err := p.fetcher.ValidateIssuer(t.Context(), p.trust.Issuer, nil); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/.well-known/openid-configuration" {
			_ = json.NewEncoder(w).Encode(Discovery{Issuer: p.server.URL, AuthorizationEndpoint: p.server.URL + "/authorize", TokenEndpoint: p.server.URL + "/token", JWKSURI: p.server.URL + "/jwks", ResponseTypes: []string{"code"}, CodeChallengeMethods: []string{"plain"}})
		} else {
			_ = json.NewEncoder(w).Encode(p.document)
		}
	})
	p.mu.Unlock()
	if err := p.fetcher.ValidateIssuer(t.Context(), p.trust.Issuer, nil); err == nil {
		t.Fatal("Issuer without S256 accepted")
	}
	p.mu.Lock()
	p.handler = nil
	p.badIssuer = true
	p.mu.Unlock()
	if err := p.fetcher.ValidateIssuer(t.Context(), p.trust.Issuer, nil); err == nil {
		t.Fatal("mismatched Issuer accepted")
	}
	p.mu.Lock()
	p.badIssuer = false
	p.document = map[string]any{"keys": []any{}}
	p.mu.Unlock()
	if err := p.fetcher.ValidateIssuer(t.Context(), p.trust.Issuer, nil); err == nil {
		t.Fatal("empty key set accepted")
	}
}

func TestIssuerValidationOperatorSecretBinding(t *testing.T) {
	p := newTestProvider(t)
	issuer := p.trust.Issuer
	issuer.SecretRef = "operator"
	if err := p.fetcher.ValidateIssuer(t.Context(), issuer, nil); err == nil {
		t.Fatal("missing binding accepted")
	}
	path := filepath.Join(t.TempDir(), "client.secret")
	if err := os.WriteFile(path, []byte("test-client-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewSecretRegistry(map[string]SecretBinding{"operator": {Issuer: issuer.URL, ClientID: issuer.ClientID, TokenOrigin: p.server.URL, Path: path}})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.fetcher.ValidateIssuer(t.Context(), issuer, registry); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := p.fetcher.ValidateIssuer(t.Context(), issuer, registry); err == nil {
		t.Fatal("insecure secret file accepted")
	}
}
