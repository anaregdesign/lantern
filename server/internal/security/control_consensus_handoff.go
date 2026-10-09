package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"time"
)

const s2cHistoricalMagic = "lantern/security/s2c/historical-h\x00\x01"

var errS2CHistorical = errors.New("invalid S2-C exact historical handoff")

// Explicit configured-origin assertions, not raw credentials or an independent
// re-audit of the origin. Every field participates in credentialEvidence.
type s2cCredentialClaim struct {
	Profile             s2cAdmissionProfile
	CredentialDigest    [32]byte
	VerificationDigest  [32]byte
	CSRFProof           [32]byte
	EnrollmentDigest    [32]byte
	NamespaceDigest     [32]byte
	ConsumeID           [32]byte
	CredentialNotBefore time.Time
	CredentialIssuedAt  time.Time
	AuthenticationTime  time.Time
	CredentialExpiresAt time.Time
	SessionExpiresAt    time.Time
	AdmissionDeadline   time.Time
}

// Present purpose claims are operation-bound and have their own exclusive
// deadline. Absence is exactly JSON null and three zero S1 purpose fields.
type s2cPurposeClaim struct {
	ID                       FullChangeID
	OperationDigest          [32]byte
	Actor                    Identity
	Reviewed                 SemanticCut
	OriginDigest             [32]byte
	Serial                   uint64
	IssuerConfigRevision     uint64
	Lineage                  uint64
	Challenge                [32]byte
	ReauthenticationEvidence [32]byte
	ReviewAt                 time.Time
	NotBefore                time.Time
	AuthenticationTime       time.Time
	ApprovedAt               time.Time
	CredentialExpiresAt      time.Time
	Deadline                 time.Time
}

type s2cHistoricalHeader struct {
	Version            uint16
	Scope              [32]byte
	OriginID           uint32
	OriginDigest       [32]byte
	Incarnation        [16]byte
	ID                 FullChangeID
	Serial             uint64
	OperationDigest    [32]byte
	Authentication     Authentication
	Lineage            uint64
	Consume            time.Time
	CredentialDeadline time.Time
	PurposeDeadline    time.Time
	Credential         s2cCredentialClaim
	Purpose            *s2cPurposeClaim
	CredentialEvidence [32]byte
	PurposeEvidence    [32]byte
	PurposeBinding     [32]byte
	Value              [32]byte
}

// No method creates a current admission, consumes again or signs replacement
// bytes. Only verification of the complete exact immutable origin input creates
// this capability; the only historical issuer lives in _test.go.
type s2cHistoricalH struct {
	scope    [32]byte
	raw      string
	handoff  *S1Handoff
	originID uint32
}

func (h *s2cHistoricalH) digest() [32]byte {
	if h == nil || h.handoff == nil {
		return [32]byte{}
	}
	return h.handoff.digest()
}

func s2cCanonicalTime(t time.Time, optional bool) bool {
	if t.IsZero() {
		return optional && t == (time.Time{})
	}
	return t.Location() == time.UTC && t.Year() >= 1 && t.Year() <= 9999 && t == t.Round(0)
}

func s2cCredentialEvidence(h s2cHistoricalHeader) [32]byte {
	return s2cHash("credential-evidence", struct {
		Scope             [32]byte
		ID                FullChangeID
		Operation         [32]byte
		Origin            [32]byte
		Serial            uint64
		Authentication    Authentication
		Lineage           uint64
		Consume, Deadline time.Time
		Claim             s2cCredentialClaim
	}{h.Scope, h.ID, h.OperationDigest, h.OriginDigest, h.Serial, h.Authentication, h.Lineage, h.Consume, h.CredentialDeadline, h.Credential})
}

func s2cVerifyCredential(h s2cHistoricalHeader, origin s2cOriginDescriptor, command S1Command) bool {
	c, a := h.Credential, h.Authentication
	if c.Profile != origin.Profile || c.CredentialDigest == [32]byte{} || c.VerificationDigest == [32]byte{} || c.ConsumeID == [32]byte{} || h.Lineage == 0 || a.Class != EndUser || a.IssuerConfigRevision == 0 || !s2cCanonicalTime(h.Consume, false) || !s2cCanonicalTime(h.CredentialDeadline, false) {
		return false
	}
	for _, stamp := range []time.Time{c.CredentialIssuedAt, c.CredentialNotBefore, c.CredentialExpiresAt, c.AdmissionDeadline} {
		if !s2cCanonicalTime(stamp, false) {
			return false
		}
	}
	if !s2cCanonicalTime(c.AuthenticationTime, true) || !s2cCanonicalTime(c.SessionExpiresAt, true) || h.Consume.Before(c.CredentialIssuedAt) || h.Consume.Before(c.CredentialNotBefore) || c.AuthenticationTime.After(h.Consume) {
		return false
	}
	deadline := c.CredentialExpiresAt
	if c.AdmissionDeadline.Before(deadline) {
		deadline = c.AdmissionDeadline
	}
	if !c.SessionExpiresAt.IsZero() && c.SessionExpiresAt.Before(deadline) {
		deadline = c.SessionExpiresAt
	}
	if h.CredentialDeadline != deadline || !h.Consume.Before(deadline) {
		return false
	}
	switch a.Provenance {
	case RFC9068Bearer:
		if !origin.Profile.HumanBearer || command.Kind != S1Management || a.SessionDigest != "" || c.CSRFProof != [32]byte{} || !c.SessionExpiresAt.IsZero() || c.EnrollmentDigest == [32]byte{} || c.NamespaceDigest == [32]byte{} {
			return false
		}
	case BrowserCode:
		if !origin.Profile.BrowserCode || c.EnrollmentDigest != [32]byte{} || c.NamespaceDigest != [32]byte{} {
			return false
		}
		if a.SessionDigest == "" {
			if c.CSRFProof != [32]byte{} || !c.SessionExpiresAt.IsZero() {
				return false
			}
		} else if !validHexDigest(a.SessionDigest) || c.CSRFProof == [32]byte{} || c.SessionExpiresAt.IsZero() {
			return false
		}
	default:
		return false
	}
	return h.CredentialEvidence == s2cCredentialEvidence(h)
}

func s2cVerifyPurpose(h s2cHistoricalHeader, operation OperationIdentity) bool {
	p := h.Purpose
	if p == nil {
		return h.PurposeEvidence == [32]byte{} && h.PurposeBinding == [32]byte{} && h.PurposeDeadline == (time.Time{})
	}
	if p.ID != h.ID || p.OperationDigest != h.OperationDigest || p.Actor != operation.actor || p.Reviewed != operation.reviewed || p.OriginDigest != h.OriginDigest || p.Serial != h.Serial || p.IssuerConfigRevision != h.Authentication.IssuerConfigRevision || p.Lineage != h.Lineage || p.Challenge == [32]byte{} || p.ReauthenticationEvidence == [32]byte{} {
		return false
	}
	for _, stamp := range []time.Time{p.ReviewAt, p.NotBefore, p.AuthenticationTime, p.ApprovedAt, p.CredentialExpiresAt, p.Deadline, h.PurposeDeadline} {
		if !s2cCanonicalTime(stamp, false) {
			return false
		}
	}
	deadline := p.Deadline
	if p.CredentialExpiresAt.Before(deadline) {
		deadline = p.CredentialExpiresAt
	}
	return p.ReviewAt.Before(p.NotBefore) && !p.AuthenticationTime.Before(p.NotBefore) && !p.ApprovedAt.Before(p.AuthenticationTime) && !h.Consume.Before(p.ApprovedAt) && h.Consume.Before(deadline) && h.PurposeDeadline == deadline && h.PurposeBinding == s1PurposeBinding(h.ID, operation) && h.PurposeEvidence == s2cHash("purpose-evidence", p)
}

// Parsing and signature validation never sample current time. Expiry governs
// the original consume only, so an exact preexpiry H can finish after expiry.
func verifyHistoricalH(t *s2cTrust, encoded []byte) (*s2cHistoricalH, error) {
	if bytes.HasPrefix(encoded, []byte(authorityHistoricalMagic)) {
		return verifyAuthorityHistoricalH(t, encoded)
	}
	if t == nil || uint64(len(encoded)) > t.bounds.HistoricalBytes || len(encoded) < len(s2cHistoricalMagic)+8+ed25519.SignatureSize || !bytes.HasPrefix(encoded, []byte(s2cHistoricalMagic)) {
		return nil, errS2CHistorical
	}
	reader := s2CapsuleReader{encoded[len(s2cHistoricalMagic):]}
	headerLength := binary.BigEndian.Uint32(reader.remaining)
	if headerLength == 0 || uint64(headerLength) > t.bounds.HeaderBytes {
		return nil, errS2CHistorical
	}
	header, err := reader.record()
	if err != nil || len(reader.remaining) < 4 {
		return nil, errS2CHistorical
	}
	opLength := binary.BigEndian.Uint32(reader.remaining)
	if opLength == 0 || uint64(opLength) > uint64(MaxImageBytes)+uint64(len("lantern/security/s1/operation\x00")) {
		return nil, errS2CHistorical
	}
	opBytes, err := reader.record()
	if err != nil || len(reader.remaining) != ed25519.SignatureSize {
		return nil, errS2CHistorical
	}
	var h s2cHistoricalHeader
	if s2StrictJSON(header, &h, t.genesis.state.configuration.Policy) != nil || h.Version != s2cVersion || h.Scope != t.scope || h.Serial == 0 || !h.ID.valid() || h.ID.Domain != t.genesis.state.projection.cut.Domain || h.ID.Cohort != t.genesis.state.projection.cut.Cohort {
		return nil, errS2CHistorical
	}
	o, known := t.origin(h.OriginID)
	if !known || o.Profile.Version != s2cVersion || h.OriginDigest != t.originDigests[o.ID] || h.Incarnation != o.Incarnation || !ed25519.Verify(ed25519.PublicKey(o.PublicKey[:]), encoded[:len(encoded)-ed25519.SignatureSize], reader.remaining) {
		return nil, errS2CHistorical
	}
	const prefix = "lantern/security/s1/operation\x00"
	if !bytes.HasPrefix(opBytes, []byte(prefix)) || len(opBytes) <= len(prefix) || h.OperationDigest != sha256.Sum256(opBytes) {
		return nil, errS2CHistorical
	}
	var op struct {
		Version  uint16
		Actor    Identity
		Reviewed SemanticCut
		Command  S1Command
	}
	if s2StrictJSON(opBytes[len(prefix):], &op, DefaultPolicyLimits()) != nil || op.Version != S1Version || !op.Actor.valid() || op.Actor.Kind != OIDCPrincipal || !op.Reviewed.valid() || op.Reviewed.Domain != h.ID.Domain || op.Reviewed.Cohort != h.ID.Cohort || validateS1Command(op.Command, DefaultPolicyLimits()) != nil {
		return nil, errS2CHistorical
	}
	// The independently fixed S1 policy is the only execution configuration.
	// Stale generation/fences/revision remain valid historical review and later
	// get S1's exact CAS disposition; no review field is rewritten here.
	operation := OperationIdentity{op.Actor, op.Reviewed, string(opBytes), h.OperationDigest}
	if !s2cVerifyCredential(h, o, op.Command) || !s2cVerifyPurpose(h, operation) {
		return nil, errS2CHistorical
	}
	a := &s1VerifiedAuthorization{h.Authentication, h.Lineage, h.CredentialEvidence, h.PurposeEvidence, h.PurposeBinding, h.Consume, h.CredentialDeadline, h.PurposeDeadline}
	handoff := &S1Handoff{h.ID, operation, h.OriginDigest, h.Serial, a}
	if !handoff.valid(t.genesis.state.projection.cut) || handoff.digest() != h.Value {
		return nil, errS2CHistorical
	}
	return &s2cHistoricalH{t.scope, string(encoded), handoff, o.ID}, nil
}

// Encodes only untrusted claim data. No signing key or issuer is present in
// production. The exact body is both the signature input and durable value.
func s2cHistoricalBody(h s2cHistoricalHeader, operation []byte, bounds s2cBounds) ([]byte, error) {
	if !bounds.valid() || len(operation) == 0 || len(operation) > MaxImageBytes+len("lantern/security/s1/operation\x00") {
		return nil, errS2CHistorical
	}
	header, err := json.Marshal(h)
	if err != nil || uint64(len(header)) > bounds.HeaderBytes {
		return nil, errS2CHistorical
	}
	n := uint64(len(s2cHistoricalMagic)) + 8 + uint64(len(header)) + uint64(len(operation)) + ed25519.SignatureSize
	if n > bounds.HistoricalBytes {
		return nil, errS2CHistorical
	}
	b := make([]byte, 0, int(n))
	b = append(b, s2cHistoricalMagic...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(header)))
	b = append(b, header...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(operation)))
	b = append(b, operation...)
	return b, nil
}
