package provider

import (
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

// loadBackupConfig reads the LANTERN_BACKUP_* contract (#770, #779).
//
// The runtime restores a missing node directory before opening the DB. The
// scheduler owns periodic native backups independently of this startup policy.
//
//   - LANTERN_BACKUP_ENABLED          (default false) enable periodic production
//     when a destination is configured.
//   - LANTERN_BACKUP_DIR              directory for private recovery points.
//   - LANTERN_BACKUP_INTERVAL         (default 5m) backup cadence.
//   - LANTERN_BACKUP_RETAIN           (default 3) keep newest N valid own
//     dumps/sets; 0 keeps all.
//   - LANTERN_BACKUP_INSTANCE_ID      (default hostname) per-instance file
//     token so shared-storage writes never collide.
//   - LANTERN_BACKUP_RESTORE_ON_START (default true) select the newest own
//     recovery point on startup when the data directory is missing.
//   - LANTERN_BACKUP_RESTORE_REQUIRED (default false) require a backup if
//     an existing database is unavailable during startup restore.
func loadBackupConfig(reader *envconfig.Reader) backup.Config {
	enabled := reader.Bool("LANTERN_BACKUP_ENABLED", false)
	dir := strings.TrimSpace(reader.String("LANTERN_BACKUP_DIR", ""))
	active := enabled && dir != ""

	instance := strings.TrimSpace(reader.String("LANTERN_BACKUP_INSTANCE_ID", ""))
	if instance == "" {
		if h, err := os.Hostname(); err == nil && strings.TrimSpace(h) != "" {
			instance = strings.TrimSpace(h)
		} else {
			instance = "lantern"
		}
	}

	// Read unconditionally (not short-circuited behind directory selection) so the
	// variable always lands in the envconfig registry — the generated env
	// reference and the unknown-variable sweep depend on every loader
	// registering its keys on every boot (#847).
	restoreOnStart := reader.Bool("LANTERN_BACKUP_RESTORE_ON_START", true)

	return backup.Config{
		Enabled:         active,
		Dir:             dir,
		Interval:        reader.Duration("LANTERN_BACKUP_INTERVAL", 5*time.Minute),
		Retain:          reader.Int("LANTERN_BACKUP_RETAIN", 3),
		InstanceID:      sanitizeInstanceID(instance),
		RestoreOnStart:  dir != "" && restoreOnStart,
		RestoreRequired: reader.Bool("LANTERN_BACKUP_RESTORE_REQUIRED", false),
		NamespaceFormat: keyspace.Version,
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

// NewBackupper selects the exact certified node DB used by the serving
// executor and replication owner. The associated receipt capability remains
// independently gated by receipt configuration and public authorization.
func NewBackupper(
	cfg backup.Config,
	receiptConfig ReceiptConfig,
	runtime *dataruntime.ServingRuntime,
	svc *dataruntime.Executor,
	certified runtimeCertified,
	reg *prometheus.Registry,
	logger *slog.Logger,
) (*backup.Backupper, error) {
	if runtime == nil || svc == nil || !certified.valid ||
		certified.runtime != runtime || certified.primary != svc ||
		certified.replication == nil {
		return nil, errors.New("backup: requires the exact certified serving runtime")
	}
	db := runtime.GraphStore()
	if db == nil {
		return nil, errors.New("backup: requires the runtime-owned graph store")
	}
	if cfg.NamespaceFormat != runtime.DataNamespaceFormat() || cfg.NamespaceFormat != svc.DataNamespaceFormat() {
		return nil, errors.New("backup: graph storage namespace format differs from certified runtime")
	}
	switch receiptConfig.Mode {
	case ReceiptModeGraphOnly:
		if runtime.ReceiptsEnabled() {
			return nil, errors.New("backup: graph-only mode received a receipt runtime")
		}
	case ReceiptModeFresh, ReceiptModeRestart:
		if !runtime.ReceiptsEnabled() {
			return nil, errors.New("backup: receipt mode requires an installed receipt runtime")
		}
		if err := runtime.CertifyReceiptBackup(svc, certified.replication); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("backup: invalid receipt mode")
	}
	return backup.NewGraphStore(svc, db, cfg, reg, logger), nil
}
