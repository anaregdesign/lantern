package security

import (
	"bytes"
	"encoding/binary"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

const operationalPolicyCandidateMagic = "lantern/security/operational-policy/candidate\x00\x01"

func handoffOf(h *s2cHistoricalH) *S1Handoff {
	if h == nil {
		return nil
	}
	return h.handoff
}

func operationalPolicyChangeForH(h *S1Handoff) (operationalpolicy.Change, operationalpolicy.Group, bool) {
	if h == nil || h.configured != nil {
		return operationalpolicy.Change{}, "", false
	}
	command, err := h.operation.command()
	if err != nil || command.Kind != S1Management {
		return operationalpolicy.Change{}, "", false
	}
	for _, change := range command.Changes {
		if change.Kind == PutOperationalPolicy && change.OperationalPolicy != nil {
			group, err := change.OperationalPolicy.Values.Group()
			return change.OperationalPolicy.Clone(), group, err == nil
		}
	}
	return operationalpolicy.Change{}, "", false
}
func hasOperationalPolicyChange(h *S1Handoff) bool {
	_, _, ok := operationalPolicyChangeForH(h)
	return ok
}
func operationalPolicySupportRequired(state *S1ApplyState, h *s2cHistoricalH) bool {
	_, group, ok := operationalPolicyChangeForH(handoffOf(h))
	return ok && state != nil && state.projection != nil && (group == operationalpolicy.GroupSearch || state.projection.snapshot.OperationalPolicies().Get(group) == nil) && h.handoff.operation.reviewed == state.projection.cut
}

// Original bytes remain immutable. The separate envelope is replaceable proof
// of minimum-version support, never another origin consume or business identity.
func encodeOperationalPolicyCandidate(t *s2cTrust, original *s2cHistoricalH, certificate []byte) (*s2cHistoricalH, error) {
	if original == nil || original.candidate != "" || len(original.raw) > MaxImageBytes*2 || len(certificate) > operationalPolicySupportCertificateMax {
		return nil, errOperationalPolicySupport
	}
	n := uint64(len(operationalPolicyCandidateMagic) + 8 + len(original.raw) + len(certificate))
	if t == nil || n > t.bounds.HistoricalBytes || n > t.bounds.PayloadBytes {
		return nil, errOperationalPolicySupport
	}
	raw := append([]byte(operationalPolicyCandidateMagic), binary.BigEndian.AppendUint32(nil, uint32(len(original.raw)))...)
	raw = append(raw, original.raw...)
	raw = binary.BigEndian.AppendUint32(raw, uint32(len(certificate)))
	raw = append(raw, certificate...)
	return verifyOperationalPolicyCandidate(t, raw)
}

func verifyOperationalPolicyCandidate(t *s2cTrust, raw []byte) (*s2cHistoricalH, error) {
	if t == nil || t.genesis == nil || t.genesis.state == nil || t.genesis.state.projection == nil || uint64(len(raw)) > t.bounds.HistoricalBytes || uint64(len(raw)) > t.bounds.PayloadBytes || len(raw) < len(operationalPolicyCandidateMagic)+8 || !bytes.HasPrefix(raw, []byte(operationalPolicyCandidateMagic)) {
		return nil, errOperationalPolicySupport
	}
	if cached := t.operationalPolicyCache.get(t, raw); cached != nil {
		return cached, nil
	}
	reader := s2CapsuleReader{raw[len(operationalPolicyCandidateMagic):]}
	inner, err := reader.record()
	if err != nil || len(reader.remaining) < 4 {
		return nil, errOperationalPolicySupport
	}
	certificate, err := reader.record()
	if err != nil || len(reader.remaining) != 0 {
		return nil, errOperationalPolicySupport
	}
	original, err := verifyAuthorityHistoricalH(t, inner)
	if err != nil || !hasOperationalPolicyChange(original.handoff) {
		return nil, errOperationalPolicySupport
	}
	profile, err := operationalPolicyProfileForOriginal(t, original)
	if err != nil {
		return nil, err
	}
	original.operationalCertificate, err = decodeOperationalPolicySupportCertificate(t, profile, original, certificate)
	if err != nil {
		return nil, err
	}
	original.candidate = string(raw)
	t.operationalPolicyCache.put(t, original)
	return original, nil
}

func requireOperationalPolicyCandidate(t *s2cTrust, state *S1ApplyState, h *s2cHistoricalH) error {
	if state == nil || state.projection == nil {
		return ErrS1Contract
	}
	if !operationalPolicySupportRequired(state, h) {
		return nil
	}
	if h.candidate == "" {
		return errOperationalPolicySupport
	}
	verified, err := verifyOperationalPolicyCandidate(t, []byte(h.candidate))
	if err != nil || verified.raw != h.raw || verified.digest() != h.digest() {
		return errOperationalPolicySupport
	}
	return nil
}

func (o *s2cParticipant) installOperationalPolicyCandidateLocked(raw []byte) error {
	candidate, err := verifyOperationalPolicyCandidate(o.trust, raw)
	if err != nil {
		return err
	}
	old := o.origins[candidate.digest()]
	if old == nil || old.raw != candidate.raw {
		return errS2CProtocol
	}
	if old.candidate != "" {
		if old.candidate != candidate.candidate {
			return ErrChangeConflict
		}
		return nil
	}
	if err := o.requireLocalOperationalPolicyCandidate(candidate); err != nil {
		return err
	}
	before, after := uint64(len(historicalWire(old))), uint64(len(historicalWire(candidate)))
	if o.pending[candidate.digest()] == nil || o.pendingBytes < before || after > o.config.PendingBytes-(o.pendingBytes-before) {
		return errS2CCapacity
	}
	o.origins[candidate.digest()], o.pending[candidate.digest()] = candidate, candidate
	o.pendingBytes = o.pendingBytes - before + after
	return nil
}

func operationalPolicyCandidateCertificate(t *s2cTrust, h *s2cHistoricalH) (*operationalPolicySupportCertificate, error) {
	if h == nil || h.candidate == "" {
		return nil, errOperationalPolicySupport
	}
	verified, err := verifyOperationalPolicyCandidate(t, []byte(h.candidate))
	if err != nil || verified.raw != h.raw {
		return nil, errOperationalPolicySupport
	}
	return cloneOperationalPolicyCertificate(verified.operationalCertificate), nil
}

func (o *s2cParticipant) requireLocalOperationalPolicyCandidate(h *s2cHistoricalH) error {
	if !operationalPolicySupportRequired(o.replayState, h) {
		return nil
	}
	cert, err := operationalPolicyCandidateCertificate(o.trust, h)
	if err != nil {
		return err
	}
	floor := o.operationalFloors[cert.statement.Group]
	if floor == nil {
		return errOperationalPolicySupport
	}
	for _, proof := range cert.voters {
		if proof.Signer == o.config.Member {
			if proof.Floor.Scope != floor.floor.Scope || proof.Floor.Index != floor.floor.Index || floor.floor.Chain != [32]byte{} && proof.Floor.Chain != floor.floor.Chain {
				return errOperationalPolicySupport
			}
			return nil
		}
	}
	return errOperationalPolicySupport
}

func (o *s2cParticipant) persistOperationalPolicyCandidateLocked(candidate *s2cHistoricalH) error {
	if candidate == nil || candidate.candidate == "" {
		return errOperationalPolicySupport
	}
	old := o.origins[candidate.digest()]
	if old == nil || old.raw != candidate.raw {
		return errS2CProtocol
	}
	if old.candidate != "" {
		if old.candidate != candidate.candidate {
			return ErrChangeConflict
		}
		return nil
	}
	before, after := uint64(len(historicalWire(old))), uint64(len(historicalWire(candidate)))
	if o.pending[candidate.digest()] == nil || o.pendingBytes < before || after > o.config.PendingBytes-(o.pendingBytes-before) {
		return errS2CCapacity
	}
	if err := o.requireLocalOperationalPolicyCandidate(candidate); err != nil {
		return err
	}
	if err := o.appendRecord(s2cPRecord{kind: s2cPOperationalPolicyCandidate, raw: candidate.candidate}, o.credit); err != nil {
		return err
	}
	if err := o.installOperationalPolicyCandidateLocked([]byte(candidate.candidate)); err != nil {
		o.unknown = err
		return errS2CUnknown
	}
	return nil
}
