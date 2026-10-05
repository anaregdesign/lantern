package security

import (
	"crypto/ed25519"
	"encoding/json"
	"testing"
	"time"
)

// Shared test-only origin issuer. No production signing/consume capability.
func s2cTestSeal(t *testing.T, trust *s2cTrust, originID uint32, key ed25519.PrivateKey, id FullChangeID, operation OperationIdentity, serial uint64, purpose bool) []byte {
	t.Helper()
	h := s1Seal(trust.genesis.state.projection, operation, 1, purpose)
	h.id, h.serial = id, serial
	return s2cTestSealH(t, trust, originID, key, h)
}

func s2cTestSealH(t *testing.T, trust *s2cTrust, originID uint32, key ed25519.PrivateKey, handoff *S1Handoff) []byte {
	t.Helper()
	o, ok := trust.origin(originID)
	if !ok || handoff == nil || handoff.authorization == nil {
		t.Fatal("invalid origin fixture")
	}
	a := handoff.authorization
	h := s2cHistoricalHeader{Version: s2cVersion, Scope: trust.scope, OriginID: originID, OriginDigest: trust.originDigests[originID], Incarnation: o.Incarnation, ID: handoff.id, Serial: handoff.serial, OperationDigest: handoff.operation.digest, Authentication: a.authentication, Lineage: a.lineage, Consume: a.consume.UTC(), CredentialDeadline: a.credentialDeadline.UTC()}
	h.Credential = s2cCredentialClaim{Profile: o.Profile, CredentialDigest: [32]byte{1}, VerificationDigest: [32]byte{2}, ConsumeID: [32]byte{3}, CredentialNotBefore: h.Consume.Add(-time.Minute), CredentialIssuedAt: h.Consume.Add(-time.Minute), AuthenticationTime: h.Consume.Add(-24 * time.Hour), CredentialExpiresAt: h.CredentialDeadline, AdmissionDeadline: h.CredentialDeadline}
	if a.authentication.Provenance == RFC9068Bearer {
		h.Credential.EnrollmentDigest, h.Credential.NamespaceDigest = [32]byte{4}, [32]byte{5}
	} else if a.authentication.SessionDigest != "" {
		h.Credential.CSRFProof, h.Credential.SessionExpiresAt = [32]byte{6}, h.CredentialDeadline
	}
	if a.purposeEvidence != [32]byte{} {
		h.Purpose = &s2cPurposeClaim{ID: h.ID, OperationDigest: h.OperationDigest, Actor: handoff.operation.actor, Reviewed: handoff.operation.reviewed, OriginDigest: h.OriginDigest, Serial: h.Serial, IssuerConfigRevision: h.Authentication.IssuerConfigRevision, Lineage: h.Lineage, Challenge: [32]byte{7}, ReauthenticationEvidence: [32]byte{8}, ReviewAt: h.Consume.Add(-4 * time.Second), NotBefore: h.Consume.Add(-3 * time.Second), AuthenticationTime: h.Consume.Add(-2 * time.Second), ApprovedAt: h.Consume.Add(-time.Second), CredentialExpiresAt: a.purposeDeadline.UTC(), Deadline: a.purposeDeadline.UTC()}
		h.PurposeDeadline = a.purposeDeadline.UTC()
	}
	return s2cTestSignHistorical(t, trust, key, h, handoff.operation)
}

// This is the only issuer. Production code can encode untrusted bodies but
// cannot turn an old timestamp/authentication into a new sealed input.
func s2cTestSignHistorical(t *testing.T, trust *s2cTrust, key ed25519.PrivateKey, h s2cHistoricalHeader, operation OperationIdentity) []byte {
	t.Helper()
	h.CredentialEvidence = s2cCredentialEvidence(h)
	if h.Purpose != nil {
		h.PurposeEvidence = s2cHash("purpose-evidence", h.Purpose)
		h.PurposeBinding = s1PurposeBinding(h.ID, operation)
	}
	a := &s1VerifiedAuthorization{h.Authentication, h.Lineage, h.CredentialEvidence, h.PurposeEvidence, h.PurposeBinding, h.Consume, h.CredentialDeadline, h.PurposeDeadline}
	h.Value = (&S1Handoff{h.ID, operation, h.OriginDigest, h.Serial, a}).digest()
	body, err := s2cHistoricalBody(h, operation.Encode(), trust.bounds)
	if err != nil {
		t.Fatal(err)
	}
	return append(body, ed25519.Sign(key, body)...)
}

func s2cTestHistoricalParts(t *testing.T, raw []byte) (s2cHistoricalHeader, OperationIdentity) {
	t.Helper()
	r := s2CapsuleReader{raw[len(s2cHistoricalMagic):]}
	b, err := r.record()
	if err != nil {
		t.Fatal(err)
	}
	var h s2cHistoricalHeader
	if err = json.Unmarshal(b, &h); err != nil {
		t.Fatal(err)
	}
	b, err = r.record()
	if err != nil {
		t.Fatal(err)
	}
	var op struct {
		Version  uint16
		Actor    Identity
		Reviewed SemanticCut
		Command  S1Command
	}
	if err = json.Unmarshal(b[len("lantern/security/s1/operation\x00"):], &op); err != nil {
		t.Fatal(err)
	}
	return h, OperationIdentity{op.Actor, op.Reviewed, string(b), h.OperationDigest}
}

func s2cTestProfile() s2cAdmissionProfile {
	return s2cAdmissionProfile{Version: s2cVersion, BrowserCode: true, HumanBearer: true, CredentialContract: [32]byte{11}, ConsumeContract: [32]byte{12}, PurposeContract: [32]byte{13}}
}

func s2cTestBounds() s2cBounds {
	return s2cBounds{s2cMaxHistoricalBytes, s2cMaxHeaderBytes, s2cMaxProofBytes, s2cMaxPayloadBytes, s2TestLimits()}
}
