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
	if c.searchIndex == nil {
		return c.restoreStorageRows(ctx, rows, view, now, nil)
	}
	c.searchCommitMu.Lock()
	defer c.searchCommitMu.Unlock()
	return c.searchIndex.RebuildDocuments(func(yield func(S, search.Document, time.Time) error) error {
		return c.restoreStorageRows(ctx, rows, view, now, yield)
	})
}

// Build the Vertex descriptors and search documents from the same decoded
// payload, then verify the Edge/Source and floor families before publishing
// the new search index.
func (c *GraphCache[S, T]) restoreStorageRows(ctx context.Context, rows *graphstore.Rows[S, T], view *graphstore.ReadView, now time.Time, indexDocument func(S, search.Document, time.Time) error) error {
	c.storage.recoveryAt = now
	for entry, err := range rows.VerifiedVertices(ctx, view.Snapshot, view.Generation) {
		if err != nil {
			return err
		}
		v := entry.Vertex
		if !cache.IsLiveAt(v.Expiration, now) {
			c.storage.recoveryExpired = true
			c.recordVertexCausalBarrierLocked(entry.Key, v.HLC)
			continue
		}
		var zero T
		c.upsertVertexStorageLocked(entry.Key, zero, v.Expiration)
		c.vertices.ReleaseValue(entry.Key)
		c.recordVertexHLCLocked(entry.Key, v.HLC)
		if indexDocument != nil {
			if err := indexDocument(entry.Key, c.searchExtract(entry.Key, v.Value), v.Expiration); err != nil {
				return err
			}
		}
	}
	if err := c.restoreStorageEdges(ctx, rows, view, now); err != nil {
		return err
	}
	for entry, err := range rows.Floors(ctx, view.Snapshot, view.Generation) {
		if err != nil {
			return err
		}
		if !cache.IsLiveAt(entry.Floor.Expiration, now) {
			c.storage.recoveryExpired = true
			continue
		}
		c.restoreStorageFloor(entry.Key, entry.Floor, now)
	}
	return ctx.Err()
}

func (c *GraphCache[S, T]) restoreStorageEdges(ctx context.Context, rows *graphstore.Rows[S, T], view *graphstore.ReadView, now time.Time) error {
	ordered, err := rows.HasSourceOrder(view.Snapshot, view.Generation)
	if err != nil {
		return err
	}
	if !ordered {
		return c.restoreLegacyStorageEdges(ctx, rows, view, now)
	}
	for entry, err := range rows.EdgeRecoverySummaries(ctx, view.Snapshot, view.Generation, now) {
		if err != nil {
			return err
		}
		if entry.Count == 0 {
			c.storage.recoveryExpired = true
			c.recordEdgeCausalBarrierLocked(entry.Key.Tail, entry.Key.Head, entry.Edge.HLC)
			continue
		}
		c.storage.recoveryExpired = c.storage.recoveryExpired || entry.Expired
		bucket := &weight{
			lastHLC: entry.Edge.HLC, lastOrdinal: entry.Edge.LastOrdinal,
			sum: entry.Weight, minExp: entry.Earliest, lastFlushLen: entry.Count,
			storedSum: entry.Weight, storedCount: entry.Count, storedExpiry: entry.Earliest,
			storedLatest: entry.Latest, storedExplicit: entry.Explicit,
		}
		c.storage.bindWeightLoader(entry.Key, bucket)
		c.installRecoveredBucket(entry.Key, bucket)
	}
	c.storage.sourceOrderVerified = true
	return nil
}

// An already pinned legacy cut cannot observe a new projection. Retain the
// original recovery path for these callers. Serving builds before pinning.
func (c *GraphCache[S, T]) restoreLegacyStorageEdges(ctx context.Context, rows *graphstore.Rows[S, T], view *graphstore.ReadView, now time.Time) error {
	for entry, err := range rows.EdgeBuckets(ctx, view.Snapshot, view.Generation, "") {
		if err != nil {
			return err
		}
		bucket := &weight{lastHLC: entry.Edge.HLC, lastOrdinal: entry.Edge.LastOrdinal, needsSort: true}
		if v := entry.Edge.Base; v != nil {
			if cache.IsLiveAt(v.Expiration, now) {
				bucket.values = append(bucket.values, weightValue{value: v.Weight, expiration: v.Expiration, hlc: v.HLC, derivedAggregate: v.DerivedAggregate})
			} else {
				c.storage.recoveryExpired = true
			}
		}
		for _, s := range entry.Sources {
			v := s.Source
			if cache.IsLiveAt(v.Expiration, now) {
				bucket.values = append(bucket.values, weightValue{value: v.Weight, expiration: v.Expiration, hlc: v.HLC, contribID: s.ID.ContribID, ordinal: s.ID.Ordinal})
			} else {
				c.storage.recoveryExpired = true
			}
		}
		bucket.flushLockedAt(now)
		if len(bucket.values) == 0 {
			c.storage.recoveryExpired = true
			c.recordEdgeCausalBarrierLocked(entry.Key.Tail, entry.Key.Head, bucket.lastHLC)
			continue
		}
		c.installRecoveredBucket(entry.Key, bucket)
		c.storage.bindWeight(entry.Key, bucket)
	}
	return ctx.Err()
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
