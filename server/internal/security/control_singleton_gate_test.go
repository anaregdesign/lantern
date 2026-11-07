package security

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// This deterministic owner fixture uses the existing private factual time and
// credential ports. It does not qualify the normal configured-source factory.
func TestAuthoritySingletonDurableAdmissionAndRenewal(t *testing.T) {
	n, owners, ticks := authorityTestCompositeMembers(t, 1, 1, nil)
	o := owners[1]
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if len(n.f.members) != 1 || len(o.network.peers) != 1 || n.f.trust.majority() != 1 || o.descriptor.PublicKey == n.f.members[0].PublicKey {
		t.Fatal("singleton fixture did not preserve independent identity/origin ownership")
	}
	if err := o.network.renewAuthority(ctx); err != nil {
		t.Fatal("self renewal", err)
	}
	public := &CurrentAuthority{origin: o}
	if !public.Ready(ctx) {
		t.Fatal("single enrolled voter did not become ready")
	}
	oldRenewal := o.network.receiver.active
	review, err := o.prepare(ctx, authorityFakeCredentialProducer{}, o.network.kernel.replayState.projection.cut, s1Changes(s1ReaderRole()))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := o.consume(ctx, review, authorityFakeCredentialProducer{}, [32]byte{})
	if err != nil || sealed.raw == "" || o.network.kernel.originSerial != 1 {
		t.Fatal("durable original H", err)
	}
	h, err := verifyHistoricalH(n.f.trust, []byte(sealed.raw))
	if err != nil || h.handoff.id != review.id {
		t.Fatal("original H lost its identity", err)
	}
	receipt, err := o.network.Drive(ctx, sealed.digest)
	if err != nil || receipt.ControlSlot != 1 {
		t.Fatal("self-driven quorum did not drain", receipt, err)
	}
	if public.Ready(ctx) || o.network.receiver.active != oldRenewal {
		t.Fatal("old-cut renewal survived a new materialized cut")
	}
	if err := o.network.renewAuthority(ctx); err != nil || !public.Ready(ctx) {
		t.Fatal("fresh self renewal after Apply", err)
	}
	if o.network.receiver.active == oldRenewal || len(o.network.receiver.active.certificate.votes) != 1 {
		t.Fatal("self renewal reused old capability or fabricated extra votes")
	}
	again, err := o.consume(ctx, review, nil, [32]byte{})
	if err != nil || again.outcome == nil || again.outcome.disposition != S1Applied || o.network.kernel.originSerial != 1 {
		t.Fatal("retry replaced original outcome or consumed another serial", err)
	}
	beforeNOOP, beforeReceipt, err := o.network.kernel.ReadLocalCut()
	if err != nil || beforeNOOP == nil {
		t.Fatal("capture NOOP target", err)
	}
	target := s2cBeginTarget{slot: beforeReceipt.ControlSlot + 1, predecessor: beforeReceipt.ControlPrefix}
	completion := singletonNOOPCompletion{
		read: func() (s2LocalReceipt, error) {
			_, receipt, err := o.network.kernel.ReadLocalCut()
			return receipt, err
		},
		drive: func(call context.Context, fixed *s2cBeginTarget) (s2LocalReceipt, error) {
			return o.network.driveAt(call, [32]byte{}, fixed)
		},
		pause: func(call context.Context) bool { return s3aPause(call, o.network.limits.Backoff) },
		verify: func(receipt s2LocalReceipt) error {
			state, installed, err := o.network.kernel.ReadLocalCut()
			if err != nil || state == nil || installed != receipt {
				return fmt.Errorf("NOOP receipt differs from installed cut: %w", errors.Join(ErrS1Contract, err))
			}
			history, err := o.network.kernel.ExportChosen(target.slot, 1, o.network.limits.RangeBytes)
			if err != nil || len(history) != 1 {
				return fmt.Errorf("NOOP lacks exact retained CHOSEN: %w", errors.Join(ErrS1Contract, err))
			}
			chosen, err := s2cDecodeMessage(n.f.trust, history[0])
			if err != nil || chosen == nil || chosen.kind != s2cChosen || chosen.slot != target.slot || chosen.predecessor != target.predecessor || chosen.value != n.f.trust.noopValue() || chosen.h != nil {
				return fmt.Errorf("retained CHOSEN differs from fixed NOOP: %w", errors.Join(ErrS1Contract, err))
			}
			certified, err := s2cVerifyChosen(n.f.trust, chosen)
			if err != nil {
				return err
			}
			applied, err := ApplyS1(beforeNOOP, certified)
			if err != nil || applied.Status != S1Noop || applied.Outcome != nil || applied.State == nil || applied.State.slot != target.slot || applied.State.prefix != receipt.ControlPrefix {
				return fmt.Errorf("installed prefix differs from certified NOOP: %w", errors.Join(ErrS1Contract, err))
			}
			known, outcome := state.Lookup(review.id)
			o.network.kernel.gate.Lock()
			retained := o.network.kernel.origins[sealed.digest]
			unchanged := o.network.kernel.originSerial == 1 && retained != nil && retained.raw == sealed.raw
			o.network.kernel.gate.Unlock()
			if known != S1Known || outcome == nil || !reflect.DeepEqual(outcome, again.outcome) || outcome.HandoffDigest() != sealed.digest || outcome.Disposition() != S1Applied || !unchanged {
				return errors.New("NOOP replaced original H, outcome or serial")
			}
			return nil
		},
	}
	// Drive is one finite attempt, not an assertion that refused credit aborted
	// entered work. Preserve the one public NOOP call, then use only its pinned
	// next position under this same context; no retry can open another slot.
	firstNOOP, driveErr := o.network.Drive(ctx, [32]byte{})
	if _, err := completion.complete(ctx, target, firstNOOP, driveErr); err != nil {
		t.Fatal("self-driven fixed NOOP", err)
	}
	if err := o.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	ticks[1].Add(authorityRenewalLifetime)
	if public.Ready(ctx) {
		t.Fatal("singleton bypassed renewal expiry")
	}
}

// These callbacks isolate only the fixture's bounded completion decisions.
// The native caller above verifies real retained CHOSEN and installed state;
// simulated receipts below exercise refusal/race guards, not native authority.
type singletonNOOPCompletion struct {
	read   func() (s2LocalReceipt, error)
	drive  func(context.Context, *s2cBeginTarget) (s2LocalReceipt, error)
	verify func(s2LocalReceipt) error
	pause  func(context.Context) bool
}

func (f singletonNOOPCompletion) complete(ctx context.Context, target s2cBeginTarget, initial s2LocalReceipt, initialErr error) (s2LocalReceipt, error) {
	if ctx == nil || target.slot == 0 {
		return s2LocalReceipt{}, ErrS1Contract
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return s2LocalReceipt{}, ErrS1Contract
	}
	finish := func(receipt s2LocalReceipt) (s2LocalReceipt, error) {
		if err := ctx.Err(); err != nil {
			return s2LocalReceipt{}, err
		}
		if receipt.ControlSlot != target.slot {
			return s2LocalReceipt{}, ErrS1Contract
		}
		if err := f.verify(receipt); err != nil {
			return s2LocalReceipt{}, err
		}
		if err := ctx.Err(); err != nil {
			return s2LocalReceipt{}, err
		}
		return receipt, nil
	}
	if err := ctx.Err(); err != nil {
		return s2LocalReceipt{}, err
	}
	if initialErr == nil {
		return finish(initial)
	}
	if !singletonNOOPErrorOnly(initialErr, errS3ACredit) {
		return s2LocalReceipt{}, initialErr
	}
	fixed := target
	for {
		if err := ctx.Err(); err != nil {
			return s2LocalReceipt{}, err
		}
		receipt, err := f.read()
		if err != nil {
			return s2LocalReceipt{}, err
		}
		if err := ctx.Err(); err != nil {
			return s2LocalReceipt{}, err
		}
		if receipt.ControlSlot == target.slot {
			return finish(receipt)
		}
		if receipt.ControlSlot != target.slot-1 || receipt.ControlPrefix != target.predecessor {
			return s2LocalReceipt{}, ErrS1Contract
		}
		receipt, err = f.drive(ctx, &fixed)
		if fixed != target {
			return s2LocalReceipt{}, ErrS1Contract
		}
		if ctx.Err() != nil {
			return s2LocalReceipt{}, ctx.Err()
		}
		if err == nil {
			return finish(receipt)
		}
		if singletonNOOPErrorOnly(err, ErrRevisionConflict) {
			// A late commit may consume the fence between observation and the
			// call. Conflict itself proves nothing and never starts a new target.
			receipt, readErr := f.read()
			if readErr != nil {
				return s2LocalReceipt{}, readErr
			}
			if receipt.ControlSlot != target.slot {
				return s2LocalReceipt{}, err
			}
			return finish(receipt)
		}
		if !singletonNOOPErrorOnly(err, errS3ACredit) {
			return s2LocalReceipt{}, err
		}
		if !f.pause(ctx) {
			if ctx.Err() != nil {
				return s2LocalReceipt{}, ctx.Err()
			}
			return s2LocalReceipt{}, errS3ACredit
		}
	}
}

// A joined refusal/cancellation/unknown error must not acquire credit-only
// recovery merely because errors.Is can also find the credit sentinel.
func singletonNOOPErrorOnly(err, expected error) bool {
	if err == expected {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !singletonNOOPErrorOnly(child, expected) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return singletonNOOPErrorOnly(wrapped.Unwrap(), expected)
	}
	return false
}

func TestSingletonNOOPCompletionFixture(t *testing.T) {
	target := s2cBeginTarget{slot: 2, predecessor: [32]byte{1}}
	pending := s2LocalReceipt{ControlSlot: 1, ControlPrefix: target.predecessor}
	done := s2LocalReceipt{ControlSlot: 2, ControlPrefix: [32]byte{2}}
	wrongPrefix := done
	wrongPrefix.ControlPrefix[0]++
	wrongPending := pending
	wrongPending.ControlPrefix[0]++
	overshot := done
	overshot.ControlSlot++
	type observation struct {
		receipt s2LocalReceipt
		err     error
	}
	for _, tc := range []struct {
		name                          string
		initial                       observation
		reads, drives                 []observation
		want                          error
		readCalls, driveCalls, pauses int
		cancelAt                      string
		unbounded, mutateTarget       bool
		stopPause                     bool
	}{
		{name: "initial success", initial: observation{receipt: done}},
		{name: "initial wrong target", initial: observation{receipt: pending}, want: ErrS1Contract},
		{name: "initial wrong prefix", initial: observation{receipt: wrongPrefix}, want: ErrS1Contract},
		{name: "initial refusal", initial: observation{err: ErrPermissionDenied}, want: ErrPermissionDenied},
		{name: "initial mixed credit", initial: observation{err: errors.Join(errS3ACredit, ErrPermissionDenied)}, want: ErrPermissionDenied},
		{name: "already installed after credit", initial: observation{err: errS3ACredit}, reads: []observation{{receipt: done}}, readCalls: 1},
		{name: "pending then bound success", initial: observation{err: errS3ACredit}, reads: []observation{{receipt: pending}}, drives: []observation{{receipt: done}}, readCalls: 1, driveCalls: 1},
		{name: "credit then late installation", initial: observation{err: fmt.Errorf("wrapped: %w", errS3ACredit)}, reads: []observation{{receipt: pending}, {receipt: done}}, drives: []observation{{err: errS3ACredit}}, readCalls: 2, driveCalls: 1, pauses: 1},
		{name: "fixed context and target across continuations", initial: observation{err: errS3ACredit}, reads: []observation{{receipt: pending}, {receipt: pending}}, drives: []observation{{err: errors.Join(errS3ACredit, fmt.Errorf("wrapped: %w", errS3ACredit))}, {receipt: done}}, readCalls: 2, driveCalls: 2, pauses: 1},
		{name: "late commit races bound call", initial: observation{err: errS3ACredit}, reads: []observation{{receipt: pending}, {receipt: done}}, drives: []observation{{err: ErrRevisionConflict}}, readCalls: 2, driveCalls: 1},
		{name: "conflict without installed target", initial: observation{err: errS3ACredit}, reads: []observation{{receipt: pending}, {receipt: pending}}, drives: []observation{{err: ErrRevisionConflict}}, want: ErrRevisionConflict, readCalls: 2, driveCalls: 1},
		{name: "conflict with wrong prefix", initial: observation{err: errS3ACredit}, reads: []observation{{receipt: pending}, {receipt: wrongPrefix}}, drives: []observation{{err: ErrRevisionConflict}}, want: ErrS1Contract, readCalls: 2, driveCalls: 1},
		{name: "overshot target", initial: observation{err: errS3ACredit}, reads: []observation{{receipt: overshot}}, want: ErrS1Contract, readCalls: 1},
		{name: "wrong pending predecessor", initial: observation{err: errS3ACredit}, reads: []observation{{receipt: wrongPending}}, want: ErrS1Contract, readCalls: 1},
		{name: "read refusal", initial: observation{err: errS3ACredit}, reads: []observation{{err: ErrPermissionDenied}}, want: ErrPermissionDenied, readCalls: 1},
		{name: "drive refusal", initial: observation{err: errS3ACredit}, reads: []observation{{receipt: pending}}, drives: []observation{{err: ErrPermissionDenied}}, want: ErrPermissionDenied, readCalls: 1, driveCalls: 1},
		{name: "drive mixed credit", initial: observation{err: errS3ACredit}, reads: []observation{{receipt: pending}}, drives: []observation{{err: errors.Join(errS3ACredit, ErrPermissionDenied)}}, want: ErrPermissionDenied, readCalls: 1, driveCalls: 1},
		{name: "mixed conflict", initial: observation{err: errS3ACredit}, reads: []observation{{receipt: pending}}, drives: []observation{{err: errors.Join(ErrRevisionConflict, errS3ACredit)}}, want: ErrRevisionConflict, readCalls: 1, driveCalls: 1},
		{name: "cancel before completion", initial: observation{receipt: done}, cancelAt: "before", want: context.Canceled},
		{name: "cancel during observation", initial: observation{err: errS3ACredit}, reads: []observation{{receipt: done}}, cancelAt: "read", want: context.Canceled, readCalls: 1},
		{name: "cancel during bound call", initial: observation{err: errS3ACredit}, reads: []observation{{receipt: pending}}, drives: []observation{{receipt: done}}, cancelAt: "drive", want: context.Canceled, readCalls: 1, driveCalls: 1},
		{name: "pause budget exhausted", initial: observation{err: errS3ACredit}, reads: []observation{{receipt: pending}}, drives: []observation{{err: errS3ACredit}}, pauses: 1, stopPause: true, want: errS3ACredit, readCalls: 1, driveCalls: 1},
		{name: "cancel during pause", initial: observation{err: errS3ACredit}, reads: []observation{{receipt: pending}}, drives: []observation{{err: errS3ACredit}}, pauses: 1, cancelAt: "pause", want: context.Canceled, readCalls: 1, driveCalls: 1},
		{name: "unbounded context", initial: observation{receipt: done}, unbounded: true, want: ErrS1Contract},
		{name: "mutated continuation target", initial: observation{err: errS3ACredit}, reads: []observation{{receipt: pending}}, drives: []observation{{receipt: done}}, mutateTarget: true, want: ErrS1Contract, readCalls: 1, driveCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var callContext = ctx
			if tc.unbounded {
				callContext = context.Background()
			}
			if tc.cancelAt == "before" {
				cancel()
			}
			reads, drives, pauses, verified := 0, 0, 0, 0
			var fixedPointer *s2cBeginTarget
			ports := singletonNOOPCompletion{
				read: func() (s2LocalReceipt, error) {
					if reads >= len(tc.reads) {
						t.Fatal("unexpected observation")
					}
					step := tc.reads[reads]
					reads++
					if tc.cancelAt == "read" {
						cancel()
					}
					return step.receipt, step.err
				},
				drive: func(call context.Context, fixed *s2cBeginTarget) (s2LocalReceipt, error) {
					if call != callContext || fixed == nil || *fixed != target || fixedPointer != nil && fixedPointer != fixed {
						t.Fatal("continuation replaced context or fixed target")
					}
					fixedPointer = fixed
					if drives >= len(tc.drives) {
						t.Fatal("unexpected bound drive")
					}
					step := tc.drives[drives]
					drives++
					if tc.cancelAt == "drive" {
						cancel()
					}
					if tc.mutateTarget {
						fixed.slot++
					}
					return step.receipt, step.err
				},
				verify: func(receipt s2LocalReceipt) error {
					verified++
					if receipt != done {
						return ErrS1Contract
					}
					return nil
				},
				pause: func(call context.Context) bool {
					if call != callContext {
						t.Fatal("pause refreshed original context")
					}
					pauses++
					if tc.cancelAt == "pause" {
						cancel()
					}
					return !tc.stopPause
				},
			}
			receipt, err := ports.complete(callContext, target, tc.initial.receipt, tc.initial.err)
			if tc.want == nil {
				if err != nil || receipt != done || verified != 1 {
					t.Fatalf("fixed completion = %+v, %v, verified=%d", receipt, err, verified)
				}
			} else if !errors.Is(err, tc.want) || receipt != (s2LocalReceipt{}) {
				t.Fatalf("refusal = %+v, %v, want %v", receipt, err, tc.want)
			}
			if reads != tc.readCalls || drives != tc.driveCalls || pauses != tc.pauses {
				t.Fatalf("unexpected continuation work: reads=%d drives=%d pauses=%d", reads, drives, pauses)
			}
		})
	}
}

func TestSingletonProtocolRetainsPromisesAcceptsAndOriginals(t *testing.T) {
	n := s2cTestNativeCluster(t, 1, nil)
	n.begin(1, [32]byte{})
	n.send(s2cPrepare, 1, 1)
	promise := n.take(s2cPromise, 1, 1)
	n.restart(1)
	if err := n.deliver(promise); err != nil {
		t.Fatal("retained self promise", err)
	}
	accept := n.take(s2cAccept, 1, 1)
	n.restart(1)
	retry, err := n.nodes[1].Retry()
	if err != nil || len(retry) != 1 || !bytes.Equal(retry[0].Bytes, accept.raw) {
		t.Fatal("singleton SELECT retry changed", err)
	}
	if err := n.deliver(accept); err != nil {
		t.Fatal(err)
	}
	vote := n.take(s2cAccepted, 1, 1)
	n.restart(1)
	if err := n.deliver(vote); err != nil {
		t.Fatal("retained self ACCEPT", err)
	}
	_, before, err := n.nodes[1].ReadLocalCut()
	if err != nil || before.ControlSlot != 1 {
		t.Fatal("singleton did not complete CHOSEN/B/DRAINED", err)
	}
	n.restart(1)
	_, after, err := n.nodes[1].ReadLocalCut()
	if err != nil || before != after {
		t.Fatal("singleton replay changed drained result", err)
	}
}
