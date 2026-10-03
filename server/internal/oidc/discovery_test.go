package oidc

import (
	"context"
	"testing"
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
