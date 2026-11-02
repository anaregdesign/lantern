package security

import (
	"bytes"
	"encoding/binary"
	"errors"
	"path/filepath"
	"testing"
)

func TestLogPolicyCandidatePreservesExactOriginal(t *testing.T) {
	fixture, original, profile, voters, origins := logPolicyCertificateFixture(t, 3)
	certificate, err := encodeLogPolicySupportCertificate(fixture.trust, profile, original, voters, origins)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := encodeLogPolicyCandidate(fixture.trust, original, certificate)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.raw != original.raw || candidate.handoff.operation != original.handoff.operation || candidate.digest() != original.digest() || candidate.candidate == "" || original.candidate != "" {
		t.Fatal("support proof rewrote or aliased the original operation")
	}
	if historicalWire(nil) != "" || historicalWire(original) != original.raw || historicalWire(candidate) != candidate.candidate {
		t.Fatal("bare original and vote-ready candidate were conflated")
	}
	retained := candidate.candidate
	certificate[0] ^= 1
	if candidate.candidate != retained {
		t.Fatal("candidate aliases caller certificate bytes")
	}
	decoded, err := verifyLogPolicyCandidate(fixture.trust, []byte(retained))
	if err != nil || decoded.raw != original.raw || decoded.digest() != original.digest() {
		t.Fatal("separate proof envelope changed original identity", err)
	}
	if _, err := encodeLogPolicyCandidate(fixture.trust, candidate, []byte("{}")); err == nil {
		t.Fatal("nested candidate accepted for encoding")
	}
}

func TestLogPolicyCandidateRejectsMalformedProofEnvelopes(t *testing.T) {
	fixture, original, profile, voters, origins := logPolicyCertificateFixture(t, 1)
	certificate, err := encodeLogPolicySupportCertificate(fixture.trust, profile, original, voters, origins)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := encodeLogPolicyCandidate(fixture.trust, original, certificate)
	if err != nil {
		t.Fatal(err)
	}
	frame := func(inner, proof []byte) []byte {
		raw := append([]byte(logPolicyCandidateMagic), binary.BigEndian.AppendUint32(nil, uint32(len(inner)))...)
		raw = append(raw, inner...)
		raw = binary.BigEndian.AppendUint32(raw, uint32(len(proof)))
		return append(raw, proof...)
	}
	for name, raw := range map[string][]byte{
		"empty":                     nil,
		"bare H":                    []byte(original.raw),
		"truncated":                 []byte(valid.candidate[:len(valid.candidate)-1]),
		"trailing":                  append([]byte(valid.candidate), 0),
		"missing original":          frame(nil, certificate),
		"missing certificate":       frame([]byte(original.raw), nil),
		"empty certificate":         frame([]byte(original.raw), []byte("{}")),
		"unknown certificate field": frame([]byte(original.raw), bytes.Replace(certificate, []byte(`"Statement":`), []byte(`"unexpected":true,"Statement":`), 1)),
		"nested candidate":          frame([]byte(valid.candidate), certificate),
		"changed original":          frame(append([]byte(original.raw), ' '), certificate),
		"wrong magic":               append([]byte("unsupported"), []byte(valid.candidate)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := verifyLogPolicyCandidate(fixture.trust, raw); err == nil {
				t.Fatal("invalid envelope constructed a vote-ready candidate")
			}
		})
	}
	bounded := *fixture.trust
	bounded.bounds.HistoricalBytes = uint64(len(valid.candidate) - 1)
	if _, err := encodeLogPolicyCandidate(&bounded, original, certificate); err == nil {
		t.Fatal("candidate exceeded configured historical capacity")
	}
	if _, err := verifyLogPolicyCandidate(&bounded, []byte(valid.candidate)); err == nil {
		t.Fatal("oversized received candidate accepted")
	}
	bounded = *fixture.trust
	bounded.bounds.PayloadBytes = uint64(len(valid.candidate) - 1)
	if _, err := encodeLogPolicyCandidate(&bounded, original, certificate); err == nil {
		t.Fatal("candidate exceeded configured payload capacity")
	}
	if _, err := verifyLogPolicyCandidate(&bounded, []byte(valid.candidate)); err == nil {
		t.Fatal("received candidate exceeded configured payload capacity")
	}
	if _, err := verifyLogPolicyCandidate(nil, []byte(valid.candidate)); err == nil {
		t.Fatal("missing independent trust accepted")
	}
}

func TestLogPolicyCandidateRequiredOnlyForFirstAdoption(t *testing.T) {
	fixture, original, profile, voters, origins := logPolicyCertificateFixture(t, 1)
	state := fixture.genesis.state
	if err := requireLogPolicyCandidate(fixture.trust, state, original); !errors.Is(err, errLogPolicySupport) {
		t.Fatal("bare first-adoption H accepted", err)
	}
	certificate, err := encodeLogPolicySupportCertificate(fixture.trust, profile, original, voters, origins)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := encodeLogPolicyCandidate(fixture.trust, original, certificate)
	if err != nil || requireLogPolicyCandidate(fixture.trust, state, candidate) != nil {
		t.Fatal("qualified first adoption rejected", err)
	}
	if err := requireLogPolicyCandidate(fixture.trust, nil, candidate); !errors.Is(err, ErrS1Contract) {
		t.Fatal("missing predecessor accepted", err)
	}
	if err := requireLogPolicyCandidate(fixture.trust, state, nil); err != nil {
		t.Fatal("ordinary NOOP acquired an activation requirement", err)
	}
	changed := *candidate
	changed.raw += " "
	if err := requireLogPolicyCandidate(fixture.trust, state, &changed); err == nil {
		t.Fatal("certificate detached from its original")
	}
	assessment, err := AssessS1(state.projection, original.handoff.operation)
	if err != nil {
		t.Fatal(err)
	}
	alreadyAdopted := *state
	alreadyAdopted.projection = assessment.Next
	if err := requireLogPolicyCandidate(fixture.trust, &alreadyAdopted, original); err != nil {
		t.Fatal("existing managed ownership required a new cohort adoption", err)
	}
}

func TestLogPolicyCandidateBareHRefusedBeforePreviewOrBallot(t *testing.T) {
	fixture, original, profile, voters, origins := logPolicyCertificateFixture(t, 1)
	config := s2cTestConfig(t, fixture, 1, filepath.Join(t.TempDir(), "participant"))
	participant, err := createS2CParticipant(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := participant.Close(); err != nil {
			t.Error(err)
		}
	})
	state, receipt, err := participant.b.ReadLocalCut()
	if err != nil {
		t.Fatal(err)
	}
	commit := s2cLogicalCommit(fixture.trust, state.slot+1, original.digest())
	if _, err := s2cPreviewCandidate(fixture.trust, state, fixture.genesis.roots, config.BScope, receipt, original, commit); !errors.Is(err, errLogPolicySupport) {
		t.Fatal("preview accepted bare first-adoption original", err)
	}
	participant.origins[original.digest()], participant.pending[original.digest()] = original, original
	participant.pendingBytes = uint64(len(original.raw))
	before := participant.p.floor
	if _, err := participant.Begin(original.digest()); !errors.Is(err, errLogPolicySupport) || participant.p.floor != before {
		t.Fatal("bare first adoption crossed the ballot durability boundary", err)
	}
	certificate, err := encodeLogPolicySupportCertificate(fixture.trust, profile, original, voters, origins)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := encodeLogPolicyCandidate(fixture.trust, original, certificate)
	if err != nil {
		t.Fatal(err)
	}
	// This positive codec/ballot fixture supplies the certificate's exact local
	// floor. The native paired tests separately establish actual floor durability.
	participant.logPolicyFloor = voters[0].Floor
	if err := participant.installLogPolicyCandidateLocked([]byte(candidate.candidate)); err != nil {
		t.Fatal(err)
	}
	if participant.origins[original.digest()].raw != original.raw || participant.pendingBytes != uint64(len(candidate.candidate)) {
		t.Fatal("proof installation lost original identity or exact pending charge")
	}
	if err := participant.installLogPolicyCandidateLocked([]byte(candidate.candidate)); err != nil || participant.pendingBytes != uint64(len(candidate.candidate)) {
		t.Fatal("same proof installation was not idempotent", err)
	}
	if _, err := participant.Begin(original.digest()); err != nil {
		t.Fatal("verified candidate could not enter the ordinary ballot path", err)
	}
}

func TestLogPolicyCandidatePhaseOneKeepsSupportWitness(t *testing.T) {
	fixture, original, profile, voters, origins := logPolicyCertificateFixture(t, 3)
	certificate, err := encodeLogPolicySupportCertificate(fixture.trust, profile, original, voters, origins)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := encodeLogPolicyCandidate(fixture.trust, original, certificate)
	if err != nil {
		t.Fatal(err)
	}
	prepare, err := s2cSignPrepare(fixture.trust, fixture.keys[1], 1, 1, fixture.genesis.state.prefix, s2cBallot{2, 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, retained := range []bool{false, true} {
		var promises []*s2cMessage
		for _, id := range []uint32{1, 2} {
			accepted, historical, admission := s2cBallot{}, (*s2cHistoricalH)(nil), [32]byte{}
			if retained {
				accepted, historical, admission = s2cBallot{1, 1}, candidate, [32]byte{7}
			}
			promise, err := s2cSignPromise(fixture.trust, fixture.keys[id], id, prepare, accepted, historical, admission)
			if err != nil {
				t.Fatal(err)
			}
			promises = append(promises, promise)
		}
		_, selected, err := s2cSelectP1(fixture.trust, promises, candidate)
		if err != nil || selected == nil || selected.candidate != candidate.candidate || selected.raw != original.raw || selected.digest() != original.digest() {
			t.Fatal("phase one discarded support proof or changed original identity", retained, err)
		}
		if err := requireLogPolicyCandidate(fixture.trust, fixture.genesis.state, selected); err != nil {
			t.Fatal("selected adoption lost verified support", err)
		}
	}
}

func TestLogPolicyCandidateCannotMasqueradeAsOriginRecord(t *testing.T) {
	fixture, original, profile, voters, origins := logPolicyCertificateFixture(t, 3)
	proof, err := encodeLogPolicySupportCertificate(fixture.trust, profile, original, voters, origins)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := encodeLogPolicyCandidate(fixture.trust, original, proof)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := createS2CParticipant(s2cTestConfig(t, fixture, 1, filepath.Join(t.TempDir(), "origin")))
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := owner.replayRecord(s2cPRecord{kind: s2cPOrigin, index: 3, raw: candidate.candidate}); err == nil {
		t.Fatal("supported envelope entered bare origin replay grammar")
	}
	relay, err := createS2CParticipant(s2cTestConfig(t, fixture, 2, filepath.Join(t.TempDir(), "relay")))
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	before := relay.pendingBytes
	if _, err := relay.forwardedHistorical(candidate.candidate); err == nil || relay.pendingBytes != before {
		t.Fatal("support bytes bypassed separate candidate ordering/accounting")
	}
}
