package security

import (
	"bytes"
	"errors"
	"testing"
)

func TestS2RestoreCompleteReplayAndOriginalOwnership(t *testing.T) {
	g, s, replay, original := s2TestHistory(t)
	encoded := s2TestEncode(t, s, g.roots)
	candidate, err := s2DecodeCapsule(encoded, s2TestLimits())
	if err != nil {
		t.Fatal(err)
	}
	restored, err := s2RestoreCapsule(candidate, g, replay, s2TestLimits())
	if err != nil || restored == nil {
		t.Fatal("restore", err)
	}
	if !bytes.Equal(encoded, s2TestEncode(t, restored, g.roots)) {
		t.Fatal("incomplete restoration")
	}
	if restored.projection.SessionLineage(s1Bob()) != 2 || len(restored.projection.Image().Sessions) != 0 {
		t.Fatal("lost lineage-only revocation")
	}
	_, before := s.Lookup(original.id)
	retry := s1Apply(t, restored, original)
	if retry.Status != S1Replay || retry.Outcome.commit != before.commit || retry.Outcome.handoff != before.handoff || retry.Outcome.operation != before.operation || !bytes.Equal(s2TestJSON(t, retry.Outcome.Items()), s2TestJSON(t, before.Items())) {
		t.Fatal("first ownership moved to retry")
	}
	if retry.State.projection.cut != restored.projection.cut || retry.State.slot != restored.slot+1 {
		t.Fatal("control/semantic distinction")
	}
}

func TestS2RestoreRejectsSelfConsistentComponentSubstitution(t *testing.T) {
	g, s, replay, _ := s2TestHistory(t)
	tests := []struct {
		name   string
		mutate func(*S1ApplyState, *s2CapsuleRoots)
	}{
		{"prefix", func(s *S1ApplyState, _ *s2CapsuleRoots) { s.prefix = [32]byte{90} }},
		{"semantic-ancestry", func(s *S1ApplyState, _ *s2CapsuleRoots) { s.projection.cut.Previous = [32]byte{91} }},
		{"semantic-frontier", func(s *S1ApplyState, _ *s2CapsuleRoots) { s.projection.cut.Frontier = [32]byte{92} }},
		{"lineage", func(s *S1ApplyState, _ *s2CapsuleRoots) {
			s.projection.lineage[s1Bob()]++
			s.projection.cut.Projection = s.projection.projectionDigest()
		}},
		{"original-H", func(s *S1ApplyState, _ *s2CapsuleRoots) {
			for _, o := range s.ledger {
				o.handoff = [32]byte{93}
				o.commit.Value = o.handoff
				break
			}
		}},
		{"missing-original-outcome", func(s *S1ApplyState, _ *s2CapsuleRoots) {
			for id := range s.ledger {
				delete(s.ledger, id)
				break
			}
		}},
		{"original-ID", func(s *S1ApplyState, _ *s2CapsuleRoots) {
			for id, o := range s.ledger {
				delete(s.ledger, id)
				o.id.Nonce = [16]byte{94}
				s.ledger[o.id] = o
				break
			}
		}},
		{"membership", func(s *S1ApplyState, _ *s2CapsuleRoots) {
			s.membership = [32]byte{95}
			for _, o := range s.ledger {
				o.commit.Membership = s.membership
			}
		}},
		{"genesis-identity", func(_ *S1ApplyState, roots *s2CapsuleRoots) { roots.Genesis = [32]byte{96} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copy, err := s2DetachState(s)
			if err != nil {
				t.Fatal(err)
			}
			roots := g.roots
			test.mutate(copy, &roots)
			candidate, err := s2DecodeCapsule(s2TestEncode(t, copy, roots), s2TestLimits())
			if err != nil {
				t.Fatal("self-consistent candidate", err)
			}
			if state, err := s2RestoreCapsule(candidate, g, replay, s2TestLimits()); !errors.Is(err, errS2Restore) || state != nil {
				t.Fatal("substitution restored", err)
			}
		})
	}
	// An attacker can select a fully valid genesis image/generation/fence. A
	// self-consistent slot-zero capsule still cannot replace the trusted root.
	for _, field := range []string{"image", "generation", "fences"} {
		t.Run(field, func(t *testing.T) {
			image := s1Image()
			generation, fences := g.state.projection.cut.Generation, g.state.projection.cut.Fences
			switch field {
			case "image":
				image.Roles[0].Name = "substituted"
			case "generation":
				generation = [16]byte{77}
			case "fences":
				fences = [32]byte{78}
			}
			p, err := NewS1Projection(image, g.state.configuration.Policy, g.state.projection.cut.Domain, g.state.projection.cut.Cohort, fences, generation)
			if err != nil {
				t.Fatal(err)
			}
			state, err := NewS1ApplyState(p, g.state.membership, g.state.configuration.Capacity, S1Retention{})
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := s2DecodeCapsule(s2TestEncode(t, state, g.roots), s2TestLimits())
			if err != nil {
				t.Fatal(err)
			}
			if restored, err := s2RestoreCapsule(candidate, g, nil, s2TestLimits()); err == nil || restored != nil {
				t.Fatal("attacker genesis restored")
			}
		})
	}
}

func TestS2RestoreBindsEveryActualExecutionField(t *testing.T) {
	g := s2TestGenesis(s1Fixture(t, s1Image()))
	tests := []struct {
		name   string
		mutate func(*S1ExecutionConfig)
	}{
		{"roles", func(c *S1ExecutionConfig) { c.Policy.MaxRoles-- }},
		{"rules", func(c *S1ExecutionConfig) { c.Policy.MaxRules-- }},
		{"prefix-bytes", func(c *S1ExecutionConfig) { c.Policy.MaxPrefixBytes-- }},
		{"policy-bytes", func(c *S1ExecutionConfig) { c.Policy.MaxTotalBytes-- }},
		{"assignments", func(c *S1ExecutionConfig) { c.Policy.MaxAssignments-- }},
		{"ledger-entries", func(c *S1ExecutionConfig) { c.Capacity.LedgerEntries-- }},
		{"restrictive-entries", func(c *S1ExecutionConfig) { c.Capacity.RestrictiveEntries-- }},
		{"image-bytes", func(c *S1ExecutionConfig) { c.Capacity.ImageBytes-- }},
		{"restrictive-image-bytes", func(c *S1ExecutionConfig) { c.Capacity.RestrictiveImageBytes-- }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := g.state.configuration
			test.mutate(&config)
			cut := g.state.projection.cut
			p, err := NewS1Projection(g.state.projection.Image(), config.Policy, cut.Domain, cut.Cohort, cut.Fences, cut.Generation)
			if err != nil {
				t.Fatal(err)
			}
			s, err := NewS1ApplyState(p, g.state.membership, config.Capacity, S1Retention{})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(s.projection.snapshot.image, g.state.projection.snapshot.image) {
				t.Fatal("image differs")
			}
			candidate, err := s2DecodeCapsule(s2TestEncode(t, s, g.roots), s2TestLimits())
			if err != nil {
				t.Fatal(err)
			}
			if restored, err := s2RestoreCapsule(candidate, g, nil, s2TestLimits()); err == nil || restored != nil {
				t.Fatal("configuration replaced by defaults")
			}
		})
	}
}

func TestS2RestoreRequiresIndependentFixedRetirementRoot(t *testing.T) {
	base := s1Fixture(t, s1Image())
	cut := base.projection.cut
	retention := S1Retention{retiredThrough: 4, domain: cut.Domain, cohort: cut.Cohort, checkpoint: [32]byte{80}}
	s, err := NewS1ApplyState(base.projection, base.membership, base.configuration.Capacity, retention)
	if err != nil {
		t.Fatal(err)
	}
	g := s2TestGenesis(s)
	g.roots.Retirement = retention.checkpoint
	op := s1Operation(t, s.projection, testIdentity(), s1Changes(s1ReaderRole()))
	h := s1Seal(s.projection, op, 1, false)
	h.id.Namespace = 5
	next := s1Next(s, h)
	r, err := ApplyS1(s, next)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := s2DecodeCapsule(s2TestEncode(t, r.State, g.roots), s2TestLimits())
	if err != nil {
		t.Fatal(err)
	}
	restored, err := s2RestoreCapsule(candidate, g, []S1CertifiedNext{next}, s2TestLimits())
	if err != nil {
		t.Fatal(err)
	}
	id := h.id
	id.Namespace = 4
	if status, _ := restored.Lookup(id); status != S1Retired {
		t.Fatal("lost fixed floor")
	}
	id.Namespace = 6
	if status, _ := restored.Lookup(id); status != S1Unresolved {
		t.Fatal("invented retirement")
	}
	for _, trusted := range []*s2TrustedGenesis{s2TestGenesis(base), {s, s2CapsuleRoots{Genesis: g.roots.Genesis, Retirement: [32]byte{81}}}} {
		if state, err := s2RestoreCapsule(candidate, trusted, []S1CertifiedNext{next}, s2TestLimits()); err == nil || state != nil {
			t.Fatal("untrusted floor accepted")
		}
	}
}

func TestS2RestoreProofSeparationAndContiguousReplay(t *testing.T) {
	g, s, replay, _ := s2TestHistory(t)
	candidate, err := s2DecodeCapsule(s2TestEncode(t, s, g.roots), s2TestLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, inputs := range [][]S1CertifiedNext{nil, replay[1:], replay[:len(replay)-1], {replay[1]}, {{}}, {replay[0], replay[0]}} {
		if restored, err := s2RestoreCapsule(candidate, g, inputs, s2TestLimits()); err == nil || restored != nil {
			t.Fatal("gap/unverified/standalone witness restored")
		}
	}
	for _, trusted := range []*s2TrustedGenesis{nil, {}, {state: s}} {
		if state, err := s2RestoreCapsule(candidate, trusted, replay, s2TestLimits()); err == nil || state != nil {
			t.Fatal("missing/late trusted genesis")
		}
	}
	if state, err := s2RestoreCapsule(nil, g, replay, s2TestLimits()); err == nil || state != nil {
		t.Fatal("nil candidate")
	}
}

func TestS2RestoreDetachedBytesItemsAndMaps(t *testing.T) {
	g, s, replay, original := s2TestHistory(t)
	encoded := s2TestEncode(t, s, g.roots)
	candidate, err := s2DecodeCapsule(encoded, s2TestLimits())
	if err != nil {
		t.Fatal(err)
	}
	encoded[0] ^= 1
	for _, o := range s.ledger {
		o.items[0].Kind = "source mutation"
		break
	}
	a, err := s2RestoreCapsule(candidate, g, replay, s2TestLimits())
	if err != nil {
		t.Fatal(err)
	}
	b, err := s2RestoreCapsule(candidate, g, replay, s2TestLimits())
	if err != nil {
		t.Fatal(err)
	}
	_, outcome := a.Lookup(original.id)
	items := outcome.Items()
	items[0].Kind = "getter mutation"
	opbytes := outcome.Operation().Encode()
	opbytes[0] ^= 1
	image := a.projection.Image()
	image.Roles[0].Name = "getter mutation"
	outcome.items[0].Kind = "state mutation"
	a.projection.lineage[s1Bob()]++
	a.projection.snapshot.image[0] ^= 1
	delete(a.ledger, original.id)
	if !bytes.Equal([]byte(candidate.canonical), s2TestEncode(t, b, g.roots)) {
		t.Fatal("shared decoded/source/restored buffers or maps")
	}
	if _, o := b.Lookup(original.id); o.commit.Slot != 2 || o.items[0].Kind != PutRole {
		t.Fatal("shared outcome")
	}
}

func TestS2RestorePreservesDifferentOriginalCASReview(t *testing.T) {
	g := s2TestGenesis(s1Fixture(t, s1Image()))
	for _, field := range []string{"generation", "fences", "future-sequence", "policy"} {
		t.Run(field, func(t *testing.T) {
			reviewed := g.state.projection.cut
			policy := g.state.configuration.Policy
			switch field {
			case "generation":
				reviewed.Generation = [16]byte{61}
			case "fences":
				reviewed.Fences = [32]byte{62}
			case "future-sequence":
				reviewed.Sequence = 999
			case "policy":
				policy.MaxPrefixBytes--
				reviewed.Policy = s1PolicyConfiguration(policy)
			}
			op, err := NewS1Operation(testIdentity(), reviewed, s1Changes(s1ReaderRole()), policy)
			if err != nil {
				t.Fatal(err)
			}
			h := s1Seal(g.state.projection, op, 1, false)
			next := s1Next(g.state, h)
			r, err := ApplyS1(g.state, next)
			if err != nil || r.Outcome.Disposition() != S1RejectedCAS {
				t.Fatal("legal S1 terminal", err)
			}
			candidate, err := s2DecodeCapsule(s2TestEncode(t, r.State, g.roots), s2TestLimits())
			if err != nil {
				t.Fatal(err)
			}
			state, err := s2RestoreCapsule(candidate, g, []S1CertifiedNext{next}, s2TestLimits())
			if err != nil {
				t.Fatal(err)
			}
			_, original := state.Lookup(h.id)
			if original.Observed() != reviewed || original.Operation() != op {
				t.Fatal("rewrote original review")
			}
		})
	}
}
