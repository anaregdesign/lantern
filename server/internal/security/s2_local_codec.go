package security

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
)

const (
	s2LocalMagic                  = "lantern/security/s2/local-materialization\x00\x01"
	s2LocalGenesis         byte   = 1
	s2LocalApply           byte   = 2
	s2LocalMaxHeaderBytes         = 4 << 10
	s2LocalMaxPayloadBytes uint64 = (32 << 20) - 36
)

var (
	errS2LocalRecord = errors.New("invalid S2 local materialization record")
	errS2LocalScope  = errors.New("invalid S2 local materialization scope")
)

// These are explicit file-content limits, not a reservation of physical disk
// blocks or resources for a future voter. They are fixed across ordinary reopen.
type s2LocalStoragePolicy struct {
	Capsule       s2CapsuleLimits
	JournalBytes  uint64
	ReplayRecords uint64
}

func (p s2LocalStoragePolicy) valid() bool {
	return p.Capsule.valid() && p.JournalBytes > 0 && p.JournalBytes <= uint64(MaxSystemJournalBytes) && p.ReplayRecords > 0 && p.ReplayRecords <= p.JournalBytes/44
}

// Scope comes independently from the owner caller. Decoded GENESIS data never
// supplies the expected scope, trusted genesis or a verified replay capability.
// All values, including the full execution and storage policies, are bound.
type s2LocalScope struct {
	StoreIdentity [32]byte
	JournalEpoch  [16]byte
	Domain        [32]byte
	Cohort        [32]byte
	Generation    [16]byte
	Membership    [32]byte
	Fences        [32]byte
	Configuration S1ExecutionConfig
	ConfigDigest  [32]byte
	Genesis       [32]byte
	Floor         s2CapsuleFloor
	Storage       s2LocalStoragePolicy
}

func (s s2LocalScope) valid() bool {
	return s.StoreIdentity != [32]byte{} && s.JournalEpoch != [16]byte{} && s.Domain != [32]byte{} && s.Cohort != [32]byte{} && s.Generation != [16]byte{} && s.Membership != [32]byte{} && s.Fences != [32]byte{} && s.Configuration.valid() && s.ConfigDigest == s.Configuration.Digest() && s.Genesis != [32]byte{} && s.Floor.Domain == s.Domain && s.Floor.Cohort == s.Cohort && (s.Floor.Through == 0 && s.Floor.Root == [32]byte{} || s.Floor.Through != 0 && s.Floor.Root != [32]byte{}) && s.Storage.valid()
}

func (s s2LocalScope) digest() [32]byte {
	if !s.valid() {
		return [32]byte{}
	}
	b, _ := json.Marshal(s)
	return s2LocalHash("lantern/security/s2/local-scope\x00\x01", b)
}

func (s s2LocalScope) tipBinding() [32]byte {
	d := s.digest()
	if d == [32]byte{} {
		return [32]byte{}
	}
	return s2LocalHash("lantern/security/s2/local-tip-binding\x00\x01", d[:])
}

func s2LocalHash(label string, b []byte) [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte(label))
	_, _ = h.Write(b)
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest
}

// Commit to every exact capsule byte, including ledger and original outcomes.
// This is an integrity commitment only; it cannot attest the retained history.
func s2LocalCapsuleDigest(capsule []byte) [32]byte {
	return s2LocalHash("lantern/security/s2/local-capsule\x00\x01", capsule)
}

func (s s2LocalScope) matchesCapsule(h s2CapsuleHeader) bool {
	c := h.Cut
	return h.valid() && c.Version == S1Version && c.Domain == s.Domain && c.Cohort == s.Cohort && c.Generation == s.Generation && c.Fences == s.Fences && c.Policy == s1PolicyConfiguration(s.Configuration.Policy) && h.Membership == s.Membership && h.Configuration == s.Configuration && h.ConfigDigest == s.ConfigDigest && h.Genesis == s.Genesis && h.Floor == s.Floor
}

func (s s2LocalScope) validateGenesis(g *s2TrustedGenesis) error {
	if !s.valid() || g == nil || g.state == nil || g.state.projection == nil || g.state.slot != 0 || len(g.state.ledger) != 0 || g.state.projection.cut.Sequence != 1 || g.state.projection.cut.Previous != [32]byte{} {
		return errS2LocalScope
	}
	h, err := s2Header(g.state, g.roots)
	if err != nil || !s.matchesCapsule(h) {
		return errS2LocalScope
	}
	if _, _, err := s2EncodeCapsule(g.state, g.roots, s.Storage.Capsule); err != nil {
		return err
	}
	return nil
}

// This format starts with GENESIS at revision 1. SystemMetadata cannot install
// MaxUint64, so neither a local revision nor the preceding slot may wrap there.
func s2LocalIndex(slot uint64) (uint64, error) {
	if slot > math.MaxUint64-2 {
		return 0, errS2LocalRecord
	}
	return slot + 1, nil
}

// JSON struct order is part of the versioned canonical wire format.
type s2LocalGenesisHeader struct {
	Scope         s2LocalScope
	LocalIndex    uint64
	CapsuleDigest [32]byte
}

type s2LocalApplyHeader struct {
	ScopeDigest           [32]byte
	LocalIndex            uint64
	PreviousCapsuleDigest [32]byte
	CapsuleDigest         [32]byte
	Commit                CommitRef
}

// An untrusted typed envelope with immutable owned capsule bytes. In particular,
// a decoded record is not an S1 state or an independently certified transition.
type s2LocalRecord struct {
	kind                  byte
	scope                 s2LocalScope
	scopeDigest           [32]byte
	localIndex            uint64
	previousCapsuleDigest [32]byte
	capsuleDigest         [32]byte
	commit                CommitRef
	capsule               string
}

func s2LocalPayloadSize(headerLength, capsuleLength uint64) (uint64, error) {
	if headerLength == 0 || headerLength > s2LocalMaxHeaderBytes || capsuleLength == 0 || capsuleLength > s2MaxCapsuleBytes {
		return 0, errS2LocalRecord
	}
	n, err := s2CheckedAdd(uint64(len(s2LocalMagic))+9, headerLength)
	if err == nil {
		n, err = s2CheckedAdd(n, capsuleLength)
	}
	if err != nil || n > s2LocalMaxPayloadBytes {
		return 0, errS2LocalRecord
	}
	return n, nil
}

func (r *s2LocalRecord) validate(expected s2LocalScope) error {
	if r == nil || !expected.valid() || r.scopeDigest != expected.digest() || uint64(len(r.capsule)) > expected.Storage.Capsule.Bytes || r.capsuleDigest != s2LocalCapsuleDigest([]byte(r.capsule)) {
		return errS2LocalRecord
	}
	// Reuse the whole bounded S2-A parser, then inspect its strictly canonical
	// header. No decoded projection/ledger escapes this validation boundary.
	if _, err := s2DecodeCapsule([]byte(r.capsule), expected.Storage.Capsule); err != nil {
		return errS2LocalRecord
	}
	reader := s2CapsuleReader{[]byte(r.capsule)[len(s2CapsuleMagic):]}
	row, err := reader.record()
	var h s2CapsuleHeader
	if err != nil || s2StrictJSON(row, &h, expected.Configuration.Policy) != nil || !expected.matchesCapsule(h) {
		return errS2LocalRecord
	}
	index, err := s2LocalIndex(h.Slot)
	if err != nil || r.localIndex != index || r.localIndex > expected.Storage.ReplayRecords {
		return errS2LocalRecord
	}
	switch r.kind {
	case s2LocalGenesis:
		if r.scope != expected || r.localIndex != 1 || h.Cut.Sequence != 1 || h.Cut.Previous != [32]byte{} || r.previousCapsuleDigest != [32]byte{} || r.commit != (CommitRef{}) {
			return errS2LocalRecord
		}
	case s2LocalApply:
		c := r.commit
		if r.scope != (s2LocalScope{}) || r.localIndex < 2 || r.previousCapsuleDigest == [32]byte{} || c.Version != S1Version || c.Domain != expected.Domain || c.Cohort != expected.Cohort || c.Membership != expected.Membership || c.Configuration != expected.ConfigDigest || c.Slot != h.Slot || c.Value == [32]byte{} {
			return errS2LocalRecord
		}
	default:
		return errS2LocalRecord
	}
	return nil
}

func s2EncodeLocalRecord(r *s2LocalRecord, expected s2LocalScope) ([]byte, error) {
	if r == nil || !expected.valid() || uint64(len(r.capsule)) > expected.Storage.Capsule.Bytes {
		return nil, errS2LocalRecord
	}
	var header []byte
	switch r.kind {
	case s2LocalGenesis:
		header, _ = json.Marshal(s2LocalGenesisHeader{r.scope, r.localIndex, r.capsuleDigest})
	case s2LocalApply:
		header, _ = json.Marshal(s2LocalApplyHeader{r.scopeDigest, r.localIndex, r.previousCapsuleDigest, r.capsuleDigest, r.commit})
	default:
		return nil, errS2LocalRecord
	}
	n, err := s2LocalPayloadSize(uint64(len(header)), uint64(len(r.capsule)))
	if err != nil {
		return nil, err
	}
	if err := r.validate(expected); err != nil {
		return nil, err
	}
	encoded := make([]byte, 0, int(n))
	encoded = append(encoded, s2LocalMagic...)
	encoded = append(encoded, r.kind)
	encoded = binary.BigEndian.AppendUint32(encoded, uint32(len(header)))
	encoded = append(encoded, header...)
	encoded = binary.BigEndian.AppendUint32(encoded, uint32(len(r.capsule)))
	encoded = append(encoded, r.capsule...)
	return encoded, nil
}

func s2DecodeLocalRecord(encoded []byte, expected s2LocalScope) (*s2LocalRecord, error) {
	// Bound the complete application payload before parsing or allocation. The
	// outer WAL frame's 36-byte sequence/HLC prefix has a separate fixed charge.
	if !expected.valid() || uint64(len(encoded)) > s2LocalMaxPayloadBytes || len(encoded) < len(s2LocalMagic)+9 || !bytes.HasPrefix(encoded, []byte(s2LocalMagic)) {
		return nil, errS2LocalRecord
	}
	kind := encoded[len(s2LocalMagic)]
	if kind != s2LocalGenesis && kind != s2LocalApply {
		return nil, errS2LocalRecord
	}
	reader := s2CapsuleReader{encoded[len(s2LocalMagic)+1:]}
	headerLength := binary.BigEndian.Uint32(reader.remaining)
	if headerLength == 0 || headerLength > s2LocalMaxHeaderBytes {
		return nil, errS2LocalRecord
	}
	header, err := reader.record()
	if err != nil || len(reader.remaining) < 4 {
		return nil, errS2LocalRecord
	}
	capsuleLength := binary.BigEndian.Uint32(reader.remaining)
	if uint64(capsuleLength) > expected.Storage.Capsule.Bytes {
		return nil, errS2LocalRecord
	}
	n, err := s2LocalPayloadSize(uint64(headerLength), uint64(capsuleLength))
	if err != nil || n != uint64(len(encoded)) {
		return nil, errS2LocalRecord
	}
	capsule, err := reader.record()
	if err != nil || len(reader.remaining) != 0 {
		return nil, errS2LocalRecord
	}
	r := &s2LocalRecord{kind: kind}
	switch kind {
	case s2LocalGenesis:
		var h s2LocalGenesisHeader
		if s2StrictJSON(header, &h, expected.Configuration.Policy) != nil || h.Scope != expected {
			return nil, errS2LocalRecord
		}
		r.scope, r.scopeDigest, r.localIndex, r.capsuleDigest = h.Scope, expected.digest(), h.LocalIndex, h.CapsuleDigest
	case s2LocalApply:
		var h s2LocalApplyHeader
		if s2StrictJSON(header, &h, expected.Configuration.Policy) != nil {
			return nil, errS2LocalRecord
		}
		r.scopeDigest, r.localIndex, r.previousCapsuleDigest, r.capsuleDigest, r.commit = h.ScopeDigest, h.LocalIndex, h.PreviousCapsuleDigest, h.CapsuleDigest, h.Commit
	}
	r.capsule = string(capsule)
	if err := r.validate(expected); err != nil {
		return nil, err
	}
	return r, nil
}
