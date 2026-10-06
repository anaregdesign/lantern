package security

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestS2CapsuleExactMeasureAndLimits(t *testing.T) {
	g, s, _, _ := s2TestHistory(t)
	encoded := s2TestEncode(t, s, g.roots)
	exact := s2TestLimits()
	exact.Bytes = uint64(len(encoded))
	b, m, err := s2EncodeCapsule(s, g.roots, exact)
	if err != nil || !bytes.Equal(encoded, b) || m.Bytes != exact.Bytes {
		t.Fatal("exact limit", m, err)
	}
	if _, err := s2DecodeCapsule(b, exact); err != nil {
		t.Fatal("decode exact", err)
	}
	exact.Bytes--
	b, m, err = s2EncodeCapsule(s, g.roots, exact)
	if !errors.Is(err, errS2Unpersistable) || b != nil || m.Bytes != uint64(len(encoded)) {
		t.Fatal("over limit", m, err)
	}
	if c, err := s2DecodeCapsule(encoded, exact); err == nil || c != nil {
		t.Fatal("decode over limit")
	}
	if _, err := s2CheckedAdd(math.MaxUint64, 1); !errors.Is(err, errS2Unpersistable) {
		t.Fatal("overflow", err)
	}
	if n, err := s2CheckedAdd(math.MaxUint64-1, 1); err != nil || n != math.MaxUint64 {
		t.Fatal("exact arithmetic", err)
	}
	for _, limits := range []s2CapsuleLimits{{}, {s2MaxCapsuleBytes + 1, 1, 1}, {1, 100001, 1}, {1, 1, 100001}} {
		if _, _, err := s2EncodeCapsule(s, g.roots, limits); err == nil {
			t.Fatal("invalid limits")
		}
	}
	count := s2TestLimits()
	count.LedgerEntries = 1
	if _, _, err := s2EncodeCapsule(s, g.roots, count); !errors.Is(err, errS2Unpersistable) {
		t.Fatal("ledger limit", err)
	}
	count = s2TestLimits()
	count.LineageEntries = 1
	if _, _, err := s2EncodeCapsule(s, g.roots, count); !errors.Is(err, errS2Unpersistable) {
		t.Fatal("lineage limit", err)
	}
}

func TestS2CapsuleMapOrderDoesNotChangeBytes(t *testing.T) {
	g, s, _, _ := s2TestHistory(t)
	before := s2TestEncode(t, s, g.roots)
	copy, err := s2DetachState(s)
	if err != nil {
		t.Fatal(err)
	}
	copy.ledger = make(map[FullChangeID]*OriginalOutcome)
	for id, o := range s.ledger {
		copy.ledger[id] = o
	}
	copy.projection.lineage = make(map[Identity]uint64)
	for id, epoch := range s.projection.lineage {
		copy.projection.lineage[id] = epoch
	}
	if !bytes.Equal(before, s2TestEncode(t, copy, g.roots)) {
		t.Fatal("map order")
	}
}

func TestS2CapsuleCompleteLedgerOverflowsWithValidS1Bounds(t *testing.T) {
	g := s2TestGenesis(s1Fixture(t, s1Image()))
	s := g.state
	// Every intent fits S1's independent operation/Image limits. Retain eight
	// large terminal-CAS intents while the visible Image remains small.
	roles := make([]Change, 8)
	for i := range roles {
		rules := make([]PermissionRule, 128)
		for j := range rules {
			prefix := strings.Repeat("x", 1021) + "<>&" // Escaping counts as real bytes.
			rules[j] = PermissionRule{ID: "rule", Effect: Allow, Action: VertexRead, Resource: DataResource, Prefix: &prefix}
		}
		roles[i] = Change{Kind: PutRole, Role: &Role{ID: "large" + string(rune('a'+i)), Rules: rules}}
	}
	op := s1Operation(t, s.projection, testIdentity(), s1Changes(roles...))
	// Advance semantics once so the large original review is stale.
	s = s1Apply(t, s, s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(s1ReaderRole())), 1, false)).State
	for i := byte(2); i < 10; i++ {
		r := s1Apply(t, s, s1Seal(g.state.projection, op, i, false))
		if r.Outcome.Disposition() != S1RejectedCAS {
			t.Fatal("terminal CAS")
		}
		s = r.State
	}
	slot, prefix := s.Position()
	ledger, floor, image := len(s.ledger), s.retiredThrough, s.projection.Image()
	encoded, measure, err := s2EncodeCapsule(s, g.roots, s2TestLimits())
	if !errors.Is(err, errS2Unpersistable) || encoded != nil || measure.Bytes <= s2MaxCapsuleBytes {
		t.Fatal("complete aggregate cap", measure, err)
	}
	if len(s.projection.snapshot.image) >= MaxImageBytes || ledger >= int(s.configuration.Capacity.LedgerEntries-s.configuration.Capacity.RestrictiveEntries) {
		t.Fatal("S1 bounds did not pass")
	}
	if s.slot != slot || s.prefix != prefix || len(s.ledger) != ledger || s.retiredThrough != floor || !bytes.Equal(s2TestJSON(t, image), s2TestJSON(t, s.projection.Image())) {
		t.Fatal("codec changed source/capacity/floor")
	}
	for _, o := range s.ledger {
		if o.commit.Slot != 1 && o.disposition != S1RejectedCAS {
			t.Fatal("replaced outcome")
		}
	}
}

func TestS2CapsuleRejectsMalformedOwnedState(t *testing.T) {
	g, s, _, _ := s2TestHistory(t)
	for _, mutate := range []func(*S1ApplyState){
		func(s *S1ApplyState) { s.configuration.Capacity.LedgerEntries = 0 },
		func(s *S1ApplyState) { s.projection.lineage[s1Bob()] = 0 },
		func(s *S1ApplyState) { s.projection.cut.Projection = [32]byte{99} },
		func(s *S1ApplyState) {
			for id := range s.ledger {
				s.ledger[id] = nil
				break
			}
		},
		func(s *S1ApplyState) {
			for _, o := range s.ledger {
				o.items = nil
				break
			}
		},
	} {
		copy, err := s2DetachState(s)
		if err != nil {
			t.Fatal(err)
		}
		mutate(copy)
		if b, _, err := s2EncodeCapsule(copy, g.roots, s2TestLimits()); err == nil || b != nil {
			t.Fatal("malformed source escaped")
		}
	}
}
