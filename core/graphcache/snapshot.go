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
// in the cache. Since #843 the snapshot is taken under a READ lock:
// expired-but-unflushed entries are FILTERED (Range skips them) rather than
// physically flushed, so concurrent readers — point reads, scans,
// traversals — keep making progress while a backup or replication
// bootstrap materialises the graph. Physical reclamation of expired
// entries belongs solely to the GC tick. The returned slice is independent
// of the cache and safe to iterate without further locking.
//
// Memory cost is O(N) in the live vertex count — the API is intended for
// replication bootstrap (#184), which is a bounded, infrequent operation.
// Hot read paths should keep using GetVertex.
func (c *GraphCache[S, T]) SnapshotVertices() []SnapshotVertex[S, T] {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotVerticesRLocked()
}

// snapshotVerticesRLocked materialises every live vertex. The caller must
// hold c.mu (read lock suffices since #843 — the pass only filters expired
// entries, never mutates). vertexHLC is safe to read here because every
// writer of that map holds c.mu.Lock. Factored out so SnapshotGraph can
// reuse it under a single lock acquisition.
func (c *GraphCache[S, T]) snapshotVerticesRLocked() []SnapshotVertex[S, T] {
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
// cache, preserving the per-contribution weight decomposition. Taken under
// a READ lock since #843 (see SnapshotVertices); the returned slice is
// independent of the cache. Edges whose every contribution has decayed
// are skipped (they would otherwise show up as ghost entries with zero
// total weight).
//
// Memory cost is O(E + sum of bucket sizes); same scope rationale as
// SnapshotVertices.
func (c *GraphCache[S, T]) SnapshotEdges() []SnapshotEdge[S] {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotEdgesRLocked(time.Now())
}

// snapshotEdgesRLocked materialises every live edge as of now. The caller
// must hold c.mu (read lock suffices — rangeBuckets takes the edgeCache
// RLock, snapshotEntry the per-weight mutex, and edgeEndpointsLive the
// inner vertex-cache lock; nothing here mutates).
func (c *GraphCache[S, T]) snapshotEdgesRLocked(now time.Time) []SnapshotEdge[S] {
	out := make([]SnapshotEdge[S], 0, c.edges.count())
	c.edges.rangeBuckets(func(tail, head S, w *weight) bool {
		contribs, ts, nonEmpty := w.snapshotEntry(now)
		if !nonEmpty {
			return true
		}
		// Referential closure (#750): a snapshot must not stream an edge whose
		// tail or head vertex is not live. vertices.Has hides
		// expired-but-not-flushed endpoints without any prior flush (#843), so
		// no path leaks a dangling edge to a deleted or expired vertex ahead
		// of the GC sweep.
		if !c.edgeEndpointsLive(tail, head) {
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
// vertex and edge — taken under a SINGLE continuous lock pass, so the
// vertex and edge sides reflect the same instant. Calling SnapshotVertices
// and SnapshotEdges separately locks twice and can observe a vertex/edge
// set that never co-existed; SnapshotGraph closes that window, which is
// what a whole-graph backup/restore needs.
//
// Since #843 the pass holds c.mu as a READ lock: every mutator that can
// change the vertex or edge SETS takes c.mu.Lock, so set-level
// point-in-time consistency is identical to the historical write-lock pass
// — while concurrent readers keep serving. The one non-excluded writer is
// the lock-free hot-edge weight append (tryAddExistingEdgeContrib), which
// never held c.mu under the old write lock either: it cannot change the
// edge set, and per-bucket atomicity is provided by the weight's own mutex,
// so the race surface is unchanged.
//
// The returned slices are independent of the cache and safe to iterate
// without further locking. Expired vertices and fully-decayed edges are
// skipped, identical to the single-kind methods. Memory cost is O(live
// vertices + live edges); like the single-kind snapshots it is intended for
// bounded, infrequent operations (backup, replication bootstrap), not hot
// read paths.
func (c *GraphCache[S, T]) SnapshotGraph() GraphSnapshot[S, T] {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return GraphSnapshot[S, T]{
		Vertices: c.snapshotVerticesRLocked(),
		Edges:    c.snapshotEdgesRLocked(time.Now()),
	}
}
