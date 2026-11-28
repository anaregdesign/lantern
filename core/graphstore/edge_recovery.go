package graphstore

import (
	"context"
	"fmt"
	"iter"
	"time"

	"github.com/anaregdesign/tarp"
)

// EdgeRecoverySummary describes one retained bucket at a pinned cut.
// Count includes a live base. An Edge may have live Sources and zero weight.
// RetainedAdds counts the physical Add rows, including expired rows.
// Retained summarizes all retained rows, including an expired base.
type EdgeRecoverySummary[K comparable] struct {
	EdgeEntry[K]
	Weight            float32
	Earliest, Latest  time.Time
	Count             int
	RetainedAdds      uint64
	Retained          EdgeSourceStats
	Explicit, Expired bool
}

// EdgeSourceStats summarizes a set of Edge rows, including its base, in
// canonical weight accumulation order.
type EdgeSourceStats struct {
	Weight           float32
	Earliest, Latest time.Time
	Count            int
	Explicit         bool
}

type edgeRecoveryStats struct {
	sources           uint64
	earliest          time.Time
	count             int
	retained          EdgeSourceStats
	explicit, expired bool
}

// EdgeRecoverySummaries streams every Edge header and its live aggregate
// from a marked cut. Exhaust the sequence to verify that every physically
// retained Source is represented exactly once by the order projection.
func (r *Rows[K, V]) EdgeRecoverySummaries(ctx context.Context, snap *tarp.Snapshot, gen uint64, now time.Time) iter.Seq2[EdgeRecoverySummary[K], error] {
	return func(yield func(EdgeRecoverySummary[K], error) bool) {
		var zero EdgeRecoverySummary[K]
		ready, err := hasSourceOrder(snap, gen)
		if err == nil && !ready {
			err = fmt.Errorf("%w: Source order missing at recovery cut", ErrInvalidRow)
		}
		if err != nil {
			yield(zero, err)
			return
		}
		var sources uint64
		var stopped bool
		err = r.walkOrderedEdges(ctx, snap, gen, "", now, true, func(edge EdgeEntry[K], detail EdgeDetail, stats edgeRecoveryStats) bool {
			sources += stats.sources
			stopped = !yield(orderedEdgeSummary(edge, detail, stats), nil)
			return !stopped
		})
		if err == nil && !stopped {
			err = verifyRecoverySourceCount(ctx, snap, gen, sources)
		}
		if err != nil {
			yield(zero, err)
		}
	}
}

func orderedEdgeSummary[K comparable](edge EdgeEntry[K], detail EdgeDetail, stats edgeRecoveryStats) EdgeRecoverySummary[K] {
	return EdgeRecoverySummary[K]{
		EdgeEntry: edge, Weight: detail.Weight, Earliest: stats.earliest, Latest: detail.Expiration,
		Count: stats.count, RetainedAdds: stats.sources, Retained: stats.retained,
		Explicit: stats.explicit, Expired: stats.expired,
	}
}

func scanSourceOrder(ctx context.Context, snap *tarp.Snapshot, bounds scanBounds, verify bool) iter.Seq2[[]byte, error] {
	if !verify {
		return snap.KeysRange(ctx, bounds.lower, bounds.upper)
	}
	return func(yield func([]byte, error) bool) {
		for row, err := range snap.ScanRange(ctx, bounds.lower, bounds.upper) {
			if err == nil && len(row.Value) != 0 {
				err = fmt.Errorf("%w: nonempty Source order key", ErrInvalidRow)
			}
			if !yield(row.Key, err) || err != nil {
				return
			}
		}
	}
}

func verifyRecoverySourceCount(ctx context.Context, snap *tarp.Snapshot, gen, count uint64) error {
	prefix, err := familyPrefix(gen, sourceFamily)
	if err != nil {
		return err
	}
	bounds := prefixBounds(prefix)
	// Every distinct projected key already resolved to its canonical identity
	// and HLC. The counts must agree to rule out uncovered Source rows.
	for _, err := range snap.KeysRange(ctx, bounds.lower, bounds.upper) {
		if err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("%w: Source missing from order projection", ErrInvalidRow)
		}
		count--
	}
	if count != 0 {
		return fmt.Errorf("%w: inconsistent Source order count", ErrInvalidRow)
	}
	return ctx.Err()
}
