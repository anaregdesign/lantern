package security

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestS1IdentityOwnershipAndMeaning(t *testing.T) {
	s := s1Fixture(t, s1Image())
	c := s1Changes(s1ReaderRole())
	o := s1Operation(t, s.projection, testIdentity(), c)
	encoded := o.Encode()
	encoded[0]++
	c.Changes[0].Role.ID = "mutated"
	if bytes.Equal(encoded, o.Encode()) || !bytes.Contains(o.Encode(), []byte("new_reader")) {
		t.Fatal("operation retained mutable input")
	}
	for _, tc := range []struct {
		name    string
		actor   Identity
		cut     SemanticCut
		command S1Command
	}{
		{"actor", s1Bob(), s.projection.cut, s1Changes(s1ReaderRole())},
		{"review", testIdentity(), func() SemanticCut { c := s.projection.cut; c.Fences[0]++; return c }(), s1Changes(s1ReaderRole())},
		{"meaning", testIdentity(), s.projection.cut, s1Changes(Change{Kind: DeleteRole, RoleID: "reader"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			different, err := NewS1Operation(tc.actor, tc.cut, tc.command, DefaultPolicyLimits())
			if err != nil {
				t.Fatal(err)
			}
			if different == o || different.Digest() == o.Digest() {
				t.Fatal("meaning omitted")
			}
		})
	}
	for _, kind := range []string{"bootstrap.reconcile", "machine.rotate", "identity.link", "role.concurrent_import", "session.cancel", "session.step_up", "unknown"} {
		if _, err := NewS1Operation(testIdentity(), s.projection.cut, S1Command{Kind: kind}, DefaultPolicyLimits()); err == nil {
			t.Fatal("unsupported", kind)
		}
	}
	if _, err := NewS1Operation(testIdentity(), s.projection.cut, s1Changes(Change{Kind: "unknown"}), DefaultPolicyLimits()); err == nil {
		t.Fatal("unknown Change")
	}
}

func TestS1FullIDAndSemanticABA(t *testing.T) {
	s := s1Fixture(t, s1Image())
	id := s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(s1ReaderRole())), 1, false).id
	for _, bad := range []FullChangeID{{}, func() FullChangeID { x := id; x.Version++; return x }(), func() FullChangeID { x := id; x.Namespace = 0; return x }()} {
		if bad.valid() {
			t.Fatal("invalid FullID")
		}
	}
	base := s.projection.cut
	first := s1Apply(t, s, s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(s1ReaderRole())), 1, false)).State
	second := s1Apply(t, first, s1Seal(first.projection, s1Operation(t, first.projection, testIdentity(), s1Changes(Change{Kind: DeleteRole, RoleID: "new_reader"})), 2, false)).State
	if second.projection.cut.Projection != base.Projection || second.projection.cut == base || second.projection.cut.Frontier == base.Frontier {
		t.Fatal("ABA lost ancestry")
	}
	image := s1Image()
	image.Roles[1].Name = "different"
	other := s1Fixture(t, image)
	if other.projection.cut.Sequence != base.Sequence || other.projection.cut == base {
		t.Fatal("equal generation/sequence confused state")
	}
}

func TestS1RegistrationClosedVariantAndCanonicalOmission(t *testing.T) {
	legacy, err := json.Marshal(S1Command{Kind: S1RevokeSession, SessionDigest: "digest", SessionLineage: 1})
	want := `{"Kind":"session.revoke","Changes":null,"Session":null,"ReplacesDigest":"","SessionDigest":"digest","SessionLineage":1}`
	if err != nil || string(legacy) != want {
		t.Fatal("older command transcript changed", string(legacy), err)
	}
	registration := &PrincipalRegistration{UserUID: "11111111111111111111111111111111", ClientProfileID: "android", IssuerConfigRevision: 1}
	for _, kind := range []string{S1Management, S1IssueSession, S1RevokeSession, "unknown"} {
		if validateS1Command(S1Command{Kind: kind, Registration: registration}, DefaultPolicyLimits()) == nil {
			t.Fatal("registration field crossed command boundary")
		}
	}
	if validateS1Command(S1Command{Kind: S1RegisterPrincipal, Registration: registration}, DefaultPolicyLimits()) != nil || ordinaryS1Command(S1RegisterPrincipal) {
		t.Fatal("registration command contract")
	}
	for _, change := range []func(*S1Command){
		func(c *S1Command) { c.Registration = nil },
		func(c *S1Command) { c.Changes = []Change{{Kind: RevokeSessions}} },
		func(c *S1Command) { c.Session = &Session{} },
		func(c *S1Command) { c.SessionLineage = 1 },
		func(c *S1Command) { c.SessionDigest = "extra" },
		func(c *S1Command) { c.ReplacesDigest = "extra" },
	} {
		c := S1Command{Kind: S1RegisterPrincipal, Registration: registration}
		change(&c)
		if validateS1Command(c, DefaultPolicyLimits()) == nil {
			t.Fatal("competing command payload accepted")
		}
	}
}
