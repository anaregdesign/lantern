package security

import (
	"errors"
	"math"
	"sort"
)

func (o *s2cParticipant) outbox(m *s2cMessage, to uint32) ([]s2cOutbox, error) {
	recipients := []uint32{to}
	if to == 0 {
		recipients = make([]uint32, len(o.trust.members))
		for i, v := range o.trust.members {
			recipients[i] = v.ID
		}
	}
	total := uint64(len(m.raw))
	if total == 0 || total > o.config.OutboxBytes/uint64(len(recipients)) {
		return nil, errS2CCapacity
	}
	out := make([]s2cOutbox, len(recipients))
	for i, id := range recipients {
		out[i] = s2cOutbox{id, []byte(m.raw)}
	}
	return out, nil
}
func (o *s2cParticipant) observe(m *s2cMessage) {
	o.counter = max(o.counter, m.ballot.Counter, m.acceptedBallot.Counter)
}
func s2cMessageList(m map[uint32]*s2cMessage) []*s2cMessage {
	ids := make([]uint32, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]*s2cMessage, 0, len(ids))
	for _, id := range ids {
		out = append(out, m[id])
	}
	return out
}

// Begin issues a new durable ballot. candidate is an already retained original
// H digest, or zero for canonical NOOP. Highest accepted P1 evidence overrides
// this preference without replacing, refreshing or reauthorizing that H.
func (o *s2cParticipant) Begin(candidate [32]byte) ([]s2cOutbox, error) {
	if o == nil {
		return nil, errS2CClosed
	}
	o.gate.Lock()
	defer o.gate.Unlock()
	defer o.poisonPanic()
	if e := o.readyLocked(); e != nil {
		return nil, e
	}
	member, _ := o.trust.member(o.config.Member)
	if !member.Proposer {
		return nil, errS2CProtocol
	}
	slot, pred, e := o.nextSlot()
	if e != nil {
		return nil, e
	}
	if candidate != [32]byte{} && o.pending[candidate] == nil {
		return nil, errS2CProtocol
	}
	if o.counter == math.MaxUint64 {
		return nil, errS2CCapacity
	}
	m, e := s2cSignPrepare(o.trust, o.key, o.config.Member, slot, pred, s2cBallot{o.counter + 1, o.config.Member})
	if e != nil {
		return nil, e
	}
	out, e := o.outbox(m, 0)
	if e != nil {
		return nil, e
	}
	if e = o.appendRecord(s2cPRecord{kind: s2cPBallot, candidate: candidate, raw: m.raw}, o.credit); e != nil {
		return nil, e
	}
	o.observe(m)
	o.prepare = m
	o.selected = nil
	o.candidate = candidate
	o.promises = map[uint32]*s2cMessage{}
	o.votes = map[uint32]*s2cMessage{}
	return out, nil
}

// Retry only retransmits the retained exact BALLOT/SELECT bytes, including
// after restart. It cannot select a second value for the existing ballot.
func (o *s2cParticipant) Retry() ([]s2cOutbox, error) {
	if o == nil {
		return nil, errS2CClosed
	}
	o.gate.Lock()
	defer o.gate.Unlock()
	defer o.poisonPanic()
	if e := o.readyLocked(); e != nil {
		return nil, e
	}
	if o.chosen != nil {
		return nil, errS2CPending
	}
	if o.selected != nil {
		return o.outbox(o.selected, 0)
	}
	if o.prepare != nil {
		return o.outbox(o.prepare, 0)
	}
	return nil, errS2CProtocol
}

// Receive accepts only encoded authenticated messages. The scheduler cannot
// inject certificate tokens, completion receipts or caller-supplied votes.
func (o *s2cParticipant) Receive(raw []byte) ([]s2cOutbox, error) {
	if o == nil {
		return nil, errS2CClosed
	}
	o.gate.Lock()
	defer o.gate.Unlock()
	defer o.poisonPanic()
	if e := o.readyLocked(); e != nil {
		return nil, e
	}
	m, e := s2cDecodeMessage(o.trust, raw)
	if e != nil {
		return nil, e
	}
	// Authentication precedes observation, but an authenticated mismatched or
	// capacity-refused attempt still raises the next local ballot. The next
	// durable BALLOT retains that increase; refused traffic sends no vote.
	o.observe(m)
	if m.kind == s2cChosen {
		return nil, o.learnChosen(m)
	}
	slot, pred, e := o.nextSlot()
	if e != nil {
		return nil, e
	}
	if m.slot != slot || m.predecessor != pred {
		return nil, errS2CProtocol
	}
	switch m.kind {
	case s2cPrepare:
		return o.receivePrepare(m)
	case s2cPromise:
		return o.receivePromise(m)
	case s2cAccept:
		return o.receiveAccept(m)
	case s2cAccepted:
		return o.receiveAccepted(m)
	default:
		return nil, errS2CProtocol
	}
}

func (o *s2cParticipant) receivePrepare(m *s2cMessage) ([]s2cOutbox, error) {
	if o.promise != nil && m.ballot.compare(o.promise.ballot) <= 0 {
		return o.outbox(o.promise, m.sender)
	}
	// An ACCEPT can establish a promise without this member seeing Prepare.
	// Its retained value supplies the immutable equal/lower-ballot snapshot.
	if o.accepted != nil && m.ballot.compare(o.accepted.ballot) <= 0 {
		p, e := s2cSignPromise(o.trust, o.key, o.config.Member, o.accepted, o.accepted.ballot, o.accepted.h, o.accepted.admission)
		if e != nil {
			return nil, e
		}
		return o.outbox(p, m.sender)
	}
	var ab s2cBallot
	var h *s2cHistoricalH
	var admission [32]byte
	if o.accepted != nil {
		ab = o.accepted.ballot
		h = o.accepted.h
		admission = o.accepted.admission
	}
	p, e := s2cSignPromise(o.trust, o.key, o.config.Member, m, ab, h, admission)
	if e != nil {
		return nil, e
	}
	out, e := o.outbox(p, m.sender)
	if e != nil {
		return nil, e
	}
	if e = o.appendRecord(s2cPRecord{kind: s2cPPromise, raw: p.raw}, o.credit); e != nil {
		return nil, e
	}
	o.observe(m)
	o.promise = p
	return out, nil
}

func (o *s2cParticipant) receivePromise(m *s2cMessage) ([]s2cOutbox, error) {
	if o.prepare == nil || m.ballot != o.prepare.ballot {
		return nil, errS2CProtocol
	}
	if o.selected != nil {
		return o.outbox(o.selected, 0)
	}
	if old := o.promises[m.sender]; old != nil && old.raw != m.raw {
		return nil, errS2CProtocol
	}
	if !o.responseFits(o.promises, m) {
		return nil, errS2CCapacity
	}
	o.promises[m.sender] = m
	if len(o.promises) < o.trust.majority() {
		return nil, nil
	}
	promises := s2cMessageList(o.promises)
	_, h, e := s2cSelectP1(o.trust, promises, o.pending[o.candidate])
	if e != nil {
		return nil, e
	}
	value := o.trust.noopValue()
	if h != nil {
		value = h.digest()
	}
	preview, e := s2cPreviewCandidate(o.trust, o.b.state, o.trust.genesis.roots, o.config.BScope, o.b.receipt, h, s2cLogicalCommit(o.trust, m.slot, value))
	if e != nil {
		return nil, e
	}
	a, e := s2cSignAccept(o.trust, o.key, o.config.Member, o.prepare, promises, h, preview.admission)
	if e != nil {
		return nil, e
	}
	out, e := o.outbox(a, 0)
	if e != nil {
		return nil, e
	}
	if e = o.appendRecord(s2cPRecord{kind: s2cPSelect, raw: a.raw}, o.credit); e != nil {
		return nil, e
	}
	o.selected = a
	return out, nil
}

func (o *s2cParticipant) receiveAccept(m *s2cMessage) ([]s2cOutbox, error) {
	if e := s2cVerifyP1(o.trust, m); e != nil {
		return nil, e
	}
	if o.promise != nil && m.ballot.compare(o.promise.ballot) < 0 {
		return nil, errS2CProtocol
	}
	if o.accepted != nil {
		cmp := m.ballot.compare(o.accepted.ballot)
		if cmp < 0 || cmp == 0 && (m.value != o.accepted.value || m.admission != o.accepted.admission) {
			return nil, errS2CProtocol
		}
		if cmp == 0 {
			reply, e := s2cSignAccepted(o.trust, o.key, o.config.Member, o.accepted, o.accepted.admission)
			if e != nil {
				return nil, e
			}
			return o.outbox(reply, m.sender)
		}
	}
	if o.selected != nil && o.selected.ballot == m.ballot && o.selected.value != m.value {
		return nil, errS2CProtocol
	}
	preview, e := s2cPreviewCandidate(o.trust, o.b.state, o.trust.genesis.roots, o.config.BScope, o.b.receipt, m.h, s2cLogicalCommit(o.trust, m.slot, m.value))
	if e != nil {
		return nil, e
	}
	if preview.admission != m.admission {
		return nil, errS2CProtocol
	}
	completion, e := o.completionCredit(m.h, preview)
	if e != nil {
		return nil, e
	}
	held := s2cMaxCredit(o.credit, completion)
	if !o.bCreditFits(held) {
		return nil, errS2CCapacity
	}
	reply, e := s2cSignAccepted(o.trust, o.key, o.config.Member, m, preview.admission)
	if e != nil {
		return nil, e
	}
	out, e := o.outbox(reply, m.sender)
	if e != nil {
		return nil, e
	}
	if e = o.appendRecord(s2cPRecord{kind: s2cPAccept, credit: held, raw: m.raw}, held); e != nil {
		return nil, e
	}
	o.observe(m)
	o.accepted = m
	o.credit = held
	// Keep an older promise snapshot only when it belongs to this exact ballot.
	// A later equal Prepare then derives its snapshot from the durable ACCEPT.
	if o.promise != nil && o.promise.ballot.compare(m.ballot) < 0 {
		o.promise = nil
	}
	return out, nil
}

func (o *s2cParticipant) receiveAccepted(m *s2cMessage) ([]s2cOutbox, error) {
	if o.selected == nil || m.ballot != o.selected.ballot || m.value != o.selected.value || m.admission != o.selected.admission {
		return nil, errS2CProtocol
	}
	if old := o.votes[m.sender]; old != nil && old.raw != m.raw {
		return nil, errS2CProtocol
	}
	if !o.responseFits(o.votes, m) {
		return nil, errS2CCapacity
	}
	o.votes[m.sender] = m
	if len(o.votes) < o.trust.majority() {
		return nil, nil
	}
	chosen, e := s2cMakeChosen(o.trust, o.selected, s2cMessageList(o.votes))
	if e != nil {
		return nil, e
	}
	out, e := o.outbox(chosen, 0)
	if e != nil {
		return nil, e
	}
	if e = o.learnChosen(chosen); e != nil {
		return nil, e
	}
	return out, nil
}

func (o *s2cParticipant) learnChosen(m *s2cMessage) error {
	next, e := s2cVerifyChosen(o.trust, m)
	if e != nil {
		return e
	}
	if m.slot <= uint64(len(o.history)) {
		old := o.history[m.slot-1]
		if old.value != m.value || old.predecessor != m.predecessor || old.admission != m.admission {
			return errS2CProtocol
		}
		if o.chosen != nil {
			return o.drainChosen()
		}
		return nil
	}
	if o.chosen != nil || m.slot != o.replayState.slot+1 || m.predecessor != o.replayState.prefix {
		return errS2CProtocol
	}
	preview, e := s2cPreviewCandidate(o.trust, o.b.state, o.trust.genesis.roots, o.config.BScope, o.b.receipt, m.h, next.certificate.commit)
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
	if !o.bCreditFits(held) {
		return &s2LocalBlockedError{next.certificate.commit, errS2CCapacity}
	}
	charge, e := s2cPCharge(uint64(len(m.raw)))
	if e != nil || charge > held.PBytes || held.PRecords < 2 {
		return errS2CCapacity
	}
	remaining := held
	remaining.PBytes -= charge
	remaining.PRecords--
	if e = o.appendRecord(s2cPRecord{kind: s2cPChosen, credit: held, raw: m.raw}, remaining); e != nil {
		if o.unknown != nil {
			return errors.Join(errS2CUnknown, e)
		}
		return &s2LocalBlockedError{next.certificate.commit, e}
	}
	o.observe(m)
	o.credit = remaining
	o.chosen = m
	o.preview = preview
	o.history = append(o.history, m)
	o.proofs = append(o.proofs, next)
	if o.hooks != nil && o.hooks.afterChosen != nil {
		o.hooks.afterChosen()
	}
	return o.drainChosen()
}

func (o *s2cParticipant) drainChosen() error {
	if o.chosen == nil {
		return nil
	}
	m := o.chosen
	preview := o.preview
	if preview == nil {
		return errS2CProtocol
	}
	if o.b.receipt.ControlSlot < m.slot {
		if o.hooks != nil && o.hooks.beforeB != nil {
			o.hooks.beforeB()
		}
		plan, e := o.b.PrepareNext(o.proofs[m.slot-1])
		if e != nil {
			return &s2LocalBlockedError{s2cLogicalCommit(o.trust, m.slot, m.value), e}
		}
		actual := o.b.pending
		if actual == nil || string(actual.payload) != preview.payload || actual.capsule != preview.capsule || actual.charge != preview.charge {
			o.unknown = errS2CProtocol
			return errors.Join(errS2CUnknown, errS2CProtocol)
		}
		_, e = o.b.CommitPrepared(plan)
		if e != nil {
			o.unknown = e
			return errors.Join(errS2CUnknown, e)
		}
		if o.credit.BBytes < preview.charge || o.credit.BRecords < 1 {
			o.unknown = errS2CProtocol
			return errS2CUnknown
		}
		o.credit.BBytes -= preview.charge
		o.credit.BRecords--
		if o.hooks != nil && o.hooks.afterB != nil {
			o.hooks.afterB()
		}
	}
	if o.b.receipt.ControlSlot != m.slot || o.b.receipt.CapsuleDigest != preview.capsuleDigest || o.b.receipt.ControlPrefix != preview.state.prefix {
		o.unknown = errS2CProtocol
		return errS2CUnknown
	}
	if o.hooks != nil && o.hooks.beforeDrained != nil {
		o.hooks.beforeDrained()
	}
	charge, _ := s2cPCharge(0)
	if o.credit.PBytes < charge || o.credit.PRecords < 1 {
		return errS2CCapacity
	}
	remaining := o.credit
	remaining.PBytes -= charge
	remaining.PRecords--
	if e := o.appendRecord(s2cPRecord{kind: s2cPDrained, receipt: o.b.receipt}, remaining); e != nil {
		return e
	}
	o.finishDrained(o.b.receipt)
	if o.hooks != nil && o.hooks.afterDrained != nil {
		o.hooks.afterDrained()
	}
	return nil
}
func (o *s2cParticipant) finishDrained(receipt s2LocalReceipt) {
	if o.chosen.h != nil {
		d := o.chosen.h.digest()
		if h := o.pending[d]; h != nil {
			o.pendingBytes -= uint64(len(h.raw))
			delete(o.pending, d)
		}
	}
	o.replayState = o.preview.state
	o.replayReceipt = receipt
	o.receipts = append(o.receipts, receipt)
	o.credit = s2cCredit{}
	o.chosen = nil
	o.preview = nil
	o.promise = nil
	o.accepted = nil
	o.prepare = nil
	o.selected = nil
	o.candidate = [32]byte{}
	o.promises = map[uint32]*s2cMessage{}
	o.votes = map[uint32]*s2cMessage{}
}

// ExportChosen is a read-only bounded copy of retained authenticated history.
// It never exposes unchosen accepts or borrows a B capsule as evidence.
func (o *s2cParticipant) ExportChosen(from, count, limit uint64) ([][]byte, error) {
	if o == nil {
		return nil, errS2CClosed
	}
	o.gate.Lock()
	defer o.gate.Unlock()
	defer o.poisonPanic()
	if e := o.readyLocked(); e != nil {
		return nil, e
	}
	if from == 0 || count == 0 || count > o.config.PPolicy.Records || limit == 0 || limit > o.config.OutboxBytes {
		return nil, errS2CProtocol
	}
	if from > uint64(len(o.history)) {
		return nil, nil
	}
	n := min(count, uint64(len(o.history))-from+1)
	var used uint64
	out := make([][]byte, 0, min(n, uint64(31)))
	for i := uint64(0); i < n; i++ {
		raw := o.history[from+i-1].raw
		if uint64(len(raw)) > limit-used {
			break
		}
		out = append(out, []byte(raw))
		used += uint64(len(raw))
	}
	return out, nil
}

// The same explicit byte budget bounds each active response set and serialized
// outbox. A refused response never spends protected completion credit.
func (o *s2cParticipant) responseFits(set map[uint32]*s2cMessage, next *s2cMessage) bool {
	var used uint64
	for id, m := range set {
		if id != next.sender {
			if uint64(len(m.raw)) > o.config.OutboxBytes-used {
				return false
			}
			used += uint64(len(m.raw))
		}
	}
	return uint64(len(next.raw)) <= o.config.OutboxBytes-used
}
