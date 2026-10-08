package peerauth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"time"

	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

const ControlHeader = "Lantern-Control-Scope"
const controlSignatureDomain = "lantern.peer.control.membership.v1\x00"

// ControlLineage is independently provisioned. A proposed binding never gets
// to select the authority stream whose later revision may fence this owner.
type ControlLineage struct {
	Deployment [16]byte `json:"deployment"`
	Instance   [16]byte `json:"instance"`
}

// ControlVoter binds distinct TLS and protocol signing identities. The private
// participant owner additionally compares the entire set to its actual kernel.
type ControlVoter struct {
	Workload Member   `json:"workload"`
	Voter    uint32   `json:"voter"`
	Key      [32]byte `json:"key"`
	Proposer bool     `json:"proposer"`
}

type ControlProfile struct {
	Version         uint16         `json:"profile_version"`
	Lineage         ControlLineage `json:"lineage"`
	NamespaceFormat string         `json:"namespace_format"`
	ProtocolScope   [32]byte       `json:"protocol_scope"`
	TrustDigest     [32]byte       `json:"trust_digest"`
	Voters          []ControlVoter `json:"voters"`
}

func (p ControlProfile) Valid() bool {
	if p.Version != 1 || p.Lineage.Deployment == [16]byte{} || p.Lineage.Instance == [16]byte{} ||
		p.NamespaceFormat != keyspace.Version || p.ProtocolScope == [32]byte{} || p.TrustDigest == [32]byte{} ||
		len(p.Voters) < 3 || len(p.Voters) > 31 || len(p.Voters)%2 == 0 {
		return false
	}
	ids, keys, workloads, spkis, origins := map[uint32]bool{}, map[[32]byte]bool{}, map[[16]byte]bool{}, map[[32]byte]bool{}, map[string]bool{}
	previous, proposers := "", 0
	for _, v := range p.Voters {
		m := v.Workload
		if v.Voter == 0 || v.Key == [32]byte{} || ids[v.Voter] || keys[v.Key] ||
			m.ID == [16]byte{} || m.SPKI == [32]byte{} || workloads[m.ID] || spkis[m.SPKI] || origins[m.Origin] ||
			!validIdentity(m.Identity) || !validOrigin(m.Origin) || m.Identity <= previous {
			return false
		}
		ids[v.Voter], keys[v.Key], workloads[m.ID], spkis[m.SPKI], origins[m.Origin] = true, true, true, true, true
		previous = m.Identity
		if v.Proposer {
			proposers++
		}
	}
	for _, v := range p.Voters {
		spki, err := x509.MarshalPKIXPublicKey(ed25519.PublicKey(v.Key[:]))
		if err != nil || spkis[sha256.Sum256(spki)] {
			return false // No voting key may also authenticate any TLS workload.
		}
	}
	return proposers > 0
}

func (p ControlProfile) Digest() [32]byte {
	raw, _ := json.Marshal(p)
	return sha256.Sum256(append([]byte("lantern.peer.control.profile.v1\x00"), raw...))
}

type ControlManifest struct {
	Version   uint64         `json:"version"`
	Profile   ControlProfile `json:"profile"`
	IssuedAt  time.Time      `json:"issued_at"`
	ExpiresAt time.Time      `json:"expires_at"`
}

func (m ControlManifest) valid() bool {
	return m.Version != 0 && m.Version != ^uint64(0) && m.Profile.Valid() && !m.IssuedAt.IsZero() &&
		m.ExpiresAt.After(m.IssuedAt) && m.ExpiresAt.Sub(m.IssuedAt) <= MaxMembershipLifetime
}

// SignControlManifest is offline operator tooling, not a voting or H signer.
func SignControlManifest(m ControlManifest, key ed25519.PrivateKey) ([]byte, error) {
	if !m.valid() || len(key) != ed25519.PrivateKeySize {
		return nil, ErrMembership
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return nil, ErrMembership
	}
	raw, err := json.Marshal(signedManifest{payload, ed25519.Sign(key, append([]byte(controlSignatureDomain), payload...))})
	if err != nil || len(raw) > MaxManifestBytes {
		return nil, ErrMembership
	}
	return raw, nil
}

// VerifyControlManifest authenticates only the independently pinned lineage.
// Comparing the immutable binding is a separate owner obligation: a legitimate
// higher-revision change must be retained as a fence, never auto-adopted.
func VerifyControlManifest(raw []byte, key ed25519.PublicKey, lineage ControlLineage) (ControlManifest, error) {
	var envelope signedManifest
	if len(raw) == 0 || len(raw) > MaxManifestBytes || len(key) != ed25519.PublicKeySize ||
		lineage.Deployment == [16]byte{} || lineage.Instance == [16]byte{} || strictJSON(raw, &envelope) != nil {
		return ControlManifest{}, ErrMembership
	}
	canonical, _ := json.Marshal(envelope)
	if !bytes.Equal(raw, canonical) || !ed25519.Verify(key, append([]byte(controlSignatureDomain), envelope.Payload...), envelope.Signature) {
		return ControlManifest{}, ErrMembership
	}
	var m ControlManifest
	if strictJSON(envelope.Payload, &m) != nil || !m.valid() || m.Profile.Lineage != lineage {
		return ControlManifest{}, ErrMembership
	}
	canonical, _ = json.Marshal(m)
	if !bytes.Equal(canonical, envelope.Payload) {
		return ControlManifest{}, ErrMembership
	}
	return m, nil
}

// This shared snapshot carries only membership/time into the strict workload
// machinery. Its zero legacy Domain is never signed, exposed, or used on wire.
func controlSnapshot(m ControlManifest, raw []byte) *membershipSnapshot {
	members := make([]Member, len(m.Profile.Voters))
	for i, v := range m.Profile.Voters {
		members[i] = v.Workload
	}
	return snapshot(Manifest{Version: m.Version, IssuedAt: m.IssuedAt, ExpiresAt: m.ExpiresAt, Members: members}, raw)
}
