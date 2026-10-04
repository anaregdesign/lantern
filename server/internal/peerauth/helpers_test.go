package peerauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

func membershipFixture(t testing.TB) (Manifest, ed25519.PrivateKey, StoreOptions, *time.Time) {
	t.Helper()
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	domain := Domain{Deployment: [16]byte{1}, NamespaceFormat: keyspace.Version, AuthMode: "off", TrustDigest: [32]byte{2}}
	m := Manifest{Version: 1, Domain: domain, IssuedAt: now, ExpiresAt: now.Add(time.Minute), Members: []Member{{ID: [16]byte{3}, Identity: "spiffe://lantern.test/node-a", SPKI: [32]byte{4}, Origin: "https://localhost:6381"}}}
	return m, private, StoreOptions{Path: filepath.Join(t.TempDir(), "membership"), Key: pub, Domain: domain, Now: func() time.Time { return now }}, &now
}

func signFixture(t testing.TB, m Manifest, key ed25519.PrivateKey) []byte {
	t.Helper()
	raw, err := SignManifest(m, key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
