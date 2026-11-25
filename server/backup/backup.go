// Package backup produces and verifies physical Tarp recovery points. Public
// graph exports remain available through the BackupSnapshot RPC.
package backup

import (
	"context"
	"sync"
	"time"

	"log/slog"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
)

// Config is the resolved backup configuration. It lives in this package
// (not provider) so the dependency direction stays provider → backup. The
// provider's loader resolves RestoreOnStart from the LANTERN_BACKUP_* env
// before constructing the Backupper.
type Config struct {
	// NamespaceFormat classifies private graph recovery keys; never inferred from values.
	NamespaceFormat string
	// Enabled gates the periodic dump loop. Resolved to false when
	// LANTERN_BACKUP_DIR is empty even if LANTERN_BACKUP_ENABLED is set.
	Enabled bool
	// Dir is the mounted directory backups are written to. Graph-only restore
	// also reads it.
	Dir string
	// Interval is the backup-production cadence.
	Interval time.Duration
	// Retain caps how many of THIS instance's own valid dumps or sets are kept
	// (newest first). 0 keeps all.
	Retain int
	// InstanceID is the per-instance ownership token hashed into physical recovery point names. Defaults to the hostname.
	InstanceID string
	// RestoreOnStart selects startup restore. Graph-only mode replays the
	// newest verified physical image before runtime construction. A receipt
	// store requires explicit recovery with its witnessed terminal cut.
	RestoreOnStart bool
	// RestoreRequired makes graph-only restore errors and missing durable fresh
	// restore evidence fail boot. An incomplete durable restart always fails
	// when its narrowly eligible repair cannot complete.
	RestoreRequired bool
}

// Stats reports the counts and bytes recorded by a backup.
type Stats struct {
	Vertices int
	Edges    int
	Receipts int
	Origins  int
	Members  int
	Bytes    int64
}

// Backupper owns periodic/manual/final production.
type Backupper struct {
	svc        *dataruntime.Executor
	graphStore *graphstore.DB

	cfg     Config
	logger  *slog.Logger
	metrics *metrics
	now     func() time.Time
	attempt chan struct{}
	fs      receiptBackupFS

	productionMu      sync.Mutex
	production        ProductionSnapshot
	productionTicker  productionTimer
	productionStopped bool
}

// UsesExecutor checks exact composition identity without running a backup.
func (b *Backupper) UsesExecutor(primary *dataruntime.Executor) bool {
	if b == nil || primary == nil {
		return false
	}
	return b.svc == primary
}

// Run keeps one scheduler resident until cancellation, including while
// production is disabled. Graceful cancellation takes a final best-effort dump
// only when the production policy captured by that attempt is enabled.
func (b *Backupper) Run(ctx context.Context) error {
	ticker, err := b.startProduction()
	if err != nil {
		return err
	}
	defer b.stopProduction()
	policy := b.Production()
	b.logger.Info("backup: periodic dump loop started",
		slog.String("dir", b.cfg.Dir),
		slog.Duration("interval", policy.Config.Interval),
		slog.Int("retain", policy.Config.Retain),
		slog.String("instance", b.cfg.InstanceID))

	for {
		select {
		case <-ctx.Done():
			b.stopProduction()
			// Final best-effort dump on graceful shutdown, bounded so it
			// never blocks the drain budget.
			finalCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if _, err := b.backupOnceWithSource(finalCtx, "shutdown"); err != nil {
				b.logger.Warn("backup: final dump on shutdown failed", slog.Any("err", err))
			}
			cancel()
			return nil
		case <-ticker.C:
			if _, err := b.backupOnceWithSource(ctx, "periodic"); err != nil {
				b.logger.Warn("backup: periodic dump failed", slog.Any("err", err))
			}
		}
	}
}

// BackupNow writes a physical recovery point immediately and returns its
// stats. It is the one-shot the periodic loop runs each tick, exposed so an
// operator- or test-driven backup reuses the same persistence and retention
// path.
func (b *Backupper) BackupNow(ctx context.Context) (Stats, error) {
	return b.backupOnceWithSource(ctx, "manual")
}

func (b *Backupper) backupOnceWithSource(ctx context.Context, source string) (Stats, error) {
	select {
	case <-ctx.Done():
		return Stats{}, ctx.Err()
	case b.attempt <- struct{}{}:
	}
	defer func() { <-b.attempt }()
	policy := b.Production()
	if source != "manual" && (!policy.Config.Enabled || policy.Config.Interval <= 0) {
		return Stats{}, nil
	}
	if b.svc == nil || b.graphStore == nil {
		return Stats{}, ErrProductionUnavailable
	}
	return b.backupGraphStore(ctx, source, policy)
}

func (b *Backupper) failed() {
	if b.metrics != nil {
		b.metrics.failures.Inc()
	}
}
