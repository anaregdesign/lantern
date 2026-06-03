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
	AddVerticesWithExpiration(items []graph.VertexItem[string, *pb.Vertex])
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

	// background GC loop driven by LanternServer.
	Watch(ctx context.Context, interval time.Duration)
}
