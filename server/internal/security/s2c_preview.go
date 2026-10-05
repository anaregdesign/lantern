package security

import "encoding/json"

// A preview owns exact deterministic successor bytes without certifying choice
// or reserving storage. Only the composite participant may use these measurements
// to reserve admission credit; only genuine verified choice can enter B Apply.
// Immutable strings prevent callers from aliasing the frozen serialized bytes.
type s2cPreview struct {
	before        s2LocalReceipt
	commit        CommitRef
	state         *S1ApplyState
	outcome       *OriginalOutcome
	status        S1ApplyStatus
	capsule       string
	capsuleDigest [32]byte
	payload       string
	charge        uint64
	admission     [32]byte
}

// The common commitment deliberately excludes B's local identity, quotas,
// receipt chain, payload length and reservation. The complete common capsule
// includes every retained original outcome and its first CommitRef. QC witnesses
// are absent both here and in the logical CommitRef, so recovery ballots cannot
// change the promised business outcome or frozen bytes.
func s2cAdmissionCommitment(scope, previous, previousCapsule [32]byte, commit CommitRef, capsule [32]byte, capsuleBytes uint64, status S1ApplyStatus) [32]byte {
	return s2cHash("admission", struct {
		Scope, Previous, PreviousCapsule [32]byte
		Commit                           CommitRef
		Capsule                          [32]byte
		CapsuleBytes                     uint64
		Status                           S1ApplyStatus
	}{scope, previous, previousCapsule, commit, capsule, capsuleBytes, status})
}

// The historical capability is exact and already verified. A nil capability
// means canonical NOOP; an empty nonnil capability must never become NOOP.
// Nothing in this path creates S1CertifiedNext, calls B.PrepareNext, samples a
// clock, consumes authority, changes H or publishes a state.
func s2cPreviewCandidate(trust *s2cTrust, state *S1ApplyState, roots s2CapsuleRoots, scope s2LocalScope, before s2LocalReceipt, historical *s2cHistoricalH, commit CommitRef) (*s2cPreview, error) {
	if trust == nil || trust.scope == [32]byte{} || trust.genesis == nil || roots != trust.genesis.roots || !trust.bounds.valid() || scope.validateGenesis(trust.genesis) != nil {
		return nil, ErrS1Contract
	}
	header, err := s2Header(state, roots)
	if err != nil || !scope.matchesCapsule(header) {
		return nil, ErrS1Contract
	}
	index, err := s2LocalIndex(state.slot)
	if err != nil || before.ScopeDigest != scope.digest() || before.LocalIndex != index || before.ControlSlot != state.slot || before.ControlPrefix != state.prefix || before.Chain == [32]byte{} {
		return nil, ErrS1Contract
	}
	predecessor, _, err := s2EncodeCapsule(state, roots, trust.bounds.Capsule)
	if err != nil || s2LocalCapsuleDigest(predecessor) != before.CapsuleDigest {
		return nil, ErrS1Contract
	}
	var h *S1Handoff
	if historical != nil {
		if historical.handoff == nil || historical.raw == "" || historical.scope != trust.scope {
			return nil, ErrS1Contract
		}
		h = historical.handoff
	}
	result, err := evaluateS1Candidate(state, h, commit, before.ControlPrefix)
	if err != nil {
		if err == ErrControlReserve {
			return nil, &s2LocalBlockedError{commit, err}
		}
		return nil, err
	}
	blocked := func(cause error) (*s2cPreview, error) { return nil, &s2LocalBlockedError{commit, cause} }
	index, err = s2LocalIndex(result.State.slot)
	if err != nil || index > scope.Storage.ReplayRecords {
		return blocked(errS2Unpersistable)
	}
	// Validate the full aggregate before detaching its maps. A terminal no-effect
	// result is still a new retained outcome and can overflow the capsule ceiling.
	if _, err = s2MeasureCapsule(result.State, roots, trust.bounds.Capsule); err != nil {
		return blocked(err)
	}
	if _, err = s2MeasureCapsule(result.State, roots, scope.Storage.Capsule); err != nil {
		return blocked(err)
	}
	frozen, err := s2DetachState(result.State)
	if err != nil {
		return blocked(err)
	}
	capsule, measure, err := s2EncodeCapsule(frozen, roots, trust.bounds.Capsule)
	if err != nil {
		return blocked(err)
	}
	capsuleDigest := s2LocalCapsuleDigest(capsule)
	record := &s2LocalRecord{kind: s2LocalApply, scopeDigest: before.ScopeDigest, localIndex: index, previousCapsuleDigest: before.CapsuleDigest, capsuleDigest: capsuleDigest, commit: commit, capsule: string(capsule)}
	// Existing B's codec bounds the hard payload ceiling. Check this protocol's
	// possibly tighter common ceiling before allocating that aggregate too.
	recordHeader, err := json.Marshal(s2LocalApplyHeader{record.scopeDigest, record.localIndex, record.previousCapsuleDigest, record.capsuleDigest, record.commit})
	if err != nil {
		return blocked(err)
	}
	payloadBytes, err := s2LocalPayloadSize(uint64(len(recordHeader)), measure.Bytes)
	if err != nil || payloadBytes > trust.bounds.PayloadBytes {
		return blocked(errS2Unpersistable)
	}
	charge, err := s2CheckedAdd(payloadBytes, 88)
	if err != nil {
		return blocked(err)
	}
	payload, err := s2EncodeLocalRecord(record, scope)
	if err != nil {
		return blocked(err)
	}
	if uint64(len(payload)) != payloadBytes {
		return blocked(errS2LocalRecord)
	}
	var outcome *OriginalOutcome
	if result.Outcome != nil {
		outcome = frozen.ledger[result.Outcome.id]
	}
	return &s2cPreview{
		before: before, commit: commit, state: frozen, outcome: outcome, status: result.Status,
		capsule: string(capsule), capsuleDigest: capsuleDigest, payload: string(payload), charge: charge,
		admission: s2cAdmissionCommitment(trust.scope, state.prefix, before.CapsuleDigest, commit, capsuleDigest, measure.Bytes, result.Status),
	}, nil
}
