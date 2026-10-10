package security

import (
	"bytes"
	"crypto/rand"
)

// Every field is owned by kernel.gate. No persisted or decoded object can
// restore pending.started or the live capability on a new process.
type authorityRenewalReceiver struct {
	kernel    *s2cParticipant
	clock     *authorityTimeOwner
	workloads [32]byte
	pending   *authorityRenewalPending
	active    *authorityRenewalActive
	closed    bool
}

type authorityRenewalPending struct {
	request authorityRenewalRequest
	raw     string
	started authorityCurrentTime
	votes   map[uint32]authorityRenewalVote
}

type authorityRenewalActive struct {
	certificate authorityRenewalCertificate
	started     authorityCurrentTime
}

func (r *authorityRenewalReceiver) challenge() ([]byte, error) {
	r.kernel.gate.Lock()
	defer r.kernel.gate.Unlock()
	defer r.kernel.poisonPanic()
	if r.closed || r.kernel.readyLocked() != nil || r.kernel.chosen != nil || r.pending != nil {
		return nil, errAuthorityRenewal
	}
	s, receipt, err := r.kernel.b.ReadLocalCut()
	if err != nil {
		r.kernel.unknown = err
		return nil, err
	}
	var challenge [32]byte
	if _, err = rand.Read(challenge[:]); err != nil {
		return nil, err
	}
	// The request-start instant precedes signing and every network send. Receipt
	// or certificate installation never supplies a new start instant.
	start, err := r.clock.current()
	if err != nil {
		return nil, err
	}
	statement := authorityRenewalStatement{1, r.kernel.trust.scope, r.kernel.trust.memberSet, r.workloads, start.profile, r.kernel.config.Member, start.stamp.process, challenge, s.slot, s.prefix, receipt.CapsuleDigest, s.projection.cut, authorityRenewalLifetime}
	raw, err := signAuthorityRenewalRequest(statement, r.kernel.trust, r.workloads, r.kernel.key)
	if err != nil {
		return nil, err
	}
	request, err := parseAuthorityRenewalRequest(raw, r.kernel.trust, r.workloads)
	if err != nil {
		return nil, err
	}
	r.pending = &authorityRenewalPending{request, string(raw), start, make(map[uint32]authorityRenewalVote, len(r.kernel.trust.members))}
	return raw, nil
}

func (r *authorityRenewalReceiver) receive(requestRaw, voteRaw []byte) error {
	r.kernel.gate.Lock()
	defer r.kernel.gate.Unlock()
	defer r.kernel.poisonPanic()
	if r.closed || r.kernel.readyLocked() != nil || r.pending == nil || !bytes.Equal(requestRaw, []byte(r.pending.raw)) {
		return errAuthorityRenewal
	}
	p := r.pending
	vote, err := parseAuthorityRenewalVote(voteRaw, p.request, r.kernel.trust)
	if err != nil {
		return err
	}
	// A correctly matched response consumes an unusable pending window. Old
	// signatures cannot later activate after catch-up, expiry or a boot change.
	now, err := r.clock.current()
	if err != nil {
		r.pending = nil
		return err
	}
	elapsed, err := r.clock.elapsed(p.started, now)
	if err != nil || elapsed.high >= p.request.statement.Lifetime || !r.matchesLocked(p.request.statement) {
		r.pending = nil
		return errAuthorityRenewal
	}
	if _, duplicate := p.votes[vote.member]; duplicate {
		return errAuthorityRenewal
	}
	p.votes[vote.member] = vote
	if len(p.votes) < r.kernel.trust.majority() {
		return nil
	}
	votes := make([]authorityRenewalVote, 0, len(p.votes))
	for _, v := range p.votes {
		votes = append(votes, v)
	}
	certificate, err := encodeAuthorityRenewalCertificate([]byte(p.raw), votes, r.kernel.trust, r.workloads)
	r.pending = nil
	if err != nil {
		return err
	}
	r.active = &authorityRenewalActive{certificate, p.started}
	return nil
}

func (r *authorityRenewalReceiver) matchesLocked(s authorityRenewalStatement) bool {
	state, receipt := r.kernel.replayState, r.kernel.replayReceipt
	return r.kernel.chosen == nil && state.slot == s.Slot && state.prefix == s.Prefix && state.projection.cut == s.Cut && receipt.CapsuleDigest == s.Capsule && receipt.ControlSlot == s.Slot && receipt.ControlPrefix == s.Prefix
}

// Caller holds kernel.gate and has frozen every non-time authorization input.
// The native sample in current is the final event; the remaining comparisons
// and immutable bookkeeping do not move its logical authorization instant.
func (r *authorityRenewalReceiver) currentLocked() (authorityCurrentTime, *authorityRenewalActive, error) {
	if r.closed || r.kernel.readyLocked() != nil || r.active == nil || !r.matchesLocked(r.active.certificate.request.statement) {
		return authorityCurrentTime{}, nil, errAuthorityRenewal
	}
	now, err := r.clock.current()
	if err != nil {
		return authorityCurrentTime{}, nil, err
	}
	elapsed, err := r.clock.elapsed(r.active.started, now)
	if err != nil || elapsed.high >= r.active.certificate.request.statement.Lifetime {
		return authorityCurrentTime{}, nil, errAuthorityRenewal
	}
	return now, r.active, nil
}

func (r *authorityRenewalReceiver) abandon(requestRaw []byte) {
	r.kernel.gate.Lock()
	defer r.kernel.gate.Unlock()
	if r.pending != nil && bytes.Equal(requestRaw, []byte(r.pending.raw)) {
		r.pending = nil
	}
}

func (r *authorityRenewalReceiver) close() {
	r.kernel.gate.Lock()
	defer r.kernel.gate.Unlock()
	r.closed = true
	r.pending = nil
	r.active = nil
}
