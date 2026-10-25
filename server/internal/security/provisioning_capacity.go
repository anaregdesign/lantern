package security

import (
	"path/filepath"
	"time"
)

// These coherent finite defaults are resolved once and bound into genesis and
// local custody. Ordinary startup must never recalculate or enlarge them.
func provisioningExecution() S1ExecutionConfig {
	return S1ExecutionConfig{Version: S1Version, Policy: DefaultPolicyLimits(), Capacity: S1Capacity{
		LedgerEntries: 10000, RestrictiveEntries: 1000, ImageBytes: MaxImageBytes, RestrictiveImageBytes: 1 << 20}}
}

func provisioningBounds() s2cBounds {
	return s2cBounds{HistoricalBytes: s2cMaxHistoricalBytes, HeaderBytes: s2cMaxHeaderBytes,
		ProofBytes: s2cMaxProofBytes, PayloadBytes: s2cMaxPayloadBytes,
		Capsule: s2CapsuleLimits{Bytes: s2MaxCapsuleBytes, LedgerEntries: 10000, LineageEntries: 10000}}
}

func provisioningParticipant(g currentGenesisDocument, state *S1ApplyState, trust *s2cTrust, member provisioningMember, id uint32) (currentParticipantDocument, s3aLimits, error) {
	cut := state.projection.cut
	scope := s2LocalScope{Domain: cut.Domain, Cohort: cut.Cohort, Generation: cut.Generation,
		Membership: state.membership, Fences: cut.Fences, Configuration: state.configuration,
		ConfigDigest: state.configuration.Digest(), Genesis: g.Roots.Genesis,
		Floor:   s2CapsuleFloor{Domain: cut.Domain, Cohort: cut.Cohort},
		Storage: s2LocalStoragePolicy{Capsule: g.Bounds.Capsule, JournalBytes: uint64(DefaultSystemJournalMax), ReplayRecords: 100000}}
	p := currentParticipantDocument{Member: id, Incarnation: member.incarnation,
		PPath: filepath.Join(member.input.StateDir, "control-protocol.wal"), BPath: filepath.Join(member.input.StateDir, "control-materialized.wal"),
		PPolicy: s2cJournalPolicy{Bytes: uint64(DefaultSystemJournalMax), Records: 100000}, OwnedOrigin: id,
		PendingBytes: 8 << 20, PendingCount: 1024}
	if !provisioningRandom(scope.StoreIdentity[:], scope.JournalEpoch[:], p.PIdentity[:], p.PEpoch[:]) {
		return currentParticipantDocument{}, s3aLimits{}, ErrProvisioning
	}
	p.BScope = scope
	count := len(trust.members)
	maxFrame := min(g.Bounds.PayloadBytes, uint64(s2cMessageOverhead)+g.Bounds.ProofBytes+g.Bounds.HistoricalBytes)
	p.OutboxBytes = max(g.Bounds.PayloadBytes, uint64(count)*maxFrame)
	limits := s3aLimits{QueueBytes: p.OutboxBytes + uint64(count)*s3aQueueOverhead, PerPeerQueue: 4,
		TransitionCount: max(16, count+1), RangeSlots: 8, RangeBytes: g.Bounds.PayloadBytes,
		MaxConnections: max(12, 2*count+1), Attempts: 3, Rounds: 32,
		RPCTimeout: 500 * time.Millisecond, Backoff: 20 * time.Millisecond, BallotBackoff: 3 * time.Second, ScheduleWindow: 15 * time.Second}
	return p, limits, nil
}
