package security

import (
	"bytes"
	"encoding/binary"
)

const logPolicyCandidateMagic = "lantern/security/log-policy/candidate\x00\x01"

func hasLogPolicyChange(h *S1Handoff) bool {
	if h == nil || h.configured != nil {
		return false
	}
	command, err := h.operation.command()
	if err != nil || command.Kind != S1Management {
		return false
	}
	for _, change := range command.Changes {
		if change.Kind == PutLogPolicy {
			return true
		}
	}
	return false
}

// Original bytes remain immutable. The separate envelope is replaceable proof
// of minimum-version support, never another origin consume or business identity.
func historicalWire(h *s2cHistoricalH) string {
	if h == nil {
		return ""
	}
	if h.candidate != "" {
		return h.candidate
	}
	return h.raw
}

func encodeLogPolicyCandidate(t *s2cTrust, original *s2cHistoricalH, certificate []byte) (*s2cHistoricalH, error) {
	if original == nil || original.candidate != "" || len(original.raw) > MaxImageBytes*2 || len(certificate) > logPolicySupportCertificateMax {
		return nil, errLogPolicySupport
	}
	n := uint64(len(logPolicyCandidateMagic) + 8 + len(original.raw) + len(certificate))
	if t == nil || n > t.bounds.HistoricalBytes || n > t.bounds.PayloadBytes {
		return nil, errLogPolicySupport
	}
	raw := append([]byte(logPolicyCandidateMagic), binary.BigEndian.AppendUint32(nil, uint32(len(original.raw)))...)
	raw = append(raw, original.raw...)
	raw = binary.BigEndian.AppendUint32(raw, uint32(len(certificate)))
	raw = append(raw, certificate...)
	return verifyLogPolicyCandidate(t, raw)
}

func verifyLogPolicyCandidate(t *s2cTrust, raw []byte) (*s2cHistoricalH, error) {
	if t == nil || t.genesis == nil || t.genesis.state == nil || t.genesis.state.projection == nil || uint64(len(raw)) > t.bounds.HistoricalBytes || uint64(len(raw)) > t.bounds.PayloadBytes || len(raw) < len(logPolicyCandidateMagic)+8 || !bytes.HasPrefix(raw, []byte(logPolicyCandidateMagic)) {
		return nil, errLogPolicySupport
	}
	if cached := t.logPolicyCache.get(t, raw); cached != nil {
		return cached, nil
	}
	reader := s2CapsuleReader{raw[len(logPolicyCandidateMagic):]}
	inner, err := reader.record()
	if err != nil || len(reader.remaining) < 4 {
		return nil, errLogPolicySupport
	}
	certificate, err := reader.record()
	if err != nil || len(reader.remaining) != 0 {
		return nil, errLogPolicySupport
	}
	original, err := verifyAuthorityHistoricalH(t, inner)
	if err != nil || !hasLogPolicyChange(original.handoff) {
		return nil, errLogPolicySupport
	}
	profile, err := logPolicyProfileForOriginal(t, original)
	if err != nil {
		return nil, err
	}
	original.logCertificate, err = decodeLogPolicySupportCertificate(t, profile, original, certificate)
	if err != nil {
		return nil, err
	}
	original.candidate = string(raw)
	t.logPolicyCache.put(t, original)
	return original, nil
}

func requireLogPolicyCandidate(t *s2cTrust, state *S1ApplyState, h *s2cHistoricalH) error {
	if state == nil || state.projection == nil {
		return ErrS1Contract
	}
	if h == nil || !hasLogPolicyChange(h.handoff) || state.projection.snapshot.LogPolicy() != nil || h.handoff.operation.reviewed != state.projection.cut {
		return nil
	}
	if h.candidate == "" {
		return errLogPolicySupport
	}
	verified, err := verifyLogPolicyCandidate(t, []byte(h.candidate))
	if err != nil || verified.raw != h.raw || verified.digest() != h.digest() {
		return errLogPolicySupport
	}
	return nil
}

func (o *s2cParticipant) installLogPolicyCandidateLocked(raw []byte) error {
	candidate, err := verifyLogPolicyCandidate(o.trust, raw)
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
	if err := o.requireLocalLogPolicyCandidate(candidate); err != nil {
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

func logPolicyCandidateCertificate(t *s2cTrust, h *s2cHistoricalH) (*logPolicySupportCertificate, error) {
	if h == nil || h.candidate == "" {
		return nil, errLogPolicySupport
	}
	verified, err := verifyLogPolicyCandidate(t, []byte(h.candidate))
	if err != nil || verified.raw != h.raw {
		return nil, errLogPolicySupport
	}
	return cloneLogPolicyCertificate(verified.logCertificate), nil
}

func (o *s2cParticipant) requireLocalLogPolicyCandidate(h *s2cHistoricalH) error {
	if h == nil || !hasLogPolicyChange(h.handoff) || o.replayState.projection.snapshot.LogPolicy() != nil || h.handoff.operation.reviewed != o.replayState.projection.cut {
		return nil
	}
	cert, err := logPolicyCandidateCertificate(o.trust, h)
	if err != nil {
		return err
	}
	if o.logPolicyFloor.Index == 0 {
		return errLogPolicySupport
	}
	for _, proof := range cert.voters {
		if proof.Signer == o.config.Member {
			if proof.Floor.Scope != o.logPolicyFloor.Scope || proof.Floor.Index != o.logPolicyFloor.Index || o.logPolicyFloor.Chain != [32]byte{} && proof.Floor.Chain != o.logPolicyFloor.Chain {
				return errLogPolicySupport
			}
			return nil
		}
	}
	return errLogPolicySupport
}

func (o *s2cParticipant) persistLogPolicyCandidateLocked(candidate *s2cHistoricalH) error {
	if candidate == nil || candidate.candidate == "" {
		return errLogPolicySupport
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
	if err := o.requireLocalLogPolicyCandidate(candidate); err != nil {
		return err
	}
	if err := o.appendRecord(s2cPRecord{kind: s2cPLogPolicyCandidate, raw: candidate.candidate}, o.credit); err != nil {
		return err
	}
	if err := o.installLogPolicyCandidateLocked([]byte(candidate.candidate)); err != nil {
		o.unknown = err
		return errS2CUnknown
	}
	return nil
}
