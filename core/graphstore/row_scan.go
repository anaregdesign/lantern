package graphstore

import (
	"bytes"
	"context"
	"fmt"
	"iter"

	"github.com/anaregdesign/tarp"
)

type scanBounds struct{ lower, upper []byte }

func prefixBounds(prefix []byte) scanBounds {
	lower, upper := tarp.PrefixRange(prefix)
	return scanBounds{lower, upper}
}

func (s scanBounds) intersect(other scanBounds) scanBounds {
	if bytes.Compare(other.lower, s.lower) > 0 {
		s.lower = other.lower
	}
	if len(other.upper) > 0 && (len(s.upper) == 0 || bytes.Compare(other.upper, s.upper) < 0) {
		s.upper = other.upper
	}
	return s
}

func (s scanBounds) after(cursor []byte, reverse bool) scanBounds {
	if reverse {
		return s.intersect(scanBounds{upper: cursor})
	}
	// Appending a zero is the first possible byte string strictly above
	// cursor. The complete cursor is never repeated in either direction.
	return s.intersect(scanBounds{lower: append(cursor, 0)})
}

func scanOptions(reverse bool) []tarp.ScanOption {
	if reverse {
		return []tarp.ScanOption{tarp.WithReverse()}
	}
	return nil
}

// Vertices walks physical Vertex rows. Prefix applies to the key projection
// before escaping, including embedded NUL. Reverse flips the ordering and
// direction of the exclusive cursor. The caller evaluates TTL at its read cut.
func (r *Rows[K, V]) Vertices(ctx context.Context, snap *tarp.Snapshot, gen uint64, prefix string, after *K, reverse bool) iter.Seq2[VertexEntry[K, V], error] {
	return func(yield func(VertexEntry[K, V], error) bool) {
		var zero VertexEntry[K, V]
		p, err := familyPrefix(gen, vertexFamily)
		if err != nil {
			yield(zero, err)
			return
		}
		bounds := prefixBounds(append(p, tarp.EncodeOrderedStringPrefix(prefix)...))
		if after != nil {
			cursor, err := r.vertexKey(gen, vertexFamily, *after)
			if err != nil {
				yield(zero, err)
				return
			}
			bounds = bounds.after(cursor, reverse)
		}
		for row, err := range snap.ScanRange(ctx, bounds.lower, bounds.upper, scanOptions(reverse)...) {
			if err != nil {
				yield(zero, err)
				return
			}
			key, err := r.decodeVertexKey(row.Key)
			if err != nil {
				yield(zero, err)
				return
			}
			value, err := r.decodeVertex(row.Value)
			if err != nil {
				yield(zero, err)
				return
			}
			if !yield(VertexEntry[K, V]{key, value}, nil) {
				return
			}
		}
	}
}

func (r *Rows[K, V]) decodeEdgeKey(k edgeTuple) (EdgeKey[K], error) {
	tail, err := r.keys.Decode([]byte(k.Tail))
	if err != nil {
		return EdgeKey[K]{}, err
	}
	head, err := r.keys.Decode([]byte(k.Head))
	return EdgeKey[K]{tail, head}, err
}

func (r *Rows[K, V]) yieldEdges(ctx context.Context, snap *tarp.Snapshot, bounds scanBounds, opts []tarp.ScanOption, yield func(EdgeEntry[K], error) bool) bool {
	var zero EdgeEntry[K]
	for row, err := range snap.ScanRange(ctx, bounds.lower, bounds.upper, opts...) {
		if err != nil {
			yield(zero, err)
			return false
		}
		k, err := edgeTupleCodec.Decode(row.Key)
		if err != nil {
			yield(zero, fmt.Errorf("%w: decode edge key: %w", ErrInvalidRow, err))
			return false
		}
		key, err := r.decodeEdgeKey(k)
		if err != nil {
			yield(zero, fmt.Errorf("%w: decode edge identity: %w", ErrInvalidRow, err))
			return false
		}
		value, err := decodeEdge(row.Value)
		if err != nil {
			yield(zero, err)
			return false
		}
		if !yield(EdgeEntry[K]{key, value}, nil) {
			return false
		}
	}
	return true
}

// Edges walks physical Edge headers in (tail, head) order. For a partial
// head it seeks each matching tail once, then reads only the matching head
// interval. KeysRange avoids loading values to discover the next tail.
func (r *Rows[K, V]) Edges(ctx context.Context, snap *tarp.Snapshot, gen uint64, tailPrefix, headPrefix string, after *EdgeKey[K], reverse bool) iter.Seq2[EdgeEntry[K], error] {
	return func(yield func(EdgeEntry[K], error) bool) {
		var zero EdgeEntry[K]
		p, err := familyPrefix(gen, edgeFamily)
		if err != nil {
			yield(zero, err)
			return
		}
		bounds := prefixBounds(append(p, tarp.EncodeOrderedStringPrefix(tailPrefix)...))
		if after != nil {
			cursor, err := r.edgeKey(gen, edgeFamily, *after)
			if err != nil {
				yield(zero, err)
				return
			}
			bounds = bounds.after(cursor, reverse)
		}
		opts := scanOptions(reverse)
		if headPrefix == "" {
			r.yieldEdges(ctx, snap, bounds, opts, yield)
			return
		}
		head := tarp.EncodeOrderedStringPrefix(headPrefix)
		for {
			var first []byte
			for key, err := range snap.KeysRange(ctx, bounds.lower, bounds.upper, opts...) {
				if err != nil {
					yield(zero, err)
					return
				}
				first = key
				break
			}
			if first == nil {
				return
			}
			k, err := edgeTupleCodec.Decode(first)
			if err != nil {
				yield(zero, fmt.Errorf("%w: decode edge key: %w", ErrInvalidRow, err))
				return
			}
			tail, err := tarp.EncodeOrderedTuplePrefix(k.Header, k.Tail)
			if err != nil {
				yield(zero, err)
				return
			}
			tailRange := prefixBounds(tail)
			match := bounds.intersect(prefixBounds(append(tail, head...)))
			if !r.yieldEdges(ctx, snap, match, opts, yield) {
				return
			}
			if reverse {
				bounds.upper = tailRange.lower
			} else {
				bounds.lower = tailRange.upper
			}
		}
	}
}

func (r *Rows[K, V]) sourceBounds(gen uint64, k EdgeKey[K]) (scanBounds, error) {
	tuple, err := r.edgeTuple(gen, sourceFamily, k)
	if err != nil {
		return scanBounds{}, err
	}
	p, err := edgeTupleCodec.Encode(tuple)
	return prefixBounds(p), err
}

// Sources enumerates each physically retained Add. Local sources are ordered
// by ordinal; explicit sources are ordered by ID. Folding uses the graph's
// HLC then ID order, independently of this storage enumeration order.
func (r *Rows[K, V]) Sources(ctx context.Context, snap *tarp.Snapshot, gen uint64, k EdgeKey[K]) iter.Seq2[SourceEntry, error] {
	return func(yield func(SourceEntry, error) bool) {
		bounds, err := r.sourceBounds(gen, k)
		if err != nil {
			yield(SourceEntry{}, err)
			return
		}
		for row, err := range scanOrderedSources(ctx, snap, bounds) {
			if err != nil {
				yield(SourceEntry{}, err)
				return
			}
			if !yield(row.entry, nil) {
				return
			}
		}
	}
}

// DeleteSources stages removal of all previously committed Add rows for a
// bucket. Queue any replacement rows after this method. The caller must
// serialize with other writes against the same snapshot.
func (r *Rows[K, V]) DeleteSources(ctx context.Context, snap *tarp.Snapshot, b Writer, gen uint64, k EdgeKey[K]) error {
	bounds, err := r.sourceBounds(gen, k)
	if err != nil {
		return err
	}
	for key, err := range snap.KeysRange(ctx, bounds.lower, bounds.upper) {
		if err != nil {
			return err
		}
		b.Delete(key)
	}
	return nil
}
