package security

import (
	"testing"
	"time"
)

func TestRegistrationHistoricalClosedVariantAndApply(t *testing.T) {
	s, view, event := registrationTestView(t, registrationImage())
	c, err := captureRegistrationCredential(view, CurrentCredentialFacts{Token: &event})
	if err != nil {
		t.Fatal(err)
	}
	command := S1Command{Kind: S1RegisterPrincipal, Registration: &PrincipalRegistration{UserUID: "11111111111111111111111111111111", ClientProfileID: "android", IssuerConfigRevision: 1}}
	op := s1Operation(t, s.projection, event.Identity, command)
	claim := c.claim
	claim.Registration = &authorityRegistrationAuthorization{Version: 1, State: c.state, ClientProfileID: "android"}
	low, high, _ := view.TimeBounds()
	h := authorityHistoricalHeader{Credential: claim, Authentication: Authentication{Provenance: NativeProviderRegistration, Class: EndUser, IssuerConfigRevision: 1}, Lineage: c.lineage,
		Time: authorityConsumeTime{UTCLow: uint64(low.UnixNano()), UTCHigh: uint64(high.UnixNano())}, CredentialDeadline: c.claim.AdmissionDeadline}
	if !verifyAuthorityCredential(h, op, command) {
		t.Fatal("closed registration rejected")
	}
	for name, edit := range map[string]func(*authorityHistoricalHeader){
		"ordinary provenance": func(h *authorityHistoricalHeader) { h.Authentication.Provenance = NativeProviderLogin },
		"no variant":          func(h *authorityHistoricalHeader) { h.Credential.Registration = nil },
		"fabricated lineage":  func(h *authorityHistoricalHeader) { h.Lineage = 1 },
		"evidence digest":     func(h *authorityHistoricalHeader) { h.Credential.Verification[0] ^= 1 },
		"purpose":             func(h *authorityHistoricalHeader) { h.PurposeBinding = [32]byte{1} },
		"expired":             func(h *authorityHistoricalHeader) { h.Time.UTCHigh = uint64(event.Native.ExpiresAt.UnixNano()) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := h
			edit(&bad)
			if verifyAuthorityCredential(bad, op, command) {
				t.Fatal("invalid closed authorization accepted")
			}
		})
	}
	if verifyAuthorityCredential(h, op, s1Changes(s1ReaderRole())) {
		t.Fatal("registration authorized management")
	}
	sealed := s1Seal(s.projection, op, 1, false)
	sealed.authorization = &s1VerifiedAuthorization{authentication: h.Authentication, lineage: h.Lineage, credentialEvidence: [32]byte{1}, consume: high, credentialDeadline: h.CredentialDeadline, registration: claim.Registration}
	if !sealed.valid(s.projection.cut) {
		t.Fatal("unknown Principal's closed handoff rejected")
	}
	if s1CurrentAuthority(s.projection, sealed, command) {
		t.Fatal("registration used ordinary S1 authority")
	}
	result := s1Apply(t, s, sealed)
	if result.Outcome == nil || result.Outcome.Disposition() != S1Applied || result.State.projection.lineage[event.Identity] != 1 {
		t.Fatal("atomic registration not applied")
	}
	original := authorityRegistrationOriginal{ID: sealed.id, Intent: op.digest, Commit: result.Outcome.Commit(), UserUID: command.Registration.UserUID}
	if !registrationOriginalMatches(result.State, event.Identity, &original) {
		t.Fatal("applied original not recognized")
	}
	changed := *result.State
	projection := *changed.projection
	projection.cut.Sequence++
	changed.projection = &projection
	if registrationOriginalMatches(&changed, event.Identity, &original) {
		t.Fatal("session transition crossed later policy cut")
	}
	bad := original
	bad.Intent[0] ^= 1
	if registrationOriginalMatches(result.State, event.Identity, &bad) {
		t.Fatal("foreign original accepted")
	}
	// Ordinary handoffs retain the nonzero-lineage requirement.
	ordinary := s1Seal(s.projection, s1Operation(t, s.projection, event.Identity, s1Changes(s1ReaderRole())), 2, false)
	ordinary.authorization.consume = high
	ordinary.authorization.credentialDeadline = high.Add(time.Minute)
	if ordinary.valid(s.projection.cut) {
		t.Fatal("unknown identity gained ordinary authority")
	}
}
