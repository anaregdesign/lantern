package service

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/mutationreceipt"
)

func TestRetiredReceiptCatalogSlotKeepsIdentityAcrossReplaceAndAbort(t *testing.T) {
	highWater := time.Date(2026, 9, 25, 2, 0, 0, 0, time.UTC).UnixMilli()
	policy := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{0x31}, Retention: time.Hour,
		MaxEntries: 8, MaxBytes: 1 << 20,
	}
	slot, err := newEmptyRetiredReceiptCatalogSlot(policy, highWater)
	if err != nil {
		t.Fatal(err)
	}
	identity := slot
	empty, revision, err := slot.snapshot(policy, highWater)
	if err != nil || len(empty.Epochs) != 0 {
		t.Fatalf("fresh retired catalog = %+v, rev=%d, err=%v", empty, revision, err)
	}

	incoming, _ := mustRetiredCatalogSnapshot(t, policy, highWater, 0x41)
	stage, err := slot.beginReplace(policy, revision, highWater, incoming)
	if err != nil {
		t.Fatal(err)
	}
	stage.Abort()
	if slot != identity {
		t.Fatal("retired catalog slot identity changed after abort")
	}
	afterAbort, revisionAfterAbort, err := slot.snapshot(policy, highWater)
	if err != nil || !reflect.DeepEqual(afterAbort, empty) || revisionAfterAbort != revision {
		t.Fatalf("aborted retired replacement = %+v, rev=%d, err=%v", afterAbort, revisionAfterAbort, err)
	}

	stage, err = slot.beginReplace(policy, revision, highWater, incoming)
	if err != nil {
		t.Fatal(err)
	}
	stage.Commit()
	if slot != identity {
		t.Fatal("retired catalog slot identity changed after commit")
	}
	installed, installedRevision, err := slot.snapshot(policy, highWater)
	if err != nil || !reflect.DeepEqual(installed, incoming) || installedRevision != revision+1 {
		t.Fatalf("installed retired catalog = %+v, rev=%d, err=%v", installed, installedRevision, err)
	}
	if _, err := slot.beginReplace(policy, revision, highWater, incoming); !errors.Is(
		err,
		errRetiredCatalogSlotDrift,
	) {
		t.Fatalf("stale retired replacement = %v", err)
	}
}

func TestRetiredReceiptCatalogSlotRejectsPolicyAndClockDrift(t *testing.T) {
	highWater := time.Date(2026, 9, 25, 2, 0, 0, 0, time.UTC).UnixMilli()
	policy := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{0x51}, Retention: time.Hour,
		MaxEntries: 8, MaxBytes: 1 << 20,
	}
	slot, err := newEmptyRetiredReceiptCatalogSlot(policy, highWater)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := slot.snapshot(policy, highWater-1); !errors.Is(
		err,
		mutationreceipt.ErrRetiredCatalogClockRollback,
	) {
		t.Fatalf("retired clock rollback = %v", err)
	}
	wrongPolicy := policy
	wrongPolicy.MaxEntries++
	if _, _, err := slot.snapshot(wrongPolicy, highWater); !errors.Is(
		err,
		mutationreceipt.ErrInvalidRetiredCatalogConfig,
	) {
		t.Fatalf("retired policy drift = %v", err)
	}
	activeEpoch, _ := mustRetiredCatalogSnapshot(t, policy, highWater, 0x61)
	activeEpoch.Epochs[0].Policy.Epoch = policy.Epoch
	activeEpoch.Epochs[0].State.Epoch = policy.Epoch
	if _, err := slot.beginReplace(policy, 1, highWater, activeEpoch); !errors.Is(
		err,
		mutationreceipt.ErrActiveEpochReceipt,
	) {
		t.Fatalf("active-epoch retired replacement = %v", err)
	}
}
