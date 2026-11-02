package security

import (
	"bytes"
	"errors"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"path/filepath"
	"testing"
)

func TestLogPolicyFloorDurableOneTimeAndCompleteReplay(t *testing.T) {
	f, h, _, _, _ := logPolicyCertificateFixture(t, 1)
	c := s2cTestConfig(t, f, 1, filepath.Join(t.TempDir(), "native"))
	k, err := createS2CParticipant(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = k.ensureLogPolicyFloorLocked(h, k.credit); err != nil {
		t.Fatal(err)
	}
	floor := k.logPolicyFloor
	if floor.Index != 3 || floor.Chain == [32]byte{} || k.logPolicyFloorOriginal != h.raw {
		t.Fatal("floor was not synchronized and retained")
	}
	if err = k.ensureLogPolicyFloorLocked(h, k.credit); err != nil || k.p.count != 3 || k.logPolicyFloor != floor {
		t.Fatal("one-time floor duplicated", err)
	}
	raw, err := logPolicyFloorBytes(h)
	if err != nil {
		t.Fatal(err)
	}
	record, err := k.encodeRecord(s2cPRecord{kind: s2cPLogPolicyFloor, index: 3, raw: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	// The preceding current-v2 reader accepted kinds1..10 only. Keep that
	// exact closed discriminator in this regression; no torn-tail repair is
	// authorized for a complete record with a new application kind.
	if s2cPForwardedH != 10 || record[len(authorityPMagic)] <= s2cPForwardedH {
		t.Fatal("minimum-version marker is readable by the preceding closed grammar")
	}
	legacy := s2cTestCluster(t, 1)
	old := s2cRecordTestOwner(t, legacy, 0)
	if _, err = old.decodeRecord(record, 3); err == nil {
		t.Fatal("historical grammar accepted current log floor")
	}
	pf, bf, err := k.Floors()
	if err != nil {
		t.Fatal(err)
	}
	if err = k.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := resumeS2CParticipant(c, pf, bf)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.logPolicyFloor != floor || !bytes.Equal([]byte(restored.logPolicyFloorOriginal), []byte(h.raw)) {
		t.Fatal("binary original or durable floor changed during recovery")
	}
	if err = restored.replayLogPolicyFloor(s2cPRecord{kind: s2cPLogPolicyFloor, index: 4, raw: string(raw)}); !errors.Is(err, errS2CProtocol) {
		t.Fatal("duplicate floor accepted", err)
	}
}

func TestLogPolicyFloorCorruptPersistedMarkerRefusesRestore(t *testing.T) {
	f, h, _, _, _ := logPolicyCertificateFixture(t, 1)
	config := s2cTestConfig(t, f, 1, filepath.Join(t.TempDir(), "native"))
	k, err := createS2CParticipant(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = k.ensureLogPolicyFloorLocked(h, k.credit); err != nil {
		t.Fatal(err)
	}
	pf, bf, err := k.Floors()
	if err != nil {
		t.Fatal(err)
	}
	body, err := logPolicyFloorBytes(h)
	if err != nil {
		t.Fatal(err)
	}
	body = bytes.Replace(body, []byte(`"Version":1`), []byte(`"Version":9`), 1)
	bad, err := k.encodeRecord(s2cPRecord{kind: s2cPLogPolicyFloor, index: pf.Index, raw: string(body)})
	if err != nil {
		t.Fatal(err)
	}
	if err = k.Close(); err != nil {
		t.Fatal(err)
	}
	s2RestoreTestReplaceFinalPayload(t, config.PPath, bad)
	// Supply even the physically updated tip. The unknown typed floor must
	// still fail semantic replay rather than be ignored as an optional tail.
	cut, err := mutationlog.InspectFileWALCut(config.PPath, pf.Index, s2cDecodeJournalPayload, func(mutationlog.Entry) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	pf.Chain = cut.ChainSHA256
	before := s2RestoreTestReadFile(t, config.PPath)
	if restored, err := resumeS2CParticipant(config, pf, bf); err == nil {
		_ = restored.Close()
		t.Fatal("unknown persisted floor was ignored")
	}
	if !bytes.Equal(before, s2RestoreTestReadFile(t, config.PPath)) {
		t.Fatal("invalid marker suffix was truncated")
	}
}

func TestLogPolicyFloorEncodingBound(t *testing.T) {
	_, h, _, _, _ := logPolicyCertificateFixture(t, 1)
	raw, err := logPolicyFloorBytes(h)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := logPolicyFloorSizeBound(uint64(len(h.raw)))
	if err != nil || bound != uint64(len(raw)) {
		t.Fatal("base64 floor accounting differs from actual persisted bytes", bound, len(raw), err)
	}
	if _, err := logPolicyFloorSizeBound(uint64(MaxImageBytes)*2 + 1); err == nil {
		t.Fatal("unbounded original size accepted")
	}
}

func TestLogPolicyFloorPreparationRefBoundToOriginalAndDetached(t *testing.T) {
	f, h, _, _, _ := logPolicyCertificateFixture(t, 1)
	config := s2cTestConfig(t, f, 1, filepath.Join(t.TempDir(), "native"))
	k, err := createS2CParticipant(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = k.ensureLogPolicyFloorLocked(h, k.credit); err != nil {
		t.Fatal(err)
	}
	projection := k.logPolicyProjectionLocked()
	if projection.Policy != nil || !projection.VersionFloor || projection.Preparation == nil {
		t.Fatal("preparation claimed a committed policy or omitted irreversible floor")
	}
	expected := LogPolicyPreparation{ChangeID: h.handoff.id, IntentDigest: h.handoff.operation.digest, ActorDigest: s2cHash("public-original-actor", h.handoff.operation.actor)}
	if *projection.Preparation != expected {
		t.Fatal("preparation reference not bound to reviewed original")
	}
	projection.Preparation.IntentDigest[0] ^= 1
	projection.Preparation.ActorDigest[0] ^= 1
	projection.Preparation.ChangeID.Nonce[0] ^= 1
	if *k.logPolicyProjectionLocked().Preparation != expected {
		t.Fatal("preparation reference aliases local state")
	}
	pf, bf, err := k.Floors()
	if err != nil {
		t.Fatal(err)
	}
	if err = k.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := resumeS2CParticipant(config, pf, bf)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if *resumed.logPolicyProjectionLocked().Preparation != expected {
		t.Fatal("restart lost original actor/ID/intent preparation link")
	}
}

func TestLogPolicyFloorRequiresOperationPurposeBeforeSideEffect(t *testing.T) {
	f, keys := authorityTestFixture(t, 1)
	header, _ := authorityTestHeader(t, f)
	bob := s1Bob()
	operation := s1Operation(t, f.genesis.state.projection, testIdentity(), s1Changes(
		Change{Kind: PutLogPolicy, LogPolicy: &LogPolicyChange{Version: LogPolicyVersion, Level: LogLevelInfo}},
		Change{Kind: PutAssignment, Identity: &bob, RoleID: "security_admin"},
	))
	header.OperationDigest = operation.digest
	signed := authorityTestSealHeader(t, f, keys[1], header, operation)
	h, err := verifyAuthorityHistoricalH(f.trust, signed)
	if err != nil {
		t.Fatal("codec fixture is not an authentic historical H", err)
	}
	config := s2cTestConfig(t, f, 1, filepath.Join(t.TempDir(), "native"))
	k, err := createS2CParticipant(config)
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := AssessS1(k.replayState.projection, operation)
	if err != nil || !assessment.NeedsPurpose || h.handoff.authorization.purposeEvidence != [32]byte{} {
		t.Fatal("fixture does not isolate absent required purpose", err)
	}
	before := k.p.floor
	if _, err := k.assessLogPolicyFloor(h); !errors.Is(err, ErrOperationAuthorization) || k.p.floor != before || k.logPolicyFloor.Index != 0 {
		t.Fatal("live logical admission could burn irreversible floor without required purpose", err)
	}
	body, err := logPolicyFloorBytes(h)
	if err != nil {
		t.Fatal(err)
	}
	framed, err := k.encodeRecord(s2cPRecord{kind: s2cPLogPolicyFloor, index: k.p.count + 1, raw: string(body)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = k.p.append(framed, 0, 0); err != nil {
		t.Fatal(err)
	}
	pf, bf, err := k.Floors()
	if err != nil {
		t.Fatal(err)
	}
	if err = k.Close(); err != nil {
		t.Fatal(err)
	}
	if restored, err := resumeS2CParticipant(config, pf, bf); err == nil {
		_ = restored.Close()
		t.Fatal("historical marker bypassed operation-purpose requirement")
	}
}
