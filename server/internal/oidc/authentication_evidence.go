package oidc

import (
	"context"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/golang-jwt/jwt/v5"
)

// VerifiedIdentity can only be produced by successful verifier paths. Its zero
// value is not evidence. Getters detach all facts; JSON cannot mint this type.
type VerifiedIdentity struct {
	evidence security.TokenAuthenticationEvidence
	// Preserve the verifier's original time representation for existing callers:
	// SessionRequest hashes its JSON before canonicalizing the durable Session.
	expiresAt, authTime time.Time
}

func (v VerifiedIdentity) Identity() security.Identity                    { return v.evidence.Identity }
func (v VerifiedIdentity) ExpiresAt() time.Time                           { return v.expiresAt }
func (v VerifiedIdentity) AuthTime() time.Time                            { return v.authTime }
func (v VerifiedIdentity) Evidence() security.TokenAuthenticationEvidence { return v.evidence }
func (VerifiedIdentity) String() string                                   { return "[redacted verified OIDC identity]" }

func evidenceDigest(domain string, value any) [32]byte {
	// Callers supply bounded value schemas. Reject unrepresentable inputs;
	// no failed encoding may turn into a shared placeholder commitment.
	raw, err := json.Marshal(value)
	if err != nil {
		return [32]byte{}
	}
	return sha256.Sum256(append([]byte("lantern/authentication/"+domain+"/v1\x00"), raw...))
}
func signedTime(date *jwt.NumericDate, present bool) security.AuthenticationTime {
	if date == nil {
		return security.AuthenticationTime{Present: present}
	}
	return security.AuthenticationTime{Present: true, Numeric: true, Seconds: date.Unix(), Nanoseconds: int32(date.Nanosecond())}
}
func tokenEvidence(raw string, trust Trust, header tokenHeader, claims tokenClaims, audience string, key crypto.PublicKey) (VerifiedIdentity, error) {
	public, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return VerifiedIdentity{}, ErrInvalidToken
	}
	parts := strings.Split(raw, ".")
	claimBytes, _ := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	var members map[string]json.RawMessage
	if json.Unmarshal(claimBytes, &members) != nil {
		return VerifiedIdentity{}, ErrInvalidToken
	}
	_, nbf := members["nbf"]
	_, ath := members["at_hash"]
	e := security.TokenAuthenticationEvidence{
		Version: 1, Policy: "lantern-oidc-v1", Identity: security.Identity{Kind: security.OIDCPrincipal, Issuer: claims.Issuer, Subject: claims.Subject},
		Credential: evidenceDigest("credential", raw), Key: evidenceDigest("key-spki", public), Configuration: evidenceDigest("trust", trust),
		AudienceClient: evidenceDigest("audience-client", struct {
			Expected    string
			Audience    []string
			Client, AZP string
		}{audience, claims.Audience, claims.ClientID, claims.AZP}),
		Algorithm: header.Algorithm, KeyID: header.KeyID, Generation: trust.Generation, ConfigRevision: trust.ConfigRevision,
		IssuedAt: signedTime(claims.IssuedAt, true), NotBefore: signedTime(claims.NotBefore, nbf), ExpiresAt: signedTime(claims.ExpiresAt, true), AuthTime: signedTime(claims.AuthTime, claims.AuthTime != nil), AccessHashPresent: ath,
	}
	if claims.Nonce != "" {
		e.Nonce = evidenceDigest("nonce", claims.Nonce)
	}
	verified := VerifiedIdentity{evidence: e, expiresAt: claims.ExpiresAt.Time}
	if claims.AuthTime != nil {
		verified.authTime = claims.AuthTime.Time
	}
	return verified, nil
}
func finishEvidence(v VerifiedIdentity, mode, profile string) (VerifiedIdentity, error) {
	v.evidence.Mode, v.evidence.Profile = mode, profile
	if _, err := v.evidence.Commitment(); err != nil {
		return VerifiedIdentity{}, ErrInvalidToken
	}
	return v, nil
}
func exchangeBinding(trust Trust, discovery Discovery, verifier string) [32]byte {
	return evidenceDigest("code-exchange-binding", struct {
		Trust     Trust
		Discovery Discovery
		PKCE      [32]byte
	}{trust, discovery, evidenceDigest("pkce", verifier)})
}

// VerifyLoginCompletion joins a consumed transaction and its actual successful
// exchange. VerifyID/VerifyLogin alone never assert completion of this flow.
func (v *Verifier) VerifyLoginCompletion(ctx context.Context, completion LoginCompletion, tokens CodeTokens) (VerifiedIdentity, error) {
	t := completion.transaction
	if completion.consumedState == [32]byte{} || tokens.binding == [32]byte{} || tokens.binding != exchangeBinding(t.trust, t.discovery, t.verifier) {
		return VerifiedIdentity{}, ErrInvalidToken
	}
	verified, err := v.VerifyLogin(ctx, tokens.idToken, t.trust, t.nonce, tokens.accessToken)
	if err != nil {
		return VerifiedIdentity{}, err
	}
	flow := "login"
	if t.stepUp {
		flow = "step-up"
	}
	if t.authorizationID != [32]byte{} {
		flow = "operation"
	}
	verified.evidence.Code = security.CodeAuthenticationEvidence{Flow: flow, Transaction: evidenceDigest("code-transaction", struct {
		State, Cookie        [32]byte
		Binding              [32]byte
		Authorization        [32]byte
		Flow                 string
		CreatedAt, ExpiresAt time.Time
	}{completion.consumedState, t.cookieDigest, tokens.binding, t.authorizationID, flow, t.createdAt, t.expiresAt}), Exchange: tokens.exchange, Nonce: verified.evidence.Nonce, PKCE: evidenceDigest("pkce", t.verifier), AuthorizationID: t.authorizationID, CreatedAt: t.createdAt, ExpiresAt: t.expiresAt, ConsumedAt: completion.consumedAt}
	return finishEvidence(verified, "code", "oidc-id")
}
