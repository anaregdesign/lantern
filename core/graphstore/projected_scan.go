package graphstore

import (
	"context"
	"fmt"
	"iter"
	"time"

	"github.com/anaregdesign/lantern/core/cache"

	"github.com/anaregdesign/tarp"
)

// projectedBounds narrows the next ordered tuple field to [lower,upper).
// The caller's limits are raw projected bytes; the bounds themselves need
// not be valid user identities.
func projectedBounds(prefix []byte, lower, upper string) scanBounds {
	bounds := prefixBounds(prefix)
	if lower != "" {
		bounds.lower = append(append([]byte(nil), prefix...), tarp.EncodeOrderedStringPrefix(lower)...)
	}
	if upper != "" {
		bounds.upper = append(append([]byte(nil), prefix...), tarp.EncodeOrderedStringPrefix(upper)...)
	}
	return bounds
}

// LiveVertexRange performs one ordered Tarp scan. Keys-only consumers inspect
// the fixed expiration metadata and never decode the application value.
func (r *Rows[K, V]) LiveVertexRange(ctx context.Context, snap *tarp.Snapshot, gen uint64, lower, upper string, after *K, reverse, keysOnly bool, now time.Time) iter.Seq2[VertexEntry[K, V], error] {
	return func(yield func(VertexEntry[K, V], error) bool) {
		var zero VertexEntry[K, V]
		p, err := familyPrefix(gen, vertexFamily)
		if err != nil {
			yield(zero, err)
			return
		}
		bounds := projectedBounds(p, lower, upper)
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
			deadline, ts, err := parseMetadata(row.Value)
			if err != nil {
				yield(zero, err)
				return
			}
			if !cache.IsLiveAt(deadline, now) {
				continue
			}
			k, err := r.decodeVertexKey(row.Key)
			if err != nil {
				yield(zero, err)
				return
			}
			v := Vertex[V]{Expiration: deadline, HLC: ts}
			if !keysOnly {
				v.Value, err = r.values.Decode(row.Value[metadataSize:])
				if err != nil {
					yield(zero, fmt.Errorf("%w: decode vertex: %w", ErrInvalidRow, err))
					return
				}
			}
			if !yield(VertexEntry[K, V]{k, v}, nil) {
				return
			}
		}
	}
}

func (r *Rows[K, V]) decodeVertexKey(raw []byte) (K, error) {
	var zero K
	tuple, err := vertexTupleCodec.Decode(raw)
	if err != nil {
		return zero, fmt.Errorf("%w: decode vertex key: %w", ErrInvalidRow, err)
	}
	k, err := r.keys.Decode([]byte(tuple.Key))
	if err != nil {
		return zero, fmt.Errorf("%w: decode vertex identity: %w", ErrInvalidRow, err)
	}
	return k, nil
}

// VertexKeysRange visits keys only. A caller enforcing data policy can
// select admitted key ranges before reading potentially private values.
func (r *Rows[K, V]) VertexKeysRange(ctx context.Context, snap *tarp.Snapshot, gen uint64, lower, upper string, after *K, reverse bool) iter.Seq2[K, error] {
	return func(yield func(K, error) bool) {
		var zero K
		p, err := familyPrefix(gen, vertexFamily)
		if err != nil {
			yield(zero, err)
			return
		}
		bounds := projectedBounds(p, lower, upper)
		if after != nil {
			k, err := r.vertexKey(gen, vertexFamily, *after)
			if err != nil {
				yield(zero, err)
				return
			}
			bounds = bounds.after(k, reverse)
		}
		for key, err := range snap.KeysRange(ctx, bounds.lower, bounds.upper, scanOptions(reverse)...) {
			if err != nil {
				yield(zero, err)
				return
			}
			k, err := r.decodeVertexKey(key)
			if err != nil {
				yield(zero, err)
				return
			}
			if !yield(k, nil) {
				return
			}
		}
	}
}

// EdgeTailsRange seeks distinct actual tails within the projected interval.
// Lower is inclusive so a pair cursor can resume within its last tail.
func (r *Rows[K, V]) EdgeTailsRange(ctx context.Context, snap *tarp.Snapshot, gen uint64, lower, upper string) iter.Seq2[K, error] {
	return func(yield func(K, error) bool) {
		var zero K
		p, err := familyPrefix(gen, edgeFamily)
		if err != nil {
			yield(zero, err)
			return
		}
		bounds := projectedBounds(p, lower, upper)
		for {
			var first []byte
			for key, err := range snap.KeysRange(ctx, bounds.lower, bounds.upper) {
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
			tuple, err := edgeTupleCodec.Decode(first)
			if err != nil {
				yield(zero, fmt.Errorf("%w: decode edge tuple: %w", ErrInvalidRow, err))
				return
			}
			tail, err := r.keys.Decode([]byte(tuple.Tail))
			if err != nil {
				yield(zero, fmt.Errorf("%w: decode tail identity: %w", ErrInvalidRow, err))
				return
			}
			if !yield(tail, nil) {
				return
			}
			tailPrefix, err := tarp.EncodeOrderedTuplePrefix(tuple.Header, tuple.Tail)
			if err != nil {
				yield(zero, err)
				return
			}
			_, bounds.lower = tarp.PrefixRange(tailPrefix)
		}
	}
}

// EdgeHeadsRange walks a half-open interval of heads on one exact tail.
// Passing after resumes strictly past that head.
func (r *Rows[K, V]) EdgeHeadsRange(ctx context.Context, snap *tarp.Snapshot, gen uint64, tail K, lower, upper string, after *K) iter.Seq2[K, error] {
	return func(yield func(K, error) bool) {
		var zero K
		p, err := r.vertexKey(gen, edgeFamily, tail)
		if err != nil {
			yield(zero, err)
			return
		}
		bounds := projectedBounds(p, lower, upper)
		if after != nil {
			k, err := r.edgeKey(gen, edgeFamily, EdgeKey[K]{tail, *after})
			if err != nil {
				yield(zero, err)
				return
			}
			bounds = bounds.after(k, false)
		}
		for key, err := range snap.KeysRange(ctx, bounds.lower, bounds.upper) {
			if err != nil {
				yield(zero, err)
				return
			}
			tuple, err := edgeTupleCodec.Decode(key)
			if err != nil {
				yield(zero, fmt.Errorf("%w: decode edge tuple: %w", ErrInvalidRow, err))
				return
			}
			head, err := r.keys.Decode([]byte(tuple.Head))
			if err != nil {
				yield(zero, fmt.Errorf("%w: decode head identity: %w", ErrInvalidRow, err))
				return
			}
			if !yield(head, nil) {
				return
			}
		}
	}
}
