package security

import (
	"crypto/sha256"
	"encoding/json"
	"time"
)

const NativeLoginLifetime = 10 * time.Minute

// NativeRegistrationPurpose binds the exceptional authentication use before
// provider verification. The zero value denotes ordinary native login. Original
// commits a complete original review for recovery; it never authorizes replay.
type NativeRegistrationPurpose struct {
	Kind     string
	Original [32]byte
}

func (p NativeRegistrationPurpose) Valid() bool {
	switch p.Kind {
	case "registration":
		return p.Original == [32]byte{}
	case "registration-recovery":
		return p.Original != [32]byte{}
	default:
		return false
	}
}

// NativeAuthenticationEvidence describes a consumed app transaction. Provider
// tokens, completion secrets, state, nonce and authorization codes never appear
// in this detached record. It is factual input, not a verifier capability.
type NativeAuthenticationEvidence struct {
	Kind, ClientProfileID, ServiceOrigin, NoncePolicy string
	Client, Transaction                               [32]byte
	CreatedAt, ExpiresAt, ConsumedAt                  time.Time
	NonceChecked, CodeHashPresent, CodeHashChecked    bool
	// Exchange commits the successful, one-time Apple code request and token
	// response. ExchangeEvidence commits the independently verified ID token.
	Exchange, ExchangeEvidence                                               [32]byte
	ExchangeIssuedAt, ExchangeNotBefore, ExchangeExpiresAt, ExchangeAuthTime AuthenticationTime
	ExchangeCodeHashPresent, ExchangeCodeHashChecked                         bool
	ExchangeAccessHashPresent, ExchangeAccessHashChecked                     bool
	// Absent on every historical native-login transcript.
	Registration NativeRegistrationPurpose `json:",omitzero"`
}

// NativeClientCommitment binds every committed profile field, including the
// private binding's reference (never its value). It grants no authority itself.
func NativeClientCommitment(c NativeClient) [32]byte {
	raw, err := json.Marshal(c)
	if err != nil {
		return [32]byte{}
	}
	return sha256.Sum256(append([]byte("lantern/native-client/v1\x00"), raw...))
}

func (n NativeAuthenticationEvidence) valid(e TokenAuthenticationEvidence) bool {
	if e.Profile != NativeEndUserProfile || e.Algorithm != "RS256" || e.Code != (CodeAuthenticationEvidence{}) || e.AccessHashChecked ||
		!validRoleID(n.ClientProfileID) || !nativeServiceOrigin(n.ServiceOrigin) || n.Client == [32]byte{} || n.Transaction == [32]byte{} ||
		n.CreatedAt.IsZero() || n.ConsumedAt.Before(n.CreatedAt) || !n.ConsumedAt.Before(n.ExpiresAt) || n.ExpiresAt.Sub(n.CreatedAt) > NativeLoginLifetime ||
		n.CodeHashPresent != n.CodeHashChecked {
		return false
	}
	if e.Mode == "native-login" {
		if n.Registration != (NativeRegistrationPurpose{}) {
			return false
		}
	} else if e.Mode != "native-registration" || !n.Registration.Valid() {
		return false
	}
	switch n.Kind {
	case GoogleIDToken:
		return e.Identity.Issuer == GoogleIssuer && n.NoncePolicy == "google-initialize-unbound" && !n.NonceChecked && !n.CodeHashPresent &&
			n.Exchange == [32]byte{} && n.ExchangeEvidence == [32]byte{} && n.ExchangeIssuedAt == (AuthenticationTime{}) && n.ExchangeNotBefore == (AuthenticationTime{}) &&
			n.ExchangeExpiresAt == (AuthenticationTime{}) && n.ExchangeAuthTime == (AuthenticationTime{}) && !n.ExchangeCodeHashPresent && !n.ExchangeCodeHashChecked &&
			!n.ExchangeAccessHashPresent && !n.ExchangeAccessHashChecked
	case AppleNative, AppleAndroidWeb:
		if e.Identity.Issuer != AppleIssuer || n.NoncePolicy != "apple-raw" || !n.NonceChecked || e.Nonce == [32]byte{} ||
			n.Exchange == [32]byte{} || n.ExchangeEvidence == [32]byte{} || n.ExchangeCodeHashPresent != n.ExchangeCodeHashChecked ||
			n.ExchangeAccessHashPresent != n.ExchangeAccessHashChecked || !n.ExchangeIssuedAt.Present || !n.ExchangeIssuedAt.Numeric || !n.ExchangeExpiresAt.Present || !n.ExchangeExpiresAt.Numeric {
			return false
		}
		for _, claim := range []AuthenticationTime{n.ExchangeIssuedAt, n.ExchangeNotBefore, n.ExchangeExpiresAt, n.ExchangeAuthTime} {
			if claim.Nanoseconds < 0 || claim.Nanoseconds >= 1e9 || !claim.Present && claim.Numeric || !claim.Numeric && (claim.Seconds != 0 || claim.Nanoseconds != 0) {
				return false
			}
		}
		return !n.ExchangeAuthTime.Present || n.ExchangeAuthTime.Numeric
	default:
		return false
	}
}

// NativeBindingMatches validates the native profile against this exact Issuer
// revision. Session expiry and current Principal/Role authorization are separate.
func (e TokenAuthenticationEvidence) NativeBindingMatches(issuer Issuer) bool {
	n := e.Native
	c, ok := issuer.NativeClient(n.ClientProfileID)
	return ok && e.Identity.Issuer == issuer.URL && e.ConfigRevision == issuer.ConfigRevision && n.Kind == c.Kind &&
		n.ServiceOrigin == c.ServiceOrigin && n.Client == NativeClientCommitment(c)
}

func nativeEvidenceExpiry(e TokenAuthenticationEvidence) time.Time {
	expires := authorityMinTime(e.ExpiresAt.Time(), e.Native.ExpiresAt)
	if e.Native.ExchangeExpiresAt.Numeric {
		expires = authorityMinTime(expires, e.Native.ExchangeExpiresAt.Time())
	}
	return expires
}

func nativeExchangeTimesValid(n NativeAuthenticationEvidence, low time.Time) bool {
	if n.Kind == GoogleIDToken {
		return true
	}
	return n.ExchangeIssuedAt.Numeric && n.ExchangeExpiresAt.Numeric && n.ExchangeExpiresAt.Time().After(n.ExchangeIssuedAt.Time()) &&
		!low.Before(n.ExchangeIssuedAt.Time()) && (!n.ExchangeNotBefore.Numeric || !low.Before(n.ExchangeNotBefore.Time())) &&
		(!n.ExchangeAuthTime.Numeric || !n.ExchangeAuthTime.Time().After(n.ExchangeIssuedAt.Time()) && !low.Before(n.ExchangeAuthTime.Time()))
}
