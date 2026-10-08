package security

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"sort"
)

const s2CapsuleMagic = "lantern/security/s2/materialized-capsule\x00\x01"

// This is the existing whole SystemMetadata compatibility ceiling, not a
// production capacity approval. Pending H, votes, evidence, WAL/tips and
// checkpoint overlap need their own later admission and durability budgets.
const s2MaxCapsuleBytes uint64 = 8 << 20

var (
	errS2Capsule       = errors.New("invalid S2 materialized capsule")
	errS2Unpersistable = errors.New("S2 materialized capsule exceeds codec limits")
)

// Every limit is explicit. No process defaults enlarge an existing hard cap.
type s2CapsuleLimits struct {
	Bytes          uint64
	LedgerEntries  uint32
	LineageEntries uint32
}

func (l s2CapsuleLimits) valid() bool {
	return l.Bytes > 0 && l.Bytes <= s2MaxCapsuleBytes && l.LedgerEntries > 0 && l.LedgerEntries <= 100000 && l.LineageEntries > 0 && l.LineageEntries <= 100000
}

// Roots are data when serialized. Only s2TrustedGenesis, supplied separately
// by a future trusted verifier, can give either root authority during restore.
type s2CapsuleRoots struct {
	Genesis, Retirement [32]byte
}

type s2CapsuleFloor struct {
	Through              uint64
	Domain, Cohort, Root [32]byte
}

type s2CapsuleHeader struct {
	Version       uint16
	Genesis       [32]byte
	Configuration S1ExecutionConfig
	ConfigDigest  [32]byte
	Membership    [32]byte
	Cut           SemanticCut
	Slot          uint64
	Prefix        [32]byte
	Floor         s2CapsuleFloor
}

type s2CapsuleLineage struct {
	Identity Identity
	Epoch    uint64
}

type s2CapsuleOperation struct {
	Actor     Identity
	Reviewed  SemanticCut
	Canonical string
	Digest    [32]byte
}

type s2CapsuleOutcome struct {
	ID                  FullChangeID
	Operation           s2CapsuleOperation
	Handoff             [32]byte
	Commit              CommitRef
	Disposition         S1Disposition
	Items               []S1ItemOutcome
	Observed, Resulting SemanticCut
}

// A measure is exactly the framed aggregate's bytes, including JSON escaping
// of retained operation bytes. It is NOT total durable/protocol storage cost.
type s2CapsuleMeasure struct {
	Bytes uint64
}

func s2CheckedAdd(total, n uint64) (uint64, error) {
	if n > math.MaxUint64-total {
		return 0, errS2Unpersistable
	}
	return total + n, nil
}

func s2Header(s *S1ApplyState, roots s2CapsuleRoots) (s2CapsuleHeader, error) {
	if s == nil || s.projection == nil || s.projection.snapshot == nil {
		return s2CapsuleHeader{}, errS2Capsule
	}
	c := s.projection.cut
	h := s2CapsuleHeader{1, roots.Genesis, s.configuration, s.configuration.Digest(), s.membership, c, s.slot, s.prefix,
		s2CapsuleFloor{s.retiredThrough, c.Domain, c.Cohort, roots.Retirement}}
	if !h.valid() || s.configuration.Policy != s.projection.snapshot.limits || len(s.projection.snapshot.image) > int(s.configuration.Capacity.ImageBytes) || c.Projection != s.projection.projectionDigest() {
		return s2CapsuleHeader{}, errS2Capsule
	}
	return h, nil
}

func (h s2CapsuleHeader) valid() bool {
	return h.Version == 1 && h.Genesis != [32]byte{} && h.Configuration.valid() && h.ConfigDigest == h.Configuration.Digest() && h.Membership != [32]byte{} && h.Cut.valid() && h.Cut.Policy == s1PolicyConfiguration(h.Configuration.Policy) && h.Prefix != [32]byte{} && h.Floor.Domain == h.Cut.Domain && h.Floor.Cohort == h.Cut.Cohort && (h.Floor.Through == 0 && h.Floor.Root == [32]byte{} || h.Floor.Through != 0 && h.Floor.Root != [32]byte{})
}

func s2Outcome(o *OriginalOutcome) s2CapsuleOutcome {
	return s2CapsuleOutcome{o.id, s2CapsuleOperation{o.operation.actor, o.operation.reviewed, o.operation.canonical, o.operation.digest}, o.handoff, o.commit, o.disposition, o.items, o.observed, o.resulting}
}

func s2SameScope(a, b SemanticCut) bool {
	return a.valid() && a.Version == b.Version && a.Domain == b.Domain && a.Cohort == b.Cohort && a.Generation == b.Generation && a.Fences == b.Fences && a.Policy == b.Policy
}

func s2ValidateOutcome(o s2CapsuleOutcome, h s2CapsuleHeader) error {
	if !o.ID.valid() || o.ID.Domain != h.Cut.Domain || o.ID.Cohort != h.Cut.Cohort || o.ID.Namespace <= h.Floor.Through || o.Observed != o.Operation.Reviewed || !o.Observed.valid() || o.Observed.Domain != h.Cut.Domain || o.Observed.Cohort != h.Cut.Cohort || !s2SameScope(o.Resulting, h.Cut) || o.Resulting.Sequence > h.Cut.Sequence || o.Handoff == [32]byte{} || o.Commit.Version != S1Version || o.Commit.Domain != h.Cut.Domain || o.Commit.Cohort != h.Cut.Cohort || o.Commit.Membership != h.Membership || o.Commit.Configuration != h.ConfigDigest || o.Commit.Slot == 0 || o.Commit.Slot > h.Slot || o.Commit.Value != o.Handoff {
		return errS2Capsule
	}
	command, err := s2DecodeOperation(o.Operation)
	if err != nil {
		return err
	}
	switch o.Disposition {
	case S1Applied, S1RejectedCAS, S1RejectedAdmin, S1RejectedAuthority, S1RejectedPurpose, S1RejectedCapacity, S1RejectedInvariant:
	default:
		return errS2Capsule
	}
	// A terminal CAS result can retain a different original generation, fence,
	// policy or sequence. Never rewrite that review to the current configuration.
	if o.Disposition == S1Applied && (!s2SameScope(o.Observed, h.Cut) || o.Observed.Sequence >= h.Cut.Sequence || validateS1Command(command, h.Configuration.Policy) != nil) {
		return errS2Capsule
	}
	n := 1
	if command.Kind == S1Management {
		n = len(command.Changes)
	}
	if len(o.Items) != n {
		return errS2Capsule
	}
	for i, item := range o.Items {
		kind := command.Kind
		if command.Kind == S1Management {
			kind = command.Changes[i].Kind
		}
		if item.Index != i || item.Kind != kind || item.Disposition != o.Disposition {
			return errS2Capsule
		}
	}
	return nil
}

func s2LineageLess(a, b Identity) bool {
	if a.Issuer != b.Issuer {
		return a.Issuer < b.Issuer
	}
	return a.Subject < b.Subject
}

func s2IDLess(a, b FullChangeID) bool {
	if a.Version != b.Version {
		return a.Version < b.Version
	}
	if a.Domain != b.Domain {
		return bytes.Compare(a.Domain[:], b.Domain[:]) < 0
	}
	if a.Cohort != b.Cohort {
		return bytes.Compare(a.Cohort[:], b.Cohort[:]) < 0
	}
	if a.Namespace != b.Namespace {
		return a.Namespace < b.Namespace
	}
	return bytes.Compare(a.Nonce[:], b.Nonce[:]) < 0
}

// Measure components without building or allocating the whole encoding. Map
// iteration order cannot affect the sum; only Encode needs sorted keys.
func s2MeasureCapsule(s *S1ApplyState, roots s2CapsuleRoots, limits s2CapsuleLimits) (s2CapsuleMeasure, error) {
	if !limits.valid() {
		return s2CapsuleMeasure{}, errS2Capsule
	}
	if s == nil || s.projection == nil || s.projection.snapshot == nil {
		return s2CapsuleMeasure{}, errS2Capsule
	}
	// Bound keyed storage before projectionDigest allocates sorted rows.
	if uint64(len(s.ledger)) > uint64(limits.LedgerEntries) || uint64(len(s.projection.lineage)) > uint64(limits.LineageEntries) {
		return s2CapsuleMeasure{}, errS2Unpersistable
	}
	h, err := s2Header(s, roots)
	if err != nil {
		return s2CapsuleMeasure{}, err
	}
	if uint64(len(s.ledger)) > uint64(h.Configuration.Capacity.LedgerEntries) {
		return s2CapsuleMeasure{}, errS2Unpersistable
	}
	// Compile the entire closure again; legacy DecodeImage's admission fallback
	// is intentionally unavailable to S2.
	snapshot, err := CompileImage(s.projection.Image(), h.Configuration.Policy)
	if err != nil || !bytes.Equal(snapshot.image, s.projection.snapshot.image) {
		return s2CapsuleMeasure{}, errS2Capsule
	}
	header, _ := json.Marshal(h)
	total := uint64(len(s2CapsuleMagic))
	add := func(n uint64) error {
		var e error
		total, e = s2CheckedAdd(total, n)
		return e
	}
	if err := add(16 + uint64(len(header)) + uint64(len(snapshot.image))); err != nil {
		return s2CapsuleMeasure{}, err
	}
	for id, epoch := range s.projection.lineage {
		if !id.valid() || id.Kind != OIDCPrincipal || epoch == 0 {
			return s2CapsuleMeasure{}, errS2Capsule
		}
		row, _ := json.Marshal(s2CapsuleLineage{id, epoch})
		if err := add(4 + uint64(len(row))); err != nil {
			return s2CapsuleMeasure{}, err
		}
	}
	for id := range snapshot.principals {
		if id.Kind == OIDCPrincipal && s.projection.lineage[id] == 0 {
			return s2CapsuleMeasure{}, errS2Capsule
		}
	}
	for id, original := range s.ledger {
		if original == nil || original.id != id {
			return s2CapsuleMeasure{}, errS2Capsule
		}
		o := s2Outcome(original)
		if err := s2ValidateOutcome(o, h); err != nil {
			return s2CapsuleMeasure{}, err
		}
		row, err := json.Marshal(o)
		if err != nil {
			return s2CapsuleMeasure{}, errS2Capsule
		}
		if err := add(4 + uint64(len(row))); err != nil {
			return s2CapsuleMeasure{}, err
		}
	}
	m := s2CapsuleMeasure{total}
	if total > limits.Bytes {
		return m, errS2Unpersistable
	}
	return m, nil
}

func s2EncodeCapsule(s *S1ApplyState, roots s2CapsuleRoots, limits s2CapsuleLimits) ([]byte, s2CapsuleMeasure, error) {
	m, err := s2MeasureCapsule(s, roots, limits)
	if err != nil {
		return nil, m, err
	}
	h, _ := s2Header(s, roots)
	encoded := make([]byte, 0, int(m.Bytes)) // Only after checked full measurement.
	encoded = append(encoded, s2CapsuleMagic...)
	word := func(n uint32) { encoded = binary.BigEndian.AppendUint32(encoded, n) }
	record := func(v any) { row, _ := json.Marshal(v); word(uint32(len(row))); encoded = append(encoded, row...) }
	record(h)
	word(uint32(len(s.projection.snapshot.image)))
	encoded = append(encoded, s.projection.snapshot.image...)
	lineage := make([]Identity, 0, len(s.projection.lineage))
	for id := range s.projection.lineage {
		lineage = append(lineage, id)
	}
	sort.Slice(lineage, func(i, j int) bool { return s2LineageLess(lineage[i], lineage[j]) })
	word(uint32(len(lineage)))
	for _, id := range lineage {
		record(s2CapsuleLineage{id, s.projection.lineage[id]})
	}
	ids := make([]FullChangeID, 0, len(s.ledger))
	for id := range s.ledger {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return s2IDLess(ids[i], ids[j]) })
	word(uint32(len(ids)))
	for _, id := range ids {
		record(s2Outcome(s.ledger[id]))
	}
	if uint64(len(encoded)) != m.Bytes {
		return nil, s2CapsuleMeasure{}, errS2Capsule
	}
	return encoded, m, nil
}
