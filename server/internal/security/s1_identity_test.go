package security

import (
	"bytes"
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
