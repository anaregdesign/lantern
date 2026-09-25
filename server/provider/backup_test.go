package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/prometheus/client_golang/prometheus"
)

// TestLoadBackupConfig covers the LANTERN_BACKUP_* resolution. The headline
// invariant (#779) is that restore-on-start selection is independent of
// replication topology. Graph-only mode restores its baseline before peer
// overlay; durable mode applies its stricter fresh/restart policy at the
// runtime construction boundary rather than suppressing restore here.
func TestLoadBackupConfig(t *testing.T) {
	t.Run("DisabledByDefault", func(t *testing.T) {
		cfg := loadBackupConfig()
		if cfg.Enabled {
			t.Errorf("Enabled = true, want false with no LANTERN_BACKUP_ENABLED")
		}
		if cfg.RestoreOnStart {
			t.Errorf("RestoreOnStart = true, want false when inactive")
		}
	})

	t.Run("EnabledRequiresDir", func(t *testing.T) {
		t.Setenv("LANTERN_BACKUP_ENABLED", "true")
		// No LANTERN_BACKUP_DIR ⇒ not active ⇒ restore off.
		cfg := loadBackupConfig()
		if cfg.Enabled {
			t.Errorf("Enabled = true, want false without LANTERN_BACKUP_DIR")
		}
		if cfg.RestoreOnStart {
			t.Errorf("RestoreOnStart = true, want false without a dir")
		}
	})

	t.Run("ActiveRestoresByDefault", func(t *testing.T) {
		t.Setenv("LANTERN_BACKUP_ENABLED", "true")
		t.Setenv("LANTERN_BACKUP_DIR", t.TempDir())
		cfg := loadBackupConfig()
		if !cfg.Enabled {
			t.Fatalf("Enabled = false, want true")
		}
		if !cfg.RestoreOnStart {
			t.Errorf("RestoreOnStart = false, want true by default")
		}
	})

	t.Run("RestoreOnStartOptOut", func(t *testing.T) {
		t.Setenv("LANTERN_BACKUP_ENABLED", "true")
		t.Setenv("LANTERN_BACKUP_DIR", t.TempDir())
		t.Setenv("LANTERN_BACKUP_RESTORE_ON_START", "false")
		cfg := loadBackupConfig()
		if cfg.RestoreOnStart {
			t.Errorf("RestoreOnStart = true, want false when opted out")
		}
	})

	t.Run("RestoreIndependentOfPeers", func(t *testing.T) {
		// Regression for #779: neither a static peer list nor DNS discovery
		// may suppress restore. Restore is the baseline; peers overlay it via
		// HLC at bootstrap. Before the fix a static list suppressed restore
		// while DNS discovery slipped through ungated — both are wrong.
		t.Setenv("LANTERN_BACKUP_ENABLED", "true")
		t.Setenv("LANTERN_BACKUP_DIR", t.TempDir())
		t.Setenv("LANTERN_PEERS", "peer-a:6380,peer-b:6380")
		t.Setenv("LANTERN_PEER_DISCOVERY", "dns")
		t.Setenv("LANTERN_PEER_DNS_NAME", "lantern")
		cfg := loadBackupConfig()
		if !cfg.RestoreOnStart {
			t.Errorf("RestoreOnStart = false, want true regardless of peer config")
		}
	})

	t.Run("InstanceIDDefaultsToHostname", func(t *testing.T) {
		t.Setenv("LANTERN_BACKUP_ENABLED", "true")
		t.Setenv("LANTERN_BACKUP_DIR", t.TempDir())
		cfg := loadBackupConfig()
		if cfg.InstanceID == "" {
			t.Errorf("InstanceID empty, want a hostname fallback")
		}
	})

	t.Run("Overrides", func(t *testing.T) {
		t.Setenv("LANTERN_BACKUP_ENABLED", "true")
		t.Setenv("LANTERN_BACKUP_DIR", t.TempDir())
		t.Setenv("LANTERN_BACKUP_INTERVAL", "90s")
		t.Setenv("LANTERN_BACKUP_RETAIN", "7")
		t.Setenv("LANTERN_BACKUP_INSTANCE_ID", "node-x")
		cfg := loadBackupConfig()
		if cfg.Interval != 90*time.Second {
			t.Errorf("Interval = %v, want 90s", cfg.Interval)
		}
		if cfg.Retain != 7 {
			t.Errorf("Retain = %d, want 7", cfg.Retain)
		}
		if cfg.InstanceID != "node-x" {
			t.Errorf("InstanceID = %q, want node-x", cfg.InstanceID)
		}
	})
}

func TestNewBackupperSelectsCertifiedRuntimeMode(t *testing.T) {
	t.Run("graph-only behavior remains lbk", func(t *testing.T) {
		receiptConfig := ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly}
		runtime, primary, certified := certifiedSnapshotInstallerRuntime(t, receiptConfig)
		dir := t.TempDir()
		backupper, err := NewBackupper(
			backup.Config{
				Enabled: true, Dir: dir, Interval: time.Hour,
				Retain: 1, InstanceID: "graph-owner", RestoreOnStart: true,
			},
			receiptConfig,
			runtime,
			primary,
			certified,
			prometheus.NewRegistry(),
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		stats, err := backupper.BackupNow(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if stats.Members != 0 || stats.Receipts != 0 || stats.Origins != 0 {
			t.Fatalf("graph-only backup emitted receipt-set stats: %+v", stats)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".lbk") {
			t.Fatalf("graph-only backup files = %+v, want one .lbk", entries)
		}
	})

	t.Run("durable mode produces a committed set", func(t *testing.T) {
		receiptConfig := validReceiptWALProviderConfig(
			filepath.Join(t.TempDir(), "receipts.wal"),
			ReceiptWALModeFresh,
		)
		runtime, primary, certified := certifiedSnapshotInstallerRuntime(t, receiptConfig)
		dir := t.TempDir()
		backupper, err := NewBackupper(
			backup.Config{
				Enabled: true, Dir: dir, Interval: time.Hour,
				Retain: 1, InstanceID: "receipt-owner",
			},
			receiptConfig,
			runtime,
			primary,
			certified,
			prometheus.NewRegistry(),
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		stats, err := backupper.BackupNow(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if stats.Members != 3 || stats.Bytes <= 0 {
			t.Fatalf("durable receipt backup stats = %+v", stats)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var manifests int
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".set.json") {
				manifests++
			}
			if strings.HasSuffix(entry.Name(), ".lbk") || strings.HasSuffix(entry.Name(), ".tmp") {
				t.Fatalf("durable receipt backup emitted legacy or temporary file %q", entry.Name())
			}
		}
		if len(entries) != 4 || manifests != 1 {
			t.Fatalf("durable receipt backup files = %+v, want three members and one manifest", entries)
		}
	})
}

func TestNewBackupperRejectsForeignCertification(t *testing.T) {
	receiptConfig := validReceiptWALProviderConfig(
		filepath.Join(t.TempDir(), "receipts-a.wal"),
		ReceiptWALModeFresh,
	)
	runtime, primary, certified := certifiedSnapshotInstallerRuntime(t, receiptConfig)

	foreignConfig := validReceiptWALProviderConfig(
		filepath.Join(t.TempDir(), "receipts-b.wal"),
		ReceiptWALModeFresh,
	)
	foreignRuntime, foreignPrimary, _ := certifiedSnapshotInstallerRuntime(t, foreignConfig)
	cfg := backup.Config{
		Enabled: true, Dir: t.TempDir(), Interval: time.Hour,
		Retain: 1, InstanceID: "receipt-owner",
	}
	for _, tc := range []struct {
		name    string
		runtime *service.ServingRuntime
		primary *service.LanternService
		marker  runtimeCertified
	}{
		{"foreign runtime", foreignRuntime, primary, certified},
		{"foreign primary", runtime, foreignPrimary, certified},
		{"foreign marker", runtime, primary, runtimeCertified{valid: true, runtime: foreignRuntime, primary: foreignPrimary}},
		{"uncertified", runtime, primary, runtimeCertified{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := NewBackupper(
				cfg,
				receiptConfig,
				tc.runtime,
				tc.primary,
				tc.marker,
				prometheus.NewRegistry(),
				nil,
			); got != nil || err == nil {
				t.Fatalf("foreign backupper = %v, %v", got, err)
			}
		})
	}
}
