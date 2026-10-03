package mutationreceipt

import (
	"errors"
	"testing"
	"time"
)

func TestReceiptLifecycleEvidenceDuplicateAndSnapshot(t *testing.T) {
	s := testStore(t, 2, 1000)
	intent := testIntent(t, 1, testStart, GroupID{1}, 0, 1)
	tx, err := s.Begin(testStart)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()
	if _, _, err := tx.Classify([]Intent{intent}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Reserve([][]byte{{1}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.SetReservedLifecycleReductions([]bool{true}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Stage(); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	tx, err = s.Begin(testStart)
	if err != nil {
		t.Fatal(err)
	}
	class, prior, err := tx.Classify([]Intent{intent})
	tx.Abort()
	if err != nil || class != Duplicate || len(prior) != 1 || !prior[0].LifecycleReduction {
		t.Fatal("replay recomputed origin effect", err)
	}
	state, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Epoch: Epoch{1}, Retention: time.Hour, MaxEntries: 2, MaxBytes: 1000}
	restored, err := NewFromSnapshot(config, state)
	if err != nil {
		t.Fatal(err)
	}
	_, row, err := restored.Lookup(intent.ID, testStart)
	if err != nil || !row.LifecycleReduction {
		t.Fatal("recovery lost lifecycle effect", err)
	}
	state.Receipts[0].LifecycleReduction = false
	if _, err := s.BeginSnapshotInstall(state); !errors.Is(err, ErrSnapshotDoesNotDominate) {
		t.Fatal("snapshot erased origin effect", err)
	}
}

func TestReceiptLifecycleEvidenceRejectsNonPut(t *testing.T) {
	s := testStore(t, 2, 1000)
	intent := testIntent(t, 1, testStart, GroupID{1}, 0, 1)
	intent.Kind = DeleteVertex
	tx, err := s.Begin(testStart)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()
	if _, _, err := tx.Classify([]Intent{intent}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Reserve([][]byte{{0}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.SetReservedLifecycleReductions([]bool{true}); !errors.Is(err, ErrInvalidBatch) {
		t.Fatal(err)
	}
	rows, err := tx.ReservedReceipts()
	if err != nil || rows[0].LifecycleReduction {
		t.Fatal("invalid effect partially set", err)
	}
}
