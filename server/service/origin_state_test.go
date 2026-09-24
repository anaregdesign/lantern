package service

import (
	"reflect"
	"testing"
	"time"

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

func TestOriginStateStageNewOriginAbortAndCommit(t *testing.T) {
	tracker := newOriginStateTracker()
	origin := hlc.NodeID{0x51}
	ts := hlc.Timestamp{WallNs: 7, NodeID: origin}
	stage, ok := tracker.stageNext(origin, 1, ts)
	if !ok {
		t.Fatal("new contiguous origin was rejected")
	}
	stage.Abort()
	stage.Abort() // A deferred panic guard after a handled abort is harmless.
	if got := tracker.OriginCount(); got != 0 {
		t.Fatalf("origin count after abort = %d, want 0", got)
	}
	if got := tracker.States(); len(got) != 0 {
		t.Fatalf("states after abort = %+v, want empty", got)
	}
	stage, ok = tracker.stageNext(origin, 1, ts)
	if !ok {
		t.Fatal("aborted new-origin seq was not reusable")
	}
	stage.Commit()
	stage.Abort() // A deferred guard after commit must not roll back.
	if got := tracker.States(); !reflect.DeepEqual(got, []OriginState{{Origin: origin, LastSeq: 1, LastHLC: ts}}) {
		t.Fatalf("states after commit = %+v, want exact committed row", got)
	}
}

func TestOriginStateStageExistingRowRestoresExactPriorHLC(t *testing.T) {
	tracker := newOriginStateTracker()
	origin := hlc.NodeID{0x61}
	oldTS := hlc.Timestamp{WallNs: 11, Logical: 3, NodeID: origin}
	newTS := hlc.Timestamp{WallNs: 12, Logical: 4, NodeID: origin}
	if !tracker.Record(origin, 1, oldTS) {
		t.Fatal("seed Record failed")
	}
	stage, ok := tracker.stageNext(origin, 2, newTS)
	if !ok {
		t.Fatal("next contiguous row was rejected")
	}
	stage.Abort()
	if got := tracker.States(); !reflect.DeepEqual(got, []OriginState{{Origin: origin, LastSeq: 1, LastHLC: oldTS}}) {
		t.Fatalf("states after abort = %+v, want exact old row", got)
	}
	stage, ok = tracker.stageNext(origin, 2, newTS)
	if !ok {
		t.Fatal("aborted seq was not reusable")
	}
	stage.Commit()
	if got := tracker.States(); !reflect.DeepEqual(got, []OriginState{{Origin: origin, LastSeq: 2, LastHLC: newTS}}) {
		t.Fatalf("states after commit = %+v, want exact new row", got)
	}
}

func TestOriginStateStageRejectsInvalidDuplicateAndGapWithoutLocking(t *testing.T) {
	tracker := newOriginStateTracker()
	origin := hlc.NodeID{0x71}
	ts := hlc.Timestamp{WallNs: 1, NodeID: origin}
	for _, tc := range []struct {
		name   string
		origin hlc.NodeID
		seq    uint64
	}{
		{name: "zero origin", seq: 1},
		{name: "zero seq", origin: origin, seq: 0},
		{name: "new origin gap", origin: origin, seq: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if stage, ok := tracker.stageNext(tc.origin, tc.seq, ts); ok || stage != nil {
				t.Fatalf("invalid row accepted: stage=%+v ok=%v", stage, ok)
			}
			if got := tracker.OriginCount(); got != 0 {
				t.Fatalf("invalid row left count %d, want 0", got)
			}
		})
	}
	if !tracker.Record(origin, 1, ts) {
		t.Fatal("seed Record failed")
	}
	for _, seq := range []uint64{0, 1, 3} {
		if stage, ok := tracker.stageNext(origin, seq, ts); ok || stage != nil {
			t.Fatalf("duplicate/gap seq %d accepted: stage=%+v ok=%v", seq, stage, ok)
		}
		if got := tracker.LocalSeq(origin); got != 1 {
			t.Fatalf("invalid seq %d changed cursor to %d", seq, got)
		}
	}
}

func TestOriginStateStageHidesTentativeRowFromReaders(t *testing.T) {
	tracker := newOriginStateTracker()
	origin := hlc.NodeID{0x81}
	ts := hlc.Timestamp{WallNs: 1, NodeID: origin}
	stage, ok := tracker.stageNext(origin, 1, ts)
	if !ok {
		t.Fatal("stage failed")
	}
	defer stage.Abort()
	started := make(chan struct{})
	read := make(chan uint64, 1)
	go func() {
		close(started)
		read <- tracker.LocalSeq(origin)
	}()
	<-started
	select {
	case got := <-read:
		t.Fatalf("tentative row became visible early: seq=%d", got)
	case <-time.After(25 * time.Millisecond):
	}
	stage.Commit()
	select {
	case got := <-read:
		if got != 1 {
			t.Fatalf("post-commit seq = %d, want 1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("reader remained blocked after Commit")
	}
}

func TestOriginStateStageDeferredAbortAfterPanic(t *testing.T) {
	tracker := newOriginStateTracker()
	origin := hlc.NodeID{0x91}
	ts := hlc.Timestamp{WallNs: 1, NodeID: origin}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("simulated WAL panic did not propagate")
			}
		}()
		stage, ok := tracker.stageNext(origin, 1, ts)
		if !ok {
			t.Fatal("stage failed")
		}
		defer stage.Abort()
		panic("simulated WAL panic")
	}()
	if got := tracker.OriginCount(); got != 0 {
		t.Fatalf("deferred Abort left count %d, want 0", got)
	}
	if !tracker.Record(origin, 1, ts) {
		t.Fatal("tracker remained locked or rejected row after panic rollback")
	}
}

func BenchmarkOriginStateStageExistingRow(b *testing.B) {
	origin := hlc.NodeID{0xa1}
	ts := hlc.Timestamp{WallNs: 1, NodeID: origin}
	b.Run("Commit", func(b *testing.B) {
		tracker := newOriginStateTracker()
		if !tracker.Record(origin, 1, ts) {
			b.Fatal("seed Record failed")
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			stage, ok := tracker.stageNext(origin, uint64(i)+2, ts)
			if !ok {
				b.Fatal("stage failed")
			}
			stage.Commit()
		}
	})
	b.Run("Abort", func(b *testing.B) {
		tracker := newOriginStateTracker()
		if !tracker.Record(origin, 1, ts) {
			b.Fatal("seed Record failed")
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			stage, ok := tracker.stageNext(origin, 2, ts)
			if !ok {
				b.Fatal("stage failed")
			}
			stage.Abort()
		}
	})
}
