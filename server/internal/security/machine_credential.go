package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"
)

const MachineTokenPrefix = "lnt_m1_"
const MaxMachineCredentials = 256
const MaxMachineCredentialLifetime = 90 * 24 * time.Hour

// MachineCredential is operator-owned, signed sys metadata. Raw random tokens
// never enter the Image, audit, public management responses or graph records.
type MachineCredential struct {
	Identity  Identity  `json:"identity"`
	Digest    string    `json:"digest"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func NewMachineToken() (string, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", err
	}
	return MachineTokenPrefix + base64.RawURLEncoding.EncodeToString(secret[:]), nil
}

// MachineTokenDigest validates a distinct canonical 256-bit bearer format.
// A digest is a lookup key for a high-entropy token, not a password hash.
func MachineTokenDigest(raw string) (string, bool) {
	if len(raw) != len(MachineTokenPrefix)+43 || !strings.HasPrefix(raw, MachineTokenPrefix) {
		return "", false
	}
	encoded := strings.TrimPrefix(raw, MachineTokenPrefix)
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return "", false
	}
	digest := sha256.Sum256([]byte("lantern.machine.bearer.v1\x00" + raw))
	return hex.EncodeToString(digest[:]), true
}

func (c MachineCredential) valid() bool {
	return c.Identity.Kind == MachinePrincipal && c.Identity.valid() && validHexDigest(c.Digest) &&
		!c.CreatedAt.IsZero() && c.ExpiresAt.After(c.CreatedAt) && c.ExpiresAt.Sub(c.CreatedAt) <= MaxMachineCredentialLifetime
}

func (s *Snapshot) compileMachines(credentials []MachineCredential) error {
	if len(credentials) > MaxMachineCredentials {
		return ErrInvalidImage
	}
	s.machines = make(map[string]MachineCredential, len(credentials))
	for _, credential := range credentials {
		if !credential.valid() {
			return ErrInvalidImage
		}
		if _, known := s.principals[credential.Identity]; !known {
			return ErrInvalidImage
		}
		if _, duplicate := s.machines[credential.Digest]; duplicate {
			return ErrInvalidImage
		}
		s.machines[credential.Digest] = credential
	}
	return nil
}

// MachineAccess uses only this immutable policy cut and performs no I/O/write.
// Account suspension and Role loss apply even while a credential is unexpired.
func (s *Snapshot) MachineAccess(raw string, now time.Time) (Identity, time.Time, bool) {
	digest, valid := MachineTokenDigest(raw)
	if s == nil || !valid {
		return Identity{}, time.Time{}, false
	}
	credential, known := s.machines[digest]
	if !known || now.Before(credential.CreatedAt) || !now.Before(credential.ExpiresAt) {
		return Identity{}, time.Time{}, false
	}
	_, active := s.AccessFor(credential.Identity)
	return credential.Identity, credential.ExpiresAt, active
}
