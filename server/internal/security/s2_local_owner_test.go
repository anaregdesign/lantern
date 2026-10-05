package security

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/mutationlog"
)

func TestS2LocalOwnerCompleteMaterialization(t *testing.T) {
	g, want, history, original := s2TestHistory(t)
	scope := s2OwnerTestScope(g)
	path := filepath.Join(t.TempDir(), "materialized.wal")
	o, err := createS2Local(path, scope, g)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	_, genesis, err := o.ReadLocalCut()
	if err != nil || genesis.LocalIndex != 1 || genesis.ControlSlot != 0 {
		t.Fatal(genesis, err)
	}
	for i, next := range history {
		p, err := o.PrepareNext(next)
		if err != nil {
			t.Fatal(i, err)
		}
		before := o.used
		charge := uint64(len(o.pending.payload)) + 88
		if o.pending.charge != charge {
			t.Fatal("inexact reservation")
		}
		r, err := o.CommitPrepared(p)
		if err != nil {
			t.Fatal(i, err)
		}
		if r.LocalIndex != uint64(i)+2 || r.ControlSlot != uint64(i)+1 || r.ScopeDigest != scope.digest() {
			t.Fatal(r)
		}
		actual, count, err := s2LocalFiles(path, scope.Storage)
		if err != nil || actual != before+charge || actual != o.used || count != r.LocalIndex {
			t.Fatal(actual, count, err)
		}
		m := o.metadata.Snapshot()
		if m.Revision != r.LocalIndex || s2LocalCapsuleDigest(m.Value) != r.CapsuleDigest {
			t.Fatal("metadata split")
		}
		if seq, ok := o.log.LastSeq(); !ok || seq != r.LocalIndex {
			t.Fatal("ring split")
		}
		if _, err = o.CommitPrepared(p); !errors.Is(err, errS2LocalPlan) {
			t.Fatal("plan reused", err)
		}
	}
	state, receipt, err := o.ReadLocalCut()
	if err != nil || !bytes.Equal(s2TestEncode(t, state, g.roots), s2TestEncode(t, want, g.roots)) {
		t.Fatal("aggregate differs", err)
	}
	status, outcome, lookupReceipt, err := o.LookupOriginal(original.id)
	if err != nil || status != S1Known || lookupReceipt != receipt || outcome.commit != history[1].certificate.commit || outcome.handoff != original.digest() {
		t.Fatal(status, outcome, err)
	}
	// Full original H was consumed before its historical deadlines. No current
	// clock is sampled or replacement proof minted by local materialization.
	outcome.items[0].Kind = "detached"
	delete(state.ledger, original.id)
	state.projection.lineage[s1Bob()] = 999
	_, again, _, err := o.LookupOriginal(original.id)
	if err != nil || again.items[0].Kind == "detached" {
		t.Fatal("original escaped", err)
	}
	state, _, err = o.ReadLocalCut()
	if err != nil || state.projection.lineage[s1Bob()] == 999 || state.ledger[original.id] == nil {
		t.Fatal("state escaped", err)
	}
}

func TestS2LocalOwnerPlanFences(t *testing.T) {
	g, _, history, _ := s2TestHistory(t)
	scope := s2OwnerTestScope(g)
	path := filepath.Join(t.TempDir(), "owner.wal")
	o, err := createS2Local(path, scope, g)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	_, floor, _ := o.ReadLocalCut()
	p, err := o.PrepareNext(history[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = o.PrepareNext(history[0]); !errors.Is(err, errS2LocalPlan) {
		t.Fatal("competing prepare", err)
	}
	for _, bad := range []*s2LocalPlan{nil, {owner: o, serial: p.serial}, {owner: &s2LocalOwner{}, serial: p.serial}, {owner: o, serial: p.serial + 1}} {
		if _, err = o.CommitPrepared(bad); !errors.Is(err, errS2LocalPlan) {
			t.Fatal("forged token", err)
		}
	}
	p.serial++
	if _, err = o.CommitPrepared(p); !errors.Is(err, errS2LocalPlan) {
		t.Fatal("changed serial", err)
	}
	p.serial--
	if err = o.DiscardLocalPlan(p); err != nil {
		t.Fatal(err)
	}
	reprepared, err := o.PrepareNext(history[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = o.CommitPrepared(p); !errors.Is(err, errS2LocalPlan) {
		t.Fatal("released token resurrected", err)
	}
	if err = o.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := resumeS2Local(path, scope, g, nil, floor)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if _, err = resumed.CommitPrepared(reprepared); !errors.Is(err, errS2LocalPlan) {
		t.Fatal("old incarnation token", err)
	}
	fresh, err := resumed.PrepareNext(history[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = resumed.CommitPrepared(fresh); err != nil {
		t.Fatal(err)
	}
}

// Policy appears in GENESIS, so solve its own encoded length before testing
// the exact file-content boundary. This is not a physical-space reservation.
func s2OwnerExactBudget(t *testing.T, g *s2TrustedGenesis, next S1CertifiedNext) s2LocalScope {
	return s2OwnerBudget(t, g, next, 0)
}

func s2OwnerBudget(t *testing.T, g *s2TrustedGenesis, next S1CertifiedNext, short uint64) s2LocalScope {
	t.Helper()
	scope := s2OwnerTestScope(g)
	scope.Storage.ReplayRecords = 8
	result, err := ApplyS1(g.state, next)
	if err != nil {
		t.Fatal(err)
	}
	genesis := s2TestEncode(t, g.state, g.roots)
	successor := s2TestEncode(t, result.State, g.roots)
	measure := func() uint64 {
		first := &s2LocalRecord{kind: s2LocalGenesis, scope: scope, scopeDigest: scope.digest(), localIndex: 1, capsuleDigest: s2LocalCapsuleDigest(genesis), capsule: string(genesis)}
		second := &s2LocalRecord{kind: s2LocalApply, scopeDigest: scope.digest(), localIndex: 2, previousCapsuleDigest: first.capsuleDigest, capsuleDigest: s2LocalCapsuleDigest(successor), commit: next.certificate.commit, capsule: string(successor)}
		a, e := s2EncodeLocalRecord(first, scope)
		if e != nil {
			t.Fatal(e)
		}
		b, e := s2EncodeLocalRecord(second, scope)
		if e != nil {
			t.Fatal(e)
		}
		return uint64(85 + len(a) + 88 + len(b) + 88)
	}
	// Digest arrays have variable-width decimal JSON elements, so changing the
	// budget can change the next header's length. Search an exact native byte
	// boundary instead of assuming decrementing a policy preserves its bytes.
	center := measure()
	for salt := byte(1); salt < 17; salt++ {
		scope.StoreIdentity[1] = salt
		for n := center - 256; n <= center+256; n++ {
			scope.Storage.JournalBytes = n
			if measure() == n+short {
				return scope
			}
		}
	}
	t.Fatal("no exact budget fixture")
	return scope
}

func TestS2LocalOwnerExactQuotaAndFiniteExhaustion(t *testing.T) {
	g, _, history, _ := s2TestHistory(t)

	for _, short := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact", true: "one-byte-short"}[short], func(t *testing.T) {
			deficit := uint64(0)
			if short {
				deficit = 1
			}
			scope := s2OwnerBudget(t, g, history[0], deficit)
			path := filepath.Join(t.TempDir(), "quota.wal")
			o, err := createS2Local(path, scope, g)
			if err != nil {
				t.Fatal(err)
			}
			defer o.Close()
			before, _, _ := s2LocalFiles(path, scope.Storage)
			p, err := o.PrepareNext(history[0])
			if short {
				var blocked *s2LocalBlockedError
				if p != nil || !errors.Is(err, errS2BlockedSameDecision) || !errors.As(err, &blocked) || blocked.Commit != history[0].certificate.commit {
					t.Fatal("not same certified decision", p, err)
				}
				after, _, e := s2LocalFiles(path, scope.Storage)
				if e != nil || after != before {
					t.Fatal("preflight wrote bytes", after, e)
				}
				if _, r, e := o.ReadLocalCut(); e != nil || r.LocalIndex != 1 {
					t.Fatal(r, e)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if before+o.pending.charge != scope.Storage.JournalBytes {
				t.Fatal("charge is not exact")
			}
			if _, err = o.CommitPrepared(p); err != nil {
				t.Fatal(err)
			}
			if o.used != scope.Storage.JournalBytes {
				t.Fatal(o.used)
			}
			if p, err = o.PrepareNext(history[1]); p != nil || !errors.Is(err, errS2BlockedSameDecision) {
				t.Fatal("finite exhaustion changed choice", p, err)
			}
		})
	}
}

func TestS2LocalOwnerPublicationPanicRequiresRecovery(t *testing.T) {
	for _, phase := range []string{"beforePublish", "afterMetadata", "afterState", "afterLog"} {
		t.Run(phase, func(t *testing.T) {
			g, _, history, _ := s2TestHistory(t)
			scope := s2OwnerTestScope(g)
			path := filepath.Join(t.TempDir(), "panic.wal")
			o, err := createS2Local(path, scope, g)
			if err != nil {
				t.Fatal(err)
			}
			_, floor, _ := o.ReadLocalCut()
			p, err := o.PrepareNext(history[0])
			if err != nil {
				t.Fatal(err)
			}
			before := o.used
			crash := func() { panic(errors.Join(mutationlog.ErrClosed, mutationlog.ErrWALIndeterminate)) }
			o.hooks = &s2LocalHooks{}
			switch phase {
			case "beforePublish":
				o.hooks.beforePublish = crash
			case "afterMetadata":
				o.hooks.afterMetadata = crash
			case "afterState":
				o.hooks.afterState = crash
			case "afterLog":
				o.hooks.afterLog = crash
			}
			func() {
				defer func() {
					if recover() == nil {
						t.Fatal("no panic")
					}
				}()
				_, _ = o.CommitPrepared(p)
			}()
			if _, _, err = o.ReadLocalCut(); !errors.Is(err, errS2LocalUnknown) {
				t.Fatal("healthy read after panic", err)
			}
			if _, err = o.PrepareNext(history[0]); !errors.Is(err, errS2LocalUnknown) {
				t.Fatal("healthy prepare after panic", err)
			}
			if _, err = o.CommitPrepared(p); !errors.Is(err, errS2LocalUnknown) {
				t.Fatal("sequence reused after panic", err)
			}
			if err = o.DiscardLocalPlan(p); !errors.Is(err, errS2LocalUnknown) {
				t.Fatal("charge released", err)
			}
			if o.pending == nil || o.used != before {
				t.Fatal("uncertain accounting was released")
			}
			if err = o.Close(); err != nil {
				t.Fatal(err)
			}
			resumed, err := resumeS2Local(path, scope, g, history[:1], floor)
			if err != nil {
				t.Fatal(err)
			}
			defer resumed.Close()
			state, r, err := resumed.ReadLocalCut()
			want, e := ApplyS1(g.state, history[0])
			if err != nil || e != nil || r.LocalIndex != 2 || !bytes.Equal(s2TestEncode(t, state, g.roots), s2TestEncode(t, want.State, g.roots)) {
				t.Fatal("incomplete recovered aggregate", r, err, e)
			}
		})
	}
}

func TestS2LocalOwnerCloseClosesAdmissionBeforeDraining(t *testing.T) {
	g, _, history, _ := s2TestHistory(t)
	scope := s2OwnerTestScope(g)
	dir := t.TempDir()
	path := filepath.Join(dir, "close.wal")
	o, err := createS2Local(path, scope, g)
	if err != nil {
		t.Fatal(err)
	}
	p, err := o.PrepareNext(history[0])
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	o.hooks = &s2LocalHooks{afterMetadata: func() { close(entered); <-release }}
	committed := make(chan error, 1)
	go func() { _, err := o.CommitPrepared(p); committed <- err }()
	<-entered
	read := make(chan error, 1)
	go func() { _, _, err := o.ReadLocalCut(); read <- err }()
	select {
	case err := <-read:
		t.Fatal("read escaped split publication", err)
	default:
	}
	closed := make(chan error, 1)
	go func() { closed <- o.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	for !o.admissionClosed.Load() {
		if time.Now().After(deadline) {
			t.Fatal("close admission stalled")
		}
		runtime.Gosched()
	}
	if _, err = o.PrepareNext(history[0]); !errors.Is(err, errS2LocalClosed) {
		t.Fatal("late admission", err)
	}
	aliasPath := path
	if runtime.GOOS != "windows" {
		alias := filepath.Join(t.TempDir(), "alias")
		if err = os.Symlink(dir, alias); err != nil {
			t.Fatal(err)
		}
		aliasPath = filepath.Join(alias, "close.wal")
	}
	if lease, err := mutationlog.AcquireFileWALLease(aliasPath); !errors.Is(err, mutationlog.ErrFileWALLeaseBusy) {
		if lease != nil {
			_ = lease.Close()
		}
		t.Fatal("lease escaped append", err)
	}
	select {
	case err := <-closed:
		t.Fatal("close overtook append", err)
	default:
	}
	close(release)
	if err = <-committed; err != nil {
		t.Fatal(err)
	}
	if err = <-closed; err != nil {
		t.Fatal(err)
	}
	if err = <-read; !errors.Is(err, errS2LocalClosed) {
		t.Fatal("queued read entered behind close", err)
	}
	if err = o.Close(); err != nil {
		t.Fatal("non-idempotent close", err)
	}
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatal("lease retained after drain", err)
	}
	_ = lease.Close()
	if _, err = os.Stat(path + ".lease"); err != nil {
		t.Fatal("lease inode removed", err)
	}
}

// TestS2LocalProcessCrash exercises actual process exit with no Close/defer
// cleanup. Readable process-cache bytes are still qualified by Resume's real
// file barriers; this is not a claim about hardware power loss.
func TestS2LocalProcessCrash(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("native lease unavailable")
	}
	if path := os.Getenv("LANTERN_S2_LOCAL_CRASH_PATH"); path != "" {
		g, _, proof, _ := s2TestHistory(t)
		scope := s2OwnerTestScope(g)
		o, err := createS2Local(path, scope, g)
		if err != nil {
			t.Fatal(err)
		}
		_, required, err := o.ReadLocalCut()
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(required); err != nil {
			t.Fatal(err)
		}
		plan, err := o.PrepareNext(proof[0])
		if err != nil {
			t.Fatal(err)
		}
		exit := func() { os.Exit(23) }
		o.hooks = &s2LocalHooks{}
		switch os.Getenv("LANTERN_S2_LOCAL_CRASH_STAGE") {
		case "beforeAppend":
			o.hooks.beforeAppend = exit
		case "beforePublish":
			o.hooks.beforePublish = exit
		case "afterMetadata":
			o.hooks.afterMetadata = exit
		case "afterState":
			o.hooks.afterState = exit
		case "afterLog":
			o.hooks.afterLog = exit
		case "holdBeforePublish":
			o.hooks.beforePublish = func() { fmt.Fprintln(os.Stdout, "held"); select {} }
		default:
			t.Fatal("unknown crash stage")
		}
		if _, err := o.CommitPrepared(plan); err != nil {
			t.Fatal(err)
		}
		t.Fatal("crash hook did not run")
	}
	for _, stage := range []string{"beforeAppend", "beforePublish", "afterMetadata", "afterState", "afterLog"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "materialized.wal")
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestS2LocalProcessCrash$")
			cmd.Env = append(os.Environ(), "LANTERN_S2_LOCAL_CRASH_PATH="+path, "LANTERN_S2_LOCAL_CRASH_STAGE="+stage)
			out, err := cmd.Output()
			var exited *exec.ExitError
			if !errors.As(err, &exited) || exited.ExitCode() != 23 {
				t.Fatalf("child exit: %v output %s", err, out)
			}
			var required s2LocalReceipt
			if err := json.Unmarshal(bytes.TrimSpace(out), &required); err != nil {
				t.Fatal(err, string(out))
			}
			g, _, proof, _ := s2TestHistory(t)
			scope := s2OwnerTestScope(g)
			expected := g.state
			if stage == "beforeAppend" {
				proof = proof[:0]
			} else {
				proof = proof[:1]
				applied, err := ApplyS1(g.state, proof[0])
				if err != nil {
					t.Fatal(err)
				}
				expected = applied.State
			}
			before, err := os.Stat(path + ".tip")
			if err != nil {
				t.Fatal(err)
			}
			o, err := resumeS2Local(path, scope, g, proof, required)
			if err != nil {
				t.Fatal("resume after process exit", err)
			}
			defer o.Close()
			got, receipt, err := o.ReadLocalCut()
			if err != nil || receipt.LocalIndex != uint64(len(proof)+1) || receipt.ControlSlot != uint64(len(proof)) {
				t.Fatal("recovered cut", receipt, err)
			}
			if !bytes.Equal(s2TestEncode(t, got, g.roots), s2TestEncode(t, expected, g.roots)) {
				t.Fatal("recovered split or stale state")
			}
			after, err := os.Stat(path + ".tip")
			if err != nil || before.Size() != after.Size() {
				t.Fatal("complete tip duplicated", err)
			}
			if err := o.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path + ".lease"); err != nil {
				t.Fatal("lease inode removed", err)
			}
			reopened, err := resumeS2Local(path, scope, g, proof, receipt)
			if err != nil {
				t.Fatal("independent recovered floor", err)
			}
			if err := reopened.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestS2LocalCrossProcessAliasLease(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("directory alias test requires Unix native lease")
	}
	path := filepath.Join(t.TempDir(), "materialized.wal")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestS2LocalProcessCrash$")
	cmd.Env = append(os.Environ(), "LANTERN_S2_LOCAL_CRASH_PATH="+path, "LANTERN_S2_LOCAL_CRASH_STAGE=holdBeforePublish")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var required s2LocalReceipt
	if err := json.Unmarshal(line, &required); err != nil {
		t.Fatal(err, string(line))
	}
	line, err = reader.ReadBytes('\n')
	if err != nil || string(line) != "held\n" {
		t.Fatal("child publication pause", string(line), err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Dir(path), alias); err != nil {
		t.Fatal(err)
	}
	aliasPath := filepath.Join(alias, filepath.Base(path))
	g, _, proof, _ := s2TestHistory(t)
	scope := s2OwnerTestScope(g)
	contender, err := resumeS2Local(aliasPath, scope, g, proof[:1], required)
	if contender != nil {
		_ = contender.Close()
		t.Fatal("alias escaped live process lease")
	}
	if !errors.Is(err, mutationlog.ErrFileWALLeaseBusy) {
		t.Fatal("alias contender was not lease-blocked", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed child succeeded")
	}
	o, err := resumeS2Local(aliasPath, scope, g, proof[:1], required)
	if err != nil {
		t.Fatal("crashed owner retained lease", err)
	}
	defer o.Close()
	_, receipt, err := o.ReadLocalCut()
	if err != nil || receipt.LocalIndex != 2 {
		t.Fatal("crashed publication not recovered", receipt, err)
	}
}

func TestS2LocalPublicationUncertaintyDominates(t *testing.T) {
	for _, sentinel := range []error{mutationlog.ErrClosed, mutationlog.ErrSeqExhausted, context.Canceled, &mutationlog.DefiniteWALAbort{Cause: errors.New("abort-shaped")}} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reverse=%v", sentinel, reverse), func(t *testing.T) {
				g, _, proof, _ := s2TestHistory(t)
				scope := s2OwnerTestScope(g)
				path := filepath.Join(t.TempDir(), "materialized.wal")
				o, err := createS2Local(path, scope, g)
				if err != nil {
					t.Fatal(err)
				}
				defer o.Close()
				_, floor, err := o.ReadLocalCut()
				if err != nil {
					t.Fatal(err)
				}
				p, err := o.PrepareNext(proof[0])
				if err != nil {
					t.Fatal(err)
				}
				mixed := fmt.Errorf("outer: %w", errors.Join(mutationlog.ErrPublicationInterrupted, sentinel))
				if reverse {
					mixed = errors.Join(sentinel, fmt.Errorf("inner: %w", mutationlog.ErrPublicationInterrupted))
				}
				o.hooks = &s2LocalHooks{beforePublish: func() { panic(mixed) }}
				var raised any
				func() { defer func() { raised = recover() }(); _, _ = o.CommitPrepared(p) }()
				if raised == nil {
					t.Fatal("publication fault did not panic")
				}
				wal := s2RestoreTestReadFile(t, path)
				tip := s2RestoreTestReadFile(t, path+".tip")
				if _, _, err := o.ReadLocalCut(); !errors.Is(err, errS2LocalUnknown) {
					t.Fatal("uncertain owner permitted read", err)
				}
				if _, _, _, err := o.LookupOriginal(proof[0].handoff.id); !errors.Is(err, errS2LocalUnknown) {
					t.Fatal("uncertain owner permitted lookup", err)
				}
				if _, err := o.PrepareNext(proof[0]); !errors.Is(err, errS2LocalUnknown) {
					t.Fatal("uncertain owner permitted preparation", err)
				}
				if _, err := o.CommitPrepared(p); !errors.Is(err, errS2LocalUnknown) {
					t.Fatal("uncertain owner permitted commit", err)
				}
				if err := o.DiscardLocalPlan(p); !errors.Is(err, errS2LocalUnknown) {
					t.Fatal("uncertain owner released charge", err)
				}
				if !bytes.Equal(wal, s2RestoreTestReadFile(t, path)) || !bytes.Equal(tip, s2RestoreTestReadFile(t, path+".tip")) {
					t.Fatal("uncertain owner reused physical sequence")
				}
				if err := o.Close(); err != nil {
					t.Fatal(err)
				}
				r, err := resumeS2Local(path, scope, g, proof[:1], floor)
				if err != nil {
					t.Fatal(err)
				}
				defer r.Close()
				state, receipt, err := r.ReadLocalCut()
				if err != nil || receipt.LocalIndex != 2 || state.slot != 1 {
					t.Fatal("durable uncertain successor not recovered", err)
				}
			})
		}
	}
}

func TestS2LocalOwnerRepresentabilityKeepsSameDecision(t *testing.T) {
	for _, bound := range []string{"replay-records", "capsule-bytes", "capsule-ledger", "S1-control-reserve"} {
		t.Run(bound, func(t *testing.T) {
			g, _, history, _ := s2TestHistory(t)
			if bound == "S1-control-reserve" {
				capacity := g.state.configuration.Capacity
				capacity.LedgerEntries, capacity.RestrictiveEntries = 1, 0
				s, err := NewS1ApplyState(g.state.projection, g.state.membership, capacity, S1Retention{})
				if err != nil {
					t.Fatal(err)
				}
				g = s2TestGenesis(s)
				bob := s1Bob()
				h := s1Seal(s.projection, s1Operation(t, s.projection, testIdentity(), s1Changes(Change{Kind: RevokeSessions, Identity: &bob})), 11, false)
				history[0] = s1Next(s, h)
				applied, err := ApplyS1(s, history[0])
				if err != nil {
					t.Fatal(err)
				}
				h = s1Seal(applied.State.projection, s1Operation(t, applied.State.projection, testIdentity(), s1Changes(Change{Kind: RevokeSessions, Identity: &bob})), 12, false)
				history[1] = s1Next(applied.State, h)
			}
			scope := s2OwnerTestScope(g)
			switch bound {
			case "replay-records":
				scope.Storage.ReplayRecords = 1
			case "capsule-bytes":
				scope.Storage.Capsule.Bytes = uint64(len(s2TestEncode(t, g.state, g.roots)))
			case "capsule-ledger":
				scope.Storage.Capsule.LedgerEntries = 1
			}
			path := filepath.Join(t.TempDir(), "bounded.wal")
			o, err := createS2Local(path, scope, g)
			if err != nil {
				t.Fatal(err)
			}
			defer o.Close()
			next := history[0]
			if bound == "capsule-ledger" || bound == "S1-control-reserve" {
				p, err := o.PrepareNext(next)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = o.CommitPrepared(p); err != nil {
					t.Fatal(err)
				}
				next = history[1]
			}
			before, receipt, err := o.ReadLocalCut()
			if err != nil {
				t.Fatal(err)
			}
			wal, tip := s2RestoreTestReadFile(t, path), s2RestoreTestReadFile(t, path+".tip")
			p, err := o.PrepareNext(next)
			var blocked *s2LocalBlockedError
			if p != nil || !errors.Is(err, errS2BlockedSameDecision) || !errors.As(err, &blocked) || blocked.Commit != next.certificate.commit {
				t.Fatal("choice changed at local bound", p, err)
			}
			if bound == "S1-control-reserve" && !errors.Is(err, ErrControlReserve) {
				t.Fatal("control reserve was replaced by business result", err)
			}
			after, got, err := o.ReadLocalCut()
			if err != nil || got != receipt || !bytes.Equal(s2TestEncode(t, before, g.roots), s2TestEncode(t, after, g.roots)) {
				t.Fatal("blocked decision advanced local state", err)
			}
			if !bytes.Equal(wal, s2RestoreTestReadFile(t, path)) || !bytes.Equal(tip, s2RestoreTestReadFile(t, path+".tip")) {
				t.Fatal("blocked decision changed journal bytes")
			}
		})
	}
}

func TestS2LocalOwnerFreezesPreparedInput(t *testing.T) {
	g, _, history, _ := s2TestHistory(t)
	scope := s2OwnerTestScope(g)
	path := filepath.Join(t.TempDir(), "frozen.wal")
	o, err := createS2Local(path, scope, g)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	_, floor, err := o.ReadLocalCut()
	if err != nil {
		t.Fatal(err)
	}
	p, err := o.PrepareNext(history[0])
	if err != nil {
		t.Fatal(err)
	}
	certificate := *history[0].certificate
	handoff := *history[0].handoff
	history[0].certificate.commit.Value[0] ^= 1
	history[0].handoff.serial++
	receipt, err := o.CommitPrepared(p)
	*history[0].certificate = certificate
	*history[0].handoff = handoff
	if err != nil {
		t.Fatal("commit consumed mutable caller input", err)
	}
	state, got, err := o.ReadLocalCut()
	if err != nil || got != receipt || state.ledger[handoff.id].handoff != handoff.digest() {
		t.Fatal("frozen original H changed", err)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := resumeS2Local(path, scope, g, history[:1], floor)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_, recovered, err := r.ReadLocalCut()
	if err != nil || recovered != receipt {
		t.Fatal("frozen decision did not survive recovery", err)
	}
}

func TestS2LocalRejectsNonemptyLeaseContent(t *testing.T) {
	g, _, _, _ := s2TestHistory(t)
	scope := s2OwnerTestScope(g)
	path := filepath.Join(t.TempDir(), "new.wal")
	if err := os.WriteFile(path+".lease", []byte("unexpected"), 0600); err != nil {
		t.Fatal(err)
	}
	if o, err := createS2Local(path, scope, g); o != nil || err == nil {
		if o != nil {
			_ = o.Close()
		}
		t.Fatal("unaccounted lease content accepted", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid create wrote WAL", err)
	}
	path = filepath.Join(t.TempDir(), "existing.wal")
	receipts := s2RestoreTestWrite(t, path, scope, g, nil)
	if err := os.WriteFile(path+".lease", []byte("unexpected"), 0600); err != nil {
		t.Fatal(err)
	}
	s2RestoreTestReject(t, path, scope, g, nil, receipts[0])
}
