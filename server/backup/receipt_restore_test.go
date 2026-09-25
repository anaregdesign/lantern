package backup

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/mutationreceipt"
)

func TestPrepareRestartReceiptStartupRestore(t *testing.T) {
	_, archive, _, _, loaded := completedReceiptBackupSet(t)
	evidence := receiptBackupSetEvidence(loaded)

	restore, err := PrepareRestartReceiptStartupRestore(evidence, archive.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if restore.BackupSetID != evidence.SetID ||
		restore.NodeID != evidence.NodeID ||
		restore.Generation != evidence.Generation ||
		restore.WALCut != evidence.WALCut ||
		restore.ArchivedEpoch != archive.Policy.Epoch ||
		restore.ArchivedPolicy != archive.Receipts.PolicyFingerprint ||
		!reflect.DeepEqual(restore.Capture.Receipts, archive.Receipts) ||
		!reflect.DeepEqual(restore.Capture.Retired, loaded.retired) {
		t.Fatalf("restart restore evidence = %+v", restore)
	}

	wrong := archive.Policy
	wrong.Retention += time.Hour
	if _, err := PrepareRestartReceiptStartupRestore(evidence, wrong); err == nil {
		t.Fatal("restart restore accepted a different receipt policy")
	}
}

func TestPrepareFreshReceiptStartupRestoreRotatesActiveEpoch(t *testing.T) {
	_, archive, _, _, loaded := completedReceiptBackupSet(t)
	evidence := receiptBackupSetEvidence(loaded)
	target := mutationreceipt.Config{
		Epoch:      mutationreceipt.Epoch{0xf1},
		Retention:  2 * time.Hour,
		MaxEntries: 16,
		MaxBytes:   1 << 20,
	}
	now := time.UnixMilli(archive.Receipts.ClockHighWaterMillis).Add(time.Minute)
	target.ClockHighWater = now.Add(time.Minute)
	effectiveHighWater := target.ClockHighWater

	restore, err := PrepareFreshReceiptStartupRestore(evidence, target, now)
	if err != nil {
		t.Fatal(err)
	}
	if restore.ArchivedEpoch != archive.Policy.Epoch ||
		restore.ArchivedPolicy != archive.Receipts.PolicyFingerprint ||
		restore.Capture.Policy.Epoch != target.Epoch ||
		restore.Capture.Receipts.Epoch != target.Epoch ||
		len(restore.Capture.Receipts.Receipts) != 0 ||
		restore.Capture.Receipts.ClockHighWaterMillis != effectiveHighWater.UnixMilli() ||
		restore.Capture.Retired.ClockHighWaterMillis != effectiveHighWater.UnixMilli() ||
		!reflect.DeepEqual(restore.Capture.Graph, archive.Graph) ||
		!reflect.DeepEqual(restore.Capture.Origins, archive.Origins) {
		t.Fatalf("fresh restore = %+v", restore)
	}
	foundArchivedActive := len(archive.Receipts.Receipts) == 0
	for _, member := range restore.Capture.Retired.Epochs {
		if member.Policy.Epoch == archive.Policy.Epoch {
			foundArchivedActive = reflect.DeepEqual(
				member.State.Receipts,
				archive.Receipts.Receipts,
			)
		}
		if member.Policy.Epoch == target.Epoch {
			t.Fatal("fresh active epoch leaked into retired evidence")
		}
	}
	if !foundArchivedActive {
		t.Fatal("archived active receipt snapshot was not retained as retired evidence")
	}
	if _, err := (ReceiptBaselineCodec{}).EncodeCombinedReceiptBaseline(
		t.Context(),
		restore.Capture,
	); err != nil {
		t.Fatalf("fresh restore is not a canonical combined baseline: %v; capture=%+v", err, restore.Capture)
	}

	sameEpoch := target
	sameEpoch.Epoch = archive.Policy.Epoch
	if _, err := PrepareFreshReceiptStartupRestore(evidence, sameEpoch, now); err == nil {
		t.Fatal("fresh restore accepted the archived active epoch")
	}
}

func TestPrepareFreshReceiptStartupRestoreChargesRawUnionBeforePruning(t *testing.T) {
	_, archive, _, _, loaded := completedReceiptBackupSet(t)
	evidence := receiptBackupSetEvidence(loaded)
	latestDeadline := archive.Receipts.ClockHighWaterMillis
	rawEntries := len(archive.Receipts.Receipts)
	for _, receipt := range archive.Receipts.Receipts {
		if latestDeadline < receipt.DeadlineMillis {
			latestDeadline = receipt.DeadlineMillis
		}
	}
	for _, member := range loaded.retired.Epochs {
		rawEntries += len(member.State.Receipts)
		for _, receipt := range member.State.Receipts {
			if latestDeadline < receipt.DeadlineMillis {
				latestDeadline = receipt.DeadlineMillis
			}
		}
	}
	if rawEntries < 2 {
		t.Fatalf("fixture raw receipt count = %d, want at least two", rawEntries)
	}
	target := mutationreceipt.Config{
		Epoch:      mutationreceipt.Epoch{0xf2},
		Retention:  time.Hour,
		MaxEntries: rawEntries - 1,
		MaxBytes:   1 << 20,
	}
	now := time.UnixMilli(latestDeadline + 1)
	if _, err := PrepareFreshReceiptStartupRestore(
		evidence,
		target,
		now,
	); !errors.Is(err, mutationreceipt.ErrRetiredCatalogCapacity) {
		t.Fatalf("expired over-capacity raw union error = %v, want %v", err, mutationreceipt.ErrRetiredCatalogCapacity)
	}

	target.MaxEntries = rawEntries
	restore, err := PrepareFreshReceiptStartupRestore(evidence, target, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(restore.Capture.Retired.Epochs) != 0 {
		t.Fatalf("expired retired evidence was not pruned: %+v", restore.Capture.Retired)
	}
}
