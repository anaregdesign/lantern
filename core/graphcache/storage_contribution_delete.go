package graphcache

import (
	"context"
	"errors"
	"fmt"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/tarp"
)

type storedContributionDelete[S comparable] struct {
	generation uint64
	buckets    map[EdgeKey[S]]*storedContributionBucket
}

type storedContributionBucket struct {
	header  graphstore.Edge
	present bool
	removed map[graphstore.SourceID]graphstore.Source
}

func (s *stagedEdgeContributionDelete[S, T]) planStoredLocked(ctx context.Context, keys []EdgeContributionKey[S], projected map[EdgeKey[S]]string, hasProjection bool) (err error) {
	c := s.base.cache
	if c.storage == nil {
		return errors.New("graphcache: stored contribution Delete requires a storage binding")
	}
	if err := c.StorageError(); err != nil {
		return err
	}
	view, err := c.storage.db.Read()
	if err != nil {
		return c.storage.fence(err)
	}
	defer func() {
		if closeErr := view.Close(); closeErr != nil {
			err = c.storage.fence(errors.Join(err, closeErr))
		}
	}()
	err = s.prepareStoredBuckets(ctx, view, keys, projected, hasProjection)
	if err != nil && !errors.Is(err, ctx.Err()) {
		return c.storage.fence(err)
	}
	return err
}

func (s *stagedEdgeContributionDelete[S, T]) prepareStoredBuckets(ctx context.Context, view *graphstore.ReadView, keys []EdgeContributionKey[S], projected map[EdgeKey[S]]string, hasProjection bool) error {
	storage := s.base.cache.storage
	s.stored = &storedContributionDelete[S]{generation: view.Generation, buckets: make(map[EdgeKey[S]]*storedContributionBucket)}
	for index, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		allowed, err := s.storedDeleteAllowed(view, key)
		if err != nil {
			return err
		}
		if !allowed {
			continue
		}
		s.accepted = append(s.accepted, IndexedEdgeContributionDelete[S]{Index: index, Key: key})
		pair := EdgeKey[S]{Tail: key.Tail, Head: key.Head}
		physical := graphstore.EdgeKey[S](pair)
		bucket := s.stored.buckets[pair]
		if bucket == nil {
			header, present, err := storage.rows.GetEdge(view.Snapshot, view.Generation, physical)
			if err != nil {
				return err
			}
			resident := s.base.cache.edges.bucket(pair.Tail, pair.Head)
			if present != (resident != nil) || present && !matchesStoredDeleteHeader(resident, header) {
				return fmt.Errorf("%w: committed contribution bucket differs from the cache", graphstore.ErrInvalidRow)
			}
			bucket = &storedContributionBucket{header: header, present: present}
			s.stored.buckets[pair] = bucket
		}
		if !bucket.present {
			continue
		}
		id := graphstore.SourceID{ContribID: key.ContribID}
		if _, removed := bucket.removed[id]; removed {
			continue
		}
		source, found, err := storage.rows.GetSource(view.Snapshot, view.Generation, physical, id)
		if err != nil {
			return err
		}
		if found {
			if bucket.removed == nil {
				bucket.removed = make(map[graphstore.SourceID]graphstore.Source)
			}
			bucket.removed[id] = source
			s.base.outcomes[index] = cache.IsLiveAt(source.Expiration, s.now)
		}
	}
	for pair, bucket := range s.stored.buckets {
		if len(bucket.removed) == 0 {
			continue
		}
		plan, err := s.planBucketLocked(pair, projected[pair], hasProjection)
		if err != nil {
			return err
		}
		excluded := make(map[graphstore.SourceID]struct{}, len(bucket.removed))
		for id := range bucket.removed {
			excluded[id] = struct{}{}
		}
		physical := graphstore.EdgeKey[S](pair)
		summary, err := storage.rows.SummarizeEdgeSources(ctx, view.Snapshot, view.Generation, physical, bucket.header, s.now, excluded)
		if err != nil {
			return err
		}
		if retained := summary.Retained; retained.Count != 0 {
			// A cold descriptor describes the retained physical rows. The
			// normal TTL read and GC paths handle siblings that already expired.
			plan.after = &weight{
				lastHLC: bucket.header.HLC, lastOrdinal: bucket.header.LastOrdinal,
				sum: retained.Weight, minExp: retained.Earliest, lastFlushLen: retained.Count,
				storedSum: retained.Weight, storedCount: retained.Count, storedExpiry: retained.Earliest,
				storedLatest: retained.Latest, storedExplicit: retained.Explicit,
			}
			storage.bindWeightLoader(physical, plan.after)
		}
		s.base.plans = append(s.base.plans, plan)
	}
	return ctx.Err()
}

func matchesStoredDeleteHeader(bucket *weight, header graphstore.Edge) bool {
	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	return bucket.cold && bucket.lastHLC == header.HLC && bucket.lastOrdinal == header.LastOrdinal
}

func (s *stagedEdgeContributionDelete[S, T]) storedDeleteAllowed(view *graphstore.ReadView, key EdgeContributionKey[S]) (bool, error) {
	c := s.base.cache
	floor, found, err := c.storage.rows.GetFloor(view.Snapshot, view.Generation, contributionDeleteFloor(key))
	if err != nil {
		return false, err
	}
	live := found && !floor.Expiration.IsZero() && s.now.Before(floor.Expiration)
	resident := c.edgeContributionTombstones[key]
	residentLive := !resident.expiration.IsZero() && s.now.Before(resident.expiration)
	if live != residentLive || live && (floor.HLC != resident.ts || !floor.Expiration.Equal(resident.expiration)) {
		return false, fmt.Errorf("%w: committed contribution tombstone differs from the cache", graphstore.ErrInvalidRow)
	}
	return !live || !s.base.ts.Less(floor.HLC), nil
}

func contributionDeleteFloor[S comparable](key EdgeContributionKey[S]) graphstore.FloorKey[S] {
	return graphstore.FloorKey[S]{Kind: graphstore.ContributionTombstone, Tail: key.Tail, Head: key.Head, ContribID: key.ContribID}
}

func (s *stagedEdgeContributionDelete[S, T]) storeContributionRows(ctx context.Context, rows *graphstore.Rows[S, T], snapshot *tarp.Snapshot, batch graphstore.Writer, generation uint64) error {
	// The serving publication gate excludes intervening graph writes. Search
	// can advance the application revision with service rows on the same cut.
	if generation != s.stored.generation {
		return fmt.Errorf("%w: contribution Delete crossed a graph generation", graphstore.ErrInvalidRow)
	}
	if err := s.base.cache.StorageError(); err != nil {
		return err
	}
	for key, before := range s.original {
		if err := ctx.Err(); err != nil {
			return err
		}
		after, present := s.base.cache.edgeContributionTombstones[key]
		if present == before.set && after.ts == before.value.ts && after.expiration.Equal(before.value.expiration) {
			continue
		}
		if err := storeFloor(rows, batch, generation, contributionDeleteFloor(key), after); err != nil {
			return err
		}
	}
	for _, plan := range s.base.plans {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := graphstore.EdgeKey[S](plan.key)
		bucket := s.stored.buckets[plan.key]
		header, present, err := rows.GetEdge(snapshot, generation, key)
		if err != nil {
			return err
		}
		if !present || !sameContributionDeleteHeader(header, bucket.header) {
			return fmt.Errorf("%w: contribution Delete header changed before publication", graphstore.ErrInvalidRow)
		}
		for id, original := range bucket.removed {
			current, found, err := rows.GetSource(snapshot, generation, key, id)
			if err != nil {
				return err
			}
			if !found || !sameStoredSource(original, current) {
				return fmt.Errorf("%w: contribution Delete source changed before publication", graphstore.ErrInvalidRow)
			}
			if err := rows.ReplaceSource(batch, generation, key, id, &original, nil); err != nil {
				return err
			}
		}
		if plan.after == nil {
			if err := rows.DeleteEdge(batch, generation, key); err != nil {
				return err
			}
			barrier := graphstore.FloorKey[S]{Kind: graphstore.EdgeBarrier, Tail: key.Tail, Head: key.Head}
			if err := storeFloor(rows, batch, generation, barrier, tombstoneEntry{ts: s.base.cache.edgeCausalBarriers[plan.key]}); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func sameContributionDeleteHeader(a, b graphstore.Edge) bool {
	return a.HLC == b.HLC && a.LastOrdinal == b.LastOrdinal &&
		(a.Base == nil && b.Base == nil || a.Base != nil && b.Base != nil && sameStoredSource(*a.Base, *b.Base))
}
