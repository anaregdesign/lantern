package security

import (
	"encoding/binary"
	"encoding/json"
	"testing"
)

func s2TestLimits() s2CapsuleLimits { return s2CapsuleLimits{s2MaxCapsuleBytes, 100000, 100000} }

func s2TestGenesis(s *S1ApplyState) *s2TrustedGenesis {
	// Simulates an INDEPENDENT trusted root, never obtained by decoding data.
	return &s2TrustedGenesis{s, s2CapsuleRoots{Genesis: s1Digest("test-independent-genesis", struct {
		Cut                       SemanticCut
		Membership, Configuration [32]byte
	}{s.projection.cut, s.membership, s.configuration.Digest()})}}
}

func s2TestEncode(t *testing.T, s *S1ApplyState, roots s2CapsuleRoots) []byte {
	t.Helper()
	encoded, measure, err := s2EncodeCapsule(s, roots, s2TestLimits())
	if err != nil || uint64(len(encoded)) != measure.Bytes {
		t.Fatal("encode/measure", measure, err)
	}
	return encoded
}

func s2TestHistory(t *testing.T) (*s2TrustedGenesis, *S1ApplyState, []S1CertifiedNext, *S1Handoff) {
	t.Helper()
	g := s2TestGenesis(s1Fixture(t, s1Image()))
	s := g.state
	var replay []S1CertifiedNext
	apply := func(h *S1Handoff) S1ApplyResult {
		next := s1Next(s, h)
		r, err := ApplyS1(s, next)
		if err != nil {
			t.Fatal(err)
		}
		replay = append(replay, next)
		s = r.State
		return r
	}
	bob := s1Bob()
	apply(s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(Change{Kind: RevokeSessions, Identity: &bob})), 1, false))
	secondRole := s1ReaderRole()
	secondRole.Role.ID = "second_reader"
	original := s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(s1ReaderRole(), secondRole)), 2, false)
	if apply(original).Outcome.Disposition() != S1Applied {
		t.Fatal("success fixture")
	}
	grant := s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(Change{Kind: PutAssignment, Identity: &bob, RoleID: "security_admin"})), 3, false)
	if apply(grant).Outcome.Disposition() != S1RejectedPurpose {
		t.Fatal("terminal fixture")
	}
	variant := *original
	a := *original.authorization
	a.credentialEvidence = [32]byte{44}
	variant.authorization, variant.origin, variant.serial = &a, [32]byte{45}, 2
	if apply(&variant).Status != S1Replay {
		t.Fatal("variant fixture")
	}
	conflict := s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(Change{Kind: RevokeSessions, Identity: &bob})), 2, false)
	if apply(conflict).Status != S1IDConflict {
		t.Fatal("conflict fixture")
	}
	if apply(nil).Status != S1Noop {
		t.Fatal("noop fixture")
	}
	stale := *original
	stale.id.Nonce = [16]byte{4}
	if apply(&stale).Outcome.Disposition() != S1RejectedCAS {
		t.Fatal("CAS fixture")
	}
	return g, s, replay, original
}

// Test-only framing helpers edit untrusted bytes without constructing trust.
type s2TestParts struct {
	header, image   []byte
	lineage, ledger [][]byte
}

func s2TestSplit(t *testing.T, encoded []byte) s2TestParts {
	t.Helper()
	r := s2CapsuleReader{encoded[len(s2CapsuleMagic):]}
	header, err := r.record()
	if err != nil {
		t.Fatal(err)
	}
	image, err := r.record()
	if err != nil {
		t.Fatal(err)
	}
	p := s2TestParts{header: append([]byte(nil), header...), image: append([]byte(nil), image...)}
	read := func() [][]byte {
		n, err := r.word()
		if err != nil {
			t.Fatal(err)
		}
		var rows [][]byte
		for i := uint32(0); i < n; i++ {
			row, err := r.record()
			if err != nil {
				t.Fatal(err)
			}
			rows = append(rows, append([]byte(nil), row...))
		}
		return rows
	}
	p.lineage, p.ledger = read(), read()
	if len(r.remaining) != 0 {
		t.Fatal("remaining")
	}
	return p
}

func (p s2TestParts) encode() []byte {
	b := []byte(s2CapsuleMagic)
	record := func(row []byte) { b = binary.BigEndian.AppendUint32(b, uint32(len(row))); b = append(b, row...) }
	record(p.header)
	record(p.image)
	for _, rows := range [][][]byte{p.lineage, p.ledger} {
		b = binary.BigEndian.AppendUint32(b, uint32(len(rows)))
		for _, row := range rows {
			record(row)
		}
	}
	return b
}

func s2TestJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
