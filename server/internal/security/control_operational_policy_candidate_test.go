package security

import (
	"bytes"
	"encoding/binary"
	"errors"
	"path/filepath"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

func TestOperationalPolicyCandidatePreservesExactOriginal(t *testing.T) {
	for _, group := range operationalpolicy.Groups() {
		t.Run(string(group), func(t *testing.T) {
			f, original, candidate := operationalPolicyTestCandidate(t, group, 3)
			if candidate.raw != original.raw || candidate.handoff.operation != original.handoff.operation || candidate.digest() != original.digest() || candidate.candidate == "" || original.candidate != "" {
				t.Fatal("certificate rewrote the immutable business original")
			}
			if historicalWire(original) != original.raw || historicalWire(candidate) != candidate.candidate {
				t.Fatal("original and vote-ready envelope conflated")
			}
			verified, err := verifyHistoricalH(f.trust, []byte(candidate.candidate))
			if err != nil || verified.raw != original.raw || verified.digest() != original.digest() {
				t.Fatal("ordinary historical dispatch lost operational envelope", err)
			}
			if _, err := encodeOperationalPolicyCandidate(f.trust, candidate, []byte("{}")); err == nil {
				t.Fatal("nested envelope accepted")
			}
			if err := requireOperationalPolicyCandidate(f.trust, f.genesis.state, original); !errors.Is(err, errOperationalPolicySupport) {
				t.Fatal("bare first adoption accepted", err)
			}
			if err := requireOperationalPolicyCandidate(f.trust, f.genesis.state, candidate); err != nil {
				t.Fatal("qualified adoption rejected", err)
			}
			changed := *candidate
			changed.raw += " "
			if requireOperationalPolicyCandidate(f.trust, f.genesis.state, &changed) == nil {
				t.Fatal("proof detached from immutable original accepted")
			}
		})
	}
	if requireOperationalPolicyCandidate(nil, nil, nil) != ErrS1Contract {
		t.Fatal("missing predecessor accepted")
	}
}

func TestOperationalPolicyCandidateStrictEnvelope(t *testing.T) {
	f, original, profile, voters, origins := operationalPolicyCertificateFixture(t, operationalpolicy.GroupSearch, 1)
	certificate, err := encodeOperationalPolicySupportCertificate(f.trust, profile, original, voters, origins)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := encodeOperationalPolicyCandidate(f.trust, original, certificate)
	if err != nil {
		t.Fatal(err)
	}
	frame := func(inner, proof []byte) []byte {
		raw := append([]byte(operationalPolicyCandidateMagic), binary.BigEndian.AppendUint32(nil, uint32(len(inner)))...)
		raw = append(raw, inner...)
		raw = binary.BigEndian.AppendUint32(raw, uint32(len(proof)))
		return append(raw, proof...)
	}
	for name, raw := range map[string][]byte{
		"empty": nil, "bare H": []byte(original.raw), "truncated": []byte(valid.candidate[:len(valid.candidate)-1]),
		"trailing": append([]byte(valid.candidate), 0), "missing original": frame(nil, certificate),
		"missing certificate": frame([]byte(original.raw), nil), "empty certificate": frame([]byte(original.raw), []byte("{}")),
		"unknown certificate": frame([]byte(original.raw), bytes.Replace(certificate, []byte(`"Statement":`), []byte(`"unknown":true,"Statement":`), 1)),
		"nested":              frame([]byte(valid.candidate), certificate),
		"unknown version":     bytes.Replace([]byte(valid.candidate), []byte(operationalPolicyCandidateMagic), []byte("lantern/security/operational-policy/candidate\x00\x09"), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := verifyOperationalPolicyCandidate(f.trust, raw); err == nil {
				t.Fatal("malformed candidate accepted")
			}
		})
	}
	if _, err := verifyOperationalPolicyCandidate(nil, []byte(valid.candidate)); err == nil {
		t.Fatal("missing independent trust accepted")
	}
	tight := *f.trust
	tight.bounds.HistoricalBytes = uint64(len(valid.candidate) - 1)
	if _, err := verifyOperationalPolicyCandidate(&tight, []byte(valid.candidate)); err == nil {
		t.Fatal("configured history bound ignored")
	}
}

func TestOperationalPolicyCandidateSearchEveryUpdate(t *testing.T) {
	for _, group := range []operationalpolicy.Group{operationalpolicy.GroupSlowRPC, operationalpolicy.GroupSearch} {
		t.Run(string(group), func(t *testing.T) {
			f, original, _ := operationalPolicyTestCandidate(t, group, 1)
			assessment, err := AssessS1(f.genesis.state.projection, original.handoff.operation)
			if err != nil {
				t.Fatal(err)
			}
			after := *f.genesis.state
			after.projection = assessment.Next
			change, _, _ := operationalPolicyChangeForH(original.handoff)
			change.ExpectedGeneration = 1
			command := s1Changes(Change{Kind: PutOperationalPolicy, OperationalPolicy: &change})
			op := s1Operation(t, after.projection, testIdentity(), command)
			// This tests the pre-certificate selection predicate only. No modified
			// unsigned operation is passed as a certificate or durable original.
			next := *original
			handoff := *original.handoff
			next.handoff = &handoff
			handoff.operation = op
			needs := operationalPolicySupportRequired(&after, &next)
			if needs != (group == operationalpolicy.GroupSearch) {
				t.Fatal("Search updates did not retain all-baseline support requirement", needs)
			}
			if needs && !errors.Is(requireOperationalPolicyCandidate(f.trust, &after, &next), errOperationalPolicySupport) {
				t.Fatal("bare Search update accepted")
			}
		})
	}
}

func TestOperationalPolicyCandidateBareHRefusedBeforeBallot(t *testing.T) {
	f, original, profile, voters, origins := operationalPolicyCertificateFixture(t, operationalpolicy.GroupSlowRPC, 1)
	k, err := createS2CParticipant(s2cTestConfig(t, f, 1, filepath.Join(t.TempDir(), "native")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := k.Close(); err != nil {
			t.Error(err)
		}
	}()
	k.origins[original.digest()], k.pending[original.digest()] = original, original
	k.pendingBytes = uint64(len(original.raw))
	before := k.p.floor
	if _, err := k.Begin(original.digest()); !errors.Is(err, errOperationalPolicySupport) || k.p.floor != before {
		t.Fatal("bare adoption crossed ballot durability boundary", err)
	}
	proof, err := encodeOperationalPolicySupportCertificate(f.trust, profile, original, voters, origins)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := encodeOperationalPolicyCandidate(f.trust, original, proof)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.installOperationalPolicyCandidateLocked([]byte(candidate.candidate)); err == nil {
		t.Fatal("candidate installed without local durable floor")
	}
	// Codec/ballot positive fixture supplies the certificate's exact local
	// floor; the separate floor tests establish actual on-disk durability.
	k.operationalFloors[operationalpolicy.GroupSlowRPC] = &operationalPolicyFloorState{floor: voters[0].Floor}
	if err := k.installOperationalPolicyCandidateLocked([]byte(candidate.candidate)); err != nil {
		t.Fatal(err)
	}
	if k.origins[original.digest()].raw != original.raw || k.pendingBytes != uint64(len(candidate.candidate)) {
		t.Fatal("candidate changed origin identity or pending charge")
	}
	if err := k.installOperationalPolicyCandidateLocked([]byte(candidate.candidate)); err != nil || k.pendingBytes != uint64(len(candidate.candidate)) {
		t.Fatal("candidate installation not idempotent", err)
	}
	if _, err := k.Begin(original.digest()); err != nil {
		t.Fatal("qualified candidate could not enter ballot", err)
	}
}
