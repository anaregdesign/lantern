package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
)

const (
	authorityRenewalRequestDomain        = "lantern/security/current-renewal/request\x00\x01"
	authorityRenewalVoteDomain           = "lantern/security/current-renewal/vote\x00\x01"
	authorityRenewalMaxStatement         = 8192
	authorityRenewalLifetime      uint64 = 15_000_000_000
)

var errAuthorityRenewal = errors.New("current authority renewal unavailable")

// One common statement; it contains no local WAL path, B identity or quota.
// Workloads binds the existing complete independently configured mTLS mapping.
type authorityRenewalStatement struct {
	Version                                uint16
	Scope, Members, Workloads, TimeProfile [32]byte
	Receiver                               uint32
	Boot                                   [16]byte
	Challenge                              [32]byte
	Slot                                   uint64
	Prefix, Capsule                        [32]byte
	Cut                                    SemanticCut
	Lifetime                               uint64
}

type authorityRenewalRequest struct {
	statement authorityRenewalStatement
	canonical string
	signature [64]byte
}

type authorityRenewalVote struct {
	member    uint32
	signature [64]byte
}

func authorityRenewalCanonical(s authorityRenewalStatement, trust *s2cTrust, workloads [32]byte) ([]byte, error) {
	if trust == nil || s.Version != 1 || s.Scope != trust.scope || s.Members != trust.memberSet || workloads == [32]byte{} || s.Workloads != workloads ||
		s.TimeProfile != sha256.Sum256([]byte(authorityTimeProfileDescription)) || s.Boot == [16]byte{} || s.Challenge == [32]byte{} || s.Prefix == [32]byte{} || s.Capsule == [32]byte{} ||
		!s.Cut.valid() || !s2SameScope(s.Cut, trust.genesis.state.projection.cut) || s.Lifetime != authorityRenewalLifetime {
		return nil, errAuthorityRenewal
	}
	if _, ok := trust.member(s.Receiver); !ok {
		return nil, errAuthorityRenewal
	}
	raw, err := json.Marshal(s)
	if err != nil || len(raw) > authorityRenewalMaxStatement {
		return nil, errAuthorityRenewal
	}
	return raw, nil
}

func signAuthorityRenewalRequest(s authorityRenewalStatement, trust *s2cTrust, workloads [32]byte, key ed25519.PrivateKey) ([]byte, error) {
	raw, err := authorityRenewalCanonical(s, trust, workloads)
	if err != nil {
		return nil, err
	}
	m, _ := trust.member(s.Receiver)
	if len(key) != ed25519.PrivateKeySize || !bytes.Equal(key.Public().(ed25519.PublicKey), m.PublicKey[:]) {
		return nil, errAuthorityRenewal
	}
	payload := append([]byte(authorityRenewalRequestDomain), raw...)
	return append(payload, ed25519.Sign(key, payload)...), nil
}

func parseAuthorityRenewalRequest(raw []byte, trust *s2cTrust, workloads [32]byte) (authorityRenewalRequest, error) {
	var result authorityRenewalRequest
	if len(raw) <= len(authorityRenewalRequestDomain)+64 || len(raw) > len(authorityRenewalRequestDomain)+authorityRenewalMaxStatement+64 || !bytes.HasPrefix(raw, []byte(authorityRenewalRequestDomain)) {
		return result, errAuthorityRenewal
	}
	unsigned := raw[:len(raw)-64]
	body := unsigned[len(authorityRenewalRequestDomain):]
	if json.Unmarshal(body, &result.statement) != nil {
		return result, errAuthorityRenewal
	}
	canonical, err := authorityRenewalCanonical(result.statement, trust, workloads)
	if err != nil || !bytes.Equal(canonical, body) {
		return result, errAuthorityRenewal
	}
	m, _ := trust.member(result.statement.Receiver)
	if !ed25519.Verify(m.PublicKey[:], unsigned, raw[len(raw)-64:]) {
		return result, errAuthorityRenewal
	}
	result.canonical = string(canonical)
	copy(result.signature[:], raw[len(raw)-64:])
	return result, nil
}

func authorityRenewalVoteBytes(canonical string, member uint32) []byte {
	b := append([]byte(authorityRenewalVoteDomain), []byte(canonical)...)
	return binary.BigEndian.AppendUint32(b, member)
}

func parseAuthorityRenewalVote(raw []byte, request authorityRenewalRequest, trust *s2cTrust) (authorityRenewalVote, error) {
	var vote authorityRenewalVote
	if len(raw) != 68 || trust == nil || request.canonical == "" {
		return vote, errAuthorityRenewal
	}
	vote.member = binary.BigEndian.Uint32(raw[:4])
	copy(vote.signature[:], raw[4:])
	m, ok := trust.member(vote.member)
	if !ok || !ed25519.Verify(m.PublicKey[:], authorityRenewalVoteBytes(request.canonical, vote.member), vote.signature[:]) {
		return authorityRenewalVote{}, errAuthorityRenewal
	}
	return vote, nil
}

// Existing participant gate serializes the observation and signature against
// ACCEPT, CHOSEN and DRAINED. Eligibility is the private mTLS/current-time
// owner's check, invoked inside that same gate, never a caller trust flag.
func (o *s2cParticipant) signAuthorityRenewal(raw []byte, workloads [32]byte, eligible func() error) ([]byte, error) {
	o.gate.Lock()
	defer o.gate.Unlock()
	defer o.poisonPanic()
	if o.readyLocked() != nil || o.chosen != nil || eligible == nil {
		return nil, errAuthorityRenewal
	}
	r, err := parseAuthorityRenewalRequest(raw, o.trust, workloads)
	if err != nil {
		return nil, err
	}
	state, receipt, err := o.b.ReadLocalCut()
	if err != nil {
		o.unknown = err
		return nil, err
	}
	s := r.statement
	if state.slot != s.Slot || state.prefix != s.Prefix || state.projection.cut != s.Cut || receipt.CapsuleDigest != s.Capsule ||
		receipt.ControlSlot != s.Slot || receipt.ControlPrefix != s.Prefix || o.replayState.slot != s.Slot || o.replayState.prefix != s.Prefix ||
		(o.accepted != nil && o.accepted.slot > s.Slot) {
		return nil, errAuthorityRenewal
	}
	if err = eligible(); err != nil {
		return nil, err
	}
	signed := ed25519.Sign(o.key, authorityRenewalVoteBytes(r.canonical, o.config.Member))
	return append(binary.BigEndian.AppendUint32(nil, o.config.Member), signed...), nil
}
