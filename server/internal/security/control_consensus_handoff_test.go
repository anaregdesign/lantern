package security

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"reflect"
	"testing"
	"time"
)

func TestS2CHistoricalRoundtrip(t *testing.T) {
	f := s2cTestCluster(t, 3)
	op := s1Operation(t, f.genesis.state.projection, testIdentity(), s1Changes(s1ReaderRole()))
	id := FullChangeID{S1Version, op.reviewed.Domain, op.reviewed.Cohort, 1, [16]byte{1}}
	for _, purpose := range []bool{false, true} {
		raw := s2cTestSeal(t, f.trust, 1, f.keys[1], id, op, 17, purpose)
		h, err := verifyHistoricalH(f.trust, raw)
		if err != nil || h.originID != 1 || h.scope != f.trust.scope || h.handoff.id != id || h.handoff.serial != 17 || h.handoff.operation != op || !bytes.Equal([]byte(h.raw), raw) {
			t.Fatal("roundtrip", err)
		}
		header, decoded := s2cTestHistoricalParts(t, raw)
		if decoded != op || h.digest() != header.Value || h.handoff.authorization.consume != header.Consume || h.handoff.authorization.credentialDeadline != header.CredentialDeadline || h.handoff.authorization.purposeDeadline != header.PurposeDeadline || h.handoff.authorization.authentication != header.Authentication || h.handoff.authorization.lineage != header.Lineage || h.handoff.authorization.credentialEvidence != header.CredentialEvidence || h.handoff.authorization.purposeEvidence != header.PurposeEvidence || h.handoff.authorization.purposeBinding != header.PurposeBinding {
			t.Fatal("lost historical field")
		}
		// Caller buffers cannot alter an issued exact historical capability.
		raw[0] ^= 1
		if h.raw[0] != s2cHistoricalMagic[0] {
			t.Fatal("borrowed input")
		}
	}
	// Verification deliberately has no clock input; the original fixture is
	// expired, and ordinary authentication is 24 hours old without a recency gate.
}

func TestS2CHistoricalTampering(t *testing.T) {
	f := s2cTestCluster(t, 3)
	op := s1Operation(t, f.genesis.state.projection, testIdentity(), s1Changes(s1ReaderRole()))
	id := FullChangeID{S1Version, op.reviewed.Domain, op.reviewed.Cohort, 1, [16]byte{1}}
	raw := s2cTestSeal(t, f.trust, 1, f.keys[1], id, op, 1, true)
	h, _ := s2cTestHistoricalParts(t, raw)
	// Enumerate every leaf, including each full S1 field and every credential /
	// purpose assertion. Preserve the original signature for every mutation.
	var mutate func(reflect.Value, string)
	mutate = func(v reflect.Value, path string) {
		if v.Type() == reflect.TypeOf(time.Time{}) {
			original := v.Interface().(time.Time)
			v.Set(reflect.ValueOf(original.Add(time.Nanosecond)))
			checkHistoricalTamper(t, f.trust, h, op, raw, path)
			v.Set(reflect.ValueOf(original))
			return
		}
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				mutate(v.Field(i), path+"/"+v.Type().Field(i).Name)
			}
		case reflect.Pointer:
			if !v.IsNil() {
				mutate(v.Elem(), path)
			}
		case reflect.Array:
			original := v.Index(0).Uint()
			v.Index(0).SetUint(original ^ 1)
			checkHistoricalTamper(t, f.trust, h, op, raw, path)
			v.Index(0).SetUint(original)
		case reflect.Uint16, reflect.Uint32, reflect.Uint64:
			original := v.Uint()
			v.SetUint(original ^ 1)
			checkHistoricalTamper(t, f.trust, h, op, raw, path)
			v.SetUint(original)
		case reflect.String:
			original := v.String()
			v.SetString(original + "x")
			checkHistoricalTamper(t, f.trust, h, op, raw, path)
			v.SetString(original)
		case reflect.Bool:
			original := v.Bool()
			v.SetBool(!original)
			checkHistoricalTamper(t, f.trust, h, op, raw, path)
			v.SetBool(original)
		default:
			t.Fatalf("uncovered leaf %s %s", path, v.Kind())
		}
	}
	mutate(reflect.ValueOf(&h).Elem(), "H")
	for _, offset := range []int{0, len(s2cHistoricalMagic), len(raw) - ed25519.SignatureSize - 1, len(raw) - 1} {
		bad := append([]byte(nil), raw...)
		bad[offset] ^= 1
		if _, err := verifyHistoricalH(f.trust, bad); err == nil {
			t.Fatalf("byte tamper %d", offset)
		}
	}
}

func checkHistoricalTamper(t *testing.T, trust *s2cTrust, h s2cHistoricalHeader, op OperationIdentity, original []byte, path string) {
	t.Helper()
	body, err := s2cHistoricalBody(h, op.Encode(), trust.bounds)
	if err != nil {
		return
	}
	body = append(body, original[len(original)-ed25519.SignatureSize:]...)
	if _, err = verifyHistoricalH(trust, body); err == nil {
		t.Fatalf("accepted changed %s", path)
	}
}

func TestS2CHistoricalSignedInvalidClaims(t *testing.T) {
	f := s2cTestCluster(t, 3)
	op := s1Operation(t, f.genesis.state.projection, testIdentity(), s1Changes(s1ReaderRole()))
	id := FullChangeID{S1Version, op.reviewed.Domain, op.reviewed.Cohort, 1, [16]byte{1}}
	raw := s2cTestSeal(t, f.trust, 1, f.keys[1], id, op, 1, true)
	base, _ := s2cTestHistoricalParts(t, raw)
	cases := map[string]func(*s2cHistoricalHeader){
		"unknown origin":  func(h *s2cHistoricalHeader) { h.OriginID = 999 },
		"wrong scope":     func(h *s2cHistoricalHeader) { h.Scope[0] ^= 1 },
		"incarnation":     func(h *s2cHistoricalHeader) { h.Incarnation[0] ^= 1 },
		"profile":         func(h *s2cHistoricalHeader) { h.Credential.Profile.ConsumeContract[0] ^= 1 },
		"machine":         func(h *s2cHistoricalHeader) { h.Authentication.Class = MachineActor },
		"unresolved":      func(h *s2cHistoricalHeader) { h.Authentication.Class = UnresolvedActor },
		"native":          func(h *s2cHistoricalHeader) { h.Authentication.Provenance = NativeMachine },
		"issuer revision": func(h *s2cHistoricalHeader) { h.Authentication.IssuerConfigRevision = 0 },
		"lineage":         func(h *s2cHistoricalHeader) { h.Lineage = 0 },
		"credential exclusive": func(h *s2cHistoricalHeader) {
			h.CredentialDeadline = h.Consume
			h.Credential.CredentialExpiresAt = h.Consume
			h.Credential.AdmissionDeadline = h.Consume
		},
		"purpose exclusive":    func(h *s2cHistoricalHeader) { h.Purpose.Deadline = h.Consume; h.PurposeDeadline = h.Consume },
		"not before":           func(h *s2cHistoricalHeader) { h.Credential.CredentialNotBefore = h.Consume.Add(time.Nanosecond) },
		"purpose actor":        func(h *s2cHistoricalHeader) { h.Purpose.Actor.Subject = "mallory" },
		"purpose ID":           func(h *s2cHistoricalHeader) { h.Purpose.ID.Nonce[0]++ },
		"purpose serial":       func(h *s2cHistoricalHeader) { h.Purpose.Serial++ },
		"purpose review":       func(h *s2cHistoricalHeader) { h.Purpose.Reviewed.Sequence++ },
		"purpose operation":    func(h *s2cHistoricalHeader) { h.Purpose.OperationDigest[0]++ },
		"purpose timing":       func(h *s2cHistoricalHeader) { h.Purpose.AuthenticationTime = h.Purpose.NotBefore.Add(-time.Nanosecond) },
		"present absent drift": func(h *s2cHistoricalHeader) { h.Purpose = nil },
		"nonUTC":               func(h *s2cHistoricalHeader) { h.Consume = h.Consume.In(time.FixedZone("offset", 3600)) },
		"missing enrollment":   func(h *s2cHistoricalHeader) { h.Credential.EnrollmentDigest = [32]byte{} },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			h := base
			p := *base.Purpose
			h.Purpose = &p
			edit(&h)
			bad := s2cTestSignHistorical(t, f.trust, f.keys[1], h, op)
			if _, err := verifyHistoricalH(f.trust, bad); err == nil {
				t.Fatal("invalid signed claims accepted")
			}
		})
	}
	if _, err := verifyHistoricalH(f.trust, s2cTestSignHistorical(t, f.trust, f.keys[2], base, op)); err == nil {
		t.Fatal("self signed origin accepted")
	}
	// Historical capabilities have no issuer/admission method. Reusing all old
	// evidence while changing any identity/operation/serial/consume invalidates
	// the signature; no production API can generate a replacement signature.
}

func TestS2CHistoricalCanonicalBounds(t *testing.T) {
	f := s2cTestCluster(t, 3)
	op := s1Operation(t, f.genesis.state.projection, testIdentity(), s1Changes(s1ReaderRole()))
	id := FullChangeID{S1Version, op.reviewed.Domain, op.reviewed.Cohort, 1, [16]byte{1}}
	raw := s2cTestSeal(t, f.trust, 1, f.keys[1], id, op, 1, false)
	for _, n := range []int{0, 1, len(s2cHistoricalMagic), len(raw) - 1} {
		if _, err := verifyHistoricalH(f.trust, raw[:n]); err == nil {
			t.Fatalf("truncation %d", n)
		}
	}
	if _, err := verifyHistoricalH(f.trust, append(append([]byte(nil), raw...), 0)); err == nil {
		t.Fatal("trailing bytes")
	}
	bad := append([]byte(nil), raw...)
	binary.BigEndian.PutUint32(bad[len(s2cHistoricalMagic):], ^uint32(0))
	if _, err := verifyHistoricalH(f.trust, bad); err == nil {
		t.Fatal("overflow header")
	}
	copyTrust := *f.trust
	copyTrust.bounds.HistoricalBytes = uint64(len(raw)) - 1
	if _, err := verifyHistoricalH(&copyTrust, raw); err == nil {
		t.Fatal("whole H bound")
	}
	// Change canonical JSON while honestly resigning: alternate whitespace and
	// a duplicate key remain invalid despite a valid configured-origin signature.
	r := s2CapsuleReader{raw[len(s2cHistoricalMagic):]}
	header, _ := r.record()
	operation, _ := r.record()
	for _, header := range [][]byte{append([]byte(" "), header...), append([]byte(`{"Version":1,`), header[1:]...)} {
		b := []byte(s2cHistoricalMagic)
		b = binary.BigEndian.AppendUint32(b, uint32(len(header)))
		b = append(b, header...)
		b = binary.BigEndian.AppendUint32(b, uint32(len(operation)))
		b = append(b, operation...)
		b = append(b, ed25519.Sign(f.keys[1], b)...)
		if _, err := verifyHistoricalH(f.trust, b); err == nil {
			t.Fatal("noncanonical header")
		}
	}
}

func TestS2CHistoricalBrowserAndDeadlineClaims(t *testing.T) {
	f := s2cTestCluster(t, 3)
	op := s1Operation(t, f.genesis.state.projection, testIdentity(), s1Changes(s1ReaderRole()))
	h := s1Seal(f.genesis.state.projection, op, 1, false)
	h.authorization.authentication.Provenance = BrowserCode
	for _, session := range []string{"", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"} {
		h.authorization.authentication.SessionDigest = session
		raw := s2cTestSealH(t, f.trust, 1, f.keys[1], h)
		if _, err := verifyHistoricalH(f.trust, raw); err != nil {
			t.Fatal("browser", err)
		}
		header, _ := s2cTestHistoricalParts(t, raw)
		if session != "" {
			header.Credential.SessionExpiresAt = header.Consume
			header.CredentialDeadline = header.Consume
			if _, err := verifyHistoricalH(f.trust, s2cTestSignHistorical(t, f.trust, f.keys[1], header, op)); err == nil {
				t.Fatal("session deadline must be exclusive")
			}
		}
	}
	raw := s2cTestSealH(t, f.trust, 1, f.keys[1], h)
	b := s2cBootstrap{f.genesis, f.members, f.origins, s2cTestBounds()}
	b.Bounds.HeaderBytes--
	other, err := newS2CTrust(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = verifyHistoricalH(other, raw); err == nil {
		t.Fatal("different common configuration")
	}
}
