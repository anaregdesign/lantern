package service

import (
	"context"
	"time"

	coregraph "github.com/anaregdesign/lantern/core/graph"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// Backend is the narrow seam the service depends on instead of the concrete
// *graphcache.GraphCache. The interface is consumer-defined here so adding new
// service-layer RPCs widens it deliberately, and tests can supply a fake.
//
// The batch types (graphcache.VertexItem, graphcache.EdgeItem, graphcache.EdgeKey) remain
// imported as plain value structs — they describe data, not behavior, so
// re-declaring them would just shuffle conversions without buying anything.
type Backend interface {
	// vertex reads/writes
	GetVertex(key string) (*pb.Vertex, bool)
	PutVerticesWithExpiration(items []graphcache.VertexItem[string, *pb.Vertex])
	DeleteVertices(keys []string) int

	// edge reads/writes
	GetEdgeDetail(tail, head string) (float32, time.Time, bool)
	AddEdgesWithExpiration(items []graphcache.EdgeItem[string])
	// AddEdgesWithExpirationContrib is the dedup-aware batch sibling of
	// AddEdgesWithExpiration: a per-item non-zero ContribID makes that
	// contribution idempotent (a retried batch is an exact no-op). Returns
	// the count of items suppressed by a matching live ContribID. Items
	// with a zero ContribID keep legacy additive semantics.
	AddEdgesWithExpirationContrib(items []graphcache.EdgeItem[string]) int
	PutEdgesWithExpiration(items []graphcache.EdgeItem[string])
	DeleteEdges(keys []graphcache.EdgeKey[string]) int

	// replicated-write entry points used by ApplyMutation (#182).
	//
	// AddEdgeWithExpirationContrib records an additive edge contribution
	// stamped with contribID; re-applying a mutation with the same id is
	// a no-op (returns false). Local non-replicated writes keep using
	// AddEdgesWithExpiration with a zero contribID.
	//
	// PutVertexWithExpirationHLC / PutEdgeWithExpirationHLC compare ts
	// against the stored last-write HLC and silently drop strictly-older
	// writes (LWW). Equal-ts writes apply (idempotent for value-equal
	// payloads).
	AddEdgeWithExpirationContrib(tail, head string, w float32, expiration time.Time, contribID graphcache.ContribID) bool
	PutVertexWithExpirationHLC(key string, value *pb.Vertex, expiration time.Time, ts hlc.Timestamp) bool
	PutEdgeWithExpirationHLC(tail, head string, w float32, expiration time.Time, ts hlc.Timestamp) bool

	// tombstone-aware Delete*/Add* entry points used by ApplyMutation
	// when LANTERN_TOMBSTONE_TTL is configured (#183). DeleteVertexHLC,
	// DeleteVerticesHLC, DeleteEdgeHLC, DeleteEdgesHLC and
	// DeleteByPrefixHLC stamp a tombstone keyed on the deleted entry so
	// late-arriving Put*/Add* with strictly-older HLC are rejected for
	// the tombstone window. AddEdgeWithExpirationContribHLC is the HLC
	// sibling of AddEdgeWithExpirationContrib that consults the edge
	// tombstone store before applying.
	AddEdgeWithExpirationContribHLC(tail, head string, w float32, expiration time.Time, contribID graphcache.ContribID, ts hlc.Timestamp) bool
	DeleteVertexHLC(key string, ts hlc.Timestamp, expiration time.Time) bool
	DeleteVerticesHLC(keys []string, ts hlc.Timestamp, expiration time.Time) int
	DeleteEdgeHLC(tail, head string, ts hlc.Timestamp, expiration time.Time) bool
	DeleteEdgesHLC(keys []graphcache.EdgeKey[string], ts hlc.Timestamp, expiration time.Time) int
	DeleteByPrefixHLC(ctx context.Context, prefix string, limit uint32, ts hlc.Timestamp, expiration time.Time) (int, error)

	// neighborhood traversal. selectSmallest steers the per-hop top-k
	// pruning: the k smallest-weight edges are kept when true, the k
	// largest when false (#560), so the caller's Objective governs which
	// edges survive both pruning and any later reduction. keep is an
	// optional frontier predicate (#601): when non-nil, a head is admitted
	// to the frontier only if keep(head) is true, evaluated BEFORE scoring
	// and the per-hop top-k prune so the surviving edges are the k strongest
	// *matching* neighbours. The seed is exempt. A nil keep accepts every
	// head (unfiltered traversal).
	NeighborWithExpirationsContext(
		ctx context.Context,
		seed string,
		step, k int,
		tfidf bool,
		selectSmallest bool,
		keep func(string) bool,
	) (*coregraph.Graph[string, *pb.Vertex], map[string]map[string]time.Time, error)

	// prefix scan / count / delete. ScanByPrefix invokes fn for each live
	// vertex whose key starts with prefix, in lexicographic order; fn
	// returns false to stop early. CountByPrefix is an index-side count
	// (may include not-yet-flushed expired entries; bounded by the GC
	// tick). DeleteByPrefix removes matching vertices up to limit (limit
	// <= 0 means unlimited) and returns how many were deleted.
	ScanByPrefix(ctx context.Context, prefix string, fn func(projected string, key string, value *pb.Vertex) bool) bool
	CountByPrefix(prefix string) int
	DeleteByPrefix(ctx context.Context, prefix string, limit int) int

	// edge-side prefix scan. ScanEdgesByPrefix invokes fn for each live
	// edge whose tail starts with tailPrefix AND whose head starts with
	// headPrefix, in ascending (tail, head) order. Either prefix may be
	// empty to disable the corresponding filter. fn returns false to
	// stop early. Plural-only on the wire (no CountEdges /
	// DeleteEdgesByPrefix in this phase).
	ScanEdgesByPrefix(ctx context.Context, tailPrefix, headPrefix string, fn func(tailProjected string, tail string, headProjected string, head string, weight float32, expiration time.Time) bool) bool

	// background GC loop driven by LanternServer.
	Watch(ctx context.Context, interval time.Duration)

	// Snapshot* return materialised dumps of the live state for the
	// replication bootstrap RPC (#184). Both calls are taken under the
	// GraphCache write lock and are intended to be called once per
	// bootstrapping peer (not on the hot path). The returned slices own
	// their backing storage and are safe to iterate without further
	// locking.
	SnapshotVertices() []graphcache.SnapshotVertex[string, *pb.Vertex]
	SnapshotEdges() []graphcache.SnapshotEdge[string]

	// VertexCount and EdgeCount return the current number of live entries
	// in the underlying graph cache. Backed by index sizes — O(1) and
	// safe to call from any RPC. Returned values are eventually-consistent
	// snapshots (may include not-yet-GC'd expired entries, bounded by the
	// LANTERN_GC_INTERVAL_SECONDS tick). Used by GetServerStatus (#314)
	// to populate the admin UI's at-a-glance counters.
	VertexCount() int
	EdgeCount() int
}
