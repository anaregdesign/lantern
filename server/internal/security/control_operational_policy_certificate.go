package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

const (
	operationalPolicySupportContractVersion uint16 = 1
	operationalPolicySupportCertificateMax         = 64 << 10
	operationalPolicySupportVoter                  = "voter"
	operationalPolicySupportOrigin                 = "origin"
	operationalPolicySupportDomain                 = "lantern/security/operational-policy/support\x00\x01"
)

var errOperationalPolicySupport = errors.New("invalid all-participant operational policy support")

// This contract names the installed runtime owner and the durable minimum
// reader/application semantics. It is not a caller's declaration of support.
func operationalPolicySupportContract(group operationalpolicy.Group) [32]byte {
	if !group.Valid() {
		return [32]byte{}
	}
	return sha256.Sum256([]byte("lantern/operational-policy-v1/" + string(group) + ": closed atomic typed values; generation CAS; actual available/prepare/apply owner; prepared application floor before signature; exact all-voter/all-origin adoption; Search every-update resolved-baseline fingerprint"))
}

type operationalPolicySupportStatement struct {
	Version            uint16
	Group              operationalpolicy.Group
	ApplicationVersion uint32
	Contract           [32]byte
	Profile            CurrentProfile
	Original           [32]byte
}

type operationalPolicySupportAttestation struct {
	Role      string
	Signer    uint32
	Floor     s2cJournalFloor
	Signature []byte
}

// The two arrays are independently bounded by the existing maximum fixed
// cohort size. Keeping them separate preserves the strict JSON array ceiling.
type operationalPolicySupportCertificateWire struct {
	Statement operationalPolicySupportStatement
	Voters    []operationalPolicySupportAttestation
	Origins   []operationalPolicySupportAttestation
}

// Only complete signature verification constructs this detached certificate.
// It carries no authority to consume credentials or mutate native state.
type operationalPolicySupportCertificate struct {
	statement operationalPolicySupportStatement
	voters    []operationalPolicySupportAttestation
	origins   []operationalPolicySupportAttestation
	raw       string
}

// operationalPolicyProfileForOriginal derives the entire expected profile from one
// authentic immutable original and independently provisioned native trust.
// A supplied public profile cannot choose another workload/time binding.
func operationalPolicyProfileForOriginal(t *s2cTrust, original *s2cHistoricalH) (CurrentProfile, error) {
	if t == nil || original == nil {
		return CurrentProfile{}, errOperationalPolicySupport
	}
	verified, err := verifyAuthorityHistoricalH(t, []byte(original.raw))
	if err != nil || verified.digest() != original.digest() || verified.scope != original.scope {
		return CurrentProfile{}, errOperationalPolicySupport
	}
	command, err := verified.handoff.operation.command()
	if err != nil || command.Kind != S1Management || verified.handoff.authorization == nil || verified.handoff.authorization.authentication.Class != EndUser {
		return CurrentProfile{}, errOperationalPolicySupport
	}
	containsOperationalPolicy := false
	for _, change := range command.Changes {
		containsOperationalPolicy = containsOperationalPolicy || change.Kind == PutOperationalPolicy
	}
	if !containsOperationalPolicy {
		return CurrentProfile{}, errOperationalPolicySupport
	}
	reader := s2CapsuleReader{[]byte(verified.raw)[len(authorityHistoricalMagic):]}
	rawHeader, err := reader.record()
	var header authorityHistoricalHeader
	if err != nil || s2StrictJSON(rawHeader, &header, t.genesis.state.configuration.Policy) != nil {
		return CurrentProfile{}, errOperationalPolicySupport
	}
	cut := t.genesis.state.projection.cut
	profile := CurrentProfile{CurrentPublicVersion, cut.Domain, cut.Cohort, cut.Generation,
		t.scope, header.Time.Profile, header.Workloads, t.genesis.state.configuration.Digest()}
	if profile.Binding() == "" {
		return CurrentProfile{}, errOperationalPolicySupport
	}
	return profile, nil
}

func newOperationalPolicySupportStatement(t *s2cTrust, profile CurrentProfile, original *s2cHistoricalH) (operationalPolicySupportStatement, error) {
	expected, err := operationalPolicyProfileForOriginal(t, original)
	if err != nil || profile != expected {
		return operationalPolicySupportStatement{}, errOperationalPolicySupport
	}
	change, group, ok := operationalPolicyChangeForH(original.handoff)
	if !ok || change.Validate() != nil {
		return operationalPolicySupportStatement{}, errOperationalPolicySupport
	}
	return operationalPolicySupportStatement{operationalPolicySupportContractVersion, group, operationalpolicy.Version, operationalPolicySupportContract(group), profile, sha256.Sum256([]byte(original.raw))}, nil
}

// The fixed role/key identity is part of the signature input. A voter signature
// cannot stand in for an origin, even on the same native participant.
func operationalPolicySupportAttestationBody(statement operationalPolicySupportStatement, attestation operationalPolicySupportAttestation) ([]byte, error) {
	if statement.Version != operationalPolicySupportContractVersion || statement.ApplicationVersion != operationalpolicy.Version || !statement.Group.Valid() || statement.Contract != operationalPolicySupportContract(statement.Group) || statement.Profile.Binding() == "" || statement.Original == [32]byte{} ||
		attestation.Role != operationalPolicySupportVoter && attestation.Role != operationalPolicySupportOrigin || attestation.Signer == 0 ||
		attestation.Floor.Scope == [32]byte{} || attestation.Floor.Index < 3 || attestation.Floor.Index == ^uint64(0) || attestation.Floor.Chain == [32]byte{} {
		return nil, errOperationalPolicySupport
	}
	body, err := json.Marshal(struct {
		Statement operationalPolicySupportStatement
		Role      string
		Signer    uint32
		Floor     s2cJournalFloor
	}{statement, attestation.Role, attestation.Signer, attestation.Floor})
	if err != nil {
		return nil, errOperationalPolicySupport
	}
	return append([]byte(operationalPolicySupportDomain), body...), nil
}

func verifyOperationalPolicySupportAttestation(t *s2cTrust, statement operationalPolicySupportStatement, attestation operationalPolicySupportAttestation) bool {
	if !operationalPolicySupportProfileMatchesTrust(t, statement.Profile) || len(attestation.Signature) != ed25519.SignatureSize {
		return false
	}
	var key [ed25519.PublicKeySize]byte
	switch attestation.Role {
	case operationalPolicySupportVoter:
		member, ok := t.member(attestation.Signer)
		if !ok {
			return false
		}
		key = member.PublicKey
	case operationalPolicySupportOrigin:
		origin, ok := t.origin(attestation.Signer)
		if !ok || origin.Profile != authorityAdmissionProfile() {
			return false
		}
		key = origin.PublicKey
	default:
		return false
	}
	body, err := operationalPolicySupportAttestationBody(statement, attestation)
	return err == nil && ed25519.Verify(key[:], body, attestation.Signature)
}

func verifyOperationalPolicySupportCertificate(t *s2cTrust, expected operationalPolicySupportStatement, wire operationalPolicySupportCertificateWire) bool {
	if t == nil || wire.Statement != expected || len(t.members) < 1 || len(t.members) > 31 || len(t.origins) < 1 || len(t.origins) > 31 ||
		len(wire.Voters) != len(t.members) || len(wire.Origins) != len(t.origins) {
		return false
	}
	floors := make(map[uint32]s2cJournalFloor, len(t.members))
	for i, member := range t.members {
		proof := wire.Voters[i]
		if proof.Role != operationalPolicySupportVoter || proof.Signer != member.ID || !verifyOperationalPolicySupportAttestation(t, expected, proof) {
			return false
		}
		floors[member.ID] = proof.Floor
	}
	for i, origin := range t.origins {
		proof := wire.Origins[i]
		if proof.Role != operationalPolicySupportOrigin || proof.Signer != origin.ID || proof.Floor != floors[origin.Member] || !verifyOperationalPolicySupportAttestation(t, expected, proof) {
			return false
		}
	}
	return true
}

// Profile fields derived from independent trust cannot be replaced even when
// checking a single remote support response before assembling the certificate.
func operationalPolicySupportProfileMatchesTrust(t *s2cTrust, profile CurrentProfile) bool {
	if t == nil || t.genesis == nil || t.genesis.state == nil || t.genesis.state.projection == nil || profile.Binding() == "" {
		return false
	}
	cut := t.genesis.state.projection.cut
	return profile.Version == CurrentPublicVersion && profile.Domain == cut.Domain && profile.Cohort == cut.Cohort && profile.Generation == cut.Generation &&
		profile.Protocol == t.scope && profile.Time == sha256.Sum256([]byte(authorityTimeProfileDescription)) && profile.Configuration == t.genesis.state.configuration.Digest()
}

// operationalPolicySupportCertificateBound renders the exact fixed cohort with its
// largest admitted decimal floor/hash encodings. Signature byte strings have a
// fixed base64 length. The original H and outer framing are charged separately.
// This preflight runs before any participant's irreversible version floor.
func operationalPolicySupportCertificateBound(t *s2cTrust, profile CurrentProfile, group operationalpolicy.Group) (uint64, error) {
	if !operationalPolicySupportProfileMatchesTrust(t, profile) || len(t.members) < 1 || len(t.members) > 31 || len(t.origins) < 1 || len(t.origins) > 31 {
		return 0, errOperationalPolicySupport
	}
	var largest [32]byte
	for i := range largest {
		largest[i] = 255
	}
	wire := operationalPolicySupportCertificateWire{Statement: operationalPolicySupportStatement{operationalPolicySupportContractVersion, group, operationalpolicy.Version, operationalPolicySupportContract(group), profile, largest}}
	floor := s2cJournalFloor{largest, ^uint64(0) - 1, largest}
	signature := bytes.Repeat([]byte{255}, ed25519.SignatureSize)
	for _, member := range t.members {
		wire.Voters = append(wire.Voters, operationalPolicySupportAttestation{operationalPolicySupportVoter, member.ID, floor, signature})
	}
	for _, origin := range t.origins {
		wire.Origins = append(wire.Origins, operationalPolicySupportAttestation{operationalPolicySupportOrigin, origin.ID, floor, signature})
	}
	raw, err := json.Marshal(wire)
	if err != nil || len(raw) > operationalPolicySupportCertificateMax || uint64(len(raw)) > t.bounds.ProofBytes {
		return 0, errOperationalPolicySupport
	}
	return uint64(len(raw)), nil
}

func encodeOperationalPolicySupportCertificate(t *s2cTrust, profile CurrentProfile, original *s2cHistoricalH, voters, origins []operationalPolicySupportAttestation) ([]byte, error) {
	statement, err := newOperationalPolicySupportStatement(t, profile, original)
	if err != nil {
		return nil, err
	}
	wire := operationalPolicySupportCertificateWire{statement, voters, origins}
	if !verifyOperationalPolicySupportCertificate(t, statement, wire) {
		return nil, errOperationalPolicySupport
	}
	raw, err := json.Marshal(wire)
	if err != nil || len(raw) > operationalPolicySupportCertificateMax || uint64(len(raw)) > t.bounds.ProofBytes {
		return nil, errOperationalPolicySupport
	}
	return raw, nil
}

func decodeOperationalPolicySupportCertificate(t *s2cTrust, profile CurrentProfile, original *s2cHistoricalH, raw []byte) (*operationalPolicySupportCertificate, error) {
	statement, err := newOperationalPolicySupportStatement(t, profile, original)
	if err != nil || len(raw) == 0 || len(raw) > operationalPolicySupportCertificateMax || uint64(len(raw)) > t.bounds.ProofBytes {
		return nil, errOperationalPolicySupport
	}
	var wire operationalPolicySupportCertificateWire
	if s2StrictJSON(raw, &wire, t.genesis.state.configuration.Policy) != nil || !verifyOperationalPolicySupportCertificate(t, statement, wire) {
		return nil, errOperationalPolicySupport
	}
	// Decode owns all arrays/signatures. Copy raw so caller mutation cannot
	// replace a previously verified candidate's retained proof bytes.
	return &operationalPolicySupportCertificate{wire.Statement, wire.Voters, wire.Origins, string(bytes.Clone(raw))}, nil
}
