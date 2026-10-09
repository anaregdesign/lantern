// Package peerauth authenticates operator-approved replication workloads.
// Public OFF/OIDC mode and human/service Roles cannot grant peer authority.
package peerauth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

const MaxManifestBytes = 64 << 10
const MaxMembershipLifetime = 10 * time.Minute
const ClockMargin = 2 * time.Second

var ErrMembership = errors.New("peer membership is invalid or unavailable")

// Domain is operator-owned and comparable. A homogeneous public mode is
// required because every approved replica receives the complete graph.
type Domain struct {
	Deployment         [16]byte `json:"deployment"`
	NamespaceFormat    string   `json:"namespace_format"`
	AuthMode           string   `json:"auth_mode"`
	SecurityGeneration [16]byte `json:"security_generation"`
	WriterPublicKey    [32]byte `json:"writer_public_key"`
	TrustDigest        [32]byte `json:"trust_digest"`
	CurrentProfile     string   `json:"current_profile,omitempty"`
}

func (d Domain) valid() bool {
	if d.Deployment == [16]byte{} || d.NamespaceFormat != keyspace.Version || d.TrustDigest == [32]byte{} {
		return false
	}
	switch d.AuthMode {
	case "off":
		return d.SecurityGeneration == [16]byte{} && d.WriterPublicKey == [32]byte{} && d.CurrentProfile == ""
	case "oidc":
		if d.CurrentProfile != "" {
			raw, err := hex.DecodeString(strings.TrimPrefix(d.CurrentProfile, "current-v2:"))
			return strings.HasPrefix(d.CurrentProfile, "current-v2:") && err == nil && len(raw) == 32 && d.CurrentProfile == "current-v2:"+hex.EncodeToString(raw) && [32]byte(raw) != [32]byte{} && d.SecurityGeneration != [16]byte{} && d.WriterPublicKey == [32]byte{}
		}
		return d.SecurityGeneration != [16]byte{} && d.WriterPublicKey != [32]byte{}
	default:
		return false
	}
}

func (d Domain) Digest() [32]byte {
	raw, _ := json.Marshal(d)
	return sha256.Sum256(append([]byte("lantern.peer.domain.v1\x00"), raw...))
}

// Member binds a stable workload ID to one URI SAN, certificate public key and
// fixed HTTPS origin. Graph NodeID/HLC origins are separate ephemeral identities.
type Member struct {
	ID       [16]byte `json:"id"`
	Identity string   `json:"identity"`
	SPKI     [32]byte `json:"spki"`
	Origin   string   `json:"origin"`
}

type Manifest struct {
	Version   uint64    `json:"version"`
	Domain    Domain    `json:"domain"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Members   []Member  `json:"members"`
}

type signedManifest struct {
	Payload   json.RawMessage `json:"payload"`
	Signature []byte          `json:"signature"`
}

// SignManifest is operator tooling input. No serving process needs this key.
func SignManifest(m Manifest, key ed25519.PrivateKey) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize || !validManifest(m) {
		return nil, ErrMembership
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return nil, ErrMembership
	}
	raw, err := json.Marshal(signedManifest{payload, ed25519.Sign(key, signedPayload(payload))})
	if err != nil || len(raw) > MaxManifestBytes {
		return nil, ErrMembership
	}
	return raw, nil
}

func signedPayload(payload []byte) []byte {
	return append([]byte("lantern.peer.membership.v1\x00"), payload...)
}

func decodeManifest(raw []byte, key ed25519.PublicKey, domain Domain) (Manifest, error) {
	var envelope signedManifest
	if len(raw) == 0 || len(raw) > MaxManifestBytes || len(key) != ed25519.PublicKeySize || !domain.valid() || strictJSON(raw, &envelope) != nil {
		return Manifest{}, ErrMembership
	}
	canonical, _ := json.Marshal(envelope)
	if !bytes.Equal(raw, canonical) || !ed25519.Verify(key, signedPayload(envelope.Payload), envelope.Signature) {
		return Manifest{}, ErrMembership
	}
	var result Manifest
	if strictJSON(envelope.Payload, &result) != nil || !validManifest(result) || result.Domain != domain {
		return Manifest{}, ErrMembership
	}
	canonical, _ = json.Marshal(result)
	if !bytes.Equal(canonical, envelope.Payload) {
		return Manifest{}, ErrMembership
	}
	return result, nil
}

// VerifyManifest returns owned operator-authenticated configuration only.
// Serving still requires a durable Store floor and current certificate proof.
func VerifyManifest(raw []byte, key ed25519.PublicKey, domain Domain) (Manifest, error) {
	return decodeManifest(raw, key, domain)
}

func strictJSON(raw []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return ErrMembership
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ErrMembership
	}
	return nil
}

func validManifest(m Manifest) bool {
	if m.Version == 0 || m.Version == ^uint64(0) || !m.Domain.valid() || m.IssuedAt.IsZero() ||
		!m.ExpiresAt.After(m.IssuedAt) || m.ExpiresAt.Sub(m.IssuedAt) > MaxMembershipLifetime ||
		len(m.Members) == 0 || len(m.Members) > 128 {
		return false
	}
	ids, origins := make(map[[16]byte]bool), make(map[string]bool)
	previous := ""
	for _, member := range m.Members {
		if member.ID == [16]byte{} || member.SPKI == [32]byte{} || !validIdentity(member.Identity) ||
			!validOrigin(member.Origin) || member.Identity <= previous || ids[member.ID] || origins[member.Origin] {
			return false
		}
		ids[member.ID], origins[member.Origin], previous = true, true, member.Identity
	}
	return true
}

func validIdentity(raw string) bool {
	if len(raw) == 0 || len(raw) > 512 || strings.ContainsAny(raw, "\\%?#") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "spiffe" || u.Host == "" || u.User != nil || u.Port() != "" ||
		u.Opaque != "" || u.RawPath != "" || u.Path == "" || strings.HasSuffix(u.Path, "/") || path.Clean(u.Path) != u.Path || u.String() != raw {
		return false
	}
	for _, ch := range u.Host {
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '-' || ch == '_') {
			return false
		}
	}
	for _, ch := range u.Path {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("/._-", ch)) {
			return false
		}
	}
	return true
}

func validOrigin(raw string) bool {
	if len(raw) == 0 || len(raw) > 1024 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Path != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.String() != raw {
		return false
	}
	host, port, err := net.SplitHostPort(u.Host)
	n, portErr := strconv.Atoi(port)
	return err == nil && host != "" && !strings.ContainsAny(host, "%\\") && portErr == nil &&
		n > 0 && n <= 65535 && strconv.Itoa(n) == port
}

func manifestLive(m Manifest, now time.Time) bool {
	return !now.Before(m.IssuedAt.Add(-ClockMargin)) && now.Add(ClockMargin).Before(m.ExpiresAt)
}
