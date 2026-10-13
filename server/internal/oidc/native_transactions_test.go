package oidc

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestNativeTransactionExpiryProofOwnershipAndShutdown(t *testing.T) {
	p := newNativeProviderFixture(t, security.GoogleIDToken)
	start, a := p.begin(t)
	other, _ := p.begin(t)
	if _, err := p.transactions.Lookup(start.AttemptID, other.CompletionSecret); err == nil {
		t.Fatal("foreign proof accepted")
	}
	c, err := p.verifier.VerifyNativeCredential(t.Context(), a, NativeInput{Provider: "google", IDToken: p.sign(t, p.claims(start))})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := NewNativeTransactions(p.bounds, "v2.2."+base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := foreign.Consume(a, c); err == nil {
		t.Fatal("foreign owner accepted")
	}
	p.nanos.Add(int64(security.NativeLoginLifetime))
	if _, err := p.transactions.Consume(a, c); err == nil {
		t.Fatal("expired attempt accepted")
	}
	p.nanos.Add(int64(time.Second))
	_, _ = p.begin(t)
	p.transactions.Close()
	if _, err := p.transactions.Begin(p.trust, p.profile, "mobile", p.discovery); err == nil {
		t.Fatal("closed owner accepted work")
	}
}

func TestNativeTransactionCapacityAndDetachedTrust(t *testing.T) {
	p := newNativeProviderFixture(t, security.GoogleIDToken)
	_, a := p.begin(t)
	copy := a.Trust()
	copy.Issuer.NativeClients[0].Presenters[0] = "attacker"
	if a.Trust().Issuer.NativeClients[0].Presenters[0] != "android-client" {
		t.Fatal("retained trust escaped")
	}
	for range MaxLoginTransactions - 1 {
		if _, err := p.transactions.Begin(p.trust, p.profile, "mobile", p.discovery); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.transactions.Begin(p.trust, p.profile, "mobile", p.discovery); err != ErrNativeCapacity {
		t.Fatal("capacity not bounded", err)
	}
	p.nanos.Add(int64(security.NativeLoginLifetime))
	if _, err := p.transactions.Begin(p.trust, p.profile, "mobile", p.discovery); err != nil {
		t.Fatal("expired capacity not reclaimed", err)
	}
}
