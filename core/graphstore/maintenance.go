package graphstore

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"time"

	"github.com/anaregdesign/tarp"
)

type layoutSample struct {
	generation, revision, records, liveBytes, liveKeys uint64
}

type layoutTask struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// The caller holds db.mu so the graph generation and engine counters agree.
func (db *DB) layoutSample() layoutSample {
	stats := db.raw.Stats()
	return layoutSample{db.head.active, db.head.revision, stats.WrittenRecords, stats.LiveRecordBytes, stats.LiveKeys}
}

type layoutSchedule struct {
	base        layoutSample
	lastRecords uint64
	quietSince  time.Time
	retryAfter  time.Time
	quiet       time.Duration
	minBytes    uint64
}

func newLayoutSchedule(initial layoutSample, now time.Time, quiet time.Duration, minBytes uint64) layoutSchedule {
	return layoutSchedule{base: initial, lastRecords: initial.records, quietSince: now, quiet: quiet, minBytes: minBytes}
}

func (s *layoutSchedule) due(now time.Time, cut layoutSample) bool {
	if cut.records != s.lastRecords {
		s.lastRecords, s.quietSince = cut.records, now
		return false
	}
	if now.Sub(s.quietSince) < s.quiet || now.Before(s.retryAfter) ||
		cut.revision == s.base.revision || cut.liveBytes < s.minBytes {
		return false
	}
	// Target a published replacement, at least one segment of live growth,
	// or enough new records to touch a quarter of the current index.
	return cut.generation != s.base.generation ||
		cut.liveBytes >= s.base.liveBytes && cut.liveBytes-s.base.liveBytes >= s.minBytes ||
		cut.records-s.base.records >= max(1, (cut.liveKeys+3)/4)
}

func (db *DB) maintainKeyOrder(ctx context.Context, now time.Time, schedule *layoutSchedule) {
	if !db.mu.TryLock() {
		// A pending acquisition, including a large foreground write, is activity.
		schedule.quietSince = now
		return
	}
	cut := db.layoutSample()
	db.mu.Unlock()
	if !schedule.due(now, cut) {
		return
	}
	start := time.Now()
	result, err := db.compactByKey(ctx, &cut)
	elapsed := time.Since(start)
	switch {
	case err == nil:
		// Count any records written during the checkpoint toward the next one.
		schedule.base = cut
		slog.Info("graphstore key-ordered compaction completed", "generation", cut.generation,
			"duration", elapsed, "unlinked_bytes", result.UnlinkedBytes,
			"pinned_retired_bytes", result.PinnedRetiredDataBytes)
	case errors.Is(err, tarp.ErrBusy), errors.Is(err, context.Canceled), errors.Is(err, tarp.ErrClosed):
		schedule.quietSince = now.Add(elapsed)
	default:
		schedule.retryAfter = now.Add(elapsed + 10*time.Minute)
		slog.Warn("graphstore key-ordered compaction deferred", "error", err)
	}
}

// CompactByKey reclaims abandoned generations and asks Tarp to cluster live
// records by their encoded keys. It runs synchronously regardless of database
// size. A pinned graph view, staging operation, or competing checkpoint can
// return tarp.ErrBusy. Backup, generation replacement and Close can interrupt
// the request. Tarp permits subsequent commits while the checkpoint runs.
func (db *DB) CompactByKey(ctx context.Context) (tarp.CompactResult, error) {
	return db.compactByKey(ctx, nil)
}

func (db *DB) compactByKey(ctx context.Context, observed *layoutSample) (tarp.CompactResult, error) {
	db.mu.Lock()
	if err := db.available(); err != nil {
		db.mu.Unlock()
		return tarp.CompactResult{}, err
	}
	if db.ordering != nil || db.staging != nil {
		db.mu.Unlock()
		return tarp.CompactResult{}, tarp.ErrBusy
	}
	ctx, cancel := context.WithCancel(ctx)
	task := &layoutTask{cancel: cancel, done: make(chan struct{})}
	db.ordering = task
	db.mu.Unlock()
	defer func() {
		cancel()
		db.mu.Lock()
		db.ordering = nil
		close(task.done)
		db.mu.Unlock()
	}()
	if observed == nil {
		if err := db.Reclaim(ctx); err != nil {
			return tarp.CompactResult{}, err
		}
	}
	db.mu.Lock()
	err := db.checkLayoutReady(ctx, observed)
	db.mu.Unlock()
	if err != nil {
		return tarp.CompactResult{}, err
	}
	result, err := db.raw.Compact(ctx, tarp.WithKeyOrder())
	var op *tarp.OpError
	if errors.Is(err, tarp.ErrUnhealthy) || errors.As(err, &op) && op.Outcome == tarp.Unknown {
		db.observeWrite(err)
	} else if op != nil && op.Outcome == tarp.Committed {
		// A cleanup error can follow a usable checkpoint. An unsafe error
		// after the manifest switch can have the same Committed outcome.
		view, healthErr := db.raw.Snapshot()
		if healthErr == nil {
			healthErr = view.Close()
		}
		err = errors.Join(err, db.observeWrite(healthErr))
	}
	return result, err
}

// The caller holds db.mu. Newly admitted short read views remain safe under
// Tarp's native pins. Long-running backups and generation installs interrupt
// the checkpoint when they acquire the graph writer gate.
func (db *DB) checkLayoutReady(ctx context.Context, observed *layoutSample) error {
	if err := db.available(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if observed != nil && db.raw.Stats().WrittenRecords != observed.records {
		return tarp.ErrBusy
	}
	db.pinMu.Lock()
	pinned := len(db.pins) != 0
	db.pinMu.Unlock()
	if pinned {
		return tarp.ErrBusy
	}
	active := generationMarker(db.head.active)
	marker, err := db.raw.Get(active)
	if errors.Is(err, tarp.ErrNotFound) || err == nil && len(marker) != 0 {
		return ErrInvalidManifest
	}
	if err != nil {
		return err
	}
	for key, err := range db.raw.KeysPrefix(ctx, []byte{'M'}) {
		if err != nil {
			return err
		}
		if len(key) != 9 || binary.BigEndian.Uint64(key[1:]) == 0 || binary.BigEndian.Uint64(key[1:]) > db.head.allocated {
			return ErrInvalidManifest
		}
		if !bytes.Equal(key, active) {
			return tarp.ErrBusy
		}
	}
	return ctx.Err()
}

// The caller holds db.mu. The compacting goroutine clears the slot on exit.
func (db *DB) interruptKeyOrder() {
	if db.ordering != nil {
		db.ordering.cancel()
	}
}
