package security

import (
	"bytes"
	"testing"
	"time"
)

func TestNativeEvidenceDomainAndLegacyEncoding(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	signed := AuthenticationTime{Present: true, Numeric: true, Seconds: now.Unix()}
	legacy := TokenAuthenticationEvidence{Version: 1, Policy: "lantern-oidc-v1", Mode: "id", Profile: "oidc-id", Identity: Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "existing"}, Algorithm: "RS256", KeyID: "key", Credential: [32]byte{1}, Key: [32]byte{2}, Configuration: [32]byte{3}, AudienceClient: [32]byte{4}, Generation: [16]byte{1}, ConfigRevision: 1, IssuedAt: signed, ExpiresAt: signed}
	raw, err := authenticationEvidenceBytes(legacy)
	if err != nil || bytes.Contains(raw, []byte(`"Native"`)) {
		t.Fatal("historical transcript changed", err)
	}
	old, err := legacy.Commitment()
	if err != nil {
		t.Fatal(err)
	}
	native := legacy
	native.Version = 2
	native.Policy = "lantern-native-login-v1"
	native.Mode = "native-login"
	native.Profile = NativeEndUserProfile
	native.Native = NativeAuthenticationEvidence{Kind: GoogleIDToken, ClientProfileID: "mobile", ServiceOrigin: "https://lantern.example", NoncePolicy: "google-initialize-unbound", Client: [32]byte{1}, Transaction: [32]byte{2}, CreatedAt: now, ConsumedAt: now, ExpiresAt: now.Add(NativeLoginLifetime)}
	digest, err := native.Commitment()
	if err != nil || digest == old {
		t.Fatal("native domain", err)
	}
	raw, err = authenticationEvidenceBytes(native)
	if err != nil || bytes.Contains(raw, []byte(`"Registration"`)) {
		t.Fatal("historical native transcript changed", err)
	}
	for _, purpose := range []NativeRegistrationPurpose{{Kind: "registration"}, {Kind: "registration-recovery", Original: [32]byte{7}}} {
		e := native
		e.Version, e.Policy, e.Mode, e.Native.Registration = 3, "lantern-native-registration-v1", "native-registration", purpose
		registered, err := e.Commitment()
		if err != nil || registered == digest {
			t.Fatal("registration purpose domain", err)
		}
		for _, mutate := range []func(*TokenAuthenticationEvidence){
			func(e *TokenAuthenticationEvidence) { e.Version = 2 },
			func(e *TokenAuthenticationEvidence) { e.Policy = "lantern-native-login-v1" },
			func(e *TokenAuthenticationEvidence) { e.Mode = "native-login" },
			func(e *TokenAuthenticationEvidence) { e.Native.Registration = NativeRegistrationPurpose{} },
			func(e *TokenAuthenticationEvidence) { e.Native.Registration.Kind = "operation" },
		} {
			bad := e
			mutate(&bad)
			if _, err := bad.Commitment(); err == nil {
				t.Fatal("purpose/version confusion accepted")
			}
		}
		bad := native
		bad.Native.Registration = purpose
		if _, err := bad.Commitment(); err == nil {
			t.Fatal("ordinary native evidence accepted a registration purpose")
		}
	}
	for _, mutate := range []func(*TokenAuthenticationEvidence){
		func(e *TokenAuthenticationEvidence) { e.Version = 1 }, func(e *TokenAuthenticationEvidence) { e.Native.NonceChecked = true },
		func(e *TokenAuthenticationEvidence) { e.Native.Transaction = [32]byte{} }, func(e *TokenAuthenticationEvidence) { e.Code.Flow = "login" },
		func(e *TokenAuthenticationEvidence) { e.Native.ConsumedAt = e.Native.ExpiresAt }, func(e *TokenAuthenticationEvidence) { e.Native.Exchange = [32]byte{1} },
	} {
		e := native
		mutate(&e)
		if _, err := e.Commitment(); err == nil {
			t.Fatal("invalid native evidence accepted")
		}
	}
	legacy.Native = native.Native
	if _, err := legacy.Commitment(); err == nil {
		t.Fatal("native evidence relabeled as legacy")
	}
}

func TestNativeRegistrationPurposeClosedShape(t *testing.T) {
	for _, purpose := range []NativeRegistrationPurpose{
		{}, {Kind: "login"}, {Kind: "registration", Original: [32]byte{1}}, {Kind: "registration-recovery"},
	} {
		if purpose.Valid() {
			t.Fatal("invalid purpose accepted", purpose.Kind)
		}
	}
}
