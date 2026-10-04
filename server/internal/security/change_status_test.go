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
