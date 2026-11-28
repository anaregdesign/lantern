package graphcache

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
	"github.com/anaregdesign/tarp"
)

// During serving, only identities changed by the in-memory sweep need a
// durable transition. Startup uses the bounded physical scan.
type storageSweep[S comparable] struct {
	vertices []S
	edges    []EdgeKey[S]
	floors   []graphstore.FloorKey[S]
}

type storageStructuralCursor[S comparable] struct {
	generation uint64
	after      graphstore.EdgeKey[S]
	hasAfter   bool
	remaining  int
}

type structuralEdgeExpiry uint8

const (
	structuralEdgeLive structuralEdgeExpiry = iota
	structuralEdgeZero
	structuralEdgeDangling
)

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
			var reclaimedDeadline time.Time
			for _, key := range keys.Vertices {
				vertex, found, err := c.storage.rows.GetVertexMetadata(snap, gen, key)
				if err != nil {
					return err
				}
				if found && !vertex.Expiration.After(now) && vertex.Expiration.After(reclaimedDeadline) {
					reclaimedDeadline = vertex.Expiration
				}
			}
			if !reclaimedDeadline.IsZero() {
				if err := graphstore.StageVertexExpiryWatermark(ctx, snap, b, gen, reclaimedDeadline); err != nil {
					return err
				}
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

// reclaimOnlineStorage scans one pinned page at a time without the Server
// publication gate. Only pending commits take the gate, and a foreground
// mutation may run between any two commits. A cache descriptor always
// reflects a completed durable commit before the next writer can enter.
func (c *GraphCache[S, T]) reclaimOnlineStorage(ctx context.Context, maintenance func(func() error) error, now time.Time) (vertices, edges, dangling int, err error) {
	c.mu.Lock()
	vertexHLCBefore := len(c.vertexHLC)
	c.vertexHLCHighWater = max(c.vertexHLCHighWater, vertexHLCBefore)
	c.mu.Unlock()
	var cursor graphstore.ExpiryCursor
	var stats GCSweepStats
	for !cursor.Done() {
		plan, err := c.storage.rows.DiscoverExpiryChunk(ctx, c.storage.db, now, cursor)
		if err != nil {
			return vertices, edges, dangling, c.onlineStorageError(ctx, err)
		}
		if !plan.Pending() {
			cursor = plan.Next()
			continue
		}
		err = maintenance(func() error {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.publicationGate != nil {
				c.publicationGate.Lock()
				defer c.publicationGate.Unlock()
			}
			if c.searchIndex != nil {
				c.searchCommitMu.Lock()
				defer c.searchCommitMu.Unlock()
			}
			if fault := c.StorageError(); fault != nil {
				return fault
			}
			next, effects, err := c.storage.rows.ReclaimExpiredChunk(ctx, c.storage.db, now, plan)
			if err != nil {
				return c.onlineStorageError(ctx, err)
			}
			removedEdges, err := c.installExpiryEffectsLocked(effects)
			if err != nil {
				return c.storage.fence(err)
			}
			vertices += len(effects.Vertices)
			edges += removedEdges
			stats.ExpiredContributions += len(effects.Sources)
			for _, edge := range effects.Edges {
				if edge.Before.Base != nil {
					stats.ExpiredContributions++
				}
			}
			cursor = next
			return nil
		})
		if err != nil {
			return vertices, edges, dangling, err
		}
	}
	if vertices > 0 {
		c.mu.RLock()
		index := c.searchIndex
		c.mu.RUnlock()
		if index != nil && index.Health() != search.IndexHealthy {
			_ = c.RebuildSearchIndex()
		}
	}
	zero, dangling, err := c.reclaimStoredStructuralEdges(ctx, maintenance, now, &stats)
	c.mu.Lock()
	c.shrinkVertexHLCLocked(vertexHLCBefore)
	c.edgeTombstoneDeadlines.shrink()
	c.edgeContributionDeadlines.shrink()
	stats.ZeroRemoved, stats.DanglingRemoved = edges+zero, dangling
	c.gcLastSweep = stats
	c.mu.Unlock()
	return vertices, edges + zero, dangling, err
}

// The ordinary TTL pass above can remove an individual Source. Structural
// deletion removes the header and every surviving Source of one Edge together.
// Discover that work on a read cut and repeat the exact predicate on the writer
// cut so a resurrected endpoint saves the whole bucket.
func (c *GraphCache[S, T]) reclaimStoredStructuralEdges(ctx context.Context, maintenance func(func() error) error, now time.Time, stats *GCSweepStats) (zero, dangling int, err error) {
	c.mu.RLock()
	cursor := c.storage.structuralCursor
	budget, retainDangling := c.gcEdgeBudget, c.retainDanglingEdgeHistory
	c.mu.RUnlock()
	if budget == 0 {
		cursor = storageStructuralCursor[S]{}
	}
	var tail S
	var haveTail bool
	for {
		var pending, moreRows, budgetReached bool
		var candidate graphstore.EdgeKey[S]
		err := c.storage.db.WithCommittedSnapshot(func(gen uint64, snap *tarp.Snapshot) error {
			if gen != cursor.generation {
				c.mu.RLock()
				c.edges.mu.RLock()
				cursor = storageStructuralCursor[S]{generation: gen, remaining: len(c.edges.tf)}
				c.edges.mu.RUnlock()
				c.mu.RUnlock()
				haveTail = false
				stats.ScannedTails = 0
			}
			var after *graphstore.EdgeKey[S]
			if cursor.hasAfter {
				after = &cursor.after
			}
			pageSize := 0
			for entry, err := range c.storage.rows.Edges(ctx, snap, gen, "", "", after, false) {
				if err != nil {
					return err
				}
				if !haveTail || entry.Key.Tail != tail {
					if budget > 0 && stats.ScannedTails == budget {
						budgetReached = true
						return nil
					}
					tail, haveTail = entry.Key.Tail, true
					stats.ScannedTails++
					cursor.remaining = max(0, cursor.remaining-1)
				}
				stats.ScannedEdges++
				cursor.after, cursor.hasAfter = entry.Key, true
				reason, err := c.storedStructuralReason(ctx, snap, gen, entry, now, retainDangling, true)
				if err != nil {
					return err
				}
				if reason != structuralEdgeLive {
					candidate, pending = entry.Key, true
					break
				}
				pageSize++
				if pageSize == 128 {
					moreRows = true
					break
				}
			}
			return nil
		})
		if err != nil {
			return zero, dangling, c.onlineStorageError(ctx, err)
		}
		if moreRows {
			continue
		}
		if !pending {
			if !budgetReached {
				cursor = storageStructuralCursor[S]{}
			}
			c.mu.Lock()
			if c.gcEdgeBudget > 0 {
				c.storage.structuralCursor = cursor
				stats.BacklogTails = cursor.remaining
			}
			c.mu.Unlock()
			return zero, dangling, nil
		}
		err = maintenance(func() error {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.publicationGate != nil {
				c.publicationGate.Lock()
				defer c.publicationGate.Unlock()
			}
			if fault := c.StorageError(); fault != nil {
				return fault
			}
			reason, err := c.pruneStoredStructuralEdge(ctx, cursor.generation, candidate, now)
			switch reason {
			case structuralEdgeZero:
				zero++
			case structuralEdgeDangling:
				dangling++
			}
			return err
		})
		if err != nil {
			return zero, dangling, err
		}
	}
}

func (c *GraphCache[S, T]) storedStructuralReason(ctx context.Context, snap *tarp.Snapshot, gen uint64, entry graphstore.EdgeEntry[S], now time.Time, retainDangling, useMirror bool) (structuralEdgeExpiry, error) {
	var zero, known bool
	if useMirror {
		c.mu.RLock()
		w := c.edges.bucket(entry.Key.Tail, entry.Key.Head)
		c.mu.RUnlock()
		if w != nil {
			w.mu.Lock()
			known = w.cold && !w.storedSummaryInvalid && (w.storedExpiry.IsZero() || now.Before(w.storedExpiry))
			zero = w.storedSum == 0 && !w.storedExplicit
			w.mu.Unlock()
		}
	}
	if !known {
		summary, err := c.storage.rows.SummarizeEdgeSources(ctx, snap, gen, entry.Key, entry.Edge, now, nil)
		if err != nil {
			return structuralEdgeLive, err
		}
		zero = summary.Weight == 0 && !summary.Explicit
	}
	if zero {
		return structuralEdgeZero, nil
	}
	if !retainDangling {
		for _, endpoint := range [...]S{entry.Key.Tail, entry.Key.Head} {
			live, err := c.storage.rows.HasVertexAt(snap, gen, endpoint, now)
			if err != nil {
				return structuralEdgeLive, err
			}
			if !live {
				return structuralEdgeDangling, nil
			}
		}
	}
	return structuralEdgeLive, nil
}

// Caller owns the publication gate and c.mu.
func (c *GraphCache[S, T]) pruneStoredStructuralEdge(ctx context.Context, generation uint64, key graphstore.EdgeKey[S], now time.Time) (structuralEdgeExpiry, error) {
	var reason structuralEdgeExpiry
	var stamp hlc.Timestamp
	rows := c.storage.rows
	err := c.storage.db.UpdateRows(ctx, func(gen uint64, snap *tarp.Snapshot, b graphstore.Writer) error {
		if gen != generation {
			return nil
		}
		edge, found, err := rows.GetEdge(snap, gen, key)
		if err != nil || !found {
			return err
		}
		reason, err = c.storedStructuralReason(ctx, snap, gen, graphstore.EdgeEntry[S]{Key: key, Edge: edge}, now, c.retainDanglingEdgeHistory, false)
		if err != nil || reason == structuralEdgeLive {
			return err
		}
		if err := rows.DeleteSources(ctx, snap, b, gen, key); err != nil {
			return err
		}
		if err := rows.DeleteEdge(b, gen, key); err != nil {
			return err
		}
		stamp = edge.HLC
		if stamp == (hlc.Timestamp{}) {
			return nil
		}
		floorKey := graphstore.FloorKey[S]{Kind: graphstore.EdgeBarrier, Tail: key.Tail, Head: key.Head}
		floor, found, err := rows.GetFloor(snap, gen, floorKey)
		if err != nil || found && !floor.HLC.Less(stamp) {
			return err
		}
		return rows.PutFloor(b, gen, floorKey, graphstore.Floor{HLC: stamp})
	})
	if err != nil {
		return structuralEdgeLive, c.onlineStorageError(ctx, err)
	}
	if reason != structuralEdgeLive {
		c.removeStoredEdgeLocked(key, stamp)
	}
	return reason, nil
}

func (c *GraphCache[S, T]) onlineStorageError(ctx context.Context, err error) error {
	var op *tarp.OpError
	if errors.Is(err, tarp.ErrUnhealthy) || errors.Is(err, tarp.ErrCommitUncertain) ||
		errors.As(err, &op) && op.Outcome != tarp.NotApplied {
		return c.storage.fence(err)
	}
	if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
		return err
	}
	// Key-order compaction can briefly reject a staged whole-Edge update.
	// The next tick may safely retry a definite admission failure.
	if op != nil && errors.Is(err, tarp.ErrBusy) {
		return err
	}
	return c.storage.fence(err)
}

func (c *GraphCache[S, T]) installExpiryEffectsLocked(effects graphstore.ExpiryEffects[S]) (int, error) {
	for _, v := range effects.Vertices {
		c.applyVertexCausalBarrierLocked(v.Key, v.Metadata.HLC)
	}
	for _, s := range effects.Sources {
		if err := c.invalidateStoredExpirySource(s.Key); err != nil {
			return 0, err
		}
	}
	removed := 0
	for _, edge := range effects.Edges {
		if edge.Removed {
			c.removeStoredEdgeLocked(edge.Key, edge.Before.HLC)
			removed++
		} else if err := c.invalidateStoredExpirySource(edge.Key); err != nil {
			return removed, err
		}
	}
	for _, floor := range effects.Floors {
		key := floor.Key
		switch key.Kind {
		case graphstore.VertexTombstone:
			c.clearVertexTombstoneLocked(key.Key)
		case graphstore.EdgeTombstone:
			c.clearEdgeTombstoneLocked(key.Tail, key.Head)
		case graphstore.ContributionTombstone:
			c.clearEdgeContributionTombstoneLocked(EdgeContributionKey[S]{Tail: key.Tail, Head: key.Head, ContribID: key.ContribID})
		}
	}
	return removed, nil
}

// Once one Source is removed, the old aggregate may be wrong even at an
// earlier wall time. Keep the exact retained count, but force summary reads
// onto the ordered disk fold until a writer installs a new descriptor.
func (c *GraphCache[S, T]) invalidateStoredExpirySource(key graphstore.EdgeKey[S]) error {
	bucket := c.edges.bucket(key.Tail, key.Head)
	if bucket == nil {
		return fmt.Errorf("%w: expired Source without a resident Edge", graphstore.ErrInvalidRow)
	}
	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	if !bucket.cold || bucket.storedCount < 1 {
		return fmt.Errorf("%w: expired Source disagrees with retained count", graphstore.ErrInvalidRow)
	}
	bucket.storedCount--
	bucket.storedSummaryInvalid = true
	return nil
}

func (c *GraphCache[S, T]) removeStoredEdgeLocked(key graphstore.EdgeKey[S], stamp hlc.Timestamp) {
	c.recordEdgeCausalBarrierLocked(key.Tail, key.Head, stamp)
	c.deleteEdgeLocked(key.Tail, key.Head)
}
