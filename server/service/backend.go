package service

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/core/cache/graph"
	coregraph "github.com/anaregdesign/lantern/core/graph"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// Backend is the narrow seam the service depends on instead of the concrete
// *graph.GraphCache. The interface is consumer-defined here so adding new
// service-layer RPCs widens it deliberately, and tests can supply a fake.
//
// The batch types (graph.VertexItem, graph.EdgeItem, graph.EdgeKey) remain
// imported as plain value structs — they describe data, not behavior, so
// re-declaring them would just shuffle conversions without buying anything.
type Backend interface {
	// vertex reads/writes
	GetVertex(key string) (*pb.Vertex, bool)
	PutVerticesWithExpiration(items []graph.VertexItem[string, *pb.Vertex])
	DeleteVertices(keys []string) int

	// edge reads/writes
	GetEdgeDetail(tail, head string) (float32, time.Time, bool)
	AddEdgesWithExpiration(items []graph.EdgeItem[string])
	PutEdgesWithExpiration(items []graph.EdgeItem[string])
	DeleteEdges(keys []graph.EdgeKey[string]) int

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
	AddEdgeWithExpirationContrib(tail, head string, w float32, expiration time.Time, contribID graph.ContribID) bool
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
	AddEdgeWithExpirationContribHLC(tail, head string, w float32, expiration time.Time, contribID graph.ContribID, ts hlc.Timestamp) bool
	DeleteVertexHLC(key string, ts hlc.Timestamp, expiration time.Time) bool
	DeleteVerticesHLC(keys []string, ts hlc.Timestamp, expiration time.Time) int
	DeleteEdgeHLC(tail, head string, ts hlc.Timestamp, expiration time.Time) bool
	DeleteEdgesHLC(keys []graph.EdgeKey[string], ts hlc.Timestamp, expiration time.Time) int
	DeleteByPrefixHLC(ctx context.Context, prefix string, limit uint32, ts hlc.Timestamp, expiration time.Time) (int, error)

	// neighborhood traversal
	NeighborWithExpirationsContext(
		ctx context.Context,
		seed string,
		step, k int,
		tfidf bool,
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
}
