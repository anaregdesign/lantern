package oidc

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/security"
)

// NativeInput is untrusted plugin output. Only VerifyNativeCredential can turn
// it into a transaction-bound opaque NativeProviderCredential.
type NativeInput struct {
	Provider, IDToken, AuthorizationCode, State string
}

func (NativeInput) String() string   { return "[redacted native provider input]" }
func (NativeInput) GoString() string { return "[redacted native provider input]" }

type NativeProviderCredential struct {
	attempt         NativeAttempt
	idToken, code   string
	verified        VerifiedIdentity
	codeHashPresent bool
}

func (NativeProviderCredential) String() string   { return "[redacted verified native credential]" }
func (NativeProviderCredential) GoString() string { return "[redacted verified native credential]" }
func (c NativeProviderCredential) matches(a NativeAttempt) bool {
	return a.owner != nil && a.transaction != nil && a.id != [32]byte{} && c.attempt == a && c.verified.evidence.Credential != [32]byte{}
}

func (v *Verifier) VerifyNativeCredential(ctx context.Context, a NativeAttempt, input NativeInput) (NativeProviderCredential, error) {
	if a.owner == nil || a.transaction == nil || len(input.IDToken) == 0 || len(input.IDToken) > maxTokenBytes {
		return NativeProviderCredential{}, ErrInvalidToken
	}
	t := a.transaction
	if t.client.Kind == security.GoogleIDToken {
		if input.Provider != "google" || input.AuthorizationCode != "" || input.State != "" {
			return NativeProviderCredential{}, ErrInvalidToken
		}
	} else {
		state := sha256.Sum256([]byte(input.State))
		if input.Provider != "apple" || len(input.AuthorizationCode) == 0 || len(input.AuthorizationCode) > 4096 || !a.owner.validState(input.State) || subtle.ConstantTimeCompare(state[:], t.state[:]) != 1 {
			return NativeProviderCredential{}, ErrInvalidToken
		}
	}
	verified, codeHash, err := v.verifyNativeID(ctx, a, input.IDToken, input.AuthorizationCode, "", false)
	if err != nil {
		return NativeProviderCredential{}, err
	}
	return NativeProviderCredential{attempt: a, idToken: input.IDToken, code: input.AuthorizationCode, verified: verified, codeHashPresent: codeHash}, nil
}

func (v *Verifier) verifyNativeID(ctx context.Context, a NativeAttempt, raw, code, access string, exchange bool) (VerifiedIdentity, bool, error) {
	if a.transaction == nil {
		return VerifiedIdentity{}, false, ErrInvalidToken
	}
	t := a.transaction
	header, claims, err := decodeToken(raw)
	if err != nil || header.Algorithm != "RS256" || header.Type != "" && header.Type != "JWT" || len(claims.Audience) != 1 || claims.Audience[0] != t.client.ClientID {
		return VerifiedIdentity{}, false, ErrInvalidToken
	}
	claimBytes, _ := base64.RawURLEncoding.Strict().DecodeString(strings.Split(raw, ".")[1])
	var members map[string]json.RawMessage
	if json.Unmarshal(claimBytes, &members) != nil {
		return VerifiedIdentity{}, false, ErrInvalidToken
	}
	// An omitted presenter is allowed by these provider profiles. An explicit
	// empty or null presenter is not an omission and cannot satisfy either one.
	if _, present := members["azp"]; present && claims.AZP == "" {
		return VerifiedIdentity{}, false, ErrInvalidToken
	}
	switch t.client.Kind {
	case security.GoogleIDToken:
		if claims.Issuer != security.GoogleIssuer || exchange || claims.AZP != "" && !slices.Contains(t.client.Presenters, claims.AZP) {
			return VerifiedIdentity{}, false, ErrInvalidToken
		}
	case security.AppleNative, security.AppleAndroidWeb:
		if claims.Issuer != security.AppleIssuer || t.nonce == "" || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(t.nonce)) != 1 || claims.AZP != "" && claims.AZP != t.client.ClientID {
			return VerifiedIdentity{}, false, ErrInvalidToken
		}
	default:
		return VerifiedIdentity{}, false, ErrInvalidToken
	}
	verified, err := v.verify(ctx, raw, t.trust, header, claims, t.client.ClientID)
	if err != nil {
		return VerifiedIdentity{}, false, err
	}
	if claims.AuthTime != nil && (claims.AuthTime.Time.After(v.now()) || claims.AuthTime.Time.After(claims.IssuedAt.Time)) {
		return VerifiedIdentity{}, false, ErrInvalidToken
	}
	_, codeHash := members["c_hash"]
	if codeHash && (t.client.Kind == security.GoogleIDToken || !nativeTokenHash(claims.CodeHash, code)) {
		return VerifiedIdentity{}, false, ErrInvalidToken
	}
	if exchange && verified.evidence.AccessHashPresent {
		if !nativeTokenHash(claims.AccessHash, access) {
			return VerifiedIdentity{}, false, ErrInvalidToken
		}
		verified.evidence.AccessHashChecked = true
	}
	// This intermediate ID fact is not a completion. It cannot issue a session;
	// only the private consumed transaction below establishes native-login.
	mode := "id"
	if exchange {
		mode = "login"
	}
	verified, err = finishEvidence(verified, mode, "oidc-id")
	return verified, codeHash, err
}

func nativeTokenHash(claim, value string) bool {
	if value == "" || len(value) > maxTokenBytes {
		return false
	}
	for n := range len(value) {
		if value[n] >= 128 {
			return false
		}
	}
	hash := sha256.Sum256([]byte(value))
	expected := base64.RawURLEncoding.EncodeToString(hash[:16])
	return subtle.ConstantTimeCompare([]byte(claim), []byte(expected)) == 1
}

// VerifyNativeCompletion re-verifies the original signatures at current time
// without repeating the code exchange. Raw tokens live only with this request's
// producer; the security owner receives detached commitments and original dates.
func (v *Verifier) VerifyNativeCompletion(ctx context.Context, c NativeCompletion, tokens NativeTokens) (VerifiedIdentity, error) {
	if !c.IsLogin() {
		return VerifiedIdentity{}, ErrInvalidToken
	}
	return v.verifyNativeCompletion(ctx, c, tokens)
}

// VerifyNativeRegistrationCompletion preserves the actual purpose and signed
// dates. It cannot turn registration or recovery into ordinary login evidence.
func (v *Verifier) VerifyNativeRegistrationCompletion(ctx context.Context, c NativeCompletion, tokens NativeTokens) (VerifiedIdentity, error) {
	if !c.RegistrationPurpose().Valid() {
		return VerifiedIdentity{}, ErrInvalidToken
	}
	return v.verifyNativeCompletion(ctx, c, tokens)
}

func (v *Verifier) verifyNativeCompletion(ctx context.Context, c NativeCompletion, tokens NativeTokens) (VerifiedIdentity, error) {
	a := c.attempt
	if !c.credential.matches(a) || c.consumedAt.IsZero() {
		return VerifiedIdentity{}, ErrInvalidToken
	}
	t := a.transaction
	verified, codeHash, err := v.verifyNativeID(ctx, a, c.credential.idToken, c.credential.code, "", false)
	if err != nil {
		return VerifiedIdentity{}, err
	}
	native := security.NativeAuthenticationEvidence{Kind: t.client.Kind, ClientProfileID: t.client.ID, ServiceOrigin: t.client.ServiceOrigin, Client: security.NativeClientCommitment(t.client),
		Transaction: evidenceDigest("native-transaction", struct {
			ID, Proof, State [32]byte
			Profile          security.CurrentProfile
			Trust            [32]byte
			Affinity         string
			Registration     security.NativeRegistrationPurpose `json:",omitzero"`
		}{a.id, t.proof, t.state, t.profile, verified.evidence.Configuration, a.owner.affinity, t.registration}),
		CreatedAt: t.createdAt, ExpiresAt: t.expiresAt, ConsumedAt: c.consumedAt, CodeHashPresent: codeHash, CodeHashChecked: codeHash, NoncePolicy: "google-initialize-unbound", Registration: t.registration}
	if t.client.Kind != security.GoogleIDToken {
		if tokens.binding == [32]byte{} || tokens.binding != nativeExchangeBinding(c) || tokens.exchange == [32]byte{} {
			return VerifiedIdentity{}, ErrInvalidToken
		}
		exchanged, exchangeCodeHash, err := v.verifyNativeID(ctx, a, tokens.idToken, c.credential.code, tokens.accessToken, true)
		if err != nil || exchanged.Identity() != verified.Identity() {
			return VerifiedIdentity{}, ErrInvalidToken
		}
		e := exchanged.evidence
		native.NoncePolicy, native.NonceChecked = "apple-raw", true
		native.Exchange = tokens.exchange
		native.ExchangeEvidence, err = e.Commitment()
		if err != nil {
			return VerifiedIdentity{}, ErrInvalidToken
		}
		native.ExchangeIssuedAt, native.ExchangeNotBefore, native.ExchangeExpiresAt, native.ExchangeAuthTime = e.IssuedAt, e.NotBefore, e.ExpiresAt, e.AuthTime
		native.ExchangeCodeHashPresent, native.ExchangeCodeHashChecked = exchangeCodeHash, exchangeCodeHash
		native.ExchangeAccessHashPresent, native.ExchangeAccessHashChecked = e.AccessHashPresent, e.AccessHashChecked
	} else if tokens != (NativeTokens{}) {
		return VerifiedIdentity{}, ErrInvalidToken
	}
	verified.evidence.Version, verified.evidence.Policy, verified.evidence.Native = 2, "lantern-native-login-v1", native
	if t.registration.Valid() {
		verified.evidence.Version, verified.evidence.Policy = 3, "lantern-native-registration-v1"
		return finishEvidence(verified, "native-registration", security.NativeEndUserProfile)
	}
	return finishEvidence(verified, "native-login", security.NativeEndUserProfile)
}
