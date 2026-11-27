package graphstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

func parseVertexProjection(data []byte) (time.Time, hlc.Timestamp, error) {
	if len(data) != metadataSize {
		return time.Time{}, hlc.Timestamp{}, fmt.Errorf("%w: invalid vertex projection size", ErrInvalidRow)
	}
	return parseMetadata(data)
}

// ScanRange reads DATA directly. Use the ordered key iterator and the same
// snapshot's cached point reads for this narrow, repeatedly accessed family.
func projectedEntries(ctx context.Context, snap *tarp.Snapshot, bounds scanBounds, reverse bool) iter.Seq2[tarp.Entry, error] {
	return func(yield func(tarp.Entry, error) bool) {
		for k, err := range snap.KeysRange(ctx, bounds.lower, bounds.upper, scanOptions(reverse)...) {
			if err != nil {
				yield(tarp.Entry{}, err)
				return
			}
			value, err := snap.Get(k)
			if err != nil {
				if errors.Is(err, tarp.ErrNotFound) {
					err = fmt.Errorf("%w: missing indexed vertex metadata", ErrInvalidRow)
				}
				yield(tarp.Entry{}, err)
				return
			}
			if !yield(tarp.Entry{Key: k, Value: value}, nil) {
				return
			}
		}
	}
}

// VerifiedVertexBytes checks the narrow ordered projection against the retained
// Vertex records, yielding the original application bytes without decoding
// the payload. Expired records are included.
func (r *Rows[K, V]) VerifiedVertexBytes(ctx context.Context, snap *tarp.Snapshot, gen uint64) iter.Seq2[VertexEntry[K, []byte], error] {
	return func(yield func(VertexEntry[K, []byte], error) bool) {
		var zero VertexEntry[K, []byte]
		first, err := familyPrefix(gen, vertexFamily)
		if err != nil {
			yield(zero, err)
			return
		}
		projection, err := familyPrefix(gen, vertexMetadataFamily)
		if err != nil {
			yield(zero, err)
			return
		}
		last, err := familyPrefix(gen, topologyFamily)
		if err != nil {
			yield(zero, err)
			return
		}
		// The recognized families occupy the contiguous interval from Vertex
		// to topology stats. Look outside it for undeclared graph records.
		header := prefixBounds(first[:len(first)-1])
		vertices, metadata := prefixBounds(first), prefixBounds(projection)
		_, graphEnd := tarp.PrefixRange(last)
		for _, bound := range []scanBounds{{header.lower, vertices.lower}, {graphEnd, header.upper}} {
			for _, err := range snap.KeysRange(ctx, bound.lower, bound.upper) {
				if err == nil {
					err = fmt.Errorf("%w: unknown graph row family", ErrInvalidRow)
				}
				yield(zero, err)
				return
			}
		}
		next, stop := iter.Pull2(snap.ScanRange(ctx, metadata.lower, metadata.upper))
		defer stop()
		for row, err := range snap.ScanRange(ctx, vertices.lower, vertices.upper) {
			if err != nil {
				yield(zero, err)
				return
			}
			meta, err, ok := next()
			if err != nil {
				yield(zero, err)
				return
			}
			if !ok {
				yield(zero, fmt.Errorf("%w: missing vertex projection", ErrInvalidRow))
				return
			}
			expiry, ts, err := parseMetadata(row.Value)
			if err != nil {
				yield(zero, err)
				return
			}
			if !bytes.Equal(row.Key[len(first):], meta.Key[len(last):]) || !bytes.Equal(row.Value[:metadataSize], meta.Value) {
				yield(zero, fmt.Errorf("%w: mismatched vertex projection", ErrInvalidRow))
				return
			}
			k, err := r.decodeVertexKey(row.Key)
			if err != nil {
				yield(zero, err)
				return
			}
			if !yield(VertexEntry[K, []byte]{Key: k, Vertex: Vertex[[]byte]{Value: row.Value[metadataSize:], Expiration: expiry, HLC: ts}}, nil) {
				return
			}
		}
		if _, err, ok := next(); err != nil {
			yield(zero, err)
		} else if ok {
			yield(zero, fmt.Errorf("%w: unowned vertex projection", ErrInvalidRow))
		}
	}
}

// VerifiedVertices is the recovery traversal. Each verified payload is
// decoded once before rebuilding the serving indexes.
func (r *Rows[K, V]) VerifiedVertices(ctx context.Context, snap *tarp.Snapshot, gen uint64) iter.Seq2[VertexEntry[K, V], error] {
	return func(yield func(VertexEntry[K, V], error) bool) {
		for row, err := range r.VerifiedVertexBytes(ctx, snap, gen) {
			if err != nil {
				yield(VertexEntry[K, V]{}, err)
				return
			}
			v, err := r.values.Decode(row.Vertex.Value)
			if err != nil {
				yield(VertexEntry[K, V]{}, fmt.Errorf("%w: decode vertex: %w", ErrInvalidRow, err))
				return
			}
			if !yield(VertexEntry[K, V]{row.Key, Vertex[V]{v, row.Vertex.Expiration, row.Vertex.HLC}}, nil) {
				return
			}
		}
	}
}
