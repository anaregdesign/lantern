package security

import (
	"errors"
	"testing"
)

func TestS1ExecutionConfigurationCommitsAllInputs(t *testing.T) {
	base := s1Fixture(t, s1Image())
	initial := base.Configuration()
	for _, tc := range []struct {
		name   string
		change func(*S1ExecutionConfig)
	}{
		{"max roles", func(c *S1ExecutionConfig) { c.Policy.MaxRoles-- }},
		{"max rules", func(c *S1ExecutionConfig) { c.Policy.MaxRules-- }},
		{"max prefix bytes", func(c *S1ExecutionConfig) { c.Policy.MaxPrefixBytes-- }},
		{"max total bytes", func(c *S1ExecutionConfig) { c.Policy.MaxTotalBytes-- }},
		{"max assignments", func(c *S1ExecutionConfig) { c.Policy.MaxAssignments-- }},
		{"ledger entries", func(c *S1ExecutionConfig) { c.Capacity.LedgerEntries-- }},
		{"restrictive entries", func(c *S1ExecutionConfig) { c.Capacity.RestrictiveEntries++ }},
		{"image bytes", func(c *S1ExecutionConfig) { c.Capacity.ImageBytes-- }},
		{"restrictive image bytes", func(c *S1ExecutionConfig) { c.Capacity.RestrictiveImageBytes++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := initial
			tc.change(&changed)
			if !changed.valid() || changed.Digest() == initial.Digest() {
				t.Fatal("execution setting omitted")
			}
		})
	}
	invalid := initial
	invalid.Version++
	if invalid.Digest() != [32]byte{} {
		t.Fatal("unrecognized configuration")
	}
	mutated := initial
	mutated.Capacity.ImageBytes--
	if base.Configuration() != initial {
		t.Fatal("mutable configuration escaped")
	}
}

func TestS1ConfigurationVariantsCannotReplaySameCertificate(t *testing.T) {
	base := s1Fixture(t, s1Image())
	h := s1Seal(base.projection, s1Operation(t, base.projection, testIdentity(), s1Changes(s1ReaderRole())), 1, false)
	next := s1Next(base, h)
	capacity := base.configuration.Capacity
	capacity.ImageBytes = uint32(len(base.projection.snapshot.image))
	capacity.RestrictiveImageBytes = 0
	small, err := NewS1ApplyState(base.projection, base.membership, capacity, S1Retention{})
	if err != nil {
		t.Fatal(err)
	}
	if small.projection.cut != base.projection.cut || small.prefix == base.prefix {
		t.Fatal("capacity not committed to certified genesis")
	}
	if _, err := ApplyS1(small, next); !errors.Is(err, ErrS1Contract) {
		t.Fatal("same certificate forked on capacity", err)
	}
	limits := base.configuration.Policy
	limits.MaxRoles = len(base.projection.Image().Roles)
	p, err := NewS1Projection(base.projection.Image(), limits, base.projection.cut.Domain, base.projection.cut.Cohort, base.projection.cut.Fences, base.projection.cut.Generation)
	if err != nil {
		t.Fatal(err)
	}
	limited, err := NewS1ApplyState(p, base.membership, base.configuration.Capacity, S1Retention{})
	if err != nil {
		t.Fatal(err)
	}
	if limited.projection.cut == base.projection.cut || limited.prefix == base.prefix {
		t.Fatal("policy config not part of review/prefix")
	}
	if _, err := ApplyS1(limited, next); !errors.Is(err, ErrS1Contract) {
		t.Fatal("same certificate forked on policy limits", err)
	}
	if _, err := NewS1Operation(testIdentity(), p.cut, s1Changes(s1ReaderRole()), DefaultPolicyLimits()); !errors.Is(err, ErrS1Contract) {
		t.Fatal("constructor accepted policy digest without actual matching limits")
	}
	tampered := *base.projection
	tampered.cut.Policy = s1PolicyConfiguration(limits)
	if _, err := NewS1ApplyState(&tampered, base.membership, base.configuration.Capacity, S1Retention{}); !errors.Is(err, ErrS1Contract) {
		t.Fatal("constructor accepted mismatched compiled policy/configuration")
	}
	tamperedState := *base
	tamperedState.configuration.Policy = limits
	if _, err := ApplyS1(&tamperedState, next); !errors.Is(err, ErrS1Contract) {
		t.Fatal("Apply accepted mismatched actual configuration")
	}
	if state, _ := small.Lookup(h.id); state != S1Unresolved || small.slot != 0 {
		t.Fatal("mismatch poisoned ID/prefix")
	}
}
