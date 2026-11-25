package graphstore

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"

	"github.com/anaregdesign/tarp"
)

// UpdateRows commits a graph mutation and its application records together.
// If a single Tarp batch cannot hold the result, it streams the same postimage
// into an unpublished generation and switches the manifest once. prepare may
// be called again on the same snapshot if its writes exceed a single batch.
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
	batch := boundedUpdate{generation: db.head.active}
	if err := prepare(db.head.active, snap, &batch); err != nil {
		return err
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
		return db.commit(next, &batch.batch)
	}
	batch.batch = tarp.WriteBatch{}
	plan := rowUpdate{generation: db.head.active, changes: make(map[string]rowChange)}
	if err := prepare(db.head.active, snap, &plan); err != nil {
		return err
	}
	if plan.err != nil {
		return plan.err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(plan.changes) < tarp.MaxWriteBatchEntries && plan.size+128 < uint64(storageOptions().TargetSegmentBytes) {
		var b tarp.WriteBatch
		for key, op := range plan.changes {
			if op.deleted {
				b.Delete([]byte(key))
			} else {
				b.Put([]byte(key), op.value)
			}
		}
		return db.commit(next, &b)
	}
	return db.replaceRows(ctx, snap, plan, next)
}

// Ordinary requests write directly into Tarp's owned batch arena. Only
// oversized requests need the map of distinct keys for generation replacement.
type boundedUpdate struct {
	generation uint64
	batch      tarp.WriteBatch
	size       uint64
	oversized  bool
	err        error
}

func (b *boundedUpdate) Put(k, v []byte) {
	if b.fits(k, v) {
		b.batch.Put(k, v)
	}
}
func (b *boundedUpdate) Delete(k []byte) {
	if b.fits(k, nil) {
		b.batch.Delete(k)
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
	b.size += cost
	b.oversized = b.batch.Len()+2 > tarp.MaxWriteBatchEntries || b.size+128 >= uint64(storageOptions().TargetSegmentBytes)
	return !b.oversized
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
	size       uint64
	err        error
}

func (p *rowUpdate) Put(key, value []byte) { p.set(key, value, false) }
func (p *rowUpdate) Delete(key []byte)     { p.set(key, nil, true) }

func (p *rowUpdate) set(key, value []byte, remove bool) {
	if p.err != nil {
		return
	}
	cost, err := rowSize(p.generation, key, value)
	if err != nil {
		p.err = err
		return
	}
	if old, ok := p.changes[string(key)]; ok {
		p.size -= uint64(len(key)) + uint64(len(old.value)) + 48
	}
	p.size += cost
	p.changes[string(key)] = rowChange{value: bytes.Clone(value), deleted: remove}
}

// The caller holds the writer gate. Only the last commit changes the logical
// revision. Interrupted copies remain unreachable and are reclaimed on reopen.
func (db *DB) replaceRows(ctx context.Context, snap *tarp.Snapshot, p rowUpdate, next manifest) error {
	allocation := db.head
	if allocation.allocated == math.MaxUint64 {
		return errors.New("graphstore: exhausted generation IDs")
	}
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
