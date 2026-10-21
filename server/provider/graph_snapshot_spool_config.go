package provider

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/replication"
)

// GraphSnapshotSpoolConfig owns the graph-only receiver's explicit temporary
// storage contract. It never inherits the Receipt WAL path or system temp dir.
type GraphSnapshotSpoolConfig = replication.GraphSnapshotSpoolConfig

func NewGraphSnapshotSpoolConfig(config *Config) GraphSnapshotSpoolConfig {
	return config.GraphSnapshotSpool
}

func loadGraphSnapshotSpoolConfig() (GraphSnapshotSpoolConfig, error) {
	const (
		directoryKey = "LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR"
		diskKey      = "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES"
		frameKey     = "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES"
	)
	directory := strings.TrimSpace(envconfig.String(directoryKey, ""))
	diskText := strings.TrimSpace(envconfig.String(diskKey, "0"))
	frameText := strings.TrimSpace(envconfig.String(frameKey, "0"))
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		if strings.HasPrefix(strings.ToUpper(name), "LANTERN_GRAPH_SNAPSHOT_SPOOL_") &&
			name != directoryKey && name != diskKey && name != frameKey {
			return GraphSnapshotSpoolConfig{}, errors.New("unknown graph Snapshot spool setting")
		}
	}
	parse := func(key, text string, bits int) (uint64, error) {
		if text == "" {
			return 0, nil
		}
		value, err := strconv.ParseUint(text, 10, bits)
		if err != nil {
			return 0, fmt.Errorf("%s must be a nonnegative integer in bytes", key)
		}
		return value, nil
	}
	disk, err := parse(diskKey, diskText, 64)
	if err != nil {
		return GraphSnapshotSpoolConfig{}, err
	}
	frame, err := parse(frameKey, frameText, strconv.IntSize-1)
	if err != nil {
		return GraphSnapshotSpoolConfig{}, err
	}
	return GraphSnapshotSpoolConfig{Directory: directory, MaxDiskBytes: disk, MaxFrameBytes: int(frame)}, nil
}

func validateGraphSnapshotSpoolConfig(config GraphSnapshotSpoolConfig, mode ReceiptWALMode, peers PeerPlaneConfig) error {
	if config == (GraphSnapshotSpoolConfig{}) {
		if mode == ReceiptWALModeGraphOnly && peers.ListenAddress != "" {
			return errors.New("graph-only HA requires an explicit Snapshot spool directory, disk quota and frame limit")
		}
		return nil
	}
	if mode != ReceiptWALModeGraphOnly {
		return errors.New("graph Snapshot spool settings require graph-only runtime mode")
	}
	return config.Validate()
}
