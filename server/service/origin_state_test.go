package service

import (
	"testing"

	"github.com/anaregdesign/lantern/core/hlc"
)

func TestOriginStateTracker_ContiguousAndSnapshotAdvance(t *testing.T) {
	tracker := newOriginStateTracker()
	origin := hlc.NodeID{0x41}
	ts := hlc.Timestamp{WallNs: 1, NodeID: origin}
	if tracker.Record(origin, 3, ts) {
		t.Fatal("future seq advanced an empty origin")
	}
	if !tracker.Record(origin, 1, ts) {
		t.Fatal("first contiguous seq was rejected")
	}
	if tracker.Record(origin, 3, ts) || tracker.Record(origin, 1, ts) {
		t.Fatal("gap or duplicate advanced origin")
	}
	if got := tracker.LocalSeq(origin); got != 1 {
		t.Fatalf("origin cursor = %d, want 1", got)
	}
	if !tracker.AdvanceSnapshot(origin, 3, ts) {
		t.Fatal("verified snapshot cutoff did not advance")
	}
	if tracker.Record(origin, 5, ts) || !tracker.Record(origin, 4, ts) {
		t.Fatal("contiguous rule was lost after snapshot")
	}
	if got := tracker.LocalSeq(origin); got != 4 {
		t.Fatalf("origin cursor = %d, want 4", got)
	}
}
