package peerauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
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

func controlFixture(t testing.TB) (ControlManifest, ed25519.PrivateKey, ControlStoreOptions, *time.Time) {
	t.Helper()
	m, key, old, now := membershipFixture(t)
	p := ControlProfile{Version: 1, Lineage: ControlLineage{Deployment: [16]byte{1}, Instance: [16]byte{2}}, NamespaceFormat: keyspace.Version, ProtocolScope: [32]byte{3}, TrustDigest: [32]byte{4}}
	for i := 1; i <= 3; i++ {
		p.Voters = append(p.Voters, ControlVoter{Workload: Member{ID: [16]byte{byte(i)}, Identity: fmt.Sprintf("spiffe://lantern.test/node-%d", i), SPKI: [32]byte{byte(i)}, Origin: fmt.Sprintf("https://localhost:%d", 6380+i)}, Voter: uint32(i), Key: [32]byte{byte(i)}, Proposer: true})
	}
	return ControlManifest{Version: 1, Profile: p, IssuedAt: m.IssuedAt, ExpiresAt: m.ExpiresAt}, key, ControlStoreOptions{Path: old.Path, Key: old.Key, Profile: p, Self: p.Voters[0].Workload, Now: old.Now}, now
}

func signControlFixture(t testing.TB, m ControlManifest, key ed25519.PrivateKey) []byte {
	t.Helper()
	raw, err := SignControlManifest(m, key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signFixture(t testing.TB, m Manifest, key ed25519.PrivateKey) []byte {
	t.Helper()
	raw, err := SignManifest(m, key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
