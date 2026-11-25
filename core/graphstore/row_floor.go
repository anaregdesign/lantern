package graphstore

import (
	"context"
	"fmt"
	"iter"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func (r *Rows[K, V]) floorKey(gen uint64, key FloorKey[K]) ([]byte, error) {
	family := byte(key.Kind)
	edge := EdgeKey[K]{key.Tail, key.Head}
	var zero K
	var noID [24]byte
	switch key.Kind {
	case VertexBarrier, VertexTombstone:
		if key.Tail == zero && key.Head == zero && key.ContribID == noID {
			return r.vertexKey(gen, family, key.Key)
		}
	case EdgeBarrier, EdgeTombstone:
		if key.Key == zero && key.ContribID == noID {
			return r.edgeKey(gen, family, edge)
		}
	case ContributionTombstone:
		if key.Key == zero && key.ContribID != noID {
			return r.sourceKey(gen, family, edge, SourceID{ContribID: key.ContribID})
		}
	}
	return nil, fmt.Errorf("%w: invalid causal identity", ErrInvalidRow)
}

func validateFloor(kind FloorKind, f Floor) error {
	isBarrier := kind == VertexBarrier || kind == EdgeBarrier
	if f.HLC == (hlc.Timestamp{}) || isBarrier != f.Expiration.IsZero() {
		return fmt.Errorf("%w: invalid floor or retention deadline", ErrInvalidRow)
	}
	return nil
}

func decodeFloor(kind FloorKind, data []byte) (Floor, error) {
	if len(data) != metadataSize {
		return Floor{}, fmt.Errorf("%w: invalid causal row size", ErrInvalidRow)
	}
	expiry, ts, err := parseMetadata(data)
	if err != nil {
		return Floor{}, err
	}
	f := Floor{ts, expiry}
	return f, validateFloor(kind, f)
}

func (r *Rows[K, V]) GetFloor(snap *tarp.Snapshot, gen uint64, k FloorKey[K]) (Floor, bool, error) {
	key, err := r.floorKey(gen, k)
	if err != nil {
		return Floor{}, false, err
	}
	return readRow(snap, key, func(data []byte) (Floor, error) { return decodeFloor(k.Kind, data) })
}

func (r *Rows[K, V]) PutFloor(b Writer, gen uint64, k FloorKey[K], f Floor) error {
	key, err := r.floorKey(gen, k)
	if err != nil {
		return err
	}
	if err := validateFloor(k.Kind, f); err != nil {
		return err
	}
	b.Put(key, appendMetadata(nil, f.Expiration, f.HLC))
	return nil
}

func (r *Rows[K, V]) DeleteFloor(b Writer, gen uint64, k FloorKey[K]) error {
	key, err := r.floorKey(gen, k)
	if err != nil {
		return err
	}
	b.Delete(key)
	return nil
}

func (r *Rows[K, V]) decodeFloorKey(kind FloorKind, key []byte) (FloorKey[K], error) {
	out := FloorKey[K]{Kind: kind}
	var edge edgeTuple
	switch kind {
	case VertexBarrier, VertexTombstone:
		tuple, err := vertexTupleCodec.Decode(key)
		if err != nil {
			return out, err
		}
		out.Key, err = r.keys.Decode([]byte(tuple.Key))
		return out, err
	case EdgeBarrier, EdgeTombstone:
		tuple, err := edgeTupleCodec.Decode(key)
		if err != nil {
			return out, err
		}
		edge = tuple
	case ContributionTombstone:
		tuple, err := sourceTupleCodec.Decode(key)
		if err != nil {
			return out, err
		}
		id, err := sourceIdentity(tuple)
		if err != nil || id.Ordinal != 0 {
			return out, fmt.Errorf("%w: invalid causal source", ErrInvalidRow)
		}
		edge = tuple.Edge
		out.ContribID = id.ContribID
	}
	k, err := r.decodeEdgeKey(edge)
	out.Tail, out.Head = k.Tail, k.Head
	return out, err
}

// Floors visits retained causal rows, including expired tombstones whose
// physical reclamation is pending. A single pinned snapshot covers all kinds.
func (r *Rows[K, V]) Floors(ctx context.Context, snap *tarp.Snapshot, gen uint64) iter.Seq2[FloorEntry[K], error] {
	return func(yield func(FloorEntry[K], error) bool) {
		for kind := VertexBarrier; kind <= ContributionTombstone; kind++ {
			p, err := familyPrefix(gen, byte(kind))
			if err != nil {
				yield(FloorEntry[K]{}, err)
				return
			}
			lo, hi := tarp.PrefixRange(p)
			for row, err := range snap.ScanRange(ctx, lo, hi) {
				if err != nil {
					yield(FloorEntry[K]{}, err)
					return
				}
				key, err := r.decodeFloorKey(kind, row.Key)
				if err != nil {
					yield(FloorEntry[K]{}, fmt.Errorf("%w: decode causal key: %w", ErrInvalidRow, err))
					return
				}
				floor, err := decodeFloor(kind, row.Value)
				if err != nil {
					yield(FloorEntry[K]{}, err)
					return
				}
				if !yield(FloorEntry[K]{key, floor}, nil) {
					return
				}
			}
		}
	}
}
