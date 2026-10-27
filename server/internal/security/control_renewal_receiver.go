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
	// Volatile serving veto until this configured proposer finishes its first
	// real recovery Drive. Challenges and votes remain independent of this
	// veto; clearing it never substitutes for an exact current certificate.
	startupRecoveryPending bool
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
	if r.closed || !r.kernel.configuredReadyLocked() || r.kernel.readyLocked() != nil || r.kernel.chosen != nil || r.pending != nil {
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
	if r.closed || !r.kernel.configuredReadyLocked() || r.kernel.readyLocked() != nil || r.pending == nil || !bytes.Equal(requestRaw, []byte(r.pending.raw)) {
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

type authorityRenewalBindingFailure string

const (
	authorityRenewalBindingNone          authorityRenewalBindingFailure = ""
	authorityRenewalBindingConfiguration authorityRenewalBindingFailure = "configuration"
	authorityRenewalBindingChosen        authorityRenewalBindingFailure = "chosen"
	authorityRenewalBindingSlot          authorityRenewalBindingFailure = "state_slot"
	authorityRenewalBindingPrefix        authorityRenewalBindingFailure = "state_prefix"
	authorityRenewalBindingCut           authorityRenewalBindingFailure = "projection_cut"
	authorityRenewalBindingCapsule       authorityRenewalBindingFailure = "capsule_digest"
	authorityRenewalBindingReceiptSlot   authorityRenewalBindingFailure = "receipt_control_slot"
	authorityRenewalBindingReceiptPrefix authorityRenewalBindingFailure = "receipt_control_prefix"
)

type authorityRenewalCallSite uint8

const (
	authorityRenewalDefault authorityRenewalCallSite = iota
	authorityRenewalPublicPurposeNotBefore
	authorityRenewalPublicPurposeRead
	authorityRenewalOriginPurposeBegin
	authorityRenewalOriginPurposeStart
	authorityRenewalOriginPurposeComplete
	authorityRenewalNetworkReady
)

func (site authorityRenewalCallSite) bindingStage() string {
	switch site {
	case authorityRenewalPublicPurposeNotBefore:
		return "renewal_binding_public_purpose_not_before"
	case authorityRenewalPublicPurposeRead:
		return "renewal_binding_public_purpose_read"
	case authorityRenewalOriginPurposeBegin:
		return "renewal_binding_origin_purpose_begin"
	case authorityRenewalOriginPurposeStart:
		return "renewal_binding_origin_purpose_start"
	case authorityRenewalOriginPurposeComplete:
		return "renewal_binding_origin_purpose_complete"
	case authorityRenewalNetworkReady:
		return "renewal_binding_network_ready"
	default:
		return "renewal_binding"
	}
}

// Keep the original conjunction's read and short-circuit order. This pure
// result is diagnostic data, never an additional authority or time query.
func (r *authorityRenewalReceiver) bindingFailureLocked(s authorityRenewalStatement) authorityRenewalBindingFailure {
	state, receipt := r.kernel.replayState, r.kernel.replayReceipt
	switch {
	case !r.kernel.configuredReadyLocked():
		return authorityRenewalBindingConfiguration
	case r.kernel.chosen != nil:
		return authorityRenewalBindingChosen
	case state.slot != s.Slot:
		return authorityRenewalBindingSlot
	case state.prefix != s.Prefix:
		return authorityRenewalBindingPrefix
	case state.projection.cut != s.Cut:
		return authorityRenewalBindingCut
	case receipt.CapsuleDigest != s.Capsule:
		return authorityRenewalBindingCapsule
	case receipt.ControlSlot != s.Slot:
		return authorityRenewalBindingReceiptSlot
	case receipt.ControlPrefix != s.Prefix:
		return authorityRenewalBindingReceiptPrefix
	default:
		return authorityRenewalBindingNone
	}
}

func (r *authorityRenewalReceiver) matchesLocked(s authorityRenewalStatement) bool {
	return r.bindingFailureLocked(s) == authorityRenewalBindingNone
}

// Caller holds kernel.gate and has frozen every non-time authorization input.
// The native sample in current is the final event; the remaining comparisons
// and immutable bookkeeping do not move its logical authorization instant.
func (r *authorityRenewalReceiver) currentLocked() (authorityCurrentTime, *authorityRenewalActive, error) {
	return r.currentLockedAt(authorityRenewalDefault)
}

// Site changes only the fixed diagnostic stage; callers still own kernel.gate.
func (r *authorityRenewalReceiver) currentLockedAt(site authorityRenewalCallSite) (authorityCurrentTime, *authorityRenewalActive, error) {
	reason := ""
	binding := authorityRenewalBindingNone
	switch {
	case r.closed:
		reason = "closed"
	case r.startupRecoveryPending:
		reason = "recovery"
	case !r.kernel.configuredReadyLocked():
		reason = "configuration"
	case r.kernel.readyLocked() != nil:
		reason = "kernel"
	case r.active == nil:
		reason = "no_certificate"
	default:
		binding = r.bindingFailureLocked(r.active.certificate.request.statement)
		if binding != authorityRenewalBindingNone {
			reason = "binding"
		}
	}
	if reason != "" {
		traceAuthorityBrowserDecision(r.clock, "renewal_current", reason)
		if binding != authorityRenewalBindingNone {
			traceAuthorityBrowserDecision(r.clock, site.bindingStage(), string(binding))
		}
		return authorityCurrentTime{}, nil, errAuthorityRenewal
	}
	now, err := r.clock.current()
	if err != nil {
		traceAuthorityBrowserDecision(r.clock, "renewal_current", "clock")
		return authorityCurrentTime{}, nil, err
	}
	elapsed, err := r.clock.elapsed(r.active.started, now)
	if err != nil {
		traceAuthorityBrowserDecision(r.clock, "renewal_current", "elapsed")
		return authorityCurrentTime{}, nil, errAuthorityRenewal
	}
	if elapsed.high >= r.active.certificate.request.statement.Lifetime {
		traceAuthorityBrowserDecision(r.clock, "renewal_current", "expired")
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
