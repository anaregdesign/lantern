package security

import (
	"bytes"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func s2cPreviewTestH(t *testing.T, f *s2cTestFixture, h *S1Handoff, origin uint32) *s2cHistoricalH {
	t.Helper()
	encoded := s2cTestSealH(t, f.trust, origin, f.keys[origin], h)
	verified, err := verifyHistoricalH(f.trust, encoded)
	if err != nil {
		t.Fatal("real historical verification", err)
	}
	return verified
}

// These are real signed member assertions used to qualify semantic parity, not
// a durability fixture. Native participant tests separately prove when members
// may issue them. No fixture constructs s1PrefixCertificate here.
func s2cPreviewTestQC(t *testing.T, f *s2cTestFixture, p *s2cPreview, h *s2cHistoricalH, ballot s2cBallot, members ...uint32) S1CertifiedNext {
	t.Helper()
	var votes []*s2cMessage
	for _, member := range members {
		vote, err := s2cSignMessage(f.trust, f.keys[member], s2cMessage{kind: s2cAccepted, sender: member, slot: p.commit.Slot, predecessor: p.before.ControlPrefix, ballot: ballot, value: p.commit.Value, admission: p.admission})
		if err != nil {
			t.Fatal("sign genuine vote", err)
		}
		votes = append(votes, vote)
	}
	proof, err := s2cBuildQC(f.trust, votes)
	if err != nil {
		t.Fatal("real majority", err)
	}
	chosen, err := s2cSignMessage(f.trust, nil, s2cMessage{kind: s2cChosen, slot: p.commit.Slot, predecessor: p.before.ControlPrefix, ballot: ballot, value: p.commit.Value, admission: p.admission, proof: proof, h: h})
	if err != nil {
		t.Fatal("genuine QC envelope", err)
	}
	next, err := s2cVerifyChosen(f.trust, chosen)
	if err != nil {
		t.Fatal("real choice verification", err)
	}
	return next
}

func s2cPreviewTestOwner(t *testing.T, f *s2cTestFixture, scope s2LocalScope) *s2LocalOwner {
	t.Helper()
	o, err := createS2Local(filepath.Join(t.TempDir(), "materialized.wal"), scope, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := o.Close(); err != nil {
			t.Error(err)
		}
	})
	return o
}

func s2cPreviewTestCandidate(t *testing.T, f *s2cTestFixture, o *s2LocalOwner, h *s2cHistoricalH) (*s2cPreview, *S1ApplyState) {
	t.Helper()
	state, before, err := o.ReadLocalCut()
	if err != nil {
		t.Fatal(err)
	}
	value := f.trust.noopValue()
	if h != nil {
		value = h.digest()
	}
	commit := s2cLogicalCommit(f.trust, state.slot+1, value)
	p, err := s2cPreviewCandidate(f.trust, state, f.genesis.roots, o.scope, before, h, commit)
	if err != nil {
		t.Fatal("uncertified preview", err)
	}
	return p, state
}

// Each successful step proves equality against two independently verified QCs
// with different ballots and voter subsets, then materializes exactly once on a
// real B file owner. Preview itself must not create a B plan or mutate a byte.
func s2cPreviewTestStep(t *testing.T, f *s2cTestFixture, o *s2LocalOwner, h *s2cHistoricalH, status S1ApplyStatus, disposition S1Disposition) *s2cPreview {
	t.Helper()
	beforeBytes, beforeCount, err := s2LocalFiles(o.lease.Path(), o.scope.Storage)
	if err != nil {
		t.Fatal(err)
	}
	p, state := s2cPreviewTestCandidate(t, f, o, h)
	if p.status != status || disposition != "" && (p.outcome == nil || p.outcome.disposition != disposition) {
		t.Fatalf("preview result: status=%s outcome=%+v, want %s/%s", p.status, p.outcome, status, disposition)
	}
	if o.pending != nil || o.state.slot != state.slot || o.receipt != p.before {
		t.Fatal("preview acquired Apply authority or mutated B")
	}
	first := s2cPreviewTestQC(t, f, p, h, s2cBallot{1, 1}, 1, 2)
	second := s2cPreviewTestQC(t, f, p, h, s2cBallot{2, 3}, 2, 3)
	if first.certificate.commit != second.certificate.commit || first.certificate.witness == second.certificate.witness {
		t.Fatal("replaceable witness fixture")
	}
	for _, next := range []S1CertifiedNext{first, second} {
		result, err := ApplyS1(state, next)
		if err != nil || result.Status != p.status || !reflect.DeepEqual(result.Outcome, p.outcome) || !bytes.Equal(s2TestEncode(t, result.State, f.genesis.roots), []byte(p.capsule)) {
			t.Fatal("genuine chosen Apply differs from preview", err)
		}
		plan, err := o.PrepareNext(next)
		if err != nil {
			t.Fatal("genuine B preparation", err)
		}
		actual := o.pending
		if actual.state.prefix != p.state.prefix || actual.capsule != p.capsule || actual.capsuleDigest != p.capsuleDigest || string(actual.payload) != p.payload || actual.charge != p.charge || p.charge != uint64(len(p.payload))+88 {
			t.Fatal("B successor/capsule/payload/charge differs")
		}
		if err := o.DiscardLocalPlan(plan); err != nil {
			t.Fatal(err)
		}
	}
	unchangedBytes, unchangedCount, err := s2LocalFiles(o.lease.Path(), o.scope.Storage)
	if err != nil || unchangedBytes != beforeBytes || unchangedCount != beforeCount {
		t.Fatal("preview or preparation appended durable bytes", err)
	}
	plan, err := o.PrepareNext(second)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := o.CommitPrepared(plan)
	if err != nil || receipt.CapsuleDigest != p.capsuleDigest || receipt.ControlPrefix != p.state.prefix {
		t.Fatal("materialized preview mismatch", err)
	}
	actualBytes, actualCount, err := s2LocalFiles(o.lease.Path(), o.scope.Storage)
	if err != nil || actualBytes != beforeBytes+p.charge || actualCount != beforeCount+1 {
		t.Fatal("actual retained file charge differs", err)
	}
	return p
}

func TestS2CPreviewEveryDispositionAndStatusMatchesGenuineChoice(t *testing.T) {
	for _, name := range []string{"applied", "authority", "purpose", "last administrator", "invariant", "capacity", "retired", "noop"} {
		t.Run(name, func(t *testing.T) {
			base := s1Fixture(t, s1Image())
			if name == "capacity" {
				capacity := base.configuration.Capacity
				capacity.ImageBytes, capacity.RestrictiveImageBytes = uint32(len(base.projection.snapshot.image)), 0
				var err error
				base, err = NewS1ApplyState(base.projection, base.membership, capacity, S1Retention{})
				if err != nil {
					t.Fatal(err)
				}
			}
			if name == "retired" {
				var err error
				base, err = NewS1ApplyState(base.projection, base.membership, base.configuration.Capacity, S1Retention{1, base.projection.cut.Domain, base.projection.cut.Cohort, [32]byte{9}})
				if err != nil {
					t.Fatal(err)
				}
			}
			f := s2cTestClusterState(t, 3, base)
			s := f.genesis.state
			o := s2cPreviewTestOwner(t, f, s2OwnerTestScope(f.genesis))
			actor, command := testIdentity(), s1Changes(s1ReaderRole())
			status, disposition := S1Original, S1Applied
			switch name {
			case "authority":
				actor, disposition = s1Bob(), S1RejectedAuthority
			case "purpose":
				bob := s1Bob()
				command, disposition = s1Changes(Change{Kind: PutAssignment, Identity: &bob, RoleID: "security_admin"}), S1RejectedPurpose
			case "last administrator":
				command, disposition = s1Changes(Change{Kind: DeletePrincipal, Identity: &actor}), S1RejectedAdmin
			case "invariant":
				bob := s1Bob()
				command, disposition = s1Changes(Change{Kind: PutAssignment, Identity: &bob, RoleID: "absent"}), S1RejectedInvariant
			case "capacity":
				disposition = S1RejectedCapacity
			case "retired":
				status, disposition = S1RetiredInput, ""
			case "noop":
				s2cPreviewTestStep(t, f, o, nil, S1Noop, "")
				return
			}
			h := s2cPreviewTestH(t, f, s1Seal(s.projection, s1Operation(t, s.projection, actor, command), 1, false), 1)
			s2cPreviewTestStep(t, f, o, h, status, disposition)
		})
	}
}

func TestS2CPreviewFullSemanticCAS(t *testing.T) {
	for _, name := range []string{"version", "generation", "sequence", "previous", "projection", "frontier", "fences", "policy"} {
		t.Run(name, func(t *testing.T) {
			f := s2cTestCluster(t, 3)
			s := f.genesis.state
			review, limits := s.projection.cut, s.configuration.Policy
			switch name {
			case "version":
				// Version is a format contract, not a historical CAS variant.
				review.Version++
				if _, err := NewS1Operation(testIdentity(), review, s1Changes(s1ReaderRole()), limits); !errors.Is(err, ErrS1Contract) {
					t.Fatal("unknown semantic version admitted", err)
				}
				return
			case "generation":
				review.Generation[0]++
			case "sequence":
				review.Sequence++
			case "previous":
				review.Previous[0]++
			case "projection":
				review.Projection[0]++
			case "frontier":
				review.Frontier[0]++
			case "fences":
				review.Fences[0]++
			case "policy":
				limits.MaxRoles--
				review.Policy = s1PolicyConfiguration(limits)
			}
			op, err := NewS1Operation(testIdentity(), review, s1Changes(s1ReaderRole()), limits)
			if err != nil {
				t.Fatal(err)
			}
			h := s2cPreviewTestH(t, f, s1Seal(s.projection, op, 1, false), 1)
			o := s2cPreviewTestOwner(t, f, s2OwnerTestScope(f.genesis))
			s2cPreviewTestStep(t, f, o, h, S1Original, S1RejectedCAS)
		})
	}
}

func TestS2CPreviewOriginalOwnershipBeforeCASAndWitnessVariants(t *testing.T) {
	f := s2cTestCluster(t, 3)
	o := s2cPreviewTestOwner(t, f, s2OwnerTestScope(f.genesis))
	s := f.genesis.state
	op := s1Operation(t, s.projection, testIdentity(), s1Changes(s1ReaderRole()))
	original := s1Seal(s.projection, op, 1, false)
	h := s2cPreviewTestH(t, f, original, 1)
	first := s2cPreviewTestStep(t, f, o, h, S1Original, S1Applied)
	variant := s1Seal(s.projection, op, 1, true)
	variant.serial = 73
	replayH := s2cPreviewTestH(t, f, variant, 2)
	replay := s2cPreviewTestStep(t, f, o, replayH, S1Replay, S1Applied)
	if h.digest() == replayH.digest() || !reflect.DeepEqual(first.outcome, replay.outcome) || replay.outcome.commit != first.commit || replay.outcome.handoff != h.digest() {
		t.Fatal("origin/serial/purpose/recovery witness rewrote original")
	}
	conflicting := s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(Change{Kind: DeleteRole, RoleID: "reader"})), 1, false)
	conflict := s2cPreviewTestStep(t, f, o, s2cPreviewTestH(t, f, conflicting, 3), S1IDConflict, "")
	if conflict.outcome != nil || !reflect.DeepEqual(conflict.state.ledger[h.handoff.id], first.outcome) {
		t.Fatal("conflict replaced original")
	}
	state, _, _ := o.ReadLocalCut()
	bob := s1Bob()
	grant := s1Operation(t, state.projection, testIdentity(), s1Changes(Change{Kind: PutAssignment, Identity: &bob, RoleID: "security_admin"}))
	denied := s2cPreviewTestStep(t, f, o, s2cPreviewTestH(t, f, s1Seal(state.projection, grant, 2, false), 1), S1Original, S1RejectedPurpose)
	retry := s2cPreviewTestStep(t, f, o, s2cPreviewTestH(t, f, s1Seal(state.projection, grant, 2, true), 2), S1Replay, S1RejectedPurpose)
	if !reflect.DeepEqual(denied.outcome, retry.outcome) {
		t.Fatal("later proof rehabilitated terminal outcome")
	}
}

func TestS2CPreviewPredecessorFenceAndDetachedBytes(t *testing.T) {
	f := s2cTestCluster(t, 3)
	o := s2cPreviewTestOwner(t, f, s2OwnerTestScope(f.genesis))
	s := f.genesis.state
	h1 := s2cPreviewTestH(t, f, s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(s1ReaderRole())), 1, false), 1)
	bob := s1Bob()
	h2 := s2cPreviewTestH(t, f, s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(Change{Kind: RevokeSessions, Identity: &bob})), 2, false), 2)
	stale, state := s2cPreviewTestCandidate(t, f, o, h2)
	s2cPreviewTestStep(t, f, o, h1, S1Original, S1Applied)
	current, receipt, _ := o.ReadLocalCut()
	for _, before := range []s2LocalReceipt{stale.before, receipt} {
		if p, err := s2cPreviewCandidate(f.trust, current, f.genesis.roots, o.scope, before, h2, stale.commit); !errors.Is(err, ErrS1Contract) || p != nil {
			t.Fatal("H2 stale predecessor/position admitted after H1", err)
		}
	}
	// The identical original H is a terminal CAS result at the actual next
	// position. No reauthorization or newer review silently replaces its bytes.
	s2cPreviewTestStep(t, f, o, h2, S1Original, S1RejectedCAS)
	capsule, payload := stale.capsule, stale.payload
	state.projection.lineage[bob] = 999
	state.ledger[h1.handoff.id] = &OriginalOutcome{}
	if stale.state.projection.lineage[bob] == 999 || stale.state.ledger[h1.handoff.id] != nil || stale.capsule != capsule || stale.payload != payload {
		t.Fatal("input aliases changed frozen preview")
	}
	stale.outcome.items[0].Kind = "local mutation"
	if stale.capsule != capsule || stale.payload != payload {
		t.Fatal("mutable result alias rewrote immutable bytes")
	}
}

func TestS2CPreviewCommonAdmissionExcludesLocalStorage(t *testing.T) {
	f := s2cTestCluster(t, 3)
	s := f.genesis.state
	h := s2cPreviewTestH(t, f, s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(s1ReaderRole())), 1, false), 1)
	base := s2OwnerTestScope(f.genesis)
	var first *s2cPreview
	differentPayload, differentCharge := false, false
	for i := byte(1); i <= 8; i++ {
		scope := base
		scope.StoreIdentity, scope.JournalEpoch = [32]byte{i, 93, 212}, [16]byte{255, i}
		scope.Storage.JournalBytes -= uint64(i) * 17
		scope.Storage.ReplayRecords -= uint64(i)
		o := s2cPreviewTestOwner(t, f, scope)
		p := s2cPreviewTestStep(t, f, o, h, S1Original, S1Applied)
		if first == nil {
			first = p
			continue
		}
		if p.admission != first.admission || p.capsule != first.capsule || p.commit != first.commit || p.before.ScopeDigest == first.before.ScopeDigest {
			t.Fatal("local scope/quota leaked into common admission")
		}
		differentPayload = differentPayload || p.payload != first.payload
		differentCharge = differentCharge || p.charge != first.charge
	}
	if !differentPayload || !differentCharge {
		t.Fatal("negative control did not vary exact local header bytes and charge")
	}
}

func TestS2CPreviewTerminalReserveAndAggregateRefusal(t *testing.T) {
	t.Run("ordinary metadata is required for terminal rejection", func(t *testing.T) {
		base := s1Fixture(t, s1Image())
		capacity := base.configuration.Capacity
		capacity.LedgerEntries, capacity.RestrictiveEntries = 2, 1
		base, err := NewS1ApplyState(base.projection, base.membership, capacity, S1Retention{})
		if err != nil {
			t.Fatal(err)
		}
		f := s2cTestClusterState(t, 3, base)
		o := s2cPreviewTestOwner(t, f, s2OwnerTestScope(f.genesis))
		s := f.genesis.state
		bob := s1Bob()
		op := s1Operation(t, s.projection, testIdentity(), s1Changes(Change{Kind: PutAssignment, Identity: &bob, RoleID: "security_admin"}))
		firstH := s2cPreviewTestH(t, f, s1Seal(s.projection, op, 1, false), 1)
		s2cPreviewTestStep(t, f, o, firstH, S1Original, S1RejectedPurpose)
		next := s2cPreviewTestH(t, f, s1Seal(s.projection, op, 2, false), 1)
		state, receipt, _ := o.ReadLocalCut()
		commit := s2cLogicalCommit(f.trust, state.slot+1, next.digest())
		if p, err := s2cPreviewCandidate(f.trust, state, f.genesis.roots, o.scope, receipt, next, commit); p != nil || !errors.Is(err, ErrControlReserve) || !errors.Is(err, errS2BlockedSameDecision) {
			t.Fatal("terminal refusal consumed protected reserve", err)
		}
		if o.receipt != receipt || state.slot != 1 || len(state.ledger) != 1 || o.pending != nil {
			t.Fatal("refusal advanced or claimed a B plan")
		}
		restrict := s1Seal(state.projection, s1Operation(t, state.projection, testIdentity(), s1Changes(Change{Kind: RevokeSessions, Identity: &bob})), 3, false)
		s2cPreviewTestStep(t, f, o, s2cPreviewTestH(t, f, restrict, 1), S1Original, S1Applied)
		s2cPreviewTestStep(t, f, o, firstH, S1Replay, S1RejectedPurpose)
	})
	t.Run("terminal whole capsule exceeds independent common bound", func(t *testing.T) {
		f := s2cTestCluster(t, 3)
		bounds := f.trust.bounds
		bounds.Capsule.Bytes = uint64(len(s2TestEncode(t, f.genesis.state, f.genesis.roots))) + 100
		var err error
		f.trust, err = newS2CTrust(s2cBootstrap{f.genesis, f.members, f.origins, bounds})
		if err != nil {
			t.Fatal(err)
		}
		o := s2cPreviewTestOwner(t, f, s2OwnerTestScope(f.genesis))
		s := f.genesis.state
		bob := s1Bob()
		h := s2cPreviewTestH(t, f, s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(Change{Kind: PutAssignment, Identity: &bob, RoleID: "security_admin"})), 1, false), 1)
		state, before, _ := o.ReadLocalCut()
		commit := s2cLogicalCommit(f.trust, 1, h.digest())
		result, err := evaluateS1Candidate(state, h.handoff, commit, before.ControlPrefix)
		if err != nil || result.Outcome.disposition != S1RejectedPurpose || len(result.State.projection.snapshot.image) != len(state.projection.snapshot.image) {
			t.Fatal("individually legal terminal candidate", err)
		}
		if p, err := s2cPreviewCandidate(f.trust, state, f.genesis.roots, o.scope, before, h, commit); p != nil || !errors.Is(err, errS2Unpersistable) || !errors.Is(err, errS2BlockedSameDecision) {
			t.Fatal("full aggregate refusal lost", err)
		}
		if o.receipt != before || o.pending != nil || len(o.state.ledger) != 0 {
			t.Fatal("aggregate refusal changed original decision or claimed ID")
		}
	})
}

func TestS2CPreviewExactCapsuleAndPayloadBounds(t *testing.T) {
	f := s2cTestCluster(t, 3)
	s := f.genesis.state
	bob := s1Bob()
	h := s2cPreviewTestH(t, f, s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(Change{Kind: PutAssignment, Identity: &bob, RoleID: "security_admin"})), 1, false), 1)
	scope := s2OwnerTestScope(f.genesis)
	o := s2cPreviewTestOwner(t, f, scope)
	exact, _ := s2cPreviewTestCandidate(t, f, o, h)
	for _, short := range []uint64{0, 1} {
		local := scope
		local.Storage.Capsule.Bytes = uint64(len(exact.capsule)) - short
		owner := s2cPreviewTestOwner(t, f, local)
		if short == 0 {
			s2cPreviewTestStep(t, f, owner, h, S1Original, S1RejectedPurpose)
			continue
		}
		state, receipt, _ := owner.ReadLocalCut()
		if p, err := s2cPreviewCandidate(f.trust, state, f.genesis.roots, local, receipt, h, exact.commit); p != nil || !errors.Is(err, errS2Unpersistable) {
			t.Fatal("one byte short terminal capsule fit", err)
		}
	}
	t.Run("one record short", func(t *testing.T) {
		local := scope
		local.Storage.ReplayRecords = 1
		owner := s2cPreviewTestOwner(t, f, local)
		state, receipt, _ := owner.ReadLocalCut()
		if p, err := s2cPreviewCandidate(f.trust, state, f.genesis.roots, local, receipt, h, exact.commit); p != nil || !errors.Is(err, errS2Unpersistable) {
			t.Fatal("one record short fit", err)
		}
	})
	t.Run("common payload bound includes entire B framing", func(t *testing.T) {
		bounds := f.trust.bounds
		// The predecessor fits, and H has its independent signed-H ceiling.
		// The complete next APPLY necessarily adds a retained terminal outcome.
		bounds.PayloadBytes = uint64(len(s2TestEncode(t, s, f.genesis.roots)))
		other := *f
		var err error
		other.trust, err = newS2CTrust(s2cBootstrap{f.genesis, f.members, f.origins, bounds})
		if err != nil {
			t.Fatal(err)
		}
		candidate := s2cPreviewTestH(t, &other, h.handoff, 1)
		owner := s2cPreviewTestOwner(t, &other, scope)
		state, receipt, _ := owner.ReadLocalCut()
		commit := s2cLogicalCommit(other.trust, 1, candidate.digest())
		if p, err := s2cPreviewCandidate(other.trust, state, f.genesis.roots, scope, receipt, candidate, commit); p != nil || !errors.Is(err, errS2Unpersistable) {
			t.Fatal("aggregate common payload bound omitted", err)
		}
		if p, err := s2cPreviewCandidate(f.trust, state, f.genesis.roots, scope, receipt, candidate, commit); p != nil || !errors.Is(err, ErrS1Contract) {
			t.Fatal("historical capability from another common scope accepted", err)
		}
	})
}

func TestS2CPreviewRejectsReboundPredecessorAndEmptyHistorical(t *testing.T) {
	f := s2cTestCluster(t, 3)
	o := s2cPreviewTestOwner(t, f, s2OwnerTestScope(f.genesis))
	p, state := s2cPreviewTestCandidate(t, f, o, nil)
	for _, alter := range []func(*s2LocalReceipt){
		func(r *s2LocalReceipt) { r.ScopeDigest[0]++ },
		func(r *s2LocalReceipt) { r.LocalIndex++ },
		func(r *s2LocalReceipt) { r.ControlSlot++ },
		func(r *s2LocalReceipt) { r.ControlPrefix[0]++ },
		func(r *s2LocalReceipt) { r.CapsuleDigest[0]++ },
		func(r *s2LocalReceipt) { r.Chain = [32]byte{} },
	} {
		before := p.before
		alter(&before)
		if preview, err := s2cPreviewCandidate(f.trust, state, f.genesis.roots, o.scope, before, nil, p.commit); preview != nil || !errors.Is(err, ErrS1Contract) {
			t.Fatal("rebound predecessor accepted", err)
		}
	}
	if preview, err := s2cPreviewCandidate(f.trust, state, f.genesis.roots, o.scope, p.before, &s2cHistoricalH{}, p.commit); preview != nil || !errors.Is(err, ErrS1Contract) {
		t.Fatal("empty historical capability became NOOP", err)
	}
	if o.receipt != p.before || o.pending != nil || state.slot != 0 {
		t.Fatal("contract refusal changed predecessor")
	}
}
