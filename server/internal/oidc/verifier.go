package oidc

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/anaregdesign/lantern/server/internal/security"
)

var ErrInvalidToken = errors.New("invalid authentication token")

const maxTokenBytes = 16 << 10

type tokenHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
	KeyID     string `json:"kid"`
}

type tokenClaims struct {
	jwt.RegisteredClaims
	ClientID   string           `json:"client_id"`
	Nonce      string           `json:"nonce"`
	AZP        string           `json:"azp"`
	AuthTime   *jwt.NumericDate `json:"auth_time"`
	AccessHash string           `json:"at_hash"`
}

// VerifiedIdentity carries authentication evidence only. Account/Issuer state,
// Role resolution and serving freshness must come from the current snapshot.
type VerifiedIdentity struct {
	Identity  security.Identity
	ExpiresAt time.Time
	AuthTime  time.Time
}

type Verifier struct {
	keys *KeyCache
	now  func() time.Time
}

func NewVerifier(keys *KeyCache) *Verifier { return NewVerifierWithClock(keys, time.Now) }
func NewVerifierWithClock(keys *KeyCache, clock func() time.Time) *Verifier {
	if clock == nil {
		clock = time.Now
	}
	return &Verifier{keys: keys, now: clock}
}

// TokenIssuer reads only a bounded unverified selector. The caller must perform
// an exact lookup in registered state before invoking VerifyAccess; never pass
// this string to Discovery or another network client.
func TokenIssuer(raw string) (string, error) {
	_, claims, err := decodeToken(raw)
	if err != nil || claims.Issuer == "" {
		return "", ErrInvalidToken
	}
	return claims.Issuer, nil
}

func decodeToken(raw string) (tokenHeader, tokenClaims, error) {
	if len(raw) == 0 || len(raw) > maxTokenBytes {
		return tokenHeader{}, tokenClaims{}, ErrInvalidToken
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || len(parts[2]) == 0 {
		return tokenHeader{}, tokenClaims{}, ErrInvalidToken
	}
	headerBytes, errH := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	claimBytes, errC := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	var header tokenHeader
	var claims tokenClaims
	if errH != nil || errC != nil || decodeJSON(headerBytes, &header) != nil || decodeJSON(claimBytes, &claims) != nil ||
		header.Algorithm == "" || header.KeyID == "" || len(header.KeyID) > 256 {
		return tokenHeader{}, tokenClaims{}, ErrInvalidToken
	}
	// A supplied authentication-time claim must be numeric. Only absence is
	// unknown; signed null cannot masquerade as a missing optional claim.
	var claimMembers map[string]json.RawMessage
	if json.Unmarshal(claimBytes, &claimMembers) != nil {
		return tokenHeader{}, tokenClaims{}, ErrInvalidToken
	}
	if _, present := claimMembers["auth_time"]; present && claims.AuthTime == nil {
		return tokenHeader{}, tokenClaims{}, ErrInvalidToken
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(headerBytes, &members) != nil {
		return tokenHeader{}, tokenClaims{}, ErrInvalidToken
	}
	for name := range members {
		if name != "alg" && name != "kid" && name != "typ" {
			return tokenHeader{}, tokenClaims{}, ErrInvalidToken
		}
	}
	return header, claims, nil
}

// VerifyAccess accepts only the RFC 9068 signed access-token profile. ID tokens,
// opaque tokens, group claims and email aliases are never alternative profiles.
func (v *Verifier) VerifyAccess(ctx context.Context, raw string, trust Trust) (VerifiedIdentity, error) {
	header, claims, err := decodeToken(raw)
	if err != nil || (header.Type != "at+jwt" && header.Type != "application/at+jwt") ||
		claims.ClientID == "" || len(claims.ClientID) > 512 || claims.ID == "" || len(claims.ID) > 512 {
		return VerifiedIdentity{}, ErrInvalidToken
	}
	verified, err := v.verify(ctx, raw, trust, header, claims, trust.Issuer.APIAudience)
	if err != nil {
		return VerifiedIdentity{}, err
	}
	// auth_time is optional for ordinary API access. When present it is
	// verified recent-auth evidence, never replaced by token issuance time.
	if claims.AuthTime != nil {
		if claims.AuthTime.Time.After(v.now()) || claims.AuthTime.Time.After(claims.IssuedAt.Time) {
			return VerifiedIdentity{}, ErrInvalidToken
		}
		verified.AuthTime = claims.AuthTime.Time
	}
	return verified, nil
}

// VerifyID is limited to the outstanding authorization-code login transaction.
// Nonce, client audience, azp and auth_time are validated independently from the
// API profile. Authentication time is optional evidence for ordinary login;
// only the consumed Server transaction may require recent evidence for step-up.
func (v *Verifier) VerifyID(ctx context.Context, raw string, trust Trust, nonce string) (VerifiedIdentity, error) {
	header, claims, err := decodeToken(raw)
	if err != nil || (header.Type != "" && header.Type != "JWT") || nonce == "" ||
		subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 ||
		(len(claims.Audience) > 1 && claims.AZP != trust.Issuer.ClientID) ||
		(claims.AZP != "" && claims.AZP != trust.Issuer.ClientID) {
		return VerifiedIdentity{}, ErrInvalidToken
	}
	verified, err := v.verify(ctx, raw, trust, header, claims, trust.Issuer.ClientID)
	if err != nil {
		return VerifiedIdentity{}, err
	}
	if claims.AuthTime != nil {
		if claims.AuthTime.Time.IsZero() || claims.AuthTime.Time.After(v.now()) || claims.AuthTime.Time.After(claims.IssuedAt.Time) {
			return VerifiedIdentity{}, ErrInvalidToken
		}
		verified.AuthTime = claims.AuthTime.Time
	}
	return verified, nil
}

// VerifyLogin additionally checks an optional signed at_hash against the
// co-returned access token. Only the pinned Ed25519 parameter set is supported
// for EdDSA, whose token hash uses SHA-512; the other accepted methods use SHA-256.
func (v *Verifier) VerifyLogin(ctx context.Context, raw string, trust Trust, nonce, accessToken string) (VerifiedIdentity, error) {
	verified, err := v.VerifyID(ctx, raw, trust, nonce)
	if err != nil {
		return VerifiedIdentity{}, err
	}
	header, claims, err := decodeToken(raw)
	if err != nil {
		return VerifiedIdentity{}, ErrInvalidToken
	}
	parts := strings.Split(raw, ".")
	claimBytes, _ := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	var members map[string]json.RawMessage
	if json.Unmarshal(claimBytes, &members) != nil {
		return VerifiedIdentity{}, ErrInvalidToken
	}
	if _, present := members["at_hash"]; !present {
		return verified, nil
	}
	if claims.AccessHash == "" || len(accessToken) == 0 || len(accessToken) > maxTokenBytes {
		return VerifiedIdentity{}, ErrInvalidToken
	}
	for i := range len(accessToken) {
		if accessToken[i] >= 128 {
			return VerifiedIdentity{}, ErrInvalidToken
		}
	}
	var expected string
	switch header.Algorithm {
	case "RS256", "PS256", "ES256":
		digest := sha256.Sum256([]byte(accessToken))
		expected = base64.RawURLEncoding.EncodeToString(digest[:16])
	case "EdDSA":
		digest := sha512.Sum512([]byte(accessToken))
		expected = base64.RawURLEncoding.EncodeToString(digest[:32])
	default:
		return VerifiedIdentity{}, ErrInvalidToken
	}
	if subtle.ConstantTimeCompare([]byte(expected), []byte(claims.AccessHash)) != 1 {
		return VerifiedIdentity{}, ErrInvalidToken
	}
	return verified, nil
}

func (v *Verifier) verify(ctx context.Context, raw string, trust Trust, header tokenHeader, claims tokenClaims, audience string) (VerifiedIdentity, error) {
	if v == nil || v.keys == nil || !trust.Issuer.Enabled || audience == "" || claims.Issuer != trust.Issuer.URL ||
		!slices.Contains(trust.Issuer.Algorithms, header.Algorithm) ||
		claims.Subject == "" || len(claims.Subject) > 255 || claims.ExpiresAt == nil || claims.IssuedAt == nil ||
		!claims.ExpiresAt.After(claims.IssuedAt.Time) || claims.ExpiresAt.Sub(claims.IssuedAt.Time) > 24*time.Hour {
		return VerifiedIdentity{}, ErrInvalidToken
	}
	for i := range len(claims.Subject) {
		if claims.Subject[i] >= 128 {
			return VerifiedIdentity{}, ErrInvalidToken
		}
	}
	key, err := v.keys.Key(ctx, trust, header.KeyID, header.Algorithm)
	if err != nil {
		return VerifiedIdentity{}, ErrInvalidToken
	}
	var validated tokenClaims
	token, err := jwt.ParseWithClaims(raw, &validated, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() == "PS256" {
			// jwt/v5 deliberately accepts legacy auto-length PSS salts. JOSE
			// requires a hash-length salt; select a request-local strict method
			// without mutating the library's global registry or verifying twice.
			strict := *jwt.SigningMethodPS256
			strict.VerifyOptions = &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}
			token.Method = &strict
		}
		return key, nil
	},
		jwt.WithValidMethods(trust.Issuer.Algorithms), jwt.WithIssuer(trust.Issuer.URL), jwt.WithAudience(audience),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithStrictDecoding(), jwt.WithTimeFunc(v.now),
		jwt.WithLeeway(30*time.Second))
	if err != nil || !token.Valid || !v.now().Before(validated.ExpiresAt.Time) {
		return VerifiedIdentity{}, ErrInvalidToken
	}
	return VerifiedIdentity{Identity: security.Identity{Kind: security.OIDCPrincipal,
		Issuer: validated.Issuer, Subject: validated.Subject}, ExpiresAt: validated.ExpiresAt.Time}, nil
}
