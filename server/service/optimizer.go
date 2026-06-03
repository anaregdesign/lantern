package service

import (
	"context"
	"math"

	coregraph "github.com/anaregdesign/lantern/core/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// optimizer post-processes the illuminated subgraph for a given seed.
// Implementations may rebuild the graph (e.g. spanning trees, shortest-path
// trees) and must honour ctx for cancellation.
type optimizer func(ctx context.Context, g *coregraph.Graph[string, *pb.Vertex], seed string) (*coregraph.Graph[string, *pb.Vertex], error)

// optimizers maps each pb.Optimization variant to its strategy. The zero /
// UNSPECIFIED variant is intentionally absent: callers treat a nil lookup
// result as "no optimization", which preserves the original behaviour where
// the switch's default branch was a no-op.
//
// Adding a new optimization is now a single map entry — no service-handler
// edit required.
var optimizers = map[pb.Optimization]optimizer{
	pb.Optimization_OPTIMIZATION_MINIMUM_SPANNING_TREE: func(ctx context.Context, g *coregraph.Graph[string, *pb.Vertex], seed string) (*coregraph.Graph[string, *pb.Vertex], error) {
		return g.MinimumSpanningTreeContext(ctx, seed)
	},
	pb.Optimization_OPTIMIZATION_MAXIMUM_SPANNING_TREE: func(ctx context.Context, g *coregraph.Graph[string, *pb.Vertex], seed string) (*coregraph.Graph[string, *pb.Vertex], error) {
		return g.MaximumSpanningTreeContext(ctx, seed)
	},
	pb.Optimization_OPTIMIZATION_SHORTEST_PATH_TREE: func(ctx context.Context, g *coregraph.Graph[string, *pb.Vertex], seed string) (*coregraph.Graph[string, *pb.Vertex], error) {
		return g.ShortestPathTreeContext(ctx, seed, identityCost)
	},
	pb.Optimization_OPTIMIZATION_SHORTEST_PATH_TREE_INVERSE: func(ctx context.Context, g *coregraph.Graph[string, *pb.Vertex], seed string) (*coregraph.Graph[string, *pb.Vertex], error) {
		return g.ShortestPathTreeContext(ctx, seed, inverseCost)
	},
}

func identityCost(weight float32) float32 { return weight }

func inverseCost(weight float32) float32 {
	if weight == 0 {
		return math.MaxFloat32
	}
	return 1 / weight
}
