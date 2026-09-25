package backup

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/server/service"
)

// PrepareRestartReceiptStartupRestore decodes one already selected backup set
// into same-epoch restore evidence. Live WAL binding and suffix validation
// remain the durable runtime's responsibility under its path lease.
func PrepareRestartReceiptStartupRestore(
	evidence ReceiptBackupSetEvidence,
	expected mutationreceipt.Config,
) (service.ReceiptStartupRestore, error) {
	capture, err := decodeReceiptStartupRestoreEvidence(evidence)
	if err != nil {
		return service.ReceiptStartupRestore{}, err
	}
	expected.ClockHighWater = time.Time{}
	configured, err := mutationreceipt.New(expected)
	if err != nil {
		return service.ReceiptStartupRestore{}, fmt.Errorf("backup: invalid restart receipt policy: %w", err)
	}
	if capture.Policy.Epoch != expected.Epoch ||
		capture.Receipts.Epoch != expected.Epoch ||
		capture.Receipts.PolicyFingerprint != configured.PolicyFingerprint() {
		return service.ReceiptStartupRestore{}, errors.New(
			"backup: active receipt policy or epoch differs from restart",
		)
	}
	return receiptStartupRestore(
		evidence,
		capture,
		capture.Policy.Epoch,
		capture.Receipts.PolicyFingerprint,
	), nil
}

// PrepareFreshReceiptStartupRestore rotates the selected backup set's active
// epoch into bounded retired evidence and returns an empty active Store for
// the configured new epoch. Aggregate capacity is charged to the raw active
// plus retired union before expired rows are pruned.
func PrepareFreshReceiptStartupRestore(
	evidence ReceiptBackupSetEvidence,
	configured mutationreceipt.Config,
	now time.Time,
) (service.ReceiptStartupRestore, error) {
	archived, err := decodeReceiptStartupRestoreEvidence(evidence)
	if err != nil {
		return service.ReceiptStartupRestore{}, err
	}
	if configured.Epoch == archived.Policy.Epoch {
		return service.ReceiptStartupRestore{}, errors.New(
			"backup: fresh receipt restore requires a different active epoch",
		)
	}
	if now.IsZero() {
		now = time.Now()
	}
	effectiveHighWater := archived.Receipts.ClockHighWaterMillis
	if now.UnixMilli() < 0 {
		return service.ReceiptStartupRestore{}, mutationreceipt.ErrInvalidClock
	}
	if effectiveHighWater < now.UnixMilli() {
		effectiveHighWater = now.UnixMilli()
	}
	if !configured.ClockHighWater.IsZero() {
		configuredHighWater := configured.ClockHighWater.UnixMilli()
		if configuredHighWater < 0 {
			return service.ReceiptStartupRestore{}, mutationreceipt.ErrInvalidClock
		}
		if effectiveHighWater < configuredHighWater {
			effectiveHighWater = configuredHighWater
		}
	}

	archivedActive, err := mutationreceipt.RetiredCatalogSnapshotFromActive(
		archived.Policy,
		archived.Receipts,
	)
	if err != nil {
		return service.ReceiptStartupRestore{}, fmt.Errorf(
			"backup: convert archived active receipts: %w",
			err,
		)
	}
	retiredConfig := mutationreceipt.RetiredCatalogConfig{
		ActiveEpoch:    configured.Epoch,
		MaxEntries:     configured.MaxEntries,
		MaxBytes:       configured.MaxBytes,
		ClockHighWater: time.UnixMilli(effectiveHighWater),
	}
	retired, err := mutationreceipt.NewRetiredCatalogFromUnion(
		retiredConfig,
		archived.Retired,
		archivedActive,
	)
	if err != nil {
		return service.ReceiptStartupRestore{}, fmt.Errorf(
			"backup: union fresh retired receipt evidence: %w",
			err,
		)
	}
	retiredState, err := retired.Snapshot(time.UnixMilli(effectiveHighWater))
	if err != nil {
		return service.ReceiptStartupRestore{}, fmt.Errorf(
			"backup: snapshot fresh retired receipt evidence: %w",
			err,
		)
	}
	configured.ClockHighWater = time.UnixMilli(effectiveHighWater)
	active, err := mutationreceipt.New(configured)
	if err != nil {
		return service.ReceiptStartupRestore{}, fmt.Errorf(
			"backup: configure fresh active receipt Store: %w",
			err,
		)
	}
	activeState, err := active.Snapshot()
	if err != nil {
		return service.ReceiptStartupRestore{}, fmt.Errorf(
			"backup: snapshot fresh active receipt Store: %w",
			err,
		)
	}
	capture := service.ReceiptWholeStateCapture{
		Graph:    archived.Graph,
		Receipts: activeState,
		Retired:  retiredState,
		Policy:   configured,
		Origins:  archived.Origins,
	}
	return receiptStartupRestore(
		evidence,
		capture,
		archived.Policy.Epoch,
		archived.Receipts.PolicyFingerprint,
	), nil
}

func decodeReceiptStartupRestoreEvidence(
	evidence ReceiptBackupSetEvidence,
) (service.ReceiptWholeStateCapture, error) {
	if evidence.SetID == 0 ||
		evidence.BackupTimestamp.IsZero() ||
		evidence.NodeID == (hlc.NodeID{}) ||
		evidence.Generation == ([16]byte{}) {
		return service.ReceiptWholeStateCapture{}, errors.New(
			"backup: receipt startup restore identity is incomplete",
		)
	}
	if err := validateReceiptArchiveWALWitness(
		"restore",
		evidence.WALCut.Seq,
		evidence.WALCut.Offset,
		evidence.WALCut.SHA256,
		evidence.WALCut.ChainSHA256,
	); err != nil {
		return service.ReceiptWholeStateCapture{}, err
	}
	archive, err := decodeWholeStateArchive(bytes.NewReader(evidence.Archive))
	if err != nil {
		return service.ReceiptWholeStateCapture{}, err
	}
	var canonical bytes.Buffer
	if err := encodeWholeStateArchive(&canonical, archive); err != nil {
		return service.ReceiptWholeStateCapture{}, err
	}
	if !bytes.Equal(canonical.Bytes(), evidence.Archive) {
		return service.ReceiptWholeStateCapture{}, wholeStateArchiveError(
			"archive is not canonical",
		)
	}
	retired, metadata, err := decodeRetiredCatalogArchive(evidence.RetiredCatalog)
	if err != nil {
		return service.ReceiptWholeStateCapture{}, err
	}
	canonicalRetired, err := encodeRetiredCatalogArchiveWithoutPolicy(metadata, retired)
	if err != nil {
		return service.ReceiptWholeStateCapture{}, err
	}
	if !bytes.Equal(canonicalRetired, evidence.RetiredCatalog) {
		return service.ReceiptWholeStateCapture{}, fmt.Errorf(
			"%w: retired catalog is not canonical",
			errReceiptCombinedBaseline,
		)
	}
	if metadata.ActiveEpoch != archive.Policy.Epoch ||
		metadata.ClockHighWaterMillis != archive.Receipts.ClockHighWaterMillis ||
		metadata.MaxEntries != archive.Policy.MaxEntries ||
		metadata.MaxBytes != archive.Policy.MaxBytes {
		return service.ReceiptWholeStateCapture{}, errors.New(
			"backup: active and retired receipt backup members differ",
		)
	}
	if err := validateReceiptArchiveCapturedWitness(
		archive,
		evidence.WALCut,
		evidence.NodeID,
		evidence.Generation,
	); err != nil {
		return service.ReceiptWholeStateCapture{}, err
	}
	retiredCount, err := receiptBackupRetiredReceiptCount(retired)
	if err != nil || retiredCount > uint64(math.MaxInt)-uint64(len(archive.Receipts.Receipts)) {
		return service.ReceiptWholeStateCapture{}, errors.New(
			"backup: receipt startup restore count overflows",
		)
	}
	stats := receiptArchiveStats(archive)
	stats.Receipts += int(retiredCount)
	stats.Members = 3
	if evidence.Stats.Vertices != stats.Vertices ||
		evidence.Stats.Edges != stats.Edges ||
		evidence.Stats.Receipts != stats.Receipts ||
		evidence.Stats.Origins != stats.Origins ||
		evidence.Stats.Members != stats.Members {
		return service.ReceiptWholeStateCapture{}, errors.New(
			"backup: receipt startup restore statistics differ",
		)
	}
	if err := validateBackupRetiredSnapshot(
		archive.Policy,
		archive.Receipts,
		retired,
	); err != nil {
		return service.ReceiptWholeStateCapture{}, err
	}
	return service.ReceiptWholeStateCapture{
		Graph:    archive.Graph,
		Receipts: archive.Receipts,
		Retired:  retired,
		Policy:   archive.Policy,
		Origins:  archive.Origins,
	}, nil
}

func receiptStartupRestore(
	evidence ReceiptBackupSetEvidence,
	capture service.ReceiptWholeStateCapture,
	archivedEpoch mutationreceipt.Epoch,
	archivedPolicy [32]byte,
) service.ReceiptStartupRestore {
	return service.ReceiptStartupRestore{
		Capture:        capture,
		WALCut:         evidence.WALCut,
		NodeID:         evidence.NodeID,
		Generation:     evidence.Generation,
		ArchivedEpoch:  archivedEpoch,
		ArchivedPolicy: archivedPolicy,
		BackupSetID:    evidence.SetID,
	}
}
