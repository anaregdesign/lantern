package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
)

const (
	logPolicySupportContractVersion uint16 = 1
	logPolicySupportCertificateMax         = 64 << 10
	logPolicySupportVoter                  = "voter"
	logPolicySupportOrigin                 = "origin"
	logPolicySupportDomain                 = "lantern/security/log-policy/support\x00\x01"
)

var errLogPolicySupport = errors.New("invalid all-participant log policy support")

// This contract names the installed runtime owner and the durable minimum
// reader/application semantics. It is not a caller's declaration of support.
func logPolicySupportContract() [32]byte {
	return sha256.Sum256([]byte("lantern/log-policy-v1: typed debug/info/warn/error; immutable desired/applied generations; owned runtime application before readiness; durable participant floor before support signature; exact all-voter/all-origin adoption"))
}

type logPolicySupportStatement struct {
	Version  uint16
	Contract [32]byte
	Profile  CurrentProfile
	Original [32]byte
}

type logPolicySupportAttestation struct {
	Role      string
	Signer    uint32
	Floor     s2cJournalFloor
	Signature []byte
}

// The two arrays are independently bounded by the existing maximum fixed
// cohort size. Keeping them separate preserves the strict JSON array ceiling.
type logPolicySupportCertificateWire struct {
	Statement logPolicySupportStatement
	Voters    []logPolicySupportAttestation
	Origins   []logPolicySupportAttestation
}

// Only complete signature verification constructs this detached certificate.
// It carries no authority to consume credentials or mutate native state.
type logPolicySupportCertificate struct {
	statement logPolicySupportStatement
	voters    []logPolicySupportAttestation
	origins   []logPolicySupportAttestation
	raw       string
}

// logPolicyProfileForOriginal derives the entire expected profile from one
// authentic immutable original and independently provisioned native trust.
// A supplied public profile cannot choose another workload/time binding.
func logPolicyProfileForOriginal(t *s2cTrust, original *s2cHistoricalH) (CurrentProfile, error) {
	if t == nil || original == nil {
		return CurrentProfile{}, errLogPolicySupport
	}
	verified, err := verifyAuthorityHistoricalH(t, []byte(original.raw))
	if err != nil || verified.digest() != original.digest() || verified.scope != original.scope {
		return CurrentProfile{}, errLogPolicySupport
	}
	command, err := verified.handoff.operation.command()
	if err != nil || command.Kind != S1Management || verified.handoff.authorization == nil || verified.handoff.authorization.authentication.Class != EndUser {
		return CurrentProfile{}, errLogPolicySupport
	}
	containsLogPolicy := false
	for _, change := range command.Changes {
		containsLogPolicy = containsLogPolicy || change.Kind == PutLogPolicy
	}
	if !containsLogPolicy {
		return CurrentProfile{}, errLogPolicySupport
	}
	reader := s2CapsuleReader{[]byte(verified.raw)[len(authorityHistoricalMagic):]}
	rawHeader, err := reader.record()
	var header authorityHistoricalHeader
	if err != nil || s2StrictJSON(rawHeader, &header, t.genesis.state.configuration.Policy) != nil {
		return CurrentProfile{}, errLogPolicySupport
	}
	cut := t.genesis.state.projection.cut
	profile := CurrentProfile{CurrentPublicVersion, cut.Domain, cut.Cohort, cut.Generation,
		t.scope, header.Time.Profile, header.Workloads, t.genesis.state.configuration.Digest()}
	if profile.Binding() == "" {
		return CurrentProfile{}, errLogPolicySupport
	}
	return profile, nil
}

func newLogPolicySupportStatement(t *s2cTrust, profile CurrentProfile, original *s2cHistoricalH) (logPolicySupportStatement, error) {
	expected, err := logPolicyProfileForOriginal(t, original)
	if err != nil || profile != expected {
		return logPolicySupportStatement{}, errLogPolicySupport
	}
	return logPolicySupportStatement{logPolicySupportContractVersion, logPolicySupportContract(), profile, sha256.Sum256([]byte(original.raw))}, nil
}

// The fixed role/key identity is part of the signature input. A voter signature
// cannot stand in for an origin, even on the same native participant.
func logPolicySupportAttestationBody(statement logPolicySupportStatement, attestation logPolicySupportAttestation) ([]byte, error) {
	if statement.Version != logPolicySupportContractVersion || statement.Contract != logPolicySupportContract() || statement.Profile.Binding() == "" || statement.Original == [32]byte{} ||
		attestation.Role != logPolicySupportVoter && attestation.Role != logPolicySupportOrigin || attestation.Signer == 0 ||
		attestation.Floor.Scope == [32]byte{} || attestation.Floor.Index < 3 || attestation.Floor.Index == ^uint64(0) || attestation.Floor.Chain == [32]byte{} {
		return nil, errLogPolicySupport
	}
	body, err := json.Marshal(struct {
		Statement logPolicySupportStatement
		Role      string
		Signer    uint32
		Floor     s2cJournalFloor
	}{statement, attestation.Role, attestation.Signer, attestation.Floor})
	if err != nil {
		return nil, errLogPolicySupport
	}
	return append([]byte(logPolicySupportDomain), body...), nil
}

func verifyLogPolicySupportAttestation(t *s2cTrust, statement logPolicySupportStatement, attestation logPolicySupportAttestation) bool {
	if !logPolicySupportProfileMatchesTrust(t, statement.Profile) || len(attestation.Signature) != ed25519.SignatureSize {
		return false
	}
	var key [ed25519.PublicKeySize]byte
	switch attestation.Role {
	case logPolicySupportVoter:
		member, ok := t.member(attestation.Signer)
		if !ok {
			return false
		}
		key = member.PublicKey
	case logPolicySupportOrigin:
		origin, ok := t.origin(attestation.Signer)
		if !ok || origin.Profile != authorityAdmissionProfile() {
			return false
		}
		key = origin.PublicKey
	default:
		return false
	}
	body, err := logPolicySupportAttestationBody(statement, attestation)
	return err == nil && ed25519.Verify(key[:], body, attestation.Signature)
}

func verifyLogPolicySupportCertificate(t *s2cTrust, expected logPolicySupportStatement, wire logPolicySupportCertificateWire) bool {
	if t == nil || wire.Statement != expected || len(t.members) < 1 || len(t.members) > 31 || len(t.origins) < 1 || len(t.origins) > 31 ||
		len(wire.Voters) != len(t.members) || len(wire.Origins) != len(t.origins) {
		return false
	}
	floors := make(map[uint32]s2cJournalFloor, len(t.members))
	for i, member := range t.members {
		proof := wire.Voters[i]
		if proof.Role != logPolicySupportVoter || proof.Signer != member.ID || !verifyLogPolicySupportAttestation(t, expected, proof) {
			return false
		}
		floors[member.ID] = proof.Floor
	}
	for i, origin := range t.origins {
		proof := wire.Origins[i]
		if proof.Role != logPolicySupportOrigin || proof.Signer != origin.ID || proof.Floor != floors[origin.Member] || !verifyLogPolicySupportAttestation(t, expected, proof) {
			return false
		}
	}
	return true
}

// Profile fields derived from independent trust cannot be replaced even when
// checking a single remote support response before assembling the certificate.
func logPolicySupportProfileMatchesTrust(t *s2cTrust, profile CurrentProfile) bool {
	if t == nil || t.genesis == nil || t.genesis.state == nil || t.genesis.state.projection == nil || profile.Binding() == "" {
		return false
	}
	cut := t.genesis.state.projection.cut
	return profile.Version == CurrentPublicVersion && profile.Domain == cut.Domain && profile.Cohort == cut.Cohort && profile.Generation == cut.Generation &&
		profile.Protocol == t.scope && profile.Time == sha256.Sum256([]byte(authorityTimeProfileDescription)) && profile.Configuration == t.genesis.state.configuration.Digest()
}

// logPolicySupportCertificateBound renders the exact fixed cohort with its
// largest admitted decimal floor/hash encodings. Signature byte strings have a
// fixed base64 length. The original H and outer framing are charged separately.
// This preflight runs before any participant's irreversible version floor.
func logPolicySupportCertificateBound(t *s2cTrust, profile CurrentProfile) (uint64, error) {
	if !logPolicySupportProfileMatchesTrust(t, profile) || len(t.members) < 1 || len(t.members) > 31 || len(t.origins) < 1 || len(t.origins) > 31 {
		return 0, errLogPolicySupport
	}
	var largest [32]byte
	for i := range largest {
		largest[i] = 255
	}
	wire := logPolicySupportCertificateWire{Statement: logPolicySupportStatement{logPolicySupportContractVersion, logPolicySupportContract(), profile, largest}}
	floor := s2cJournalFloor{largest, ^uint64(0) - 1, largest}
	signature := bytes.Repeat([]byte{255}, ed25519.SignatureSize)
	for _, member := range t.members {
		wire.Voters = append(wire.Voters, logPolicySupportAttestation{logPolicySupportVoter, member.ID, floor, signature})
	}
	for _, origin := range t.origins {
		wire.Origins = append(wire.Origins, logPolicySupportAttestation{logPolicySupportOrigin, origin.ID, floor, signature})
	}
	raw, err := json.Marshal(wire)
	if err != nil || len(raw) > logPolicySupportCertificateMax || uint64(len(raw)) > t.bounds.ProofBytes {
		return 0, errLogPolicySupport
	}
	return uint64(len(raw)), nil
}

func encodeLogPolicySupportCertificate(t *s2cTrust, profile CurrentProfile, original *s2cHistoricalH, voters, origins []logPolicySupportAttestation) ([]byte, error) {
	statement, err := newLogPolicySupportStatement(t, profile, original)
	if err != nil {
		return nil, err
	}
	wire := logPolicySupportCertificateWire{statement, voters, origins}
	if !verifyLogPolicySupportCertificate(t, statement, wire) {
		return nil, errLogPolicySupport
	}
	raw, err := json.Marshal(wire)
	if err != nil || len(raw) > logPolicySupportCertificateMax || uint64(len(raw)) > t.bounds.ProofBytes {
		return nil, errLogPolicySupport
	}
	return raw, nil
}

func decodeLogPolicySupportCertificate(t *s2cTrust, profile CurrentProfile, original *s2cHistoricalH, raw []byte) (*logPolicySupportCertificate, error) {
	statement, err := newLogPolicySupportStatement(t, profile, original)
	if err != nil || len(raw) == 0 || len(raw) > logPolicySupportCertificateMax || uint64(len(raw)) > t.bounds.ProofBytes {
		return nil, errLogPolicySupport
	}
	var wire logPolicySupportCertificateWire
	if s2StrictJSON(raw, &wire, t.genesis.state.configuration.Policy) != nil || !verifyLogPolicySupportCertificate(t, statement, wire) {
		return nil, errLogPolicySupport
	}
	// Decode owns all arrays/signatures. Copy raw so caller mutation cannot
	// replace a previously verified candidate's retained proof bytes.
	return &logPolicySupportCertificate{wire.Statement, wire.Voters, wire.Origins, string(bytes.Clone(raw))}, nil
}
