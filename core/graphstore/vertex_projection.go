package graphstore

import (
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

// VerifiedVertices is the recovery traversal. It checks the entire ordered
// metadata projection against the canonical Vertex rows in one linear pass.
func (r *Rows[K, V]) VerifiedVertices(ctx context.Context, snap *tarp.Snapshot, gen uint64) iter.Seq2[VertexEntry[K, V], error] {
	return func(yield func(VertexEntry[K, V], error) bool) {
		var zero VertexEntry[K, V]
		family, err := familyPrefix(gen, vertexMetadataFamily)
		if err != nil {
			yield(zero, err)
			return
		}
		next, stop := iter.Pull2(snap.ScanPrefix(ctx, family))
		defer stop()
		for row, err := range r.Vertices(ctx, snap, gen, "", nil, false) {
			if err != nil {
				yield(zero, err)
				return
			}
			metadata, err, ok := next()
			if err != nil {
				yield(zero, err)
				return
			}
			if !ok {
				yield(zero, fmt.Errorf("%w: missing vertex projection", ErrInvalidRow))
				return
			}
			key, err := r.decodeVertexKey(metadata.Key)
			if err != nil {
				yield(zero, err)
				return
			}
			expiry, ts, err := parseVertexProjection(metadata.Value)
			if err != nil {
				yield(zero, err)
				return
			}
			if key != row.Key || !expiry.Equal(row.Vertex.Expiration) || ts != row.Vertex.HLC {
				yield(zero, fmt.Errorf("%w: mismatched vertex projection", ErrInvalidRow))
				return
			}
			if !yield(row, nil) {
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
