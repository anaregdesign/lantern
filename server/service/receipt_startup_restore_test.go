package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
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
	restore := ReceiptStartupRestore{
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
	config.StartupRestore = &restore
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

	config.StartupRestore = nil
	if opened, err := OpenDurableReceiptWALServingRuntime(config); opened != nil ||
		!errors.Is(err, ErrDurableReceiptWALBackupFallbackEligible) {
		if opened != nil {
			_ = opened.Close()
		}
		t.Fatalf("normal restart = %p, %v; want eligible sidecar failure", opened, err)
	}

	config.StartupRestore = &restore
	repaired, err := OpenDurableReceiptWALServingRuntimeFromBackup(config)
	if err != nil {
		t.Fatal(err)
	}
	repairedPrimary := repaired.NewLanternService(nil).WithTombstoneTTL(time.Hour)
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

	config.StartupRestore = nil
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
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
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
	base := ReceiptStartupRestore{
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
		mutate func(*ReceiptStartupRestore)
		want   string
	}{
		{
			name: "node ID",
			mutate: func(restore *ReceiptStartupRestore) {
				restore.NodeID[0] ^= 1
			},
			want: "identity or epoch",
		},
		{
			name: "epoch",
			mutate: func(restore *ReceiptStartupRestore) {
				restore.ArchivedEpoch[0] ^= 1
			},
			want: "identity or epoch",
		},
		{
			name: "policy",
			mutate: func(restore *ReceiptStartupRestore) {
				restore.ArchivedPolicy[0] ^= 1
			},
			want: "policy differs",
		},
		{
			name: "generation",
			mutate: func(restore *ReceiptStartupRestore) {
				restore.Generation[0] ^= 1
			},
			want: "generation at backup cut differs",
		},
		{
			name: "WAL witness",
			mutate: func(restore *ReceiptStartupRestore) {
				restore.WALCut.SHA256[0] ^= 1
			},
			want: "WAL prefix differs",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restore := cloneReceiptStartupRestore(base)
			tc.mutate(&restore)
			attempt := config
			attempt.StartupRestore = &restore
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
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
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
	restore := ReceiptStartupRestore{
		Capture:        backupCapture.WholeState,
		WALCut:         backupCapture.WALTip,
		NodeID:         backupCapture.NodeID,
		Generation:     backupCapture.Generation,
		ArchivedEpoch:  config.Receipt.Epoch,
		ArchivedPolicy: backupCapture.WholeState.Receipts.PolicyFingerprint,
		BackupSetID:    1,
	}
	config.StartupRestore = &restore
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
	config.StartupRestore = nil
	if opened, err := OpenDurableReceiptWALServingRuntime(config); opened != nil ||
		!errors.Is(err, ErrDurableReceiptWALBackupFallbackEligible) {
		if opened != nil {
			_ = opened.Close()
		}
		t.Fatalf("restart after interrupted quarantine = %p, %v", opened, err)
	}
	config.StartupRestore = &restore
	repaired, err = OpenDurableReceiptWALServingRuntimeFromBackup(config)
	if err != nil {
		t.Fatal(err)
	}
	repairedPrimary := repaired.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	if err := repaired.CompleteStartupRestore(t.Context(), repairedPrimary); err != nil {
		_ = repaired.Close()
		t.Fatal(err)
	}
	if err := repaired.Close(); err != nil {
		t.Fatal(err)
	}
	config.StartupRestore = nil
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
			primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
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
			restore := ReceiptStartupRestore{
				Capture:        backupCapture.WholeState,
				WALCut:         backupCapture.WALTip,
				NodeID:         backupCapture.NodeID,
				Generation:     backupCapture.Generation,
				ArchivedEpoch:  config.Receipt.Epoch,
				ArchivedPolicy: backupCapture.WholeState.Receipts.PolicyFingerprint,
				BackupSetID:    1,
			}
			config.StartupRestore = &restore
			repaired, err := OpenDurableReceiptWALServingRuntimeFromBackup(config)
			if err != nil {
				t.Fatal(err)
			}
			repairedPrimary := repaired.NewLanternService(nil).WithTombstoneTTL(time.Hour)
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

			config.StartupRestore = nil
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
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
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
	config.StartupRestore = &ReceiptStartupRestore{
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
