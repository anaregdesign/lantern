package security

import (
	"bytes"
	"encoding/binary"
	"errors"
)

const s2cPMagic = "lantern/security/s2c/participant\x00\x01"
const (
	s2cPGenesis byte = iota + 1
	s2cPOrigin
	s2cPBallot
	s2cPPromise
	s2cPSelect
	s2cPAccept
	s2cPChosen
	s2cPDrained
)

// Fixed-width record metadata keeps quota and completion calculations exact,
// independent of decimal counters or optional JSON fields. Full signed values
// occur only in the separately length-bounded raw body.
const s2cPFixedBytes = 1 + 32 + 8 + 32 + 32 + 144 + 4

type s2cPRecord struct {
	kind      byte
	index     uint64
	candidate [32]byte
	credit    s2cCredit
	receipt   s2LocalReceipt
	raw       string
}

func (o *s2cParticipant) encodeRecord(r s2cPRecord) ([]byte, error) {
	n := uint64(len(s2cPMagic)+s2cPFixedBytes) + uint64(len(r.raw))
	if r.kind < s2cPGenesis || r.kind > s2cPDrained || r.index == 0 || n > o.trust.bounds.PayloadBytes || n > s2cMaxPayloadBytes {
		return nil, errS2CProtocol
	}
	b := make([]byte, 0, int(n))
	b = append(b, s2cPMagic...)
	b = append(b, r.kind)
	b = append(b, o.binding[:]...)
	b = binary.BigEndian.AppendUint64(b, r.index)
	b = append(b, r.candidate[:]...)
	for _, v := range []uint64{r.credit.PBytes, r.credit.PRecords, r.credit.BBytes, r.credit.BRecords} {
		b = binary.BigEndian.AppendUint64(b, v)
	}
	q := r.receipt
	b = append(b, q.ScopeDigest[:]...)
	b = binary.BigEndian.AppendUint64(b, q.LocalIndex)
	b = append(b, q.Chain[:]...)
	b = append(b, q.CapsuleDigest[:]...)
	b = binary.BigEndian.AppendUint64(b, q.ControlSlot)
	b = append(b, q.ControlPrefix[:]...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(r.raw)))
	b = append(b, r.raw...)
	return b, nil
}
func (o *s2cParticipant) decodeRecord(raw []byte, index uint64) (s2cPRecord, error) {
	var r s2cPRecord
	if len(raw) < len(s2cPMagic)+s2cPFixedBytes || uint64(len(raw)) > o.trust.bounds.PayloadBytes || uint64(len(raw)) > s2cMaxPayloadBytes || !bytes.HasPrefix(raw, []byte(s2cPMagic)) {
		return r, errS2CProtocol
	}
	b := raw[len(s2cPMagic):]
	r.kind = b[0]
	b = b[1:]
	if !bytes.Equal(b[:32], o.binding[:]) {
		return r, errS2CProtocol
	}
	b = b[32:]
	word := func() uint64 { v := binary.BigEndian.Uint64(b); b = b[8:]; return v }
	r.index = word()
	copy(r.candidate[:], b[:32])
	b = b[32:]
	r.credit = s2cCredit{word(), word(), word(), word()}
	copy(r.receipt.ScopeDigest[:], b[:32])
	b = b[32:]
	r.receipt.LocalIndex = word()
	copy(r.receipt.Chain[:], b[:32])
	b = b[32:]
	copy(r.receipt.CapsuleDigest[:], b[:32])
	b = b[32:]
	r.receipt.ControlSlot = word()
	copy(r.receipt.ControlPrefix[:], b[:32])
	b = b[32:]
	n := binary.BigEndian.Uint32(b)
	b = b[4:]
	if r.kind < s2cPGenesis || r.kind > s2cPDrained || r.index != index || uint64(n) != uint64(len(b)) {
		return r, errS2CProtocol
	}
	r.raw = string(b)
	return r, nil
}
func (o *s2cParticipant) appendRecord(r s2cPRecord, remaining s2cCredit) error {
	if o.p.count == ^uint64(0) {
		return errS2CCapacity
	}
	r.index = o.p.count + 1
	raw, e := o.encodeRecord(r)
	if e != nil {
		return e
	}
	if o.hooks != nil && o.hooks.beforePAppend != nil {
		o.hooks.beforePAppend(r.kind)
	}
	_, e = o.p.append(raw, remaining.PBytes, remaining.PRecords)
	if e != nil {
		if o.p.guard() != nil {
			o.unknown = errors.Join(errS2CUnknown, e)
		}
		if o.unknown != nil {
			return errors.Join(errS2CUnknown, e)
		}
		return e
	}
	if o.hooks != nil && o.hooks.afterPAppend != nil {
		o.hooks.afterPAppend(r.kind)
	}
	return nil
}
func s2cPCharge(body uint64) (uint64, error) {
	return s2CheckedAdd(uint64(len(s2cPMagic)+s2cPFixedBytes+88), body)
}
func (o *s2cParticipant) completionCredit(h *s2cHistoricalH, preview *s2cPreview) (s2cCredit, error) {
	bound := s2cChosenBytesBound(o.trust, h)
	chosen, e := s2cPCharge(bound)
	if e != nil || bound == 0 || bound+uint64(len(s2cPMagic)+s2cPFixedBytes) > o.trust.bounds.PayloadBytes || bound+uint64(len(s2cPMagic)+s2cPFixedBytes) > s2cMaxPayloadBytes {
		return s2cCredit{}, errS2CCapacity
	}
	drained, _ := s2cPCharge(0)
	total, e := s2CheckedAdd(chosen, drained)
	if e != nil {
		return s2cCredit{}, e
	}
	return s2cCredit{total, 2, preview.charge, 1}, nil
}
func s2cMaxCredit(a, b s2cCredit) s2cCredit {
	return s2cCredit{max(a.PBytes, b.PBytes), max(a.PRecords, b.PRecords), max(a.BBytes, b.BBytes), max(a.BRecords, b.BRecords)}
}
func (o *s2cParticipant) bCreditFits(c s2cCredit) bool {
	used, count := o.b.used, o.b.receipt.LocalIndex
	return used <= o.config.BScope.Storage.JournalBytes && c.BBytes <= o.config.BScope.Storage.JournalBytes-used && count <= o.config.BScope.Storage.ReplayRecords && c.BRecords <= o.config.BScope.Storage.ReplayRecords-count
}
