package graphstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/anaregdesign/tarp"
)

// EnsureSourceOrder builds or verifies the derived order on the active cut.
// It is called before serving. Interrupted work is retryable, and publishing
// the projection marker does not advance the application revision.
func (r *Rows[K, V]) EnsureSourceOrder(ctx context.Context, db *DB) error {
	return r.buildSourceOrder(ctx, db, true)
}

// BuildSourceOrder backfills a legacy active generation before recovery pins
// a read cut. A marked generation is validated by EdgeRecoverySummaries.
// An empty Source family needs no migration before runtime certification.
func (r *Rows[K, V]) BuildSourceOrder(ctx context.Context, db *DB) error {
	return r.buildSourceOrder(ctx, db, false)
}

func (r *Rows[K, V]) buildSourceOrder(ctx context.Context, db *DB, verifyReady bool) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	snap, err := db.derivedBuildSnapshot(ctx)
	if err != nil {
		return err
	}
	defer snap.Close()
	gen := db.head.active
	ready, err := hasSourceOrder(snap, gen)
	if err != nil {
		return err
	}
	if ready && !verifyReady {
		return nil
	}
	if !ready {
		db.interruptKeyOrder()
		builder := derivedBuildBatch{apply: db.apply, chunk: generationWriter{id: gen}}
		count, err := walkExpectedSourceOrder(ctx, snap, gen, func(key []byte) error {
			return builder.stage(ctx, key, nil, false)
		})
		if err != nil {
			return err
		}
		if count == 0 && !verifyReady {
			return ctx.Err()
		}
		if err := builder.flush(); err != nil {
			return err
		}
		if err := snap.Close(); err != nil {
			return err
		}
		snap, err = db.derivedBuildSnapshot(ctx)
		if err != nil {
			return err
		}
		defer snap.Close()
	}
	if err := verifySourceOrder(ctx, snap, gen); err != nil || ready {
		return err
	}
	var batch tarp.WriteBatch
	if err := r.MarkSourceOrder(&batch, gen); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return db.apply(&batch)
}

// Canonical Source identities are unique. If each expected key is present,
// the projection is exact when its key count agrees.
func verifySourceOrder(ctx context.Context, snap *tarp.Snapshot, gen uint64) error {
	want, err := walkExpectedSourceOrder(ctx, snap, gen, func(key []byte) error {
		value, err := snap.Get(key)
		if errors.Is(err, tarp.ErrNotFound) || err == nil && len(value) != 0 {
			return fmt.Errorf("%w: missing or nonempty Source order key", ErrInvalidRow)
		}
		return err
	})
	if err != nil {
		return err
	}
	marker, err := familyPrefix(gen, sourceOrderFamily)
	if err != nil {
		return err
	}
	bounds := prefixBounds(marker).after(marker, false)
	for _, err := range snap.KeysRange(ctx, bounds.lower, bounds.upper) {
		if err != nil {
			return err
		}
		if want == 0 {
			return fmt.Errorf("%w: unexpected Source order key", ErrInvalidRow)
		}
		want--
	}
	if want != 0 {
		return fmt.Errorf("%w: inconsistent Source order count", ErrInvalidRow)
	}
	return ctx.Err()
}

func walkExpectedSourceOrder(ctx context.Context, snap *tarp.Snapshot, gen uint64, visit func([]byte) error) (uint64, error) {
	prefix, err := familyPrefix(gen, sourceFamily)
	if err != nil {
		return 0, err
	}
	bounds := prefixBounds(prefix)
	var count uint64
	for row, err := range snap.ScanRange(ctx, bounds.lower, bounds.upper) {
		if err != nil {
			return 0, err
		}
		tuple, err := sourceTupleCodec.Decode(row.Key)
		if err != nil {
			return 0, fmt.Errorf("%w: decode Source key: %w", ErrInvalidRow, err)
		}
		id, err := sourceIdentity(tuple)
		if err != nil {
			return 0, err
		}
		source, err := decodeAdd(row.Value)
		if err != nil {
			return 0, err
		}
		key, err := sourceOrderKey(tuple.Edge, source.HLC, id)
		if err != nil {
			return 0, err
		}
		if err := visit(key); err != nil {
			return 0, err
		}
		count++
	}
	return count, nil
}
