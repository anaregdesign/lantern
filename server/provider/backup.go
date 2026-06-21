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

// loadBackupConfig reads the LANTERN_BACKUP_* contract and resolves the
// restore-on-startup decision against peer mode (#770).
//
//   - LANTERN_BACKUP_ENABLED          (default false) master switch for the
//     periodic dump loop. Resolved to off when LANTERN_BACKUP_DIR is empty.
//   - LANTERN_BACKUP_DIR              mounted directory to write/read dumps.
//   - LANTERN_BACKUP_INTERVAL         (default 5m) dump cadence.
//   - LANTERN_BACKUP_RETAIN           (default 3) keep newest N own dumps;
//     0 keeps all.
//   - LANTERN_BACKUP_INSTANCE_ID      (default hostname) per-instance file
//     token so shared-storage writes never collide.
//   - LANTERN_BACKUP_RESTORE_ON_START (default true) restore the newest
//     dump on boot. Restore is single-instance-gated: in multi-peer mode it
//     is skipped (peer bootstrap is the recovery path) unless
//     LANTERN_BACKUP_RESTORE_FORCE=true.
//   - LANTERN_BACKUP_RESTORE_FORCE    (default false) restore even when
//     peers are configured.
//   - LANTERN_BACKUP_RESTORE_REQUIRED (default false) fail boot when a
//     restore errors instead of warning and continuing.
func loadBackupConfig(hasPeers bool) backup.Config {
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

	restoreOnStart := active &&
		envconfig.Bool("LANTERN_BACKUP_RESTORE_ON_START", true) &&
		(!hasPeers || envconfig.Bool("LANTERN_BACKUP_RESTORE_FORCE", false))

	return backup.Config{
		Enabled:         active,
		Dir:             dir,
		Interval:        envconfig.Duration("LANTERN_BACKUP_INTERVAL", 5*time.Minute),
		Retain:          envconfig.Int("LANTERN_BACKUP_RETAIN", 3),
		InstanceID:      sanitizeInstanceID(instance),
		RestoreOnStart:  restoreOnStart,
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
