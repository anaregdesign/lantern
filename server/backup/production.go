package backup

import (
	"errors"
	"strings"
	"time"
)

// ProductionConfig is the atomic, operational part of backup configuration.
// Destination, instance identity and startup restoration remain bootstrap-owned.
type ProductionConfig struct {
	Enabled  bool
	Interval time.Duration
	Retain   int
}

// ProductionSnapshot identifies one immutable effective production policy.
// Generation zero denotes the externally supplied bootstrap configuration.
type ProductionSnapshot struct {
	Generation uint64
	Config     ProductionConfig
}

var (
	ErrProductionConflict = errors.New("backup: production generation conflicts")
	ErrProductionStopped  = errors.New("backup: production owner has stopped")
)

type productionTimer interface {
	Reset(time.Duration)
	Stop()
}

// Validate rejects invalid managed policies without interpreting environment
// variables. Durations retain their exact nanosecond representation.
func (c ProductionConfig) Validate() error {
	if c.Interval <= 0 {
		return errors.New("backup: production interval must be positive")
	}
	if c.Retain < 0 {
		return errors.New("backup: production retain must be nonnegative")
	}
	return nil
}

// RetentionAcknowledgementRequired reports changes whose future successful
// backups may delete previously retained recovery points. The native authority
// binds the acknowledgement to its reviewed intent; this owner stores no grant.
func RetentionAcknowledgementRequired(current ProductionSnapshot, next ProductionConfig) bool {
	return next.Retain > 0 && (current.Generation == 0 || current.Config.Retain == 0 || next.Retain < current.Config.Retain)
}

// Production returns a value copy of the currently effective policy.
func (b *Backupper) Production() ProductionSnapshot {
	b.productionMu.Lock()
	defer b.productionMu.Unlock()
	return b.production
}

// ValidateProduction checks the immutable local prerequisites without applying
// a policy or inspecting/deleting backup files.
func (b *Backupper) ValidateProduction(config ProductionConfig) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if config.Enabled && strings.TrimSpace(b.cfg.Dir) == "" {
		return errors.New("backup: production requires an externally configured destination")
	}
	return nil
}

// ApplyProduction installs a verified committed policy. The caller must perform
// authority/CAS and any required retention acknowledgement before this trusted
// local seam. Apply never writes, creates, prunes or restores backup files.
func (b *Backupper) ApplyProduction(next ProductionSnapshot) error {
	if err := b.ValidateProduction(next.Config); err != nil {
		return err
	}
	b.productionMu.Lock()
	defer b.productionMu.Unlock()
	if b.productionStopped {
		return ErrProductionStopped
	}
	if next.Generation == 0 || next.Generation < b.production.Generation ||
		(next.Generation == b.production.Generation && next != b.production) {
		return ErrProductionConflict
	}
	if next == b.production {
		return nil
	}
	if b.productionTicker != nil && (next.Config.Enabled != b.production.Config.Enabled || next.Config.Interval != b.production.Config.Interval) {
		if next.Config.Enabled {
			b.productionTicker.Reset(next.Config.Interval)
		} else {
			b.productionTicker.Stop()
		}
	}
	b.production = next
	return nil
}

func (b *Backupper) startProduction() (*time.Ticker, error) {
	b.productionMu.Lock()
	defer b.productionMu.Unlock()
	if b.productionStopped {
		return nil, ErrProductionStopped
	}
	if b.productionTicker != nil {
		return nil, errors.New("backup: production owner is already running")
	}
	interval := b.production.Config.Interval
	if interval <= 0 {
		// Preserve the legacy disabled/invalid-interval bootstrap behavior,
		// while keeping the owner available for a valid managed policy.
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	if !b.production.Config.Enabled || b.production.Config.Interval <= 0 {
		ticker.Stop()
	}
	b.productionTicker = ticker
	return ticker, nil
}

func (b *Backupper) stopProduction() {
	b.productionMu.Lock()
	defer b.productionMu.Unlock()
	b.productionStopped = true
	if b.productionTicker != nil {
		b.productionTicker.Stop()
	}
}
