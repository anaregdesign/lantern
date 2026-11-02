package security

import (
	"bytes"
	"encoding/json"

	"github.com/anaregdesign/lantern/core/mutationlog"
)

type logPolicyFloorRecord struct {
	Version  uint16
	Contract [32]byte
	Original []byte
}

func logPolicyFloorBytes(h *s2cHistoricalH) ([]byte, error) {
	if h == nil || !hasLogPolicyChange(h.handoff) {
		return nil, errLogPolicySupport
	}
	return json.Marshal(logPolicyFloorRecord{logPolicySupportContractVersion, logPolicySupportContract(), []byte(h.raw)})
}

func logPolicyFloorSizeBound(originalBytes uint64) (uint64, error) {
	if originalBytes > uint64(MaxImageBytes)*2 {
		return 0, errS2CCapacity
	}
	header, err := json.Marshal(logPolicyFloorRecord{logPolicySupportContractVersion, logPolicySupportContract(), []byte{}})
	if err != nil {
		return 0, err
	}
	return s2CheckedAdd(uint64(len(header)), 4*((originalBytes+2)/3))
}

// Both live side effects and historical replay enforce the same logical
// authorization at the exact predecessor. Native time is sampled only live.
func (k *s2cParticipant) assessLogPolicyFloor(h *s2cHistoricalH) (S1Assessment, error) {
	if h == nil || !hasLogPolicyChange(h.handoff) || k.replayState.projection.snapshot.LogPolicy() != nil {
		return S1Assessment{}, errLogPolicySupport
	}
	command, err := h.handoff.operation.command()
	if err != nil {
		return S1Assessment{}, err
	}
	if h.handoff.operation.reviewed != k.replayState.projection.cut {
		return S1Assessment{}, ErrRevisionConflict
	}
	if !s1CurrentAuthority(k.replayState.projection, h.handoff, command) {
		return S1Assessment{}, ErrPermissionDenied
	}
	assessment, err := AssessS1(k.replayState.projection, h.handoff.operation)
	if err != nil || assessment.ProvenNonexpanding {
		return S1Assessment{}, errLogPolicySupport
	}
	if assessment.NeedsPurpose && h.handoff.authorization.purposeEvidence == [32]byte{} {
		return S1Assessment{}, ErrOperationAuthorization
	}
	return assessment, nil
}

func (k *s2cParticipant) replayLogPolicyFloor(r s2cPRecord) error {
	var floor logPolicyFloorRecord
	if k.logPolicyFloor.Index != 0 || s2StrictJSON([]byte(r.raw), &floor, DefaultPolicyLimits()) != nil || floor.Version != logPolicySupportContractVersion || floor.Contract != logPolicySupportContract() {
		return errS2CProtocol
	}
	h, err := verifyAuthorityHistoricalH(k.trust, floor.Original)
	if err != nil || !hasLogPolicyChange(h.handoff) {
		return errS2CProtocol
	}
	assessment, err := k.assessLogPolicyFloor(h)
	if err != nil {
		return err
	}
	if _, err = authoritySuccessorCharge(k, h.handoff.id, h.handoff.operation, assessment); err != nil {
		return err
	}
	canonical, err := logPolicyFloorBytes(h)
	if err != nil || !bytes.Equal(canonical, []byte(r.raw)) {
		return errS2CProtocol
	}
	k.logPolicyFloor = s2cJournalFloor{Scope: k.binding, Index: r.index}
	k.logPolicyFloorOriginal = h.raw
	k.logPolicyPreparation = LogPolicyPreparation{ChangeID: h.handoff.id, IntentDigest: h.handoff.operation.digest, ActorDigest: s2cHash("public-original-actor", h.handoff.operation.actor)}
	return nil
}

// Complete P history is retained today. This exact prefix check reconstructs
// the minimum-version marker witness without inventing one from the B image.
func (k *s2cParticipant) restoreLogPolicyFloor() error {
	if k.logPolicyFloor.Index == 0 || k.logPolicyFloor.Chain != [32]byte{} {
		return nil
	}
	cut, err := mutationlog.InspectFileWALCut(k.config.PPath, k.logPolicyFloor.Index, s2cDecodeJournalPayload, func(e mutationlog.Entry) error {
		if _, ok := e.Op.(s2cJournalPayload); !ok {
			return errS2CProtocol
		}
		return nil
	})
	if err != nil || cut.ObservedLast < k.logPolicyFloor.Index || cut.ChainSHA256 == [32]byte{} {
		return errS2CProtocol
	}
	k.logPolicyFloor.Chain = cut.ChainSHA256
	check := func(h *s2cHistoricalH) error {
		if h == nil || h.candidate == "" || !hasLogPolicyChange(h.handoff) {
			return nil
		}
		cert, err := logPolicyCandidateCertificate(k.trust, h)
		if err != nil {
			return err
		}
		for _, v := range cert.voters {
			if v.Signer == k.config.Member && v.Floor != k.logPolicyFloor {
				return errS2CProtocol
			}
		}
		return nil
	}
	for _, h := range k.origins {
		if err := check(h); err != nil {
			return err
		}
	}
	for _, m := range k.history {
		if err := check(m.h); err != nil {
			return err
		}
	}
	return nil
}

func (k *s2cParticipant) ensureLogPolicyFloorLocked(h *s2cHistoricalH, protected s2cCredit) error {
	if k.logPolicyFloor.Index != 0 {
		return k.restoreLogPolicyFloor()
	}
	raw, err := logPolicyFloorBytes(h)
	if err != nil {
		return err
	}
	if err = k.appendRecord(s2cPRecord{kind: s2cPLogPolicyFloor, raw: string(raw)}, s2cMaxCredit(k.credit, protected)); err != nil {
		return err
	}
	k.logPolicyFloor = k.p.floor
	k.logPolicyFloorOriginal = h.raw
	k.logPolicyPreparation = LogPolicyPreparation{ChangeID: h.handoff.id, IntentDigest: h.handoff.operation.digest, ActorDigest: s2cHash("public-original-actor", h.handoff.operation.actor)}
	return nil
}
