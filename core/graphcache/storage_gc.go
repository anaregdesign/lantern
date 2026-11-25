package graphcache

import (
	"context"
	"slices"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/tarp"
)

// Only changed identities, including rows already dead at startup, need a
// durable transition. The active generation and current identities are read
// again under the owner's publication gate before reclaiming anything.
type storageSweep[S comparable] struct {
	vertices []S
	edges    []EdgeKey[S]
	floors   []graphstore.FloorKey[S]
}

// SetStorageMaintenance installs the serving owner's mutation exclusion for
// GC before starting Watch. Standalone users serialize mutations themselves.
func (c *GraphCache[S, T]) SetStorageMaintenance(gate func(func() error) error) {
	if c.storage != nil {
		c.storage.maintenance = gate
	}
}

func (c *GraphCache[S, T]) bindStorageGCHooks() {
	c.edges.onPruned = func(tailID, headID vertexID) {
		if !c.storage.collecting {
			return
		}
		tail, tok := c.edges.resolveID(tailID)
		head, hok := c.edges.resolveID(headID)
		if tok && hok {
			c.storage.pending.edges = append(c.storage.pending.edges, EdgeKey[S]{tail, head})
		}
	}
}

func (c *GraphCache[S, T]) flushStorage(ctx context.Context) error {
	if err := c.StorageError(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	work := c.storage.pending
	apply := func(keys StorageKeys[S], floors []graphstore.FloorKey[S]) error {
		now := c.applicationTime()
		for _, key := range keys.Edges {
			// Materialize just the affected bucket so expired sources are
			// dropped from its postimage by the same fold as the live cache.
			if bucket := c.edges.bucket(key.Tail, key.Head); bucket != nil {
				bucket.mu.Lock()
				loaded := bucket.editLocked(now)
				if loaded {
					bucket.flushLockedAt(now)
				}
				bucket.mu.Unlock()
				if !loaded {
					return c.StorageError()
				}
			}
		}
		err := c.storage.db.UpdateRows(ctx, func(gen uint64, snap *tarp.Snapshot, b graphstore.Writer) error {
			if err := c.storeRowsLocked(ctx, c.storage.rows, snap, b, gen, keys); err != nil {
				return err
			}
			for _, k := range floors {
				active, found, err := c.storage.rows.GetFloor(snap, gen, k)
				if err != nil {
					return err
				}
				// The identity may have been renewed between startup and GC.
				if found && !active.Expiration.IsZero() && !now.Before(active.Expiration) {
					if err := c.storage.rows.DeleteFloor(b, gen, k); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return c.storage.fence(err)
		}
		c.StoragePublished(keys)
		return nil
	}
	for group := range slices.Chunk(work.vertices, 128) {
		if err := apply(StorageKeys[S]{Vertices: group}, nil); err != nil {
			return err
		}
	}
	for group := range slices.Chunk(work.edges, 128) {
		if err := apply(StorageKeys[S]{Edges: group}, nil); err != nil {
			return err
		}
	}
	for group := range slices.Chunk(work.floors, 128) {
		if err := apply(StorageKeys[S]{}, group); err != nil {
			return err
		}
	}
	c.storage.pending = storageSweep[S]{}
	return nil
}
