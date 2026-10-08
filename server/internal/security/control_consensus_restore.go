package security

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
)

// Resume never adopts B-only state or creates a missing family. P is owned and
// authenticated first. Its complete retained choice history supplies exactly
// the proof prefix matching the existing B records, with at most one tail.
func resumeS2CParticipant(c s2cParticipantConfig, requiredP s2cJournalFloor, requiredB s2LocalReceipt) (_ *s2cParticipant, err error) {
	o, err := s2cNewParticipant(c)
	if err != nil {
		return nil, err
	}
	o.key = append(ed25519.PrivateKey(nil), c.Key...)
	transferred := false
	defer func() {
		if !transferred {
			err = errors.Join(err, o.closeResources())
		}
	}()
	for _, path := range []string{o.config.PPath, o.config.PPath + ".tip", o.config.PPath + ".lease", o.config.BPath, o.config.BPath + ".tip", o.config.BPath + ".lease"} {
		if _, err = os.Lstat(path); err != nil {
			return nil, err
		}
	}
	var used uint64 = 85
	bUsed, err := o.genesisBUsage()
	if err != nil {
		return nil, err
	}
	validate := func(index uint64, raw []byte) error {
		r, e := o.decodeRecord(raw, index)
		if e != nil {
			return e
		}
		beforeSlot := o.replayState.slot
		var applyCharge uint64
		if r.kind == s2cPDrained && o.preview != nil {
			applyCharge = o.preview.charge
		}
		if e = o.replayRecord(r); e != nil {
			return e
		}
		if o.replayState.slot != beforeSlot {
			bUsed, e = s2CheckedAdd(bUsed, applyCharge)
			if e != nil {
				return e
			}
		}
		charge, e := s2cJournalCharge(raw)
		if e != nil {
			return e
		}
		used, e = s2CheckedAdd(used, charge)
		if e != nil {
			return e
		}
		if used > c.PPolicy.Bytes || o.credit.PBytes > c.PPolicy.Bytes-used || index > c.PPolicy.Records || o.credit.PRecords > c.PPolicy.Records-index {
			return errS2CCapacity
		}
		// During a chosen-but-undrained tail B may have consumed its credit. P's
		// historical check deliberately uses the authenticated predecessor usage;
		// exact actual B reconciliation occurs below after its same-FD barriers.
		if bUsed > c.BScope.Storage.JournalBytes || o.credit.BBytes > c.BScope.Storage.JournalBytes-bUsed || o.replayState.slot+1 > c.BScope.Storage.ReplayRecords || o.credit.BRecords > c.BScope.Storage.ReplayRecords-(o.replayState.slot+1) {
			return errS2CCapacity
		}
		return nil
	}
	o.p, err = resumeS2CJournal(o.config.PPath, o.binding, c.PPolicy, requiredP, validate)
	if err != nil {
		return nil, err
	}
	if !o.bootstrapped || len(o.receipts) == 0 {
		return nil, errS2CProtocol
	}
	// A physical framing preflight bounds count before allocating the exact proof
	// slice. B's Resume acquires its lease and rechecks all bytes/records itself.
	_, count, e := s2LocalFiles(o.config.BPath, c.BScope.Storage)
	if e != nil {
		return nil, e
	}
	if count == 0 || count > uint64(len(o.proofs))+1 || count < uint64(len(o.receipts)) {
		return nil, errS2CProtocol
	}
	if count+1 < uint64(len(o.proofs))+1 {
		return nil, errS2CProtocol
	}
	strongest := o.receipts[len(o.receipts)-1]
	if requiredB != (s2LocalReceipt{}) {
		if requiredB.ScopeDigest != c.BScope.digest() || requiredB.LocalIndex != requiredB.ControlSlot+1 || requiredB.LocalIndex > count || requiredB.Chain == [32]byte{} {
			return nil, errS2CProtocol
		}
		if requiredB.LocalIndex > strongest.LocalIndex {
			strongest = requiredB
		} else if requiredB.LocalIndex == strongest.LocalIndex && requiredB != strongest {
			return nil, errS2CProtocol
		}
	}
	o.b, err = resumeS2Local(o.config.BPath, c.BScope, o.trust.genesis, o.proofs[:count-1], strongest)
	if err != nil {
		return nil, err
	}
	// Validate every retained DRAINED physical commitment, plus an independently
	// supplied older floor. Highest receipt containment alone cannot justify a
	// contradictory earlier local receipt embedded in P.
	floors := append([]s2LocalReceipt(nil), o.receipts...)
	if requiredB != (s2LocalReceipt{}) {
		floors = append(floors, requiredB)
	}
	if err = o.verifyBReceipts(floors); err != nil {
		return nil, err
	}
	if o.chosen == nil {
		if o.b.receipt != o.replayReceipt || o.b.used != bUsed {
			return nil, errS2CProtocol
		}
	} else {
		if count == o.chosen.slot+1 {
			if o.b.receipt.CapsuleDigest != o.preview.capsuleDigest || o.b.receipt.ControlPrefix != o.preview.state.prefix || o.credit.BBytes < o.preview.charge || o.credit.BRecords < 1 || o.b.used != bUsed+o.preview.charge {
				return nil, errS2CProtocol
			}
			o.credit.BBytes -= o.preview.charge
			o.credit.BRecords--
		} else if o.b.receipt != o.replayReceipt || o.b.used != bUsed {
			return nil, errS2CProtocol
		}
		if !o.bCreditFits(o.credit) {
			return nil, errS2CCapacity
		}
		if err = o.drainChosen(); err != nil {
			return nil, err
		}
	}
	transferred = true
	return o, nil
}

func (o *s2cParticipant) genesisBUsage() (uint64, error) {
	capsule, _, e := s2EncodeCapsule(o.trust.genesis.state, o.trust.genesis.roots, o.config.BScope.Storage.Capsule)
	if e != nil {
		return 0, e
	}
	r := &s2LocalRecord{kind: s2LocalGenesis, scope: o.config.BScope, scopeDigest: o.config.BScope.digest(), localIndex: 1, capsuleDigest: s2LocalCapsuleDigest(capsule), capsule: string(capsule)}
	raw, e := s2EncodeLocalRecord(r, o.config.BScope)
	if e != nil {
		return 0, e
	}
	return uint64(85 + len(raw) + 88), nil
}
func (o *s2cParticipant) verifyBReceipts(required []s2LocalReceipt) error {
	seqs := make([]uint64, len(required))
	byIndex := make(map[uint64][]s2LocalReceipt, len(required))
	for i, floor := range required {
		if floor.LocalIndex == 0 || floor.LocalIndex > o.b.receipt.LocalIndex || floor.ControlSlot+1 != floor.LocalIndex {
			return errS2CProtocol
		}
		seqs[i] = floor.LocalIndex
		byIndex[floor.LocalIndex] = append(byIndex[floor.LocalIndex], floor)
	}
	decode := func(raw []byte) (mutationlog.MutationOp, error) { return s2DecodeLocalRecord(raw, o.config.BScope) }
	validate := func(e mutationlog.Entry) error {
		r, ok := e.Op.(*s2LocalRecord)
		if !ok || e.HLC != (hlc.Timestamp{}) || r.localIndex != e.Seq {
			return errS2CProtocol
		}
		for _, floor := range byIndex[e.Seq] {
			prefix := o.trust.genesis.state.prefix
			if floor.ControlSlot != 0 {
				if floor.ControlSlot > uint64(len(o.history)) {
					return errS2CProtocol
				}
				if floor.ControlSlot < uint64(len(o.receipts)) {
					prefix = o.receipts[floor.ControlSlot].ControlPrefix
				} else if o.preview != nil {
					prefix = o.preview.state.prefix
				} else {
					return errS2CProtocol
				}
			}
			if floor.ScopeDigest != o.config.BScope.digest() || floor.CapsuleDigest != r.capsuleDigest || floor.ControlPrefix != prefix {
				return errS2CProtocol
			}
		}
		return nil
	}
	// All independently required cuts share one bounded two-pass inspection.
	cuts, e := mutationlog.InspectFileWALCuts(o.b.lease.Path(), seqs, decode, validate)
	if e != nil {
		return e
	}
	for i, cut := range cuts {
		if cut.ChainSHA256 != required[i].Chain {
			return errS2CProtocol
		}
	}
	return nil
}

func (o *s2cParticipant) replayRecord(r s2cPRecord) error {
	if r.kind != s2cPBallot && r.candidate != [32]byte{} || r.kind != s2cPAccept && r.kind != s2cPChosen && r.credit != (s2cCredit{}) || r.kind != s2cPDrained && r.receipt != (s2LocalReceipt{}) {
		return errS2CProtocol
	}
	if r.kind == s2cPGenesis {
		expected, _ := json.Marshal(o.manifest)
		if r.index != 1 || !bytes.Equal([]byte(r.raw), expected) {
			return errS2CProtocol
		}
		return nil
	}
	if r.index == 1 {
		return errS2CProtocol
	}
	if r.kind == s2cPDrained {
		if r.raw != "" {
			return errS2CProtocol
		}
		expected := o.replayReceipt
		if !o.bootstrapped {
			if r.index != 2 || r.receipt.Chain == [32]byte{} {
				return errS2CProtocol
			}
			expected.Chain = r.receipt.Chain
			if expected != r.receipt {
				return errS2CProtocol
			}
			o.bootstrapped = true
			o.replayReceipt = r.receipt
			o.receipts = append(o.receipts, r.receipt)
			return nil
		}
		if o.chosen == nil || o.preview == nil || r.receipt.Chain == [32]byte{} || r.receipt.ScopeDigest != o.config.BScope.digest() || r.receipt.LocalIndex != o.chosen.slot+1 || r.receipt.ControlSlot != o.chosen.slot || r.receipt.ControlPrefix != o.preview.state.prefix || r.receipt.CapsuleDigest != o.preview.capsuleDigest {
			return errS2CProtocol
		}
		charge, _ := s2cPCharge(0)
		if o.credit.PBytes < charge || o.credit.PRecords < 1 || o.credit.BBytes < o.preview.charge || o.credit.BRecords < 1 {
			return errS2CProtocol
		}
		o.finishDrained(r.receipt)
		return nil
	}
	if !o.bootstrapped || o.chosen != nil {
		return errS2CProtocol
	}
	if r.kind == s2cPOrigin {
		h, e := verifyHistoricalH(o.trust, []byte(r.raw))
		if e != nil || o.config.OwnedOrigin == 0 || h.originID != o.config.OwnedOrigin {
			return errS2CProtocol
		}
		d := h.digest()
		if _, exists := o.serials[h.handoff.serial]; exists {
			return errS2CProtocol
		}
		if uint64(len(o.pending)) >= o.config.PendingCount || uint64(len(h.raw)) > o.config.PendingBytes-o.pendingBytes {
			return errS2CCapacity
		}
		o.origins[d] = h
		o.serials[h.handoff.serial] = d
		o.pending[d] = h
		o.pendingBytes += uint64(len(h.raw))
		return nil
	}
	m, e := s2cDecodeMessage(o.trust, []byte(r.raw))
	if e != nil {
		return e
	}
	if m.slot != o.replayState.slot+1 || m.predecessor != o.replayState.prefix {
		return errS2CProtocol
	}
	switch r.kind {
	case s2cPBallot:
		if m.kind != s2cPrepare || m.sender != o.config.Member || m.ballot.Counter <= o.counter || r.candidate != [32]byte{} && o.pending[r.candidate] == nil {
			return errS2CProtocol
		}
		o.prepare = m
		o.selected = nil
		o.candidate = r.candidate
	case s2cPPromise:
		if m.kind != s2cPromise || m.sender != o.config.Member || o.promise != nil && m.ballot.compare(o.promise.ballot) <= 0 || o.accepted != nil && m.ballot.compare(o.accepted.ballot) <= 0 {
			return errS2CProtocol
		}
		if o.accepted == nil {
			if m.acceptedBallot != (s2cBallot{}) {
				return errS2CProtocol
			}
		} else if m.acceptedBallot != o.accepted.ballot || m.acceptedValue != o.accepted.value || m.acceptedAdmission != o.accepted.admission || !s2cSameHistorical(m.h, o.accepted.h) {
			return errS2CProtocol
		}
		o.promise = m
	case s2cPSelect:
		if m.kind != s2cAccept || m.sender != o.config.Member || o.prepare == nil || o.prepare.ballot != m.ballot || o.selected != nil {
			return errS2CProtocol
		}
		rows, e := s2cDecodeProof(o.trust, m.proof, s2cPromise, m)
		if e != nil {
			return e
		}
		highest, _, e := s2cHighestAccepted(rows)
		if e != nil {
			return e
		}
		if highest == (s2cBallot{}) && !s2cSameHistorical(m.h, o.pending[o.candidate]) {
			return errS2CProtocol
		}
		preview, e := o.replayPreview(m)
		if e != nil {
			return e
		}
		if preview.admission != m.admission {
			return errS2CProtocol
		}
		o.selected = m
	case s2cPAccept:
		if m.kind != s2cAccept || o.promise != nil && m.ballot.compare(o.promise.ballot) < 0 || o.accepted != nil && m.ballot.compare(o.accepted.ballot) <= 0 || o.selected != nil && o.selected.ballot == m.ballot && o.selected.value != m.value {
			return errS2CProtocol
		}
		preview, e := o.replayPreview(m)
		if e != nil {
			return e
		}
		if preview.admission != m.admission {
			return errS2CProtocol
		}
		completion, e := o.completionCredit(m.h, preview)
		if e != nil {
			return e
		}
		held := s2cMaxCredit(o.credit, completion)
		if r.credit != held {
			return errS2CProtocol
		}
		o.accepted = m
		o.credit = held
		if o.promise != nil && o.promise.ballot.compare(m.ballot) < 0 {
			o.promise = nil
		}
	case s2cPChosen:
		if m.kind != s2cChosen {
			return errS2CProtocol
		}
		next, e := s2cVerifyChosen(o.trust, m)
		if e != nil {
			return e
		}
		preview, e := o.replayPreview(m)
		if e != nil {
			return e
		}
		if preview.admission != m.admission {
			return errS2CProtocol
		}
		completion, e := o.completionCredit(m.h, preview)
		if e != nil {
			return e
		}
		held := s2cMaxCredit(o.credit, completion)
		if r.credit != held {
			return errS2CProtocol
		}
		charge, _ := s2cPCharge(uint64(len(m.raw)))
		if charge > held.PBytes || held.PRecords < 2 {
			return errS2CProtocol
		}
		held.PBytes -= charge
		held.PRecords--
		o.credit = held
		o.chosen = m
		o.preview = preview
		o.history = append(o.history, m)
		o.proofs = append(o.proofs, next)
	default:
		return errS2CProtocol
	}
	o.observe(m)
	return nil
}
func (o *s2cParticipant) replayPreview(m *s2cMessage) (*s2cPreview, error) {
	return s2cPreviewCandidate(o.trust, o.replayState, o.trust.genesis.roots, o.config.BScope, o.replayReceipt, m.h, s2cLogicalCommit(o.trust, m.slot, m.value))
}
func s2cSameHistorical(a, b *s2cHistoricalH) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.raw == b.raw
}
