package graphstore

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/anaregdesign/tarp"
)

type FoldedEdge[K comparable] struct {
	Key    EdgeKey[K]
	Detail EdgeDetail
}

type orderedSource struct {
	tail, head string
	entry      SourceEntry
}

func scanOrderedSources(ctx context.Context, snap *tarp.Snapshot, bounds scanBounds) iter.Seq2[orderedSource, error] {
	return func(yield func(orderedSource, error) bool) {
		for row, err := range snap.ScanRange(ctx, bounds.lower, bounds.upper) {
			if err != nil {
				yield(orderedSource{}, err)
				return
			}
			tuple, err := sourceTupleCodec.Decode(row.Key)
			if err != nil {
				yield(orderedSource{}, fmt.Errorf("%w: decode source key: %w", ErrInvalidRow, err))
				return
			}
			id, err := sourceIdentity(tuple)
			if err != nil {
				yield(orderedSource{}, err)
				return
			}
			value, err := decodeAdd(row.Value)
			if err != nil {
				yield(orderedSource{}, err)
				return
			}
			if !yield(orderedSource{tuple.Edge.Tail, tuple.Edge.Head, SourceEntry{id, value}}, nil) {
				return
			}
		}
	}
}

// FoldedEdges merges two ascending ordered ranges in one pinned generation,
// decoding the rows once. It emits nonzero live sums and leaves endpoint
// visibility to the caller. Working memory is bounded by the largest bucket.
func (r *Rows[K, V]) FoldedEdges(ctx context.Context, snap *tarp.Snapshot, gen uint64, tailPrefix string, now time.Time) iter.Seq2[FoldedEdge[K], error] {
	return func(yield func(FoldedEdge[K], error) bool) {
		var zero FoldedEdge[K]
		edgePrefix, err := familyPrefix(gen, edgeFamily)
		if err != nil {
			yield(zero, err)
			return
		}
		sourcePrefix, err := familyPrefix(gen, sourceFamily)
		if err != nil {
			yield(zero, err)
			return
		}
		prefix := tarp.EncodeOrderedStringPrefix(tailPrefix)
		edges := prefixBounds(append(edgePrefix, prefix...))
		sources := prefixBounds(append(sourcePrefix, prefix...))
		next, stop := iter.Pull2(scanOrderedSources(ctx, snap, sources))
		defer stop()
		source, scanErr, has := next()
		var scratch []SourceEntry
		for row, err := range snap.ScanRange(ctx, edges.lower, edges.upper) {
			if err != nil {
				yield(zero, err)
				return
			}
			pair, err := edgeTupleCodec.Decode(row.Key)
			if err != nil {
				yield(zero, fmt.Errorf("%w: decode edge key: %w", ErrInvalidRow, err))
				return
			}
			edge, err := decodeEdge(row.Value)
			if err != nil {
				yield(zero, err)
				return
			}
			scratch = liveBase(scratch, edge, now)
			for has && scanErr == nil {
				cmp := strings.Compare(source.tail, pair.Tail)
				if cmp == 0 {
					cmp = strings.Compare(source.head, pair.Head)
				}
				if cmp > 0 {
					break
				}
				if cmp < 0 {
					yield(zero, fmt.Errorf("%w: source without edge", ErrInvalidRow))
					return
				}
				scratch, err = addLiveSource(scratch, edge.LastOrdinal, source.entry, now)
				if err != nil {
					yield(zero, err)
					return
				}
				source, scanErr, has = next()
			}
			if scanErr != nil {
				yield(zero, scanErr)
				return
			}
			d := foldSources(scratch)
			if !d.Found {
				continue
			}
			k, err := r.decodeEdgeKey(pair)
			if err != nil {
				yield(zero, fmt.Errorf("%w: decode edge identity: %w", ErrInvalidRow, err))
				return
			}
			if !yield(FoldedEdge[K]{k, d}, nil) {
				return
			}
		}
		if scanErr != nil {
			yield(zero, scanErr)
		} else if has {
			yield(zero, fmt.Errorf("%w: source without edge", ErrInvalidRow))
		}
	}
}
