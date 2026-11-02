package security

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

func TestOperationalPolicyFloorDurablePerGroupWithoutAdoption(t *testing.T) {
	for _, group := range operationalpolicy.Groups() {
		t.Run(string(group), func(t *testing.T) {
			f, h, _, _, _ := operationalPolicyCertificateFixture(t, group, 1)
			config := s2cTestConfig(t, f, 1, filepath.Join(t.TempDir(), "native"))
			k, err := createS2CParticipant(config)
			if err != nil {
				t.Fatal(err)
			}
			if err := k.ensureOperationalPolicyFloorLocked(h, k.credit); err != nil {
				t.Fatal(err)
			}
			floor := *k.operationalFloors[group]
			if floor.floor.Index != 3 || floor.floor.Chain == [32]byte{} || floor.original != h.raw {
				t.Fatal("preparation floor not durable or original changed")
			}
			if err := k.ensureOperationalPolicyFloorLocked(h, k.credit); err != nil || k.p.count != 3 || *k.operationalFloors[group] != floor {
				t.Fatal("one-time group floor duplicated", err)
			}
			projection := k.operationalPolicyProjectionLocked()
			if projection.Policies.Get(group) != nil || len(projection.Preparations) != 1 {
				t.Fatal("preparation applied uncommitted values or lost floor")
			}
			expected := OperationalPolicyPreparation{Group: group, Version: operationalpolicy.Version, ChangeID: h.handoff.id,
				IntentDigest: h.handoff.operation.digest, ActorDigest: s2cHash("public-original-actor", h.handoff.operation.actor)}
			if projection.Preparations[0] != expected {
				t.Fatal("preparation not bound to immutable ID/intent/actor")
			}
			projection.Preparations[0].ActorDigest[0] ^= 1
			projection.Preparations[0].IntentDigest[0] ^= 1
			projection.Preparations[0].ChangeID.Nonce[0] ^= 1
			if k.operationalPolicyProjectionLocked().Preparations[0] != expected {
				t.Fatal("preparation projection leaked state")
			}
			if !errors.Is(k.operationalOwnersAvailableLocked(), ErrOperationalPolicyUnavailable) {
				t.Fatal("PREPARED-only floor did not require actual application owner")
			}
			pf, bf, err := k.Floors()
			if err != nil {
				t.Fatal(err)
			}
			if err := k.Close(); err != nil {
				t.Fatal(err)
			}
			restored, err := resumeS2CParticipant(config, pf, bf)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := restored.Close(); err != nil {
					t.Error(err)
				}
			}()
			if *restored.operationalFloors[group] != floor || restored.operationalPolicyProjectionLocked().Preparations[0] != expected || restored.operationalPolicyProjectionLocked().Policies.Get(group) != nil {
				t.Fatal("restart lost prepared-only floor or invented committed values")
			}
			if !errors.Is(restored.operationalOwnersAvailableLocked(), ErrOperationalPolicyUnavailable) {
				t.Fatal("prepared-only restart ignored missing owner")
			}
			raw, err := operationalPolicyFloorBytes(h)
			if err != nil {
				t.Fatal(err)
			}
			if err := restored.replayOperationalPolicyFloor(s2cPRecord{kind: s2cPOperationalPolicyFloor, index: 4, raw: string(raw)}); !errors.Is(err, errS2CProtocol) {
				t.Fatal("duplicate complete floor accepted", err)
			}
		})
	}
}

func TestOperationalPolicyFloorCorruptCompleteMarkerRefusesRestore(t *testing.T) {
	for name, corrupt := range map[string]func([]byte) []byte{
		"schema": func(raw []byte) []byte {
			return bytes.Replace(raw, []byte(`"Version":1`), []byte(`"Version":9`), 1)
		},
		"application": func(raw []byte) []byte {
			return bytes.Replace(raw, []byte(`"ApplicationVersion":1`), []byte(`"ApplicationVersion":9`), 1)
		},
		"unknown group": func(raw []byte) []byte {
			return bytes.Replace(raw, []byte(`"Group":"slow_rpc"`), []byte(`"Group":"unknown"`), 1)
		},
		"different group": func(raw []byte) []byte {
			return bytes.Replace(raw, []byte(`"Group":"slow_rpc"`), []byte(`"Group":"rate"`), 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f, h, _, _, _ := operationalPolicyCertificateFixture(t, operationalpolicy.GroupSlowRPC, 1)
			config := s2cTestConfig(t, f, 1, filepath.Join(t.TempDir(), "native"))
			k, err := createS2CParticipant(config)
			if err != nil {
				t.Fatal(err)
			}
			if err := k.ensureOperationalPolicyFloorLocked(h, k.credit); err != nil {
				t.Fatal(err)
			}
			pf, bf, err := k.Floors()
			if err != nil {
				t.Fatal(err)
			}
			body, err := operationalPolicyFloorBytes(h)
			if err != nil {
				t.Fatal(err)
			}
			bad := corrupt(body)
			if bytes.Equal(body, bad) {
				t.Fatal("corruption fixture did not change marker")
			}
			encoded, err := k.encodeRecord(s2cPRecord{kind: s2cPOperationalPolicyFloor, index: pf.Index, raw: string(bad)})
			if err != nil {
				t.Fatal(err)
			}
			if err := k.Close(); err != nil {
				t.Fatal(err)
			}
			s2RestoreTestReplaceFinalPayload(t, config.PPath, encoded)
			cut, err := mutationlog.InspectFileWALCut(config.PPath, pf.Index, s2cDecodeJournalPayload, func(mutationlog.Entry) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			pf.Chain = cut.ChainSHA256
			before := s2RestoreTestReadFile(t, config.PPath)
			if resumed, err := resumeS2CParticipant(config, pf, bf); err == nil {
				_ = resumed.Close()
				t.Fatal("complete unknown/mismatched application floor ignored")
			}
			if !bytes.Equal(before, s2RestoreTestReadFile(t, config.PPath)) {
				t.Fatal("complete invalid marker was silently truncated")
			}
		})
	}
}

func TestOperationalPolicyFloorExactBoundAndSeparateGrammar(t *testing.T) {
	for _, group := range operationalpolicy.Groups() {
		t.Run(string(group), func(t *testing.T) {
			_, h, _, _, _ := operationalPolicyCertificateFixture(t, group, 1)
			raw, err := operationalPolicyFloorBytes(h)
			if err != nil {
				t.Fatal(err)
			}
			bound, err := operationalPolicyFloorSizeBound(uint64(len(h.raw)), group)
			if err != nil || bound != uint64(len(raw)) {
				t.Fatal("floor accounting did not match exact persisted bytes", bound, len(raw), err)
			}
		})
	}
	if _, err := operationalPolicyFloorSizeBound(uint64(MaxImageBytes)*2+1, operationalpolicy.GroupSlowRPC); err == nil {
		t.Fatal("unbounded original accepted")
	}
	if _, err := operationalPolicyFloorSizeBound(10, "unknown"); err == nil {
		t.Fatal("unknown group acquired floor bound")
	}
	if _, err := operationalPolicyFloorBytes(nil); err == nil {
		t.Fatal("nil original acquired floor")
	}
	if s2cPLogPolicyFloor != 11 || s2cPLogPolicyCandidate != 12 || s2cPOperationalPolicyFloor <= s2cPLogPolicyCandidate {
		t.Fatal("operational markers changed the completed logging grammar")
	}
}
