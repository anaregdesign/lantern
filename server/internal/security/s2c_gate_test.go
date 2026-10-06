package security

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// These gates compose independent Ed25519 trust, immutable historical H,
// authenticated protocol, owned native P, preview, native B, and cold recovery.
// Their scheduler delivers only copies of real encoded participant outboxes.

func s2cGateOrigin(t *testing.T, n *s2cTestNetwork, origin uint32, nonce byte, role string) (FullChangeID, [32]byte) {
	t.Helper()
	state, _, err := n.nodes[origin].ReadLocalCut()
	if err != nil {
		t.Fatal(err)
	}
	change := s1ReaderRole()
	change.Role.ID = role
	op := s1Operation(t, state.projection, testIdentity(), s1Changes(change))
	id := FullChangeID{S1Version, op.reviewed.Domain, op.reviewed.Cohort, 1, [16]byte{nonce}}
	raw := s2cTestSeal(t, n.fixture.trust, origin, n.fixture.keys[origin], id, op, uint64(nonce), false)
	h, err := verifyHistoricalH(n.fixture.trust, raw)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately old input. Recovery has no time sample or current-admission
	// issuer with which it could refresh the original consume or deadline.
	if !h.handoff.authorization.credentialDeadline.Before(time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)) {
		t.Fatal("fixture is not the expired original H")
	}
	digest, err := n.nodes[origin].persistSealedOriginH(raw)
	if err != nil || digest != h.digest() {
		t.Fatal("durable sealed origin", err)
	}
	return id, digest
}

func s2cGateOutcome(t *testing.T, n *s2cTestNetwork, member uint32, id FullChangeID, value [32]byte, slot uint64) CommitRef {
	t.Helper()
	status, outcome, receipt, err := n.nodes[member].LookupOriginal(id)
	if err != nil || status != S1Known || outcome == nil || outcome.HandoffDigest() != value || outcome.Disposition() != S1Applied || outcome.Commit().Slot != slot || receipt.ControlSlot < slot {
		t.Fatal("exact original outcome", member, status, outcome, receipt, err)
	}
	return outcome.Commit()
}

func TestS2CGateHiddenChoiceExactExpiredH(t *testing.T) {
	var commits []CommitRef
	for _, hiddenB := range []bool{false, true} {
		t.Run(fmt.Sprintf("B-accepted-%t", hiddenB), func(t *testing.T) {
			n := s2cTestNativeCluster(t, 3, nil)
			id, value := s2cGateOrigin(t, n, 2, 1, "expired_original")
			n.selectValue(2, value, 1, 2)
			n.send(s2cAccept, 2, 1)
			if hiddenB {
				n.send(s2cAccept, 2, 2)
			}
			// A-only acceptance and A+B choice produce the same observations at
			// the recovering A/C pair. Every Accepted response/QC is lost, and
			// B (also the origin) is unavailable for the whole recovery.
			n.queue = nil
			n.stop(2)
			delete(n.fixture.keys, 2)
			n.restart(1)
			n.restart(3)
			for _, member := range []uint32{1, 3} {
				history, err := n.nodes[member].ExportChosen(1, 1, 1<<20)
				if err != nil || len(history) != 0 {
					t.Fatal("unchosen accept exported as history", member, err)
				}
			}
			// Neither a pending H nor an earliest-choice oracle is supplied.
			// A must recover the highest accepted full H from its own journal.
			n.selectValue(1, [32]byte{}, 1, 3)
			n.choose(1, 1, 3)
			n.send(s2cChosen, 1, 3)
			commit := s2cGateOutcome(t, n, 1, id, value, 1)
			if other := s2cGateOutcome(t, n, 3, id, value, 1); other != commit {
				t.Fatal("different commit across recovery quorum")
			}
			commits = append(commits, commit)
			n.queue = nil
			n.restart(1)
			n.restart(3)
			for _, member := range []uint32{1, 3} {
				if got := s2cGateOutcome(t, n, member, id, value, 1); got != commit {
					t.Fatal("cold restart changed original outcome")
				}
				history, err := n.nodes[member].ExportChosen(1, 1, 1<<20)
				if err != nil || len(history) != 1 {
					t.Fatal("full chosen history missing after cold restart", err)
				}
				chosen, err := s2cDecodeMessage(n.fixture.trust, history[0])
				if err != nil || chosen.h == nil || chosen.h.digest() != value || chosen.h.handoff.id != id || chosen.h.handoff.authorization.consume != time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC) {
					t.Fatal("immutable full H did not survive both journals", err)
				}
			}
		})
	}
	if len(commits) != 2 || commits[0] != commits[1] {
		t.Fatal("first CommitRef depends on hidden earlier quorum", commits)
	}
}

func TestS2CGateConcurrentProposersAndBoundedCatchup(t *testing.T) {
	n := s2cTestNativeCluster(t, 3, nil)
	id, value := s2cGateOrigin(t, n, 3, 1, "concurrent_original")
	n.begin(1, [32]byte{})
	n.send(s2cPrepare, 1, 1)
	n.begin(3, value)
	n.send(s2cPrepare, 3, 3)
	n.send(s2cPrepare, 3, 2)
	// Duplicate a genuine Promise; it must not count as a second signer.
	duplicate := n.take(s2cPromise, 2, 3)
	if err := n.deliver(duplicate); err != nil {
		t.Fatal(err)
	}
	if err := n.deliver(duplicate); err != nil {
		t.Fatal("exact Promise retransmission", err)
	}
	for _, e := range n.queue {
		if e.kind() == s2cAccept {
			t.Fatal("duplicate signer formed a quorum")
		}
	}
	n.send(s2cPromise, 3, 3)
	// Delayed lower Prepare cannot erase the durable higher promise.
	before, _, _ := n.nodes[2].Floors()
	_ = n.deliver(n.take(s2cPrepare, 1, 2))
	after, _, err := n.nodes[2].Floors()
	if err != nil || after != before {
		t.Fatal("lower Prepare appended or closed the participant", err)
	}
	n.choose(3, 2, 3)
	commit := s2cGateOutcome(t, n, 3, id, value, 1)
	// Drop all chosen dissemination and stale traffic, then discard every
	// participant's in-memory proof. Member 1 has never received this full H.
	n.queue = nil
	for _, member := range []uint32{1, 2, 3} {
		n.restart(member)
	}
	status, _, _, err := n.nodes[1].LookupOriginal(id)
	if err != nil || status != S1Unresolved {
		t.Fatal("lagging peer learned a dropped choice", status, err)
	}
	history, err := n.nodes[3].ExportChosen(1, 1, 1<<20)
	if err != nil || len(history) != 1 {
		t.Fatal("retained chosen export", err)
	}
	if tiny, err := n.nodes[3].ExportChosen(1, 1, uint64(len(history[0])-1)); err == nil && len(tiny) != 0 {
		t.Fatal("export exceeded byte bound")
	}
	for _, member := range []uint32{1, 2} {
		if err := n.deliver(s2cTestEnvelope{3, member, history[0]}); err != nil {
			t.Fatal("verified native history catch-up", member, err)
		}
		if got := s2cGateOutcome(t, n, member, id, value, 1); got != commit {
			t.Fatal("catch-up changed first CommitRef")
		}
		p, b, _ := n.nodes[member].Floors()
		if err := n.deliver(s2cTestEnvelope{3, member, history[0]}); err != nil {
			t.Fatal("duplicate retained chosen", err)
		}
		p2, b2, _ := n.nodes[member].Floors()
		if p != p2 || b != b2 {
			t.Fatal("duplicate choice appended either journal")
		}
	}
	history = nil
	n.queue = nil
	n.restart(1)
	// A stable proposer completes the next slot after the finite faults.
	n.selectValue(1, [32]byte{}, 1, 2)
	n.choose(1, 1, 2)
	n.send(s2cChosen, 1, 2)
	n.send(s2cChosen, 1, 3)
	for _, member := range []uint32{1, 2, 3} {
		state, receipt, err := n.nodes[member].ReadLocalCut()
		if err != nil || state.slot != 2 || receipt.ControlSlot != 2 {
			t.Fatal("stable-period completion", member, receipt, err)
		}
	}
	t.Logf("delivered %d actual encoded messages with duplicate, delay, reorder, loss and cold restarts", n.steps)
}

func TestS2CGateContiguousBoundedHistory(t *testing.T) {
	n := s2cTestNativeCluster(t, 3, nil)
	id, value := s2cGateOrigin(t, n, 1, 1, "retained_history")
	for slot := 1; slot <= 3; slot++ {
		candidate := [32]byte{}
		if slot == 1 {
			candidate = value
		}
		n.selectValue(1, candidate, 1, 2)
		n.choose(1, 1, 2)
		n.send(s2cChosen, 1, 2)
		// Member 3 receives nothing during the three decisions. No scheduler
		// proof archive is retained across the cold restart below.
		n.queue = nil
	}
	for _, member := range []uint32{1, 2, 3} {
		n.restart(member)
	}
	all, err := n.nodes[1].ExportChosen(1, 3, 1<<20)
	if err != nil || len(all) != 3 {
		t.Fatal("three-slot retained history", err)
	}
	for i, raw := range all {
		message, err := s2cDecodeMessage(n.fixture.trust, raw)
		if err != nil || message.kind != s2cChosen || message.slot != uint64(i+1) {
			t.Fatal("retained history position or authentication", i, err)
		}
	}
	beforeP, beforeB, _ := n.nodes[1].Floors()
	bounded, err := n.nodes[1].ExportChosen(1, 3, uint64(len(all[0])+len(all[1])))
	if err != nil || len(bounded) != 2 || !bytes.Equal(bounded[0], all[0]) || !bytes.Equal(bounded[1], all[1]) {
		t.Fatal("exact export byte bound", err)
	}
	bounded, err = n.nodes[1].ExportChosen(2, 1, 1<<20)
	if err != nil || len(bounded) != 1 || !bytes.Equal(bounded[0], all[1]) {
		t.Fatal("export start/count bound", err)
	}
	bounded, err = n.nodes[1].ExportChosen(4, 1, 1<<20)
	if err != nil || len(bounded) != 0 {
		t.Fatal("future unchosen history exported", err)
	}
	afterP, afterB, err := n.nodes[1].Floors()
	if err != nil || beforeP != afterP || beforeB != afterB {
		t.Fatal("read-only export appended a journal", err)
	}
	gap := func(raw []byte) {
		t.Helper()
		p, b, _ := n.nodes[3].Floors()
		if err := n.deliver(s2cTestEnvelope{1, 3, raw}); err == nil {
			t.Fatal("genuine future choice bypassed contiguous predecessor")
		}
		p2, b2, err := n.nodes[3].Floors()
		if err != nil || p != p2 || b != b2 {
			t.Fatal("history gap changed local proof or materialization", err)
		}
	}
	gap(all[1])
	if err := n.deliver(s2cTestEnvelope{1, 3, all[0]}); err != nil {
		t.Fatal(err)
	}
	gap(all[2])
	for _, raw := range all[1:] {
		if err := n.deliver(s2cTestEnvelope{1, 3, raw}); err != nil {
			t.Fatal("contiguous stable catch-up", err)
		}
	}
	all, bounded = nil, nil
	n.queue = nil
	n.restart(3)
	want, _, err := n.nodes[1].ReadLocalCut()
	if err != nil {
		t.Fatal(err)
	}
	actual, receipt, err := n.nodes[3].ReadLocalCut()
	if err != nil || receipt.ControlSlot != 3 || !bytes.Equal(s2TestEncode(t, actual, n.fixture.genesis.roots), s2TestEncode(t, want, n.fixture.genesis.roots)) {
		t.Fatal("native full-history catch-up differs after cold restart", receipt, err)
	}
	s2cGateOutcome(t, n, 3, id, value, 1)
}

type s2cGateFiles map[string][]byte

func s2cGateSnapshot(t *testing.T, config s2cParticipantConfig) s2cGateFiles {
	t.Helper()
	files := s2cGateFiles{}
	for _, path := range []string{config.PPath, config.PPath + ".tip", config.BPath, config.BPath + ".tip"} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files[path] = body
	}
	return files
}

func s2cGateRestore(t *testing.T, files s2cGateFiles, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if err := os.WriteFile(path, files[path], 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func s2cGateRejectResume(t *testing.T, config s2cParticipantConfig, p s2cJournalFloor, b s2LocalReceipt) {
	t.Helper()
	before := s2cGateSnapshot(t, config)
	o, err := resumeS2CParticipant(config, p, b)
	if o != nil {
		_ = o.Close()
	}
	if err == nil || o != nil {
		t.Fatal("unqualified native recovery exposed an owner", err)
	}
	for path, old := range before {
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(old, actual) {
			t.Fatal("rejected recovery changed retained evidence", path, err)
		}
	}
}

func TestS2CGateIndependentRollbackFloors(t *testing.T) {
	t.Run("P-forgotten-promise-with-unchanged-B", func(t *testing.T) {
		n := s2cTestNativeCluster(t, 3, nil)
		config := n.configs[1]
		old := s2cGateSnapshot(t, config)
		_, oldB, _ := n.nodes[1].Floors()
		n.begin(3, [32]byte{})
		n.send(s2cPrepare, 3, 1)
		p, b, err := n.nodes[1].Floors()
		if err != nil || b != oldB {
			t.Fatal("P-only promise advanced B", err)
		}
		n.stop(1)
		s2cGateRestore(t, old, config.PPath, config.PPath+".tip")
		s2cGateRejectResume(t, config, p, b)
	})
	t.Run("known-B-rollback", func(t *testing.T) {
		n := s2cTestNativeCluster(t, 3, nil)
		config := n.configs[1]
		old := s2cGateSnapshot(t, config)
		n.selectValue(1, [32]byte{}, 1, 2)
		n.choose(1, 1, 2)
		p, b, err := n.nodes[1].Floors()
		if err != nil || b.ControlSlot != 1 {
			t.Fatal(err)
		}
		n.stop(1)
		s2cGateRestore(t, old, config.BPath, config.BPath+".tip")
		s2cGateRejectResume(t, config, p, b)
	})
	t.Run("floor-is-contained-never-lowered", func(t *testing.T) {
		n := s2cTestNativeCluster(t, 3, nil)
		p0, b0, _ := n.nodes[1].Floors()
		n.selectValue(1, [32]byte{}, 1, 2)
		n.choose(1, 1, 2)
		p1, b1, _ := n.nodes[1].Floors()
		n.stop(1)
		o, err := resumeS2CParticipant(n.configs[1], p0, b0)
		if err != nil {
			t.Fatal("containing stronger prefix", err)
		}
		n.nodes[1] = o
		p, b, err := o.Floors()
		if err != nil || p != p1 || b != b1 {
			t.Fatal("resume lowered a retained floor", err)
		}
	})
}

func TestS2CGateProcessCrashCuts(t *testing.T) {
	if dir := os.Getenv("LANTERN_S2C_CRASH_DIR"); dir != "" {
		f := s2cTestCluster(t, 3)
		config := s2cTestConfig(t, f, 1, dir)
		o, err := resumeS2CParticipant(config, s2cJournalFloor{}, s2LocalReceipt{})
		if err != nil {
			t.Fatal("child native resume", err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "incoming-choice"))
		if err != nil {
			t.Fatal(err)
		}
		exit := func() { os.Exit(73) }
		o.hooks = &s2cParticipantHooks{}
		switch os.Getenv("LANTERN_S2C_CRASH_STAGE") {
		case "beforeChosen":
			o.hooks.beforePAppend = func(kind byte) {
				if kind == s2cPChosen {
					exit()
				}
			}
		case "afterChosenAppend":
			o.hooks.afterPAppend = func(kind byte) {
				if kind == s2cPChosen {
					exit()
				}
			}
		case "afterChosen":
			o.hooks.afterChosen = exit
		case "beforeB":
			o.hooks.beforeB = exit
		case "afterB":
			o.hooks.afterB = exit
		case "beforeDrained":
			o.hooks.beforeDrained = exit
		case "afterDrainedAppend":
			o.hooks.afterPAppend = func(kind byte) {
				if kind == s2cPDrained {
					exit()
				}
			}
		case "afterDrained":
			o.hooks.afterDrained = exit
		default:
			t.Fatal("unknown child cut")
		}
		_, err = o.Receive(raw)
		t.Fatal("process did not reach requested crash cut", err)
	}
	for _, stage := range []string{"beforeChosen", "afterChosenAppend", "afterChosen", "beforeB", "afterB", "beforeDrained", "afterDrainedAppend", "afterDrained"} {
		t.Run(stage, func(t *testing.T) {
			n := s2cTestNativeCluster(t, 3, nil)
			id, value := s2cGateOrigin(t, n, 2, 1, "process_original")
			n.selectValue(2, value, 2, 3)
			n.choose(2, 2, 3)
			incoming := n.take(s2cChosen, 2, 1)
			config := n.configs[1]
			p, b, err := n.nodes[1].Floors()
			if err != nil {
				t.Fatal(err)
			}
			n.stop(1)
			dir := filepath.Dir(config.PPath)
			if err = os.WriteFile(filepath.Join(dir, "incoming-choice"), incoming.raw, 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestS2CGateProcessCrashCuts$")
			command.Env = append(os.Environ(), "LANTERN_S2C_CRASH_DIR="+dir, "LANTERN_S2C_CRASH_STAGE="+stage)
			output, err := command.CombinedOutput()
			var exited *exec.ExitError
			if !errors.As(err, &exited) || exited.ExitCode() != 73 {
				t.Fatalf("child crash: %v\n%s", err, output)
			}
			if err = os.Remove(filepath.Join(dir, "incoming-choice")); err != nil {
				t.Fatal(err)
			}
			before := s2cGateSnapshot(t, config)
			// Every post-CHOSEN cut must resume using P alone: the child is
			// dead, the delivered message file is gone, and no proof is passed.
			o, err := resumeS2CParticipant(config, p, b)
			if err != nil {
				t.Fatal("cold native crash recovery", err)
			}
			n.nodes[1] = o
			if stage == "beforeChosen" {
				status, _, _, err := o.LookupOriginal(id)
				if err != nil || status != S1Unresolved {
					t.Fatal("choice published before durable CHOSEN", status, err)
				}
				if err := n.deliver(incoming); err != nil {
					t.Fatal("stable delivery after pre-CHOSEN crash", err)
				}
			}
			incoming = s2cTestEnvelope{}
			s2cGateOutcome(t, n, 1, id, value, 1)
			_, count, err := s2LocalFiles(config.BPath, config.BScope.Storage)
			if err != nil || count != 2 {
				t.Fatal("B APPLY missing or duplicated", count, err)
			}
			if stage == "afterB" || stage == "beforeDrained" || stage == "afterDrainedAppend" || stage == "afterDrained" {
				for _, path := range []string{config.BPath, config.BPath + ".tip"} {
					after, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(before[path], after) {
						t.Fatal("complete B was appended twice", stage, err)
					}
				}
			}
			n.queue = nil
			n.restart(1)
			s2cGateOutcome(t, n, 1, id, value, 1)
		})
	}
}

func TestS2CGateClosedNativeRecovery(t *testing.T) {
	for _, damage := range []string{"P-torn", "B-torn", "B-ahead-of-chosen"} {
		t.Run(damage, func(t *testing.T) {
			n := s2cTestNativeCluster(t, 3, nil)
			config := n.configs[1]
			n.selectValue(1, [32]byte{}, 1, 2)
			n.send(s2cAccept, 1, 1)
			n.send(s2cAccept, 1, 2)
			beforeChosen := s2cGateSnapshot(t, config)
			n.send(s2cAccepted, 2, 1)
			n.send(s2cAccepted, 1, 1)
			n.stop(1)
			switch damage {
			case "B-ahead-of-chosen":
				s2cGateRestore(t, beforeChosen, config.PPath, config.PPath+".tip")
			default:
				path := config.PPath
				if damage == "B-torn" {
					path = config.BPath
				}
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = file.Write([]byte{0xff, 0x01, 0x02}); err != nil {
					t.Fatal(err)
				}
				if err = file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			s2cGateRejectResume(t, config, s2cJournalFloor{}, s2LocalReceipt{})
		})
	}
}

// Measure real B framing from the pure evaluator, without constructing a
// certificate or calling ApplyS1/PrepareNext before a genuine quorum exists.
func s2cGateBNeed(t *testing.T, trust *s2cTrust, scope s2LocalScope, historical *s2cHistoricalH) uint64 {
	t.Helper()
	g := trust.genesis
	value := trust.noopValue()
	var h *S1Handoff
	if historical != nil {
		h, value = historical.handoff, historical.digest()
	}
	commit := s2cLogicalCommit(trust, 1, value)
	result, err := evaluateS1Candidate(g.state, h, commit, g.state.prefix)
	if err != nil {
		t.Fatal(err)
	}
	initial := s2TestEncode(t, g.state, g.roots)
	successor := s2TestEncode(t, result.State, g.roots)
	first := &s2LocalRecord{kind: s2LocalGenesis, scope: scope, scopeDigest: scope.digest(), localIndex: 1, capsuleDigest: s2LocalCapsuleDigest(initial), capsule: string(initial)}
	second := &s2LocalRecord{kind: s2LocalApply, scopeDigest: scope.digest(), localIndex: 2, previousCapsuleDigest: first.capsuleDigest, capsuleDigest: s2LocalCapsuleDigest(successor), commit: commit, capsule: string(successor)}
	a, err := s2EncodeLocalRecord(first, scope)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s2EncodeLocalRecord(second, scope)
	if err != nil {
		t.Fatal(err)
	}
	return 85 + uint64(len(a)) + 88 + uint64(len(b)) + 88
}

func s2cGateBBudget(t *testing.T, trust *s2cTrust, scope s2LocalScope, historical *s2cHistoricalH, deficit uint64) s2LocalScope {
	t.Helper()
	scope.Storage.ReplayRecords = 2
	center := s2cGateBNeed(t, trust, scope, historical)
	// GENESIS contains policy, whose digest has variable-width JSON bytes.
	// Search an actual fixed point rather than pretend a decremented quota
	// leaves its own journal framing unchanged.
	for salt := byte(1); salt < 17; salt++ {
		scope.StoreIdentity[2] = salt
		for budget := center - 256; budget <= center+256; budget++ {
			scope.Storage.JournalBytes = budget
			if s2cGateBNeed(t, trust, scope, historical) == budget+deficit {
				return scope
			}
		}
	}
	t.Fatal("no exact native B byte-boundary fixture")
	return scope
}

func TestS2CGateExactCompletionBudget(t *testing.T) {
	for _, mode := range []string{"exact", "B-one-byte-short", "B-one-record-short", "P-one-record-short", "B-success-before-DRAINED"} {
		t.Run(mode, func(t *testing.T) {
			f := s2cTestCluster(t, 3)
			op := s1Operation(t, f.genesis.state.projection, testIdentity(), s1Changes(s1ReaderRole()))
			id := FullChangeID{S1Version, op.reviewed.Domain, op.reviewed.Cohort, 1, [16]byte{1}}
			raw := s2cTestSeal(t, f.trust, 2, f.keys[2], id, op, 1, false)
			h, err := verifyHistoricalH(f.trust, raw)
			if err != nil {
				t.Fatal(err)
			}
			n := s2cTestNativeCluster(t, 3, func(member uint32, config *s2cParticipantConfig) {
				if member != 1 {
					return
				}
				deficit := uint64(0)
				if mode == "B-one-byte-short" {
					deficit = 1
				}
				config.BScope = s2cGateBBudget(t, config.Trust, config.BScope, h, deficit)
				if mode == "B-one-record-short" {
					config.BScope.Storage.ReplayRecords = 1
				}
				if mode == "P-one-record-short" {
					// Accepter-only: bootstrap 2 + ACCEPT 1 + completion 2.
					config.PPolicy.Records, config.PendingCount = 4, 1
				}
			})
			value, err := n.nodes[2].persistSealedOriginH(raw)
			if err != nil || value != h.digest() {
				t.Fatal(err)
			}
			n.selectValue(2, value, 2, 3)
			p, b, _ := n.nodes[1].Floors()
			err = n.deliver(n.take(s2cAccept, 2, 1))
			short := mode == "B-one-byte-short" || mode == "B-one-record-short" || mode == "P-one-record-short"
			if short {
				if err == nil {
					t.Fatal("insufficient completion quota issued Accepted")
				}
				p2, b2, e := n.nodes[1].Floors()
				if e != nil || p != p2 || b != b2 {
					t.Fatal("definite capacity refusal changed either journal", e)
				}
			} else if err != nil {
				t.Fatal("exact completion capacity rejected", err)
			}
			n.choose(2, 2, 3)
			chosen := n.take(s2cChosen, 2, 1)
			if short && mode != "P-one-record-short" {
				err = n.deliver(chosen)
				var blocked *s2LocalBlockedError
				if !errors.As(err, &blocked) || blocked.Commit != s2cLogicalCommit(f.trust, 1, value) {
					t.Fatal("capacity refusal changed or lost the genuine decision", err)
				}
				status, outcome, _, err := n.nodes[1].LookupOriginal(id)
				if err != nil || status != S1Unresolved || outcome != nil {
					t.Fatal("capacity failure became a terminal outcome", status, err)
				}
				return
			}
			// Learning a genuine choice needs one fewer P record than first
			// accepting it. The four-record learner may therefore finish even
			// though its earlier Accepted response was correctly withheld.
			if mode == "B-success-before-DRAINED" {
				p, b, _ = n.nodes[1].Floors()
				n.nodes[1].hooks = &s2cParticipantHooks{afterB: func() { panic("B durable, DRAINED absent") }}
				panicked := false
				func() {
					defer func() { panicked = recover() != nil }()
					_ = n.deliver(chosen)
				}()
				if !panicked {
					t.Fatal("did not reach the exact-budget B completion cut")
				}
				if _, _, err := n.nodes[1].ReadLocalCut(); err == nil {
					t.Fatal("uncertain composite exposed B")
				}
				files := s2cGateSnapshot(t, n.configs[1])
				n.stop(1)
				o, err := resumeS2CParticipant(n.configs[1], p, b)
				if err != nil {
					t.Fatal("consumed B credit counted twice on exact-budget recovery", err)
				}
				n.nodes[1] = o
				for _, path := range []string{n.configs[1].BPath, n.configs[1].BPath + ".tip"} {
					actual, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(files[path], actual) {
						t.Fatal("exact-budget B appended twice", err)
					}
				}
			} else if err = n.deliver(chosen); err != nil {
				t.Fatal("exact-budget genuine choice", err)
			}
			s2cGateOutcome(t, n, 1, id, value, 1)
			o := n.nodes[1]
			if o.b.used != n.configs[1].BScope.Storage.JournalBytes || o.b.receipt.LocalIndex != 2 || o.credit != (s2cCredit{}) {
				t.Fatal("completion did not consume the exact native budget", o.b.used, o.credit)
			}
			used := o.b.used
			n.restart(1)
			if n.nodes[1].b.used != used || n.nodes[1].credit != (s2cCredit{}) {
				t.Fatal("cold restart refilled consumed storage")
			}
		})
	}
}

func TestS2CGateChurnPreservesMaximumCredit(t *testing.T) {
	n := s2cTestNativeCluster(t, 3, func(member uint32, config *s2cParticipantConfig) {
		if member == 1 {
			config.PPolicy.Records, config.PendingCount = 8, 1
		}
	})
	var changes []Change
	for i := 0; i < 10; i++ {
		change := s1ReaderRole()
		change.Role.ID = fmt.Sprintf("large_role_%02d", i)
		changes = append(changes, change)
	}
	state := n.fixture.genesis.state
	op := s1Operation(t, state.projection, testIdentity(), s1Changes(changes...))
	id := FullChangeID{S1Version, op.reviewed.Domain, op.reviewed.Cohort, 1, [16]byte{1}}
	raw := s2cTestSeal(t, n.fixture.trust, 2, n.fixture.keys[2], id, op, 1, false)
	value, err := n.nodes[2].persistSealedOriginH(raw)
	if err != nil {
		t.Fatal(err)
	}
	n.selectValue(2, value, 1, 2)
	n.send(s2cAccept, 2, 1) // Large H is accepted at A only, never chosen.
	o := n.nodes[1]
	oldCredit, oldUsed, oldCount := o.credit, o.p.used, o.p.count
	if oldCredit.PBytes == 0 || oldCredit.BBytes == 0 {
		t.Fatal("large acceptance has no recoverable credit")
	}
	n.queue = nil
	// B/C report no accepted value. Their independent higher ballot may select
	// NOOP, while A must retain the conservative maximum and old actual bytes.
	n.selectValue(3, [32]byte{}, 2, 3)
	accept := n.take(s2cAccept, 3, 1)
	if err := n.deliver(accept); err != nil {
		t.Fatal(err)
	}
	charge, _ := s2cPCharge(uint64(len(accept.raw)))
	if o.p.used != oldUsed+charge || o.p.count != oldCount+1 || o.credit != oldCredit {
		t.Fatal("replacement lost retained ACCEPT bytes or maximum completion credit", oldUsed, o.p.used, oldCredit, o.credit)
	}
	if !bytes.Contains(s2RestoreTestReadFile(t, n.configs[1].PPath), raw) {
		t.Fatal("old full H removed from retained journal")
	}
	n.send(s2cAccept, 3, 3)
	// Fresh promises may consume only the ordinary pool. One fits; the next
	// must stop, leaving exactly CHOSEN+DRAINED's protected record allowance.
	n.begin(2, [32]byte{})
	n.send(s2cPrepare, 2, 1)
	n.begin(2, [32]byte{})
	before, _, _ := o.Floors()
	if err := n.deliver(n.take(s2cPrepare, 2, 1)); err == nil {
		t.Fatal("preemption stole protected completion records")
	}
	after, _, err := o.Floors()
	if err != nil || before != after || o.credit != oldCredit {
		t.Fatal("failed churn append changed protected state", err)
	}
	n.send(s2cAccepted, 3, 3)
	n.send(s2cAccepted, 1, 3)
	n.send(s2cChosen, 3, 1)
	if o.p.count != 8 || o.credit != (s2cCredit{}) || o.b.receipt.ControlSlot != 1 {
		t.Fatal("protected completion failed at finite record limit", o.p.count, o.credit)
	}
	status, outcome, _, err := o.LookupOriginal(id)
	if err != nil || status != S1Unresolved || outcome != nil {
		t.Fatal("superseded unchosen H became an outcome", status, err)
	}
	used := o.p.used
	n.queue = nil
	n.restart(1)
	o = n.nodes[1]
	if o.p.used != used || o.p.count != 8 || o.credit != (s2cCredit{}) {
		t.Fatal("restart refunded retained bytes or records")
	}
	if out, err := o.Begin([32]byte{}); err == nil || len(out) != 0 {
		t.Fatal("finite exhausted journal claimed new-round liveness", err)
	}
}

// A completed fsync is assumed to survive in the supported storage model.
// File-prefix rollback without an independent floor and hardware power loss
// cannot be established by these native process/file tests. The process crash
// gates exercise real exit without Close; stage callbacks are not injected
// physical write/fsync failures and are not claimed as power-loss evidence.
