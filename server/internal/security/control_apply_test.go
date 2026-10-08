package security

import (
	"errors"
	"maps"
	"math"
	"reflect"
	"testing"
)

func TestS1CandidateRequiresExactLogicalPositionWithoutCertification(t *testing.T) {
	s := s1Fixture(t, s1Image())
	h := s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(s1ReaderRole())), 1, false)
	next := s1Next(s, h)
	want := s1Apply(t, s, h)
	// The evaluator does not need a witness. The same uncertified candidate
	// cannot enter ApplyS1: only that wrapper requires the opaque certificate.
	got, err := evaluateS1Candidate(s, h, next.certificate.commit, s.prefix)
	if err != nil || !s1ApplyResultsEqual(got, want) {
		t.Fatal("uncertified evaluator differs", err)
	}
	next.certificate.witness = [32]byte{}
	if result, err := ApplyS1(s, next); !errors.Is(err, ErrS1Contract) || result.State != nil {
		t.Fatal("uncertified evaluation became Apply authority", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*CommitRef, *[32]byte)
	}{
		{"version", func(c *CommitRef, _ *[32]byte) { c.Version++ }},
		{"domain", func(c *CommitRef, _ *[32]byte) { c.Domain[0]++ }},
		{"cohort", func(c *CommitRef, _ *[32]byte) { c.Cohort[0]++ }},
		{"membership", func(c *CommitRef, _ *[32]byte) { c.Membership[0]++ }},
		{"configuration", func(c *CommitRef, _ *[32]byte) { c.Configuration[0]++ }},
		{"slot", func(c *CommitRef, _ *[32]byte) { c.Slot++ }},
		{"value", func(c *CommitRef, _ *[32]byte) { c.Value[0]++ }},
		{"predecessor", func(_ *CommitRef, p *[32]byte) { p[0]++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			commit, previous := next.certificate.commit, s.prefix
			tc.change(&commit, &previous)
			if result, err := evaluateS1Candidate(s, h, commit, previous); !errors.Is(err, ErrS1Contract) || result.State != nil {
				t.Fatal("invalid logical candidate advanced state", err)
			}
		})
	}
	exhausted := *s
	exhausted.slot = math.MaxUint64
	if _, err := evaluateS1Candidate(&exhausted, h, next.certificate.commit, s.prefix); !errors.Is(err, ErrS1Contract) {
		t.Fatal("slot overflow", err)
	}
	if s.slot != 0 || len(s.ledger) != 0 || s.projection.cut.Sequence != 1 {
		t.Fatal("candidate evaluation mutated predecessor")
	}
}

// Compare every result/state field, including the full outcome and ledger,
// compiled policy/indexes, canonical image, cut and lineage. Only derived Scope
// caches are excluded: their LRU history can differ after equivalent evaluation.
// Copy the reference-bearing path so comparison never changes either result.
func s1ApplyResultWithoutScopeCaches(result S1ApplyResult) S1ApplyResult {
	if result.State == nil {
		return result
	}
	state := *result.State
	result.State = &state
	if state.projection == nil {
		return result
	}
	projection := *state.projection
	state.projection = &projection
	if projection.snapshot == nil {
		return result
	}
	snapshot := *projection.snapshot
	projection.snapshot = &snapshot
	if snapshot.policy != nil {
		policy := *snapshot.policy
		snapshot.policy = &policy
		policy.scopeCache = nil
	}
	withoutCache := func(access *Access) *Access {
		if access == nil {
			return nil
		}
		copy := *access
		copy.cache = nil
		return &copy
	}
	snapshot.accessSets = maps.Clone(snapshot.accessSets)
	for key, access := range snapshot.accessSets {
		snapshot.accessSets[key] = withoutCache(access)
	}
	snapshot.principals = maps.Clone(snapshot.principals)
	for identity, principal := range snapshot.principals {
		principal.access = withoutCache(principal.access)
		snapshot.principals[identity] = principal
	}
	return result
}

func s1ApplyResultsEqual(a, b S1ApplyResult) bool {
	return reflect.DeepEqual(s1ApplyResultWithoutScopeCaches(a), s1ApplyResultWithoutScopeCaches(b))
}

func TestS1ApplySemanticComparisonIgnoresScopeCacheHistory(t *testing.T) {
	s := s1Fixture(t, s1Image())
	h := s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(s1ReaderRole())), 1, false)
	next := s1Next(s, h)
	want := s1Apply(t, s, h)
	got, err := evaluateS1Candidate(s, h, next.certificate.commit, s.prefix)
	if err != nil {
		t.Fatal(err)
	}
	warm := func(snapshot *Snapshot, identities []Identity) {
		for _, identity := range identities {
			access, active := snapshot.AccessFor(identity)
			if !active {
				t.Fatal("inactive cache fixture")
			}
			for _, action := range []Action{VertexRead, VertexWrite, VertexDelete, Query, CDCIdentity, CDCValue, Export, ReceiptRead} {
				access.Scope(action)
			}
		}
	}
	warm(got.State.projection.snapshot, []Identity{testIdentity(), s1Bob()})
	warm(want.State.projection.snapshot, []Identity{s1Bob(), testIdentity()})
	if reflect.DeepEqual(got, want) {
		t.Fatal("opposite Scope cache history was not established")
	}
	if !s1ApplyResultsEqual(got, want) {
		t.Fatal("cache history changed semantic result comparison")
	}
	for _, result := range []S1ApplyResult{got, want} {
		snapshot := result.State.projection.snapshot
		for _, identity := range []Identity{testIdentity(), s1Bob()} {
			access, _ := snapshot.AccessFor(identity)
			if snapshot.policy.scopeCache == nil || access.cache != snapshot.policy.scopeCache {
				t.Fatal("comparison mutated an original Scope cache reference")
			}
		}
	}
}

func TestS1ApplySemanticComparisonRejectsStateChanges(t *testing.T) {
	s := s1Fixture(t, s1Image())
	h := s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(s1ReaderRole())), 1, false)
	want := s1Apply(t, s, h)
	for _, tc := range []struct {
		name   string
		change func(*S1ApplyResult)
	}{
		{"status", func(r *S1ApplyResult) { r.Status = S1Replay }},
		{"missing state", func(r *S1ApplyResult) { r.State = nil }},
		{"missing projection", func(r *S1ApplyResult) { r.State.projection = nil }},
		{"missing snapshot", func(r *S1ApplyResult) { r.State.projection.snapshot = nil }},
		{"missing outcome", func(r *S1ApplyResult) { r.Outcome = nil }},
		{"outcome ID", func(r *S1ApplyResult) { r.Outcome.id.Nonce[0]++ }},
		{"outcome operation", func(r *S1ApplyResult) { r.Outcome.operation.canonical += "changed" }},
		{"outcome handoff", func(r *S1ApplyResult) { r.Outcome.handoff[0]++ }},
		{"outcome commit", func(r *S1ApplyResult) { r.Outcome.commit.Value[0]++ }},
		{"outcome disposition", func(r *S1ApplyResult) { r.Outcome.disposition = S1RejectedCAS }},
		{"outcome items", func(r *S1ApplyResult) { r.Outcome.items[0].Disposition = S1RejectedCAS }},
		{"outcome observed", func(r *S1ApplyResult) { r.Outcome.observed.Sequence++ }},
		{"outcome resulting", func(r *S1ApplyResult) { r.Outcome.resulting.Sequence++ }},
		{"ledger outcome", func(r *S1ApplyResult) {
			other := *r.Outcome
			other.commit.Slot++
			r.State.ledger[r.Outcome.id] = &other
		}},
		{"ledger ID", func(r *S1ApplyResult) { delete(r.State.ledger, r.Outcome.id) }},
		{"membership", func(r *S1ApplyResult) { r.State.membership[0]++ }},
		{"slot", func(r *S1ApplyResult) { r.State.slot++ }},
		{"prefix", func(r *S1ApplyResult) { r.State.prefix[0]++ }},
		{"configuration", func(r *S1ApplyResult) { r.State.configuration.Capacity.LedgerEntries++ }},
		{"retention floor", func(r *S1ApplyResult) { r.State.retiredThrough++ }},
		{"cut", func(r *S1ApplyResult) { r.State.projection.cut.Policy[0]++ }},
		{"lineage", func(r *S1ApplyResult) { r.State.projection.lineage[s1Bob()]++ }},
		{"canonical image", func(r *S1ApplyResult) { r.State.projection.snapshot.image[0]++ }},
		{"Role bytes", func(r *S1ApplyResult) { r.State.projection.snapshot.roleBytes[0]++ }},
		{"policy limits", func(r *S1ApplyResult) { r.State.projection.snapshot.limits.MaxAssignments++ }},
		{"compiled policy", func(r *S1ApplyResult) { r.State.projection.snapshot.policy.maxAssignments++ }},
		{"compiled Role", func(r *S1ApplyResult) { delete(r.State.projection.snapshot.policy.roles, "new_reader") }},
		{"access set", func(r *S1ApplyResult) { delete(r.State.projection.snapshot.accessSets, "security_admin") }},
		{"principal", func(r *S1ApplyResult) {
			principal := r.State.projection.snapshot.principals[s1Bob()]
			principal.state = Suspended
			r.State.projection.snapshot.principals[s1Bob()] = principal
		}},
		{"principal access", func(r *S1ApplyResult) { r.State.projection.snapshot.principals[s1Bob()].access.setKey = "changed" }},
		{"issuer", func(r *S1ApplyResult) { delete(r.State.projection.snapshot.issuers, testIdentity().Issuer) }},
		{"session", func(r *S1ApplyResult) { r.State.projection.snapshot.sessions["changed"] = Session{} }},
		{"machine credential", func(r *S1ApplyResult) {
			r.State.projection.snapshot.machines = map[string]MachineCredential{"changed": {}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := s1Apply(t, s, h)
			if !s1ApplyResultsEqual(got, want) {
				t.Fatal("independent evaluation differs before negative control")
			}
			tc.change(&got)
			if s1ApplyResultsEqual(got, want) {
				t.Fatal("semantic state change was ignored")
			}
		})
	}
}

func TestS1ApplyAuthenticityAndPrefixBeforeOwnership(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alter func(*S1Handoff, *S1CertifiedNext)
	}{
		{"zero authorization", func(h *S1Handoff, n *S1CertifiedNext) { h.authorization = nil }},
		{"forged evidence", func(h *S1Handoff, n *S1CertifiedNext) { h.authorization.credentialEvidence = [32]byte{} }},
		{"wrong ID scope", func(h *S1Handoff, n *S1CertifiedNext) { h.id.Domain[0]++ }},
		{"rebound review", func(h *S1Handoff, n *S1CertifiedNext) { h.operation.reviewed.Cohort[0]++ }},
		{"expired consume", func(h *S1Handoff, n *S1CertifiedNext) { h.authorization.consume = h.authorization.credentialDeadline }},
		{"expired purpose", func(h *S1Handoff, n *S1CertifiedNext) { h.authorization.consume = h.authorization.purposeDeadline }},
		{"rebound purpose", func(h *S1Handoff, n *S1CertifiedNext) { h.authorization.purposeBinding[0]++ }},
		{"machine", func(h *S1Handoff, n *S1CertifiedNext) { h.authorization.authentication.Class = MachineActor }},
		{"missing prefix", func(h *S1Handoff, n *S1CertifiedNext) { n.certificate = nil }},
		{"gap", func(h *S1Handoff, n *S1CertifiedNext) { n.certificate.commit.Slot++ }},
		{"wrong predecessor", func(h *S1Handoff, n *S1CertifiedNext) { n.certificate.previous[0]++ }},
		{"wrong membership", func(h *S1Handoff, n *S1CertifiedNext) { n.certificate.commit.Membership[0]++ }},
		{"wrong value", func(h *S1Handoff, n *S1CertifiedNext) { n.certificate.commit.Value[0]++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := s1Fixture(t, s1Image())
			op := s1Operation(t, s.projection, testIdentity(), s1Changes(s1ReaderRole()))
			h := s1Seal(s.projection, op, 1, true)
			id := h.id
			n := s1Next(s, h)
			tc.alter(h, &n)
			if _, err := ApplyS1(s, n); err == nil {
				t.Fatal("invalid input applied")
			}
			if state, _ := s.Lookup(id); state != S1Unresolved || s.slot != 0 || s.projection.cut.Sequence != 1 {
				t.Fatal("invalid input claimed ID/prefix")
			}
			valid := s1Seal(s.projection, op, 1, true)
			if result := s1Apply(t, s, valid); result.Outcome.Disposition() != S1Applied {
				t.Fatal("legitimate ID poisoned")
			}
		})
	}
}

func TestS1ApplyOriginalBeforeCASAndConflict(t *testing.T) {
	s := s1Fixture(t, s1Image())
	op := s1Operation(t, s.projection, testIdentity(), s1Changes(s1ReaderRole()))
	h := s1Seal(s.projection, op, 1, true)
	first := s1Apply(t, s, h)
	variant := s1Seal(s.projection, op, 1, false)
	variant.origin = [32]byte{20}
	variant.serial = 99
	variant.authorization.credentialEvidence = [32]byte{21}
	retry := s1Apply(t, first.State, variant)
	if retry.Status != S1Replay || retry.Outcome != first.Outcome || retry.State.projection != first.State.projection || retry.Outcome.Commit().Slot != 1 || retry.Outcome.HandoffDigest() != h.digest() {
		t.Fatal("original rewritten or retried after CAS")
	}
	items := retry.Outcome.Items()
	items[0].Disposition = S1RejectedCAS
	if retry.Outcome.Items()[0].Disposition != S1Applied {
		t.Fatal("mutable result escaped")
	}
	for _, changed := range []OperationIdentity{
		s1Operation(t, s.projection, s1Bob(), s1Changes(s1ReaderRole())),
		s1Operation(t, s.projection, testIdentity(), s1Changes(Change{Kind: DeleteRole, RoleID: "reader"})),
		s1Operation(t, first.State.projection, testIdentity(), s1Changes(s1ReaderRole())),
	} {
		h := s1Seal(s.projection, changed, 1, false)
		conflict := s1Apply(t, retry.State, h)
		if conflict.Status != S1IDConflict || conflict.Outcome != nil {
			t.Fatal("changed actor/intent/review not conflict")
		}
		_, original := conflict.State.Lookup(h.id)
		if original != first.Outcome {
			t.Fatal("ledger overwritten")
		}
	}
	if state, _ := s.Lookup(h.id); state != S1Unresolved || s.projection.cut.Sequence != 1 {
		t.Fatal("pure input mutation")
	}
}

func TestS1ApplyTerminalRejectionReplayAndCapacity(t *testing.T) {
	s := s1Fixture(t, s1Image())
	bob := s1Bob()
	grant := s1Operation(t, s.projection, testIdentity(), s1Changes(Change{Kind: PutAssignment, Identity: &bob, RoleID: "security_admin"}))
	h := s1Seal(s.projection, grant, 1, false)
	denied := s1Apply(t, s, h)
	if denied.Outcome.Disposition() != S1RejectedPurpose || denied.State.projection != s.projection {
		t.Fatal("purpose rejection effect")
	}
	withProof := s1Seal(s.projection, grant, 1, true)
	retry := s1Apply(t, denied.State, withProof)
	if retry.Status != S1Replay || retry.Outcome != denied.Outcome {
		t.Fatal("terminal refusal rehabilitated")
	}
	small, err := NewS1ApplyState(s.projection, s.membership, S1Capacity{LedgerEntries: 2, RestrictiveEntries: 1, ImageBytes: MaxImageBytes, RestrictiveImageBytes: 1 << 20}, S1Retention{})
	if err != nil {
		t.Fatal(err)
	}
	r := s1Apply(t, small, h)
	second := s1Seal(s.projection, grant, 2, false)
	if _, err := ApplyS1(r.State, s1Next(r.State, second)); !errors.Is(err, ErrControlReserve) {
		t.Fatal("refusals consumed reserve", err)
	}
	restrict := s1Seal(r.State.projection, s1Operation(t, r.State.projection, testIdentity(), s1Changes(Change{Kind: RevokeSessions, Identity: &bob})), 3, false)
	r2 := s1Apply(t, r.State, restrict)
	if r2.Outcome.Disposition() != S1Applied {
		t.Fatal("restrictive reserve unavailable")
	}
	if _, err := ApplyS1(r2.State, s1Next(r2.State, second)); !errors.Is(err, ErrControlReserve) {
		t.Fatal("total cap bypass")
	}
	if retry.State.slot != 2 || len(retry.State.ledger) != 1 {
		t.Fatal("replay consumed new capacity")
	}
}

func TestS1RetentionAndNoop(t *testing.T) {
	s := s1Fixture(t, s1Image())
	noop := s1Apply(t, s, nil)
	if noop.Status != S1Noop || noop.State.projection != s.projection || noop.State.projection.cut != s.projection.cut || noop.State.slot != 1 {
		t.Fatal("NOOP changed review")
	}
	h := s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(s1ReaderRole())), 1, false)
	if s1Apply(t, noop.State, h).Outcome.Disposition() != S1Applied {
		t.Fatal("NOOP invalidated review")
	}
	retained := S1Retention{retiredThrough: 1, domain: s.projection.cut.Domain, cohort: s.projection.cut.Cohort, checkpoint: [32]byte{99}}
	retired, err := NewS1ApplyState(s.projection, s.membership, s.configuration.Capacity, retained)
	if err != nil {
		t.Fatal(err)
	}
	if state, _ := retired.Lookup(h.id); state != S1Retired {
		t.Fatal("certified floor absent")
	}
	result := s1Apply(t, retired, h)
	if result.Status != S1RetiredInput || len(result.State.ledger) != 0 || result.State.projection != s.projection {
		t.Fatal("retired ID revived")
	}
	h.id.Namespace = 2
	if state, _ := retired.Lookup(h.id); state != S1Unresolved {
		t.Fatal("absence presented as noncommit")
	}
	retained.domain[0]++
	if _, err := NewS1ApplyState(s.projection, s.membership, s.configuration.Capacity, retained); err == nil {
		t.Fatal("wrong floor scope")
	}
}

func TestS1HandoffRecoveryHasNoAmbientTime(t *testing.T) {
	s := s1Fixture(t, s1Image())
	op := s1Operation(t, s.projection, testIdentity(), s1Changes(s1ReaderRole()))
	h := s1Seal(s.projection, op, 1, false)
	// Fixture deadline is long before this test runs. Apply checks historical
	// consumption only and has no now/clock/proof-consumer parameter.
	a := s1Apply(t, s, h)
	b := s1Apply(t, s, h)
	if a.Outcome.Disposition() != S1Applied || a.State.projection.cut != b.State.projection.cut || a.State.prefix != b.State.prefix || !reflect.DeepEqual(a.Outcome, b.Outcome) || !reflect.DeepEqual(a.State.projection.Image(), b.State.projection.Image()) {
		t.Fatal("Apply depends on wall time/side effects")
	}
}

func TestS1FinalCapacityRefusalCannotConsumeRestrictiveMetadata(t *testing.T) {
	base := s1Fixture(t, s1Image())
	capacity := S1Capacity{LedgerEntries: 2, RestrictiveEntries: 1, ImageBytes: uint32(len(base.projection.snapshot.image))}
	s, err := NewS1ApplyState(base.projection, base.membership, capacity, S1Retention{})
	if err != nil {
		t.Fatal(err)
	}
	bob := s1Bob()
	grant := s1Operation(t, s.projection, testIdentity(), s1Changes(Change{Kind: PutAssignment, Identity: &bob, RoleID: "security_admin"}))
	ordinary := s1Apply(t, s, s1Seal(s.projection, grant, 1, false))
	if ordinary.Outcome.Disposition() != S1RejectedPurpose || len(ordinary.State.ledger) != 1 {
		t.Fatal("ordinary fixture")
	}
	op := s1Operation(t, ordinary.State.projection, testIdentity(), s1Changes(s1ReaderRole()))
	a, err := AssessS1(ordinary.State.projection, op)
	if err != nil || !a.ProvenNonexpanding || len(a.Next.snapshot.image) <= int(capacity.ImageBytes) {
		t.Fatal("oversized nonexpanding fixture", err)
	}
	h := s1Seal(ordinary.State.projection, op, 2, false)
	refusal, err := ApplyS1(ordinary.State, s1Next(ordinary.State, h))
	if !errors.Is(err, ErrControlReserve) || refusal.State != nil || refusal.Outcome != nil {
		t.Fatal("capacity refusal consumed reserve", err)
	}
	if ordinary.State.slot != 1 || len(ordinary.State.ledger) != 1 {
		t.Fatal("refusal advanced prefix/ledger")
	}
	if status, _ := ordinary.State.Lookup(h.id); status != S1Unresolved {
		t.Fatal("unreserved refusal claimed ID")
	}
	revoke := s1Seal(ordinary.State.projection, s1Operation(t, ordinary.State.projection, testIdentity(), s1Changes(Change{Kind: RevokeSessions, Identity: &bob})), 3, false)
	result := s1Apply(t, ordinary.State, revoke)
	if result.Outcome.Disposition() != S1Applied || len(result.State.ledger) != 2 || result.State.projection.SessionLineage(bob) != 2 {
		t.Fatal("following restriction lost reserve")
	}
}
