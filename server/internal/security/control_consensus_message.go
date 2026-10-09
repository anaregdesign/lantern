package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"sort"
)

const (
	s2cPrepare byte = iota + 1
	s2cPromise
	s2cAccept
	s2cAccepted
	s2cChosen
)

const (
	s2cMessageMagic      = "lantern/security/s2c/message\x00\x01"
	s2cProofMagic        = "lantern/security/s2c/proof\x00\x01"
	s2cSignedHeaderBytes = 261
	s2cSummaryBytes      = s2cSignedHeaderBytes + ed25519.SignatureSize
	s2cMessageOverhead   = len(s2cMessageMagic) + s2cSummaryBytes + 8
	s2cHardProofBytes    = 64 << 10
	s2cHardPayloadBytes  = (32 << 20) - 36
)

var errS2CMessage = errors.New("invalid S2-C authenticated protocol message")

// Ballot order is lexicographic. A zero ballot denotes no accepted value only;
// it is never a protocol request, and overflow cannot silently wrap its counter.
type s2cBallot struct {
	Counter uint64
	Member  uint32
}

func (b s2cBallot) valid() bool { return b.Counter != 0 && b.Member != 0 }
func (b s2cBallot) compare(other s2cBallot) int {
	if b.Counter < other.Counter || b.Counter == other.Counter && b.Member < other.Member {
		return -1
	}
	if b == other {
		return 0
	}
	return 1
}

// Messages own immutable wire strings. These fields are package-private protocol
// data, never a caller-supplied certificate. Constructors and verifiers re-read
// authenticated bytes before using messages supplied by another component.
type s2cMessage struct {
	kind                             byte
	scope                            [32]byte
	sender                           uint32
	slot                             uint64
	predecessor                      [32]byte
	ballot                           s2cBallot
	value, admission                 [32]byte
	acceptedBallot                   s2cBallot
	acceptedValue, acceptedAdmission [32]byte
	proofDigest                      [32]byte
	signature                        [ed25519.SignatureSize]byte
	proof                            string
	h                                *s2cHistoricalH
	raw                              string
}

func s2cProofDigest(proof string) [32]byte {
	if proof == "" {
		return [32]byte{}
	}
	h := sha256.New()
	_, _ = h.Write([]byte("lantern/security/s2c/proof-digest\x00\x01"))
	_, _ = h.Write([]byte(proof))
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func s2cHeader(m *s2cMessage) []byte {
	b := make([]byte, 0, s2cSignedHeaderBytes)
	b = append(b, m.kind)
	b = append(b, m.scope[:]...)
	b = binary.BigEndian.AppendUint32(b, m.sender)
	b = binary.BigEndian.AppendUint64(b, m.slot)
	b = append(b, m.predecessor[:]...)
	b = binary.BigEndian.AppendUint64(b, m.ballot.Counter)
	b = binary.BigEndian.AppendUint32(b, m.ballot.Member)
	b = append(b, m.value[:]...)
	b = append(b, m.admission[:]...)
	b = binary.BigEndian.AppendUint64(b, m.acceptedBallot.Counter)
	b = binary.BigEndian.AppendUint32(b, m.acceptedBallot.Member)
	b = append(b, m.acceptedValue[:]...)
	b = append(b, m.acceptedAdmission[:]...)
	return append(b, m.proofDigest[:]...)
}

func s2cSigningBytes(m *s2cMessage) []byte {
	return append([]byte(s2cMessageMagic), s2cHeader(m)...)
}

func s2cReadSummary(encoded []byte) (*s2cMessage, error) {
	if len(encoded) != s2cSummaryBytes {
		return nil, errS2CMessage
	}
	m := &s2cMessage{}
	i := 0
	m.kind = encoded[i]
	i++
	copy(m.scope[:], encoded[i:i+32])
	i += 32
	m.sender = binary.BigEndian.Uint32(encoded[i : i+4])
	i += 4
	m.slot = binary.BigEndian.Uint64(encoded[i : i+8])
	i += 8
	copy(m.predecessor[:], encoded[i:i+32])
	i += 32
	m.ballot.Counter = binary.BigEndian.Uint64(encoded[i : i+8])
	i += 8
	m.ballot.Member = binary.BigEndian.Uint32(encoded[i : i+4])
	i += 4
	copy(m.value[:], encoded[i:i+32])
	i += 32
	copy(m.admission[:], encoded[i:i+32])
	i += 32
	m.acceptedBallot.Counter = binary.BigEndian.Uint64(encoded[i : i+8])
	i += 8
	m.acceptedBallot.Member = binary.BigEndian.Uint32(encoded[i : i+4])
	i += 4
	copy(m.acceptedValue[:], encoded[i:i+32])
	i += 32
	copy(m.acceptedAdmission[:], encoded[i:i+32])
	i += 32
	copy(m.proofDigest[:], encoded[i:i+32])
	i += 32
	copy(m.signature[:], encoded[i:])
	return m, nil
}

func s2cValidBallot(t *s2cTrust, b s2cBallot) bool {
	if t == nil || !b.valid() {
		return false
	}
	member, ok := t.member(b.Member)
	return ok && member.Proposer
}

// Summary validation deliberately does not require the full historical value:
// a proof contains fixed signed assertions, followed by its selected H once.
func s2cVerifySummary(t *s2cTrust, m *s2cMessage) error {
	if t == nil || m == nil || m.scope != t.scope || m.slot == 0 || m.slot > math.MaxUint64-2 || m.predecessor == [32]byte{} || !s2cValidBallot(t, m.ballot) {
		return errS2CMessage
	}
	if m.kind < s2cPrepare || m.kind > s2cChosen {
		return errS2CMessage
	}
	if m.kind == s2cChosen {
		if m.sender != 0 || m.signature != [ed25519.SignatureSize]byte{} {
			return errS2CMessage
		}
	} else {
		member, ok := t.member(m.sender)
		if !ok || !ed25519.Verify(ed25519.PublicKey(member.PublicKey[:]), s2cSigningBytes(m), m.signature[:]) {
			return errS2CMessage
		}
		if (m.kind == s2cPrepare || m.kind == s2cAccept) && (m.sender != m.ballot.Member || !member.Proposer) {
			return errS2CMessage
		}
	}
	if m.kind != s2cPromise && (m.acceptedBallot != (s2cBallot{}) || m.acceptedValue != [32]byte{} || m.acceptedAdmission != [32]byte{}) {
		return errS2CMessage
	}
	switch m.kind {
	case s2cPrepare:
		if m.value != [32]byte{} || m.admission != [32]byte{} || m.proofDigest != [32]byte{} {
			return errS2CMessage
		}
	case s2cPromise:
		if m.value != [32]byte{} || m.admission != [32]byte{} || m.proofDigest != [32]byte{} {
			return errS2CMessage
		}
		if m.acceptedBallot == (s2cBallot{}) {
			if m.acceptedValue != [32]byte{} || m.acceptedAdmission != [32]byte{} {
				return errS2CMessage
			}
		} else if !s2cValidBallot(t, m.acceptedBallot) || m.acceptedBallot.compare(m.ballot) > 0 || m.acceptedValue == [32]byte{} || m.acceptedAdmission == [32]byte{} {
			return errS2CMessage
		}
	case s2cAccept, s2cChosen:
		if m.value == [32]byte{} || m.admission == [32]byte{} || m.proofDigest == [32]byte{} {
			return errS2CMessage
		}
	case s2cAccepted:
		if m.value == [32]byte{} || m.admission == [32]byte{} || m.proofDigest != [32]byte{} {
			return errS2CMessage
		}
	}
	return nil
}

func s2cSameAttempt(a, b *s2cMessage) bool {
	return a.scope == b.scope && a.slot == b.slot && a.predecessor == b.predecessor && a.ballot == b.ballot
}

func s2cDecodeProof(t *s2cTrust, proof string, kind byte, request *s2cMessage) ([]*s2cMessage, error) {
	if t == nil || uint64(len(proof)) > t.bounds.ProofBytes || len(proof) > s2cHardProofBytes || len(proof) < len(s2cProofMagic)+1 || proof[:len(s2cProofMagic)] != s2cProofMagic {
		return nil, errS2CMessage
	}
	n := int(proof[len(s2cProofMagic)])
	if n < t.majority() || n > 31 || len(proof) != len(s2cProofMagic)+1+n*s2cSummaryBytes {
		return nil, errS2CMessage
	}
	rows := make([]*s2cMessage, 0, n)
	var previous uint32
	for i := 0; i < n; i++ {
		start := len(s2cProofMagic) + 1 + i*s2cSummaryBytes
		m, err := s2cReadSummary([]byte(proof[start : start+s2cSummaryBytes]))
		if err != nil || m.kind != kind || m.sender <= previous || s2cVerifySummary(t, m) != nil || !s2cSameAttempt(m, request) {
			return nil, errS2CMessage
		}
		previous = m.sender
		rows = append(rows, m)
	}
	return rows, nil
}

func s2cEncodeProof(t *s2cTrust, rows []*s2cMessage) (string, error) {
	if t == nil || len(rows) < t.majority() || len(rows) > 31 {
		return "", errS2CMessage
	}
	n := len(s2cProofMagic) + 1 + len(rows)*s2cSummaryBytes
	if uint64(n) > t.bounds.ProofBytes || n > s2cHardProofBytes {
		return "", errS2CMessage
	}
	ordered := append([]*s2cMessage(nil), rows...)
	for _, m := range ordered {
		if m == nil {
			return "", errS2CMessage
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].sender < ordered[j].sender })
	b := make([]byte, 0, n)
	b = append(b, s2cProofMagic...)
	b = append(b, byte(len(ordered)))
	var previous uint32
	for _, m := range ordered {
		if m.sender <= previous {
			return "", errS2CMessage
		}
		previous = m.sender
		b = append(b, s2cHeader(m)...)
		b = append(b, m.signature[:]...)
	}
	return string(b), nil
}

// Equal maximum accepted ballots with different values are corruption. Their
// logical values cannot be resolved by sorting hashes or choosing a newer H.
func s2cHighestAccepted(rows []*s2cMessage) (s2cBallot, [32]byte, error) {
	var highest s2cBallot
	var value [32]byte
	var admission [32]byte
	for _, m := range rows {
		if m.acceptedBallot == (s2cBallot{}) {
			continue
		}
		cmp := m.acceptedBallot.compare(highest)
		if cmp > 0 {
			highest, value = m.acceptedBallot, m.acceptedValue
			admission = m.acceptedAdmission
		}
		if cmp == 0 && (value != m.acceptedValue || admission != m.acceptedAdmission) {
			return s2cBallot{}, [32]byte{}, errS2CMessage
		}
	}
	return highest, value, nil
}

func s2cVerifyP1Body(t *s2cTrust, m *s2cMessage) error {
	if m == nil || m.kind != s2cAccept || m.proofDigest != s2cProofDigest(m.proof) {
		return errS2CMessage
	}
	rows, err := s2cDecodeProof(t, m.proof, s2cPromise, m)
	if err != nil {
		return err
	}
	highest, value, err := s2cHighestAccepted(rows)
	if err != nil || highest.valid() && m.value != value {
		return errS2CMessage
	}
	return nil
}

func s2cVerifyP1(t *s2cTrust, m *s2cMessage) error {
	_, err := s2cTrustedMessage(t, m, s2cAccept)
	return err
}

func s2cVerifyQC(t *s2cTrust, m *s2cMessage) error {
	if m == nil || m.kind != s2cChosen || m.proofDigest != s2cProofDigest(m.proof) {
		return errS2CMessage
	}
	rows, err := s2cDecodeProof(t, m.proof, s2cAccepted, m)
	if err != nil {
		return err
	}
	for _, vote := range rows {
		if vote.value != m.value || vote.admission != m.admission {
			return errS2CMessage
		}
	}
	return nil
}

func s2cDecodeMessage(t *s2cTrust, encoded []byte) (*s2cMessage, error) {
	if t == nil || uint64(len(encoded)) > t.bounds.PayloadBytes || len(encoded) > s2cHardPayloadBytes || uint64(s2cMessageOverhead) > t.bounds.HeaderBytes || s2cMessageOverhead > s2cHardProofBytes || len(encoded) < s2cMessageOverhead || !bytes.Equal(encoded[:len(s2cMessageMagic)], []byte(s2cMessageMagic)) {
		return nil, errS2CMessage
	}
	start := len(s2cMessageMagic)
	m, err := s2cReadSummary(encoded[start : start+s2cSummaryBytes])
	if err != nil || s2cVerifySummary(t, m) != nil {
		return nil, errS2CMessage
	}
	start += s2cSummaryBytes
	proofLen := uint64(binary.BigEndian.Uint32(encoded[start : start+4]))
	hLen := uint64(binary.BigEndian.Uint32(encoded[start+4 : start+8]))
	start += 8
	if proofLen > t.bounds.ProofBytes || proofLen > s2cHardProofBytes || hLen > t.bounds.HistoricalBytes || hLen > 8<<20 || proofLen+hLen != uint64(len(encoded)-start) {
		return nil, errS2CMessage
	}
	m.proof = string(encoded[start : start+int(proofLen)])
	if m.proofDigest != s2cProofDigest(m.proof) {
		return nil, errS2CMessage
	}
	if hLen != 0 {
		m.h, err = verifyHistoricalH(t, encoded[start+int(proofLen):])
		if err != nil {
			return nil, errS2CMessage
		}
	}
	value := t.noopValue()
	if m.h != nil {
		value = m.h.digest()
	}
	switch m.kind {
	case s2cPrepare, s2cAccepted:
		if m.h != nil || m.proof != "" {
			return nil, errS2CMessage
		}
	case s2cPromise:
		if m.proof != "" || m.acceptedBallot == (s2cBallot{}) && m.h != nil || m.acceptedBallot.valid() && m.acceptedValue != value {
			return nil, errS2CMessage
		}
	case s2cAccept:
		if m.value != value || s2cVerifyP1Body(t, m) != nil {
			return nil, errS2CMessage
		}
	case s2cChosen:
		if m.value != value || s2cVerifyQC(t, m) != nil {
			return nil, errS2CMessage
		}
	}
	m.raw = string(encoded)
	return m, nil
}

func s2cSignMessage(t *s2cTrust, key ed25519.PrivateKey, m s2cMessage) (*s2cMessage, error) {
	if t == nil {
		return nil, errS2CMessage
	}
	m.scope = t.scope
	m.proofDigest = s2cProofDigest(m.proof)
	if m.kind != s2cChosen {
		member, ok := t.member(m.sender)
		if !ok || len(key) != ed25519.PrivateKeySize || !bytes.Equal(key[ed25519.SeedSize:], member.PublicKey[:]) {
			return nil, errS2CMessage
		}
		copy(m.signature[:], ed25519.Sign(key, s2cSigningBytes(&m)))
	} else if m.sender != 0 {
		return nil, errS2CMessage
	}
	var h string
	if m.h != nil {
		h = m.h.raw
	}
	total := uint64(s2cMessageOverhead) + uint64(len(m.proof)) + uint64(len(h))
	if uint64(len(m.proof)) > t.bounds.ProofBytes || len(m.proof) > s2cHardProofBytes || uint64(len(h)) > t.bounds.HistoricalBytes || len(h) > 8<<20 || total > t.bounds.PayloadBytes || total > s2cHardPayloadBytes || uint64(s2cMessageOverhead) > t.bounds.HeaderBytes {
		return nil, errS2CMessage
	}
	b := make([]byte, 0, int(total))
	b = append(b, s2cMessageMagic...)
	b = append(b, s2cHeader(&m)...)
	b = append(b, m.signature[:]...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(m.proof)))
	b = binary.BigEndian.AppendUint32(b, uint32(len(h)))
	b = append(b, m.proof...)
	b = append(b, h...)
	return s2cDecodeMessage(t, b)
}

func s2cSignPrepare(t *s2cTrust, key ed25519.PrivateKey, sender uint32, slot uint64, pred [32]byte, ballot s2cBallot) (*s2cMessage, error) {
	return s2cSignMessage(t, key, s2cMessage{kind: s2cPrepare, sender: sender, slot: slot, predecessor: pred, ballot: ballot})
}

func s2cTrustedMessage(t *s2cTrust, m *s2cMessage, kind byte) (*s2cMessage, error) {
	if m == nil {
		return nil, errS2CMessage
	}
	verified, err := s2cDecodeMessage(t, []byte(m.raw))
	if err != nil || verified.kind != kind {
		return nil, errS2CMessage
	}
	return verified, nil
}

func s2cSignPromise(t *s2cTrust, key ed25519.PrivateKey, sender uint32, prepare *s2cMessage, acceptedBallot s2cBallot, acceptedH *s2cHistoricalH, acceptedAdmission [32]byte) (*s2cMessage, error) {
	if prepare == nil {
		return nil, errS2CMessage
	}
	request, err := s2cDecodeMessage(t, []byte(prepare.raw))
	if err != nil || request.kind != s2cPrepare && request.kind != s2cAccept {
		return nil, errS2CMessage
	}
	m := s2cMessage{kind: s2cPromise, sender: sender, slot: request.slot, predecessor: request.predecessor, ballot: request.ballot, acceptedBallot: acceptedBallot, acceptedAdmission: acceptedAdmission, h: acceptedH}
	if acceptedBallot.valid() {
		m.acceptedValue = t.noopValue()
		if acceptedH != nil {
			m.acceptedValue = acceptedH.digest()
		}
	}
	return s2cSignMessage(t, key, m)
}

// The only choice without a carried accepted value is the caller's exact pending
// historical H, or canonical NOOP. A winning missing H is never replaced by NOOP.
func s2cSelectP1(t *s2cTrust, promises []*s2cMessage, candidate *s2cHistoricalH) (string, *s2cHistoricalH, error) {
	if t == nil || len(promises) < t.majority() || len(promises) > 31 {
		return "", nil, errS2CMessage
	}
	rows := make([]*s2cMessage, 0, len(promises))
	for _, p := range promises {
		m, err := s2cTrustedMessage(t, p, s2cPromise)
		if err != nil || len(rows) != 0 && !s2cSameAttempt(rows[0], m) {
			return "", nil, errS2CMessage
		}
		rows = append(rows, m)
	}
	proof, err := s2cEncodeProof(t, rows)
	if err != nil {
		return "", nil, err
	}
	highest, value, err := s2cHighestAccepted(rows)
	if err != nil {
		return "", nil, err
	}
	if !highest.valid() {
		if candidate == nil {
			return proof, nil, nil
		}
		h, err := verifyHistoricalH(t, []byte(candidate.raw))
		if err != nil {
			return "", nil, errS2CMessage
		}
		return proof, h, nil
	}
	if value == t.noopValue() {
		return proof, nil, nil
	}
	for _, m := range rows {
		if m.acceptedBallot == highest && m.h != nil && m.h.digest() == value {
			return proof, m.h, nil
		}
	}
	return "", nil, errS2CMessage
}

func s2cSignAccept(t *s2cTrust, key ed25519.PrivateKey, sender uint32, prepare *s2cMessage, promises []*s2cMessage, candidate *s2cHistoricalH, admission [32]byte) (*s2cMessage, error) {
	request, err := s2cTrustedMessage(t, prepare, s2cPrepare)
	if err != nil {
		return nil, err
	}
	proof, h, err := s2cSelectP1(t, promises, candidate)
	if err != nil {
		return nil, err
	}
	value := t.noopValue()
	if h != nil {
		value = h.digest()
	}
	return s2cSignMessage(t, key, s2cMessage{kind: s2cAccept, sender: sender, slot: request.slot, predecessor: request.predecessor, ballot: request.ballot, value: value, admission: admission, proof: proof, h: h})
}

func s2cSignAccepted(t *s2cTrust, key ed25519.PrivateKey, sender uint32, accept *s2cMessage, admission [32]byte) (*s2cMessage, error) {
	request, err := s2cTrustedMessage(t, accept, s2cAccept)
	if err != nil || admission != request.admission {
		return nil, errS2CMessage
	}
	return s2cSignMessage(t, key, s2cMessage{kind: s2cAccepted, sender: sender, slot: request.slot, predecessor: request.predecessor, ballot: request.ballot, value: request.value, admission: admission})
}

func s2cBuildQC(t *s2cTrust, votes []*s2cMessage) (string, error) {
	if t == nil || len(votes) < t.majority() || len(votes) > 31 {
		return "", errS2CMessage
	}
	rows := make([]*s2cMessage, 0, len(votes))
	for _, v := range votes {
		m, err := s2cTrustedMessage(t, v, s2cAccepted)
		if err != nil || len(rows) != 0 && (!s2cSameAttempt(rows[0], m) || rows[0].value != m.value || rows[0].admission != m.admission) {
			return "", errS2CMessage
		}
		rows = append(rows, m)
	}
	return s2cEncodeProof(t, rows)
}

// Chosen is a proof envelope: the matching majority signs every logical field.
// It needs no additional leader signature, and can be reconstructed after a
// lost QC from retained votes without acquiring a proposer's signing key.
func s2cMakeChosen(t *s2cTrust, accept *s2cMessage, votes []*s2cMessage) (*s2cMessage, error) {
	request, err := s2cTrustedMessage(t, accept, s2cAccept)
	if err != nil {
		return nil, err
	}
	proof, err := s2cBuildQC(t, votes)
	if err != nil {
		return nil, err
	}
	return s2cSignMessage(t, nil, s2cMessage{kind: s2cChosen, slot: request.slot, predecessor: request.predecessor, ballot: request.ballot, value: request.value, admission: request.admission, proof: proof, h: request.h})
}

func s2cVerifyChosen(t *s2cTrust, chosen *s2cMessage) (S1CertifiedNext, error) {
	m, err := s2cTrustedMessage(t, chosen, s2cChosen)
	if err != nil || t.genesis == nil || t.genesis.state == nil || t.genesis.state.projection == nil {
		return S1CertifiedNext{}, errS2CMessage
	}
	commit := s2cLogicalCommit(t, m.slot, m.value)
	var h *S1Handoff
	if m.h != nil {
		h = m.h.handoff
	}
	return S1CertifiedNext{certificate: &s1PrefixCertificate{commit: commit, previous: m.predecessor, witness: m.proofDigest}, handoff: h}, nil
}

// A logical position/value identifies a preview; it does not confer Apply
// authority. Only verified matching Accepted signatures produce that authority.
func s2cLogicalCommit(t *s2cTrust, slot uint64, value [32]byte) CommitRef {
	if t == nil || t.genesis == nil || t.genesis.state == nil || t.genesis.state.projection == nil || slot == 0 || slot > math.MaxUint64-2 || value == [32]byte{} {
		return CommitRef{}
	}
	g := t.genesis.state
	return CommitRef{Version: S1Version, Domain: g.projection.cut.Domain, Cohort: g.projection.cut.Cohort, Membership: g.membership, Configuration: g.configuration.Digest(), Slot: slot, Value: value}
}

// Reserve the largest possible witness, not the currently collected majority.
// Every term is bounded before conversion; accepted completion can therefore
// survive a replacement quorum without increasing this encoded-message charge.
func s2cChosenBytesBound(t *s2cTrust, h *s2cHistoricalH) uint64 {
	var n uint64
	if h != nil {
		n = uint64(len(h.raw))
	}
	return s2cChosenSizeBound(t, n)
}

func s2cChosenSizeBound(t *s2cTrust, historicalBytes uint64) uint64 {
	if t == nil || len(t.members) < 3 || len(t.members) > 31 || uint64(s2cMessageOverhead) > t.bounds.HeaderBytes {
		return 0
	}
	base := uint64(s2cMessageOverhead)
	if historicalBytes > t.bounds.HistoricalBytes || historicalBytes > 8<<20 {
		return 0
	}
	base += historicalBytes
	payload := min(t.bounds.PayloadBytes, uint64(s2cHardPayloadBytes))
	if base > payload {
		return 0
	}
	proof := min(t.bounds.ProofBytes, uint64(s2cHardProofBytes), payload-base)
	framing := uint64(len(s2cProofMagic) + 1)
	if proof < framing {
		return 0
	}
	count := min(uint64(len(t.members)), (proof-framing)/uint64(s2cSummaryBytes))
	if count < uint64(t.majority()) {
		return 0
	}
	return base + framing + count*uint64(s2cSummaryBytes)
}
