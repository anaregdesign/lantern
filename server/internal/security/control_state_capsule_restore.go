package security

import (
	"bytes"
	"errors"
)

var errS2Restore = errors.New("S2 capsule differs from independently trusted replay")

// NO production constructor exists. A future authentic historical verifier
// must supply independently trusted exact genesis and any matching retirement
// root. Decoder data, a caller bool/hash or a standalone later-slot QC cannot
// produce this capability. Package-private tests use explicit trusted fixtures.
type s2TrustedGenesis struct {
	state *S1ApplyState
	roots s2CapsuleRoots
}

// Clone by recompiling the whole policy/admin closure and owning all mutable
// bytes/maps/item slices. It preserves cut, lineage, ledger, control and floor;
// NewS1Projection/NewS1ApplyState are not checkpoint restore shortcuts.
func s2DetachState(s *S1ApplyState) (*S1ApplyState, error) {
	snapshot, err := CompileImage(s.projection.Image(), s.configuration.Policy)
	if err != nil {
		return nil, errS2Restore
	}
	p := &S1Projection{snapshot: snapshot, cut: s.projection.cut, lineage: make(map[Identity]uint64, len(s.projection.lineage))}
	for id, epoch := range s.projection.lineage {
		p.lineage[id] = epoch
	}
	copy := *s
	copy.projection = p
	copy.ledger = make(map[FullChangeID]*OriginalOutcome, len(s.ledger))
	for id, original := range s.ledger {
		o := *original
		o.items = append([]S1ItemOutcome(nil), original.items...)
		copy.ledger[id] = &o
	}
	return &copy, nil
}

func s2RestoreCapsule(candidate *s2DecodedCandidate, genesis *s2TrustedGenesis, replay []S1CertifiedNext, limits s2CapsuleLimits) (*S1ApplyState, error) {
	if candidate == nil || genesis == nil || genesis.state == nil || !limits.valid() {
		return nil, errS2Restore
	}
	g := genesis.state
	// Trust is an independently supplied input, not inferred from slot zero.
	// Still require the exact initial S1 shape and complete consistent encoding.
	if g.slot != 0 || len(g.ledger) != 0 || g.projection == nil || g.projection.cut.Sequence != 1 || g.projection.cut.Previous != [32]byte{} {
		return nil, errS2Restore
	}
	if _, _, err := s2EncodeCapsule(g, genesis.roots, limits); err != nil {
		return nil, errS2Restore
	}
	if _, err := s2DecodeCapsule([]byte(candidate.canonical), limits); err != nil {
		return nil, errS2Restore
	}
	state, err := s2DetachState(g)
	if err != nil {
		return nil, err
	}
	for _, next := range replay {
		// These are already opaque verified inputs. Apply enforces exact scoped
		// contiguous predecessor, configuration and original ownership semantics.
		result, err := ApplyS1(state, next)
		if err != nil {
			return nil, errS2Restore
		}
		state = result.State
	}
	expected, _, err := s2EncodeCapsule(state, genesis.roots, limits)
	if err != nil || !bytes.Equal(expected, []byte(candidate.canonical)) {
		return nil, errS2Restore
	}
	// Only the independently replayed state escapes, never decoded structures.
	return s2DetachState(state)
}
