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

func TestRetiredReceiptCatalogSlotLookupManyPreservesIdentityAndClock(t *testing.T) {
	highWater := time.Date(2026, 9, 25, 2, 0, 0, 0, time.UTC).UnixMilli()
	policy := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{0x71}, Retention: time.Hour,
		MaxEntries: 8, MaxBytes: 1 << 20,
	}
	state, id := mustRetiredCatalogSnapshot(t, policy, highWater, 0x72)
	slot, err := newRetiredReceiptCatalogSlot(policy, highWater, state)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := mutationreceipt.NewID(
		state.Epochs[0].Policy.Epoch,
		time.UnixMilli(highWater),
		[24]byte{0x73},
	)
	if err != nil {
		t.Fatal(err)
	}
	later := time.UnixMilli(highWater).Add(time.Minute)
	observations, err := slot.lookupMany(policy, []mutationreceipt.ID{id, unknown, id}, later)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 3 ||
		observations[0].Status != mutationreceipt.Confirmed ||
		observations[1].Status != mutationreceipt.NoLongerProvable ||
		observations[2].Status != mutationreceipt.Confirmed {
		t.Fatalf("retired lookup observations = %+v", observations)
	}
	snapshot, revision, err := slot.snapshot(policy, later.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ClockHighWaterMillis != later.UnixMilli() || revision != 2 {
		t.Fatalf("retired lookup state = high-water %d, revision %d", snapshot.ClockHighWaterMillis, revision)
	}

	active, err := mutationreceipt.NewID(policy.Epoch, later, [24]byte{0x74})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := slot.lookupMany(
		policy,
		[]mutationreceipt.ID{id, active},
		later.Add(time.Minute),
	); got != nil || !errors.Is(err, mutationreceipt.ErrActiveEpochReceipt) {
		t.Fatalf("active-epoch lookup = %+v, %v", got, err)
	}
	after, afterRevision, err := slot.snapshot(policy, later.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, snapshot) || afterRevision != revision {
		t.Fatal("rejected retired lookup changed catalog state")
	}
}
