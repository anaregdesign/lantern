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
