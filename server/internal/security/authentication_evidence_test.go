package security

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestAuthenticationEvidenceMaximumEnvelopeAndTimePresence(t *testing.T) {
	now := time.Date(9999, 10, 8, 0, 0, 0, 999999999, time.FixedZone("maximum-offset", 23*3600+59*60))
	// Escaping-heavy values at the supported per-field maxima must fit even with
	// both the exact purpose binding and original token identity retained.
	identity := Identity{Kind: OIDCPrincipal, Issuer: "https://idp.example/" + strings.Repeat("\u2028", (2048-len("https://idp.example/"))/3) + strings.Repeat("&", (2048-len("https://idp.example/"))%3), Subject: strings.Repeat("\x01", 255)}
	signed := AuthenticationTime{Present: true, Numeric: true, Seconds: now.Unix(), Nanoseconds: 999999999}
	event := TokenAuthenticationEvidence{Version: 1, Policy: "lantern-oidc-v1", Mode: "code", Profile: "oidc-id", Identity: identity, Algorithm: "EdDSA", KeyID: strings.Repeat("\x01", 256), Credential: [32]byte{1}, Key: [32]byte{2}, Configuration: [32]byte{3}, AudienceClient: [32]byte{4}, Generation: [16]byte{1}, ConfigRevision: 1, IssuedAt: signed, ExpiresAt: signed, AuthTime: signed, Nonce: [32]byte{5}, Code: CodeAuthenticationEvidence{Flow: "operation", AuthorizationID: [32]byte{9}, Transaction: [32]byte{6}, Exchange: [32]byte{7}, Nonce: [32]byte{5}, PKCE: [32]byte{8}, CreatedAt: now, ConsumedAt: now, ExpiresAt: now.Add(time.Minute)}}
	var full [32]byte
	for i := range full {
		full[i] = 255
	}
	event.Credential, event.Key, event.Configuration, event.AudienceClient, event.Nonce = full, full, full, full, full
	event.Code.Transaction, event.Code.Exchange, event.Code.Nonce, event.Code.PKCE, event.Code.AuthorizationID = full, full, full, full, full
	for i := range event.Generation {
		event.Generation[i] = 255
	}
	event.ConfigRevision = math.MaxUint64
	event.NotBefore = AuthenticationTime{Present: true, Numeric: true, Seconds: math.MinInt64, Nanoseconds: 999999999}
	commitment, err := event.Commitment()
	if err != nil {
		t.Fatal(err)
	}
	envelope := PurposeAuthenticationEvidence{Version: 1, Kind: "operation-approval", Binding: ManagementBinding{Actor: identity, Generation: event.Generation, Writer: full, ExpectedDigest: full, IntentDigest: full, IssuerConfigRevision: math.MaxUint64, ExpectedRevision: math.MaxUint64, ChangeID: event.Generation}, AuthorizationID: event.Code.AuthorizationID, ReviewAt: now, NotBefore: now, ApprovedAt: now, ExpiresAt: now, Event: event, EventCommitment: commitment}
	raw, err := authenticationEvidenceBytes(envelope)
	if err != nil || len(raw) > MaxAuthenticationEvidenceBytes {
		t.Fatal("maximum legal envelope does not fit", len(raw), err)
	}
	t.Logf("maximum envelope: %d / %d bytes", len(raw), MaxAuthenticationEvidenceBytes)
	for _, mutate := range []func(*TokenAuthenticationEvidence){func(e *TokenAuthenticationEvidence) { e.KeyID += "x" }, func(e *TokenAuthenticationEvidence) { e.Mode = "unknown" }, func(e *TokenAuthenticationEvidence) { e.Key = [32]byte{} }, func(e *TokenAuthenticationEvidence) { e.Code.Flow = "step-up" }, func(e *TokenAuthenticationEvidence) { e.AccessHashPresent = true }, func(e *TokenAuthenticationEvidence) { e.AuthTime.Numeric = false }, func(e *TokenAuthenticationEvidence) { e.IssuedAt.Nanoseconds = 1e9 }} {
		e := event
		mutate(&e)
		if _, err := e.Commitment(); err == nil {
			t.Fatal("unsupported event accepted")
		}
	}
	absent := AuthenticationTime{}
	null := AuthenticationTime{Present: true}
	zero := AuthenticationTime{Present: true, Numeric: true, Seconds: time.Time{}.Unix()}
	if absent == null || null == zero || !absent.Time().IsZero() || !null.Time().IsZero() || !zero.Time().IsZero() {
		t.Fatal("presence/value collapsed")
	}
	// nbf outside RFC3339's year range was accepted by the parser already.
	ancient := event
	ancient.NotBefore = AuthenticationTime{Present: true, Numeric: true, Seconds: -999999999999}
	if _, err := ancient.Commitment(); err != nil {
		t.Fatal("retention narrowed signed-date policy", err)
	}
}
