package security

import (
	"errors"
	"os"
	"testing"

	"github.com/anaregdesign/lantern/core/mutationlog"
)

// These cross-component gates exercise real owned native P/B descriptors,
// authenticated outboxes, composite fail-stop, and two-journal recovery. A
// checkpoint panic does not stand in for a hardware power-loss experiment.
func TestS2CIOGateEveryDurableTransitionClosesComposite(t *testing.T) {
	for _, transition := range []struct {
		name string
		kind byte
	}{
		{"ORIGIN_H", s2cPOrigin}, {"BALLOT", s2cPBallot},
		{"PROMISE", s2cPPromise}, {"SELECT", s2cPSelect},
		{"ACCEPT", s2cPAccept}, {"CHOSEN", s2cPChosen},
		{"B_APPLY", 0}, {"DRAINED", s2cPDrained},
	} {
		for _, fault := range []string{"native_tip_error", "mixed_uncertainty_panic"} {
			t.Run(transition.name+"/"+fault, func(t *testing.T) {
				n := s2cTestNativeCluster(t, 3, nil)
				state, _, err := n.nodes[1].ReadLocalCut()
				if err != nil {
					t.Fatal(err)
				}
				op := s1Operation(t, state.projection, testIdentity(), s1Changes(s1ReaderRole()))
				id := FullChangeID{S1Version, op.reviewed.Domain, op.reviewed.Cohort, 1, [16]byte{41}}
				raw := s2cTestSeal(t, n.fixture.trust, 1, n.fixture.keys[1], id, op, 41, false)
				var value [32]byte
				if transition.kind != s2cPOrigin {
					value, err = n.nodes[1].persistSealedOriginH(raw)
					if err != nil {
						t.Fatal(err)
					}
				}
				member := uint32(1)
				var incoming []byte
				switch transition.kind {
				case s2cPOrigin, s2cPBallot:
				case s2cPPromise:
					n.begin(1, value)
					member = 2
					incoming = n.take(s2cPrepare, 1, 2).raw
				case s2cPSelect:
					n.begin(1, value)
					n.send(s2cPrepare, 1, 1)
					n.send(s2cPrepare, 1, 2)
					n.send(s2cPromise, 1, 1)
					incoming = n.take(s2cPromise, 2, 1).raw
				case s2cPAccept:
					n.selectValue(1, value, 1, 2)
					member = 2
					incoming = n.take(s2cAccept, 1, 2).raw
				default:
					n.selectValue(1, value, 1, 2)
					n.send(s2cAccept, 1, 1)
					n.send(s2cAccept, 1, 2)
					n.send(s2cAccepted, 1, 1)
					incoming = n.take(s2cAccepted, 2, 1).raw
				}
				o := n.nodes[member]
				pFloor, bFloor, err := o.Floors()
				if err != nil {
					t.Fatal(err)
				}
				mixed := errors.Join(mutationlog.ErrWALIndeterminate,
					&mutationlog.DefiniteWALAbort{Cause: errors.New("lost acknowledgement")})
				fired := false
				trip := func() {
					fired = true
					if fault == "mixed_uncertainty_panic" {
						panic(mixed)
					}
					var closeErr error
					if transition.kind == 0 {
						closeErr = o.b.tip.Close()
					} else {
						closeErr = o.p.tip.Close()
					}
					if closeErr != nil {
						t.Fatal(closeErr)
					}
				}
				o.hooks = &s2cParticipantHooks{}
				if transition.kind == 0 {
					o.b.hooks = &s2LocalHooks{}
					if fault == "native_tip_error" {
						o.b.hooks.beforeAppend = trip
					} else {
						o.b.hooks.afterState = trip
					}
				} else if fault == "native_tip_error" {
					o.hooks.beforePAppend = func(kind byte) {
						if kind == transition.kind {
							o.p.hooks = &s2cJournalHooks{beforeAppend: trip}
						}
					}
				} else {
					o.hooks.afterPAppend = func(kind byte) {
						if kind == transition.kind {
							trip()
						}
					}
				}
				var out []s2cOutbox
				var result error
				var panicked any
				func() {
					defer func() { panicked = recover() }()
					switch transition.kind {
					case s2cPOrigin:
						var digest [32]byte
						digest, result = o.persistSealedOriginH(raw)
						if digest != [32]byte{} {
							t.Fatal("uncertain origin returned a dispatchable digest")
						}
					case s2cPBallot:
						out, result = o.Begin(value)
					default:
						out, result = o.Receive(incoming)
					}
				}()
				if !fired || len(out) != 0 || (panicked != nil) != (fault == "mixed_uncertainty_panic") {
					t.Fatalf("fault boundary/dispatch: fired=%v out=%d panic=%v", fired, len(out), panicked)
				}
				if panicked == nil && !errors.Is(result, errS2CUnknown) {
					t.Fatalf("native I/O error escaped fail-stop: %v", result)
				}
				if panicked != nil && panicked != mixed {
					t.Fatalf("panic changed: %v", panicked)
				}
				s2cIOGateAssertClosed(t, o, id, raw, incoming)
				for _, path := range []string{o.config.PPath, o.config.BPath} {
					lease, err := mutationlog.AcquireFileWALLease(path)
					if lease != nil {
						_ = lease.Close()
					}
					if !errors.Is(err, mutationlog.ErrFileWALLeaseBusy) {
						t.Fatalf("uncertainty released ownership: %s: %v", path, err)
					}
				}
				// Recover with the independently retained pre-failure floors. The
				// old owner and scheduler are discarded; no volatile proof is input.
				if err := o.Close(); err != nil {
					t.Fatal(err)
				}
				n.nodes[member] = nil
				n.queue = nil
				restored, err := resumeS2CParticipant(n.configs[member], pFloor, bFloor)
				if err != nil {
					t.Fatal("cold native recovery", err)
				}
				n.nodes[member] = restored
				_, receipt, err := restored.ReadLocalCut()
				if err != nil || receipt.ControlSlot < bFloor.ControlSlot {
					t.Fatal("recovery lowered prior cut", receipt, err)
				}
				if transition.kind == 0 || transition.kind == s2cPDrained || transition.kind == s2cPChosen {
					s2cGateOutcome(t, n, member, id, value, 1)
				}
				// A second cold resume uses the newly published exact floors and
				// must not append another materialization or protocol record.
				pAfter, bAfter, err := restored.Floors()
				if err != nil {
					t.Fatal(err)
				}
				added := uint64(1)
				if transition.kind == 0 || transition.kind == s2cPDrained || transition.kind == s2cPChosen {
					added = 2 // exact CHOSEN plus DRAINED, including recovered tails
				}
				if pAfter.Index != pFloor.Index+added {
					t.Fatal("native tail was not retained exactly once", pFloor, pAfter)
				}
				beforeFiles := s2cGateSnapshot(t, n.configs[member])
				n.restart(member)
				pAgain, bAgain, err := n.nodes[member].Floors()
				if err != nil || pAfter != pAgain || bAfter != bAgain {
					t.Fatal("second resume changed cut", err)
				}
				for path, before := range beforeFiles {
					after, err := os.ReadFile(path)
					if err != nil || string(before) != string(after) {
						t.Fatal("second recovery rewrote retained bytes", path, err)
					}
				}
			})
		}
	}
}

func s2cIOGateAssertClosed(t *testing.T, o *s2cParticipant, id FullChangeID, origin, incoming []byte) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if !errors.Is(err, errS2CUnknown) {
			t.Fatalf("uncertain composite remained callable: %v", err)
		}
	}
	s, receipt, err := o.ReadLocalCut()
	check(err)
	if s != nil || receipt != (s2LocalReceipt{}) {
		t.Fatal("uncertain read exposed materialization")
	}
	status, outcome, receipt, err := o.LookupOriginal(id)
	check(err)
	if status != S1Unresolved || outcome != nil || receipt != (s2LocalReceipt{}) {
		t.Fatal("uncertain lookup exposed outcome")
	}
	p, b, err := o.Floors()
	check(err)
	if p != (s2cJournalFloor{}) || b != (s2LocalReceipt{}) {
		t.Fatal("uncertain owner exposed floor")
	}
	digest, err := o.persistSealedOriginH(origin)
	check(err)
	if digest != [32]byte{} {
		t.Fatal("uncertain owner dispatched origin")
	}
	for _, call := range []func() ([]s2cOutbox, error){
		func() ([]s2cOutbox, error) { return o.Begin([32]byte{}) },
		o.Retry, func() ([]s2cOutbox, error) { return o.Receive(incoming) },
	} {
		out, err := call()
		check(err)
		if len(out) != 0 {
			t.Fatal("uncertain owner replied")
		}
	}
	history, err := o.ExportChosen(1, 1, 1<<20)
	check(err)
	if len(history) != 0 {
		t.Fatal("uncertain owner exported choice")
	}
}

func TestS2CIOGatePreviewDivergenceClosesBeforeBAppend(t *testing.T) {
	for _, field := range []string{"payload", "capsule", "charge"} {
		t.Run(field, func(t *testing.T) {
			n := s2cTestNativeCluster(t, 3, nil)
			id, value := s2cGateOrigin(t, n, 1, 61, "preview_parity")
			n.selectValue(1, value, 1, 2)
			n.send(s2cAccept, 1, 1)
			n.send(s2cAccept, 1, 2)
			n.send(s2cAccepted, 1, 1)
			incoming := n.take(s2cAccepted, 2, 1).raw
			o := n.nodes[1]
			origin := []byte(o.origins[value].raw)
			pFloor, bFloor, err := o.Floors()
			if err != nil {
				t.Fatal(err)
			}
			beforeFiles := s2cGateSnapshot(t, o.config)
			beforeUsage := o.b.used
			var expectedCharge uint64
			var expectedCapsule, expectedPrefix [32]byte
			fired := false
			o.hooks = &s2cParticipantHooks{afterChosen: func() {
				// Choice and its exact H are genuinely durable. Corrupt only the
				// volatile preview so Apply must detect the independent mismatch.
				fired = true
				expectedCharge = o.preview.charge
				expectedCapsule = o.preview.capsuleDigest
				expectedPrefix = o.preview.state.prefix
				switch field {
				case "payload":
					o.preview.payload += "changed"
				case "capsule":
					o.preview.capsule += "changed"
				case "charge":
					o.preview.charge++
				}
			}}
			out, err := o.Receive(incoming)
			if !fired || len(out) != 0 || !errors.Is(err, errS2CUnknown) || !errors.Is(err, errS2CProtocol) || errors.Is(err, errS2BlockedSameDecision) {
				t.Fatalf("preview divergence did not fail-stop before replying: fired=%v out=%d err=%v", fired, len(out), err)
			}
			if o.p.count != pFloor.Index+1 || o.chosen == nil || o.b.receipt != bFloor || o.b.used != beforeUsage {
				t.Fatal("preview divergence crossed B/DRAINED boundary")
			}
			for _, path := range []string{o.config.BPath, o.config.BPath + ".tip"} {
				before, exists := beforeFiles[path]
				if !exists {
					t.Fatal("missing canonical B snapshot", path)
				}
				after, err := os.ReadFile(path)
				if err != nil || string(after) != string(before) {
					t.Fatal("mismatched preview appended B bytes", path, err)
				}
			}
			s2cIOGateAssertClosed(t, o, id, origin, incoming)
			if err := o.Close(); err != nil {
				t.Fatal(err)
			}
			n.nodes[1], n.queue = nil, nil
			// Resume has only retained authentic choice and independent floors;
			// it cannot reuse the deliberately corrupted volatile preview.
			restored, err := resumeS2CParticipant(n.configs[1], pFloor, bFloor)
			if err != nil {
				t.Fatal("exact historical recovery after preview divergence", err)
			}
			n.nodes[1] = restored
			s2cGateOutcome(t, n, 1, id, value, 1)
			if restored.b.used != beforeUsage+expectedCharge || restored.b.receipt.CapsuleDigest != expectedCapsule || restored.b.receipt.ControlPrefix != expectedPrefix {
				t.Fatal("recovery changed the original exact successor or charge")
			}
		})
	}
}
