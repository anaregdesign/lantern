package graphcache

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

// GraphSnapshot is the result of GraphCache.SnapshotGraph: every live
// vertex and edge materialised under a single lock acquisition, so the two
// slices reflect one point-in-time. The type is named GraphSnapshot (not
// SnapshotGraph) so the SnapshotGraph name is free for the method.
type GraphSnapshot[S comparable, T any] struct {
	Vertices []SnapshotVertex[S, T]
	Edges    []SnapshotEdge[S]
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
	return c.snapshotVerticesLocked()
}

// snapshotVerticesLocked materialises every live vertex. The caller MUST
// hold c.mu as a WRITE lock: it calls c.vertices.Flush(), which mutates the
// cache, so a read lock is insufficient. Factored out so SnapshotGraph can
// reuse it under a single lock acquisition.
func (c *GraphCache[S, T]) snapshotVerticesLocked() []SnapshotVertex[S, T] {
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
	return c.snapshotEdgesLocked(time.Now())
}

// snapshotEdgesLocked materialises every live edge as of now. The caller
// MUST hold c.mu (write lock — symmetric with snapshotVerticesLocked so
// SnapshotGraph can take the lock once for both sides).
func (c *GraphCache[S, T]) snapshotEdgesLocked(now time.Time) []SnapshotEdge[S] {
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

// SnapshotGraph returns a materialised whole-graph snapshot — every live
// vertex and edge — taken under a SINGLE write-lock pass, so the vertex and
// edge sides reflect the same instant. Calling SnapshotVertices and
// SnapshotEdges separately locks twice and can observe a vertex/edge set
// that never co-existed; SnapshotGraph closes that window, which is what a
// whole-graph backup/restore needs.
//
// The returned slices are independent of the cache and safe to iterate
// without further locking. Expired vertices and fully-decayed edges are
// skipped, identical to the single-kind methods. Memory cost is O(live
// vertices + live edges); like the single-kind snapshots it is intended for
// bounded, infrequent operations (backup, replication bootstrap), not hot
// read paths.
func (c *GraphCache[S, T]) SnapshotGraph() GraphSnapshot[S, T] {
	c.mu.Lock()
	defer c.mu.Unlock()
	return GraphSnapshot[S, T]{
		Vertices: c.snapshotVerticesLocked(),
		Edges:    c.snapshotEdgesLocked(time.Now()),
	}
}
