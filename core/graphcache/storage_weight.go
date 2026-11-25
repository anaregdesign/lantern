package graphcache

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/tarp"
)

// bindWeight installs a cold bucket descriptor after its exact contents have
// been committed. Keep its Put floor, count and aggregate metadata, which
// support causal checks and preflight budgets without loading a source.
func (s *storageResidency[S, T]) bindWeight(key graphstore.EdgeKey[S], w *weight) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cold {
		return
	}
	w.storedCount = len(w.values)
	w.storedZero = w.sum == 0
	w.storedExplicit = false
	w.storedExpiry = time.Time{}
	for _, v := range w.values {
		w.storedExplicit = w.storedExplicit || !v.contribID.IsZero()
		if !v.expiration.IsZero() && (w.storedExpiry.IsZero() || v.expiration.Before(w.storedExpiry)) {
			w.storedExpiry = v.expiration
		}
	}
	w.cold = true
	w.values = nil
	if w.load != nil {
		return
	}
	w.load = func() ([]weightValue, error) {
		var values []weightValue
		err := s.db.WithCommittedSnapshot(func(g uint64, snap *tarp.Snapshot) error {
			e, found, err := s.rows.GetEdge(snap, g, key)
			if err != nil {
				return err
			}
			if !found {
				return graphstore.ErrInvalidRow
			}
			if base := e.Base; base != nil {
				values = append(values, weightValue{value: base.Weight, expiration: base.Expiration, hlc: base.HLC, derivedAggregate: base.DerivedAggregate})
			}
			for row, err := range s.rows.Sources(context.Background(), snap, g, key) {
				if err != nil {
					return err
				}
				if row.ID.Ordinal > e.LastOrdinal {
					return graphstore.ErrInvalidRow
				}
				src := row.Source
				values = append(values, weightValue{value: src.Weight, expiration: src.Expiration, hlc: src.HLC, contribID: row.ID.ContribID, ordinal: row.ID.Ordinal})
			}
			return nil
		})
		if err != nil {
			return nil, s.fence(err)
		}
		return values, nil
	}
}

// A read borrows one bucket for its duration. A writer retains the loaded
// rows until the graph/relay commit succeeds or its staged candidate aborts.
func (w *weight) loadLocked(now time.Time) bool {
	if !w.cold || w.values != nil {
		return true
	}
	values, err := w.load()
	if err != nil {
		return false
	}
	w.values = values
	w.needsSort = true
	w.flushLockedAt(now)
	return true
}

func (w *weight) releaseReadLocked() {
	if w.cold {
		w.values = nil
	}
}

func (w *weight) editLocked(now time.Time) bool {
	if !w.loadLocked(now) {
		return false
	}
	w.cold = false
	return true
}
