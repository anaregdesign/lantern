package client

import (
	"testing"
	"time"
)

// TestNewLantern_BaseURLValidation covers the constructor's argument-
// shape guards: empty baseURL, missing scheme, bare host:port. The SDK
// must fail loudly rather than producing a *Lantern that 404s on every
// call.
//
// Happy-path round-trip coverage lives in tests/integration (against
// the real server/service handler) — keeping a fake-handler smoke
// suite here would just duplicate that without exercising any code
// path tests/integration can't reach.
func TestNewLantern_BaseURLValidation(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
	}{
		{name: "empty", baseURL: ""},
		{name: "missing scheme", baseURL: "lantern:6381"},
		{name: "bare host:port", baseURL: "localhost:6380"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewLantern(tc.baseURL); err == nil {
				t.Fatalf("NewLantern(%q): want error, got nil", tc.baseURL)
			}
		})
	}
}

// TestExpirationFromTTL pins the opt-in decay contract (#523): a
// non-positive ttl yields the zero time.Time (the wire's permanent
// sentinel), while a positive ttl materialises an absolute expiration
// at now+ttl. The relative-TTL convenience methods (PutVertex, AddEdge,
// PutEdge) route through this helper, so an omitted/zero TTL stores a
// vertex/edge permanently rather than injecting a hidden default.
func TestExpirationFromTTL(t *testing.T) {
	t.Run("zero ttl is permanent", func(t *testing.T) {
		if got := expirationFromTTL(0); !got.IsZero() {
			t.Fatalf("expirationFromTTL(0) = %v, want zero time", got)
		}
	})
	t.Run("negative ttl is permanent", func(t *testing.T) {
		if got := expirationFromTTL(-time.Hour); !got.IsZero() {
			t.Fatalf("expirationFromTTL(-1h) = %v, want zero time", got)
		}
	})
	t.Run("positive ttl materialises now+ttl", func(t *testing.T) {
		const ttl = time.Minute
		before := time.Now().Add(ttl)
		got := expirationFromTTL(ttl)
		after := time.Now().Add(ttl)
		if got.IsZero() {
			t.Fatalf("expirationFromTTL(%v) = zero time, want absolute expiration", ttl)
		}
		if got.Before(before) || got.After(after) {
			t.Fatalf("expirationFromTTL(%v) = %v, want within [%v, %v]", ttl, got, before, after)
		}
	})
}
