package provider

import (
	"errors"
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
// In graph-only mode, restore-on-start is unconditional: the newest valid dump
// is replayed on boot as a baseline, independent of replication topology.
// When peers exist the subsequent peer bootstrap overlays that baseline
// through the normal write path, so HLC ordering lets newer peer state win per
// key. Durable receipt mode restores only at NewServingRuntime, under the WAL
// lease and before runtime certification.
//
//   - LANTERN_BACKUP_ENABLED          (default false) master switch for
//     periodic backup production. Resolved to off when LANTERN_BACKUP_DIR is
//     empty.
//   - LANTERN_BACKUP_DIR              mounted directory to write backup files;
//     graph-only restore also reads it.
//   - LANTERN_BACKUP_INTERVAL         (default 5m) backup cadence.
//   - LANTERN_BACKUP_RETAIN           (default 3) keep newest N valid own
//     dumps/sets; 0 keeps all.
//   - LANTERN_BACKUP_INSTANCE_ID      (default hostname) per-instance file
//     token so shared-storage writes never collide.
//   - LANTERN_BACKUP_RESTORE_ON_START (default true) startup restore of the
//     newest graph dump or canonical receipt backup set before serving.
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

	// Read unconditionally (not short-circuited behind `active &&`) so the
	// variable always lands in the envconfig registry — the generated env
	// reference and the unknown-variable sweep depend on every loader
	// registering its keys on every boot (#847).
	restoreOnStart := envconfig.Bool("LANTERN_BACKUP_RESTORE_ON_START", true)

	return backup.Config{
		Enabled:         active,
		Dir:             dir,
		Interval:        envconfig.Duration("LANTERN_BACKUP_INTERVAL", 5*time.Minute),
		Retain:          envconfig.Int("LANTERN_BACKUP_RETAIN", 3),
		InstanceID:      sanitizeInstanceID(instance),
		RestoreOnStart:  active && restoreOnStart,
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

// NewBackupper constructs the snapshot-durability engine from the exact
// certified runtime. Graph-only mode preserves the historical .lbk path;
// durable receipt mode selects the certified same-cut receipt source.
func NewBackupper(
	cfg backup.Config,
	receiptConfig ReceiptWALConfig,
	runtime *service.ServingRuntime,
	svc *service.LanternService,
	certified runtimeCertified,
	reg *prometheus.Registry,
	logger *slog.Logger,
) (*backup.Backupper, error) {
	if runtime == nil || svc == nil || !certified.valid ||
		certified.runtime != runtime || certified.primary != svc ||
		certified.replication == nil {
		return nil, errors.New("backup: requires the exact certified serving runtime")
	}
	switch receiptConfig.Mode {
	case ReceiptWALModeGraphOnly:
		if runtime.DurableReceiptWAL() {
			return nil, errors.New("backup: graph-only mode received a durable serving runtime")
		}
		return backup.New(svc, cfg, reg, logger), nil
	case ReceiptWALModeFresh, ReceiptWALModeRestart:
		if !runtime.DurableReceiptWAL() {
			return nil, errors.New("backup: durable receipt mode received a graph-only serving runtime")
		}
		source, policy, err := runtime.ReceiptWholeStateBackupSource(
			svc,
			certified.replication,
		)
		if err != nil {
			return nil, err
		}
		backupper, err := backup.NewReceipt(svc, source, policy, cfg, reg, logger)
		if err != nil {
			return nil, err
		}
		if err := runtime.CertifyReceiptBackup(svc, certified.replication); err != nil {
			return nil, err
		}
		return backupper, nil
	default:
		return nil, errors.New("backup: invalid receipt WAL mode")
	}
}
