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
// receiver uses it as the causal floor when replaying the bucket. A zero-ID
// contribution is the LWW Put row and is restored through
// PutEdgeWithExpirationHLC; non-zero-ID additive rows use
// AddEdgeWithExpirationContribHLC so ContribID dedup remains effective.
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

// SnapshotVertexCausalBarrier / SnapshotEdgeCausalBarrier represent accepted
// HLC Put overwrites that were already expired at their final application
// sample. They carry no live value: only the identity and retained LWW floor
// needed to prevent a delayed older mutation from resurrecting it.
type SnapshotVertexCausalBarrier[S comparable] struct {
	Key S
	HLC hlc.Timestamp
}

type SnapshotEdgeCausalBarrier[S comparable] struct {
	Tail S
	Head S
	HLC  hlc.Timestamp
}

// CausalBarrierSnapshot is exported separately from GraphSnapshot because
// backup/ordinary snapshot readers consume live graph state only. Replication
// bootstrap streams these records ahead of live entries using explicit causal-
// barrier frames rather than overloading the live vertex/edge shapes.
type CausalBarrierSnapshot[S comparable] struct {
	Vertices []SnapshotVertexCausalBarrier[S]
	Edges    []SnapshotEdgeCausalBarrier[S]
}

// ReplicationSnapshot is one causal point-in-time image for replication
// bootstrap. Barriers and live state must be captured together: a separate
// barrier pass followed by a live pass can lose an LWW floor when TTL GC moves
// an expired live Put into the retained barrier store between the two calls.
type ReplicationSnapshot[S comparable, T any] struct {
	Barriers CausalBarrierSnapshot[S]
	Graph    GraphSnapshot[S, T]
}

// SnapshotCausalBarriers returns an owned copy of every retained
// accepted-expired Put floor. The barriers are not GC state and therefore are
// not filtered by wall time. A read lock is sufficient: HLC apply paths mutate
// both maps only while holding c.mu.Lock.
func (c *GraphCache[S, T]) SnapshotCausalBarriers() CausalBarrierSnapshot[S] {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotCausalBarriersRLocked()
}

func (c *GraphCache[S, T]) snapshotCausalBarriersRLocked() CausalBarrierSnapshot[S] {
	out := CausalBarrierSnapshot[S]{
		Vertices: make([]SnapshotVertexCausalBarrier[S], 0, len(c.vertexCausalBarriers)),
		Edges:    make([]SnapshotEdgeCausalBarrier[S], 0, len(c.edgeCausalBarriers)),
	}
	for key, ts := range c.vertexCausalBarriers {
		out.Vertices = append(out.Vertices, SnapshotVertexCausalBarrier[S]{Key: key, HLC: ts})
	}
	for key, ts := range c.edgeCausalBarriers {
		out.Edges = append(out.Edges, SnapshotEdgeCausalBarrier[S]{Tail: key.Tail, Head: key.Head, HLC: ts})
	}
	return out
}

// SnapshotReplication captures retained barriers and the live graph under one
// continuous write lock after migrating expired live Put floors. The migration
// closes the pre-GC window in which ordinary live snapshot helpers correctly
// filter an expired value/bucket while its HLC still resides only in live
// metadata. It deliberately does not run general TTL/dangling reclamation;
// bootstrap remains an infrequent O(N+E) operation and only causal metadata is
// moved before materialisation.
func (c *GraphCache[S, T]) SnapshotReplication() ReplicationSnapshot[S, T] {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.migrateExpiredVertexHLCToBarriersLocked(now)
	c.migrateExpiredEdgeHLCToBarriersLocked(now)
	return ReplicationSnapshot[S, T]{
		Barriers: c.snapshotReplicationCausalBarriersRLocked(),
		Graph: GraphSnapshot[S, T]{
			Vertices: c.snapshotVerticesRLocked(now),
			Edges:    c.snapshotEdgesRLocked(now),
		},
	}
}

// snapshotReplicationCausalBarriersRLocked extends the retained barrier maps
// with every non-zero Put floor still carried by a live edge bucket. A bucket
// may contain a short-lived Put row followed by longer-lived Add rows. Once
// the Put row expires, SnapshotEdge necessarily contains only Add rows; its
// bucket-level HLC lets those rows replay after a barrier, but does not itself
// persist the floor on the receiver. Emitting an explicit barrier frame first
// closes that hole. When the zero-ContribID Put row is still live, its replay
// supersedes the equal barrier again; when only Add rows survive, the barrier
// remains alongside them. The merge is output-only for live buckets so an
// infrequent Snapshot does not inflate the source's retained-capacity maps.
func (c *GraphCache[S, T]) snapshotReplicationCausalBarriersRLocked() CausalBarrierSnapshot[S] {
	out := c.snapshotCausalBarriersRLocked()
	edgeFloors := make(map[EdgeKey[S]]hlc.Timestamp, len(out.Edges))
	for _, barrier := range out.Edges {
		edgeFloors[EdgeKey[S]{Tail: barrier.Tail, Head: barrier.Head}] = barrier.HLC
	}
	c.edges.rangeBuckets(func(tail, head S, w *weight) bool {
		ts := w.lastPutTimestamp()
		if ts == (hlc.Timestamp{}) {
			return true
		}
		key := EdgeKey[S]{Tail: tail, Head: head}
		if existing, ok := edgeFloors[key]; !ok || existing.Less(ts) {
			edgeFloors[key] = ts
		}
		return true
	})
	out.Edges = make([]SnapshotEdgeCausalBarrier[S], 0, len(edgeFloors))
	for key, ts := range edgeFloors {
		out.Edges = append(out.Edges, SnapshotEdgeCausalBarrier[S]{Tail: key.Tail, Head: key.Head, HLC: ts})
	}
	return out
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
	return c.snapshotVerticesRLocked(time.Now())
}

// snapshotVerticesRLocked materialises every live vertex. The caller must
// hold c.mu (read lock suffices since #843 — the pass only filters expired
// entries, never mutates). vertexHLC is safe to read here because every
// writer of that map holds c.mu.Lock. Factored out so SnapshotGraph can
// reuse it under a single lock acquisition.
func (c *GraphCache[S, T]) snapshotVerticesRLocked(now time.Time) []SnapshotVertex[S, T] {
	out := make([]SnapshotVertex[S, T], 0, c.vertices.Count())
	c.vertices.RangeAt(now, func(key S, value T, expiration time.Time) bool {
		var ts hlc.Timestamp
		if c.vertexHLC != nil {
			ts = c.vertexHLC[key]
		}
		if barrier, ok := c.vertexCausalBarriers[key]; ok && ts.Less(barrier) {
			ts = barrier
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
		if !c.vertices.HasAt(tail, now) || !c.vertices.HasAt(head, now) {
			return true
		}
		// A retained accepted-expired Put floor may coexist with newer additive
		// contributions. Add contributions do not carry per-contribution HLC on
		// the Snapshot wire, so lift the edge frame's bucket floor to the maximum
		// of the physical bucket and retained barrier. Barrier-first replay then
		// accepts the equal-HLC Add rows while continuing to fence older Puts.
		if barrier, ok := c.edgeCausalBarriers[EdgeKey[S]{Tail: tail, Head: head}]; ok && ts.Less(barrier) {
			ts = barrier
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
	now := time.Now()
	return GraphSnapshot[S, T]{
		Vertices: c.snapshotVerticesRLocked(now),
		Edges:    c.snapshotEdgesRLocked(now),
	}
}
