package graphstore

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
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

// EdgeBucket groups a committed header with all of its retained Add rows.
// Sources is borrowed until the caller advances the iterator.
type EdgeBucket[K comparable] struct {
	EdgeEntry[K]
	Sources []SourceEntry
}

// EdgeBuckets merges the Edge and Source families in physical order, verifying
// that every Add has an owning header and belongs to its ordinal range.
func (r *Rows[K, V]) EdgeBuckets(ctx context.Context, snap *tarp.Snapshot, gen uint64, tailPrefix string) iter.Seq2[EdgeBucket[K], error] {
	return func(yield func(EdgeBucket[K], error) bool) {
		var zero EdgeBucket[K]
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
			scratch = scratch[:0]
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
				if source.entry.ID.Ordinal > edge.LastOrdinal {
					yield(zero, fmt.Errorf("%w: Add ordinal exceeds Edge high-water mark", ErrInvalidRow))
					return
				}
				scratch = append(scratch, source.entry)
				source, scanErr, has = next()
			}
			if scanErr != nil {
				yield(zero, scanErr)
				return
			}
			k, err := r.decodeEdgeKey(pair)
			if err != nil {
				yield(zero, fmt.Errorf("%w: decode edge identity: %w", ErrInvalidRow, err))
				return
			}
			if !yield(EdgeBucket[K]{EdgeEntry: EdgeEntry[K]{k, edge}, Sources: scratch}, nil) {
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

// FoldedEdges emits nonzero live sums and leaves endpoint visibility to the
// caller. Working memory is bounded by the largest bucket.
func (r *Rows[K, V]) FoldedEdges(ctx context.Context, snap *tarp.Snapshot, gen uint64, tailPrefix string, now time.Time) iter.Seq2[FoldedEdge[K], error] {
	return func(yield func(FoldedEdge[K], error) bool) {
		var scratch []SourceEntry
		for bucket, err := range r.EdgeBuckets(ctx, snap, gen, tailPrefix) {
			if err != nil {
				yield(FoldedEdge[K]{}, err)
				return
			}
			scratch = liveBase(scratch, bucket.Edge, now)
			for _, source := range bucket.Sources {
				if cache.IsLiveAt(source.Source.Expiration, now) {
					scratch = append(scratch, source)
				}
			}
			if d := foldSources(scratch); d.Found && !yield(FoldedEdge[K]{bucket.Key, d}, nil) {
				return
			}
		}
	}
}
