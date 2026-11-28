package graphstore

import (
	"context"
	"fmt"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

type sourceOrderTuple struct {
	Edge      edgeTuple
	HLC       hlc.Timestamp
	ContribID [24]byte
	Ordinal   uint64
}

var sourceOrderCodec = orderedTuple[sourceOrderTuple]()

func sourceOrderKey(edge edgeTuple, stamp hlc.Timestamp, id SourceID) ([]byte, error) {
	edge.Header.Family = sourceOrderFamily
	return sourceOrderCodec.Encode(sourceOrderTuple{edge, stamp, id.ContribID, id.Ordinal})
}

// ReplaceSource co-stages a canonical Source and its ordering key. Before
// describes this identity on the read cut. A nil value means absence.
func (r *Rows[K, V]) ReplaceSource(b Writer, gen uint64, key EdgeKey[K], id SourceID, before, after *Source) error {
	edge, err := r.edgeTuple(gen, sourceFamily, key)
	if err != nil {
		return err
	}
	kind, identity, err := id.diskIdentity()
	if err != nil {
		return err
	}
	return replaceSource(b, sourceTuple{edge, kind, identity}, id, before, after)
}

func replaceSource(b Writer, tuple sourceTuple, id SourceID, before, after *Source) error {
	if after != nil && after.DerivedAggregate {
		return fmt.Errorf("%w: aggregate provenance on Add", ErrInvalidRow)
	}
	primary, err := sourceTupleCodec.Encode(tuple)
	if err != nil {
		return err
	}
	if before != nil && (after == nil || before.HLC != after.HLC) {
		oldOrder, err := sourceOrderKey(tuple.Edge, before.HLC, id)
		if err != nil {
			return err
		}
		b.Delete(oldOrder)
	}
	if after == nil {
		b.Delete(primary)
		return nil
	}
	order, err := sourceOrderKey(tuple.Edge, after.HLC, id)
	if err != nil {
		return err
	}
	b.Put(primary, appendSource(nil, *after))
	b.Put(order, nil)
	return nil
}

// MarkSourceOrder certifies a complete projection after all Sources in a new
// hidden generation have been staged through ReplaceSource.
func (r *Rows[K, V]) MarkSourceOrder(b Writer, gen uint64) error {
	marker, err := familyPrefix(gen, sourceOrderFamily)
	if err != nil {
		return err
	}
	b.Put(marker, nil)
	return nil
}

func hasSourceOrder(snap *tarp.Snapshot, gen uint64) (bool, error) {
	marker, err := familyPrefix(gen, sourceOrderFamily)
	if err != nil {
		return false, err
	}
	_, found, err := readRow(snap, marker, func(value []byte) (struct{}, error) {
		if len(value) != 0 {
			return struct{}{}, fmt.Errorf("%w: nonempty Source order marker", ErrInvalidRow)
		}
		return struct{}{}, nil
	})
	return found, err
}

// HasSourceOrder reports whether the pinned generation advertises an ordered
// Source projection. Recovery verifies its contents on that same cut.
func (r *Rows[K, V]) HasSourceOrder(snap *tarp.Snapshot, gen uint64) (bool, error) {
	return hasSourceOrder(snap, gen)
}

// SummarizeEdgeSources streams live weights and retained row counts for the
// pinned Edge header, omitting the selected Add identities. The cut must have
// a verified Source order. Endpoint liveness is independent of retained rows.
func (r *Rows[K, V]) SummarizeEdgeSources(ctx context.Context, snap *tarp.Snapshot, gen uint64, key EdgeKey[K], edge Edge, now time.Time, excluded map[SourceID]struct{}) (EdgeRecoverySummary[K], error) {
	var zero EdgeRecoverySummary[K]
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	ready, err := hasSourceOrder(snap, gen)
	if err != nil {
		return zero, err
	}
	if !ready {
		return zero, fmt.Errorf("%w: Source order missing at Edge summary cut", ErrInvalidRow)
	}
	var stats edgeRecoveryStats
	fold := edgeSourceFold{base: edge.Base, now: now, recovery: &stats, excluded: excluded}
	if err := r.scanOrderedEdgeSources(ctx, snap, gen, key, edge.LastOrdinal, nil, &fold); err != nil {
		return zero, err
	}
	detail := fold.finish()
	return orderedEdgeSummary(EdgeEntry[K]{Key: key, Edge: edge}, detail, stats), nil
}

func (r *Rows[K, V]) foldOrderedSources(ctx context.Context, snap *tarp.Snapshot, gen uint64, key EdgeKey[K], edge Edge, now time.Time, charge func(int) error) (EdgeDetail, error) {
	fold := edgeSourceFold{base: edge.Base, now: now}
	if err := r.scanOrderedEdgeSources(ctx, snap, gen, key, edge.LastOrdinal, charge, &fold); err != nil {
		return EdgeDetail{}, err
	}
	return fold.finish(), nil
}

func (r *Rows[K, V]) scanOrderedEdgeSources(ctx context.Context, snap *tarp.Snapshot, gen uint64, key EdgeKey[K], lastOrdinal uint64, charge func(int) error, fold *edgeSourceFold) error {
	tuple, err := r.edgeTuple(gen, sourceOrderFamily, key)
	if err != nil {
		return err
	}
	prefix, err := edgeTupleCodec.Encode(tuple)
	if err != nil {
		return err
	}
	bounds := prefixBounds(prefix)
	for physical, err := range snap.KeysRange(ctx, bounds.lower, bounds.upper) {
		if err != nil {
			return err
		}
		ordered, err := decodeSourceOrderKey(physical)
		if err != nil {
			return err
		}
		if err := fold.add(snap, ordered, lastOrdinal, charge); err != nil {
			return err
		}
	}
	return ctx.Err()
}

type edgeSourceFold struct {
	base     *Source
	now      time.Time
	detail   EdgeDetail
	recovery *edgeRecoveryStats
	excluded map[SourceID]struct{}
}

func (f *edgeSourceFold) add(snap *tarp.Snapshot, row sourceOrderTuple, lastOrdinal uint64, charge func(int) error) error {
	if row.Ordinal > lastOrdinal {
		return fmt.Errorf("%w: Add ordinal exceeds Edge high-water mark", ErrInvalidRow)
	}
	// Only canonical rows count against the same budget as a legacy fold.
	// A fixed-size locator is smaller than its charged key and Source value,
	// so ordered scans do at most two bounded row accesses per Source.
	source, err := readOrderedSource(snap, row, charge)
	if err != nil {
		return err
	}
	if _, excluded := f.excluded[SourceID{ContribID: row.ContribID, Ordinal: row.Ordinal}]; excluded {
		return nil
	}
	if f.recovery != nil {
		f.recovery.sources++
	}
	if f.base != nil && !source.HLC.Less(f.base.HLC) {
		f.addSource(*f.base, false)
		f.base = nil
	}
	f.addSource(source, row.Ordinal == 0)
	return nil
}

func (f *edgeSourceFold) addSource(source Source, explicit bool) {
	stats := f.recovery
	if stats != nil {
		retained := &stats.retained
		retained.Weight += source.Weight
		retained.Count++
		retained.Explicit = retained.Explicit || explicit
		if deadline := source.Expiration; !deadline.IsZero() {
			if retained.Earliest.IsZero() || deadline.Before(retained.Earliest) {
				retained.Earliest = deadline
			}
			if deadline.After(retained.Latest) {
				retained.Latest = deadline
			}
		}
	}
	if !cache.IsLiveAt(source.Expiration, f.now) {
		if stats != nil {
			stats.expired = true
		}
		return
	}
	f.detail.add(source)
	if stats != nil {
		stats.count++
		stats.explicit = stats.explicit || explicit
		if deadline := source.Expiration; !deadline.IsZero() && (stats.earliest.IsZero() || deadline.Before(stats.earliest)) {
			stats.earliest = deadline
		}
	}
}

func (f *edgeSourceFold) finish() EdgeDetail {
	if f.base != nil {
		f.addSource(*f.base, false)
	}
	f.detail.Found = f.detail.Weight != 0
	return f.detail
}

func decodeSourceOrderKey(physical []byte) (sourceOrderTuple, error) {
	row, err := sourceOrderCodec.Decode(physical)
	if err != nil {
		return row, fmt.Errorf("%w: decode Source order key: %w", ErrInvalidRow, err)
	}
	return row, nil
}

func readOrderedSource(snap *tarp.Snapshot, tuple sourceOrderTuple, charge func(int) error) (Source, error) {
	kind, identity, err := (SourceID{ContribID: tuple.ContribID, Ordinal: tuple.Ordinal}).diskIdentity()
	if err != nil {
		return Source{}, err
	}
	tuple.Edge.Header.Family = sourceFamily
	primary, err := sourceTupleCodec.Encode(sourceTuple{tuple.Edge, kind, identity})
	if err != nil {
		return Source{}, err
	}
	source, found, err := readChargedRow(snap, primary, decodeAdd, charge)
	if err != nil {
		return Source{}, err
	}
	if !found || source.HLC != tuple.HLC {
		return Source{}, fmt.Errorf("%w: Source order does not match primary", ErrInvalidRow)
	}
	return source, nil
}
