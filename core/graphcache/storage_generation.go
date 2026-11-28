package graphcache

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/graphstore"
)

// WriteStorageGeneration writes a complete graph to an unpublished generation.
// The caller also stages its service records and decides when to publish.
// Space used for encoding is bounded by a chunk plus the current row.
func (c *GraphCache[S, T]) WriteStorageGeneration(ctx context.Context, rows *graphstore.Rows[S, T], target *graphstore.Generation) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.publicationGate != nil {
		c.publicationGate.Lock()
		defer c.publicationGate.Unlock()
	}
	var source *graphstore.ReadView
	if c.storage != nil {
		var err error
		source, err = c.storage.db.Read()
		if err != nil {
			return c.storage.fence(err)
		}
		defer source.Close()
	}
	chunk := storageChunk{ctx: ctx, target: target}
	g := target.ID()
	now := c.applicationTime()
	c.migrateExpiredVertexHLCToBarriersLocked(now)
	c.vertices.RangeAt(now, func(k S, v T, expiry time.Time) bool {
		chunk.check(rows.PutVertex(&chunk, g, k, graphstore.Vertex[T]{Value: v, Expiration: expiry, HLC: c.vertexHLC[k]}))
		return chunk.err == nil
	})
	c.edges.rangeBuckets(func(tail, head S, w *weight) bool {
		if chunk.err != nil {
			return false
		}
		key := graphstore.EdgeKey[S]{Tail: tail, Head: head}
		edge := graphstore.Edge{}
		w.mu.Lock()
		if source != nil && w.cold && w.values == nil {
			edge.HLC, edge.LastOrdinal = w.lastHLC, w.lastOrdinal
			w.mu.Unlock()
			err := c.storage.streamColdEdge(&chunk, rows, source, key, edge, now)
			if err != nil && err != ctx.Err() {
				err = c.storage.fence(err)
			}
			chunk.check(err)
			return chunk.err == nil
		}
		if !w.loadLocked(now) {
			w.mu.Unlock()
			chunk.check(c.StorageError())
			return false
		}
		edge.HLC, edge.LastOrdinal = w.lastHLC, w.lastOrdinal
		for _, v := range w.values {
			if chunk.err != nil {
				break
			}
			src := v.storedSource()
			if v.contribID.IsZero() && v.ordinal == 0 {
				edge.Base = &src
				continue
			}
			id := graphstore.SourceID{ContribID: v.contribID, Ordinal: v.ordinal}
			chunk.check(rows.ReplaceSource(&chunk, g, key, id, nil, &src))
		}
		w.releaseReadLocked()
		w.mu.Unlock()
		chunk.check(rows.PutEdge(&chunk, g, key, edge))
		return chunk.err == nil
	})
	for k, ts := range c.vertexCausalBarriers {
		if chunk.err != nil {
			return chunk.err
		}
		chunk.check(rows.PutFloor(&chunk, g, graphstore.FloorKey[S]{Kind: graphstore.VertexBarrier, Key: k}, graphstore.Floor{HLC: ts}))
	}
	for k, ts := range c.edgeCausalBarriers {
		if chunk.err != nil {
			return chunk.err
		}
		chunk.check(rows.PutFloor(&chunk, g, graphstore.FloorKey[S]{Kind: graphstore.EdgeBarrier, Tail: k.Tail, Head: k.Head}, graphstore.Floor{HLC: ts}))
	}
	for k, t := range c.vertexTombstones {
		if chunk.err != nil {
			return chunk.err
		}
		if t.expiration.After(now) {
			chunk.check(rows.PutFloor(&chunk, g, graphstore.FloorKey[S]{Kind: graphstore.VertexTombstone, Key: k}, graphstore.Floor{HLC: t.ts, Expiration: t.expiration}))
		}
	}
	for k, t := range c.edgeTombstones {
		if chunk.err != nil {
			return chunk.err
		}
		if t.expiration.After(now) {
			chunk.check(rows.PutFloor(&chunk, g, graphstore.FloorKey[S]{Kind: graphstore.EdgeTombstone, Tail: k.Tail, Head: k.Head}, graphstore.Floor{HLC: t.ts, Expiration: t.expiration}))
		}
	}
	for k, t := range c.edgeContributionTombstones {
		if chunk.err != nil {
			return chunk.err
		}
		if t.expiration.After(now) {
			chunk.check(rows.PutFloor(&chunk, g, graphstore.FloorKey[S]{Kind: graphstore.ContributionTombstone, Tail: k.Tail, Head: k.Head, ContribID: k.ContribID}, graphstore.Floor{HLC: t.ts, Expiration: t.expiration}))
		}
	}
	if err := c.StorageError(); err != nil {
		return err
	}
	chunk.check(rows.MarkIncomingEdgeIndex(&chunk, g))
	chunk.check(rows.MarkSourceOrder(&chunk, g))
	if err := chunk.flush(); err != nil {
		return err
	}
	return target.BuildTopologyStats(ctx)
}

// streamColdEdge copies a pinned bucket one Source at a time. The destination
// remains hidden even when one bucket needs several durable staging chunks.
func (s *storageResidency[S, T]) streamColdEdge(chunk *storageChunk, rows *graphstore.Rows[S, T], source *graphstore.ReadView, key graphstore.EdgeKey[S], edge graphstore.Edge, now time.Time) error {
	stored, found, err := s.rows.GetEdge(source.Snapshot, source.Generation, key)
	if err != nil {
		return err
	}
	if !found || stored.HLC != edge.HLC || stored.LastOrdinal != edge.LastOrdinal {
		return graphstore.ErrInvalidRow
	}
	if stored.Base != nil && cache.IsLiveAt(stored.Base.Expiration, now) {
		edge.Base = stored.Base
	}
	for sourceRow, err := range s.rows.Sources(chunk.ctx, source.Snapshot, source.Generation, key) {
		if err != nil {
			return err
		}
		if sourceRow.ID.Ordinal > edge.LastOrdinal {
			return graphstore.ErrInvalidRow
		}
		if cache.IsLiveAt(sourceRow.Source.Expiration, now) {
			chunk.check(rows.ReplaceSource(chunk, chunk.target.ID(), key, sourceRow.ID, nil, &sourceRow.Source))
			if chunk.err != nil {
				return nil
			}
		}
	}
	chunk.check(rows.PutEdge(chunk, chunk.target.ID(), key, edge))
	return nil
}

type storageRecord struct{ k, v []byte }

// A one-MiB Vertex expands during encoding and has a separate TTL row.
// Coalesce several such Vertices into each durable staging write.
const storageGenerationChunkBytes = 4 << 20

type storageChunk struct {
	ctx     context.Context
	target  *graphstore.Generation
	records []storageRecord
	bytes   int
	err     error
}

func (w *storageChunk) check(err error) {
	if w.err == nil {
		w.err = err
	}
}
func (w *storageChunk) Put(k, v []byte) {
	if w.err != nil {
		return
	}
	if w.err = w.ctx.Err(); w.err != nil {
		return
	}
	if len(w.records) >= 512 || w.bytes+len(k)+len(v) > storageGenerationChunkBytes {
		if w.flush() != nil {
			return
		}
	}
	w.records = append(w.records, storageRecord{k, v})
	w.bytes += len(k) + len(v)
}
func (w *storageChunk) Delete(key []byte) {
	panic("graphcache: a new graph generation contains only puts")
}
func (w *storageChunk) flush() error {
	if w.err != nil {
		return w.err
	}
	if w.err = w.ctx.Err(); w.err != nil || len(w.records) == 0 {
		return w.err
	}
	w.err = w.target.Apply(func(_ uint64, b graphstore.Writer) error {
		for _, r := range w.records {
			b.Put(r.k, r.v)
		}
		return nil
	})
	clear(w.records)
	w.records = w.records[:0]
	w.bytes = 0
	return w.err
}
