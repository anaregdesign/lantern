package graphcache

import (
	"context"
	"time"

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
			chunk.check(rows.PutSource(&chunk, g, key, id, src))
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
	return chunk.flush()
}

type storageRecord struct{ k, v []byte }
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
	if len(w.records) >= 512 || w.bytes+len(k)+len(v) > 1<<20 {
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
