package security

import (
	"encoding/json"
	"time"
)

// Bounds the still-variable sample and cryptographic commitments after all
// business/source/purpose/certificate inputs have been frozen. JSON byte arrays
// use their largest decimal elements; native counters use 20 decimal digits;
// the derived UTC field uses its longest admitted encoding. This constructs
// neither a signed H nor an authorization/prefix capability.
func authorityHistoricalSizeBound(h authorityHistoricalHeader, op OperationIdentity, limits s2cBounds) (uint64, error) {
	var largest [32]byte
	for i := range largest {
		largest[i] = 255
	}
	h.Time.Counter, h.Time.SourceSequence = ^uint64(0), ^uint64(0)
	h.Time.UTCLow, h.Time.UTCHigh = ^uint64(0), ^uint64(0)
	h.ConsumeUpper = time.Date(2079, 12, 31, 23, 59, 59, 999999999, time.UTC)
	h.CredentialEvidence, h.Value = largest, largest
	if h.Purpose != nil {
		h.PurposeEvidence = largest
	}
	body, err := authorityHistoricalBody(h, op.Encode(), limits)
	if err != nil {
		return 0, err
	}
	return uint64(len(body) + 64), nil
}

// Measures the actual assessed successor and terminal outcome without issuing
// any S1 handoff. Only the prefix and two new handoff hash occurrences vary with
// the final native time/signature. Largest byte-array encodings dominate those
// fields; projection/lineage/operation and all prior outcomes remain exact.
// This temporary sizing state is never returned or passed to materialization.
func authoritySuccessorCharge(o *s2cParticipant, id FullChangeID, op OperationIdentity, a S1Assessment) (uint64, error) {
	s := o.replayState
	c := s.configuration.Capacity
	if a.Next == nil || op.reviewed != s.projection.cut || s.slot > ^uint64(0)-3 || id.Namespace <= s.retiredThrough || s.ledger[id] != nil {
		return 0, errS2CProtocol
	}
	entries := uint64(len(s.ledger)) + uint64(len(o.pending))
	entryLimit, imageLimit := uint64(c.LedgerEntries), uint64(c.ImageBytes)
	if !a.ProvenNonexpanding {
		entryLimit -= uint64(c.RestrictiveEntries)
		imageLimit -= uint64(c.RestrictiveImageBytes)
	}
	if entries >= entryLimit || uint64(len(a.Next.snapshot.image)) > imageLimit {
		return 0, ErrControlReserve
	}
	command, err := op.command()
	if err != nil {
		return 0, err
	}
	var largest [32]byte
	for i := range largest {
		largest[i] = 255
	}
	commit := s2cLogicalCommit(o.trust, s.slot+1, largest)
	items := []S1ItemOutcome{{0, command.Kind, S1Applied}}
	if command.Kind == S1Management {
		items = make([]S1ItemOutcome, len(command.Changes))
		for i, change := range command.Changes {
			items[i] = S1ItemOutcome{i, change.Kind, S1Applied}
		}
	}
	outcome := &OriginalOutcome{id: id, operation: op, handoff: largest, commit: commit, disposition: S1Applied, items: items, observed: op.reviewed, resulting: a.Next.cut}
	next := *s
	next.projection, next.slot, next.prefix = a.Next, commit.Slot, largest
	next.ledger = make(map[FullChangeID]*OriginalOutcome, len(s.ledger)+1)
	for key, value := range s.ledger {
		next.ledger[key] = value
	}
	next.ledger[id] = outcome
	limits := o.trust.bounds.Capsule
	local := o.config.BScope.Storage.Capsule
	limits.Bytes = min(limits.Bytes, local.Bytes)
	limits.LedgerEntries = min(limits.LedgerEntries, local.LedgerEntries)
	limits.LineageEntries = min(limits.LineageEntries, local.LineageEntries)
	measure, err := s2MeasureCapsule(&next, o.trust.genesis.roots, limits)
	if err != nil {
		return 0, err
	}
	header, err := json.Marshal(s2LocalApplyHeader{o.config.BScope.digest(), commit.Slot + 1, o.replayReceipt.CapsuleDigest, largest, commit})
	if err != nil {
		return 0, err
	}
	payload, err := s2LocalPayloadSize(uint64(len(header)), measure.Bytes)
	if err != nil || payload > o.trust.bounds.PayloadBytes {
		return 0, errS2CCapacity
	}
	return s2CheckedAdd(payload, 88)
}

func authorityOriginCapacity(o *s2cParticipant, h authorityHistoricalHeader, op OperationIdentity, a S1Assessment) (s2cCredit, uint64, error) {
	hBytes, err := authorityHistoricalSizeBound(h, op, o.trust.bounds)
	if err != nil {
		return s2cCredit{}, 0, err
	}
	if uint64(len(o.pending)) >= o.config.PendingCount || o.pendingBytes > o.config.PendingBytes || hBytes > o.config.PendingBytes-o.pendingBytes {
		return s2cCredit{}, 0, errS2CCapacity
	}
	bCharge, err := authoritySuccessorCharge(o, h.ID, op, a)
	if err != nil {
		return s2cCredit{}, 0, err
	}
	chosen := s2cChosenSizeBound(o.trust, hBytes)
	if chosen == 0 || chosen+uint64(len(authorityPMagic)+s2cPFixedBytes) > o.trust.bounds.PayloadBytes {
		return s2cCredit{}, 0, errS2CCapacity
	}
	chosenCharge, err := s2cPCharge(chosen)
	if err != nil {
		return s2cCredit{}, 0, err
	}
	drained, _ := s2cPCharge(0)
	completion := s2cMaxCredit(o.credit, s2cCredit{chosenCharge + drained, 2, bCharge, 1})
	hCharge, err := s2cPCharge(hBytes)
	if err != nil {
		return s2cCredit{}, 0, err
	}
	completion.PBytes, err = s2CheckedAdd(completion.PBytes, hCharge)
	if err != nil || completion.PRecords == ^uint64(0) {
		return s2cCredit{}, 0, errS2CCapacity
	}
	completion.PRecords++
	return completion, hBytes, nil
}
