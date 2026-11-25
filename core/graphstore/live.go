package graphstore

import (
	"bytes"
	"context"
	"slices"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/tarp"
)

// VertexAt reads through one pinned cut, keeping a live nil value distinct
// from an absent or expired vertex.
func (r *Rows[K, V]) VertexAt(snap *tarp.Snapshot, generation uint64, key K, now time.Time) (V, bool, error) {
	var zero V
	v, found, err := r.GetVertex(snap, generation, key)
	if err != nil || !found || !cache.IsLiveAt(v.Expiration, now) {
		return zero, false, err
	}
	return v.Value, true, nil
}

type EdgeDetail struct {
	Weight     float32
	Expiration time.Time
	Found      bool
}

// EdgeAt folds only this bucket's live sources and requires both endpoints in
// the same snapshot. Replicated weights sum in deterministic HLC/identity
// order; zero-HLC, zero-ID local events retain their insertion order.
func (r *Rows[K, V]) EdgeAt(ctx context.Context, snap *tarp.Snapshot, gen uint64, key EdgeKey[K], now time.Time) (EdgeDetail, error) {
	var result EdgeDetail
	if err := ctx.Err(); err != nil {
		return result, err
	}
	for _, endpoint := range []K{key.Tail, key.Head} {
		_, found, err := r.VertexAt(snap, gen, endpoint, now)
		if err != nil || !found {
			return result, err
		}
	}
	edge, found, err := r.GetEdge(snap, gen, key)
	if err != nil || !found {
		return result, err
	}
	var sources []SourceEntry
	if edge.Base != nil && cache.IsLiveAt(edge.Base.Expiration, now) {
		sources = append(sources, SourceEntry{Source: *edge.Base})
	}
	for entry, err := range r.Sources(ctx, snap, gen, key) {
		if err != nil {
			return result, err
		}
		if cache.IsLiveAt(entry.Source.Expiration, now) {
			sources = append(sources, entry)
		}
	}
	slices.SortStableFunc(sources, func(a, b SourceEntry) int {
		if a.Source.HLC.Less(b.Source.HLC) {
			return -1
		}
		if b.Source.HLC.Less(a.Source.HLC) {
			return 1
		}
		return bytes.Compare(a.ID.ContribID[:], b.ID.ContribID[:])
	})
	for _, entry := range sources {
		result.Weight += entry.Source.Weight
		if entry.Source.Expiration.After(result.Expiration) {
			result.Expiration = entry.Source.Expiration
		}
	}
	result.Found = result.Weight != 0
	return result, nil
}
