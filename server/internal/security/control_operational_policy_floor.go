package security

import (
	"bytes"
	"encoding/json"

	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

type operationalPolicyFloorRecord struct {
	Version            uint16
	Group              operationalpolicy.Group
	ApplicationVersion uint32
	Contract           [32]byte
	Original           []byte
}

type operationalPolicyFloorState struct {
	floor       s2cJournalFloor
	original    string
	preparation OperationalPolicyPreparation
}

func operationalPolicyFloorBytes(h *s2cHistoricalH) ([]byte, error) {
	_, group, ok := operationalPolicyChangeForH(handoffOf(h))
	if h == nil || !ok {
		return nil, errOperationalPolicySupport
	}
	return json.Marshal(operationalPolicyFloorRecord{operationalPolicySupportContractVersion, group, operationalpolicy.Version, operationalPolicySupportContract(group), []byte(h.raw)})
}

func operationalPolicyFloorSizeBound(originalBytes uint64, group operationalpolicy.Group) (uint64, error) {
	if originalBytes > uint64(MaxImageBytes)*2 || !group.Valid() {
		return 0, errS2CCapacity
	}
	header, err := json.Marshal(operationalPolicyFloorRecord{operationalPolicySupportContractVersion, group, operationalpolicy.Version, operationalPolicySupportContract(group), []byte{}})
	if err != nil {
		return 0, err
	}
	return s2CheckedAdd(uint64(len(header)), 4*((originalBytes+2)/3))
}

func (o *s2cParticipant) assessOperationalPolicyFloor(h *s2cHistoricalH) (S1Assessment, error) {
	if h == nil || !operationalPolicySupportRequired(o.replayState, h) {
		return S1Assessment{}, errOperationalPolicySupport
	}
	command, err := h.handoff.operation.command()
	if err != nil {
		return S1Assessment{}, err
	}
	if h.handoff.operation.reviewed != o.replayState.projection.cut {
		return S1Assessment{}, ErrRevisionConflict
	}
	if !s1CurrentAuthority(o.replayState.projection, h.handoff, command) {
		return S1Assessment{}, ErrPermissionDenied
	}
	assessment, err := AssessS1(o.replayState.projection, h.handoff.operation)
	if err != nil || assessment.ProvenNonexpanding {
		return S1Assessment{}, errOperationalPolicySupport
	}
	if assessment.NeedsPurpose && h.handoff.authorization.purposeEvidence == [32]byte{} {
		return S1Assessment{}, ErrOperationAuthorization
	}
	return assessment, nil
}

func operationalPolicyPreparationFor(h *s2cHistoricalH, group operationalpolicy.Group) OperationalPolicyPreparation {
	return OperationalPolicyPreparation{Group: group, Version: operationalpolicy.Version, ChangeID: h.handoff.id, IntentDigest: h.handoff.operation.digest, ActorDigest: s2cHash("public-original-actor", h.handoff.operation.actor)}
}

func (o *s2cParticipant) replayOperationalPolicyFloor(r s2cPRecord) error {
	var record operationalPolicyFloorRecord
	if s2StrictJSON([]byte(r.raw), &record, DefaultPolicyLimits()) != nil || !record.Group.Valid() || record.Version != operationalPolicySupportContractVersion || record.ApplicationVersion != operationalpolicy.Version || record.Contract != operationalPolicySupportContract(record.Group) || o.operationalFloors[record.Group] != nil {
		return errS2CProtocol
	}
	h, err := verifyAuthorityHistoricalH(o.trust, record.Original)
	_, group, ok := operationalPolicyChangeForH(handoffOf(h))
	if err != nil || !ok || group != record.Group || o.replayState.projection.snapshot.OperationalPolicies().Get(group) != nil {
		return errS2CProtocol
	}
	assessment, err := o.assessOperationalPolicyFloor(h)
	if err != nil {
		return err
	}
	if _, err = authoritySuccessorCharge(o, h.handoff.id, h.handoff.operation, assessment); err != nil {
		return err
	}
	canonical, err := operationalPolicyFloorBytes(h)
	if err != nil || !bytes.Equal(canonical, []byte(r.raw)) {
		return errS2CProtocol
	}
	o.operationalFloors[group] = &operationalPolicyFloorState{floor: s2cJournalFloor{Scope: o.binding, Index: r.index}, original: h.raw, preparation: operationalPolicyPreparationFor(h, group)}
	return nil
}

func (o *s2cParticipant) restoreOperationalPolicyFloors() error {
	var groups []operationalpolicy.Group
	var indexes []uint64
	for _, group := range operationalpolicy.Groups() {
		if f := o.operationalFloors[group]; f != nil && f.floor.Chain == [32]byte{} {
			groups, indexes = append(groups, group), append(indexes, f.floor.Index)
		}
	}
	if len(groups) != 0 {
		cuts, err := mutationlog.InspectFileWALCuts(o.config.PPath, indexes, s2cDecodeJournalPayload, func(e mutationlog.Entry) error {
			if _, ok := e.Op.(s2cJournalPayload); !ok {
				return errS2CProtocol
			}
			return nil
		})
		if err != nil {
			return err
		}
		for i, cut := range cuts {
			if cut.ObservedLast < indexes[i] || cut.ChainSHA256 == [32]byte{} {
				return errS2CProtocol
			}
			o.operationalFloors[groups[i]].floor.Chain = cut.ChainSHA256
		}
	}
	check := func(h *s2cHistoricalH) error {
		if h == nil || h.candidate == "" || !hasOperationalPolicyChange(h.handoff) {
			return nil
		}
		cert, err := operationalPolicyCandidateCertificate(o.trust, h)
		if err != nil {
			return err
		}
		floor := o.operationalFloors[cert.statement.Group]
		if floor == nil {
			return errS2CProtocol
		}
		for _, proof := range cert.voters {
			if proof.Signer == o.config.Member && proof.Floor != floor.floor {
				return errS2CProtocol
			}
		}
		return nil
	}
	for _, h := range o.origins {
		if err := check(h); err != nil {
			return err
		}
	}
	for _, m := range o.history {
		if err := check(m.h); err != nil {
			return err
		}
	}
	for _, group := range operationalpolicy.Groups() {
		if o.replayState.projection.snapshot.OperationalPolicies().Get(group) != nil && o.operationalFloors[group] == nil {
			return errS2CProtocol
		}
	}
	return nil
}

func (o *s2cParticipant) ensureOperationalPolicyFloorLocked(h *s2cHistoricalH, protected s2cCredit) error {
	_, group, ok := operationalPolicyChangeForH(handoffOf(h))
	if !ok {
		return errOperationalPolicySupport
	}
	if o.operationalFloors[group] != nil {
		return o.restoreOperationalPolicyFloors()
	}
	raw, err := operationalPolicyFloorBytes(h)
	if err != nil {
		return err
	}
	if err = o.appendRecord(s2cPRecord{kind: s2cPOperationalPolicyFloor, raw: string(raw)}, s2cMaxCredit(o.credit, protected)); err != nil {
		return err
	}
	o.operationalFloors[group] = &operationalPolicyFloorState{floor: o.p.floor, original: h.raw, preparation: operationalPolicyPreparationFor(h, group)}
	return nil
}
