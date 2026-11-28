package graphstore

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"maps"
	"math"
	"slices"

	"github.com/anaregdesign/tarp"
)

// UpdateRows commits a graph mutation and its application records together.
// If a single Tarp batch cannot hold the result, it stages the distinct
// postimage across segments. prepare may be called again on the same snapshot
// if its writes exceed a single batch.
// It must only stage rows and return the same postimage on each invocation.
func (db *DB) UpdateRows(ctx context.Context, prepare func(uint64, *tarp.Snapshot, Writer) error) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.available(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	snap, err := db.raw.Snapshot()
	if err != nil {
		return err
	}
	defer snap.Close()
	sizer, err := db.rowBatchSizer()
	if err != nil {
		return err
	}
	batch := boundedUpdate{generation: db.head.active, sizer: sizer}
	if err := prepare(db.head.active, snap, &batch); err != nil {
		return err
	}
	if !batch.oversized && batch.err == nil {
		if err := batch.topology.finish(ctx, snap, db.head.active, &batch); err != nil {
			return err
		}
	}
	if batch.err != nil || batch.batch.Len() == 0 && !batch.oversized {
		return batch.err
	}
	next := db.head
	if err := next.advance(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !batch.oversized {
		if err := db.commitRows(next, &batch.batch, batch.graphBytes); !exceedsBatchCapacity(err) {
			return err
		}
	}
	batch.batch = tarp.WriteBatch{}
	batch.topology = topologyChanges{}
	plan := rowUpdate{generation: db.head.active, changes: make(map[string]rowChange)}
	if err := prepare(db.head.active, snap, &plan); err != nil {
		return err
	}
	if plan.err == nil {
		if err := plan.topology.finish(ctx, snap, db.head.active, &plan); err != nil {
			return err
		}
	}
	if plan.err != nil {
		return plan.err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Try the distinct postimage before going through disk staging.
	applied, err := db.applyCoalescedRows(ctx, snap, next, plan)
	if err != nil || applied {
		return err
	}
	err = db.commitStagedRows(ctx, next, plan)
	if canReplaceStagedLimit(err) {
		return db.replaceRows(ctx, snap, plan, next)
	}
	return err
}

func (db *DB) rowBatchSizer() (tarp.WriteBatchSizer, error) {
	sizer := db.raw.WriteBatchSizer()
	// The application manifest is appended to every ordinary commit.
	err := sizer.Add(uint64(len(manifestKey)), uint64(len(db.head.encode())))
	return sizer, err
}

func (db *DB) applyCoalescedRows(ctx context.Context, snap *tarp.Snapshot, next manifest, plan rowUpdate) (bool, error) {
	sizer, err := db.rowBatchSizer()
	if err != nil {
		return false, err
	}
	fits := true
	for key, op := range plan.changes {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if op.deleted {
			physical := []byte(key)
			present := false
			for _, err := range snap.KeysRange(ctx, physical, append([]byte(key), 0)) {
				if err != nil {
					return false, err
				}
				present = true
				break
			}
			if !present {
				delete(plan.changes, key)
				continue
			}
		}
		if fits {
			if err := sizer.Add(uint64(len(key)), uint64(len(op.value))); err != nil {
				if !canCoalesce(err) {
					return false, err
				}
				fits = false
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !fits {
		return false, nil
	}
	var batch tarp.WriteBatch
	var graphBytes uint64
	for key, op := range plan.changes {
		if key[0] == 'G' {
			graphBytes += uint64(len(key)) + uint64(len(op.value)) + 48
		}
		if op.deleted {
			batch.Delete([]byte(key))
		} else {
			batch.Put([]byte(key), op.value)
		}
	}
	err = db.commitRows(next, &batch, graphBytes)
	if exceedsBatchCapacity(err) {
		return false, nil
	}
	return true, err
}

func canCoalesce(err error) bool {
	limit, ok := err.(*tarp.BatchLimitError)
	return ok && (limit.Kind == tarp.BatchRecordBytesLimit || limit.Kind == tarp.BatchInputCountLimit)
}

func exceedsBatchCapacity(err error) bool {
	// Apply returns this validation error directly, before publishing any
	// records. An OpError carries its own publication outcome and must be
	// returned to the caller intact.
	limit, ok := err.(*tarp.BatchLimitError)
	return ok && limit.Kind == tarp.BatchRecordBytesLimit
}

func (db *DB) commitRows(next manifest, batch *tarp.WriteBatch, graphBytes uint64) error {
	if err := db.commit(next, batch); err != nil {
		return err
	}
	db.graphInput += graphBytes
	return nil
}

// The graph writer gate stays held through spooling and publication. The
// manifest, graph rows and service rows advance together without a generation
// change, so already pinned views and generation-scoped consumers still work.
func (db *DB) commitStagedRows(ctx context.Context, next manifest, plan rowUpdate) error {
	db.interruptKeyOrder()
	keys := append(slices.Collect(maps.Keys(plan.changes)), manifestKey)
	slices.Sort(keys)
	var graphBytes uint64
	err := db.observeWrite(db.raw.ApplyStaged(ctx, func(yield func(tarp.StagedMutation, error) bool) {
		for _, key := range keys {
			if key == manifestKey {
				if !yield(tarp.StagedMutation{Key: []byte(key), Value: next.encode()}, nil) {
					return
				}
				continue
			}
			op := plan.changes[key]
			if key[0] == 'G' {
				graphBytes += uint64(len(key)) + uint64(len(op.value)) + 48
			}
			if !yield(tarp.StagedMutation{Key: []byte(key), Value: op.value, Delete: op.deleted}, nil) {
				return
			}
		}
	}))
	if err != nil {
		return err
	}
	db.head = next
	db.graphInput += graphBytes
	return nil
}

func canReplaceStagedLimit(err error) bool {
	// A second publication is permitted only for a definite staged limit
	// with successful cleanup.
	op, ok := err.(*tarp.OpError)
	if !ok || op.Outcome != tarp.NotApplied || errors.Is(err, tarp.ErrUnhealthy) || errors.Is(err, tarp.ErrCommitUncertain) {
		return false
	}
	return op.Phase == "input" && canCoalesce(op.Err) ||
		op.Phase == "prepare" && errors.Is(op.Err, tarp.ErrTooLarge)
}

// Ordinary requests write directly into Tarp's owned batch arena. Only
// oversized requests need the map of distinct keys for staged publication.
type boundedUpdate struct {
	generation uint64
	batch      tarp.WriteBatch
	sizer      tarp.WriteBatchSizer
	topology   topologyChanges
	graphBytes uint64
	oversized  bool
	err        error
}

func (b *boundedUpdate) Put(k, v []byte) {
	if b.fits(k, v) {
		b.batch.Put(k, v)
		b.topology.note(k, true)
	}
}
func (b *boundedUpdate) Delete(k []byte) {
	if b.fits(k, nil) {
		b.batch.Delete(k)
		b.topology.note(k, false)
	}
}
func (b *boundedUpdate) fits(k, v []byte) bool {
	if b.err != nil || b.oversized {
		return false
	}
	cost, err := rowSize(b.generation, k, v)
	if err != nil {
		b.err = err
		return false
	}
	if err := b.sizer.Add(uint64(len(k)), uint64(len(v))); err != nil {
		if canCoalesce(err) {
			b.oversized = true
		} else {
			b.err = err
		}
		return false
	}
	if k[0] == 'G' {
		b.graphBytes += cost
	}
	return true
}

func rowSize(gen uint64, k, v []byte) (uint64, error) {
	if len(k) < 10 || (k[0] != 'G' && k[0] != 'S') || binary.BigEndian.Uint64(k[1:9]) != gen {
		return 0, errors.New("graphstore: update outside the active generation")
	}
	cost := uint64(len(k)) + uint64(len(v)) + 48
	if cost > uint64(storageOptions().MaxRecordBytes) {
		return 0, tarp.ErrTooLarge
	}
	return cost, nil
}

type rowChange struct {
	value   []byte
	deleted bool
}

type rowUpdate struct {
	generation uint64
	changes    map[string]rowChange
	topology   topologyChanges
	err        error
}

func (p *rowUpdate) Put(key, value []byte) { p.set(key, value, false) }
func (p *rowUpdate) Delete(key []byte)     { p.set(key, nil, true) }

func (p *rowUpdate) set(key, value []byte, remove bool) {
	if p.err != nil {
		return
	}
	if _, err := rowSize(p.generation, key, value); err != nil {
		p.err = err
		return
	}
	p.changes[string(key)] = rowChange{value: bytes.Clone(value), deleted: remove}
	p.topology.note(key, !remove)
}

// The caller holds the writer gate. Only the last commit changes the logical
// revision. Interrupted copies remain unreachable and are reclaimed on reopen.
func (db *DB) replaceRows(ctx context.Context, snap *tarp.Snapshot, p rowUpdate, next manifest) error {
	allocation := db.head
	if allocation.allocated == math.MaxUint64 {
		return errors.New("graphstore: exhausted generation IDs")
	}
	db.interruptKeyOrder()
	allocation.allocated++
	id := allocation.allocated
	var marker tarp.WriteBatch
	marker.Put(generationMarker(id), nil)
	if err := db.commit(allocation, &marker); err != nil {
		return err
	}
	defer db.requestReclamation()
	chunk := generationWriter{id: id}
	write := func(oldKey, value []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if chunk.batch.Len() >= 512 || chunk.bytes+uint64(len(oldKey))+uint64(len(value))+48 > 1<<20 && chunk.batch.Len() != 0 {
			if err := db.apply(&chunk.batch); err != nil {
				return err
			}
			chunk.batch.Reset()
			chunk.bytes = 0
		}
		key := bytes.Clone(oldKey)
		binary.BigEndian.PutUint64(key[1:9], id)
		chunk.Put(key, value)
		return chunk.err
	}
	for _, domain := range []byte{'G', 'S'} {
		prefix := binary.BigEndian.AppendUint64([]byte{domain}, p.generation)
		for entry, err := range snap.ScanPrefix(ctx, prefix) {
			if err != nil {
				return err
			}
			if _, replaced := p.changes[string(entry.Key)]; !replaced {
				if err := write(entry.Key, entry.Value); err != nil {
					return err
				}
			}
		}
	}
	for key, op := range p.changes {
		if !op.deleted {
			if err := write([]byte(key), op.value); err != nil {
				return err
			}
		}
	}
	if err := db.apply(&chunk.batch); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	next.active, next.allocated = id, id
	return db.commit(next, &tarp.WriteBatch{})
}
