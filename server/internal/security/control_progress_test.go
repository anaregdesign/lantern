package security

import (
	"context"
	"testing"
	"time"
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
		c.Limits.RPCTimeout = 200 * time.Millisecond
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
	if n.nodes[2].send(ctx, 3, history[1]) == nil {
		t.Fatal("noncontiguous reordered QC accepted")
	}
	after, err := n.nodes[3].Floors()
	if err != nil || after != before {
		t.Fatal("gap changed durable state", err)
	}
	for slot := uint64(1); slot <= 2; slot++ {
		if count, err := n.nodes[3].CatchUp(ctx, 2); err != nil || count != 1 {
			t.Fatal("bounded contiguous range", count, err)
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
