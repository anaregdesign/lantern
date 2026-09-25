package service

import (
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func mustMutationLogEntry(t *testing.T, log *mutationlog.Log, seq uint64) mutationlog.Entry {
	t.Helper()
	entries, cancel, err := log.Subscribe(seq)
	if err != nil {
		t.Fatalf("Subscribe(%d): %v", seq, err)
	}
	defer func() { _ = cancel() }()
	select {
	case entry := <-entries:
		if entry.Seq != seq {
			t.Fatalf("log entry seq = %d, want %d", entry.Seq, seq)
		}
		return entry
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out reading log entry %d", seq)
		return mutationlog.Entry{}
	}
}

func mustGraphMutation(t *testing.T, op mutationlog.MutationOp) *pb.Mutation {
	t.Helper()
	mutation, ok := graphMutationFromLog(op)
	if !ok {
		t.Fatalf("graph mutation unavailable from %T", op)
	}
	return mutation
}

func insertReceiptSnapshotFrames(
	frames []*pb.SnapshotResponse,
	index int,
	additions ...*pb.SnapshotResponse,
) []*pb.SnapshotResponse {
	tail := append([]*pb.SnapshotResponse(nil), frames[index:]...)
	frames = append(frames[:index], additions...)
	return append(frames, tail...)
}

func mustRetiredCatalogSnapshot(
	t testing.TB,
	active mutationreceipt.Config,
	highWaterMillis int64,
	seed byte,
) (mutationreceipt.RetiredCatalogSnapshot, mutationreceipt.ID) {
	t.Helper()
	retiredEpoch := mutationreceipt.Epoch{seed}
	if retiredEpoch == active.Epoch {
		retiredEpoch[1] = 1
	}
	policy := mutationreceipt.Config{
		Epoch:          retiredEpoch,
		Retention:      2 * time.Hour,
		MaxEntries:     active.MaxEntries,
		MaxBytes:       active.MaxBytes,
		ClockHighWater: time.UnixMilli(highWaterMillis),
	}
	store, err := mutationreceipt.New(policy)
	if err != nil {
		t.Fatal(err)
	}
	issued := time.UnixMilli(highWaterMillis)
	id, err := mutationreceipt.NewID(retiredEpoch, issued, [24]byte{seed, 1})
	if err != nil {
		t.Fatal(err)
	}
	intent := mutationreceipt.Intent{
		ID:     id,
		Group:  mutationreceipt.GroupID{seed, 2},
		Count:  1,
		Kind:   mutationreceipt.PutVertex,
		Digest: mutationreceipt.IntentDigest([]byte{seed, 3}),
	}
	tx, err := store.Begin(issued)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()
	classification, _, err := tx.Classify([]mutationreceipt.Intent{intent})
	if err != nil || classification != mutationreceipt.Fresh {
		t.Fatalf("classify retired receipt = %v, %v", classification, err)
	}
	if err := tx.Reserve([][]byte{{seed, 4}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Stage(); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	state, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return mutationreceipt.RetiredCatalogSnapshot{
		Version:              1,
		ClockHighWaterMillis: highWaterMillis,
		Epochs: []mutationreceipt.RetiredEpochSnapshot{{
			Policy: mutationreceipt.RetiredEpochPolicy{
				Epoch:      retiredEpoch,
				Retention:  policy.Retention,
				MaxEntries: policy.MaxEntries,
				MaxBytes:   policy.MaxBytes,
			},
			State: state,
		}},
	}, id
}

func mustReplaceRetiredCatalog(
	t testing.TB,
	slot *retiredReceiptCatalogSlot,
	policy mutationreceipt.Config,
	highWaterMillis int64,
	state mutationreceipt.RetiredCatalogSnapshot,
) {
	t.Helper()
	_, revision, err := slot.snapshot(policy, highWaterMillis)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := slot.beginReplace(policy, revision, highWaterMillis, state)
	if err != nil {
		t.Fatal(err)
	}
	stage.Commit()
}
