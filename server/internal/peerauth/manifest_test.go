package peerauth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func TestManifestPinsSignatureDomainAndCanonicalShape(t *testing.T) {
	m, key, options, now := membershipFixture(t)
	raw := signFixture(t, m, key)
	decoded, err := decodeManifest(raw, options.Key, options.Domain)
	if err != nil || decoded.Version != 1 || !manifestLive(decoded, *now) {
		t.Fatal("valid membership rejected", err)
	}
	for _, mutate := range []func(*Domain){
		func(d *Domain) { d.Deployment[0]++ },
		func(d *Domain) { d.NamespaceFormat = "" },
		func(d *Domain) { d.AuthMode = "oidc"; d.SecurityGeneration[0] = 1; d.WriterPublicKey[0] = 2 },
		func(d *Domain) { d.TrustDigest[0]++ },
	} {
		d := options.Domain
		mutate(&d)
		if _, err := decodeManifest(raw, options.Key, d); err == nil {
			t.Fatal("incompatible domain accepted")
		}
	}
	wrong, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := decodeManifest(raw, wrong, options.Domain); err == nil {
		t.Fatal("wrong operator accepted")
	}
	for _, invalid := range [][]byte{append(bytes.Clone(raw), ' '), bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":2`), 1), bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1), bytes.Repeat([]byte("x"), MaxManifestBytes+1)} {
		if _, err := decodeManifest(invalid, options.Key, options.Domain); err == nil {
			t.Fatal("tampered or ambiguous manifest accepted")
		}
	}
	if manifestLive(m, m.ExpiresAt.Add(-ClockMargin)) || manifestLive(m, m.IssuedAt.Add(-ClockMargin-time.Second)) {
		t.Fatal("expiry/clock margin ignored")
	}
}

func TestManifestBoundsAndExplicitWorkloadIdentity(t *testing.T) {
	m, key, _, _ := membershipFixture(t)
	for _, mutate := range []func(*Manifest){
		func(m *Manifest) { m.ExpiresAt = m.IssuedAt.Add(MaxMembershipLifetime + time.Second) },
		func(m *Manifest) { m.Members = append(m.Members, m.Members[0]) },
		func(m *Manifest) { m.Members[0].Origin = "http://localhost:6381" },
		func(m *Manifest) { m.Members[0].Origin = "https://localhost:6381/peer" },
		func(m *Manifest) { m.Members[0].Identity = "spiffe://lantern.test/a/../b" },
		func(m *Manifest) { m.Members[0].Identity = "spiffe://lantern.test/node%2Fa" },
		func(m *Manifest) { m.Members[0].Identity = "spiffe://lantern.test/" },
		func(m *Manifest) { m.Members[0].Identity = "spiffe://lantern.test/node/" },
		func(m *Manifest) { m.Members[0].SPKI = [32]byte{} },
		func(m *Manifest) { m.Version = 0 },
	} {
		candidate := m
		candidate.Members = append([]Member(nil), m.Members...)
		mutate(&candidate)
		if _, err := SignManifest(candidate, key); err == nil {
			t.Fatal("invalid manifest signed")
		}
	}
}
