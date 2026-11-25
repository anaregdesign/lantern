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

// HasVertexAt checks TTL from the row header without decoding the application
// payload. It is used for keys-only scans and edge endpoint liveness.
func (r *Rows[K, V]) HasVertexAt(snap *tarp.Snapshot, gen uint64, key K, now time.Time) (bool, error) {
	physical, err := r.vertexKey(gen, vertexFamily, key)
	if err != nil {
		return false, err
	}
	expiry, found, err := readRow(snap, physical, func(data []byte) (time.Time, error) {
		deadline, _, err := parseMetadata(data)
		return deadline, err
	})
	return found && cache.IsLiveAt(expiry, now), err
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
		found, err := r.HasVertexAt(snap, gen, endpoint, now)
		if err != nil || !found {
			return result, err
		}
	}
	edge, found, err := r.GetEdge(snap, gen, key)
	if err != nil || !found {
		return result, err
	}
	sources := liveBase(nil, edge, now)
	for entry, err := range r.Sources(ctx, snap, gen, key) {
		if err != nil {
			return result, err
		}
		sources, err = addLiveSource(sources, edge.LastOrdinal, entry, now)
		if err != nil {
			return result, err
		}
	}
	return foldSources(sources), nil
}

func liveBase(scratch []SourceEntry, e Edge, now time.Time) []SourceEntry {
	sources := scratch[:0]
	if e.Base != nil && cache.IsLiveAt(e.Base.Expiration, now) {
		sources = append(sources, SourceEntry{Source: *e.Base})
	}
	return sources
}

func addLiveSource(sources []SourceEntry, lastOrdinal uint64, entry SourceEntry, now time.Time) ([]SourceEntry, error) {
	if entry.ID.Ordinal > lastOrdinal {
		return nil, ErrInvalidRow
	}
	if cache.IsLiveAt(entry.Source.Expiration, now) {
		sources = append(sources, entry)
	}
	return sources, nil
}

func foldSources(sources []SourceEntry) EdgeDetail {
	var result EdgeDetail
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
	return result
}
