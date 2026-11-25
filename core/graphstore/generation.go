package graphstore

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/anaregdesign/tarp"
)

// Writer queues mixed mutations. A *tarp.WriteBatch implements Writer.
type Writer interface {
	Put(key, value []byte)
	Delete(key []byte)
}

var (
	// ErrGenerationChanged indicates a foreground commit since staging began.
	ErrGenerationChanged = errors.New("graphstore: active state changed during generation install")
	// ErrGenerationBusy means another install owns this DB's staging slot.
	ErrGenerationBusy = errors.New("graphstore: generation install in progress")
	// ErrGenerationClosed means the staging handle was published or aborted.
	ErrGenerationClosed = errors.New("graphstore: generation install is closed")
)

// Generation owns an unpublished replacement image. Writes to the active graph
// can continue while it is assembled. A stale image cannot replace newer state.
type Generation struct {
	db               *DB
	id, baseRevision uint64
}

// ID returns the physical generation assigned to this image.
func (g *Generation) ID() uint64 { return g.id }

// ServicePrefix returns the ordered prefix reserved for the caller's private
// service records. Append a family byte and its key components before storing.
func ServicePrefix(generation uint64) []byte {
	return binary.BigEndian.AppendUint64([]byte{'S'}, generation)
}

func generationMarker(id uint64) []byte {
	return binary.BigEndian.AppendUint64([]byte{'M'}, id)
}

// BeginGeneration durably reserves a fresh key range. A crash leaves an
// unreferenced range for Reclaim; its identifier will never be reused.
func (db *DB) BeginGeneration() (*Generation, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.available(); err != nil {
		return nil, err
	}
	if db.staging != nil {
		return nil, ErrGenerationBusy
	}
	if db.head.allocated == math.MaxUint64 {
		return nil, errors.New("graphstore: exhausted generation IDs")
	}
	db.interruptKeyOrder()
	next := db.head
	next.allocated++
	var batch tarp.WriteBatch
	batch.Put(generationMarker(next.allocated), nil)
	if err := db.commit(next, &batch); err != nil {
		return nil, err
	}
	g := &Generation{db: db, id: next.allocated, baseRevision: next.revision}
	db.staging = g
	return g, nil
}

type generationWriter struct {
	id    uint64
	batch tarp.WriteBatch
	bytes uint64
	err   error
}

func (w *generationWriter) accepts(key, value []byte) bool {
	if w.err != nil {
		return false
	}
	if len(key) < 10 || (key[0] != 'G' && key[0] != 'S') || binary.BigEndian.Uint64(key[1:9]) != w.id {
		w.err = errors.New("graphstore: staged key outside its generation")
		return false
	}
	if w.batch.Len() >= tarp.MaxWriteBatchEntries {
		w.err = fmt.Errorf("graphstore: too many operations in generation chunk: %w", tarp.ErrTooLarge)
		return false
	}
	cost := uint64(len(key)) + uint64(len(value)) + 48
	if cost > uint64(storageOptions().MaxRecordBytes) || cost > uint64(storageOptions().TargetSegmentBytes)-64-w.bytes {
		w.err = fmt.Errorf("graphstore: oversized generation chunk: %w", tarp.ErrTooLarge)
		return false
	}
	w.bytes += cost
	return true
}
func (w *generationWriter) Put(key, value []byte) {
	if w.accepts(key, value) {
		w.batch.Put(key, value)
	}
}
func (w *generationWriter) Delete(key []byte) {
	if w.accepts(key, nil) {
		w.batch.Delete(key)
	}
}

// Apply durably writes one bounded chunk into the hidden generation. The
// caller owns framing/validation and may start a new chunk on a definite
// size error. No iteration order is implied across concurrent callers.
func (g *Generation) Apply(prepare func(uint64, Writer) error) error {
	db := g.db
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.available(); err != nil {
		return err
	}
	if db.staging != g {
		return ErrGenerationClosed
	}
	writer := generationWriter{id: g.id}
	if err := prepare(g.id, &writer); err != nil {
		return err
	}
	if writer.err != nil {
		return writer.err
	}
	return db.apply(&writer.batch)
}

// Publish verifies the immutable active and staged state under the writer
// gate, then atomically switches the head. Supply validation for application
// evidence such as the origin vector, footer, cut, and local admission.
func (g *Generation) Publish(ctx context.Context, validate func(active, staged uint64, snap *tarp.Snapshot) error) error {
	db := g.db
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.available(); err != nil {
		return err
	}
	if db.staging != g {
		return ErrGenerationClosed
	}
	if db.head.revision != g.baseRevision {
		return ErrGenerationChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if validate != nil {
		snap, err := db.raw.Snapshot()
		if err != nil {
			return err
		}
		defer snap.Close()
		if err := validate(db.head.active, g.id, snap); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	next := db.head
	if err := next.advance(); err != nil {
		return err
	}
	next.active = g.id
	if err := db.commit(next, &tarp.WriteBatch{}); err != nil {
		return err
	}
	db.staging = nil
	db.requestReclamation()
	return nil
}

// Abort relinquishes this handle. Its hidden rows can be reclaimed.
func (g *Generation) Abort() {
	db := g.db
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.staging == g {
		db.staging = nil
		db.requestReclamation()
	}
}

// Reclaim deletes old and abandoned G/S rows in bounded batches. The current
// generation, an active install, and generations retained by ReadView remain
// intact. Cleanup is retryable following cancellation, disk pressure or crash.
func (db *DB) Reclaim(ctx context.Context) error {
	db.mu.Lock()
	if err := db.available(); err != nil {
		db.mu.Unlock()
		return err
	}
	snap, err := db.raw.Snapshot()
	db.mu.Unlock()
	if err != nil {
		return err
	}
	defer snap.Close()
	lower, upper := tarp.PrefixRange([]byte{'M'})
	for key, err := range snap.KeysRange(ctx, lower, upper) {
		if err != nil {
			return err
		}
		if len(key) != 9 {
			return ErrInvalidManifest
		}
		if err := db.reclaimGeneration(ctx, binary.BigEndian.Uint64(key[1:])); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (db *DB) reclaimGeneration(ctx context.Context, id uint64) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		db.mu.Lock()
		done, err := db.reclaimChunk(ctx, id)
		db.mu.Unlock()
		if done || err != nil {
			return err
		}
	}
}

// Caller holds the writer gate. The two range scans stop after one chunk;
// repeated calls seek directly to the first remaining row.
func (db *DB) reclaimChunk(ctx context.Context, id uint64) (bool, error) {
	if err := db.available(); err != nil {
		return false, err
	}
	if id == 0 || id > db.head.allocated {
		return false, ErrInvalidManifest
	}
	if id == db.head.active || (db.staging != nil && db.staging.id == id) {
		return true, nil
	}
	db.pinMu.Lock()
	pinned := db.pins[id] != 0
	if pinned {
		db.retiredPins[id] = true
	}
	db.pinMu.Unlock()
	if pinned {
		return true, nil
	}
	var batch tarp.WriteBatch
	var total uint64
	for _, family := range []byte{'G', 'S'} {
		prefix := binary.BigEndian.AppendUint64([]byte{family}, id)
		for key, err := range db.raw.KeysPrefix(ctx, prefix) {
			if err != nil {
				return false, err
			}
			if len(key) < 10 || !bytes.HasPrefix(key, prefix) {
				return false, ErrInvalidManifest
			}
			cost := uint64(len(key)) + 48
			if batch.Len() > 0 && (batch.Len() >= 256 || total+cost > 1<<20) {
				return false, db.apply(&batch)
			}
			total += cost
			batch.Delete(key)
		}
	}
	// The marker is last, even when there are no rows.
	batch.Delete(generationMarker(id))
	return true, db.apply(&batch)
}

// StartMaintenance starts generation cleanup and idle key-ordered compaction
// for a serving DB. Inspection and staged restore can open a store without
// scheduling this work.
// Close stops the worker and joins it before releasing directory ownership.
func (db *DB) StartMaintenance() {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed || db.cancelReclaim != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	db.cancelReclaim = cancel
	db.reclaimDone = make(chan struct{})
	sample := db.layoutSample()
	go db.reclaimLoop(ctx, sample)
	if db.head.allocated > 1 {
		db.requestReclamation()
	}
}

func (db *DB) requestReclamation() {
	select {
	case db.reclaimSignal <- struct{}{}:
	default:
	}
}

func (db *DB) reclaimLoop(ctx context.Context, initial layoutSample) {
	defer close(db.reclaimDone)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	layout := newLayoutSchedule(initial, time.Now(), 2*time.Minute, uint64(storageOptions().TargetSegmentBytes))
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Reclamation can delay delivery. Date the actual observation.
			db.maintainKeyOrder(ctx, time.Now(), &layout)
			continue
		case <-db.reclaimSignal:
		}
		if err := db.Reclaim(ctx); err != nil && ctx.Err() == nil {
			// Disk space may be restored independently of new foreground work.
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
				db.requestReclamation()
			}
		}
	}
}
