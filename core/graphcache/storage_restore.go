package graphcache

import (
	"context"
	"errors"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/search"
)

// RestoreStorage rebuilds the derived graph indexes from one pinned generation.
// The caller owns a fresh, private cache and discards it on any failure.
func (c *GraphCache[S, T]) RestoreStorage(ctx context.Context, rows *graphstore.Rows[S, T], view *graphstore.ReadView) error {
	if c.vertices.Count() != 0 || c.edges.count() != 0 {
		return errors.New("graphcache: recovery requires an empty cache")
	}
	if err := c.bindStorage(rows, view); err != nil {
		return err
	}
	c.BeginSearchIndexRecovery()
	return c.restoreStorage(ctx, rows, view)
}

func (c *GraphCache[S, T]) restoreStorage(ctx context.Context, rows *graphstore.Rows[S, T], view *graphstore.ReadView) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.applicationTime()
	c.storage.pending.observed = now
	for entry, err := range rows.Vertices(ctx, view.Snapshot, view.Generation, "", nil, false) {
		if err != nil {
			return err
		}
		v := entry.Vertex
		if !cache.IsLiveAt(v.Expiration, now) {
			c.storage.pending.vertices = append(c.storage.pending.vertices, entry.Key)
			c.recordVertexCausalBarrierLocked(entry.Key, v.HLC)
			continue
		}
		var zero T
		c.upsertVertexStorageLocked(entry.Key, zero, v.Expiration)
		c.vertices.ReleaseValue(entry.Key)
		c.recordVertexHLCLocked(entry.Key, v.HLC)
	}
	for entry, err := range rows.Edges(ctx, view.Snapshot, view.Generation, "", "", nil, false) {
		if err != nil {
			return err
		}
		expiredRows := false
		bucket := &weight{lastHLC: entry.Edge.HLC, lastOrdinal: entry.Edge.LastOrdinal, needsSort: true}
		if v := entry.Edge.Base; v != nil && !cache.IsLiveAt(v.Expiration, now) {
			expiredRows = true
		}
		if v := entry.Edge.Base; v != nil && cache.IsLiveAt(v.Expiration, now) {
			bucket.values = append(bucket.values, weightValue{value: v.Weight, expiration: v.Expiration, hlc: v.HLC, derivedAggregate: v.DerivedAggregate})
		}
		for s, err := range rows.Sources(ctx, view.Snapshot, view.Generation, entry.Key) {
			if err != nil {
				return err
			}
			if s.ID.Ordinal > entry.Edge.LastOrdinal {
				return graphstore.ErrInvalidRow
			}
			v := s.Source
			if cache.IsLiveAt(v.Expiration, now) {
				bucket.values = append(bucket.values, weightValue{value: v.Weight, expiration: v.Expiration, hlc: v.HLC, contribID: s.ID.ContribID, ordinal: s.ID.Ordinal})
			} else {
				expiredRows = true
			}
		}
		if expiredRows {
			c.storage.pending.edges = append(c.storage.pending.edges, EdgeKey[S]{entry.Key.Tail, entry.Key.Head})
		}
		bucket.flushLockedAt(now)
		if len(bucket.values) == 0 {
			c.recordEdgeCausalBarrierLocked(entry.Key.Tail, entry.Key.Head, bucket.lastHLC)
			continue
		}
		c.installRecoveredBucket(entry.Key, bucket)
		c.storage.bindWeight(entry.Key, bucket)
	}
	for entry, err := range rows.Floors(ctx, view.Snapshot, view.Generation) {
		if err != nil {
			return err
		}
		if !cache.IsLiveAt(entry.Floor.Expiration, now) {
			c.storage.pending.floors = append(c.storage.pending.floors, entry.Key)
			continue
		}
		c.restoreStorageFloor(entry.Key, entry.Floor, now)
	}
	if c.searchIndex == nil {
		return ctx.Err()
	}
	// Traverse the original pin once. Loading every recovered cold vertex
	// individually would acquire a new Tarp snapshot for every document.
	c.searchCommitMu.Lock()
	defer c.searchCommitMu.Unlock()
	return c.searchIndex.RebuildDocuments(func(yield func(S, search.Document, time.Time) error) error {
		for entry, err := range rows.Vertices(ctx, view.Snapshot, view.Generation, "", nil, false) {
			if err != nil {
				return err
			}
			v := entry.Vertex
			if cache.IsLiveAt(v.Expiration, now) {
				if err := yield(entry.Key, c.searchExtract(entry.Key, v.Value), v.Expiration); err != nil {
					return err
				}
			}
		}
		return ctx.Err()
	})
}

func (c *GraphCache[S, T]) installRecoveredBucket(key graphstore.EdgeKey[S], bucket *weight) {
	tail, head := c.edges.internEndpoints(key.Tail, key.Head)
	heads := c.edges.tf[tail]
	if heads == nil {
		heads = make(map[vertexID]*weight)
		c.edges.tf[tail] = heads
	}
	heads[head] = bucket
	c.edges.edgeCount++
	c.edges.df[head]++
	c.onEdgeAddedLocked(true, tail, head, key.Head)
	c.reconcileEdgeCausalUsageLocked(EdgeKey[S]{Tail: key.Tail, Head: key.Head})
}

func (c *GraphCache[S, T]) restoreStorageFloor(k graphstore.FloorKey[S], f graphstore.Floor, now time.Time) {
	switch k.Kind {
	case graphstore.VertexBarrier:
		c.recordVertexCausalBarrierLocked(k.Key, f.HLC)
	case graphstore.EdgeBarrier:
		c.recordEdgeCausalBarrierLocked(k.Tail, k.Head, f.HLC)
	case graphstore.VertexTombstone:
		c.setVertexTombstoneLocked(k.Key, f.HLC, f.Expiration)
	case graphstore.EdgeTombstone:
		c.setEdgeTombstoneLocked(k.Tail, k.Head, f.HLC, f.Expiration)
	case graphstore.ContributionTombstone:
		c.setEdgeContributionTombstoneLocked(EdgeContributionKey[S]{k.Tail, k.Head, k.ContribID}, f.HLC, f.Expiration, now)
	}
}
