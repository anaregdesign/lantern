package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestDiscoveryExactRegisteredIssuer(t *testing.T) {
	p := newTestProvider(t)
	if _, err := p.fetcher.Discover(context.Background(), p.trust.Issuer); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.badIssuer = true
	p.mu.Unlock()
	if _, err := p.fetcher.Discover(context.Background(), p.trust.Issuer); err == nil {
		t.Fatal("Discovery changed the registered Issuer identity")
	}
	disabled := p.trust.Issuer
	disabled.Enabled = false
	calls := p.discovery.Load()
	if _, err := p.fetcher.Discover(context.Background(), disabled); err == nil || p.discovery.Load() != calls {
		t.Fatal("disabled Issuer contacted Discovery")
	}
}

func TestDiscoveryGoogleDestinationsPinnedForEveryProfile(t *testing.T) {
	p := newNativeProviderFixture(t, security.GoogleIDToken)
	for _, profile := range []string{"native", "browser", "API"} {
		t.Run(profile, func(t *testing.T) {
			issuer := p.trust.Issuer
			if profile != "native" {
				issuer.NativeClients = nil
				if profile == "browser" {
					issuer.ClientID, issuer.RedirectURI = "browser", "https://lantern.example/auth/callback"
				} else {
					issuer.APIAudience = "api"
				}
			}
			original := p.discovery
			if _, err := p.fetcher.Discover(t.Context(), issuer); err != nil {
				t.Fatal("pinned Google metadata rejected", err)
			}
			for _, mutate := range []func(*Discovery){
				func(d *Discovery) { d.AuthorizationEndpoint = "https://attacker.example/authorize" },
				func(d *Discovery) { d.TokenEndpoint = "https://attacker.example/token" },
				func(d *Discovery) { d.JWKSURI = "https://attacker.example/keys" },
			} {
				p.discovery = original
				mutate(&p.discovery)
				if _, err := p.fetcher.Discover(t.Context(), issuer); err == nil {
					t.Fatal("Google profile accepted a changed provider destination")
				}
			}
			p.discovery = original
		})
	}
}

func TestDiscoveryIndependentAPIProfile(t *testing.T) {
	p := newTestProvider(t)
	api := p.trust.Issuer
	api.ClientID, api.RedirectURI, api.SecretRef = "", "", ""
	document := Discovery{Issuer: api.URL, JWKSURI: p.server.URL + "/jwks"}
	p.mu.Lock()
	p.handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(document)
	})
	p.mu.Unlock()
	if _, err := p.fetcher.Discover(t.Context(), api); err != nil {
		t.Fatal("API-only discovery required unused login endpoints", err)
	}
	if _, err := p.fetcher.Discover(t.Context(), p.trust.Issuer); err == nil {
		t.Fatal("browser profile accepted missing login endpoints")
	}
	for _, tc := range []struct{ name, authorize, token, jwks string }{
		{"invalid optional authorization", "http://idp.example/authorize", "", p.server.URL + "/jwks"},
		{"invalid optional token", "", "https://user:password@idp.example/token", p.server.URL + "/jwks"},
		{"missing JWKS", "", "", ""},
		{"invalid JWKS", "", "", "http://idp.example/keys"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p.mu.Lock()
			document.AuthorizationEndpoint, document.TokenEndpoint, document.JWKSURI = tc.authorize, tc.token, tc.jwks
			p.mu.Unlock()
			if _, err := p.fetcher.Discover(t.Context(), api); err == nil {
				t.Fatal("invalid endpoint accepted")
			}
		})
	}
}
