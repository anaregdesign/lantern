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

// resolveOptimizer returns the post-traversal reduction for an
// (algorithm, objective) pair. Returns nil when no reduction is needed
// — the caller treats nil as "return the raw discovered subgraph".
//
// Adding a new algorithm × objective combination is now a single switch
// arm; no service-handler edit required. Per #410 the dispatch surface
// is the orthogonal three-axes design (algorithm, objective, weighting)
// and the historical flat Optimization enum is gone.
//
// Note: objective ALGORITHM_UNSPECIFIED → nil (no reduction). Objective
// OBJECTIVE_UNSPECIFIED resolves to MAXIMIZE per the proto-level default
// (#560): a bare illuminate keeps the strongest edges both when pruning the
// per-hop top-k and when reducing the discovered subgraph.
func resolveOptimizer(algo pb.Algorithm, obj pb.Objective) optimizer {
	switch algo {
	case pb.Algorithm_ALGORITHM_MINIMUM_SPANNING_TREE:
		if obj == pb.Objective_OBJECTIVE_MINIMIZE {
			return func(ctx context.Context, g *coregraph.Graph[string, *pb.Vertex], seed string) (*coregraph.Graph[string, *pb.Vertex], error) {
				return g.MinimumSpanningTreeContext(ctx, seed)
			}
		}
		return func(ctx context.Context, g *coregraph.Graph[string, *pb.Vertex], seed string) (*coregraph.Graph[string, *pb.Vertex], error) {
			return g.MaximumSpanningTreeContext(ctx, seed)
		}
	case pb.Algorithm_ALGORITHM_SHORTEST_PATH_TREE:
		if obj == pb.Objective_OBJECTIVE_MINIMIZE {
			return func(ctx context.Context, g *coregraph.Graph[string, *pb.Vertex], seed string) (*coregraph.Graph[string, *pb.Vertex], error) {
				return g.ShortestPathTreeContext(ctx, seed, identityCost)
			}
		}
		return func(ctx context.Context, g *coregraph.Graph[string, *pb.Vertex], seed string) (*coregraph.Graph[string, *pb.Vertex], error) {
			return g.ShortestPathTreeContext(ctx, seed, inverseCost)
		}
	}
	return nil
}

func identityCost(weight float32) float32 { return weight }

func inverseCost(weight float32) float32 {
	if weight == 0 {
		return math.MaxFloat32
	}
	return 1 / weight
}

// algorithmLabel / objectiveLabel / weightingLabel produce the canonical
// metric label string for each axis. UNSPECIFIED resolves to the same
// label the server would resolve it to at execution time:
//   - Algorithm UNSPECIFIED → "none"  (no reduction)
//   - Objective UNSPECIFIED → "maximize"
//   - Weighting UNSPECIFIED → "raw"
//
// Unknown enum values (a future axis added in proto without a metrics
// update) fall through to a synthetic "unknown" bucket so dashboards
// still surface them instead of crashing the pre-warm step.
func algorithmLabel(a pb.Algorithm) string {
	switch a {
	case pb.Algorithm_ALGORITHM_UNSPECIFIED:
		return "none"
	case pb.Algorithm_ALGORITHM_MINIMUM_SPANNING_TREE:
		return "mst"
	case pb.Algorithm_ALGORITHM_SHORTEST_PATH_TREE:
		return "spt"
	}
	return "unknown"
}

func objectiveLabel(o pb.Objective) string {
	switch o {
	case pb.Objective_OBJECTIVE_MINIMIZE:
		return "minimize"
	case pb.Objective_OBJECTIVE_UNSPECIFIED, pb.Objective_OBJECTIVE_MAXIMIZE:
		return "maximize"
	}
	return "unknown"
}

func weightingLabel(w pb.Weighting) string {
	switch w {
	case pb.Weighting_WEIGHTING_UNSPECIFIED, pb.Weighting_WEIGHTING_RAW:
		return "raw"
	case pb.Weighting_WEIGHTING_TFIDF:
		return "tfidf"
	}
	return "unknown"
}
