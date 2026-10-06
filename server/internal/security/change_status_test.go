package security

import (
	"errors"
	"testing"
)

func TestChangeStatusIsRetainedCommitProof(t *testing.T) {
	store, _, _ := testStore(t, true)
	result, err := store.ReconcileBootstrap(t.Context(), 0, [16]byte{1}, testImage())
	if err != nil {
		t.Fatal(err)
	}
	status, err := store.ChangeStatus([16]byte{1})
	if err != nil || status != result {
		t.Fatal(status, err)
	}
	if _, err = store.ChangeStatus([16]byte{2}); !errors.Is(err, ErrUnknownChange) {
		t.Fatal("unknown ID proved noncommit", err)
	}
}

func TestChangeStatusOriginalProofSurvivesSignedReplicaApply(t *testing.T) {
	writer, _, options := testStore(t, true)
	first, err := writer.ReconcileBootstrap(t.Context(), 0, [16]byte{1}, testImage())
	if err != nil {
		t.Fatal(err)
	}
	one, _ := writer.Current()
	options.PrivateKey = nil
	options.Committer = &fakeCommitter{}
	replica, err := NewStore(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := replica.Apply(t.Context(), one.Encode()); err != nil {
		t.Fatal(err)
	}
	second, err := writer.Commit(t.Context(), 1, [16]byte{2}, nativeTestSuspendedImage())
	if err != nil {
		t.Fatal(err)
	}
	two, _ := writer.Current()
	if err := replica.Apply(t.Context(), two.Encode()); err != nil {
		t.Fatal(err)
	}
	for id, expected := range map[[16]byte]ChangeResult{{1}: first, {2}: second} {
		status, err := replica.ChangeStatus(id)
		if err != nil || status != expected {
			t.Fatal("replica changed original retained proof", status, err)
		}
	}
	if first.Digest == second.Digest {
		t.Fatal("fixture did not change current policy")
	}
}
