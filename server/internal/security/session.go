package security

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"
)

const (
	BrowserCookieProfile   = "browser_cookie"
	NativeEndUserProfile   = "native_end_user"
	NativeCredentialPrefix = "lnu1_"
)

// Session contains only the digest of an opaque random credential. Fixed expiry
// avoids a synchronous security write on every business data request. AuthTime
// is verified provider evidence; zero means unknown, never CreatedAt or iat.
type Session struct {
	Profile              string    `json:"profile,omitempty"`
	ClientProfileID      string    `json:"client_profile_id,omitempty"`
	ServiceOrigin        string    `json:"service_origin,omitempty"`
	CSRFDigest           string    `json:"csrf_digest,omitempty"`
	IssuerConfigRevision uint64    `json:"issuer_config_revision"`
	Digest               string    `json:"digest"`
	Identity             Identity  `json:"identity"`
	CreatedAt            time.Time `json:"created_at"`
	ExpiresAt            time.Time `json:"expires_at"`
	AuthTime             time.Time `json:"auth_time"`
	Revoked              bool      `json:"revoked"`
}

// CredentialProfile interprets the historical empty profile as browser-cookie
// only. It never grants the native profile to an old cookie session.
func (s Session) CredentialProfile() string {
	if s.Profile == "" {
		return BrowserCookieProfile
	}
	return s.Profile
}

func (s Session) valid() bool {
	if !validHexDigest(s.Digest) || !s.Identity.valid() || s.Identity.Kind != OIDCPrincipal || s.CreatedAt.IsZero() ||
		!s.ExpiresAt.After(s.CreatedAt) || s.ExpiresAt.Sub(s.CreatedAt) > MaxSessionLifetime || s.AuthTime.After(s.CreatedAt) {
		return false
	}
	switch s.CredentialProfile() {
	case BrowserCookieProfile:
		return s.ClientProfileID == "" && s.ServiceOrigin == "" && (s.CSRFDigest == "" || validHexDigest(s.CSRFDigest))
	case NativeEndUserProfile:
		return s.CSRFDigest == "" && validRoleID(s.ClientProfileID) && nativeServiceOrigin(s.ServiceOrigin) && s.IssuerConfigRevision != 0
	default:
		return false
	}
}

// NativeCredentialDigest accepts exactly the domain-separated opaque native
// encoding. A recognized prefix with malformed bytes must never fall back to
// the external API-token or machine verifiers.
func NativeCredentialDigest(raw string) (string, bool) {
	if len(raw) != len(NativeCredentialPrefix)+43 || !strings.HasPrefix(raw, NativeCredentialPrefix) {
		return "", false
	}
	encoded := strings.TrimPrefix(raw, NativeCredentialPrefix)
	secret, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(secret) != 32 || base64.RawURLEncoding.EncodeToString(secret) != encoded {
		return "", false
	}
	digest := sha256.Sum256(append([]byte("lantern/native-end-user/v1\x00"), secret...))
	return hex.EncodeToString(digest[:]), true
}
