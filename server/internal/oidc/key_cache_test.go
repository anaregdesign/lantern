package oidc

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"testing"
)

func TestKeyCacheSingleflightAndUnknownKidFloor(t *testing.T) {
	p := newTestProvider(t)
	cache := NewKeyCache(p.fetcher)
	cache.now = p.now
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if _, err := cache.Key(context.Background(), p.trust, "key", "EdDSA"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if p.discovery.Load() != 1 || p.jwks.Load() != 1 {
		t.Fatal("concurrent cold verification was not coalesced")
	}
	for i := range 1000 {
		if _, err := cache.Key(context.Background(), p.trust, fmt.Sprintf("unknown-%d", i), "EdDSA"); err == nil {
			t.Fatal("unknown kid accepted")
		}
	}
	if p.discovery.Load() != 1 || len(cache.entries) != 1 {
		t.Fatal("unknown kid flood grew network traffic or cache state")
	}
	if _, err := cache.Key(context.Background(), p.trust, "key", "RS256"); err == nil {
		t.Fatal("algorithm/key kind confusion accepted")
	}
}

func TestKeyCacheRotationExpiryAndTrustEpoch(t *testing.T) {
	p := newTestProvider(t)
	cache := NewKeyCache(p.fetcher)
	cache.now = p.now
	first, err := cache.Key(context.Background(), p.trust, "key", "EdDSA")
	if err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.document = map[string]any{"keys": []jsonKey{{Kind: "OKP", ID: "rotated", Algorithm: "EdDSA", Curve: "Ed25519", X: base64.RawURLEncoding.EncodeToString(pub)}}}
	p.mu.Unlock()
	p.nanos.Add(int64(keyRefreshFloor))
	rotated, err := cache.Key(context.Background(), p.trust, "rotated", "EdDSA")
	if err != nil || string(first.(ed25519.PublicKey)) == string(rotated.(ed25519.PublicKey)) {
		t.Fatal("rotated key not installed")
	}
	if _, err := cache.Key(context.Background(), p.trust, "key", "EdDSA"); err == nil {
		t.Fatal("removed key retained after replacement")
	}
	p.nanos.Add(int64(keyCacheTTL))
	p.mu.Lock()
	p.fail = true
	p.mu.Unlock()
	if _, err := cache.Key(context.Background(), p.trust, "rotated", "EdDSA"); err == nil {
		t.Fatal("expired keys used after refresh failure")
	}
	newTrust := p.trust
	newTrust.ConfigRevision++
	if _, err := cache.Key(context.Background(), newTrust, "rotated", "EdDSA"); err == nil {
		t.Fatal("disable/re-enable epoch reused old keys")
	}
	p.mu.Lock()
	p.fail = false
	p.mu.Unlock()
	newTrust.ConfigRevision++
	if _, err := cache.Key(context.Background(), newTrust, "rotated", "EdDSA"); err != nil {
		t.Fatal(err)
	}
	newTrust.Issuer.Enabled = false
	if _, err := cache.Key(context.Background(), newTrust, "rotated", "EdDSA"); err == nil {
		t.Fatal("disabled Issuer accepted cached authentication")
	}
}

func TestKeyCacheBoundedTrustEntries(t *testing.T) {
	p := newTestProvider(t)
	cache := NewKeyCache(p.fetcher)
	for i := range keyCacheEntries + 10 {
		trust := p.trust
		trust.ConfigRevision = uint64(i + 1)
		if _, err := cache.Key(context.Background(), trust, "key", "EdDSA"); err != nil {
			t.Fatal(err)
		}
	}
	if len(cache.entries) != keyCacheEntries {
		t.Fatal("trust cache not bounded")
	}
}
