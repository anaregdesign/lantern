package provider

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/service"
)

func certifiedSnapshotInstallerRuntime(
	t *testing.T,
	config ReceiptWALConfig,
) (*service.ServingRuntime, *service.LanternService, runtimeCertified) {
	t.Helper()
	replicationConfig := ReplicationConfig{
		NodeID: hlc.NodeID{0x61}, nodeIDExplicit: config.Mode != ReceiptWALModeGraphOnly,
	}
	runtime, cleanup, err := NewServingRuntime(
		config,
		CacheConfig{TTL: time.Hour},
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
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	replicationService, err := runtime.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	certified, err := NewRuntimeCertified(runtime, primary, replicationService)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, primary, certified
}

func TestSnapshotInstallerSelectionMatchesRuntimeAndIsShared(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		name    string
		config  func(string) ReceiptWALConfig
		durable bool
	}{
		{
			name: "graph only",
			config: func(string) ReceiptWALConfig {
				return ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly}
			},
		},
		{
			name: "durable",
			config: func(path string) ReceiptWALConfig {
				return validReceiptWALProviderConfig(path, ReceiptWALModeFresh)
			},
			durable: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := tc.config(filepath.Join(t.TempDir(), "receipts.wal"))
			runtime, primary, certified := certifiedSnapshotInstallerRuntime(t, config)
			selection, err := NewSnapshotInstallerSelection(
				config,
				CacheConfig{TTL: time.Hour},
				SearchConfig{},
				runtime,
				primary,
				logger,
				certified,
			)
			if err != nil {
				t.Fatal(err)
			}
			if (selection.selected() != nil) != tc.durable {
				t.Fatalf("selected installer = %T, want durable %t", selection.selected(), tc.durable)
			}
			if tc.durable {
				if got := selection.selected().RequiredFormat(); got != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V2 {
					t.Fatalf("required format = %v", got)
				}
			}

			pumpConfig := newReplicationPumpConfig(
				PeerConfig{}, nil, ReplicationConfig{}, AuthConfig{}, primary, nil, logger, selection,
			)
			antiEntropyConfig := newAntiEntropyReplicationConfig(
				PeerConfig{}, nil, ReplicationConfig{}, AntiEntropyConfig{}, AuthConfig{},
				primary, nil, logger, selection,
			)
			if pumpConfig.SnapshotInstaller != selection.selected() ||
				antiEntropyConfig.SnapshotInstaller != selection.selected() ||
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
				CacheConfig{TTL: time.Hour},
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
	foreignPrimary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	if selection, err := NewSnapshotInstallerSelection(
		config,
		CacheConfig{TTL: time.Hour},
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
		CacheConfig{TTL: time.Hour},
		SearchConfig{},
		foreignRuntime,
		primary,
		nil,
		certified,
	); selection != nil || err == nil {
		t.Fatalf("foreign runtime selection = %v, %v", selection, err)
	}
}
