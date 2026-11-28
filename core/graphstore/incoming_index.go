package graphstore

import (
	"context"
	"fmt"
	"iter"

	"github.com/anaregdesign/tarp"
)

func (r *Rows[K, V]) edgeKeys(gen uint64, k EdgeKey[K]) (primary, incoming []byte, err error) {
	tuple, err := r.edgeTuple(gen, edgeFamily, k)
	if err != nil {
		return nil, nil, err
	}
	primary, err = edgeTupleCodec.Encode(tuple)
	if err != nil {
		return nil, nil, err
	}
	incoming, err = encodeIncomingEdgeKey(tuple)
	return primary, incoming, err
}

func encodeIncomingEdgeKey(tuple edgeTuple) ([]byte, error) {
	tuple.Header.Family = incomingEdgeFamily
	tuple.Tail, tuple.Head = tuple.Head, tuple.Tail
	return edgeTupleCodec.Encode(tuple)
}

// MarkIncomingEdgeIndex records that a generation has a complete incoming
// projection. A generation builder must stage all its Edge headers first.
func (r *Rows[K, V]) MarkIncomingEdgeIndex(b Writer, gen uint64) error {
	marker, err := familyPrefix(gen, incomingEdgeFamily)
	if err != nil {
		return err
	}
	b.Put(marker, nil)
	return nil
}

// HasIncomingEdgeIndex reports whether the pinned generation has a complete
// projection. Rows written by an interrupted backfill may precede its marker.
func (r *Rows[K, V]) HasIncomingEdgeIndex(snap *tarp.Snapshot, gen uint64) (bool, error) {
	return hasIncomingEdgeIndex(snap, gen)
}

func hasIncomingEdgeIndex(snap *tarp.Snapshot, gen uint64) (bool, error) {
	marker, err := familyPrefix(gen, incomingEdgeFamily)
	if err != nil {
		return false, err
	}
	_, found, err := readRow(snap, marker, func(value []byte) (struct{}, error) {
		if len(value) != 0 {
			return struct{}{}, fmt.Errorf("%w: nonempty incoming index marker", ErrInvalidRow)
		}
		return struct{}{}, nil
	})
	return found, err
}

// IncomingEdges visits the projected pairs for one exact head, ordered by
// tail. It reads only keys. Check HasIncomingEdgeIndex on the same snapshot,
// and use the primary rows to evaluate Edge liveness.
func (r *Rows[K, V]) IncomingEdges(ctx context.Context, snap *tarp.Snapshot, gen uint64, head K) iter.Seq2[EdgeKey[K], error] {
	return func(yield func(EdgeKey[K], error) bool) {
		h, err := header(gen, incomingEdgeFamily)
		if err != nil {
			yield(EdgeKey[K]{}, err)
			return
		}
		encoded, err := r.keys.Encode(head)
		if err != nil {
			yield(EdgeKey[K]{}, err)
			return
		}
		prefix, err := tarp.EncodeOrderedTuplePrefix(h, string(encoded))
		if err != nil {
			yield(EdgeKey[K]{}, err)
			return
		}
		lo, hi := tarp.PrefixRange(prefix)
		for key, err := range snap.KeysRange(ctx, lo, hi) {
			if err != nil {
				yield(EdgeKey[K]{}, err)
				return
			}
			tuple, err := edgeTupleCodec.Decode(key)
			if err != nil {
				yield(EdgeKey[K]{}, fmt.Errorf("%w: decode incoming edge key: %w", ErrInvalidRow, err))
				return
			}
			tuple.Tail, tuple.Head = tuple.Head, tuple.Tail
			edge, err := r.decodeEdgeKey(tuple)
			if err != nil {
				yield(EdgeKey[K]{}, fmt.Errorf("%w: decode incoming edge identity: %w", ErrInvalidRow, err))
				return
			}
			if !yield(edge, nil) {
				return
			}
		}
	}
}

// EnsureIncomingEdgeIndex backfills the active graph before serving. All
// chunks share the same pinned set of Edge keys and the DB writer gate.
// Interrupted chunks can be retried because they contain only derived keys.
// Publishing the completion marker does not advance the application revision.
func (r *Rows[K, V]) EnsureIncomingEdgeIndex(ctx context.Context, db *DB) error {
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

	gen := db.head.active
	if present, err := r.HasIncomingEdgeIndex(snap, gen); err != nil || present {
		return err
	}
	db.interruptKeyOrder()
	primary, err := familyPrefix(gen, edgeFamily)
	if err != nil {
		return err
	}
	bounds := prefixBounds(primary)
	var batch tarp.WriteBatch
	var size uint64
	for key, err := range snap.KeysRange(ctx, bounds.lower, bounds.upper) {
		if err != nil {
			return err
		}
		tuple, err := edgeTupleCodec.Decode(key)
		if err != nil {
			return fmt.Errorf("%w: decode primary edge key: %w", ErrInvalidRow, err)
		}
		incoming, err := encodeIncomingEdgeKey(tuple)
		if err != nil {
			return err
		}
		recordBytes, err := rowSize(gen, incoming, nil)
		if err != nil {
			return err
		}
		if batch.Len() > 0 && (batch.Len() >= 512 || size+recordBytes > 1<<20) {
			if err := db.apply(&batch); err != nil {
				return err
			}
			batch.Reset()
			size = 0
		}
		batch.Put(incoming, nil)
		size += recordBytes
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.MarkIncomingEdgeIndex(&batch, gen); err != nil {
		return err
	}
	return db.apply(&batch)
}
