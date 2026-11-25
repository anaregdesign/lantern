package graphcache

import (
	"context"
	"errors"
	"math"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/tarp"
)

// StorageKeys identifies the graph portion of one service publication. Edge
// keys include both endpoint rows, since accepting an edge can create them.
type StorageKeys[S comparable] struct {
	Vertices      []S
	Edges         []EdgeKey[S]
	Contributions []EdgeContributionKey[S]
}

// StorageRowWriter adds graph rows to the caller's commit alongside its log
// and receipt rows. An open transaction already holds the visibility locks.
type StorageRowWriter[S comparable, T any] interface {
	StoreRows(context.Context, *graphstore.Rows[S, T], *tarp.Snapshot, graphstore.Writer, uint64, StorageKeys[S]) error
}

func (c *GraphCache[S, T]) StoreRows(ctx context.Context, rows *graphstore.Rows[S, T], snapshot *tarp.Snapshot, batch graphstore.Writer, generation uint64, keys StorageKeys[S]) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.publicationGate != nil {
		c.publicationGate.Lock()
		defer c.publicationGate.Unlock()
	}
	return c.storeRowsLocked(ctx, rows, snapshot, batch, generation, keys)
}

func (c *GraphCache[S, T]) storeRowsLocked(ctx context.Context, rows *graphstore.Rows[S, T], snapshot *tarp.Snapshot, batch graphstore.Writer, generation uint64, keys StorageKeys[S]) error {
	vertices := make(map[S]struct{}, len(keys.Vertices))
	edges := make(map[EdgeKey[S]]struct{}, len(keys.Edges))
	for _, key := range keys.Vertices {
		vertices[key] = struct{}{}
	}
	for _, key := range keys.Edges {
		edges[key] = struct{}{}
	}
	for _, key := range keys.Contributions {
		edges[EdgeKey[S]{Tail: key.Tail, Head: key.Head}] = struct{}{}
		fk := graphstore.FloorKey[S]{Kind: graphstore.ContributionTombstone, Tail: key.Tail, Head: key.Head, ContribID: key.ContribID}
		if err := storeFloor(rows, batch, generation, fk, c.edgeContributionTombstones[key]); err != nil {
			return err
		}
	}
	for key := range edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		vertices[key.Tail] = struct{}{}
		vertices[key.Head] = struct{}{}
		if err := c.storeEdgeLocked(ctx, rows, snapshot, batch, generation, key); err != nil {
			return err
		}
		fk := graphstore.FloorKey[S]{Kind: graphstore.EdgeBarrier, Tail: key.Tail, Head: key.Head}
		if err := storeFloor(rows, batch, generation, fk, tombstoneEntry{ts: c.edgeCausalBarriers[key]}); err != nil {
			return err
		}
		fk.Kind = graphstore.EdgeTombstone
		if err := storeFloor(rows, batch, generation, fk, c.edgeTombstones[key]); err != nil {
			return err
		}
	}
	for key := range vertices {
		if err := ctx.Err(); err != nil {
			return err
		}
		value, expires, present, cold := c.vertices.PeekResident(key)
		var err error
		if present && !cold {
			err = rows.PutVertex(batch, generation, key, graphstore.Vertex[T]{Value: value, Expiration: expires, HLC: c.vertexHLC[key]})
		} else if !present {
			err = rows.DeleteVertex(batch, generation, key)
		}
		if err != nil {
			return err
		}
		fk := graphstore.FloorKey[S]{Kind: graphstore.VertexBarrier, Key: key}
		if err := storeFloor(rows, batch, generation, fk, tombstoneEntry{ts: c.vertexCausalBarriers[key]}); err != nil {
			return err
		}
		fk.Kind = graphstore.VertexTombstone
		if err := storeFloor(rows, batch, generation, fk, c.vertexTombstones[key]); err != nil {
			return err
		}
	}
	if err := c.StorageError(); err != nil {
		return err
	}
	return ctx.Err()
}

func storeFloor[S comparable, T any](rows *graphstore.Rows[S, T], batch graphstore.Writer, generation uint64, key graphstore.FloorKey[S], entry tombstoneEntry) error {
	if entry.ts == (hlc.Timestamp{}) {
		return rows.DeleteFloor(batch, generation, key)
	}
	return rows.PutFloor(batch, generation, key, graphstore.Floor{HLC: entry.ts, Expiration: entry.expiration})
}

func (c *GraphCache[S, T]) storeEdgeLocked(ctx context.Context, rows *graphstore.Rows[S, T], snapshot *tarp.Snapshot, batch graphstore.Writer, generation uint64, key EdgeKey[S]) error {
	k := graphstore.EdgeKey[S]{Tail: key.Tail, Head: key.Head}
	bucket := c.edges.bucket(key.Tail, key.Head)
	if bucket != nil {
		bucket.mu.Lock()
		cold := bucket.cold
		bucket.mu.Unlock()
		if cold {
			return nil
		}
	}
	var edge graphstore.Edge
	var sources map[graphstore.SourceID]graphstore.Source
	if bucket != nil {
		edge, sources = storageEdge(bucket)
	}
	for entry, err := range rows.Sources(ctx, snapshot, generation, k) {
		if err != nil {
			return err
		}
		next, keep := sources[entry.ID]
		switch {
		case !keep:
			if err := rows.DeleteSource(batch, generation, k, entry.ID); err != nil {
				return err
			}
		case sameStoredSource(entry.Source, next):
			delete(sources, entry.ID)
		}
	}
	for id, source := range sources {
		if err := rows.PutSource(batch, generation, k, id, source); err != nil {
			return err
		}
	}
	if bucket == nil {
		return rows.DeleteEdge(batch, generation, k)
	}
	return rows.PutEdge(batch, generation, k, edge)
}

func storageEdge(bucket *weight) (graphstore.Edge, map[graphstore.SourceID]graphstore.Source) {
	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	edge := graphstore.Edge{HLC: bucket.lastHLC, LastOrdinal: bucket.lastOrdinal}
	sources := make(map[graphstore.SourceID]graphstore.Source, len(bucket.values))
	for _, v := range bucket.values {
		source := graphstore.Source{Weight: v.value, Expiration: v.expiration, HLC: v.hlc, DerivedAggregate: v.derivedAggregate}
		if v.contribID.IsZero() && v.ordinal == 0 {
			edge.Base = &source
			continue
		}
		id := graphstore.SourceID{ContribID: v.contribID, Ordinal: v.ordinal}
		sources[id] = source
	}
	return edge, sources
}

func sameStoredSource(a, b graphstore.Source) bool {
	return math.Float32bits(a.Weight) == math.Float32bits(b.Weight) && a.Expiration.Equal(b.Expiration) && a.HLC == b.HLC && a.DerivedAggregate == b.DerivedAggregate
}

var errStorageStageClosed = errors.New("graphcache: storage capture requires an applied, open graph transaction")
