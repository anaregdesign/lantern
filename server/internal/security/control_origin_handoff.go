package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"time"
)

const authorityHistoricalMagic = "lantern/security/s2c/historical\x00\x02"

// Historical data, never a constructor for authorityCurrentTime. The origin
// attests the native sample/UTC association, while peers can independently
// verify interval/deadline arithmetic and the exact bound quorum statement.
type authorityConsumeTime struct {
	Profile               [32]byte
	HostBoot              [32]byte
	Process               [16]byte
	Counter, StartCounter uint64
	SourceSequence        uint64
	UTCLow, UTCHigh       uint64
}

func (s authorityConsumeTime) utcTimes() (time.Time, time.Time) {
	return time.Unix(0, int64(s.UTCLow)).UTC(), time.Unix(0, int64(s.UTCHigh)).UTC()
}

type authorityHistoricalHeader struct {
	Version         uint16
	Scope           [32]byte
	OriginID        uint32
	OriginDigest    [32]byte
	Incarnation     [16]byte
	ID              FullChangeID
	Serial          uint64
	OperationDigest [32]byte
	Authentication  Authentication
	Lineage         uint64
	Time            authorityConsumeTime
	// Conservative derived upper endpoint, not an exact real-world UTC claim.
	ConsumeUpper                        time.Time
	CredentialDeadline, PurposeDeadline time.Time
	Credential                          authorityCredentialClaim
	Purpose                             *authorityPurposeClaim
	Workloads                           [32]byte
	Renewal                             []byte
	CredentialEvidence, PurposeEvidence [32]byte
	PurposeBinding, Value               [32]byte
}

func authorityCredentialEvidence(h authorityHistoricalHeader) [32]byte {
	return s2cHash("current-credential-evidence-v2", struct {
		Scope, Origin, Operation [32]byte
		ID                       FullChangeID
		Serial, Lineage          uint64
		Authentication           Authentication
		Time                     authorityConsumeTime
		ConsumeUpper, Deadline   time.Time
		Credential               authorityCredentialClaim
		Workloads, Renewal       [32]byte
	}{h.Scope, h.OriginDigest, h.OperationDigest, h.ID, h.Serial, h.Lineage, h.Authentication, h.Time, h.ConsumeUpper, h.CredentialDeadline, h.Credential, h.Workloads, sha256.Sum256(h.Renewal)})
}

func verifyAuthorityConsumeTime(h authorityHistoricalHeader, t *s2cTrust, operation OperationIdentity, origin s2cOriginDescriptor) bool {
	s := h.Time
	p := authorityOperationalTimePremises()
	if s.Profile != sha256.Sum256([]byte(authorityTimeProfileDescription)) || s.HostBoot == [32]byte{} || s.Process == [16]byte{} || s.SourceSequence == 0 ||
		s.UTCLow < p.validUTC.low || s.UTCHigh >= p.validUTC.high || s.UTCLow > s.UTCHigh || s.UTCHigh-s.UTCLow >= p.maxWidth {
		return false
	}
	_, upper := s.utcTimes()
	if h.ConsumeUpper != upper || !s2cCanonicalTime(h.ConsumeUpper, false) {
		return false
	}
	start := authorityTimeStamp{boot: s.HostBoot, process: s.Process, nanos: s.StartCounter}
	end := authorityTimeStamp{boot: s.HostBoot, process: s.Process, nanos: s.Counter}
	elapsed, err := p.elapsed(start, end)
	if err != nil || elapsed.high >= authorityRenewalLifetime {
		return false
	}
	certificate, err := parseAuthorityRenewalCertificate(h.Renewal, t, h.Workloads)
	if err != nil {
		return false
	}
	r := certificate.request.statement
	return r.Receiver == origin.Member && r.Boot == s.Process && r.TimeProfile == s.Profile && r.Cut == operation.reviewed
}

func authorityHistoricalBody(h authorityHistoricalHeader, operation []byte, bounds s2cBounds) ([]byte, error) {
	if !bounds.valid() || len(operation) == 0 || len(operation) > MaxImageBytes+len("lantern/security/s1/operation\x00") || len(h.Renewal) > authorityRenewalMaxCertificate {
		return nil, errS2CHistorical
	}
	header, err := json.Marshal(h)
	if err != nil || uint64(len(header)) > bounds.HeaderBytes {
		return nil, errS2CHistorical
	}
	n := uint64(len(authorityHistoricalMagic)+8+ed25519.SignatureSize) + uint64(len(header)) + uint64(len(operation))
	if n > bounds.HistoricalBytes {
		return nil, errS2CHistorical
	}
	out := make([]byte, 0, int(n))
	out = append(out, authorityHistoricalMagic...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(header)))
	out = append(out, header...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(operation)))
	return append(out, operation...), nil
}

func verifyAuthorityHistoricalH(t *s2cTrust, encoded []byte) (*s2cHistoricalH, error) {
	if t == nil || uint64(len(encoded)) > t.bounds.HistoricalBytes || len(encoded) < len(authorityHistoricalMagic)+8+ed25519.SignatureSize || !bytes.HasPrefix(encoded, []byte(authorityHistoricalMagic)) {
		return nil, errS2CHistorical
	}
	r := s2CapsuleReader{encoded[len(authorityHistoricalMagic):]}
	if n := binary.BigEndian.Uint32(r.remaining); n == 0 || uint64(n) > t.bounds.HeaderBytes {
		return nil, errS2CHistorical
	}
	header, err := r.record()
	if err != nil || len(r.remaining) < 4 {
		return nil, errS2CHistorical
	}
	if n := binary.BigEndian.Uint32(r.remaining); n == 0 || uint64(n) > uint64(MaxImageBytes)+uint64(len("lantern/security/s1/operation\x00")) {
		return nil, errS2CHistorical
	}
	opBytes, err := r.record()
	if err != nil || len(r.remaining) != ed25519.SignatureSize {
		return nil, errS2CHistorical
	}
	var h authorityHistoricalHeader
	if s2StrictJSON(header, &h, t.genesis.state.configuration.Policy) != nil || h.Version != authorityAdmissionVersion || h.Scope != t.scope || h.Serial == 0 || !h.ID.valid() ||
		h.ID.Domain != t.genesis.state.projection.cut.Domain || h.ID.Cohort != t.genesis.state.projection.cut.Cohort {
		return nil, errS2CHistorical
	}
	origin, known := t.origin(h.OriginID)
	if !known || origin.Profile != authorityAdmissionProfile() || h.ID.Namespace != origin.Namespace || h.OriginDigest != t.originDigests[origin.ID] || h.Incarnation != origin.Incarnation ||
		!ed25519.Verify(origin.PublicKey[:], encoded[:len(encoded)-ed25519.SignatureSize], r.remaining) {
		return nil, errS2CHistorical
	}
	const prefix = "lantern/security/s1/operation\x00"
	if !bytes.HasPrefix(opBytes, []byte(prefix)) || h.OperationDigest != sha256.Sum256(opBytes) {
		return nil, errS2CHistorical
	}
	var op struct {
		Version  uint16
		Actor    Identity
		Reviewed SemanticCut
		Command  S1Command
	}
	if s2StrictJSON(opBytes[len(prefix):], &op, t.genesis.state.configuration.Policy) != nil || op.Version != S1Version || op.Actor.Kind != OIDCPrincipal {
		return nil, errS2CHistorical
	}
	operation, err := NewS1Operation(op.Actor, op.Reviewed, op.Command, t.genesis.state.configuration.Policy)
	if err != nil || operation.canonical != string(opBytes) || !verifyAuthorityConsumeTime(h, t, operation, origin) || !verifyAuthorityCredential(h, operation, op.Command) || !verifyAuthorityPurpose(h, operation) ||
		h.CredentialEvidence != authorityCredentialEvidence(h) {
		return nil, errS2CHistorical
	}
	a := &s1VerifiedAuthorization{h.Authentication, h.Lineage, h.CredentialEvidence, h.PurposeEvidence, h.PurposeBinding, h.ConsumeUpper, h.CredentialDeadline, h.PurposeDeadline}
	handoff := &S1Handoff{h.ID, operation, h.OriginDigest, h.Serial, a}
	if !handoff.valid(t.genesis.state.projection.cut) || handoff.digest() != h.Value {
		return nil, errS2CHistorical
	}
	return &s2cHistoricalH{t.scope, string(encoded), handoff, origin.ID}, nil
}
