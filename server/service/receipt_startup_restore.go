package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
)

func validateFreshReceiptStartupRestore(
	config DurableReceiptWALRuntimeConfig,
	restore ReceiptStartupRestore,
) error {
	if restore.ArchivedEpoch == (mutationreceipt.Epoch{}) ||
		restore.ArchivedEpoch == config.Receipt.Epoch {
		return errors.New("service: fresh receipt restore requires a different archived active epoch")
	}
	if restore.Capture.Policy.Epoch != config.Receipt.Epoch ||
		restore.Capture.Receipts.Epoch != config.Receipt.Epoch {
		return errors.New("service: fresh receipt restore target epoch differs from the configured epoch")
	}
	if len(restore.Capture.Receipts.Receipts) != 0 {
		return errors.New("service: fresh receipt restore active Store must be empty")
	}
	_, err := stageReceiptStartupRestore(config, restore)
	return err
}

func validateReceiptStartupRestoreIdentity(
	config DurableReceiptWALRuntimeConfig,
	restore ReceiptStartupRestore,
) error {
	if restore.BackupSetID == 0 ||
		restore.NodeID == (hlc.NodeID{}) ||
		restore.Generation == ([16]byte{}) ||
		restore.ArchivedEpoch == (mutationreceipt.Epoch{}) ||
		restore.ArchivedPolicy == ([sha256.Size]byte{}) {
		return errors.New("service: receipt startup restore identity is incomplete")
	}
	if restore.WALCut.Offset < 8 ||
		(restore.WALCut.Seq == 0) != (restore.WALCut.Offset == 8) ||
		restore.WALCut.SHA256 == ([sha256.Size]byte{}) ||
		restore.WALCut.ChainSHA256 == ([sha256.Size]byte{}) {
		return errors.New("service: receipt startup restore WAL witness is invalid")
	}
	if len(restore.Capture.Graph) < 2 || restore.Capture.Graph[0].GetHeader() == nil {
		return errors.New("service: receipt startup restore graph header is missing")
	}
	header := restore.Capture.Graph[0].GetHeader()
	if header.GetCutoffLocalSeq() != restore.WALCut.Seq {
		return errors.New("service: receipt startup restore graph and WAL cut differ")
	}
	cutoff, ok := receiptSnapshotHLC(header.GetCutoffHlc())
	if !ok || cutoff.NodeID != restore.NodeID {
		return errors.New("service: receipt startup restore graph and NodeID differ")
	}
	if config.BaselineCodec == nil {
		return errors.New("service: receipt startup restore requires the combined baseline codec")
	}
	return nil
}

func stageReceiptStartupRestore(
	config DurableReceiptWALRuntimeConfig,
	restore ReceiptStartupRestore,
) (*ReceiptBaselineCandidate, error) {
	if err := validateReceiptStartupRestoreIdentity(config, restore); err != nil {
		return nil, err
	}
	raw, err := config.BaselineCodec.EncodeCombinedReceiptBaseline(
		context.Background(),
		restore.Capture,
	)
	if err != nil {
		return nil, fmt.Errorf("service: encode receipt startup restore: %w", err)
	}
	stage, err := config.BaselineCodec.StageCombinedReceiptBaseline(
		context.Background(),
		raw,
		clearReceiptClockHighWater(config.Receipt),
		config.DefaultTTL,
		config.ConfigureGraph,
	)
	if err != nil {
		return nil, fmt.Errorf("service: stage receipt startup restore: %w", err)
	}
	if stage == nil || stage.Graph == nil || stage.Receipts == nil ||
		stage.CutoffLocalSeq != restore.WALCut.Seq ||
		!stage.CutoffHLC.Equal(mustReceiptStartupRestoreCutoff(restore)) {
		return nil, errors.New("service: staged receipt startup restore provenance differs")
	}
	return stage, nil
}

func mustReceiptStartupRestoreCutoff(restore ReceiptStartupRestore) hlc.Timestamp {
	cutoff, _ := receiptSnapshotHLC(restore.Capture.Graph[0].GetHeader().GetCutoffHlc())
	return cutoff
}

// OpenDurableReceiptWALServingRuntimeFromBackup is the narrow same-epoch
// restart repair path. The caller must first attempt normal restart and may
// call this only for ErrDurableReceiptWALBackupFallbackEligible. One lease
// covers all WAL, journal, identity, generation, backup-cut, suffix, and live
// owner validation.
func OpenDurableReceiptWALServingRuntimeFromBackup(
	config DurableReceiptWALRuntimeConfig,
) (*ServingRuntime, error) {
	if err := validateDurableReceiptWALRuntimeConfig(config); err != nil {
		return nil, err
	}
	if config.StartupRestore == nil {
		return nil, errors.New("service: receipt WAL backup fallback requires restore evidence")
	}
	restore := cloneReceiptStartupRestore(*config.StartupRestore)
	if restore.NodeID != config.NodeID ||
		restore.ArchivedEpoch != config.Receipt.Epoch ||
		restore.Capture.Policy.Epoch != config.Receipt.Epoch ||
		restore.Capture.Receipts.Epoch != config.Receipt.Epoch {
		return nil, errors.New("service: receipt WAL backup identity or epoch differs from restart")
	}
	configured, err := mutationreceipt.New(clearReceiptClockHighWater(config.Receipt))
	if err != nil {
		return nil, err
	}
	if configured.PolicyFingerprint() != restore.ArchivedPolicy {
		return nil, errors.New("service: receipt WAL backup policy differs from restart")
	}
	candidate, generation, committed, err := openReceiptWALBackupCandidate(config, restore)
	if err != nil {
		return nil, err
	}
	runtime, err := certifyReceiptWALServingRuntime(candidate, config, generation, committed)
	if err != nil {
		return nil, err
	}
	runtime.receipt.startupRestore = &restore
	runtime.receipt.recaptureOnRestore = true
	return runtime, nil
}

func openReceiptWALBackupCandidate(
	config DurableReceiptWALRuntimeConfig,
	restore ReceiptStartupRestore,
) (_ *receiptWALOwnedCandidate, _ [16]byte, _ receiptBaselineReference, err error) {
	lease, err := mutationlog.AcquireFileWALLease(config.Path)
	if err != nil {
		return nil, [16]byte{}, receiptBaselineReference{}, err
	}
	var journal *mutationreceipt.ClockJournal
	var tip *mutationlog.FileWALTipJournal
	var logOwner io.Closer
	defer func() {
		if err != nil {
			if logOwner != nil {
				err = errors.Join(err, logOwner.Close())
			}
			err = errors.Join(err, journal.Close(), tip.Close(), lease.Close())
		}
	}()

	policyStore, err := mutationreceipt.New(clearReceiptClockHighWater(config.Receipt))
	if err != nil {
		return nil, [16]byte{}, receiptBaselineReference{}, err
	}
	policy := policyStore.PolicyFingerprint()
	var genesis [16]byte
	err = lease.WithPath(func(path string) error {
		var readErr error
		genesis, readErr = readReceiptRuntimeGeneration(
			path,
			config.Receipt.Epoch,
			policy,
			config.NodeID,
		)
		return readErr
	})
	if err != nil {
		return nil, [16]byte{}, receiptBaselineReference{}, err
	}

	var scan receiptBaselineWALScan
	var cut mutationlog.FileWALCut
	var damagedBaseline bool
	err = lease.WithPath(func(path string) error {
		var scanErr error
		scan, scanErr = scanReceiptBaselineWAL(path, config.Receipt, config.NodeID)
		if scanErr != nil {
			return scanErr
		}
		if scan.hasMarker && scan.firstGeneration != genesis {
			return errors.New("service: receipt baseline generation does not descend from genesis")
		}
		if scan.hasMarker && scan.markerSequence > restore.WALCut.Seq {
			return errors.New("service: valid receipt baseline generation follows the backup WAL cut")
		}
		damagedBaseline, scanErr = requireReceiptWALBackupFallback(path, scan)
		if scanErr != nil {
			return scanErr
		}
		var inspectErr error
		cut, inspectErr = mutationlog.InspectFileWALCut(
			path,
			restore.WALCut.Seq,
			decodeReceiptWALUnion,
			validateReceiptWALUnionEntry,
		)
		return inspectErr
	})
	if err != nil {
		return nil, [16]byte{}, receiptBaselineReference{}, err
	}
	if cut.Offset != restore.WALCut.Offset ||
		cut.SHA256 != restore.WALCut.SHA256 ||
		cut.ChainSHA256 != restore.WALCut.ChainSHA256 {
		return nil, [16]byte{}, receiptBaselineReference{},
			errors.New("service: current receipt WAL prefix differs from backup witness")
	}
	activeGeneration := genesis
	var committed receiptBaselineReference
	if scan.hasMarker {
		activeGeneration = scan.activeGeneration
		committed = scan.marker.reference()
	}
	if activeGeneration != restore.Generation {
		return nil, [16]byte{}, receiptBaselineReference{},
			errors.New("service: receipt WAL generation at backup cut differs")
	}

	journal, err = mutationreceipt.ResumeClockJournal(lease.Path(), config.Receipt.Epoch, policy)
	if err != nil {
		return nil, [16]byte{}, receiptBaselineReference{},
			fmt.Errorf("receipt WAL clock journal: %w", err)
	}
	tip, err = mutationlog.ResumeFileWALTipJournal(
		lease.Path(),
		receiptWALTipBinding(config.Receipt.Epoch, policy),
	)
	if err != nil {
		return nil, [16]byte{}, receiptBaselineReference{},
			fmt.Errorf("receipt WAL tip journal: %w", err)
	}

	stage, err := stageReceiptStartupRestore(config, restore)
	if err != nil {
		return nil, [16]byte{}, receiptBaselineReference{}, err
	}
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	state, logOwner, err := resumeStagedReceiptBaselineWALCandidate(
		context.Background(),
		lease.Path(),
		config.Receipt,
		now,
		config.Log,
		config.BaselineCodec,
		journal.HighWaterMillis(),
		tip,
		stage,
		stage.CutoffHLC,
		restore.WALCut.Seq,
	)
	if err != nil {
		return nil, [16]byte{}, receiptBaselineReference{}, err
	}
	storeSnapshot, err := state.receipts.Snapshot()
	if err != nil {
		return nil, [16]byte{}, receiptBaselineReference{}, err
	}
	finalConfig := clearReceiptClockHighWater(config.Receipt)
	finalConfig.ClockHighWater = time.UnixMilli(storeSnapshot.ClockHighWaterMillis)
	state.receipts, err = mutationreceipt.NewFromSnapshotWithClockHighWaterSink(
		finalConfig,
		storeSnapshot,
		journal.Advance,
	)
	if err != nil {
		return nil, [16]byte{}, receiptBaselineReference{},
			fmt.Errorf("receipt WAL backup clock binding: %w", err)
	}
	walProvenance, err := state.log.FileWALTipProvenance(lease.Path())
	if err != nil {
		return nil, [16]byte{}, receiptBaselineReference{},
			fmt.Errorf("receipt WAL backup provenance: %w", err)
	}
	if damagedBaseline {
		err = lease.WithPath(func(path string) error {
			_, quarantineErr := (receiptBaselineSidecarStore{walPath: path}).
				quarantineCommittedDamage(
					scan.marker.Format,
					scan.marker.Digest,
					scan.marker.Size,
				)
			return quarantineErr
		})
		if err != nil {
			return nil, [16]byte{}, receiptBaselineReference{},
				fmt.Errorf("service: preserve damaged receipt baseline: %w", err)
		}
	}
	return &receiptWALOwnedCandidate{
		state: state, logOwner: logOwner, journal: journal, tip: tip, lease: lease,
		walProvenance: walProvenance, baseline: scan,
	}, activeGeneration, committed, nil
}

func requireReceiptWALBackupFallback(
	path string,
	scan receiptBaselineWALScan,
) (bool, error) {
	if !scan.hasMarker {
		return false, errors.New("service: current receipt WAL has no committed baseline to repair")
	}
	_, err := (receiptBaselineSidecarStore{walPath: path}).load(
		scan.marker.Format,
		scan.marker.Digest,
		scan.marker.Size,
	)
	switch {
	case err == nil:
		return false, errors.New("service: current receipt WAL baseline is valid")
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case errors.Is(err, errReceiptBaselineSidecar):
		return true, nil
	default:
		return false, fmt.Errorf("service: inspect current receipt WAL baseline: %w", err)
	}
}

// CompleteStartupRestore publishes any pending restore as a fresh canonical
// combined baseline. It must run with the exact primary service before runtime
// certification and before any listener or background worker is constructed.
func (r *ServingRuntime) CompleteStartupRestore(
	ctx context.Context,
	primary *LanternService,
) error {
	if r == nil || primary == nil || primary.runtime != r {
		return errors.New("service: startup restore requires the exact runtime primary")
	}
	if r.receipt == nil || r.receipt.startupRestore == nil {
		return nil
	}
	restore := r.receipt.startupRestore
	capture := cloneReceiptWholeStateCapture(restore.Capture)
	if r.receipt.recaptureOnRestore {
		source, err := NewReceiptWholeStateSource(primary, r.receipt.store)
		if err != nil {
			return fmt.Errorf("service: bind startup restore capture: %w", err)
		}
		capture, err = source.Capture(ctx, r.receipt.policy)
		if err != nil {
			return fmt.Errorf("service: capture recovered receipt runtime: %w", err)
		}
	}
	if err := primary.InstallReceiptBaseline(ctx, capture); err != nil {
		return fmt.Errorf("service: commit startup restore baseline: %w", err)
	}
	r.receipt.startupRestore = nil
	r.receipt.recaptureOnRestore = false
	return nil
}

func cloneReceiptStartupRestore(restore ReceiptStartupRestore) ReceiptStartupRestore {
	restore.Capture = cloneReceiptWholeStateCapture(restore.Capture)
	return restore
}

func cloneReceiptWholeStateCapture(capture ReceiptWholeStateCapture) ReceiptWholeStateCapture {
	capture.Graph = cloneReceiptBaselineGraphFrames(capture.Graph)
	if capture.Receipts.Receipts != nil {
		capture.Receipts.Receipts = append(
			make([]mutationreceipt.Receipt, 0, len(capture.Receipts.Receipts)),
			capture.Receipts.Receipts...,
		)
	}
	for i := range capture.Receipts.Receipts {
		capture.Receipts.Receipts[i].Result = append(
			[]byte(nil),
			capture.Receipts.Receipts[i].Result...,
		)
	}
	if capture.Retired.Epochs != nil {
		capture.Retired.Epochs = append(
			make([]mutationreceipt.RetiredEpochSnapshot, 0, len(capture.Retired.Epochs)),
			capture.Retired.Epochs...,
		)
	}
	for i := range capture.Retired.Epochs {
		rows := append(
			[]mutationreceipt.Receipt(nil),
			capture.Retired.Epochs[i].State.Receipts...,
		)
		for j := range rows {
			rows[j].Result = append([]byte(nil), rows[j].Result...)
		}
		capture.Retired.Epochs[i].State.Receipts = rows
	}
	if capture.Origins != nil {
		capture.Origins = append(
			make([]OriginState, 0, len(capture.Origins)),
			capture.Origins...,
		)
	}
	return capture
}
