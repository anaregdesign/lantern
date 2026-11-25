package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/tarp"
	"github.com/prometheus/client_golang/prometheus"
)

// TestLoadBackupConfig covers the LANTERN_BACKUP_* resolution. The headline
// invariant (#779/#2011) is that restore-on-start selection is independent of
// production enablement and replication topology. Graph-only restores before peer
// overlay; durable mode applies its stricter fresh/restart policy at the
// runtime construction boundary rather than suppressing restore here.
func TestLoadBackupConfig(t *testing.T) {
	t.Run("DisabledByDefault", func(t *testing.T) {
		cfg := loadBackupConfig(testEnvironmentReader())
		if cfg.Enabled {
			t.Errorf("Enabled = true, want false with no LANTERN_BACKUP_ENABLED")
		}
		if cfg.RestoreOnStart {
			t.Errorf("RestoreOnStart = true, want false without a directory")
		}
	})

	for _, test := range []struct {
		name    string
		input   []string
		restore bool
	}{
		{"ProductionUnsetRestoreDefault", nil, true},
		{"ProductionFalseRestoreDefault", []string{"LANTERN_BACKUP_ENABLED=false"}, true},
		{"ProductionFalseRestoreTrue", []string{"LANTERN_BACKUP_ENABLED=false", "LANTERN_BACKUP_RESTORE_ON_START=true"}, true},
		{"ProductionFalseRestoreFalse", []string{"LANTERN_BACKUP_ENABLED=false", "LANTERN_BACKUP_RESTORE_ON_START=false"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := append([]string{"LANTERN_BACKUP_DIR=" + t.TempDir()}, test.input...)
			config := loadBackupConfig(envconfig.NewReader(input))
			if config.Enabled || config.RestoreOnStart != test.restore {
				t.Fatalf("independent production/restore = %+v", config)
			}
		})
	}

	t.Run("EnabledRequiresDir", func(t *testing.T) {
		t.Setenv("LANTERN_BACKUP_ENABLED", "true")
		// No directory disables both production and startup restoration.
		cfg := loadBackupConfig(testEnvironmentReader())
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
		cfg := loadBackupConfig(testEnvironmentReader())
		if !cfg.Enabled {
			t.Fatalf("Enabled = false, want true")
		}
		if !cfg.RestoreOnStart {
			t.Errorf("RestoreOnStart = false, want true by default")
		}
		if cfg.NamespaceFormat != keyspace.Version {
			t.Errorf("NamespaceFormat = %q, want production format %q", cfg.NamespaceFormat, keyspace.Version)
		}
	})

	t.Run("RestoreOnStartOptOut", func(t *testing.T) {
		t.Setenv("LANTERN_BACKUP_ENABLED", "true")
		t.Setenv("LANTERN_BACKUP_DIR", t.TempDir())
		t.Setenv("LANTERN_BACKUP_RESTORE_ON_START", "false")
		cfg := loadBackupConfig(testEnvironmentReader())
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
		cfg := loadBackupConfig(testEnvironmentReader())
		if !cfg.RestoreOnStart {
			t.Errorf("RestoreOnStart = false, want true regardless of peer config")
		}
	})

	t.Run("InstanceIDDefaultsToHostname", func(t *testing.T) {
		t.Setenv("LANTERN_BACKUP_ENABLED", "true")
		t.Setenv("LANTERN_BACKUP_DIR", t.TempDir())
		cfg := loadBackupConfig(testEnvironmentReader())
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
		cfg := loadBackupConfig(testEnvironmentReader())
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
	t.Run("graph-only namespace mismatch fails before file effects", func(t *testing.T) {
		for _, mismatch := range []string{"configuration", "service"} {
			t.Run(mismatch, func(t *testing.T) {
				receiptConfig := ReceiptWALConfig{Mode: ReceiptWALModeGraphOnly}
				runtime, primary, certified := certifiedSnapshotInstallerRuntime(t, receiptConfig)
				dir := filepath.Join(t.TempDir(), "not-created")
				cfg := backup.Config{Enabled: true, Dir: dir, Interval: time.Hour}
				if mismatch == "configuration" {
					cfg.NamespaceFormat = keyspace.Version
				} else {
					primary.WithDataNamespace()
				}
				got, err := NewBackupper(cfg, receiptConfig, runtime, primary, certified, prometheus.NewRegistry(), nil)
				if got != nil || err == nil || !strings.Contains(err.Error(), "namespace format differs") {
					t.Fatalf("mismatched backupper = %v, %v", got, err)
				}
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatalf("namespace rejection created backup directory: %v", err)
				}
			})
		}
	})

	for _, mode := range []ReceiptWALMode{ReceiptWALModeGraphOnly, ReceiptWALModeFresh} {
		t.Run(string(mode), func(t *testing.T) {
			cfg := ReceiptWALConfig{Mode: mode}
			if mode == ReceiptWALModeFresh {
				cfg = validReceiptWALProviderConfig(mode)
			}
			rt, primary, cert := certifiedSnapshotInstallerRuntime(t, cfg)
			dir := t.TempDir()
			points := backup.Config{Enabled: true, Dir: dir, Interval: time.Hour, Retain: 1, InstanceID: "owner", NamespaceFormat: rt.DataNamespaceFormat(), RestoreOnStart: true}
			owner, err := NewBackupper(points, cfg, rt, primary, cert, prometheus.NewRegistry(), nil)
			if err != nil {
				t.Fatal(err)
			}
			stats, err := owner.BackupNow(t.Context())
			if err != nil || stats.Members != 2 || stats.Bytes <= 0 {
				t.Fatalf("private backup: %+v, %v", stats, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || !entries[0].IsDir() || !strings.HasPrefix(entries[0].Name(), "lantern-tarp-") {
				t.Fatalf("private backup points: %v, %v", entries, err)
			}
			if _, err := tarp.VerifyBackup(t.Context(), filepath.Join(dir, entries[0].Name(), "image")); err != nil {
				t.Fatal(err)
			}
			if mode == ReceiptWALModeFresh {
				if _, err := primary.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "acknowledged-after-backup"}}); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(t.TempDir(), "lost")
				if err := backup.RestoreGraphStoreOnStartup(t.Context(), target, rt.NodeID(), points); err == nil {
					t.Fatal("automatic graph-only discovery accepted a private receipt image")
				}
				cfg.Mode = ReceiptWALModeRestart
				_, cleanup, err := NewServingRuntime(cfg,
					CacheConfig{CacheSettings: CacheSettings{TTL: time.Hour, DataDir: target}},
					SearchConfig{}, MutationLogConfig{Capacity: 8, SubscriberBuffer: 2},
					ReplicationConfig{NodeID: rt.NodeID(), nodeIDExplicit: true}, points, nil)
				if cleanup != nil {
					cleanup()
				}
				if err == nil {
					t.Fatal("receipt restart accepted a discoverable backup older than an acknowledged write")
				}
				if _, err := os.Lstat(target); !os.IsNotExist(err) {
					t.Fatalf("receipt restart populated the missing target: %v", err)
				}
			}
		})
	}
}

func TestNewBackupperRejectsForeignCertification(t *testing.T) {
	receiptConfig := validReceiptWALProviderConfig(ReceiptWALModeFresh)
	runtime, primary, certified := certifiedSnapshotInstallerRuntime(t, receiptConfig)

	foreignConfig := validReceiptWALProviderConfig(ReceiptWALModeFresh)
	foreignRuntime, foreignPrimary, _ := certifiedSnapshotInstallerRuntime(t, foreignConfig)
	cfg := backup.Config{
		Enabled: true, Dir: t.TempDir(), Interval: time.Hour,
		Retain: 1, InstanceID: "receipt-owner",
	}
	for _, tc := range []struct {
		name    string
		runtime *dataruntime.ServingRuntime
		primary *dataruntime.Executor
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
