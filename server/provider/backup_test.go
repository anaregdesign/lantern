package provider

import (
	"testing"
	"time"
)

// TestLoadBackupConfig covers the LANTERN_BACKUP_* resolution. The headline
// invariant (#779) is that restore-on-start is UNCONDITIONAL — a baseline
// independent of replication topology — so a restart never comes up empty
// while it waits for peers, and a whole-cluster cold start recovers from the
// dump instead of staying empty. Replica priority is achieved later, at
// bootstrap time, via HLC ordering — not by suppressing restore here.
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
