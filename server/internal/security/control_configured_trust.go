package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"reflect"
	"time"
)

const configuredTrustVersion uint16 = 1
const configuredTrustMagic = "lantern/security/configured-trust/historical\x00\x01"
const configuredOperationMagic = "lantern/security/configured-trust/operation\x00\x01"

// This class is distinct from OIDC, machine and registration authorization.
// Only verification of an independently enrolled native origin's exact signed
// configuration assertion constructs it. No public caller can supply it.
type authorityConfiguredAuthorization struct {
	desired     *ConfiguredTrust
	predecessor [32]byte
	slot        uint64
	capability  [32]byte
}

type configuredTrustHeader struct {
	Version                 uint16
	Scope                   [32]byte
	OriginID                uint32
	OriginDigest            [32]byte
	Incarnation             [16]byte
	ID                      FullChangeID
	Serial                  uint64
	Reviewed                SemanticCut
	Predecessor             [32]byte
	Desired                 [32]byte
	Operation               [32]byte
	Value                   [32]byte
	Slot                    uint64
	ConsumeLow, ConsumeHigh time.Time
	Endorsements            []configuredTrustEndorsement
}

type configuredOperation struct {
	Version  uint16
	Reviewed SemanticCut
	Issuers  []Issuer `json:"issuers"`
}

func configuredTrustCapability(t *s2cTrust, d s2cOriginDescriptor) [32]byte {
	enrolled, known := t.origin(d.ID)
	if !known || enrolled != d || t == nil || t.scope == [32]byte{} || t.originDigests[d.ID] == [32]byte{} || d.ConfiguredTrust != configuredTrustVersion || d.Profile != authorityAdmissionProfile() || d.Namespace == 0 {
		return [32]byte{}
	}
	return s2cHash("configured-trust-capability-v1", struct {
		Scope, Origin [32]byte
		Version       uint16
	}{t.scope, t.originDigests[d.ID], configuredTrustVersion})
}

func newConfiguredOperation(reviewed SemanticCut, desired *ConfiguredTrust) (OperationIdentity, error) {
	if !desired.valid() || !reviewed.valid() {
		return OperationIdentity{}, ErrS1Contract
	}
	body, err := json.Marshal(configuredOperation{configuredTrustVersion, reviewed, desired.issuers})
	if err != nil || len(body) > MaxImageBytes {
		return OperationIdentity{}, ErrS1Contract
	}
	raw := append([]byte(configuredOperationMagic), body...)
	return OperationIdentity{reviewed: reviewed, canonical: string(raw), digest: sha256.Sum256(raw)}, nil
}

func decodeConfiguredOperation(op OperationIdentity) (*ConfiguredTrust, error) {
	raw := []byte(op.canonical)
	if op.actor != (Identity{}) || !op.reviewed.valid() || len(raw) <= len(configuredOperationMagic) || len(raw) > MaxImageBytes+len(configuredOperationMagic) || !bytes.HasPrefix(raw, []byte(configuredOperationMagic)) || sha256.Sum256(raw) != op.digest {
		return nil, ErrS1Contract
	}
	var body configuredOperation
	if s2StrictJSON(raw[len(configuredOperationMagic):], &body, DefaultPolicyLimits()) != nil || body.Version != configuredTrustVersion || body.Reviewed != op.reviewed {
		return nil, ErrS1Contract
	}
	desired, err := NewConfiguredTrust(body.Issuers)
	if err != nil {
		return nil, err
	}
	expected, err := newConfiguredOperation(op.reviewed, desired)
	if err != nil || expected != op {
		return nil, ErrS1Contract
	}
	return desired, nil
}

func (h *S1Handoff) configuredDigest() [32]byte {
	if h == nil || h.configured == nil || h.configured.desired == nil || h.authorization != nil {
		return [32]byte{}
	}
	return s1Digest("configured-trust-handoff-v1", struct {
		ID                               FullChangeID
		Operation                        string
		Origin                           [32]byte
		Serial                           uint64
		Capability, Predecessor, Desired [32]byte
		Slot                             uint64
	}{h.id, h.operation.canonical, h.origin, h.serial, h.configured.capability, h.configured.predecessor, h.configured.desired.Digest(), h.configured.slot})
}

func (h *S1Handoff) validConfigured(scope SemanticCut) bool {
	if h == nil || h.configured == nil || h.authorization != nil || !h.id.valid() || h.id.Domain != scope.Domain || h.id.Cohort != scope.Cohort || h.origin == [32]byte{} || h.serial == 0 || h.operation.reviewed.Domain != scope.Domain || h.operation.reviewed.Cohort != scope.Cohort || h.configured.predecessor == [32]byte{} || h.configured.capability == [32]byte{} {
		return false
	}
	desired, err := decodeConfiguredOperation(h.operation)
	return err == nil && h.configured.desired != nil && desired.Digest() == h.configured.desired.Digest() && h.configuredDigest() != [32]byte{}
}

func evaluateConfiguredCandidate(s, advanced *S1ApplyState, h *S1Handoff, previous [32]byte) (S1ApplyResult, error) {
	// An old attestation cannot be moved to a new prefix, even after a NOOP.
	if h.configured.slot != s.slot || h.configured.predecessor != previous || h.operation.reviewed != s.projection.cut || h.id.Namespace <= s.retiredThrough {
		return S1ApplyResult{}, ErrRevisionConflict
	}
	next, err := assessConfiguredTrust(s.projection, h.configured.desired)
	if err != nil {
		return S1ApplyResult{}, err
	}
	limit := s.configuration.Capacity.ImageBytes
	nonexpanding := configuredTrustNonexpanding(s.projection.snapshot, next.snapshot)
	if !nonexpanding {
		limit -= s.configuration.Capacity.RestrictiveImageBytes
	}
	if len(next.snapshot.image) > int(limit) {
		return S1ApplyResult{}, ErrControlReserve
	}
	advanced.projection = next
	// Configuration history lives in the authenticated projection and native
	// prefix; no fabricated person, ordinary outcome or enrollment is introduced.
	return S1ApplyResult{State: advanced, Status: S1Original}, nil
}

func configuredTrustBody(h configuredTrustHeader, op []byte, bounds s2cBounds) ([]byte, error) {
	header, err := json.Marshal(h)
	if err != nil || !bounds.valid() || len(op) == 0 || len(op) > MaxImageBytes+len(configuredOperationMagic) || uint64(len(header)) > bounds.HeaderBytes {
		return nil, errS2CHistorical
	}
	n := uint64(len(configuredTrustMagic)) + 8 + uint64(len(header)) + uint64(len(op)) + ed25519.SignatureSize
	if n > bounds.HistoricalBytes {
		return nil, errS2CHistorical
	}
	raw := make([]byte, 0, int(n))
	raw = append(raw, configuredTrustMagic...)
	raw = binary.BigEndian.AppendUint32(raw, uint32(len(header)))
	raw = append(raw, header...)
	raw = binary.BigEndian.AppendUint32(raw, uint32(len(op)))
	raw = append(raw, op...)
	return raw, nil
}

func verifyConfiguredTrustH(t *s2cTrust, raw []byte) (*s2cHistoricalH, error) {
	if t == nil || uint64(len(raw)) > t.bounds.HistoricalBytes || len(raw) < len(configuredTrustMagic)+8+ed25519.SignatureSize || !bytes.HasPrefix(raw, []byte(configuredTrustMagic)) {
		return nil, errS2CHistorical
	}
	r := s2CapsuleReader{raw[len(configuredTrustMagic):]}
	header, err := r.record()
	if err != nil || uint64(len(header)) > t.bounds.HeaderBytes {
		return nil, errS2CHistorical
	}
	opRaw, err := r.record()
	if err != nil || len(opRaw) > MaxImageBytes+len(configuredOperationMagic) || len(r.remaining) != ed25519.SignatureSize {
		return nil, errS2CHistorical
	}
	var headerValue configuredTrustHeader
	if s2StrictJSON(header, &headerValue, DefaultPolicyLimits()) != nil {
		return nil, errS2CHistorical
	}
	h := headerValue
	d, known := t.origin(h.OriginID)
	capability := configuredTrustCapability(t, d)
	if !known || !verifyConfiguredEndorsements(t, configuredStatement(h), h.Endorsements) || !verifyConfiguredEndorsementWindow(h.Endorsements, h.ConsumeLow, h.ConsumeHigh) || capability == [32]byte{} || h.Version != configuredTrustVersion || h.Scope != t.scope || h.OriginDigest != t.originDigests[d.ID] || h.Incarnation != d.Incarnation || h.ID.Namespace != d.Namespace || h.Operation != sha256.Sum256(opRaw) || !ed25519.Verify(ed25519.PublicKey(d.PublicKey[:]), raw[:len(raw)-ed25519.SignatureSize], r.remaining) {
		return nil, errS2CHistorical
	}
	op := OperationIdentity{reviewed: h.Reviewed, canonical: string(opRaw), digest: h.Operation}
	desired, err := decodeConfiguredOperation(op)
	if err != nil || desired.Digest() != h.Desired {
		return nil, errS2CHistorical
	}
	handoff := &S1Handoff{id: h.ID, operation: op, origin: h.OriginDigest, serial: h.Serial, configured: &authorityConfiguredAuthorization{desired: desired, predecessor: h.Predecessor, slot: h.Slot, capability: capability}}
	if !handoff.valid(t.genesis.state.projection.cut) || handoff.digest() != h.Value {
		return nil, errS2CHistorical
	}
	return &s2cHistoricalH{scope: t.scope, raw: string(raw), handoff: handoff, originID: d.ID}, nil
}

// A configuration candidate certifies one exact prefix, so a different chosen
// value conclusively makes it inapplicable. Release only volatile pending
// credit after authenticated DRAINED; retain signed origin bytes and native
// history. Ordinary person change outcomes retain their existing semantics.
func (o *s2cParticipant) discardStaleConfiguredPendingLocked() {
	for digest, h := range o.pending {
		if h.handoff.configured != nil && h.handoff.configured.slot < o.replayState.slot {
			o.pendingBytes -= uint64(len(h.raw))
			delete(o.pending, digest)
		}
	}
}

// Issuer trust additions/edits use ordinary image capacity even when no current
// subject has grants. Only unchanged trust and pure disabling/withdrawal may
// use the existing revocation reserve.
func configuredTrustNonexpanding(before, after *Snapshot) bool {
	if !s1PermissionsSubset(before, after) {
		return false
	}
	for url, issuer := range after.issuers {
		old, known := before.issuers[url]
		if !known {
			return false
		}
		if reflect.DeepEqual(configuredIssuerSemantics(old), configuredIssuerSemantics(issuer)) {
			continue
		}
		if issuer.Enabled {
			return false
		}
		old.Enabled, old.Deleted = issuer.Enabled, issuer.Deleted
		if !reflect.DeepEqual(configuredIssuerSemantics(old), configuredIssuerSemantics(issuer)) {
			return false
		}
	}
	return true
}
