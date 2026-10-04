package security

import (
	"context"
	"testing"
)

func TestSignedChangeHistoryCheckpointPreservesReplayWindow(t *testing.T) {
	options := nativeTestOptions(t, t.TempDir()+"/unused")
	recorder := &fakeCommitter{}
	store, err := NewStore(StoreOptions{Generation: options.Generation, PublicKey: options.PublicKey, PrivateKey: options.PrivateKey, Limits: options.Limits, Committer: recorder})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReconcileBootstrap(context.Background(), 0, [16]byte{1}, testImage()); err != nil {
		t.Fatal(err)
	}
	image := nativeTestSuspendedImage()
	result, err := store.Commit(context.Background(), 1, [16]byte{2}, image)
	if err != nil {
		t.Fatal(err)
	}
	for sequence := uint64(2); sequence < 270; sequence++ {
		id := [16]byte{byte(sequence + 1), byte((sequence + 1) >> 8)}
		if _, err := store.Commit(context.Background(), sequence, id, image); err != nil {
			t.Fatal(sequence, err)
		}
	}
	current, _ := store.Current()
	decoded, err := DecodeRevision(current.Encode(), options.PublicKey, options.Limits)
	if err != nil || !decoded.completeCheckpointHistory() {
		t.Fatal("checkpoint evidence incomplete", err)
	}
	replica, err := NewStore(StoreOptions{Generation: options.Generation, PublicKey: options.PublicKey, PrivateKey: options.PrivateKey, Limits: options.Limits, Committer: &fakeCommitter{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := replica.restoreCheckpoint(decoded); err != nil {
		t.Fatal(err)
	}
	if len(replica.changes) != retainedChanges {
		t.Fatal("checkpoint window is not bounded")
	}
	sequence := uint64(269)
	id := [16]byte{byte(sequence + 1), byte((sequence + 1) >> 8)}
	replay, err := replica.Commit(context.Background(), sequence, id, image)
	if err != nil || !replay.Replayed || replay.Revision != 270 {
		t.Fatal("checkpoint lost retained replay", replay, err)
	}
	if _, known := replica.changes[[16]byte{2}]; known || result.Revision != 2 {
		t.Fatal("old change IDs were retained without bound")
	}
	bad := append([]byte(nil), current.Encode()...)
	bad[revisionHeaderBytes] ^= 1
	if _, err := DecodeRevision(bad, options.PublicKey, options.Limits); err == nil {
		t.Fatal("unsigned history edit accepted")
	}
}
