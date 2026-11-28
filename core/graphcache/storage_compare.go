package graphcache

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/hlc"
)

// MatchesStorageGeneration verifies the complete retained graph against a
// pinned cut before retrying a failed Snapshot. A deadline can pass while the
// attempt is faulted; elapsed rows still participate until GC has committed
// their removal. This check never compacts, hydrates or changes the graph.
func (c *GraphCache[S, T]) MatchesStorageGeneration(ctx context.Context, rows *graphstore.Rows[S, T], view *graphstore.ReadView) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if view == nil || rows == nil {
		return false, errors.New("graphcache: missing storage comparison cut")
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.publicationGate != nil {
		c.publicationGate.Lock()
		defer c.publicationGate.Unlock()
	}
	if err := c.StorageError(); err != nil {
		return false, err
	}
	var vertices, clocks int
	for row, err := range rows.VerifiedVertexBytes(ctx, view.Snapshot, view.Generation) {
		if err != nil {
			return false, err
		}
		value, expiry, present, cold := c.vertices.PeekResident(row.Key)
		if !present || !expiry.Equal(row.Vertex.Expiration) || c.vertexHLC[row.Key] != row.Vertex.HLC {
			return false, nil
		}
		if !cold {
			if equal, err := rows.ValueMatches(value, row.Vertex.Value); !equal || err != nil {
				return false, err
			}
		}
		vertices++
		if row.Vertex.HLC != (hlc.Timestamp{}) {
			clocks++
		}
	}
	if vertices != c.vertices.Count() || clocks != len(c.vertexHLC) {
		return false, nil
	}

	var edges int
	var scratch []graphstore.SourceEntry
	for row, err := range rows.EdgeBuckets(ctx, view.Snapshot, view.Generation, "") {
		if err != nil {
			return false, err
		}
		bucket := c.edges.bucket(row.Key.Tail, row.Key.Head)
		if bucket == nil || !matchesStorageWeight(bucket, row, &scratch) {
			return false, nil
		}
		edges++
	}
	if edges != c.edges.count() {
		return false, nil
	}

	var floors int
	for row, err := range rows.Floors(ctx, view.Snapshot, view.Generation) {
		if err != nil {
			return false, err
		}
		expected, ok := c.retainedFloor(row.Key)
		if !ok || expected.ts != row.Floor.HLC || !expected.expiration.Equal(row.Floor.Expiration) {
			return false, nil
		}
		floors++
	}
	if floors != len(c.vertexCausalBarriers)+len(c.edgeCausalBarriers)+len(c.vertexTombstones)+len(c.edgeTombstones)+len(c.edgeContributionTombstones) {
		return false, nil
	}
	// Nonresident payloads belong to the active durable revision. A retired
	// generation, a superseded same-generation read cut or a different DB
	// cannot vouch for those descriptors.
	if c.storage != nil {
		if view.Database() != c.storage.db {
			return false, nil
		}
		current, err := c.storage.db.Read()
		if err != nil {
			return false, err
		}
		same := current.Generation == view.Generation && current.Revision == view.Revision
		if err := current.Close(); err != nil || !same {
			return false, err
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return true, nil
}

func matchesStorageWeight[K comparable](w *weight, row graphstore.EdgeBucket[K], scratch *[]graphstore.SourceEntry) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	edge, sources := row.Edge, row.Sources
	if w.lastHLC != edge.HLC || w.lastOrdinal != edge.LastOrdinal {
		return false
	}
	want := len(sources)
	if edge.Base != nil {
		want++
	}
	if w.cold {
		if w.storedCount != want {
			return false
		}
		// Online reclamation can invalidate a derived summary without
		// retaining a second copy of the bucket. The outer comparison
		// also verifies that this is the active committed storage cut.
		if w.storedSummaryInvalid {
			return true
		}
		detail, folded := row.RetainedDetail(*scratch)
		*scratch = folded
		var earliest time.Time
		var explicit bool
		for _, entry := range folded {
			expires := entry.Source.Expiration
			if !expires.IsZero() && (earliest.IsZero() || expires.Before(earliest)) {
				earliest = expires
			}
			explicit = explicit || entry.ID.ContribID != ([24]byte{})
		}
		sameSum := w.storedSum == detail.Weight || math.IsNaN(float64(w.storedSum)) && math.IsNaN(float64(detail.Weight))
		return sameSum &&
			w.storedExpiry.Equal(earliest) && w.storedLatest.Equal(detail.Expiration) && w.storedExplicit == explicit
	}
	if len(w.values) != want {
		return false
	}
	// Hot rows may have been changed by the failed attempt. Their causal
	// fold order differs from their physical order, so match by source ID.
	retained := make(map[graphstore.SourceID]graphstore.Source, want)
	for _, item := range sources {
		retained[item.ID] = item.Source
	}
	if edge.Base != nil {
		retained[graphstore.SourceID{}] = *edge.Base
	}
	for _, v := range w.values {
		id := graphstore.SourceID{ContribID: v.contribID, Ordinal: v.ordinal}
		if stored, ok := retained[id]; !ok || !sameStoredSource(v.storedSource(), stored) {
			return false
		}
		delete(retained, id)
	}
	return len(retained) == 0
}

func (c *GraphCache[S, T]) retainedFloor(k graphstore.FloorKey[S]) (tombstoneEntry, bool) {
	edge := EdgeKey[S]{Tail: k.Tail, Head: k.Head}
	switch k.Kind {
	case graphstore.VertexBarrier:
		ts, ok := c.vertexCausalBarriers[k.Key]
		return tombstoneEntry{ts: ts}, ok
	case graphstore.EdgeBarrier:
		ts, ok := c.edgeCausalBarriers[edge]
		return tombstoneEntry{ts: ts}, ok
	case graphstore.VertexTombstone:
		v, ok := c.vertexTombstones[k.Key]
		return v, ok
	case graphstore.EdgeTombstone:
		v, ok := c.edgeTombstones[edge]
		return v, ok
	case graphstore.ContributionTombstone:
		v, ok := c.edgeContributionTombstones[EdgeContributionKey[S]{Tail: k.Tail, Head: k.Head, ContribID: k.ContribID}]
		return v, ok
	}
	return tombstoneEntry{}, false
}
