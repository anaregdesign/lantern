package dataruntime

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
)

func TestReceiptStartupRestoreRepairsMissingSameEpochBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	build := image.codec.build
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	if err := primary.InstallReceiptBaseline(context.Background(), image.capture); err != nil {
		t.Fatal(err)
	}
	source, err := NewReceiptWholeStateSource(primary, runtime.receipt.store)
	if err != nil {
		t.Fatal(err)
	}
	backupCapture, err := source.CaptureForBackup(context.Background(), runtime.receipt.policy)
	if err != nil {
		t.Fatal(err)
	}
	bindReceiptStartupTestCodec(t, image.codec, build, backupCapture.WholeState)
	committed := runtime.receipt.committedBaseline
	restore := receiptStartupRestore{
		Capture:        backupCapture.WholeState,
		WALCut:         backupCapture.WALTip,
		NodeID:         backupCapture.NodeID,
		Generation:     backupCapture.Generation,
		ArchivedEpoch:  config.Receipt.Epoch,
		ArchivedPolicy: backupCapture.WholeState.Receipts.PolicyFingerprint,
		BackupSetID:    1,
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	config.startupRestore = &restore
	if opened, err := OpenDurableReceiptWALServingRuntimeFromBackup(config); opened != nil ||
		err == nil || !strings.Contains(err.Error(), "baseline is valid") {
		if opened != nil {
			_ = opened.Close()
		}
		t.Fatalf("fallback with valid current baseline = %p, %v", opened, err)
	}
	if err := os.Remove(receiptBaselineSidecarPath(path, committed.Digest)); err != nil {
		t.Fatal(err)
	}

	config.startupRestore = nil
	if opened, err := OpenDurableReceiptWALServingRuntime(config); opened != nil ||
		!errors.Is(err, ErrDurableReceiptWALBackupFallbackEligible) {
		if opened != nil {
			_ = opened.Close()
		}
		t.Fatalf("normal restart = %p, %v; want eligible sidecar failure", opened, err)
	}

	config.startupRestore = &restore
	repaired, err := OpenDurableReceiptWALServingRuntimeFromBackup(config)
	if err != nil {
		t.Fatal(err)
	}
	repairedPrimary := repaired.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	if err := repaired.CompleteStartupRestore(context.Background(), repairedPrimary); err != nil {
		_ = repaired.Close()
		t.Fatal(err)
	}
	if repaired.receipt.generation == backupCapture.Generation {
		_ = repaired.Close()
		t.Fatal("startup restore did not rotate the endpoint generation")
	}
	if repaired.receipt.committedBaseline == (receiptBaselineReference{}) {
		_ = repaired.Close()
		t.Fatal("startup restore did not commit a canonical baseline")
	}
	if err := repaired.Close(); err != nil {
		t.Fatal(err)
	}

	config.startupRestore = nil
	restarted, err := OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := restarted.Close(); err != nil {
			t.Error(err)
		}
	}()
	if restarted.receipt.generation != repaired.receipt.generation {
		t.Fatal("normal restart did not retain the restored generation")
	}
	if _, ok := restarted.graph.GetVertex("baseline-searchable"); !ok {
		t.Fatal("normal restart lost the restored graph")
	}
}

func TestReceiptStartupRestoreRejectsUnprovenRestartEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	build := image.codec.build
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	if err := primary.InstallReceiptBaseline(context.Background(), image.capture); err != nil {
		t.Fatal(err)
	}
	source, err := NewReceiptWholeStateSource(primary, runtime.receipt.store)
	if err != nil {
		t.Fatal(err)
	}
	backupCapture, err := source.CaptureForBackup(context.Background(), runtime.receipt.policy)
	if err != nil {
		t.Fatal(err)
	}
	bindReceiptStartupTestCodec(t, image.codec, build, backupCapture.WholeState)
	sidecar := receiptBaselineSidecarPath(path, runtime.receipt.committedBaseline.Digest)
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sidecar); err != nil {
		t.Fatal(err)
	}
	base := receiptStartupRestore{
		Capture:        backupCapture.WholeState,
		WALCut:         backupCapture.WALTip,
		NodeID:         backupCapture.NodeID,
		Generation:     backupCapture.Generation,
		ArchivedEpoch:  config.Receipt.Epoch,
		ArchivedPolicy: backupCapture.WholeState.Receipts.PolicyFingerprint,
		BackupSetID:    1,
	}

	for _, tc := range []struct {
		name   string
		mutate func(*receiptStartupRestore)
		want   string
	}{
		{
			name: "node ID",
			mutate: func(restore *receiptStartupRestore) {
				restore.NodeID[0] ^= 1
			},
			want: "identity or epoch",
		},
		{
			name: "epoch",
			mutate: func(restore *receiptStartupRestore) {
				restore.ArchivedEpoch[0] ^= 1
			},
			want: "identity or epoch",
		},
		{
			name: "policy",
			mutate: func(restore *receiptStartupRestore) {
				restore.ArchivedPolicy[0] ^= 1
			},
			want: "policy differs",
		},
		{
			name: "generation",
			mutate: func(restore *receiptStartupRestore) {
				restore.Generation[0] ^= 1
			},
			want: "generation at backup cut differs",
		},
		{
			name: "WAL witness",
			mutate: func(restore *receiptStartupRestore) {
				restore.WALCut.SHA256[0] ^= 1
			},
			want: "WAL prefix differs",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restore := cloneReceiptStartupRestore(base)
			tc.mutate(&restore)
			attempt := config
			attempt.startupRestore = &restore
			got, err := OpenDurableReceiptWALServingRuntimeFromBackup(attempt)
			if got != nil {
				_ = got.Close()
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("fallback error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestReceiptStartupRestoreRepairsCorruptSameEpochBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	build := image.codec.build
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
		t.Fatal(err)
	}
	source, err := NewReceiptWholeStateSource(primary, runtime.receipt.store)
	if err != nil {
		t.Fatal(err)
	}
	backupCapture, err := source.CaptureForBackup(t.Context(), runtime.receipt.policy)
	if err != nil {
		t.Fatal(err)
	}
	bindReceiptStartupTestCodec(t, image.codec, build, backupCapture.WholeState)
	sidecar := receiptBaselineSidecarPath(
		path,
		runtime.receipt.committedBaseline.Digest,
	)
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte("corrupt committed baseline")
	if err := os.WriteFile(sidecar, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if opened, err := OpenDurableReceiptWALServingRuntime(config); opened != nil ||
		!errors.Is(err, ErrDurableReceiptWALBackupFallbackEligible) {
		if opened != nil {
			_ = opened.Close()
		}
		t.Fatalf("normal corrupt-sidecar restart = %p, %v", opened, err)
	}
	restore := receiptStartupRestore{
		Capture:        backupCapture.WholeState,
		WALCut:         backupCapture.WALTip,
		NodeID:         backupCapture.NodeID,
		Generation:     backupCapture.Generation,
		ArchivedEpoch:  config.Receipt.Epoch,
		ArchivedPolicy: backupCapture.WholeState.Receipts.PolicyFingerprint,
		BackupSetID:    1,
	}
	config.startupRestore = &restore
	repaired, err := OpenDurableReceiptWALServingRuntimeFromBackup(config)
	if err != nil {
		t.Fatal(err)
	}
	quarantined, err := filepath.Glob(sidecar + ".quarantine-*")
	if err != nil || len(quarantined) != 1 {
		_ = repaired.Close()
		t.Fatalf("quarantined baseline = %v, %v; want one", quarantined, err)
	}
	if preserved, err := os.ReadFile(quarantined[0]); err != nil ||
		string(preserved) != string(corrupt) {
		_ = repaired.Close()
		t.Fatalf("quarantined baseline bytes = %q, %v", preserved, err)
	}
	if err := repaired.Close(); err != nil {
		t.Fatal(err)
	}
	config.startupRestore = nil
	if opened, err := OpenDurableReceiptWALServingRuntime(config); opened != nil ||
		!errors.Is(err, ErrDurableReceiptWALBackupFallbackEligible) {
		if opened != nil {
			_ = opened.Close()
		}
		t.Fatalf("restart after interrupted quarantine = %p, %v", opened, err)
	}
	config.startupRestore = &restore
	repaired, err = OpenDurableReceiptWALServingRuntimeFromBackup(config)
	if err != nil {
		t.Fatal(err)
	}
	repairedPrimary := repaired.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	if err := repaired.CompleteStartupRestore(t.Context(), repairedPrimary); err != nil {
		_ = repaired.Close()
		t.Fatal(err)
	}
	if err := repaired.Close(); err != nil {
		t.Fatal(err)
	}
	config.startupRestore = nil
	restarted, err := OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := restarted.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, ok := restarted.graph.GetVertex("baseline-searchable"); !ok {
		t.Fatal("corrupt-sidecar repair lost the restored graph")
	}
}

func TestReceiptStartupRestorePublicationCrashesRemainFailClosed(t *testing.T) {
	tests := []struct {
		name          string
		sidecarPoint  receiptBaselineSidecarFaultPoint
		installPoint  receiptBaselineInstallFaultPoint
		panics        bool
		wantCommitted bool
	}{
		{
			name:         "before sidecar rename",
			sidecarPoint: receiptBaselineBeforeRename,
		},
		{
			name:         "after sidecar rename",
			sidecarPoint: receiptBaselineAfterRename,
		},
		{
			name:         "after sidecar before marker",
			installPoint: receiptBaselineAfterSidecarBeforeFinalCut,
		},
		{
			name:          "after marker before publish",
			installPoint:  receiptBaselineAfterMarkerBeforePublish,
			panics:        true,
			wantCommitted: true,
		},
		{
			name:          "after publish",
			installPoint:  receiptBaselineAfterPublish,
			panics:        true,
			wantCommitted: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "receipts.wal")
			config := baselineRuntimeTestConfig(path)
			image := newReceiptBaselineTestImage(t, config)
			build := image.codec.build
			config.BaselineCodec = image.codec
			runtime, err := CreateDurableReceiptWALServingRuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			primary := runtime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
			if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
				t.Fatal(err)
			}
			source, err := NewReceiptWholeStateSource(primary, runtime.receipt.store)
			if err != nil {
				t.Fatal(err)
			}
			backupCapture, err := source.CaptureForBackup(t.Context(), runtime.receipt.policy)
			if err != nil {
				t.Fatal(err)
			}
			bindReceiptStartupTestCodec(t, image.codec, build, backupCapture.WholeState)
			committed := runtime.receipt.committedBaseline
			if err := runtime.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(receiptBaselineSidecarPath(path, committed.Digest)); err != nil {
				t.Fatal(err)
			}
			image.codec.raw = []byte("canonical-startup-restore-replacement")
			restore := receiptStartupRestore{
				Capture:        backupCapture.WholeState,
				WALCut:         backupCapture.WALTip,
				NodeID:         backupCapture.NodeID,
				Generation:     backupCapture.Generation,
				ArchivedEpoch:  config.Receipt.Epoch,
				ArchivedPolicy: backupCapture.WholeState.Receipts.PolicyFingerprint,
				BackupSetID:    1,
			}
			config.startupRestore = &restore
			repaired, err := OpenDurableReceiptWALServingRuntimeFromBackup(config)
			if err != nil {
				t.Fatal(err)
			}
			repairedPrimary := repaired.NewExecutor(nil).WithTombstoneTTL(time.Hour)
			if tc.sidecarPoint != "" {
				repaired.receipt.sidecarFault = func(point receiptBaselineSidecarFaultPoint) error {
					if point == tc.sidecarPoint {
						return errors.New("injected startup sidecar crash")
					}
					return nil
				}
			}
			if tc.installPoint != "" {
				repaired.receipt.installFault = func(point receiptBaselineInstallFaultPoint) error {
					if point == tc.installPoint {
						return errors.New("injected startup baseline crash")
					}
					return nil
				}
			}
			if tc.panics {
				func() {
					defer func() {
						if recover() == nil {
							t.Error("startup restore publication did not panic")
						}
					}()
					_ = repaired.CompleteStartupRestore(t.Context(), repairedPrimary)
				}()
			} else if err := repaired.CompleteStartupRestore(
				t.Context(),
				repairedPrimary,
			); err == nil {
				t.Fatal("startup restore publication crash succeeded")
			}
			if err := repaired.Close(); err != nil {
				t.Fatal(err)
			}

			config.startupRestore = nil
			restarted, err := OpenDurableReceiptWALServingRuntime(config)
			if !tc.wantCommitted {
				if restarted != nil {
					_ = restarted.Close()
				}
				if restarted != nil ||
					!errors.Is(err, ErrDurableReceiptWALBackupFallbackEligible) {
					t.Fatalf(
						"uncommitted startup crash restart = %p, %v",
						restarted,
						err,
					)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := restarted.Close(); err != nil {
					t.Error(err)
				}
			}()
			if _, ok := restarted.graph.GetVertex("baseline-searchable"); !ok {
				t.Fatal("committed startup crash lost the restored graph")
			}
		})
	}
}

func TestReceiptStartupRestoreRejectsGenerationAfterBackupCut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := baselineRuntimeTestConfig(path)
	image := newReceiptBaselineTestImage(t, config)
	build := image.codec.build
	config.BaselineCodec = image.codec
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
		t.Fatal(err)
	}
	source, err := NewReceiptWholeStateSource(primary, runtime.receipt.store)
	if err != nil {
		t.Fatal(err)
	}
	backupCapture, err := source.CaptureForBackup(t.Context(), runtime.receipt.policy)
	if err != nil {
		t.Fatal(err)
	}
	bindReceiptStartupTestCodec(t, image.codec, build, backupCapture.WholeState)
	if err := primary.InstallReceiptBaseline(t.Context(), image.capture); err != nil {
		t.Fatal(err)
	}
	sidecar := receiptBaselineSidecarPath(
		path,
		runtime.receipt.committedBaseline.Digest,
	)
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sidecar); err != nil {
		t.Fatal(err)
	}
	if opened, err := OpenDurableReceiptWALServingRuntime(config); opened != nil ||
		!errors.Is(err, ErrDurableReceiptWALBackupFallbackEligible) {
		if opened != nil {
			_ = opened.Close()
		}
		t.Fatalf("normal post-cut baseline restart = %p, %v", opened, err)
	}
	config.startupRestore = &receiptStartupRestore{
		Capture:        backupCapture.WholeState,
		WALCut:         backupCapture.WALTip,
		NodeID:         backupCapture.NodeID,
		Generation:     backupCapture.Generation,
		ArchivedEpoch:  config.Receipt.Epoch,
		ArchivedPolicy: backupCapture.WholeState.Receipts.PolicyFingerprint,
		BackupSetID:    1,
	}
	if opened, err := OpenDurableReceiptWALServingRuntimeFromBackup(config); opened != nil ||
		err == nil || !strings.Contains(err.Error(), "follows the backup WAL cut") {
		if opened != nil {
			_ = opened.Close()
		}
		t.Fatalf("post-cut generation fallback = %p, %v", opened, err)
	}
}

func bindReceiptStartupTestCodec(
	t *testing.T,
	codec *receiptBaselineTestCodec,
	build func() (*ReceiptBaselineCandidate, error),
	capture ReceiptWholeStateCapture,
) {
	t.Helper()
	cutoff, ok := receiptSnapshotHLC(capture.Graph[0].GetHeader().GetCutoffHlc())
	if !ok {
		t.Fatal("backup capture cutoff is invalid")
	}
	codec.build = func() (*ReceiptBaselineCandidate, error) {
		candidate, err := build()
		if err != nil {
			return nil, err
		}
		receipts, err := mutationreceipt.NewFromSnapshot(capture.Policy, capture.Receipts)
		if err != nil {
			return nil, err
		}
		candidate.Receipts = receipts
		candidate.Retired = capture.Retired
		candidate.Policy = capture.Policy
		candidate.Origins = capture.Origins
		candidate.CutoffLocalSeq = capture.Graph[0].GetHeader().GetCutoffLocalSeq()
		candidate.CutoffHLC = cutoff
		return candidate, nil
	}
}

func validateFreshReceiptStartupRestore(
	config ReceiptDatabaseConfig,
	restore receiptStartupRestore,
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
	config ReceiptDatabaseConfig,
	restore receiptStartupRestore,
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
	config ReceiptDatabaseConfig,
	restore receiptStartupRestore,
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
		durableRuntimeGraphPolicy(config),
	)
	if err != nil {
		return nil, fmt.Errorf("service: stage receipt startup restore: %w", err)
	}
	if stage == nil || stage.Graph == nil || stage.Receipts == nil ||
		stage.NamespaceFormat != config.NamespaceFormat ||
		stage.CutoffLocalSeq != restore.WALCut.Seq ||
		!stage.CutoffHLC.Equal(mustReceiptStartupRestoreCutoff(restore)) {
		return nil, errors.New("service: staged receipt startup restore provenance differs")
	}
	return stage, nil
}

func mustReceiptStartupRestoreCutoff(restore receiptStartupRestore) hlc.Timestamp {
	cutoff, _ := receiptSnapshotHLC(restore.Capture.Graph[0].GetHeader().GetCutoffHlc())
	return cutoff
}

// OpenDurableReceiptWALServingRuntimeFromBackup is the narrow same-epoch
// restart repair path. The caller must first attempt normal restart and may
// call this only for ErrDurableReceiptWALBackupFallbackEligible. One lease
// covers all WAL, journal, identity, generation, backup-cut, suffix, and live
// owner validation.
func OpenDurableReceiptWALServingRuntimeFromBackup(
	config ReceiptDatabaseConfig,
) (*ServingRuntime, error) {
	if err := validateReceiptDatabaseConfig(config); err != nil {
		return nil, err
	}
	if config.startupRestore == nil {
		return nil, errors.New("service: receipt WAL backup fallback requires restore evidence")
	}
	restore := cloneReceiptStartupRestore(*config.startupRestore)
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
	config ReceiptDatabaseConfig,
	restore receiptStartupRestore,
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
		if err := preflightDataNamespaceWAL(path, config, false); err != nil {
			return err
		}
		if err := ValidateDataSnapshotNamespace(restore.Capture.Graph, config.NamespaceFormat); err != nil {
			return err
		}
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
// combined baseline. It must run with the exact primary executor before runtime
// certification and before any listener or background worker is constructed.
func (r *ServingRuntime) CompleteStartupRestore(
	ctx context.Context,
	primary *Executor,
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

func cloneReceiptStartupRestore(restore receiptStartupRestore) receiptStartupRestore {
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
