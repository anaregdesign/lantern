package graphcache

import (
	"context"
	"slices"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/tarp"
)

// During serving, only identities changed by the in-memory sweep need a
// durable transition. Startup uses the bounded physical scan.
type storageSweep[S comparable] struct {
	vertices []S
	edges    []EdgeKey[S]
	floors   []graphstore.FloorKey[S]
}

// CompleteStorageRecovery makes observed expiry durable in bounded chunks
// and prepares the incoming Edge projection before serving.
func (c *GraphCache[S, T]) CompleteStorageRecovery(ctx context.Context) error {
	if c.storage == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.StorageError(); err != nil {
		return err
	}
	if c.storage.recoveryExpired {
		if err := c.storage.rows.ReclaimExpired(ctx, c.storage.db, c.storage.recoveryAt); err != nil {
			if err != ctx.Err() {
				return c.storage.fence(err)
			}
			return err
		}
		c.storage.recoveryExpired = false
	}
	if err := c.storage.rows.EnsureIncomingEdgeIndex(ctx, c.storage.db); err != nil {
		return err
	}
	if !c.storage.sourceOrderVerified {
		if err := c.storage.rows.EnsureSourceOrder(ctx, c.storage.db); err != nil {
			return err
		}
		c.storage.sourceOrderVerified = true
	}
	return c.storage.db.EnsureTopologyStats(ctx)
}

// SetStorageMaintenance installs the serving owner's mutation exclusion for
// GC before starting Watch. Standalone users serialize mutations themselves.
func (c *GraphCache[S, T]) SetStorageMaintenance(gate func(func() error) error) {
	if c.storage != nil {
		c.hookMu.Lock()
		defer c.hookMu.Unlock()
		c.storage.maintenance = gate
	}
}

func (c *GraphCache[S, T]) bindStorageGCHooks() {
	c.edges.onPruned = func(tailID, headID vertexID) {
		tail, tok := c.edges.resolveID(tailID)
		head, hok := c.edges.resolveID(headID)
		if tok && hok {
			c.storage.pending.edges = append(c.storage.pending.edges, EdgeKey[S]{tail, head})
		}
	}
}

func (c *GraphCache[S, T]) flushStorage(ctx context.Context, now time.Time) error {
	if err := c.StorageError(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	work := c.storage.pending
	apply := func(keys StorageKeys[S], floors []graphstore.FloorKey[S]) error {
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
