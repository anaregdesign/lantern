package provider

import (
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/service"
)

// loadBackupConfig reads the LANTERN_BACKUP_* contract (#770, #779).
//
// Restore-on-start is unconditional: the newest valid dump is replayed on
// boot as a baseline, independent of replication topology. When peers exist
// the subsequent peer bootstrap overlays that baseline through the normal
// write path, so HLC ordering lets newer peer state win per key (replica
// priority); when no peer is reachable — a solo instance or a whole-cluster
// cold start — the restored baseline IS the recovered state. This is the
// most-stable arrangement: a restart never comes up with less than its own
// last dump, and never serves an empty graph while it waits for peers.
//
//   - LANTERN_BACKUP_ENABLED          (default false) master switch for the
//     periodic dump loop. Resolved to off when LANTERN_BACKUP_DIR is empty.
//   - LANTERN_BACKUP_DIR              mounted directory to write/read dumps.
//   - LANTERN_BACKUP_INTERVAL         (default 5m) dump cadence.
//   - LANTERN_BACKUP_RETAIN           (default 3) keep newest N own dumps;
//     0 keeps all.
//   - LANTERN_BACKUP_INSTANCE_ID      (default hostname) per-instance file
//     token so shared-storage writes never collide.
//   - LANTERN_BACKUP_RESTORE_ON_START (default true) replay the newest dump
//     on boot, before serving. Set false to skip restore (pure peer
//     bootstrap / start empty).
//   - LANTERN_BACKUP_RESTORE_REQUIRED (default false) fail boot when a
//     restore errors instead of warning and continuing.
func loadBackupConfig() backup.Config {
	enabled := envconfig.Bool("LANTERN_BACKUP_ENABLED", false)
	dir := strings.TrimSpace(envconfig.String("LANTERN_BACKUP_DIR", ""))
	active := enabled && dir != ""

	instance := strings.TrimSpace(envconfig.String("LANTERN_BACKUP_INSTANCE_ID", ""))
	if instance == "" {
		if h, err := os.Hostname(); err == nil && strings.TrimSpace(h) != "" {
			instance = strings.TrimSpace(h)
		} else {
			instance = "lantern"
		}
	}

	return backup.Config{
		Enabled:         active,
		Dir:             dir,
		Interval:        envconfig.Duration("LANTERN_BACKUP_INTERVAL", 5*time.Minute),
		Retain:          envconfig.Int("LANTERN_BACKUP_RETAIN", 3),
		InstanceID:      sanitizeInstanceID(instance),
		RestoreOnStart:  active && envconfig.Bool("LANTERN_BACKUP_RESTORE_ON_START", true),
		RestoreRequired: envconfig.Bool("LANTERN_BACKUP_RESTORE_REQUIRED", false),
	}
}

// sanitizeInstanceID strips characters that would make an unsafe path
// segment so the instance token is always a clean filename component.
func sanitizeInstanceID(s string) string {
	repl := func(r rune) rune {
		switch r {
		case '/', '\\', ' ', '\t', '\n':
			return '_'
		default:
			return r
		}
	}
	return strings.Map(repl, s)
}

// NewBackupConfig is the wire selector for the resolved backup config.
func NewBackupConfig(c *Config) backup.Config { return c.Backup }

// NewBackupper constructs the snapshot-durability engine, registering its
// metrics on the shared registry. It is always non-nil; a disabled config
// yields a Backupper whose Run / RestoreOnStartup are no-ops.
func NewBackupper(cfg backup.Config, svc *service.LanternService, reg *prometheus.Registry, logger *slog.Logger) *backup.Backupper {
	return backup.New(svc, cfg, reg, logger)
}
