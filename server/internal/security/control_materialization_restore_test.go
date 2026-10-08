package security

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"

	"github.com/anaregdesign/lantern/core/mutationlog"
)

func s2OwnerTestScope(g *s2TrustedGenesis) s2LocalScope {
	s := g.state
	c := s.projection.cut
	return s2LocalScope{
		StoreIdentity: [32]byte{1}, JournalEpoch: [16]byte{2},
		Domain: c.Domain, Cohort: c.Cohort, Generation: c.Generation,
		Membership: s.membership, Fences: c.Fences,
		Configuration: s.configuration, ConfigDigest: s.configuration.Digest(),
		Genesis: g.roots.Genesis,
		Floor:   s2CapsuleFloor{s.retiredThrough, c.Domain, c.Cohort, g.roots.Retirement},
		Storage: s2LocalStoragePolicy{s2TestLimits(), uint64(MaxSystemJournalBytes), 100000},
	}
}

func s2RestoreTestWrite(t *testing.T, path string, scope s2LocalScope, g *s2TrustedGenesis, replay []S1CertifiedNext) []s2LocalReceipt {
	t.Helper()
	owner, err := createS2Local(path, scope, g)
	if err != nil {
		t.Fatal("create", err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	_, genesis, err := owner.ReadLocalCut()
	if err != nil {
		t.Fatal(err)
	}
	receipts := []s2LocalReceipt{genesis}
	for _, next := range replay {
		plan, err := owner.PrepareNext(next)
		if err != nil {
			t.Fatal("prepare", err)
		}
		receipt, err := owner.CommitPrepared(plan)
		if err != nil {
			t.Fatal("commit", err)
		}
		receipts = append(receipts, receipt)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	return receipts
}

func s2RestoreTestReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func s2RestoreTestWriteFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func s2RestoreTestReject(t *testing.T, path string, scope s2LocalScope, g *s2TrustedGenesis, replay []S1CertifiedNext, required s2LocalReceipt) {
	t.Helper()
	wal, walErr := os.ReadFile(path)
	tip, tipErr := os.ReadFile(path + ".tip")
	owner, err := resumeS2Local(path, scope, g, replay, required)
	if owner != nil {
		_ = owner.Close()
	}
	if err == nil || owner != nil {
		t.Fatal("rejected input returned an owner", err)
	}
	if after, err := os.ReadFile(path); (walErr == nil) != (err == nil) || !bytes.Equal(wal, after) {
		t.Fatal("failed recovery changed WAL bytes", err)
	}
	if after, err := os.ReadFile(path + ".tip"); (tipErr == nil) != (err == nil) || !bytes.Equal(tip, after) {
		t.Fatal("failed recovery changed tip bytes", err)
	}
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatal("failed recovery retained lease", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestS2LocalRestoreExactIndependentHistory(t *testing.T) {
	g, expected, replay, original := s2TestHistory(t)
	scope := s2OwnerTestScope(g)
	path := filepath.Join(t.TempDir(), "local.wal")
	receipts := s2RestoreTestWrite(t, path, scope, g, replay)
	// Different verified witnesses for the same logical decisions do not move
	// first ownership or require rewriting the local journal.
	rewitnessed := append([]S1CertifiedNext(nil), replay...)
	for i := range rewitnessed {
		certificate := *rewitnessed[i].certificate
		certificate.witness = [32]byte{byte(i + 30)}
		rewitnessed[i].certificate = &certificate
	}
	for _, required := range []s2LocalReceipt{receipts[0], receipts[3], receipts[len(receipts)-1]} {
		owner, err := resumeS2Local(path, scope, g, rewitnessed, required)
		if err != nil || owner == nil {
			t.Fatal("independent replay", err)
		}
		state, receipt, err := owner.ReadLocalCut()
		if err != nil || receipt != receipts[len(receipts)-1] || !bytes.Equal(s2TestEncode(t, state, g.roots), s2TestEncode(t, expected, g.roots)) {
			t.Fatal("complete cut or receipt changed across recovery", err)
		}
		if state.projection.SessionLineage(s1Bob()) != 2 || len(state.projection.Image().Sessions) != 0 {
			t.Fatal("lineage-only revocation disappeared")
		}
		status, outcome, lookupReceipt, err := owner.LookupOriginal(original.id)
		_, want := expected.Lookup(original.id)
		if err != nil || status != S1Known || lookupReceipt != receipt || outcome.commit != want.commit || outcome.handoff != want.handoff || outcome.operation != want.operation || !bytes.Equal(s2TestJSON(t, outcome.Items()), s2TestJSON(t, want.Items())) {
			t.Fatal("original outcome or first CommitRef changed", err)
		}
		if receipt.LocalIndex != expected.slot+1 || receipt.ControlSlot != expected.slot || receipt.ControlPrefix != expected.prefix || receipt.CapsuleDigest != s2LocalCapsuleDigest(s2TestEncode(t, expected, g.roots)) {
			t.Fatal("receipt does not identify the complete materialized cut")
		}
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestS2LocalRestoreRequiresCompleteIndependentProof(t *testing.T) {
	g, final, replay, _ := s2TestHistory(t)
	scope := s2OwnerTestScope(g)
	path := filepath.Join(t.TempDir(), "local.wal")
	receipts := s2RestoreTestWrite(t, path, scope, g, replay)
	extra := append(append([]S1CertifiedNext(nil), replay...), s1Next(final, nil))
	replaced := append([]S1CertifiedNext(nil), replay...)
	replaced[0] = s1Next(g.state, nil)
	for _, test := range []struct {
		name   string
		replay []S1CertifiedNext
	}{
		{"missing-all", nil}, {"missing-head", replay[1:]},
		{"missing-tail", replay[:len(replay)-1]}, {"extra", extra},
		{"repeated-first", append([]S1CertifiedNext{replay[0]}, replay...)},
		{"unverified", []S1CertifiedNext{{}}}, {"other-first-decision", replaced},
	} {
		t.Run(test.name, func(t *testing.T) {
			s2RestoreTestReject(t, path, scope, g, test.replay, receipts[0])
		})
	}
	image := s1Image()
	image.Roles[0].Name = "different independently trusted genesis"
	wrongGenesis := &s2TrustedGenesis{s1Fixture(t, image), g.roots}
	for _, test := range []struct {
		name    string
		genesis *s2TrustedGenesis
	}{
		{"missing-root", nil}, {"empty-root", &s2TrustedGenesis{}},
		{"wrong-semantic-root", wrongGenesis}, {"late-state-is-not-genesis", &s2TrustedGenesis{final, g.roots}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s2RestoreTestReject(t, path, scope, test.genesis, replay, receipts[0])
		})
	}
}

func TestS2LocalRestoreBindsEveryRequiredCutField(t *testing.T) {
	g, _, replay, _ := s2TestHistory(t)
	scope := s2OwnerTestScope(g)
	path := filepath.Join(t.TempDir(), "local.wal")
	receipts := s2RestoreTestWrite(t, path, scope, g, replay)
	for _, test := range []struct {
		name   string
		mutate func(*s2LocalReceipt)
	}{
		{"zero", func(r *s2LocalReceipt) { *r = s2LocalReceipt{} }},
		{"scope", func(r *s2LocalReceipt) { r.ScopeDigest[0] ^= 1 }},
		{"index", func(r *s2LocalReceipt) { r.LocalIndex-- }},
		{"future-index", func(r *s2LocalReceipt) { r.LocalIndex += 100; r.ControlSlot += 100 }},
		{"chain", func(r *s2LocalReceipt) { r.Chain[0] ^= 1 }},
		{"capsule", func(r *s2LocalReceipt) { r.CapsuleDigest[0] ^= 1 }},
		{"control-slot", func(r *s2LocalReceipt) { r.ControlSlot-- }},
		{"control-prefix", func(r *s2LocalReceipt) { r.ControlPrefix[0] ^= 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			required := receipts[len(receipts)-1]
			test.mutate(&required)
			s2RestoreTestReject(t, path, scope, g, replay, required)
		})
	}
	// A self-consistent rollback of both files needs an independent known-ACK
	// floor. A genesis-only floor deliberately makes no stronger claim.
	wal := s2RestoreTestReadFile(t, path)
	tip := s2RestoreTestReadFile(t, path+".tip")
	offset := 8
	for range 3 {
		offset += 8 + int(binary.BigEndian.Uint32(wal[offset:]))
	}
	s2RestoreTestWriteFile(t, path, wal[:offset])
	s2RestoreTestWriteFile(t, path+".tip", tip[:77+3*44])
	s2RestoreTestReject(t, path, scope, g, replay[:2], receipts[len(receipts)-1])
	owner, err := resumeS2Local(path, scope, g, replay[:2], receipts[0])
	if err != nil || owner == nil {
		t.Fatal("minimal genesis continuity could not restore its exact prefix", err)
	}
	_, receipt, err := owner.ReadLocalCut()
	if err != nil || receipt != receipts[2] {
		t.Fatal("rollback prefix silently relabeled", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestS2LocalRestoreScopeAndPathRemainFixed(t *testing.T) {
	g, _, replay, _ := s2TestHistory(t)
	scope := s2OwnerTestScope(g)
	path := filepath.Join(t.TempDir(), "local.wal")
	receipts := s2RestoreTestWrite(t, path, scope, g, replay)
	for _, test := range []struct {
		name   string
		mutate func(*s2LocalScope)
	}{
		{"owner-identity", func(s *s2LocalScope) { s.StoreIdentity[0]++ }},
		{"epoch", func(s *s2LocalScope) { s.JournalEpoch[0]++ }},
		{"domain", func(s *s2LocalScope) { s.Domain[0]++; s.Floor.Domain = s.Domain }},
		{"cohort", func(s *s2LocalScope) { s.Cohort[0]++; s.Floor.Cohort = s.Cohort }},
		{"generation", func(s *s2LocalScope) { s.Generation[0]++ }},
		{"membership", func(s *s2LocalScope) { s.Membership[0]++ }},
		{"fences", func(s *s2LocalScope) { s.Fences[0]++ }},
		{"configuration", func(s *s2LocalScope) { s.Configuration.Policy.MaxRoles--; s.ConfigDigest = s.Configuration.Digest() }},
		{"genesis", func(s *s2LocalScope) { s.Genesis[0]++ }},
		{"floor", func(s *s2LocalScope) { s.Floor.Through = 1; s.Floor.Root = [32]byte{90} }},
		{"capsule-bytes", func(s *s2LocalScope) { s.Storage.Capsule.Bytes-- }},
		{"ledger-bound", func(s *s2LocalScope) { s.Storage.Capsule.LedgerEntries-- }},
		{"lineage-bound", func(s *s2LocalScope) { s.Storage.Capsule.LineageEntries-- }},
		{"journal-budget", func(s *s2LocalScope) { s.Storage.JournalBytes-- }},
		{"replay-bound", func(s *s2LocalScope) { s.Storage.ReplayRecords-- }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := scope
			test.mutate(&changed)
			s2RestoreTestReject(t, path, changed, g, replay, receipts[0])
		})
	}
	t.Run("copied-path", func(t *testing.T) {
		copyPath := filepath.Join(t.TempDir(), "copied.wal")
		s2RestoreTestWriteFile(t, copyPath, s2RestoreTestReadFile(t, path))
		s2RestoreTestWriteFile(t, copyPath+".tip", s2RestoreTestReadFile(t, path+".tip"))
		s2RestoreTestReject(t, copyPath, scope, g, replay, receipts[0])
	})
}

func TestS2LocalRestoreRejectsIncompleteOrUnexplainedFrontiers(t *testing.T) {
	g, _, replay, _ := s2TestHistory(t)
	scope := s2OwnerTestScope(g)
	for _, failure := range []string{"missing-wal", "missing-tip", "header-only", "partial-genesis", "partial-wal", "gap-two", "gap-all"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "local.wal")
			receipts := s2RestoreTestWrite(t, path, scope, g, replay)
			wal := s2RestoreTestReadFile(t, path)
			tip := s2RestoreTestReadFile(t, path+".tip")
			proof := replay
			switch failure {
			case "missing-wal":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "missing-tip":
				if err := os.Remove(path + ".tip"); err != nil {
					t.Fatal(err)
				}
			case "header-only":
				s2RestoreTestWriteFile(t, path, wal[:8])
				s2RestoreTestWriteFile(t, path+".tip", tip[:77])
				proof = nil
			case "partial-genesis":
				end := 16 + int(binary.BigEndian.Uint32(wal[8:]))
				s2RestoreTestWriteFile(t, path, wal[:end-1])
				s2RestoreTestWriteFile(t, path+".tip", tip[:77])
				proof = nil
			case "partial-wal":
				s2RestoreTestWriteFile(t, path, wal[:len(wal)-1])
			case "gap-two":
				s2RestoreTestWriteFile(t, path+".tip", tip[:len(tip)-2*44])
			case "gap-all":
				s2RestoreTestWriteFile(t, path+".tip", tip[:77])
			}
			s2RestoreTestReject(t, path, scope, g, proof, receipts[0])
		})
	}
	for partial := 1; partial < 44; partial++ {
		t.Run(fmt.Sprintf("partial-tip-%02d", partial), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "local.wal")
			receipts := s2RestoreTestWrite(t, path, scope, g, replay[:1])
			tip := s2RestoreTestReadFile(t, path+".tip")
			s2RestoreTestWriteFile(t, path+".tip", tip[:len(tip)-44+partial])
			s2RestoreTestReject(t, path, scope, g, replay[:1], receipts[0])
		})
	}
}

func TestS2LocalRestoreAccountsActualBytesAndOneMissingTip(t *testing.T) {
	g, _, history, _ := s2TestHistory(t)
	replay := history[:1]
	scope := s2OwnerExactBudget(t, g, replay[0])
	for _, cut := range []string{"equal-frontier", "missing-final-tip", "actual-over-budget"} {
		t.Run(cut, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "local.wal")
			receipts := s2RestoreTestWrite(t, path, scope, g, replay)
			wal := s2RestoreTestReadFile(t, path)
			tip := s2RestoreTestReadFile(t, path+".tip")
			if uint64(len(wal)+len(tip)) != scope.Storage.JournalBytes {
				t.Fatal("fixture is not at exact byte budget")
			}
			before := len(tip)
			if cut == "actual-over-budget" {
				s2RestoreTestWriteFile(t, path+".tip", append(tip, 0))
				s2RestoreTestReject(t, path, scope, g, replay, receipts[0])
				return
			}
			if cut == "missing-final-tip" {
				before -= 44
				s2RestoreTestWriteFile(t, path+".tip", tip[:before])
			}
			owner, err := resumeS2Local(path, scope, g, replay, receipts[len(receipts)-1])
			if err != nil || owner == nil {
				t.Fatal("resume within exact reserved charge", err)
			}
			_, receipt, err := owner.ReadLocalCut()
			if err != nil || receipt != receipts[len(receipts)-1] {
				t.Fatal("catchup changed materialized frontier", err)
			}
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
			after := s2RestoreTestReadFile(t, path+".tip")
			if !bytes.Equal(after, tip) || len(after)-before != len(tip)-before {
				t.Fatal("catchup did not append exactly the one missing tip record")
			}
			if !bytes.Equal(s2RestoreTestReadFile(t, path), wal) {
				t.Fatal("resume rewrote WAL")
			}
			// Equal-frontier reopen is repeatable without consuming another 44 bytes.
			owner, err = resumeS2Local(path, scope, g, replay, receipt)
			if err != nil {
				t.Fatal(err)
			}
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(s2RestoreTestReadFile(t, path+".tip"), tip) {
				t.Fatal("equal frontier appended duplicate tip")
			}
		})
	}
}

func TestS2LocalRestoreCompleteGenesisWithOnlyTipHeader(t *testing.T) {
	g := s2TestGenesis(s1Fixture(t, s1Image()))
	scope := s2OwnerTestScope(g)
	path := filepath.Join(t.TempDir(), "local.wal")
	receipts := s2RestoreTestWrite(t, path, scope, g, nil)
	wal := s2RestoreTestReadFile(t, path)
	tip := s2RestoreTestReadFile(t, path+".tip")
	s2RestoreTestWriteFile(t, path+".tip", tip[:77])
	owner, err := resumeS2Local(path, scope, g, nil, receipts[0])
	if err != nil || owner == nil {
		t.Fatal("complete independently trusted uncertain genesis could not recover", err)
	}
	state, receipt, err := owner.ReadLocalCut()
	if err != nil || receipt != receipts[0] || !bytes.Equal(s2TestEncode(t, state, g.roots), s2TestEncode(t, g.state, g.roots)) {
		t.Fatal("genesis catchup changed the trusted aggregate", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(s2RestoreTestReadFile(t, path), wal) || !bytes.Equal(s2RestoreTestReadFile(t, path+".tip"), tip) {
		t.Fatal("genesis catchup did not consume exactly the missing 44 bytes")
	}
}

// Reframe one changed final application payload and rebuild its last tip hash.
// This produces valid physical checksums and commitments, never trusted proof.
func s2RestoreTestReplaceFinalPayload(t *testing.T, path string, payload []byte, mutateHeader ...func([]byte)) {
	t.Helper()
	wal := s2RestoreTestReadFile(t, path)
	offset, last := 8, 8
	for offset < len(wal) {
		last = offset
		offset += 8 + int(binary.BigEndian.Uint32(wal[offset:]))
	}
	body := append(append([]byte(nil), wal[last+8:last+44]...), payload...)
	for _, mutate := range mutateHeader {
		mutate(body[:36])
	}
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
	crc := crc32.New(crc32.MakeTable(crc32.Castagnoli))
	_, _ = crc.Write(frame)
	_, _ = crc.Write(body)
	frame = binary.BigEndian.AppendUint32(frame, crc.Sum32())
	frame = append(frame, body...)
	s2RestoreTestWriteFile(t, path, append(wal[:last:last], frame...))
	seq := binary.BigEndian.Uint64(body)
	cut, err := mutationlog.InspectFileWALCut(path, seq, func(b []byte) (mutationlog.MutationOp, error) { return append([]byte(nil), b...), nil }, func(mutationlog.Entry) error { return nil })
	if err != nil {
		t.Fatal("forged bytes are not a valid physical WAL", err)
	}
	tip := s2RestoreTestReadFile(t, path+".tip")
	record := tip[len(tip)-44:]
	copy(record[8:40], cut.ChainSHA256[:])
	binary.BigEndian.PutUint32(record[40:], crc32.Checksum(record[:40], crc32.MakeTable(crc32.Castagnoli)))
	s2RestoreTestWriteFile(t, path+".tip", tip)
}

func TestS2LocalRestoreRejectsInvalidApplicationRecordsInsideValidFrames(t *testing.T) {
	g, _, replay, _ := s2TestHistory(t)
	scope := s2OwnerTestScope(g)
	for _, failure := range []string{"nonzero-hlc", "another-genesis", "old-format", "trailing-byte"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "local.wal")
			receipts := s2RestoreTestWrite(t, path, scope, g, replay[:1])
			wal := s2RestoreTestReadFile(t, path)
			firstEnd := 16 + int(binary.BigEndian.Uint32(wal[8:]))
			payload := append([]byte(nil), wal[firstEnd+44:]...)
			var mutate []func([]byte)
			switch failure {
			case "nonzero-hlc":
				mutate = append(mutate, func(header []byte) { binary.BigEndian.PutUint64(header[8:16], 1) })
			case "another-genesis":
				payload = append([]byte(nil), wal[8+44:firstEnd]...)
			case "old-format":
				payload = []byte("LNSEC03")
			case "trailing-byte":
				payload = append(payload, 0)
			}
			s2RestoreTestReplaceFinalPayload(t, path, payload, mutate...)
			s2RestoreTestReject(t, path, scope, g, replay[:1], receipts[0])
		})
	}
}

func TestS2LocalRestoreRejectsPhysicallyValidForgedCapsules(t *testing.T) {
	g, expected, replay, original := s2TestHistory(t)
	scope := s2OwnerTestScope(g)
	for _, failure := range []string{"original-H", "original-result", "lineage"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "local.wal")
			receipts := s2RestoreTestWrite(t, path, scope, g, replay)
			forged, err := s2DetachState(expected)
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "original-H":
				forged.ledger[original.id].handoff = [32]byte{90}
				forged.ledger[original.id].commit.Value = [32]byte{90}
			case "original-result":
				outcome := forged.ledger[original.id]
				outcome.disposition = S1RejectedPurpose
				for i := range outcome.items {
					outcome.items[i].Disposition = S1RejectedPurpose
				}
			case "lineage":
				forged.projection.lineage[s1Bob()]++
				forged.projection.cut.Projection = forged.projection.projectionDigest()
			}
			capsule := s2TestEncode(t, forged, g.roots)
			r := &s2LocalRecord{kind: s2LocalApply, scopeDigest: scope.digest(), localIndex: receipts[len(receipts)-1].LocalIndex, previousCapsuleDigest: receipts[len(receipts)-2].CapsuleDigest, capsuleDigest: s2LocalCapsuleDigest(capsule), commit: replay[len(replay)-1].certificate.commit, capsule: string(capsule)}
			payload, err := s2EncodeLocalRecord(r, scope)
			if err != nil {
				t.Fatal("self-consistent forged capsule did not pass syntax", err)
			}
			s2RestoreTestReplaceFinalPayload(t, path, payload)
			s2RestoreTestReject(t, path, scope, g, replay, receipts[0])
		})
	}
}

func TestS2LocalRestoreReplayPanicReleasesResourcesWithoutMutation(t *testing.T) {
	g, _, replay, _ := s2TestHistory(t)
	scope := s2OwnerTestScope(g)
	for _, at := range []int{1, 3, len(replay) + 1} {
		t.Run(fmt.Sprintf("record-%d", at), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "local.wal")
			receipts := s2RestoreTestWrite(t, path, scope, g, replay)
			wal := s2RestoreTestReadFile(t, path)
			tip := s2RestoreTestReadFile(t, path+".tip")
			// Keep the final tip missing to prove replay must finish before any
			// recovery attestation, even when earlier records already restored.
			s2RestoreTestWriteFile(t, path+".tip", tip[:len(tip)-44])
			var owner *s2LocalOwner
			var gotPanic any
			seen := 0
			func() {
				defer func() { gotPanic = recover() }()
				owner, _ = resumeS2LocalWithHooks(path, scope, g, replay, receipts[0], &s2LocalHooks{replay: func() {
					seen++
					if seen == at {
						panic("injected detached replay panic")
					}
				}})
			}()
			if gotPanic != "injected detached replay panic" || owner != nil || seen != at {
				t.Fatal("replay panic escaped its exact failure boundary", gotPanic, seen)
			}
			if !bytes.Equal(s2RestoreTestReadFile(t, path), wal) || !bytes.Equal(s2RestoreTestReadFile(t, path+".tip"), tip[:len(tip)-44]) {
				t.Fatal("replay panic changed files or attested an unvalidated suffix")
			}
			owner, err := resumeS2Local(path, scope, g, replay, receipts[len(receipts)-1])
			if err != nil || owner == nil {
				t.Fatal("panic leaked the exclusive owner or resources", err)
			}
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(s2RestoreTestReadFile(t, path+".tip"), tip) {
				t.Fatal("fresh owner could not recover the exact pending tip")
			}
		})
	}
}
