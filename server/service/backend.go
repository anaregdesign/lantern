package service

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/core/cache/graph"
	coregraph "github.com/anaregdesign/lantern/core/graph"
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

	// background GC loop driven by LanternServer.
	Watch(ctx context.Context, interval time.Duration)
}
