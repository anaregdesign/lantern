package graphcache

import (
	"context"
	"errors"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

type vertexPreimageReader[S comparable] func() (map[S]storedVertexBefore, error)

func (r vertexPreimageReader[S]) read() (map[S]storedVertexBefore, error) {
	if r == nil {
		return nil, nil
	}
	return r()
}

// PutVerticesFromCommittedCut admits local Put items using the active Tarp
// projection. The serving publisher must hold its write gate and have no
// graph effects awaiting WAL repair. This changes only preimage authority.
func (c *GraphCache[S, T]) PutVerticesFromCommittedCut(ctx context.Context, items []VertexItem[S, T], ts hlc.Timestamp, ifAbsent bool) ([]PutOutcome, error) {
	outcomes := make([]PutOutcome, len(items))
	readBefore := c.vertexPreimageReader(ctx, vertexItemKeys(items))
	if ifAbsent {
		_, _, err := c.putVerticesIfAbsentChecked(items, ts, true, nil, outcomes, readBefore)
		return outcomes, err
	}
	_, err := c.putVerticesWithExpirationHLC(items, ts, true, outcomes, readBefore)
	return outcomes, err
}

// ApplyVertexPutsFromCommittedCut applies already sequenced peer effects
// using the active Tarp projection and the existing replica admission and
// search behavior. It requires the same clean write cut as a local Put.
func (c *GraphCache[S, T]) ApplyVertexPutsFromCommittedCut(ctx context.Context, items []VertexItem[S, T], ts hlc.Timestamp) ([]PutOutcome, error) {
	outcomes := make([]PutOutcome, len(items))
	_, err := c.putVerticesWithExpirationHLC(items, ts, false, outcomes, c.vertexPreimageReader(ctx, vertexItemKeys(items)))
	return outcomes, err
}

// DeleteVerticesFromCommittedCut plans exact HLC deletes on a clean Tarp
// cut. Replicated effects bypass local admission limits as on the RAM path.
func (c *GraphCache[S, T]) DeleteVerticesFromCommittedCut(ctx context.Context, keys []S, ts hlc.Timestamp, expiration time.Time, local bool) ([]bool, []int, error) {
	_, existed, accepted, err := c.deleteVerticesHLC(keys, ts, expiration, local, false, true, c.vertexPreimageReader(ctx, keys))
	return existed, accepted, err
}

func (c *GraphCache[S, T]) vertexPreimageReader(ctx context.Context, keys []S) vertexPreimageReader[S] {
	return func() (map[S]storedVertexBefore, error) {
		if c.storage == nil {
			return nil, errors.New("graphcache: committed Vertex planning requires bound storage")
		}
		return c.storedVertexPreimagesLocked(ctx, keys)
	}
}

type storedVertexBefore struct {
	meta      graphstore.VertexMetadata
	present   bool
	barrier   graphstore.Floor
	tombstone graphstore.Floor
}

func (b storedVertexBefore) writeAllowed(ts hlc.Timestamp, wall time.Time) bool {
	zero := hlc.Timestamp{}
	if b.present && b.meta.HLC != zero && ts.Less(b.meta.HLC) {
		return false
	}
	if b.barrier.HLC != zero && ts.Less(b.barrier.HLC) {
		return false
	}
	return b.tombstone.HLC == zero || !cache.IsLiveAt(b.tombstone.Expiration, wall) || !ts.Less(b.tombstone.HLC)
}

func (b storedVertexBefore) lifetime(now time.Time) lifetimeBefore {
	return lifetimeBefore{expiration: b.meta.Expiration, live: b.present && cache.IsLiveAt(b.meta.Expiration, now)}
}

// Read only the touched Vertex metadata and floors from one committed cut.
// The caller holds the graph lock and the serving publisher serializes
// durable graph changes. Search preparation may release the graph lock and
// then retry against a new cut.
func (c *GraphCache[S, T]) storedVertexPreimagesLocked(ctx context.Context, keys []S) (map[S]storedVertexBefore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.storage == nil || len(keys) == 0 {
		return nil, nil
	}
	if err := c.StorageError(); err != nil {
		return nil, err
	}
	before := make(map[S]storedVertexBefore, len(keys))
	err := c.storage.db.WithCommittedSnapshot(func(gen uint64, snap *tarp.Snapshot) error {
		for _, key := range keys {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, ok := before[key]; ok {
				continue
			}
			meta, present, err := c.storage.rows.GetVertexMetadata(snap, gen, key)
			if err != nil {
				return err
			}
			withoutFloors := storedVertexBefore{meta: meta, present: present}
			entry, err := c.storage.readVertexFloors(snap, gen, key, withoutFloors)
			if err != nil {
				return err
			}
			before[key] = entry
		}
		return nil
	})
	if err != nil && err != ctx.Err() {
		return nil, c.storage.fence(err)
	}
	return before, ctx.Err()
}

func (s *storageResidency[S, T]) readVertexFloors(snap *tarp.Snapshot, gen uint64, key S, before storedVertexBefore) (storedVertexBefore, error) {
	barrier, _, err := s.rows.GetFloor(snap, gen, graphstore.FloorKey[S]{Kind: graphstore.VertexBarrier, Key: key})
	if err != nil {
		return storedVertexBefore{}, err
	}
	tombstone, _, err := s.rows.GetFloor(snap, gen, graphstore.FloorKey[S]{Kind: graphstore.VertexTombstone, Key: key})
	if err != nil {
		return storedVertexBefore{}, err
	}
	before.barrier, before.tombstone = barrier, tombstone
	return before, nil
}

func (c *GraphCache[S, T]) vertexLifetimeReaderLocked(before map[S]storedVertexBefore, now time.Time) func(S) lifetimeBefore {
	if before != nil {
		return func(key S) lifetimeBefore { return before[key].lifetime(now) }
	}
	return func(key S) lifetimeBefore {
		_, expiration, present, _ := c.vertices.PeekResident(key)
		return lifetimeBefore{expiration: expiration, live: present && cache.IsLiveAt(expiration, now)}
	}
}

func (c *GraphCache[S, T]) vertexWriteAdmissionLocked(ts hlc.Timestamp, before map[S]storedVertexBefore) func(S) bool {
	if before == nil {
		return func(key S) bool { return c.vertexWriteAllowedLocked(key, ts) }
	}
	wall := time.Now()
	return func(key S) bool { return before[key].writeAllowed(ts, wall) }
}
