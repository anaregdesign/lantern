package peerauth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"testing"
)

func TestControlManifestCanonicalTypedLineage(t *testing.T) {
	m, key, opts, _ := controlFixture(t)
	raw := signControlFixture(t, m, key)
	verified, err := VerifyControlManifest(raw, opts.Key, opts.Profile.Lineage)
	if err != nil || verified.Profile.Digest() != m.Profile.Digest() {
		t.Fatal(err)
	}
	legacy, legacyKey, legacyOpts, _ := membershipFixture(t)
	if _, err := VerifyManifest(raw, opts.Key, legacyOpts.Domain); err == nil {
		t.Fatal("control accepted as legacy")
	}
	if _, err := VerifyControlManifest(signFixture(t, legacy, legacyKey), legacyOpts.Key, opts.Profile.Lineage); err == nil {
		t.Fatal("legacy accepted as control")
	}
	foreign := opts.Profile.Lineage
	foreign.Instance[0]++
	if _, err := VerifyControlManifest(raw, opts.Key, foreign); err == nil {
		t.Fatal("foreign lineage")
	}
	wrong := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	if _, err := VerifyControlManifest(raw, wrong, opts.Profile.Lineage); err == nil {
		t.Fatal("wrong root")
	}
	if _, err := VerifyControlManifest(append(raw, '\n'), opts.Key, opts.Profile.Lineage); err == nil {
		t.Fatal("noncanonical")
	}
	for name, mutate := range map[string]func(*ControlProfile){
		"duplicate voter": func(p *ControlProfile) { p.Voters[1].Voter = p.Voters[0].Voter },
		"TLS key reused for vote": func(p *ControlProfile) {
			spki, err := x509.MarshalPKIXPublicKey(ed25519.PublicKey(p.Voters[0].Key[:]))
			if err != nil {
				t.Fatal(err)
			}
			p.Voters[1].Workload.SPKI = sha256.Sum256(spki)
		},
		"duplicate key":      func(p *ControlProfile) { p.Voters[1].Key = p.Voters[0].Key },
		"duplicate workload": func(p *ControlProfile) { p.Voters[1].Workload.ID = p.Voters[0].Workload.ID },
		"duplicate spki":     func(p *ControlProfile) { p.Voters[1].Workload.SPKI = p.Voters[0].Workload.SPKI },
		"missing voter":      func(p *ControlProfile) { p.Voters = p.Voters[:2] },
		"scope":              func(p *ControlProfile) { p.ProtocolScope = [32]byte{} },
		"plaintext":          func(p *ControlProfile) { p.Voters[0].Workload.Origin = "http://localhost:6381" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := m
			bad.Profile.Voters = append([]ControlVoter(nil), m.Profile.Voters...)
			mutate(&bad.Profile)
			if _, err := SignControlManifest(bad, key); err == nil {
				t.Fatal("invalid profile signed")
			}
		})
	}
}
