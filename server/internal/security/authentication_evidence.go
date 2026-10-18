package security

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"time"
)

// MaxAuthenticationEvidenceBytes bounds one detached, process-owned envelope.
const MaxAuthenticationEvidenceBytes = 16 << 10

// AuthenticationTime preserves a claim's presence independently of its value.
// Numeric=false with Present=true records an accepted JSON null (nbf only).
// Seconds/nanoseconds retain the verifier's NumericDate interpretation without
// imposing an RFC3339 year restriction on previously accepted signed dates.
type AuthenticationTime struct {
	Present     bool
	Numeric     bool
	Seconds     int64
	Nanoseconds int32
}

func (t AuthenticationTime) Time() time.Time {
	if !t.Present || !t.Numeric {
		return time.Time{}
	}
	return time.Unix(t.Seconds, int64(t.Nanoseconds)).UTC()
}

// CodeAuthenticationEvidence describes a completed, consumed Code transaction.
// Commitments contain no state, cookie, code, PKCE verifier or access token.
type CodeAuthenticationEvidence struct {
	Flow                               string
	Transaction, Exchange, Nonce, PKCE [32]byte
	AuthorizationID                    [32]byte
	CreatedAt, ExpiresAt, ConsumedAt   time.Time
}

// TokenAuthenticationEvidence is detached factual input from the trusted
// provider adapter, NOT proof of a producer or a current-admission capability.
// Only oidc's opaque verifier result establishes how these facts were obtained.
// All members are values: copying this record cannot mutate its owner.
type TokenAuthenticationEvidence struct {
	Version                                        uint8
	Mode, Profile, Policy                          string
	Identity                                       Identity
	Credential, Key, Configuration, AudienceClient [32]byte
	Algorithm, KeyID                               string
	Generation                                     [16]byte
	ConfigRevision                                 uint64
	IssuedAt, NotBefore, ExpiresAt, AuthTime       AuthenticationTime
	Nonce                                          [32]byte
	AccessHashPresent, AccessHashChecked           bool
	Code                                           CodeAuthenticationEvidence
	// omitzero preserves every existing v1 transcript byte. Native completion
	// uses its own version and commitment domain, never a synthetic Code flow.
	Native NativeAuthenticationEvidence `json:",omitzero"`
}

// Commitment is a versioned factual commitment, not a signature or an assertion
// of current authorization. It also rejects unrepresentable/unbounded envelopes.
func (e TokenAuthenticationEvidence) Commitment() ([32]byte, error) {
	nativeLogin := e.Version == 2 && e.Policy == "lantern-native-login-v1" && e.Mode == "native-login"
	nativeRegistration := e.Version == 3 && e.Policy == "lantern-native-registration-v1" && e.Mode == "native-registration"
	native := nativeLogin || nativeRegistration
	if (!native && (e.Version != 1 || e.Policy != "lantern-oidc-v1")) || !e.Identity.valid() || e.Identity.Kind != OIDCPrincipal ||
		len(e.KeyID) == 0 || len(e.KeyID) > 256 || e.Generation == [16]byte{} || e.ConfigRevision == 0 ||
		e.Credential == [32]byte{} || e.Key == [32]byte{} || e.Configuration == [32]byte{} || e.AudienceClient == [32]byte{} ||
		!e.IssuedAt.Present || !e.IssuedAt.Numeric || !e.ExpiresAt.Present || !e.ExpiresAt.Numeric {
		return [32]byte{}, ErrOperationAuthorization
	}
	if native {
		if !e.Native.valid(e) {
			return [32]byte{}, ErrOperationAuthorization
		}
	} else if e.Native != (NativeAuthenticationEvidence{}) {
		return [32]byte{}, ErrOperationAuthorization
	}
	for _, claim := range []AuthenticationTime{e.IssuedAt, e.NotBefore, e.ExpiresAt, e.AuthTime} {
		if claim.Nanoseconds < 0 || claim.Nanoseconds >= 1e9 || !claim.Present && claim.Numeric || !claim.Numeric && (claim.Seconds != 0 || claim.Nanoseconds != 0) {
			return [32]byte{}, ErrOperationAuthorization
		}
	}
	if e.AuthTime.Present && !e.AuthTime.Numeric {
		return [32]byte{}, ErrOperationAuthorization
	}
	switch e.Algorithm {
	case "EdDSA", "ES256", "RS256", "PS256":
	default:
		return [32]byte{}, ErrOperationAuthorization
	}
	switch e.Mode {
	case "native-login", "native-registration":
		if !native {
			return [32]byte{}, ErrOperationAuthorization
		}
	case "access":
		if e.Profile != "rfc9068" || e.Code != (CodeAuthenticationEvidence{}) {
			return [32]byte{}, ErrOperationAuthorization
		}
	case "id", "login":
		if e.Mode == "id" && e.AccessHashChecked || e.Mode == "login" && e.AccessHashPresent != e.AccessHashChecked {
			return [32]byte{}, ErrOperationAuthorization
		}
		if e.Profile != "oidc-id" || e.Code != (CodeAuthenticationEvidence{}) {
			return [32]byte{}, ErrOperationAuthorization
		}
	case "code":
		if e.AccessHashPresent != e.AccessHashChecked || e.Profile != "oidc-id" || e.Code.Transaction == [32]byte{} || e.Code.Exchange == [32]byte{} || e.Code.Nonce != e.Nonce || e.Code.PKCE == [32]byte{} {
			return [32]byte{}, ErrOperationAuthorization
		}
		switch e.Code.Flow {
		case "login", "step-up":
			if e.Code.AuthorizationID != [32]byte{} {
				return [32]byte{}, ErrOperationAuthorization
			}
		case "operation":
			if e.Code.AuthorizationID == [32]byte{} {
				return [32]byte{}, ErrOperationAuthorization
			}
		default:
			return [32]byte{}, ErrOperationAuthorization
		}
	default:
		return [32]byte{}, ErrOperationAuthorization
	}
	raw, err := authenticationEvidenceBytes(e)
	if err != nil || len(raw) > MaxAuthenticationEvidenceBytes {
		return [32]byte{}, ErrOperationAuthorization
	}
	domain := "lantern/authentication/token/v1\x00"
	if native {
		domain = "lantern/authentication/native-token/v1\x00"
	}
	if nativeRegistration {
		domain = "lantern/authentication/native-registration/v1\x00"
	}
	return sha256.Sum256(append([]byte(domain), raw...)), nil
}

func authenticationEvidenceBytes(value any) ([]byte, error) {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(value)
	return b.Bytes(), err
}
