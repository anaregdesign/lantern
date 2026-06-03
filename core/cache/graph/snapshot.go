package graph

import (
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

// SnapshotVertex is one entry of GraphCache.SnapshotVertices. It carries
// the live vertex value, its expiration, and the last replication HLC the
// LWW apply path (#182) recorded for this key. HLC is the zero value when
// the vertex has never been touched by a replicated Put — that means
// "no causal floor", which is exactly what receivers feed back into
// PutVertexWithExpirationHLC.
type SnapshotVertex[S comparable, T any] struct {
	Key        S
	Value      T
	Expiration time.Time
	HLC        hlc.Timestamp
}

// SnapshotContribution mirrors a single live entry inside an edge's
// weight bucket. Snapshot deliberately preserves the per-contribution
// decomposition so that the receiver's ContribID dedup (#182) continues
// to suppress duplicates when the live tail re-delivers the same
// contribution after bootstrap.
type SnapshotContribution struct {
	Weight     float32
	Expiration time.Time
	ContribID  ContribID
}

// SnapshotEdge is one entry of GraphCache.SnapshotEdges. HLC carries the
// bucket's last LWW position (zero when no LWW write has happened); the
// receiver uses it as the causal floor when replaying each contribution
// via AddEdgeWithExpirationContribHLC.
type SnapshotEdge[S comparable] struct {
	Tail          S
	Head          S
	HLC           hlc.Timestamp
	Contributions []SnapshotContribution
}

// SnapshotVertices returns a materialised snapshot of every live vertex
// in the cache. The snapshot is taken under a single write-lock pass; the
// returned slice is independent of the cache and safe to iterate without
// further locking. Expired vertices (those past Expiration at snapshot
// time) are skipped.
//
// Memory cost is O(N) in the live vertex count — the API is intended for
// replication bootstrap (#184), which is a bounded, infrequent operation.
// Hot read paths should keep using GetVertex.
func (c *GraphCache[S, T]) SnapshotVertices() []SnapshotVertex[S, T] {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.vertices.Flush()
	out := make([]SnapshotVertex[S, T], 0, c.vertices.Count())
	c.vertices.Range(func(key S, value T, expiration time.Time) bool {
		var ts hlc.Timestamp
		if c.vertexHLC != nil {
			ts = c.vertexHLC[key]
		}
		out = append(out, SnapshotVertex[S, T]{
			Key:        key,
			Value:      value,
			Expiration: expiration,
			HLC:        ts,
		})
		return true
	})
	return out
}

// SnapshotEdges returns a materialised snapshot of every live edge in the
// cache, preserving the per-contribution weight decomposition. The
// snapshot is taken under a single write-lock pass; the returned slice is
// independent of the cache. Edges whose every contribution has decayed
// are skipped (they would otherwise show up as ghost entries with zero
// total weight).
//
// Memory cost is O(E + sum of bucket sizes); same scope rationale as
// SnapshotVertices.
func (c *GraphCache[S, T]) SnapshotEdges() []SnapshotEdge[S] {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	out := make([]SnapshotEdge[S], 0, c.edges.count())
	c.edges.rangeBuckets(func(tail, head S, w *weight) bool {
		contribs, ts, nonEmpty := w.snapshotEntry(now)
		if !nonEmpty {
			return true
		}
		out = append(out, SnapshotEdge[S]{
			Tail:          tail,
			Head:          head,
			HLC:           ts,
			Contributions: contribs,
		})
		return true
	})
	return out
}
