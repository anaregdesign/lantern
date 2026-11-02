package security

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"testing"
)

func TestLogPolicySupportCertificateAllFixedParticipants(t *testing.T) {
	for _, count := range []int{1, 3, 31} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			fixture, original, profile, voters, origins := logPolicyCertificateFixture(t, count)
			raw, err := encodeLogPolicySupportCertificate(fixture.trust, profile, original, voters, origins)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := decodeLogPolicySupportCertificate(fixture.trust, profile, original, raw)
			if err != nil || decoded.statement.Original == [32]byte{} || decoded.raw != string(raw) || len(decoded.voters) != count || len(decoded.origins) != count {
				t.Fatal("complete fixed-cohort certificate rejected", err)
			}
			bound, err := logPolicySupportCertificateBound(fixture.trust, profile)
			if err != nil || bound < uint64(len(raw)) {
				t.Fatal("certificate exceeded preflight bound", bound, len(raw), err)
			}
			retained := decoded.raw
			raw[0] ^= 1
			if decoded.raw != retained {
				t.Fatal("verified proof retained caller bytes")
			}
			for _, absent := range []logPolicySupportCertificateWire{
				{decoded.statement, voters[:count-1], origins},
				{decoded.statement, voters, origins[:count-1]},
			} {
				if verifyLogPolicySupportCertificate(fixture.trust, decoded.statement, absent) {
					t.Fatal("missing fixed participant accepted as quorum support")
				}
			}
			t.Logf("fixed_voters=%d enrolled_origins=%d certificate_bytes=%d", count, count, len(retained))
		})
	}
}

func TestLogPolicySupportCertificateRejectsDifferentEvidence(t *testing.T) {
	fixture, original, profile, voters, origins := logPolicyCertificateFixture(t, 3)
	statement, err := newLogPolicySupportStatement(fixture.trust, profile, original)
	if err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*logPolicySupportCertificateWire){
		"contract version":               func(w *logPolicySupportCertificateWire) { w.Statement.Version++ },
		"contract digest":                func(w *logPolicySupportCertificateWire) { w.Statement.Contract[0] ^= 1 },
		"another original":               func(w *logPolicySupportCertificateWire) { w.Statement.Original[0] ^= 1 },
		"another profile":                func(w *logPolicySupportCertificateWire) { w.Statement.Profile.Membership[0] ^= 1 },
		"missing voter":                  func(w *logPolicySupportCertificateWire) { w.Voters = w.Voters[:2] },
		"missing origin":                 func(w *logPolicySupportCertificateWire) { w.Origins = w.Origins[:2] },
		"duplicate voter":                func(w *logPolicySupportCertificateWire) { w.Voters[1] = w.Voters[0] },
		"duplicate origin":               func(w *logPolicySupportCertificateWire) { w.Origins[1] = w.Origins[0] },
		"extra voter":                    func(w *logPolicySupportCertificateWire) { w.Voters = append(w.Voters, w.Voters[0]) },
		"wrong voter role":               func(w *logPolicySupportCertificateWire) { w.Voters[0].Role = logPolicySupportOrigin },
		"wrong origin role":              func(w *logPolicySupportCertificateWire) { w.Origins[0].Role = logPolicySupportVoter },
		"wrong voter":                    func(w *logPolicySupportCertificateWire) { w.Voters[0].Signer = 99 },
		"wrong origin":                   func(w *logPolicySupportCertificateWire) { w.Origins[0].Signer = 99 },
		"reordered voters":               func(w *logPolicySupportCertificateWire) { w.Voters[0], w.Voters[1] = w.Voters[1], w.Voters[0] },
		"reordered origins":              func(w *logPolicySupportCertificateWire) { w.Origins[0], w.Origins[1] = w.Origins[1], w.Origins[0] },
		"unsigned voter":                 func(w *logPolicySupportCertificateWire) { w.Voters[0].Signature = nil },
		"unsigned origin":                func(w *logPolicySupportCertificateWire) { w.Origins[0].Signature = nil },
		"voter signature used as origin": func(w *logPolicySupportCertificateWire) { w.Origins[0].Signature = w.Voters[0].Signature },
		"another floor":                  func(w *logPolicySupportCertificateWire) { w.Voters[0].Floor.Index++ },
		"zero floor":                     func(w *logPolicySupportCertificateWire) { w.Voters[0].Floor = s2cJournalFloor{} },
		"unfenced initial floor":         func(w *logPolicySupportCertificateWire) { w.Voters[0].Floor.Index = 2 },
		"different origin floor": func(w *logPolicySupportCertificateWire) {
			w.Origins[0].Floor.Index++
			_, originKeys := authorityTestFixture(t, 3)
			body, bodyErr := logPolicySupportAttestationBody(w.Statement, w.Origins[0])
			if bodyErr != nil {
				t.Fatal(bodyErr)
			}
			w.Origins[0].Signature = ed25519.Sign(originKeys[1], body)
		},
	} {
		t.Run(name, func(t *testing.T) {
			wire := logPolicySupportCertificateWire{statement, cloneLogPolicyAttestations(voters), cloneLogPolicyAttestations(origins)}
			edit(&wire)
			if verifyLogPolicySupportCertificate(fixture.trust, statement, wire) {
				t.Fatal("invalid evidence accepted")
			}
			raw, _ := json.Marshal(wire)
			if _, err := decodeLogPolicySupportCertificate(fixture.trust, profile, original, raw); err == nil {
				t.Fatal("untrusted evidence constructed verified certificate")
			}
		})
	}
}

func TestLogPolicySupportCertificateStrictRepresentation(t *testing.T) {
	fixture, original, profile, voters, origins := logPolicyCertificateFixture(t, 3)
	raw, err := encodeLogPolicySupportCertificate(fixture.trust, profile, original, voters, origins)
	if err != nil {
		t.Fatal(err)
	}
	for name, malformed := range map[string][]byte{
		"empty":                nil,
		"null":                 []byte("null"),
		"unknown field":        bytes.Replace(raw, []byte(`"Statement":`), []byte(`"unexpected":true,"Statement":`), 1),
		"duplicate field":      bytes.Replace(raw, []byte(`"Version":1`), []byte(`"Version":1,"Version":1`), 1),
		"unknown nested field": bytes.Replace(raw, []byte(`"Floor":{`), []byte(`"Floor":{"unexpected":true,`), 1),
		"noncanonical number":  bytes.Replace(raw, []byte(`"Version":1`), []byte(`"Version":1.0`), 1),
		"whitespace":           append([]byte(" "), raw...),
		"trailing":             append(bytes.Clone(raw), []byte("{}")...),
		"oversized":            bytes.Repeat([]byte(" "), logPolicySupportCertificateMax+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeLogPolicySupportCertificate(fixture.trust, profile, original, malformed); err == nil {
				t.Fatal("malformed certificate accepted")
			}
		})
	}
	bounded := *fixture.trust
	bounded.bounds.ProofBytes = uint64(len(raw) - 1)
	if _, err := encodeLogPolicySupportCertificate(&bounded, profile, original, voters, origins); err == nil {
		t.Fatal("configured proof capacity ignored by encoding")
	}
	if _, err := logPolicySupportCertificateBound(&bounded, profile); err == nil {
		t.Fatal("configured proof capacity ignored by preflight bound")
	}
	if _, err := decodeLogPolicySupportCertificate(&bounded, profile, original, raw); err == nil {
		t.Fatal("configured proof capacity ignored by decoding")
	}
}

func TestLogPolicySupportCertificateRequiresExactCurrentLogOriginal(t *testing.T) {
	fixture, original, profile, voters, origins := logPolicyCertificateFixture(t, 3)
	otherProfile := profile
	otherProfile.Membership[0] ^= 1
	if _, err := newLogPolicySupportStatement(fixture.trust, otherProfile, original); err == nil {
		t.Fatal("caller-selected workload profile replaced original")
	}
	header, op := authorityTestHeader(t, fixture)
	plainRaw := authorityTestSealHeader(t, fixture, authorityCertificateOriginKeys(t, 3)[1], header, op)
	plain, err := verifyAuthorityHistoricalH(fixture.trust, plainRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newLogPolicySupportStatement(fixture.trust, profile, plain); err == nil {
		t.Fatal("unrelated security original accepted as log adoption")
	}
	certificate, err := encodeLogPolicySupportCertificate(fixture.trust, profile, original, voters, origins)
	if err != nil {
		t.Fatal(err)
	}
	reader := s2CapsuleReader{[]byte(original.raw)[len(authorityHistoricalMagic):]}
	headerRaw, err := reader.record()
	if err != nil {
		t.Fatal(err)
	}
	var logHeader authorityHistoricalHeader
	if err := json.Unmarshal(headerRaw, &logHeader); err != nil {
		t.Fatal(err)
	}
	logCommand, err := original.handoff.operation.command()
	if err != nil {
		t.Fatal(err)
	}
	for _, differentIntent := range []bool{false, true} {
		nextHeader := logHeader
		nextOperation := original.handoff.operation
		if differentIntent {
			logCommand.Changes[0].LogPolicy.Level = LogLevelWarn
			nextOperation = s1Operation(t, fixture.genesis.state.projection, testIdentity(), logCommand)
			nextHeader.OperationDigest = nextOperation.digest
		} else {
			nextHeader.ID.Nonce[0]++
		}
		nextRaw := authorityTestSealHeader(t, fixture, authorityCertificateOriginKeys(t, 3)[1], nextHeader, nextOperation)
		nextOriginal, err := verifyAuthorityHistoricalH(fixture.trust, nextRaw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeLogPolicySupportCertificate(fixture.trust, profile, nextOriginal, certificate); err == nil {
			t.Fatal("support replayed against a different authentic ID or intent")
		}
	}
	if _, err := decodeLogPolicySupportCertificate(fixture.trust, profile, original, []byte(original.raw)); err == nil {
		t.Fatal("bare historical handoff accepted as support certificate")
	}
	changed := *original
	changed.raw += " "
	if _, err := encodeLogPolicySupportCertificate(fixture.trust, profile, &changed, voters, origins); err == nil {
		t.Fatal("unauthentic original accepted")
	}
	if _, err := decodeLogPolicySupportCertificate(nil, profile, original, []byte("{}")); err == nil {
		t.Fatal("missing independent trust accepted")
	}
	legacy := s2cTestCluster(t, 1)
	legacyOperation := s1Operation(t, legacy.genesis.state.projection, testIdentity(), s1Changes(Change{Kind: PutLogPolicy, LogPolicy: &LogPolicyChange{Version: LogPolicyVersion, Level: LogLevelInfo}}))
	legacyID := FullChangeID{S1Version, legacyOperation.reviewed.Domain, legacyOperation.reviewed.Cohort, 1, [16]byte{1}}
	legacyRaw := s2cTestSeal(t, legacy.trust, 1, legacy.keys[1], legacyID, legacyOperation, 1, false)
	legacyOriginal, err := verifyHistoricalH(legacy.trust, legacyRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logPolicyProfileForOriginal(legacy.trust, legacyOriginal); err == nil {
		t.Fatal("historical noncurrent-origin assertion accepted for adoption")
	}
}
