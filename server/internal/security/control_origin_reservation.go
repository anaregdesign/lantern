package security

import (
	"encoding/json"
)

const authorityAdmissionVersion uint16 = 2

func authorityAdmissionProfile() s2cAdmissionProfile {
	return s2cAdmissionProfile{Version: authorityAdmissionVersion, BrowserCode: true, HumanBearer: true,
		CredentialContract: s2cHash("current-credential-v2", "original token presence/value or native session; authentic installed S1"),
		ConsumeContract: s2cHash("current-consume-v2", struct {
			Time     string
			Lifetime uint64
			Output   string
		}{authorityTimeProfileDescription, authorityRenewalLifetime, "A: bounded immutable unit; no physical completion deadline"}),
		PurposeContract: s2cHash("current-purpose-v2", "full S1 cut, original event, exclusive origin serial consume")}
}

// A reservation is durable bookkeeping, never authentication or a handoff.
// Every row remains bounded by P's physical record/byte limits and is retained
// through intact restart. In particular, a bare reservation is not resumed by
// calling a credential verifier and resealing that ID with a later timestamp.
type authorityOriginReservation struct {
	Version     uint16
	Scope       [32]byte
	Origin      uint32
	Descriptor  [32]byte
	Incarnation [16]byte
	Serial      uint64
	ID          FullChangeID
	Operation   [32]byte
}

func (o *s2cParticipant) validateReservation(r authorityOriginReservation) error {
	d, ok := o.trust.origin(o.config.OwnedOrigin)
	if !ok || d.Profile != authorityAdmissionProfile() || r.Version != authorityAdmissionVersion || r.Scope != o.trust.scope ||
		r.Origin != d.ID || r.Descriptor != o.trust.originDigests[d.ID] || r.Incarnation != d.Incarnation ||
		o.originSerial == ^uint64(0) || r.Serial != o.originSerial+1 || !r.ID.valid() ||
		r.ID.Domain != o.replayState.projection.cut.Domain || r.ID.Cohort != o.replayState.projection.cut.Cohort ||
		r.ID.Namespace != d.Namespace || r.ID.Namespace <= o.replayState.retiredThrough || r.Operation == [32]byte{} || o.originIDs[r.ID] != 0 {
		return errS2CProtocol
	}
	return nil
}

func (o *s2cParticipant) installReservation(r authorityOriginReservation) {
	o.originSerial = r.Serial
	o.originReservations[r.Serial] = r
	o.originIDs[r.ID] = r.Serial
}

// Called only with the participant gate held. The caller preflights immutable
// H, outcome and pending charges before invoking this method. Keeping the gate
// through final consume/H append prevents another transition spending those
// credits in between; a definite refusal burns only this reservation record.
func (o *s2cParticipant) reserveOriginLocked(id FullChangeID, operation [32]byte, protected s2cCredit) (authorityOriginReservation, error) {
	var zero authorityOriginReservation
	if err := o.readyLocked(); err != nil {
		return zero, err
	}
	if o.chosen != nil {
		return zero, errS2CPending
	}
	d, ok := o.trust.origin(o.config.OwnedOrigin)
	if !ok || o.originSerial == ^uint64(0) {
		return zero, errS2CProtocol
	}
	r := authorityOriginReservation{authorityAdmissionVersion, o.trust.scope, d.ID, o.trust.originDigests[d.ID], d.Incarnation, o.originSerial + 1, id, operation}
	if err := o.validateReservation(r); err != nil {
		return zero, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return zero, err
	}
	// A reservation cannot steal a previously protected completion obligation.
	protected = s2cMaxCredit(protected, o.credit)
	if !o.bCreditFits(protected) {
		return zero, errS2CCapacity
	}
	if err = o.appendRecord(s2cPRecord{kind: s2cPOriginReservation, raw: string(raw)}, protected); err != nil {
		return zero, err
	}
	o.installReservation(r)
	return r, nil
}

func (o *s2cParticipant) replayReservation(raw string) error {
	var r authorityOriginReservation
	if len(raw) > 4096 || s2StrictJSON([]byte(raw), &r, o.trust.genesis.state.configuration.Policy) != nil {
		return errS2CProtocol
	}
	if err := o.validateReservation(r); err != nil {
		return err
	}
	o.installReservation(r)
	return nil
}

func (o *s2cParticipant) reservationMatches(h *s2cHistoricalH) bool {
	d, ok := o.trust.origin(h.originID)
	if !ok {
		return false
	}
	if d.Profile.Version == s2cVersion {
		return true
	}
	r, ok := o.originReservations[h.handoff.serial]
	return ok && r.Origin == h.originID && r.Descriptor == h.handoff.origin && r.ID == h.handoff.id && r.Operation == h.handoff.operation.digest
}
