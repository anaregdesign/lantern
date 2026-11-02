package provider

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
)

func certifiedSnapshotInstallerRuntime(
	t *testing.T,
	config ReceiptWALConfig,
) (*dataruntime.ServingRuntime, *dataruntime.Executor, runtimeCertified) {
	t.Helper()
	replicationConfig := ReplicationConfig{
		NodeID: hlc.NodeID{0x61}, nodeIDExplicit: config.Mode != ReceiptWALModeGraphOnly,
	}
	runtime, cleanup, err := NewServingRuntime(
		config,
		CacheConfig{CacheSettings: CacheSettings{TTL: time.Hour}},
		SearchConfig{},
		MutationLogConfig{Capacity: 8, SubscriberBuffer: 2},
		replicationConfig,
		backup.Config{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	primary := runtime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	replicationService, err := runtime.NewReplication(primary)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewRuntimeRestored(runtime, primary)
	if err != nil {
		t.Fatal(err)
	}
	certified, err := NewRuntimeCertified(runtime, primary, replicationService, restored, NetConfig{})
	if err != nil {
		t.Fatal(err)
	}
	return runtime, primary, certified
}

func TestSnapshotInstallerSelectionMatchesRuntimeAndIsShared(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		name   string
		config func(string) ReceiptWALConfig
		format pb.SnapshotFormat
	}{
		{
			name: "graph only",
			config: func(string) ReceiptWALConfig {
				return ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly}
			},
			format: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
		},
		{
			name: "durable",
			config: func(path string) ReceiptWALConfig {
				return validReceiptWALProviderConfig(path, ReceiptWALModeFresh)
			},
			format: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := tc.config(filepath.Join(t.TempDir(), "receipts.wal"))
			runtime, primary, certified := certifiedSnapshotInstallerRuntime(t, config)
			selection, err := NewSnapshotInstallerSelection(
				config,
				GraphSnapshotSpoolConfig{},
				PeerPlaneConfig{},
				CacheConfig{CacheSettings: CacheSettings{TTL: time.Hour}},
				SearchConfig{},
				runtime,
				primary,
				logger,
				certified,
			)
			if err != nil {
				t.Fatal(err)
			}
			selected := selection.selected()
			if selected == nil {
				t.Fatal("format selection did not provide an owned installer")
			}
			if got := selected.RequiredFormat(); got != tc.format {
				t.Fatalf("required format = %v, want %v", got, tc.format)
			}
			if !selected.CompatibleFormat(tc.format) {
				t.Fatalf("selected installer rejects its required format %v", tc.format)
			}
			otherFormat := pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT
			if tc.format == otherFormat {
				otherFormat = pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1
			}
			if selected.CompatibleFormat(otherFormat) {
				t.Fatalf("selected installer accepts the other runtime format %v", otherFormat)
			}

			pumpConfig := newReplicationPumpConfig(
				PeerConfig{}, nil, ReplicationConfig{}, nil, primary, nil, logger, selection,
			)
			antiEntropyConfig := newAntiEntropyReplicationConfig(
				PeerConfig{}, nil, ReplicationConfig{}, AntiEntropyConfig{}, nil,
				primary, nil, logger, selection,
			)
			if pumpConfig.SnapshotInstaller != selected ||
				antiEntropyConfig.SnapshotInstaller != selected ||
				pumpConfig.SnapshotInstaller != antiEntropyConfig.SnapshotInstaller {
				t.Fatal("Pump and anti-entropy did not receive the exact shared installer")
			}
		})
	}
}

func TestSnapshotInstallerSelectionRejectsRuntimeModeMismatch(t *testing.T) {
	tests := []struct {
		name            string
		runtimeConfig   ReceiptWALConfig
		selectionConfig ReceiptWALConfig
	}{
		{
			name:            "durable selection with graph runtime",
			runtimeConfig:   ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly},
			selectionConfig: validReceiptWALProviderConfig(filepath.Join(t.TempDir(), "selection.wal"), ReceiptWALModeFresh),
		},
		{
			name:            "graph selection with durable runtime",
			runtimeConfig:   validReceiptWALProviderConfig(filepath.Join(t.TempDir(), "runtime.wal"), ReceiptWALModeFresh),
			selectionConfig: ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runtime, primary, certified := certifiedSnapshotInstallerRuntime(t, tc.runtimeConfig)
			if selection, err := NewSnapshotInstallerSelection(
				tc.selectionConfig,
				GraphSnapshotSpoolConfig{},
				PeerPlaneConfig{},
				CacheConfig{CacheSettings: CacheSettings{TTL: time.Hour}},
				SearchConfig{},
				runtime,
				primary,
				nil,
				certified,
			); selection != nil || err == nil {
				t.Fatalf("mismatched selection = %v, %v", selection, err)
			}
		})
	}
}

func TestSnapshotInstallerSelectionRejectsForeignCertifiedIdentity(t *testing.T) {
	config := ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly}
	runtime, primary, certified := certifiedSnapshotInstallerRuntime(t, config)
	foreignPrimary := runtime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	if selection, err := NewSnapshotInstallerSelection(
		config,
		GraphSnapshotSpoolConfig{},
		PeerPlaneConfig{},
		CacheConfig{CacheSettings: CacheSettings{TTL: time.Hour}},
		SearchConfig{},
		runtime,
		foreignPrimary,
		nil,
		certified,
	); selection != nil || err == nil {
		t.Fatalf("foreign primary selection = %v, %v", selection, err)
	}

	foreignRuntime, _, _ := certifiedSnapshotInstallerRuntime(t, config)
	if selection, err := NewSnapshotInstallerSelection(
		config,
		GraphSnapshotSpoolConfig{},
		PeerPlaneConfig{},
		CacheConfig{CacheSettings: CacheSettings{TTL: time.Hour}},
		SearchConfig{},
		foreignRuntime,
		primary,
		nil,
		certified,
	); selection != nil || err == nil {
		t.Fatalf("foreign runtime selection = %v, %v", selection, err)
	}
}

func TestSnapshotInstallerSelectionRequiresConfiguredGraphHASpool(t *testing.T) {
	config := ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly}
	runtime, primary, certified := certifiedSnapshotInstallerRuntime(t, config)
	peers := PeerPlaneConfig{ListenAddress: "127.0.0.1:6391"}
	if selected, err := NewSnapshotInstallerSelection(config, GraphSnapshotSpoolConfig{}, peers,
		CacheConfig{CacheSettings: CacheSettings{TTL: time.Hour}}, SearchConfig{}, runtime, primary, nil, certified); err == nil || selected != nil {
		t.Fatal("graph-only HA constructed an installer without explicit spool capacity")
	}
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	spool := GraphSnapshotSpoolConfig{Directory: directory, MaxDiskBytes: 128 << 20, MaxFrameBytes: 32 << 20}
	selected, err := NewSnapshotInstallerSelection(config, spool, peers,
		CacheConfig{CacheSettings: CacheSettings{TTL: time.Hour}}, SearchConfig{}, runtime, primary, nil, certified)
	if err != nil || selected == nil || selected.selected() == nil {
		t.Fatalf("explicit private graph Snapshot spool was not admitted: %v", err)
	}
}
