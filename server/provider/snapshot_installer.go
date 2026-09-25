package provider

import (
	"errors"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
)

const (
	receiptSnapshotInstallerMaxFrameBytes = 8 << 20
	receiptSnapshotInstallerMaxFrames     = 1 << 20
	receiptSnapshotInstallerMaxTotalBytes = 512 << 20
	receiptSnapshotInstallerMaxOrigins    = 1 << 16
)

// SnapshotInstallerSelection owns the one format policy and receipt installer
// shared by Pump and anti-entropy. A nil installer preserves graph-only mode.
type SnapshotInstallerSelection struct {
	installer replication.SnapshotInstaller
}

func NewSnapshotInstallerSelection(
	config ReceiptWALConfig,
	cacheConfig CacheConfig,
	searchConfig SearchConfig,
	runtime *service.ServingRuntime,
	primary *service.LanternService,
	logger *slog.Logger,
	certified runtimeCertified,
) (*SnapshotInstallerSelection, error) {
	if runtime == nil || primary == nil || !certified.valid ||
		certified.runtime != runtime || certified.primary != primary ||
		certified.replication == nil {
		return nil, errors.New("receipt Snapshot installer requires a certified serving runtime")
	}
	switch config.Mode {
	case ReceiptWALModeGraphOnly:
		if runtime.DurableReceiptWAL() {
			return nil, errors.New("graph-only Snapshot installer selection received a durable runtime")
		}
		return &SnapshotInstallerSelection{}, nil
	case ReceiptWALModeFresh, ReceiptWALModeRestart:
		if !runtime.DurableReceiptWAL() {
			return nil, errors.New("receipt Snapshot installer selection received a graph-only runtime")
		}
	default:
		return nil, errors.New("receipt Snapshot installer selection received an invalid runtime mode")
	}

	maxReceipts := uint64(config.MaxEntries)
	if maxReceipts > receiptSnapshotInstallerMaxFrames-2 {
		maxReceipts = receiptSnapshotInstallerMaxFrames - 2
	}
	collector, err := backup.NewReceiptSnapshotCollector(backup.ReceiptSnapshotCollectorConfig{
		TempDir: filepath.Dir(config.Path),
		Limits: backup.ReceiptSnapshotCollectorLimits{
			MaxFrameBytes:  receiptSnapshotInstallerMaxFrameBytes,
			MaxFrames:      receiptSnapshotInstallerMaxFrames,
			MaxTotalBytes:  receiptSnapshotInstallerMaxTotalBytes,
			MaxReceipts:    maxReceipts,
			MaxOrigins:     receiptSnapshotInstallerMaxOrigins,
			MaxGraphFrames: receiptSnapshotInstallerMaxFrames - 2,
		},
		ExpectedPolicy: config.receiptConfig(time.Time{}),
		DefaultTTL:     cacheConfig.TTL,
		ConfigureGraph: receiptWALGraphConfigurator(cacheConfig, searchConfig),
	})
	if err != nil {
		return nil, err
	}
	installer, err := backup.NewReceiptSnapshotInstaller(collector, primary, logger)
	if err != nil {
		return nil, err
	}
	return &SnapshotInstallerSelection{installer: installer}, nil
}

func (s *SnapshotInstallerSelection) selected() replication.SnapshotInstaller {
	if s == nil {
		return nil
	}
	return s.installer
}
