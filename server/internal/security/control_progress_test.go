package security

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestS3ADriverRealTLSAndChosenCatchup(t *testing.T) {
	n := s3aTestCluster(t, nil)
	n.startAll()
	ctx := s3aTestContext(t)
	id, value := n.origin(1, 31)
	r, err := n.nodes[1].Drive(ctx, value)
	if err != nil || r.ControlSlot != 1 {
		t.Fatal("network drive", r, err)
	}
	for member := uint32(1); member <= 3; member++ {
		n.waitCut(member, 1)
		status, out, _, err := n.nodes[member].kernel.LookupOriginal(id)
		if err != nil || status != S1Known || out.HandoffDigest() != value || out.Disposition() != S1Applied {
			t.Fatal("original outcome", member, status, out, err)
		}
	}
}

func TestS3ADriverContiguousCatchupDuplicateAndReorderedQC(t *testing.T) {
	n := s3aTestCluster(t, func(_ uint32, c *s3aConfig) {
		c.Limits.RangeSlots = 1
		// This tests range cardinality and QC ordering, using the shared
		// finite RPC profile rather than a separate latency requirement.
		// Keep background broadcasts partitioned from the laggard even after
		// its listener starts. Only the explicit real-TLS relay/range below
		// crosses the partition, so a late queued send cannot repair the gap.
		c.hooks = &s3aHooks{beforeSend: func(_ context.Context, to uint32, _ []byte) error {
			if to == 3 {
				return errS3AWire
			}
			return nil
		}}
	})
	n.start(1)
	n.start(2)
	ctx := s3aTestContext(t)
	for slot := uint64(1); slot <= 2; slot++ {
		if receipt, err := n.nodes[1].Drive(ctx, [32]byte{}); err != nil || receipt.ControlSlot != slot {
			t.Fatal("produce chosen history", slot, receipt, err)
		}
		n.waitCut(2, slot)
	}
	n.start(3)
	history, err := n.nodes[2].kernel.ExportChosen(1, 2, n.configs[2].Limits.RangeBytes)
	if err != nil || len(history) != 2 {
		t.Fatal(err)
	}
	before, err := n.nodes[3].Floors()
	if err != nil {
		t.Fatal(err)
	}
	if err := n.nodes[2].send(ctx, 3, history[1]); !errors.Is(err, errS3AWire) {
		t.Fatal("noncontiguous reordered QC was not rejected by the peer", err)
	}
	after, err := n.nodes[3].Floors()
	if err != nil || after != before {
		t.Fatal("gap changed durable state", err)
	}
	for slot := uint64(1); slot <= 2; slot++ {
		if count, err := n.nodes[3].CatchUp(ctx, 2); err != nil || count != 1 {
			t.Fatal("bounded contiguous range", slot, count, err)
		}
		n.waitCut(3, slot)
	}
	before, err = n.nodes[3].Floors()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := n.nodes[2].send(ctx, 3, history[0]); err != nil {
			t.Fatal("valid duplicate QC relay", err)
		}
	}
	after, err = n.nodes[3].Floors()
	if err != nil || after != before {
		t.Fatal("duplicate changed original result/floors", err)
	}
}

// A consumed scheduling target must not invalidate a certificate renewed for
// the installed prefix, even when that prefix's semantic policy did not change.
func TestS3ADriverConsumedBoundTarget(t *testing.T) {
	n := s3aTestCluster(t, func(_ uint32, c *s3aConfig) {
		clock, _ := fakeAuthorityTimeOwnerAt(t, c.Membership.Now())
		if err := bindAuthorityNetworkTime(c, clock); err != nil {
			t.Fatal(err)
		}
		c.hooks = &s3aHooks{beforeRenewal: func(ctx context.Context) { <-ctx.Done() }}
	})
	n.startAll()
	o := n.nodes[1]
	ctx := s3aTestContext(t)
	state, _, err := o.kernel.ReadLocalCut()
	if err != nil {
		t.Fatal(err)
	}
	target := s2cBeginTarget{slot: state.slot + 1, predecessor: state.prefix}
	if receipt, err := o.Drive(ctx, [32]byte{}); err != nil || receipt.ControlSlot != target.slot {
		t.Fatal("close the scheduling target with real quorum", receipt, err)
	}
	for member := uint32(1); member <= 3; member++ {
		n.waitCut(member, target.slot)
	}
	if err := o.renewAuthority(ctx); err != nil {
		t.Fatal("renew exact installed prefix", err)
	}
	before, err := o.Floors()
	if err != nil {
		t.Fatal(err)
	}
	o.kernel.gate.Lock()
	counter, active := o.kernel.counter, o.receiver.active
	o.kernel.gate.Unlock()
	if active == nil {
		t.Fatal("fixture lacks fresh current certificate")
	}
	for _, expected := range []*s2cBeginTarget{&target, &s2cBeginTarget{}} {
		receipt, err := o.driveAt(ctx, [32]byte{}, expected)
		if !errors.Is(err, ErrRevisionConflict) || receipt != (s2LocalReceipt{}) {
			t.Fatal("stale or present-zero target became a successful drive", receipt, err)
		}
		after, floorErr := o.Floors()
		o.kernel.gate.Lock()
		_, current, currentErr := o.receiver.currentLocked()
		unchanged := o.kernel.counter == counter && o.receiver.active == active && current == active
		o.kernel.gate.Unlock()
		if floorErr != nil || after != before || currentErr != nil || !unchanged {
			t.Fatal("stale scheduling appended P/B, changed ballot or invalidated current certificate", after, before, floorErr, currentErr, unchanged)
		}
	}
}

func TestS3ADriverBoundTargetMatchedAndUnbound(t *testing.T) {
	n := s3aTestCluster(t, nil)
	n.startAll()
	o := n.nodes[1]
	ctx := s3aTestContext(t)
	id, value := n.origin(1, 93)
	state, _, err := o.kernel.ReadLocalCut()
	if err != nil {
		t.Fatal(err)
	}
	target := s2cBeginTarget{slot: state.slot + 1, predecessor: state.prefix}
	receipt, err := o.driveAt(ctx, value, &target)
	if err != nil || receipt.ControlSlot != target.slot {
		t.Fatal("matched target failed real phase one", receipt, err)
	}
	status, original, _, err := o.kernel.LookupOriginal(id)
	if err != nil || status != S1Known || original == nil || original.HandoffDigest() != value || original.Disposition() != S1Applied {
		t.Fatal("matched drive replaced or lost exact original H", status, original, err)
	}
	for member := uint32(1); member <= 3; member++ {
		n.waitCut(member, target.slot)
	}
	// The public wrapper remains unbound and may initiate the next real slot.
	if next, err := o.Drive(ctx, [32]byte{}); err != nil || next.ControlSlot != target.slot+1 {
		t.Fatal("unbound public Drive acquired the conditional fence", next, err)
	}
}

func TestS3ADriverRefusedBeginCannotClaimCatchup(t *testing.T) {
	for _, credit := range []bool{true, false} {
		name := "unknown_candidate"
		if credit {
			name = "local_credit"
		}
		t.Run(name, func(t *testing.T) {
			n := s3aTestCluster(t, func(_ uint32, c *s3aConfig) {
				clock, _ := fakeAuthorityTimeOwnerAt(t, c.Membership.Now())
				if err := bindAuthorityNetworkTime(c, clock); err != nil {
					t.Fatal(err)
				}
				c.hooks = &s3aHooks{beforeRenewal: func(ctx context.Context) { <-ctx.Done() }, beforeSend: func(_ context.Context, to uint32, _ []byte) error {
					if to == 3 {
						return errS3AWire
					}
					return nil
				}}
			})
			n.start(1)
			n.start(2)
			ctx := s3aTestContext(t)
			chosen, err := n.nodes[1].Drive(ctx, [32]byte{})
			if err != nil || chosen.ControlSlot != 1 {
				t.Fatal("produce authentic peer CHOSEN", chosen, err)
			}
			n.waitCut(2, 1)
			o := n.start(3)
			state, receipt, err := o.kernel.ReadLocalCut()
			if err != nil || receipt.ControlSlot != 0 {
				t.Fatal("laggard must start before the retained CHOSEN", receipt, err)
			}
			target := s2cBeginTarget{slot: state.slot + 1, predecessor: state.prefix}
			var ballots atomic.Int32
			o.kernel.gate.Lock()
			o.receiver.startupRecoveryPending = true
			o.kernel.hooks = &s2cParticipantHooks{beforePAppend: func(kind byte) {
				if kind == s2cPBallot {
					ballots.Add(1)
				}
			}}
			o.kernel.gate.Unlock()
			// Neither local credit refusal nor an unknown-candidate protocol refusal
			// counts as a successful Begin when CatchUp later installs the target.
			candidate := [32]byte{0xee}
			if credit {
				candidate = [32]byte{}
				o.localSlot <- struct{}{}
				defer func() { <-o.localSlot }()
			}
			got, err := o.driveAt(ctx, candidate, &target)
			if !errors.Is(err, ErrRevisionConflict) || got != (s2LocalReceipt{}) {
				t.Fatal("peer catchup falsely satisfied a refused conditional Begin", got, err)
			}
			_, installed, cutErr := o.kernel.ReadLocalCut()
			o.kernel.gate.Lock()
			startupPending := o.receiver.startupRecoveryPending
			o.kernel.gate.Unlock()
			if cutErr != nil || installed.ControlSlot != chosen.ControlSlot || installed.ControlPrefix != chosen.ControlPrefix || ballots.Load() != 0 || o.ballotSlot != 0 || !o.ballotReady.IsZero() || !startupPending {
				t.Fatal("refused Begin appended a ballot/fence or failed actual peer catchup", installed, chosen, ballots.Load(), cutErr)
			}
		})
	}
}
